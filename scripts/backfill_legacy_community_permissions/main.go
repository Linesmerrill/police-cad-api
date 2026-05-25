package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/models"
)

// Backfill: find communities that pre-date the permission-based system —
// the ownerID is set but no role grants the owner administrator power, so
// the owner is effectively locked out of their own community in the new UI.
//
// Reality check: a full scan turns up >100k matches because the vast majority
// of communities in the DB are dormant pre-permission-system docs nobody ever
// touched in the new UI. We don't want to seed roles on those — they'd never
// be used and we'd risk surprising someone who still relies on the old flow.
//
// To separate "stranded by the migrate bug, owner locked out" from "abandoned
// legacy doc nobody is using," we surface two signals per candidate:
//
//   updatedAt           — how recently the doc was written (proxy for activity)
//   ownerHasOtherAdmin — does this owner have a different community where
//                       they already have administrator? (i.e. they migrated
//                       successfully and this is the leftover legacy doc)
//
// Default behavior: dry-run, print compact one-line summary per candidate,
// bucketed report at the end. Filters:
//   --updated-within-days=N         skip docs not written in the last N days
//   --skip-if-owner-admin-elsewhere skip docs where the owner already has
//                                   admin on another community (the smoking
//                                   gun of "this is a migration leftover")
//   --id=<hex>                      target a single community
//   --verbose                       print per-row action list (heavy)
//   --apply                         actually write
//
// Usage:
//   MONGO_URI="..." DB_NAME="..." go run ./scripts/backfill_legacy_community_permissions
//   MONGO_URI="..." DB_NAME="..." go run ./scripts/backfill_legacy_community_permissions \
//     --updated-within-days=30 --skip-if-owner-admin-elsewhere --apply

// lightCommunity / lightCommunityDetails decode only what the script reads.
// Critically, the departments / events / fines / penalCodes nested fields are
// kept as raw arrays/structs so corrupt numeric values (e.g. basePayPerHour
// written as scientific-notation floats that overflow int64) don't fail the
// whole decode.
type lightCommunity struct {
	ID      primitive.ObjectID    `bson:"_id"`
	Details lightCommunityDetails `bson:"community"`
}

type lightCommunityDetails struct {
	Name              string                         `bson:"name"`
	OwnerID           string                         `bson:"ownerID"`
	Roles             []models.Role                  `bson:"roles"`
	TenCodes          []bson.Raw                     `bson:"tenCodes"`
	Fines             lightFines                     `bson:"fines"`
	PenalCodes        lightPenalCodes                `bson:"penalCodes"`
	InviteCodeIds     *[]string                      `bson:"inviteCodeIds"`
	BanList           *[]string                      `bson:"banList"`
	Departments       *[]bson.Raw                    `bson:"departments"`
	Events            *[]bson.Raw                    `bson:"events"`
	Visibility        string                         `bson:"visibility"`
	Members           map[string]models.MemberDetail `bson:"members"`
	MembersCount      int                            `bson:"membersCount"`
	UpdatedAt         primitive.DateTime             `bson:"updatedAt"`
	PendingDeletionAt *primitive.DateTime            `bson:"pendingDeletionAt,omitempty"`
}

type lightFines struct {
	Currency   string     `bson:"currency"`
	Categories []bson.Raw `bson:"categories"`
}

