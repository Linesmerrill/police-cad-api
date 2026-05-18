// One-shot backfill: synthesize `subscription_events` rows for every user
// who has touched subscription state in Mongo (user.subscription.id or
// user.subscription.source set). For each such user we call RevenueCat's
// REST API and write one row per known subscription product, so the new
// admin Subscriptions dashboard can find historical purchases that
// happened before the audit collection existed.
//
// Backfilled rows are clearly labelled:
//
//	provider:        "revenuecat"
//	eventType:       "BACKFILL"
//	providerEventId: "backfill:<userId>:<productId>"
//
// The providerEventId convention makes re-runs idempotent — the unique
// (provider, providerEventId) index returns E11000 on the second insert.
//
// READ-ONLY against RevenueCat. Only writes to the subscription_events
// collection. Never modifies the User document or any other state.
//
// Usage:
//
//	# Default: anyone we have ANY subscription hint about.
//	DB_URI=... DB_NAME=... REVENUECAT_SECRET_API_KEY=... \
//	    go run ./scripts/backfill_subscription_events
//
//	# Targeted: pass specific user ids (e.g. from reconciliation_<date>.csv).
//	go run ./scripts/backfill_subscription_events \
//	    --user-ids=5e8adfd6...,601d6c68...,68695e49...
package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const rcBaseURL = "https://api.revenuecat.com/v1"

// rcSubscription mirrors the shape RevenueCat returns under
// subscriber.subscriptions[productId]. Only the fields we persist are
// listed — RC returns ~20 fields, the rest stay in rawPayload.
type rcSubscription struct {
	ExpiresDate             *time.Time `json:"expires_date"`
	PurchaseDate            *time.Time `json:"purchase_date"`
	OriginalPurchaseDate    *time.Time `json:"original_purchase_date"`
	UnsubscribeDetectedAt   *time.Time `json:"unsubscribe_detected_at"`
	BillingIssuesDetectedAt *time.Time `json:"billing_issues_detected_at"`
	Store                   string     `json:"store"`
	IsSandbox               bool       `json:"is_sandbox"`
	PeriodType              string     `json:"period_type"`
	PriceInPurchasedCurrency struct {
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
	} `json:"price_in_purchased_currency"`
}

type rcResponse struct {
	Subscriber struct {
		OriginalAppUserID string                    `json:"original_app_user_id"`
		Subscriptions     map[string]rcSubscription `json:"subscriptions"`
	} `json:"subscriber"`
}

func main() {
	userIDsFlag := flag.String("user-ids", "", "Comma-separated user ids to target (skips the default broad scan). Useful for backfilling the users flagged by scripts/reconcile_revenuecat/main.go.")
	usersCSVFlag := flag.String("users-csv", "", "Path to a CSV whose first column is user_id (the reconciliation_<date>.csv format works). Read in addition to --user-ids.")
	flag.Parse()

	mongoURI := mustEnv("DB_URI")
	dbName := mustEnv("DB_NAME")
	rcKey := mustEnv("REVENUECAT_SECRET_API_KEY")

	targetIDs, err := collectTargetUserIDs(*userIDsFlag, *usersCSVFlag)
	if err != nil {
		die("read target user ids: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		die("connect mongo: %v", err)
	}
	defer client.Disconnect(ctx)

	users := client.Database(dbName).Collection("users")
	events := client.Database(dbName).Collection("subscription_events")

	// Two modes:
	//   - Targeted: backfill exactly the user ids passed in. Used to fix
	//     specific reconciliation-flagged accounts whose subscription
	//     struct is wiped (id="" / source="") and would be missed by
	//     the broad-scan filter below.
	//   - Broad: anyone we have ANY subscription hint about. Includes
	//     createdAt so wiped-state users still get caught — the previous
	//     filter required id/source which is exactly what gets nulled
	//     out in the failure mode we're trying to repair.
	var filter bson.M
	if len(targetIDs) > 0 {
		oids := make([]interface{}, 0, len(targetIDs))
		for _, id := range targetIDs {
			oid, err := primitiveObjectIDFromHex(id)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  ! skipping invalid user id %q: %v\n", id, err)
				continue
			}
			oids = append(oids, oid)
		}
		if len(oids) == 0 {
			die("no valid user ids in --user-ids / --users-csv")
		}
		filter = bson.M{"_id": bson.M{"$in": oids}}
		fmt.Printf("Targeted mode: %d user id(s)\n", len(oids))
	} else {
		filter = bson.M{
			"$or": []bson.M{
				{"user.subscription.id": bson.M{"$nin": []interface{}{"", nil}}},
				{"user.subscription.source": bson.M{"$nin": []interface{}{"", nil}}},
				{"user.subscription.stripeCustomerId": bson.M{"$nin": []interface{}{"", nil}}},
				{"user.subscription.createdAt": bson.M{"$ne": nil}},
			},
		}
	}

	cursor, err := users.Find(ctx, filter, options.Find().SetBatchSize(200))
	if err != nil {
		die("query users: %v", err)
	}
	defer cursor.Close(ctx)

	httpClient := &http.Client{Timeout: 30 * time.Second}
	scanned, backfilled, skippedDup, rcMisses, rcErrors := 0, 0, 0, 0, 0

	for cursor.Next(ctx) {
		scanned++
		var doc struct {
			ID   string `bson:"_id"`
			User struct {
				Email string `bson:"email"`
			} `bson:"user"`
		}
		if err := cursor.Decode(&doc); err != nil {
			continue
		}

		sub, rawBody, err := fetchRCSubscriber(ctx, httpClient, rcKey, doc.ID)
		if err != nil {
			if err == errNotFound {
				rcMisses++
				continue
			}
			rcErrors++
			fmt.Fprintf(os.Stderr, "  ! RC lookup failed for %s (%s): %v\n", doc.ID, doc.User.Email, err)
			continue
		}

		if len(sub.Subscriber.Subscriptions) == 0 {
			rcMisses++
			continue
		}

		for productID, s := range sub.Subscriber.Subscriptions {
			plan, isAnnual, _ := parseProductIDLocal(productID)
			row := bson.M{
				"providerEventId":  fmt.Sprintf("backfill:%s:%s", doc.ID, productID),
				"userId":           doc.ID,
				"userEmail":        doc.User.Email,
				"provider":         "revenuecat",
				"store":            strings.ToUpper(s.Store),
				"eventType":        "BACKFILL",
				"plan":             plan,
				"isAnnual":         isAnnual,
				"productId":        productID,
				"priceLocal":       s.PriceInPurchasedCurrency.Amount,
				"currency":         s.PriceInPurchasedCurrency.Currency,
				"purchasedAt":      s.PurchaseDate,
				"expiresAt":        s.ExpiresDate,
				"environment":      envFromIsSandbox(s.IsSandbox),
				"rawPayload":       string(rawBody),
				"processingStatus": "ok",
				"createdAt":        time.Now().UTC(),
			}
			if _, err := events.InsertOne(ctx, row); err != nil {
				if mongo.IsDuplicateKeyError(err) {
					skippedDup++
					continue
				}
				fmt.Fprintf(os.Stderr, "  ! insert failed for %s/%s: %v\n", doc.ID, productID, err)
				continue
			}
			backfilled++
			fmt.Printf("BACKFILLED %s (%s) — %s %s\n", doc.ID, doc.User.Email, strings.ToUpper(s.Store), productID)
		}

		if scanned%50 == 0 {
			fmt.Printf("... scanned %d users, backfilled %d rows, skipped %d dup, %d RC misses, %d RC errors\n",
				scanned, backfilled, skippedDup, rcMisses, rcErrors)
		}
	}
	if err := cursor.Err(); err != nil {
		die("cursor: %v", err)
	}
	fmt.Printf("Done. Scanned %d users, backfilled %d rows, %d already-backfilled, %d users with no RC record, %d RC errors.\n",
		scanned, backfilled, skippedDup, rcMisses, rcErrors)
}

