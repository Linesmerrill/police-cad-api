package main

// Backfill: every community's owner is an approved member holding admin.
//
// Product rule: a community must never be orphaned. The owner cannot leave and
// must always be an approved member with Head Admin, even if they no longer
// play. Communities created before 2025 never gave their owner a
// user.communities entry or an admin role, and the API only heals that lazily
// when someone loads the community (applyCommunityBackfill and
// healOwnerMembership in api/handlers). Communities nobody opens stay broken,
// e.g. one whose de facto admin left with a join request pending that nobody
// can approve.
//
// For every community not pending deletion whose ownerID is the ObjectId of an
// existing user, this:
//   a. makes the owner an approved member: adds a missing user.communities
//      entry, or promotes a pending/declined/banned one in place. An owner
//      banned from their own community is also pulled off community.banList
//      and reported in its own bucket;
//   b. gives the owner admin when no role grants it (models.OwnerHasAdmin):
//      adds them to the existing Head Admin role (models.IsHeadAdminRole) or
//      pushes models.BuildHeadAdminRole(owner);
//   c. sets community.membersCount to the number of approved members.
//
// Communities whose ownerID is not an ObjectId, or whose owner account no
// longer exists, are reported only and never changed: those need a human
// decision about who should own them.
//
// It walks communities in _id order in batches, sleeping between batches so it
// does not load production, and prints the last _id it finished so an
// interrupted run can continue with --after.
//
// Usage:
//   MONGO_URI=... DB_NAME=... go run ./scripts/backfill_owner_admin                 # dry run
//   MONGO_URI=... DB_NAME=... go run ./scripts/backfill_owner_admin --id=<hex>      # one community
//   MONGO_URI=... DB_NAME=... go run ./scripts/backfill_owner_admin --apply         # write
//   flags: --after=<hex> resume after this _id, --limit=N stop after N communities,
//          --batch=N communities per batch (default 500), --sleep=250ms between batches

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/models"
)

// lightCommunity decodes only what the script reads. Everything else in a
// community (departments, fines, economy numerics) is left undecoded so a
// corrupt field elsewhere cannot fail the row.
type lightCommunity struct {
	ID      primitive.ObjectID `bson:"_id"`
	Details struct {
		Name         string        `bson:"name"`
		OwnerID      string        `bson:"ownerID"`
		Roles        []models.Role `bson:"roles"`
		BanList      []string      `bson:"banList"`
		MembersCount bson.RawValue `bson:"membersCount"`
	} `bson:"community"`
}

type lightUser struct {
	ID      primitive.ObjectID `bson:"_id"`
	Details struct {
		Communities []struct {
			CommunityID string `bson:"communityId"`
			Status      string `bson:"status"`
		} `bson:"communities"`
	} `bson:"user"`
}

const (
	bucketMissingMembership = "ownerMissingMembership"
	bucketOwnerBanned       = "ownerBanned"
	bucketMissingAdmin      = "ownerMissingAdmin"
	bucketCountCorrected    = "countCorrected"
	bucketAccountMissing    = "ownerAccountMissing"
	bucketInvalidOwnerID    = "invalidOwnerID"
	bucketDecodeError       = "decodeError"
	bucketWriteError        = "writeError"
)

var bucketOrder = []string{
	bucketMissingMembership, bucketOwnerBanned, bucketMissingAdmin, bucketCountCorrected,
	bucketAccountMissing, bucketInvalidOwnerID, bucketDecodeError, bucketWriteError,
}

type report struct {
	maxSamples int
	totals     map[string]int
	samples    map[string][]string
}

