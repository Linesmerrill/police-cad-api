// One-off backfill: give every license a canonical set of keys.
//
// Why: the licenses collection was written by two clients that never agreed on
// a key set. ~316k legacy documents store the civilian as license.ownerID, the
// type as license.licenseType and the notes as license.additionalNotes, while
// ~101k newer ones use license.civilianID / type / notes. Because the list
// endpoint only ever filtered on license.civilianID, the legacy records were
// invisible to the API and roughly 170k civilians saw an empty license list on
// mobile. See models/license_normalize.go for the full explanation.
//
// The API now reads both shapes and writes the canonical one, so records heal
// as they are edited. This script finishes the job for the ones nobody touches.
//
// This backfill is deliberately ADDITIVE. It only fills in a canonical key that
// is missing, and reformats expirationDate. It never deletes a legacy key and
// never rewrites license.status or license.licenseType, because the website's
// police and dispatch dashboards still render licenses straight from the
// Mongoose model and their <select> options are the lowercase legacy values
// (views/license-list.ejs, views/police-dashboard.ejs). Rewriting those in
// place would blank the dropdowns on a live CAD screen. Case normalization is
// handled on the API's read path instead, where it costs nothing.
//
// expirationDate is safe to rewrite: every dashboard that renders it uses
// <input type="date">, which requires yyyy-mm-dd and already fails to display
// the mm/dd/yyyy values. Anything ambiguous ("2045", "11/25") is left alone.
//
// Field mapping is decided by models.LicenseDetails.NormalizeFields,
// deliberately — there must be exactly one implementation of this logic, and it
// is the one covered by models/license_normalize_test.go.
//
// READ-ONLY by default. Pass --apply to actually write.
//
// Usage:
//
//	DB_URI=mongodb+srv://... DB_NAME=lpc \
//	go run ./scripts/normalize_licenses                  # dry-run preview
//	go run ./scripts/normalize_licenses --apply          # write
//	go run ./scripts/normalize_licenses --apply --batch=2000
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

// Pointer fields so we can tell "absent" from "empty string".
type licenseDoc struct {
	ID      primitive.ObjectID `bson:"_id"`
	Details struct {
		Type            *string `bson:"type"`
		Notes           *string `bson:"notes"`
		CivilianID      *string `bson:"civilianID"`
		ExpirationDate  *string `bson:"expirationDate"`
		LicenseType     *string `bson:"licenseType"`
		AdditionalNotes *string `bson:"additionalNotes"`
		OwnerID         *string `bson:"ownerID"`
	} `bson:"license"`
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// blank reports whether a canonical key still needs filling in.
func blank(s *string) bool {
	return s == nil || *s == ""
}

// changes returns the $set fields needed to canonicalize this document.
func changes(d licenseDoc) bson.M {
	details := models.LicenseDetails{
		Type:            deref(d.Details.Type),
		Notes:           deref(d.Details.Notes),
		CivilianID:      deref(d.Details.CivilianID),
		ExpirationDate:  deref(d.Details.ExpirationDate),
		LicenseType:     deref(d.Details.LicenseType),
		AdditionalNotes: deref(d.Details.AdditionalNotes),
		OwnerID:         deref(d.Details.OwnerID),
	}
	details.NormalizeFields()

	set := bson.M{}

	// Fill a missing canonical key from its legacy twin. An empty legacy value
	// has nothing to contribute, so it does not earn a new key.
	if blank(d.Details.Type) && details.Type != "" {
		set["license.type"] = details.Type
	}
	if blank(d.Details.Notes) && details.Notes != "" {
		set["license.notes"] = details.Notes
	}
	if blank(d.Details.CivilianID) && details.CivilianID != "" {
		set["license.civilianID"] = details.CivilianID
	}

	// Only rewrite a date that is present and actually changed.
	if d.Details.ExpirationDate != nil && *d.Details.ExpirationDate != details.ExpirationDate {
		set["license.expirationDate"] = details.ExpirationDate
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

	coll := client.Database(dbName).Collection("licenses")

	// Candidates: a legacy key present while its canonical twin is missing, or
	// an expiration date that is not already yyyy-mm-dd.
	missing := func(canonical, legacy string) bson.M {
		return bson.M{
			"license." + canonical: bson.M{"$in": bson.A{nil, ""}},
			"license." + legacy:    bson.M{"$nin": bson.A{nil, ""}},
		}
	}
	filter := bson.M{"$or": bson.A{
		missing("type", "licenseType"),
		missing("notes", "additionalNotes"),
		missing("civilianID", "ownerID"),
		bson.M{"license.expirationDate": bson.M{
			"$exists": true,
			"$not":    bson.M{"$regex": `^\d{4}-\d{2}-\d{2}$`},
		}},
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
	fmt.Printf("candidate documents: %d\n\n", total)
	if total == 0 {
		fmt.Println("nothing to do.")
		return
	}

	projection := options.Find().SetProjection(bson.M{
		"license.type":            1,
		"license.notes":           1,
		"license.civilianID":      1,
		"license.expirationDate":  1,
		"license.licenseType":     1,
		"license.additionalNotes": 1,
		"license.ownerID":         1,
	})

	cur, err := coll.Find(ctx, filter, projection)
	if err != nil {
		log.Fatalf("find: %v", err)
	}
	defer func() { _ = cur.Close(ctx) }()

	perField := map[string]int{}
	var scanned, needing, written, unparseableDates int
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
		var d licenseDoc
		if err := cur.Decode(&d); err != nil {
			log.Printf("skip document (decode): %v", err)
			continue
		}
		scanned++

		set := changes(d)
		if _, dateChanged := set["license.expirationDate"]; !dateChanged && d.Details.ExpirationDate != nil {
			// Present, not canonical, and NormalizeFields declined to guess.
			if raw := *d.Details.ExpirationDate; raw != "" && len(raw) != 10 {
				unparseableDates++
			}
		}
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
		"license.type",
		"license.notes",
		"license.civilianID",
		"license.expirationDate",
	} {
		fmt.Printf("  %-26s %d\n", f, perField[f])
	}
	fmt.Printf("\nleft alone (ambiguous dates like \"2045\" or \"11/25\"): %d\n", unparseableDates)
	fmt.Println("legacy keys and license.status are never modified; see the header comment.")

	if *apply {
		fmt.Printf("\nmodified: %d\n", written)
	} else {
		fmt.Printf("\nRe-run with --apply to write these %d documents.\n", needing)
	}
}