type lightPenalCodes struct {
	Categories []bson.Raw `bson:"categories"`
}

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry-run)")
	idFlag := flag.String("id", "", "remediate only this community _id (hex). default: scan all")
	updatedWithin := flag.Int("updated-within-days", 0, "only consider docs whose updatedAt is within the last N days (0 = no filter)")
	skipOtherAdmin := flag.Bool("skip-if-owner-admin-elsewhere", false, "skip docs where the owner already has admin on another community")
	verbose := flag.Bool("verbose", false, "print the per-row action list (default: compact one-line summary)")
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	communities := client.Database(dbName).Collection("communities")

	filter := bson.M{
		"community.ownerID":           bson.M{"$exists": true, "$ne": ""},
		"community.pendingDeletionAt": bson.M{"$exists": false},
	}
	if *idFlag != "" {
		oid, err := primitive.ObjectIDFromHex(*idFlag)
		if err != nil {
			log.Fatalf("invalid --id: %v", err)
		}
		filter["_id"] = oid
	}

	// Push the updated-within filter to Mongo when set — this is what makes
	// the scan tractable on a large DB; "updated in last 30 days" is the
	// usual triage cut.
	var sinceCutoff time.Time
	if *updatedWithin > 0 {
		sinceCutoff = time.Now().Add(-time.Duration(*updatedWithin) * 24 * time.Hour)
		filter["community.updatedAt"] = bson.M{"$gte": primitive.NewDateTimeFromTime(sinceCutoff)}
	}

	cur, err := communities.Find(ctx, filter)
	if err != nil {
		log.Fatalf("find: %v", err)
	}
	defer cur.Close(ctx)

	// Cache owner -> "owner has admin on another community" to avoid repeating
	// the per-row lookup. False = checked, no admin elsewhere; true = checked,
	// admin elsewhere; absent = not checked yet.
	ownerOtherAdminCache := map[string]bool{}

	var (
		scanned       int
		alreadyFine   int
		needsFix      int
		updatedCount  int
		skippedDecode int
		skippedOther  int // skipped due to --skip-if-owner-admin-elsewhere
	)

	type candidate struct {
		id              primitive.ObjectID
		ownerID         string
		name            string
		updatedAt       time.Time
		daysSinceUpdate int
		ownerHasOther   bool
		actions         []string
		setUpdate       bson.M
	}
	var candidates []candidate

	buckets := map[string]int{
		"updated <= 30d":        0,
		"updated 31-90d":        0,
		"updated 91-365d":       0,
		"updated > 365d":        0,
		"never updated":         0,
		"owner admin elsewhere": 0,
	}

	for cur.Next(ctx) {
		scanned++

		var doc lightCommunity
		if err := cur.Decode(&doc); err != nil {
			log.Printf("decode error: %v (raw _id=%v)", err, cur.Current.Lookup("_id"))
			skippedDecode++
			continue
		}

		owner := strings.TrimSpace(doc.Details.OwnerID)
		if owner == "" {
			skippedDecode++
			continue
		}
		if ownerHasAdmin(doc.Details.Roles, owner) {
			alreadyFine++
			continue
		}

		// Owner-elsewhere check (cached). We do this lookup before deciding
		// whether to skip so the bucket counts are accurate even when not
		// filtering on it.
		hasOther, ok := ownerOtherAdminCache[owner]
		if !ok {
			hasOther, err = ownerHasAdminOnAnotherCommunity(ctx, communities, owner, doc.ID)
			if err != nil {
				log.Printf("owner-other-admin lookup failed for owner=%s: %v", owner, err)
			}
			ownerOtherAdminCache[owner] = hasOther
		}

		updatedAt := doc.Details.UpdatedAt.Time()
		daysSince := -1
		if !updatedAt.IsZero() {
			daysSince = int(time.Since(updatedAt).Hours() / 24)
		}

		switch {
		case updatedAt.IsZero():
			buckets["never updated"]++
		case daysSince <= 30:
			buckets["updated <= 30d"]++
		case daysSince <= 90:
			buckets["updated 31-90d"]++
		case daysSince <= 365:
			buckets["updated 91-365d"]++
		default:
			buckets["updated > 365d"]++
		}
		if hasOther {
			buckets["owner admin elsewhere"]++
		}

		if *skipOtherAdmin && hasOther {
			skippedOther++
			continue
		}

		needsFix++

		set := bson.M{}
		actions := []string{}

		newRoles := append([]models.Role{}, doc.Details.Roles...)
		newRoles = append(newRoles, headAdminRole(owner))
		set["community.roles"] = newRoles
		actions = append(actions, "add Head Admin role")

		if len(doc.Details.TenCodes) == 0 {
			set["community.tenCodes"] = models.DefaultTenCodes()
			actions = append(actions, "seed tenCodes")
		}
		if doc.Details.Fines.Currency == "" && len(doc.Details.Fines.Categories) == 0 {
			set["community.fines"] = models.DefaultCommunityFines()
			actions = append(actions, "seed fines")
		}
		if len(doc.Details.PenalCodes.Categories) == 0 {
			set["community.penalCodes"] = models.DefaultCommunityPenalCodes()
			actions = append(actions, "seed penalCodes")
		}
		if doc.Details.InviteCodeIds == nil {
			set["community.inviteCodeIds"] = []string{}
			actions = append(actions, "init inviteCodeIds")
		}
		if doc.Details.BanList == nil {
			set["community.banList"] = []string{}
			actions = append(actions, "init banList")
		}
		if doc.Details.Departments == nil {
			set["community.departments"] = []models.Department{}
			actions = append(actions, "init departments")
		}
		if doc.Details.Events == nil {
			set["community.events"] = []models.Event{}
			actions = append(actions, "init events")
		}
		if doc.Details.Visibility == "" {
			set["community.visibility"] = "public"
			actions = append(actions, "default visibility=public")
		}

		if doc.Details.Members == nil {
			set["community.members"] = map[string]models.MemberDetail{owner: {}}
			actions = append(actions, "init members{owner}")
		} else if _, hasOwnerMember := doc.Details.Members[owner]; !hasOwnerMember {
			set["community.members."+owner] = models.MemberDetail{}
			actions = append(actions, "members[owner]={}")
		}

		expected := len(doc.Details.Members)
		if _, hasOwnerMember := doc.Details.Members[owner]; !hasOwnerMember {
			expected++
		}
		if expected == 0 {
			expected = 1
		}
		if doc.Details.MembersCount != expected {
			set["community.membersCount"] = expected
			actions = append(actions, fmt.Sprintf("membersCount=%d", expected))
		}

		set["community.updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

		candidates = append(candidates, candidate{
			id:              doc.ID,
			ownerID:         owner,
			name:            doc.Details.Name,
			updatedAt:       updatedAt,
			daysSinceUpdate: daysSince,
			ownerHasOther:   hasOther,
			actions:         actions,
			setUpdate:       set,
		})

		if !*apply {
			continue
		}

		if _, err := communities.UpdateOne(ctx, bson.M{"_id": doc.ID}, bson.M{"$set": set}); err != nil {
			log.Printf("update failed for %s: %v", doc.ID.Hex(), err)
			skippedDecode++
			continue
		}
		updatedCount++
	}
	if err := cur.Err(); err != nil {
		log.Fatalf("cursor: %v", err)
	}

	// Sort candidates by recency descending so the most-active are at the top.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].updatedAt.After(candidates[j].updatedAt)
	})

	fmt.Println()
	fmt.Println("=== legacy community permission backfill ===")
	fmt.Printf("scanned                          : %d\n", scanned)
	fmt.Printf("already fine                     : %d\n", alreadyFine)
	fmt.Printf("needs fix                        : %d\n", needsFix)
	fmt.Printf("skipped (owner-admin-elsewhere)  : %d\n", skippedOther)
	fmt.Printf("skipped (decode/error)           : %d\n", skippedDecode)
	if *apply {
		fmt.Printf("updated                          : %d\n", updatedCount)
	}
	if *updatedWithin > 0 {
		fmt.Printf("filter                           : updatedAt >= %s (last %dd)\n", sinceCutoff.UTC().Format("2006-01-02"), *updatedWithin)
	}

	fmt.Println()
	fmt.Println("buckets (across all candidates lacking admin role for owner):")
	for _, k := range []string{"updated <= 30d", "updated 31-90d", "updated 91-365d", "updated > 365d", "never updated", "owner admin elsewhere"} {
		fmt.Printf("  %-25s %d\n", k, buckets[k])
	}

	fmt.Println()
	fmt.Println("candidates (most recently active first):")
	fmt.Println("  _id                       ownerID                   daysSinceUpdate  ownerAdminElsewhere  name")
	for _, c := range candidates {
		dStr := "n/a"
		if c.daysSinceUpdate >= 0 {
			dStr = fmt.Sprintf("%d", c.daysSinceUpdate)
		}
		fmt.Printf("  %s  %-24s  %-15s  %-19v  %s\n", c.id.Hex(), c.ownerID, dStr, c.ownerHasOther, c.name)
		if *verbose {
			for _, a := range c.actions {
				fmt.Printf("      - %s\n", a)
			}
		}
	}

	if !*apply {
		fmt.Println()
		fmt.Println("(dry-run — no writes performed. re-run with --apply to commit.)")
	}
}

