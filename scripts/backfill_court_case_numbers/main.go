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
)

// One-off backfill: populate `courtCase.caseNumber` on existing court cases that
// don't have one yet. Sequences are bucketed by (communityID, createdAt-year)
// using the same `casecounters` collection the live generator uses, so post-
// backfill creates continue from where the backfill left off.
//
// Usage:
//   MONGO_URI="..." DB_NAME="..." go run ./scripts/backfill_court_case_numbers
//   # add --dry-run to preview without writing

func main() {
	dryRun := flag.Bool("dry-run", false, "preview without writing")
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	cases := client.Database(dbName).Collection("courtcases")
	counters := client.Database(dbName).Collection("casecounters")

	// Find cases missing or empty caseNumber. Sort oldest first so generated
	// sequences are roughly chronological.
	filter := bson.M{
		"$or": []bson.M{
			{"courtCase.caseNumber": bson.M{"$exists": false}},
			{"courtCase.caseNumber": ""},
			{"courtCase.caseNumber": nil},
		},
	}
	total, err := cases.CountDocuments(ctx, filter)
	if err != nil {
		log.Fatalf("count: %v", err)
	}
	fmt.Printf("found %d court cases without caseNumber\n", total)
	if total == 0 {
		return
	}

	cur, err := cases.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "courtCase.createdAt", Value: 1}}))
	if err != nil {
		log.Fatalf("find: %v", err)
	}
	defer cur.Close(ctx)

	updated := 0
	skipped := 0
	for cur.Next(ctx) {
		var doc struct {
			ID        primitive.ObjectID `bson:"_id"`
			CourtCase struct {
				CommunityID string             `bson:"communityID"`
				CreatedAt   primitive.DateTime `bson:"createdAt"`
			} `bson:"courtCase"`
		}
		if err := cur.Decode(&doc); err != nil {
			log.Printf("decode error on %s: %v", cur.Current.Lookup("_id"), err)
			skipped++
			continue
		}
		if doc.CourtCase.CommunityID == "" {
			log.Printf("skipping %s: missing communityID", doc.ID.Hex())
			skipped++
			continue
		}

		year := doc.CourtCase.CreatedAt.Time().UTC().Year()
		if year < 2020 || year > 2100 {
			year = time.Now().UTC().Year()
		}
		counterID := fmt.Sprintf("%s:%d", doc.CourtCase.CommunityID, year)

		if *dryRun {
			fmt.Printf("would update %s -> CC-%d-NNNNNN (community=%s)\n",
				doc.ID.Hex(), year, doc.CourtCase.CommunityID)
			updated++
			continue
		}

		var counter struct {
			Seq int64 `bson:"seq"`
		}
		err := counters.FindOneAndUpdate(ctx,
			bson.M{"_id": counterID},
			bson.M{"$inc": bson.M{"seq": 1}},
			options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
		).Decode(&counter)
		if err != nil {
			log.Printf("counter increment failed for %s: %v", doc.ID.Hex(), err)
			skipped++
			continue
		}
		caseNumber := fmt.Sprintf("CC-%d-%06d", year, counter.Seq)

		_, err = cases.UpdateOne(ctx,
			bson.M{"_id": doc.ID},
			bson.M{"$set": bson.M{"courtCase.caseNumber": caseNumber}},
		)
		if err != nil {
			log.Printf("update failed for %s: %v", doc.ID.Hex(), err)
			skipped++
			continue
		}
		updated++
		if updated%100 == 0 {
			fmt.Printf("  progress: %d/%d\n", updated, total)
		}
	}
	if err := cur.Err(); err != nil {
		log.Fatalf("cursor: %v", err)
	}

	fmt.Printf("\ndone — updated=%d skipped=%d\n", updated, skipped)
	if *dryRun {
		fmt.Println("(dry-run — no writes performed)")
	}
}