var errNotFound = fmt.Errorf("revenuecat subscriber not found")

func fetchRCSubscriber(ctx context.Context, c *http.Client, apiKey, appUserID string) (*rcResponse, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rcBaseURL+"/subscribers/"+appUserID, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil, errNotFound
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		time.Sleep(5 * time.Second)
		return fetchRCSubscriber(ctx, c, apiKey, appUserID)
	}
	if resp.StatusCode/100 != 2 {
		return nil, nil, fmt.Errorf("RC %s: %s", resp.Status, string(body))
	}
	var out rcResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&out); err != nil {
		return nil, nil, err
	}
	return &out, body, nil
}

// parseProductIDLocal mirrors the logic in api/handlers/subscription_helpers.go.
// Duplicated here so the script has zero dependencies on the handlers package
// (which pulls in the whole Stripe SDK, gorilla/mux, etc. for nothing).
func parseProductIDLocal(productID string) (plan string, isAnnual, ok bool) {
	id := strings.ToLower(strings.TrimSpace(productID))
	if id == "" {
		return "", false, false
	}
	if i := strings.Index(id, ":"); i >= 0 {
		id = id[:i]
	}
	if i := strings.LastIndex(id, "."); i >= 0 {
		id = id[i+1:]
	}
	switch {
	case strings.HasSuffix(id, "_annual"):
		isAnnual = true
		plan = strings.TrimSuffix(id, "_annual")
	case strings.HasSuffix(id, "_monthly"):
		isAnnual = false
		plan = strings.TrimSuffix(id, "_monthly")
	default:
		return "", false, false
	}
	switch plan {
	case "base", "premium", "premium_plus":
		return plan, isAnnual, true
	default:
		return "", false, false
	}
}

func envFromIsSandbox(sandbox bool) string {
	if sandbox {
		return "SANDBOX"
	}
	return "PRODUCTION"
}

// collectTargetUserIDs merges --user-ids and the first column of --users-csv
// (the format the reconciliation script emits). De-dupes, preserves order
// of first appearance for deterministic logs.
func collectTargetUserIDs(idsFlag, csvPath string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, id := range strings.Split(idsFlag, ",") {
		add(id)
	}
	if csvPath != "" {
		f, err := os.Open(csvPath)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r := csv.NewReader(f)
		r.FieldsPerRecord = -1 // tolerate ragged rows
		for i := 0; ; i++ {
			rec, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("csv row %d: %v", i+1, err)
			}
			if len(rec) == 0 {
				continue
			}
			// Skip the header row written by reconcile_revenuecat.
			if i == 0 && strings.EqualFold(strings.TrimSpace(rec[0]), "user_id") {
				continue
			}
			add(rec[0])
		}
	}
	return out, nil
}

func primitiveObjectIDFromHex(s string) (interface{}, error) {
	return primitive.ObjectIDFromHex(strings.TrimSpace(s))
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		die("missing env var %s", k)
	}
	return v
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