func (r *report) add(bucket, sample string) {
	r.totals[bucket]++
	if len(r.samples[bucket]) < r.maxSamples {
		r.samples[bucket] = append(r.samples[bucket], sample)
	}
}

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run)")
	idFlag := flag.String("id", "", "process only this community _id (hex)")
	afterFlag := flag.String("after", "", "resume: only communities with _id greater than this (hex)")
	limit := flag.Int("limit", 0, "stop after this many communities (0 = no limit)")
	batchSize := flag.Int("batch", 500, "communities per batch")
	sleep := flag.Duration("sleep", 250*time.Millisecond, "pause between batches")
	maxSamples := flag.Int("samples", 10, "samples printed per bucket")
	flag.Parse()

	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		uri = os.Getenv("DB_URI")
	}
	if uri == "" {
		log.Fatal("MONGO_URI (or DB_URI) env var is required")
	}
	dbName := os.Getenv("DB_NAME")
	if dbName == "" {
		log.Fatal("DB_NAME env var is required")
	}
	if *batchSize < 1 || *batchSize > 5000 {
		log.Fatal("--batch must be between 1 and 5000")
	}

	connectCtx, cancelConnect := context.WithTimeout(context.Background(), 30*time.Second)
	client, err := mongo.Connect(connectCtx, options.Client().ApplyURI(uri))
	cancelConnect()
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	db := client.Database(dbName)
	communities := db.Collection("communities")
	users := db.Collection("users")

	base := bson.M{"community.pendingDeletionAt": nil}
	var after primitive.ObjectID
	if *idFlag != "" {
		oid, err := primitive.ObjectIDFromHex(*idFlag)
		if err != nil {
			log.Fatalf("invalid --id: %v", err)
		}
		base["_id"] = oid
	} else if *afterFlag != "" {
		oid, err := primitive.ObjectIDFromHex(*afterFlag)
		if err != nil {
			log.Fatalf("invalid --after: %v", err)
		}
		after = oid
	}

	rep := &report{maxSamples: *maxSamples, totals: map[string]int{}, samples: map[string][]string{}}
	var scanned, healthy, changed int
	start := time.Now()

	for {
		filter := bson.M{}
		for k, v := range base {
			filter[k] = v
		}
		if _, single := filter["_id"]; !single && !after.IsZero() {
			filter["_id"] = bson.M{"$gt": after}
		}
		n := int64(*batchSize)
		if *limit > 0 {
			remaining := int64(*limit - scanned)
			if remaining <= 0 {
				break
			}
			if remaining < n {
				n = remaining
			}
		}

		// resumeHint is the last _id of the previous batch: everything up to
		// it is done, so --after=<it> redoes only this batch.
		resumeHint := after.Hex()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		batch, read, lastID, err := loadBatch(ctx, communities, filter, n, rep)
		if err != nil {
			cancel()
			log.Fatalf("load batch (resume with --after=%s): %v", resumeHint, err)
		}
		if read == 0 {
			cancel()
			break
		}
		scanned += read
		after = lastID

		owners, err := loadOwners(ctx, users, batch)
		if err != nil {
			cancel()
			log.Fatalf("load owners (resume with --after=%s): %v", resumeHint, err)
		}
		approved, err := countApproved(ctx, users, batch)
		if err != nil {
			cancel()
			log.Fatalf("count members (resume with --after=%s): %v", resumeHint, err)
		}

		for _, c := range batch {
			did, ok := processCommunity(ctx, communities, users, c, owners, approved[c.ID.Hex()], *apply, rep)
			if !ok {
				continue
			}
			if did {
				changed++
			} else {
				healthy++
			}
		}
		cancel()

		fmt.Fprintf(os.Stderr, "scanned %d, last _id %s (%s)\n", scanned, after.Hex(), time.Since(start).Round(time.Second))
		if _, single := base["_id"]; single {
			break
		}
		time.Sleep(*sleep)
	}

	mode := "DRY RUN (no writes)"
	if *apply {
		mode = "APPLIED"
	}
	fmt.Println()
	fmt.Printf("=== owner admin backfill: %s ===\n", mode)
	fmt.Printf("scanned            : %d\n", scanned)
	fmt.Printf("already healthy    : %d\n", healthy)
	if *apply {
		fmt.Printf("changed            : %d\n", changed)
	} else {
		fmt.Printf("would change       : %d\n", changed)
	}
	fmt.Printf("last _id           : %s\n", after.Hex())
	fmt.Println()
	for _, b := range bucketOrder {
		fmt.Printf("%-24s %d\n", b, rep.totals[b])
	}
	for _, b := range bucketOrder {
		if len(rep.samples[b]) == 0 {
			continue
		}
		fmt.Printf("\n%s samples:\n", b)
		for _, s := range rep.samples[b] {
			fmt.Printf("  %s\n", s)
		}
	}
	if rep.totals[bucketAccountMissing]+rep.totals[bucketInvalidOwnerID] > 0 {
		fmt.Println("\nownerAccountMissing / invalidOwnerID communities were NOT changed; they need a human decision on ownership.")
	}
}

// loadBatch reads up to n communities in _id order. It returns the decoded
// rows, how many documents were read, and the last _id read (counting rows
// that failed to decode, so the walk never stalls on a corrupt document).
func loadBatch(ctx context.Context, coll *mongo.Collection, filter bson.M, n int64, rep *report) ([]lightCommunity, int, primitive.ObjectID, error) {
	opts := options.Find().
		SetSort(bson.D{{Key: "_id", Value: 1}}).
		SetLimit(n).
		SetProjection(bson.M{
			"community.name":         1,
			"community.ownerID":      1,
			"community.roles":        1,
			"community.banList":      1,
			"community.membersCount": 1,
		})
	cur, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, primitive.NilObjectID, err
	}
	defer cur.Close(ctx)

	var out []lightCommunity
	var last primitive.ObjectID
	read := 0
	for cur.Next(ctx) {
		read++
		if id, ok := cur.Current.Lookup("_id").ObjectIDOK(); ok {
			last = id
		}
		var c lightCommunity
		if err := cur.Decode(&c); err != nil {
			rep.add(bucketDecodeError, fmt.Sprintf("%s  %v", last.Hex(), err))
			continue
		}
		out = append(out, c)
	}
	return out, read, last, cur.Err()
}

