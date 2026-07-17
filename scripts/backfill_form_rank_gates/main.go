package main

// Backfill: migrate form templates from the legacy single-department rank
// gate shape (formTemplate.departmentId + visibleToRankRule + editableByRankRule)
// to the per-department rankGates array.
//
// The API normalizes legacy rows on read via FormTemplateDetails.EffectiveRankGates,
// so enforcement already works without this migration. This script makes the
// stored shape consistent so the legacy fields can eventually be dropped and
// so future writes never mix both shapes.
//
// Default behavior: dry-run — print each row that would change. Pass --apply
// to write. --id targets a single template _id (hex).
//
// Usage:
//   MONGO_URI="..." DB_NAME="..." go run ./scripts/backfill_form_rank_gates
//   MONGO_URI="..." DB_NAME="..." go run ./scripts/backfill_form_rank_gates --apply

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/models"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry-run)")
	idFlag := flag.String("id", "", "migrate only this form template _id (hex). default: scan all")
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

	col := client.Database(dbName).Collection("formTemplates")

	// Candidates: a legacy departmentId is set and no rankGates array exists
	// yet. We normalize in code (not a pure DB projection) so the exact same
	// EffectiveRankGates logic the API uses drives the migration.
	filter := bson.M{
		"formTemplate.departmentId": bson.M{"$exists": true, "$ne": ""},
		"$or": []bson.M{
			{"formTemplate.rankGates": bson.M{"$exists": false}},
			{"formTemplate.rankGates": bson.M{"$size": 0}},
		},
	}
	if *idFlag != "" {
		oid, err := primitive.ObjectIDFromHex(*idFlag)
		if err != nil {
			log.Fatalf("invalid --id: %v", err)
		}
		filter["_id"] = oid
	}

	cur, err := col.Find(ctx, filter)
	if err != nil {
		log.Fatalf("find: %v", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	var scanned, changed, skipped int
	for cur.Next(ctx) {
		var tpl models.FormTemplate
		if err := cur.Decode(&tpl); err != nil {
			log.Printf("skip (decode error): %v", err)
			skipped++
			continue
		}
		scanned++

		gates := tpl.Details.EffectiveRankGates()
		if len(gates) == 0 {
			// departmentId set but no actual rank rule — nothing meaningful
			// to carry forward. Just clear the stray legacy departmentId.
			log.Printf("clear-only  _id=%s slug=%q (legacy departmentId, no rule)", tpl.ID.Hex(), tpl.Details.Slug)
			if *apply {
				if _, err := col.UpdateByID(ctx, tpl.ID, bson.M{"$unset": bson.M{
					"formTemplate.departmentId":       "",
					"formTemplate.visibleToRankRule":  "",
					"formTemplate.editableByRankRule": "",
				}}); err != nil {
					log.Printf("  update failed: %v", err)
					skipped++
					continue
				}
			}
			changed++
			continue
		}

		log.Printf("migrate     _id=%s slug=%q dept=%s -> %d gate(s)", tpl.ID.Hex(), tpl.Details.Slug, gates[0].DepartmentID, len(gates))
		if *apply {
			if _, err := col.UpdateByID(ctx, tpl.ID, bson.M{
				"$set": bson.M{"formTemplate.rankGates": gates},
				"$unset": bson.M{
					"formTemplate.departmentId":       "",
					"formTemplate.visibleToRankRule":  "",
					"formTemplate.editableByRankRule": "",
				},
			}); err != nil {
				log.Printf("  update failed: %v", err)
				skipped++
				continue
			}
		}
		changed++
	}
	if err := cur.Err(); err != nil {
		log.Fatalf("cursor: %v", err)
	}

	mode := "DRY-RUN (no writes)"
	if *apply {
		mode = "APPLIED"
	}
	log.Printf("done [%s]: scanned=%d changed=%d skipped=%d", mode, scanned, changed, skipped)
}
