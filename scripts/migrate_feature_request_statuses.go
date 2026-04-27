package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// One-off migration: rename feature-request statuses after dropping
// "under_review" and renaming "in_progress" -> "beta_testing".
//
//   under_review -> open
//   in_progress  -> beta_testing
//
// Usage:
//   MONGO_URI="..." DB_NAME="..." go run scripts/migrate_feature_request_statuses.go
//   # add --dry-run to preview without writing

func main() {
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

	dryRun := false
	for _, a := range os.Args[1:] {
		if a == "--dry-run" || a == "-n" {
			dryRun = true
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	col := client.Database(dbName).Collection("featureRequests")

	migrations := []struct {
		from string
		to   string
	}{
		{"under_review", "open"},
		{"in_progress", "beta_testing"},
	}

	for _, m := range migrations {
		filter := bson.M{"status": m.from}
		count, err := col.CountDocuments(ctx, filter)
		if err != nil {
			log.Fatalf("count %s: %v", m.from, err)
		}
		fmt.Printf("%-14s -> %-14s : %d documents\n", m.from, m.to, count)
		if count == 0 || dryRun {
			continue
		}
		res, err := col.UpdateMany(ctx, filter, bson.M{
			"$set": bson.M{
				"status":    m.to,
				"updatedAt": time.Now(),
			},
		})
		if err != nil {
			log.Fatalf("update %s: %v", m.from, err)
		}
		fmt.Printf("  modified=%d matched=%d\n", res.ModifiedCount, res.MatchedCount)
	}

	if dryRun {
		fmt.Println("\n(dry-run — no writes performed)")
	}
}