// loadOwners fetches every valid owner in the batch in one query.
func loadOwners(ctx context.Context, users *mongo.Collection, batch []lightCommunity) (map[string]*lightUser, error) {
	ids := make([]primitive.ObjectID, 0, len(batch))
	seen := map[primitive.ObjectID]bool{}
	for _, c := range batch {
		oid, err := primitive.ObjectIDFromHex(strings.TrimSpace(c.Details.OwnerID))
		if err != nil || seen[oid] {
			continue
		}
		seen[oid] = true
		ids = append(ids, oid)
	}
	out := map[string]*lightUser{}
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := users.Find(ctx, bson.M{"_id": bson.M{"$in": ids}},
		options.Find().SetProjection(bson.M{"user.communities.communityId": 1, "user.communities.status": 1}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var u lightUser
		if err := cur.Decode(&u); err != nil {
			// Treat as present with no readable entries; the write paths below
			// filter on the stored state, so nothing is clobbered.
			if id, ok := cur.Current.Lookup("_id").ObjectIDOK(); ok {
				out[id.Hex()] = &lightUser{ID: id}
			}
			continue
		}
		out[u.ID.Hex()] = &u
	}
	return out, cur.Err()
}

// countApproved returns, per community in the batch, how many distinct users
// hold an approved entry for it. One aggregation per batch, served by the
// user_communities_idx {communityId, status} index.
func countApproved(ctx context.Context, users *mongo.Collection, batch []lightCommunity) (map[string]int, error) {
	ids := make([]string, 0, len(batch))
	for _, c := range batch {
		ids = append(ids, c.ID.Hex())
	}
	out := map[string]int{}
	if len(ids) == 0 {
		return out, nil
	}
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"user.communities": bson.M{"$elemMatch": bson.M{
			"communityId": bson.M{"$in": ids},
			"status":      "approved",
		}}}}},
		{{Key: "$project", Value: bson.M{"c": "$user.communities"}}},
		{{Key: "$unwind", Value: "$c"}},
		{{Key: "$match", Value: bson.M{"c.communityId": bson.M{"$in": ids}, "c.status": "approved"}}},
		// A user with two approved rows for one community is one member.
		{{Key: "$group", Value: bson.M{"_id": bson.M{"c": "$c.communityId", "u": "$_id"}}}},
		{{Key: "$group", Value: bson.M{"_id": "$_id.c", "n": bson.M{"$sum": 1}}}},
	}
	cur, err := users.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var row struct {
			ID string `bson:"_id"`
			N  int    `bson:"n"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, err
		}
		out[row.ID] = row.N
	}
	return out, cur.Err()
}

// storedCount reads membersCount whatever numeric type it was written as.
// Missing or unreadable values report -1 so they always get corrected.
func storedCount(v bson.RawValue) int {
	if v.Type == 0 {
		return -1
	}
	if n, ok := v.Int32OK(); ok {
		return int(n)
	}
	if n, ok := v.Int64OK(); ok {
		return int(n)
	}
	if f, ok := v.DoubleOK(); ok && f == float64(int(f)) {
		return int(f)
	}
	return -1
}

// processCommunity reports and (with apply) repairs one community. ok is false
// when the community was only reported (bad owner) or a write failed; did is
// whether anything needed changing.
func processCommunity(ctx context.Context, communities, users *mongo.Collection, c lightCommunity, owners map[string]*lightUser, approved int, apply bool, rep *report) (did, ok bool) {
	ownerHex := strings.TrimSpace(c.Details.OwnerID)
	label := fmt.Sprintf("%s  owner=%s  %q", c.ID.Hex(), ownerHex, c.Details.Name)

	ownerOID, err := primitive.ObjectIDFromHex(ownerHex)
	if err != nil {
		rep.add(bucketInvalidOwnerID, label)
		return false, false
	}
	owner := owners[ownerOID.Hex()]
	if owner == nil {
		rep.add(bucketAccountMissing, label)
		return false, false
	}
	communityID := c.ID.Hex()

	// a. membership
	status := ""
	for _, uc := range owner.Details.Communities {
		if uc.CommunityID != communityID {
			continue
		}
		if uc.Status == "approved" {
			status = "approved"
			break
		}
		if status == "" {
			status = uc.Status
			if status == "" {
				status = "unknown"
			}
		}
	}
	inBanList := false
	for _, b := range c.Details.BanList {
		if b == ownerHex {
			inBanList = true
			break
		}
	}
	needsMembership := status != "approved"
	if status == "banned" || inBanList {
		rep.add(bucketOwnerBanned, fmt.Sprintf("%s  entry=%s  banList=%v", label, statusOrNone(status), inBanList))
	} else if needsMembership {
		rep.add(bucketMissingMembership, fmt.Sprintf("%s  entry=%s", label, statusOrNone(status)))
	}

	// b. admin
	needsAdmin := !models.OwnerHasAdmin(c.Details.Roles, ownerHex)
	headAdminIdx := -1
	if needsAdmin {
		for i := range c.Details.Roles {
			if models.IsHeadAdminRole(c.Details.Roles[i]) {
				headAdminIdx = i
				break
			}
		}
		how := "stamp Head Admin role"
		if headAdminIdx >= 0 {
			how = "add to existing Head Admin role"
		}
		rep.add(bucketMissingAdmin, fmt.Sprintf("%s  %s", label, how))
	}

	// c. count
	expected := approved
	if needsMembership {
		expected++
	}
	stored := storedCount(c.Details.MembersCount)
	needsCount := stored != expected
	if needsCount {
		rep.add(bucketCountCorrected, fmt.Sprintf("%s  %d -> %d", label, stored, expected))
	}

	if !needsMembership && !inBanList && !needsAdmin && !needsCount {
		return false, true
	}
	if !apply {
		return true, true
	}

	if needsMembership {
		if err := approveOwner(ctx, users, ownerOID, communityID, status != ""); err != nil {
			rep.add(bucketWriteError, fmt.Sprintf("%s  membership: %v", label, err))
			return true, false
		}
	}

	filter := bson.M{"_id": c.ID}
	update := bson.M{}
	set := bson.M{}
	var updateOpts *options.UpdateOptions
	if needsCount {
		set["community.membersCount"] = expected
	}
	if inBanList {
		update["$pull"] = bson.M{"community.banList": ownerHex}
	}
	if needsAdmin {
		switch {
		case headAdminIdx >= 0:
			update["$addToSet"] = bson.M{"community.roles.$[ha].members": ownerHex}
			updateOpts = options.Update().SetArrayFilters(options.ArrayFilters{
				Filters: []interface{}{bson.M{"ha._id": c.Details.Roles[headAdminIdx].ID}},
			})
		case len(c.Details.Roles) == 0:
			// $push onto a null roles field errors, so set the array, but only
			// while it is still empty so a role added meanwhile is not lost.
			filter["$or"] = bson.A{
				bson.M{"community.roles": bson.M{"$exists": false}},
				bson.M{"community.roles": nil},
				bson.M{"community.roles": bson.M{"$size": 0}},
			}
			set["community.roles"] = []models.Role{models.BuildHeadAdminRole(ownerHex)}
		default:
			update["$push"] = bson.M{"community.roles": models.BuildHeadAdminRole(ownerHex)}
		}
	}
	if len(set) > 0 || len(update) > 0 {
		set["community.updatedAt"] = primitive.NewDateTimeFromTime(time.Now())
		update["$set"] = set
		var opts []*options.UpdateOptions
		if updateOpts != nil {
			opts = append(opts, updateOpts)
		}
		res, err := communities.UpdateOne(ctx, filter, update, opts...)
		if err != nil {
			rep.add(bucketWriteError, fmt.Sprintf("%s  community: %v", label, err))
			return true, false
		}
		if res.MatchedCount == 0 {
			rep.add(bucketWriteError, fmt.Sprintf("%s  community changed during the run; re-run with --id", label))
			return true, false
		}
	}
	return true, true
}

func statusOrNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// approveOwner mirrors ensureCommunityMembership in api/handlers: promote an
// existing entry in place, else add one. Filters match only while the entry is
// still missing or unapproved, so a concurrent heal is not duplicated.
func approveOwner(ctx context.Context, users *mongo.Collection, ownerID primitive.ObjectID, communityID string, hasEntry bool) error {
	if hasEntry {
		_, err := users.UpdateOne(ctx,
			bson.M{"_id": ownerID, "user.communities": bson.M{"$elemMatch": bson.M{
				"communityId": communityID,
				"status":      bson.M{"$ne": "approved"},
			}}},
			bson.M{"$set": bson.M{"user.communities.$.status": "approved"}})
		return err
	}
	if _, err := users.UpdateOne(ctx,
		bson.M{"_id": ownerID, "user.communities": nil},
		bson.M{"$set": bson.M{"user.communities": bson.A{}}}); err != nil {
		return err
	}
	_, err := users.UpdateOne(ctx,
		bson.M{"_id": ownerID, "user.communities.communityId": bson.M{"$ne": communityID}},
		bson.M{"$push": bson.M{"user.communities": models.UserCommunity{
			ID:          primitive.NewObjectID().Hex(),
			CommunityID: communityID,
			Status:      "approved",
		}}})
	return err
}