// ownerHasAdmin reports whether any role contains ownerID AND has an enabled
// permission named "administrator".
func ownerHasAdmin(roles []models.Role, ownerID string) bool {
	if ownerID == "" {
		return false
	}
	for _, r := range roles {
		hasOwner := false
		for _, m := range r.Members {
			if m == ownerID {
				hasOwner = true
				break
			}
		}
		if !hasOwner {
			continue
		}
		for _, p := range r.Permissions {
			if p.Enabled && strings.EqualFold(p.Name, "administrator") {
				return true
			}
		}
	}
	return false
}

// ownerHasAdminOnAnotherCommunity returns true if the owner has administrator
// in any community whose _id is NOT excludeID. Indicates the user migrated
// successfully and this candidate is likely the abandoned legacy leftover.
func ownerHasAdminOnAnotherCommunity(ctx context.Context, communities *mongo.Collection, ownerID string, excludeID primitive.ObjectID) (bool, error) {
	if ownerID == "" {
		return false, nil
	}
	// Pull just _id and roles to keep payload small and dodge corrupt fields.
	opts := options.Find().SetProjection(bson.M{"_id": 1, "community.roles": 1})
	cur, err := communities.Find(ctx, bson.M{
		"community.ownerID":           ownerID,
		"community.pendingDeletionAt": bson.M{"$exists": false},
		"_id":                         bson.M{"$ne": excludeID},
	}, opts)
	if err != nil {
		return false, err
	}
	defer cur.Close(ctx)

	for cur.Next(ctx) {
		var doc struct {
			ID      primitive.ObjectID `bson:"_id"`
			Details struct {
				Roles []models.Role `bson:"roles"`
			} `bson:"community"`
		}
		if err := cur.Decode(&doc); err != nil {
			continue
		}
		if ownerHasAdmin(doc.Details.Roles, ownerID) {
			return true, nil
		}
	}
	return false, cur.Err()
}

