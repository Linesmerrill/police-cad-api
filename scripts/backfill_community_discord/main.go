// One-off backfill: give communities a first-class Discord invite.
//
// Why: communities here are Discord-run RP servers and the CAD is a supplemental
// tool. A member who requests to join needs sending to that community's own
// server for whatever training the owner requires — but until now there was no
// field to put the invite in, so owners smuggled it into the free-text
// description or it existed only inside an RP promotion nobody outside the owner
// could see.
//
// A production survey (Aug 2026) over the 2,285 public communities with at least
// two members found 108 with an invite in the description and 225 carrying one
// in an RP promotion: 309 communities, 13.5% overall and 52% of boosted ones,
// that can be given a working invite without the owner lifting a finger.
//
// The API now resolves all three sources on read (models.CommunityDetails
// ResolveDiscordInvite) and normalizes on write, so this script is not required
// for correctness — it promotes the derived value into the real field so the
// resolution stops being recomputed on every read and owners can see and edit
// what we found.
//
// This backfill is deliberately ADDITIVE and CONSERVATIVE:
//   - it only writes community.discordInviteUrl when that field is empty
//   - it never edits community.description, so an owner who wrote their invite
//     into their description keeps it exactly as they wrote it
//   - it never touches rpPromotion, which is moderation-bearing state
//   - it skips promotions removed by staff moderation, since whatever got a post
//     pulled from the shared Discord is not something to hand new members
//
// Extraction and validation are decided by the models package, deliberately —
// there must be exactly one implementation, and it is the one covered by
// models/community_onboarding_test.go.
//
// READ-ONLY by default. Pass --apply to actually write.
//
// Usage:
//
//	DB_URI=mongodb+srv://... DB_NAME=lpc \
//	go run ./scripts/backfill_community_discord                 # dry-run preview
//	go run ./scripts/backfill_community_discord --apply         # write
//	go run ./scripts/backfill_community_discord --apply --batch=2000
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/models"
)

// communityDoc is the minimum needed to resolve an invite. Decoding the whole
// community would pull megabytes of penal codes and departments per document.
type communityDoc struct {
	ID      primitive.ObjectID `bson:"_id"`
	Details struct {
		Name             string              `bson:"name"`
		Description      string              `bson:"description"`
		DiscordInviteURL string              `bson:"discordInviteUrl"`
		Visibility       string              `bson:"visibility"`
		RpPromotion      *models.RpPromotion `bson:"rpPromotion"`
	} `bson:"community"`
}

// resolve reuses the model's chain so the script and the API can never disagree
// about what a community's invite is.
func resolve(d communityDoc) (invite string, source string) {
	details := models.CommunityDetails{
		Description:      d.Details.Description,
		DiscordInviteURL: d.Details.DiscordInviteURL,
		RpPromotion:      d.Details.RpPromotion,
	}
	return details.ResolveDiscordInvite()
}

func main() {
	apply := flag.Bool("apply", false, "write changes (default is a read-only preview)")
	batchSize := flag.Int("batch", 1000, "documents per bulk write")
	publicOnly := flag.Bool("public-only", true, "only backfill communities with visibility=public")
	samples := flag.Int("samples", 8, "how many resolved examples to print")
	flag.Parse()

	uri := os.Getenv("DB_URI")
	dbName := os.Getenv("DB_NAME")
	if uri == "" || dbName == "" {
		log.Fatal("DB_URI and DB_NAME are required")
	}
	if *batchSize < 1 {
		log.Fatal("--batch must be at least 1")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Disconnect(ctx) }()

	coll := client.Database(dbName).Collection("communities")

	// Candidates: no invite of their own, but something we might derive one from.
	// Narrowing here rather than scanning all 159k communities keeps this to the
	// few thousand documents that could possibly change.
	filter := bson.M{
		"community.discordInviteUrl": bson.M{"$in": bson.A{nil, ""}},
		"$or": bson.A{
			bson.M{"community.description": bson.M{"$regex": `discord\.gg|discord(app)?\.com/invite`, "$options": "i"}},
			bson.M{"community.rpPromotion.history.data.inviteUrl": bson.M{"$nin": bson.A{nil, ""}}},
		},
	}
	if *publicOnly {
		filter["community.visibility"] = "public"
	}

	mode := "DRY-RUN (no writes)"
	if *apply {
		mode = "APPLY"
	}
	fmt.Printf("Mode: %s\nBatch: %d\nPublic only: %v\n\n", mode, *batchSize, *publicOnly)

	total, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		log.Fatalf("count: %v", err)
	}
	fmt.Printf("candidate documents: %d\n\n", total)
	if total == 0 {
		fmt.Println("nothing to do.")
		return
	}

	projection := options.Find().SetProjection(bson.M{
		"community.name":             1,
		"community.description":      1,
		"community.discordInviteUrl": 1,
		"community.visibility":       1,
		"community.rpPromotion":      1,
	})

	cur, err := coll.Find(ctx, filter, projection)
	if err != nil {
		log.Fatalf("find: %v", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	perSource := map[string]int{}
	var scanned, resolved, written, unresolvable int
	var batch []mongo.WriteModel

	flush := func() {
		if !*apply || len(batch) == 0 {
			batch = batch[:0]
			return
		}
		res, err := coll.BulkWrite(ctx, batch, options.BulkWrite().SetOrdered(false))
		if err != nil {
			log.Fatalf("bulk write: %v", err)
		}
		written += int(res.ModifiedCount)
		batch = batch[:0]
	}

	for cur.Next(ctx) {
		var d communityDoc
		// This is the raw driver cursor, where Decode inside a Next loop is the
		// correct idiom. The DecodeCurrent footgun applies to the repo's
		// databases.MongoCursor wrapper, which this script does not use.
		if err := cur.Decode(&d); err != nil {
			log.Printf("skip document (decode): %v", err)
			continue
		}
		scanned++

		invite, source := resolve(d)
		if invite == "" {
			// Matched the regex but held no extractable code — for example a bare
			// mention of "discord.com/invite" with nothing after it.
			unresolvable++
			continue
		}
		// The owner source cannot occur here: the filter requires the field to be
		// empty. Anything else would mean the filter and the resolver disagree.
		if source == models.DiscordInviteSourceOwner {
			log.Printf("unexpected owner-sourced invite on %s, skipping", d.ID.Hex())
			continue
		}

		resolved++
		perSource[source]++
		if resolved <= *samples {
			name := d.Details.Name
			if len(name) > 30 {
				name = name[:30]
			}
			fmt.Printf("  e.g. %-32q %-12s -> %s\n", name, source, invite)
		}

		batch = append(batch, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"_id": d.ID}).
			SetUpdate(bson.M{"$set": bson.M{"community.discordInviteUrl": invite}}))

		if len(batch) >= *batchSize {
			flush()
		}
	}
	if err := cur.Err(); err != nil {
		log.Fatalf("cursor: %v", err)
	}
	flush()

	fmt.Printf("\nscanned:              %d\n", scanned)
	fmt.Printf("resolved an invite:   %d\n", resolved)
	for _, source := range []string{models.DiscordInviteSourcePromotion, models.DiscordInviteSourceDescription} {
		fmt.Printf("  from %-12s     %d\n", source+":", perSource[source])
	}
	fmt.Printf("matched but no code:  %d\n", unresolvable)
	if *apply {
		fmt.Printf("documents written:    %d\n", written)
	} else {
		fmt.Printf("\nDRY-RUN: no changes written. Re-run with --apply to write.\n")
	}
}
