// One-off backfill: collapse the two vehicle flag encodings into one.
//
// Why: validRegistration, validInsurance, isStolen and isExempt are stored in
// two encodings. Newer records use "true"/"false". Older ones use a 1-based
// select index from the original form, whose polarity differs per field — "1"
// is a valid registration but "1" is NOT stolen. Every client rolled its own
// truth test, several applied one shared test to all four fields, and the
// stolen flag ended up inverted on the CAD search, the department dashboard and
// the mobile app. See models/vehicle_flags.go for the full explanation.
//
// The API now resolves both encodings on read and writes only the canonical
// form, so records heal as they are edited. This script finishes the job for
// the ~92% that may never be touched. Once it has run everywhere, the legacy
// branches in models/vehicle_flags.go (and the mirrored client helpers) can go.
//
// Polarity is decided by models.VehicleDetails.NormalizeFlags, deliberately —
// there must be exactly one implementation of this logic, and it is the one
// covered by models/vehicle_flags_test.go.
//
// Scope: only fields that are PRESENT and not already canonical are rewritten.
// An absent isExempt stays absent rather than adding a field to ~2.19M
// documents for no behavioural gain (the API already reads absent as false).
//
// READ-ONLY by default. Pass --apply to actually write.
//
// Usage:
//
//	DB_URI=mongodb+srv://... DB_NAME=lpc \
//	go run ./scripts/normalize_vehicle_flags                  # dry-run preview
//	go run ./scripts/normalize_vehicle_flags --apply          # write
//	go run ./scripts/normalize_vehicle_flags --apply --batch=2000
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

// Pointer fields so we can tell "absent" from "empty string" — we only rewrite
// what is actually there.
type flagDoc struct {
	ID      primitive.ObjectID `bson:"_id"`
	Details struct {
		ValidRegistration *string `bson:"validRegistration"`
		ValidInsurance    *string `bson:"validInsurance"`
		IsStolen          *string `bson:"isStolen"`
		IsExempt          *string `bson:"isExempt"`
	} `bson:"vehicle"`
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// changes returns the $set fields needed to canonicalize this document.
func changes(d flagDoc) bson.M {
	details := models.VehicleDetails{
		ValidRegistration: deref(d.Details.ValidRegistration),
		ValidInsurance:    deref(d.Details.ValidInsurance),
		IsStolen:          deref(d.Details.IsStolen),
		IsExempt:          deref(d.Details.IsExempt),
	}
	details.NormalizeFlags()

	set := bson.M{}
	if d.Details.ValidRegistration != nil && *d.Details.ValidRegistration != details.ValidRegistration {
		set["vehicle.validRegistration"] = details.ValidRegistration
	}
	if d.Details.ValidInsurance != nil && *d.Details.ValidInsurance != details.ValidInsurance {
		set["vehicle.validInsurance"] = details.ValidInsurance
	}
	if d.Details.IsStolen != nil && *d.Details.IsStolen != details.IsStolen {
		set["vehicle.isStolen"] = details.IsStolen
	}
	if d.Details.IsExempt != nil && *d.Details.IsExempt != details.IsExempt {
		set["vehicle.isExempt"] = details.IsExempt
	}
	return set
}

func main() {
	apply := flag.Bool("apply", false, "write changes (default is a read-only preview)")
	batchSize := flag.Int("batch", 1000, "documents per bulk write")
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

	coll := client.Database(dbName).Collection("vehicles")

	canonical := bson.A{models.FlagTrue, models.FlagFalse}
	// Any document where at least one PRESENT flag is not already canonical.
	// $exists guards keep absent fields from dragging in documents that have
	// nothing to fix.
	filter := bson.M{"$or": bson.A{
		bson.M{"vehicle.validRegistration": bson.M{"$exists": true, "$nin": canonical}},
		bson.M{"vehicle.validInsurance": bson.M{"$exists": true, "$nin": canonical}},
		bson.M{"vehicle.isStolen": bson.M{"$exists": true, "$nin": canonical}},
		bson.M{"vehicle.isExempt": bson.M{"$exists": true, "$nin": canonical}},
	}}

	mode := "DRY-RUN (no writes)"
	if *apply {
		mode = "APPLY"
	}
	fmt.Printf("Mode: %s\nBatch: %d\n\n", mode, *batchSize)

	total, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		log.Fatalf("count: %v", err)
	}
	fmt.Printf("documents needing normalization: %d\n\n", total)
	if total == 0 {
		fmt.Println("nothing to do.")
		return
	}

	projection := options.Find().SetProjection(bson.M{
		"vehicle.validRegistration": 1,
		"vehicle.validInsurance":    1,
		"vehicle.isStolen":          1,
		"vehicle.isExempt":          1,
	})

	cur, err := coll.Find(ctx, filter, projection)
	if err != nil {
		log.Fatalf("find: %v", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	// Per-field tallies so the dry-run says exactly what would change.
	perField := map[string]int{}
	var scanned, needing, written int
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
		var d flagDoc
		if err := cur.Decode(&d); err != nil {
			log.Printf("skip document (decode): %v", err)
			continue
		}
		scanned++

		set := changes(d)
		if len(set) == 0 {
			continue
		}
		needing++
		for k := range set {
			perField[k]++
		}

		batch = append(batch, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"_id": d.ID}).
			SetUpdate(bson.M{"$set": set}))

		if len(batch) >= *batchSize {
			flush()
			fmt.Printf("  ...%d/%d scanned, %d to change\n", scanned, total, needing)
		}
	}
	if err := cur.Err(); err != nil {
		log.Fatalf("cursor: %v", err)
	}
	flush()

	fmt.Printf("\nscanned:          %d\n", scanned)
	fmt.Printf("documents to fix: %d\n", needing)
	for _, f := range []string{
		"vehicle.validRegistration",
		"vehicle.validInsurance",
		"vehicle.isStolen",
		"vehicle.isExempt",
	} {
		fmt.Printf("  %-28s %d\n", f, perField[f])
	}
	if *apply {
		fmt.Printf("\nmodified: %d\n", written)
	} else {
		fmt.Printf("\nRe-run with --apply to write these %d documents.\n", needing)
	}
}
