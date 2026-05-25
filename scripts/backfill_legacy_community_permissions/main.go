package main

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

// Backfill: find communities that pre-date the permission-based system —
// the ownerID is set but no role grants the owner administrator power, so
// the owner is effectively locked out of their own community in the new UI.
//
// We seed the same Head Admin role + defaults that CreateCommunityHandler
// writes for fresh communities, so a remediated community is indistinguishable
// from a freshly created one.
//
// Dry-run by default. Pass --apply to write. Pass --id=<hex> to target a
// single community.
//
// Usage:
//   MONGO_URI="..." DB_NAME="..." go run ./scripts/backfill_legacy_community_permissions
//   MONGO_URI="..." DB_NAME="..." go run ./scripts/backfill_legacy_community_permissions --apply
//   MONGO_URI="..." DB_NAME="..." go run ./scripts/backfill_legacy_community_permissions --id=64f50d4819cb4c0002df71aa --apply

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry-run)")
	idFlag := flag.String("id", "", "remediate only this community _id (hex). default: scan all")
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	communities := client.Database(dbName).Collection("communities")

	// Scan all non-pending-deletion communities with an ownerID. Mongo can't
	// cheaply express "no role grants ownerID administrator" — that needs to
	// walk the embedded roles array per doc — so we filter precisely in Go.
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

	cur, err := communities.Find(ctx, filter)
	if err != nil {
		log.Fatalf("find: %v", err)
	}
	defer cur.Close(ctx)

	var (
		scanned     int
		needsFix    int
		updated     int
		alreadyFine int
		skipped     int
	)
	type fixRecord struct {
		id      string
		name    string
		ownerID string
		actions []string
	}
	var fixes []fixRecord

	for cur.Next(ctx) {
		scanned++

		var doc models.Community
		if err := cur.Decode(&doc); err != nil {
			log.Printf("decode error: %v (raw _id=%v)", err, cur.Current.Lookup("_id"))
			skipped++
			continue
		}

		owner := strings.TrimSpace(doc.Details.OwnerID)
		if owner == "" {
			skipped++
			continue
		}
		if ownerHasAdmin(doc.Details.Roles, owner) {
			alreadyFine++
			continue
		}

		needsFix++

		set := bson.M{}
		actions := []string{}

		// Always add a fresh Head Admin role for the owner. This is the core
		// fix — the user is presently locked out without it.
		newRoles := append([]models.Role{}, doc.Details.Roles...)
		newRoles = append(newRoles, headAdminRole(owner))
		set["community.roles"] = newRoles
		actions = append(actions, "add Head Admin role")

		// Conservative defaults: only seed where the field is unmistakably
		// absent so we never clobber a customer's customizations.
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

		// Ensure the owner has a member entry. If the map is nil-or-missing
		// or absent of the owner, add a zero-value MemberDetail like the
		// rest of the join-flow expects.
		if doc.Details.Members == nil {
			set["community.members"] = map[string]models.MemberDetail{
				owner: {},
			}
			actions = append(actions, "init members{owner}")
		} else if _, ok := doc.Details.Members[owner]; !ok {
			// Mongo allows dotted member-map writes; safer than overwriting
			// the whole map and clobbering other members.
			set["community.members."+owner] = models.MemberDetail{}
			actions = append(actions, "members[owner]={}")
		}

		// Recompute membersCount from the resulting member map.
		expected := len(doc.Details.Members)
		if _, ok := doc.Details.Members[owner]; !ok {
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

		rec := fixRecord{
			id:      doc.ID.Hex(),
			name:    doc.Details.Name,
			ownerID: owner,
			actions: actions,
		}
		fixes = append(fixes, rec)

		if !*apply {
			continue
		}

		_, err := communities.UpdateOne(ctx, bson.M{"_id": doc.ID}, bson.M{"$set": set})
		if err != nil {
			log.Printf("update failed for %s: %v", doc.ID.Hex(), err)
			skipped++
			continue
		}
		updated++
	}
	if err := cur.Err(); err != nil {
		log.Fatalf("cursor: %v", err)
	}

	fmt.Println()
	fmt.Println("=== legacy community permission backfill ===")
	fmt.Printf("scanned        : %d\n", scanned)
	fmt.Printf("already fine   : %d\n", alreadyFine)
	fmt.Printf("needs fix      : %d\n", needsFix)
	if *apply {
		fmt.Printf("updated        : %d\n", updated)
	}
	fmt.Printf("skipped/errors : %d\n", skipped)
	fmt.Println()

	for _, f := range fixes {
		fmt.Printf("  %s  owner=%s  name=%q\n", f.id, f.ownerID, f.name)
		for _, a := range f.actions {
			fmt.Printf("      - %s\n", a)
		}
	}

	if !*apply {
		fmt.Println()
		fmt.Println("(dry-run — no writes performed. re-run with --apply to commit.)")
	}
}

// ownerHasAdmin reports whether any role contains ownerID AND has an enabled
// permission named "administrator". This mirrors what CreateCommunityHandler
// stamps for new communities.
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

// headAdminRole mirrors the role stamped by CreateCommunityHandler. Kept
// here (instead of exported from handlers) because the script must not
// drag in the entire handlers package; the shape is small and stable.
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
