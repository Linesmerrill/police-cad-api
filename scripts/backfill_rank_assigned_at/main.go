// One-off backfill: stamp rankAssignedAt on department members who hold a rank
// but have no rank-assignment timestamp.
//
// Why: rankStatsSince() (api/handlers/rank.go) only measures progress since the
// last promotion when BOTH the community has rankSettings.resetStatsOnPromotion
// enabled AND the member has a rankAssignedAt. Members ranked before that
// feature shipped (2026-06-24), or seeded onto a default rank by the old
// GetRankProgressHandler path, have no timestamp — so they silently fall back to
// ALL-TIME counting. Their stats never appear to reset after a promotion and
// they can chain-promote off lifetime totals, which is exactly the reported bug.
//
// Scope: only communities with resetStatsOnPromotion == true (elsewhere the
// timestamp is unused, so writing one would be noise). Only members that have a
// rankId but no rankAssignedAt are touched; everyone already stamped is left
// alone.
//
// The stamp value is "now" by default: the member's real last-promotion time was
// never recorded, and starting their count from the backfill moment is the
// predictable choice (no surprise wave of instant promotions). Pass --since to
// use a specific RFC3339 instant instead — e.g. the date the feature shipped, if
// you'd rather credit work done since then.
//
// READ-ONLY by default. Pass --apply to actually write.
//
// Usage:
//
//	DB_URI=mongodb+srv://... DB_NAME=lpc \
//	go run ./scripts/backfill_rank_assigned_at                     # dry-run preview
//	go run ./scripts/backfill_rank_assigned_at --apply             # write (stamp = now)
//	go run ./scripts/backfill_rank_assigned_at --apply --since=2026-06-24T00:00:00Z
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

type member struct {
	UserID         string             `bson:"userID"`
	RankID         string             `bson:"rankId"`
	RankAssignedAt primitive.DateTime `bson:"rankAssignedAt"`
}

type department struct {
	ID      string   `bson:"_id"`
	Name    string   `bson:"name"`
	Members []member `bson:"members"`
}

type communityDetails struct {
	Name         string       `bson:"name"`
	Departments  []department `bson:"departments"`
	RankSettings struct {
		ResetStatsOnPromotion bool `bson:"resetStatsOnPromotion"`
	} `bson:"rankSettings"`
}

type communityDoc struct {
	ID      primitive.ObjectID `bson:"_id"`
	Details communityDetails   `bson:"community"`
}

func main() {
	apply := flag.Bool("apply", false, "write changes (default is a read-only preview)")
	sinceStr := flag.String("since", "", "RFC3339 instant to stamp instead of now (e.g. 2026-06-24T00:00:00Z)")
	flag.Parse()

	uri := os.Getenv("DB_URI")
	dbName := os.Getenv("DB_NAME")
	if uri == "" || dbName == "" {
		log.Fatal("DB_URI and DB_NAME are required")
	}

	stamp := time.Now()
	if *sinceStr != "" {
		parsed, err := time.Parse(time.RFC3339, *sinceStr)
		if err != nil {
			log.Fatalf("invalid --since (want RFC3339): %v", err)
		}
		stamp = parsed
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Disconnect(ctx) }()

	coll := client.Database(dbName).Collection("communities")

	// Only communities running the reset-on-promotion behavior.
	cur, err := coll.Find(ctx, bson.M{"community.rankSettings.resetStatsOnPromotion": true})
	if err != nil {
		log.Fatalf("find communities: %v", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	mode := "DRY-RUN (no writes)"
	if *apply {
		mode = "APPLY"
	}
	fmt.Printf("Mode: %s\nStamp: %s\n\n", mode, stamp.UTC().Format(time.RFC3339))

	var totalComms, totalRanked, totalMissing, totalWritten int

	for cur.Next(ctx) {
		var c communityDoc
		if err := cur.Decode(&c); err != nil {
			log.Printf("skip community (decode): %v", err)
			continue
		}
		totalComms++

		setFields := bson.M{}
		var missingHere, rankedHere int

		for di, d := range c.Details.Departments {
			for mi, m := range d.Members {
				if m.RankID == "" {
					continue
				}
				rankedHere++
				if m.RankAssignedAt != 0 {
					continue
				}
				missingHere++
				setFields[fmt.Sprintf("community.departments.%d.members.%d.rankAssignedAt", di, mi)] =
					primitive.NewDateTimeFromTime(stamp)
			}
		}

		totalRanked += rankedHere
		totalMissing += missingHere
		if missingHere == 0 {
			continue
		}

		fmt.Printf("%-40s %d/%d members missing rankAssignedAt\n", c.Details.Name, missingHere, rankedHere)

		if *apply {
			res, err := coll.UpdateOne(ctx, bson.M{"_id": c.ID}, bson.M{"$set": setFields})
			if err != nil {
				log.Printf("  update failed for %s: %v", c.ID.Hex(), err)
				continue
			}
			if res.ModifiedCount > 0 {
				totalWritten += missingHere
			}
		}
	}
	if err := cur.Err(); err != nil {
		log.Fatalf("cursor: %v", err)
	}

	fmt.Printf("\ncommunities with resetStatsOnPromotion=true: %d\n", totalComms)
	fmt.Printf("ranked members:                              %d\n", totalRanked)
	fmt.Printf("missing rankAssignedAt:                      %d\n", totalMissing)
	if *apply {
		fmt.Printf("stamped:                                     %d\n", totalWritten)
	} else {
		fmt.Printf("\nRe-run with --apply to write these %d timestamps.\n", totalMissing)
	}
}
