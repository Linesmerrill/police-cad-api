// Reconciliation report: find users who appear to be on the "free" plan in
// our database but who still have an active subscription according to
// RevenueCat. These are the customers most likely to be in the
// jaseysbro@gmail.com situation — getting charged in the Play Store /
// App Store while our app treats them as non-paying.
//
// READ-ONLY. Writes a CSV report and never mutates the database or any
// store/RevenueCat record. Manual cancellation (or restore) is up to a
// human reviewing the output.
//
// Usage:
//
//	DB_URI=mongodb+srv://... \
//	DB_NAME=lpc \
//	REVENUECAT_SECRET_API_KEY=sk_xxx \
//	go run ./scripts/reconcile_revenuecat
//
// Outputs reconciliation_<UTC date>.csv in the working directory.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const rcBaseURL = "https://api.revenuecat.com/v1"

type rcSubscription struct {
	ExpiresDate            *time.Time `json:"expires_date"`
	UnsubscribeDetectedAt  *time.Time `json:"unsubscribe_detected_at"`
	BillingIssuesDetectedAt *time.Time `json:"billing_issues_detected_at"`
	Store                  string     `json:"store"`
	PurchaseDate           *time.Time `json:"purchase_date"`
	OriginalPurchaseDate   *time.Time `json:"original_purchase_date"`
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		die("connect mongo: %v", err)
	}
	defer client.Disconnect(ctx)

	users := client.Database(dbName).Collection("users")

	// Look at users who LOOK like they're not paying but who subscribed
	// some time in the past (subscription.createdAt set OR updatedAt in
	// the last 12 months). We don't want to hit RC for every legacy free
	// account — only ones with any history of touching subscription state.
	cutoff := time.Now().UTC().AddDate(-1, 0, 0)
	filter := bson.M{
		"user.subscription.active": false,
		"user.subscription.plan":   bson.M{"$in": []string{"free", ""}},
		"$or": []bson.M{
			{"user.subscription.createdAt": bson.M{"$ne": nil}},
			{"user.subscription.updatedAt": bson.M{"$gte": cutoff}},
		},
	}

	cursor, err := users.Find(ctx, filter, options.Find().SetBatchSize(200))
	if err != nil {
		die("query users: %v", err)
	}
	defer cursor.Close(ctx)

	out, err := os.Create(fmt.Sprintf("reconciliation_%s.csv", time.Now().UTC().Format("2006-01-02")))
	if err != nil {
		die("create csv: %v", err)
	}
	defer out.Close()
	w := csv.NewWriter(out)
	defer w.Flush()
	_ = w.Write([]string{"user_id", "email", "store", "product_id", "expires_at", "purchase_date", "price", "currency"})

	httpClient := &http.Client{Timeout: 30 * time.Second}
	scanned := 0
	flagged := 0
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
		sub, err := fetchRCSubscriber(ctx, httpClient, rcKey, doc.ID)
		if err != nil {
			// 404 = no RC record. Other errors: log and move on.
			if err != errNotFound {
				fmt.Fprintf(os.Stderr, "  ! RC lookup failed for %s: %v\n", doc.ID, err)
			}
			continue
		}
		now := time.Now().UTC()
		for productID, s := range sub.Subscriber.Subscriptions {
			if s.ExpiresDate == nil || !s.ExpiresDate.After(now) {
				continue
			}
			if s.UnsubscribeDetectedAt != nil {
				continue
			}
			if s.BillingIssuesDetectedAt != nil {
				continue
			}
			flagged++
			_ = w.Write([]string{
				doc.ID,
				doc.User.Email,
				s.Store,
				productID,
				s.ExpiresDate.Format(time.RFC3339),
				timeStr(s.PurchaseDate),
				strconv.FormatFloat(s.PriceInPurchasedCurrency.Amount, 'f', 2, 64),
				s.PriceInPurchasedCurrency.Currency,
			})
			fmt.Printf("FLAGGED %s (%s) — %s %s expires %s\n",
				doc.ID, doc.User.Email, s.Store, productID, s.ExpiresDate.Format(time.RFC3339))
		}
		if scanned%50 == 0 {
			fmt.Printf("... scanned %d users, flagged %d so far\n", scanned, flagged)
		}
	}
	if err := cursor.Err(); err != nil {
		die("cursor: %v", err)
	}
	fmt.Printf("Done. Scanned %d users, flagged %d. Report: %s\n", scanned, flagged, out.Name())
}

var errNotFound = fmt.Errorf("revenuecat subscriber not found")

func fetchRCSubscriber(ctx context.Context, c *http.Client, apiKey, appUserID string) (*rcResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rcBaseURL+"/subscribers/"+appUserID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNotFound
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		// RC sometimes rate-limits aggressive scans. Back off and retry once.
		time.Sleep(5 * time.Second)
		return fetchRCSubscriber(ctx, c, apiKey, appUserID)
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("RC %s: %s", resp.Status, string(body))
	}
	var out rcResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func timeStr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
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