// headAdminRole mirrors the role stamped by CreateCommunityHandler.
func headAdminRole(ownerID string) models.Role {
	return models.Role{
		ID:      primitive.NewObjectID(),
		Name:    "Head Admin",
		Members: []string{ownerID},
		Permissions: []models.Permission{
			{ID: primitive.NewObjectID(), Name: "administrator", Description: "Head Admin", Enabled: true},
			{ID: primitive.NewObjectID(), Name: "manage community settings", Description: "Allows managing community settings", Enabled: false},
			{ID: primitive.NewObjectID(), Name: "manage community events", Description: "Allows managing community events", Enabled: false},
			{ID: primitive.NewObjectID(), Name: "manage departments", Description: "Allows managing departments", Enabled: false},
			{ID: primitive.NewObjectID(), Name: "manage roles", Description: "Allows managing roles", Enabled: false},
			{ID: primitive.NewObjectID(), Name: "manage members", Description: "Allows managing members", Enabled: false},
			{ID: primitive.NewObjectID(), Name: "manage bans", Description: "Allows managing bans", Enabled: false},
			{ID: primitive.NewObjectID(), Name: "administrator", Description: "Members with this permission will have every permission and will also bypass all community specific permissions or restrictions (for example, these members would get access to all settings and pages). This is a dangerous permission to grant.", Enabled: false},
		},
	}
}
