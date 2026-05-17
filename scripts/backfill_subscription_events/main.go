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
//	DB_URI=mongodb+srv://... \
//	DB_NAME=lpc \
//	REVENUECAT_SECRET_API_KEY=sk_xxx \
//	go run ./scripts/backfill_subscription_events
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
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
	mongoURI := mustEnv("DB_URI")
	dbName := mustEnv("DB_NAME")
	rcKey := mustEnv("REVENUECAT_SECRET_API_KEY")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		die("connect mongo: %v", err)
	}
	defer client.Disconnect(ctx)

	users := client.Database(dbName).Collection("users")
	events := client.Database(dbName).Collection("subscription_events")

	// Anyone with subscription state we know about. This is the set of
	// users whose history we can hope to recover from RC.
	filter := bson.M{
		"$or": []bson.M{
			{"user.subscription.id": bson.M{"$nin": []interface{}{"", nil}}},
			{"user.subscription.source": bson.M{"$nin": []interface{}{"", nil}}},
		},
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
