// Bulk-sync admin subscriptions: walk every user whose Mongo doc claims
// they're actively subscribed via app_store / play_store, re-check that
// against RevenueCat, and (in --apply mode) write the authoritative
// state back to the User document — same logic as clicking "Fix in DB"
// in the admin dashboard, just unattended.
//
// This exists to clean up the lingering damage from a webhook bug where
// HandleRevenueCatWebhook was filtering by user.id (never matched)
// instead of _id. Every iOS/Android user who expired between when the
// bug was introduced and when it was fixed is still ghost-active in
// our DB. This script downgrades them in batches with full audit trail.
//
// READ-ONLY by default. Pass --apply to actually write. Every write is
// mirrored to subscription_events with provider="admin",
// eventType="admin_bulk_sync" so the downgrade is reversible.
//
// Usage:
//
//	# Dry-run, default 50-user cap, RC only
//	DB_URI=mongodb+srv://... DB_NAME=lpc REVENUECAT_SECRET_API_KEY=sk_xxx \
//	  go run ./scripts/bulk_sync_subscriptions
//
//	# Apply changes to the first 200 candidates
//	DB_URI=... DB_NAME=... REVENUECAT_SECRET_API_KEY=... \
//	  go run ./scripts/bulk_sync_subscriptions --apply --limit=200
//
// Outputs bulk_sync_<UTC date>.csv in the working directory with one
// row per candidate (whether dry-run or apply).
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const rcBaseURL = "https://api.revenuecat.com/v1"

type rcSubscription struct {
	ExpiresDate             *time.Time `json:"expires_date"`
	PurchaseDate            *time.Time `json:"purchase_date"`
	OriginalPurchaseDate    *time.Time `json:"original_purchase_date"`
	UnsubscribeDetectedAt   *time.Time `json:"unsubscribe_detected_at"`
	BillingIssuesDetectedAt *time.Time `json:"billing_issues_detected_at"`
	Store                   string     `json:"store"`
}

type rcResponse struct {
	Subscriber struct {
		Subscriptions map[string]rcSubscription `json:"subscriptions"`
	} `json:"subscriber"`
}

// candidate is the post-classification view of one user.
type candidate struct {
	UserID      string
	Email       string
	DBPlan      string
	DBSource    string
	Verdict     string // "downgrade" | "active_ok" | "no_rc_record" | "rc_error"
	LivePlan    string // best-known plan from RC
	LiveStatus  string // "active" | "canceled_in_period" | "expired" | "billing_issue"
	ExpiresAt   *time.Time
	CancelAt    *time.Time
	Store       string
	NewActive   bool
	NewPlan     string
}

func main() {
	dryRun := flag.Bool("dry-run", true, "preview only — no DB writes (default true)")
	apply := flag.Bool("apply", false, "actually write changes (overrides --dry-run)")
	limit := flag.Int("limit", 50, "max users to process this run (safety cap)")
	source := flag.String("source", "revenuecat", "which provider to check: revenuecat (only one supported today)")
	flag.Parse()

	if *apply {
		*dryRun = false
	}

	if *source != "revenuecat" {
		die("only --source=revenuecat is supported in this version")
	}

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

	// Candidates: active=true AND looks like an iOS/Android user.
	// Three patterns we catch:
	//   1) source explicitly app_store or play_store
	//   2) source blank but sub.id looks like a non-Stripe transaction
	//      id (the webhook bug meant source never got written for many
	//      of these — they were the original purchase doc with id but
	//      nothing else ever updated)
	//   3) source blank, sub.id blank, but no Stripe customer (legacy
	//      mobile users from before the source field existed)
	// Stripe-source users are deliberately excluded — different webhook
	// path, didn't have the bug.
	filter := bson.M{
		"user.subscription.active": true,
		"$or": []bson.M{
			{"user.subscription.source": bson.M{"$in": []string{"app_store", "play_store"}}},
			{
				"user.subscription.source": bson.M{"$in": []interface{}{"", nil}},
				"user.subscription.id":     bson.M{"$exists": true, "$ne": "", "$not": bson.M{"$regex": "^sub_"}},
			},
			{
				"user.subscription.source":           bson.M{"$in": []interface{}{"", nil}},
				"user.subscription.stripeCustomerId": bson.M{"$in": []interface{}{"", nil}},
			},
		},
	}

	// Pre-count so you know the blast radius before any RC calls happen.
	total, err := users.CountDocuments(ctx, filter)
	if err != nil {
		die("count candidates: %v", err)
	}
	fmt.Printf("Matched %d candidate users by filter.\n", total)
	if total == 0 {
		fmt.Println("Nothing to process. If you expected matches, double-check the filter or DB connection.")
		return
	}

	cursor, err := users.Find(ctx, filter, options.Find().SetBatchSize(100))
	if err != nil {
		die("query users: %v", err)
	}
	defer cursor.Close(ctx)

	out, err := os.Create(fmt.Sprintf("bulk_sync_%s.csv", time.Now().UTC().Format("2006-01-02_15-04-05")))
	if err != nil {
		die("create csv: %v", err)
	}
	defer out.Close()
	w := csv.NewWriter(out)
	defer w.Flush()
	_ = w.Write([]string{
		"user_id", "email", "db_plan", "db_source", "verdict",
		"live_status", "live_plan", "expires_at", "cancel_at", "store",
		"new_active", "new_plan", "applied",
	})

	httpClient := &http.Client{Timeout: 30 * time.Second}
	now := time.Now().UTC()
	scanned, processed, applied := 0, 0, 0

	mode := "DRY-RUN"
	if !*dryRun {
		mode = "APPLY"
	}
	fmt.Printf("Bulk sync starting in %s mode. Limit: %d.\n", mode, *limit)
	if *dryRun {
		fmt.Println("(no DB writes will be performed; pass --apply to actually downgrade)")
	}
	fmt.Println()

	for cursor.Next(ctx) && processed < *limit {
		scanned++
		var doc struct {
			ID   primitive.ObjectID `bson:"_id"`
			User struct {
				Email        string `bson:"email"`
				Subscription struct {
					Plan   string `bson:"plan"`
					Source string `bson:"source"`
					ID     string `bson:"id"`
				} `bson:"subscription"`
			} `bson:"user"`
		}
		if err := cursor.Decode(&doc); err != nil {
			continue
		}
		userID := doc.ID.Hex()

		c := candidate{
			UserID:   userID,
			Email:    doc.User.Email,
			DBPlan:   doc.User.Subscription.Plan,
			DBSource: doc.User.Subscription.Source,
		}

		rc, err := fetchRCSubscriber(ctx, httpClient, rcKey, userID)
		if err != nil {
			if err == errNotFound {
				c.Verdict = "no_rc_record"
			} else {
				c.Verdict = "rc_error"
				fmt.Fprintf(os.Stderr, "  ! RC lookup failed for %s: %v\n", userID, err)
			}
			writeRow(w, c, false)
			continue
		}

		bestPlan, bestStatus, bestExp, bestCancel, bestStore := pickBestRCState(rc, now)
		c.LivePlan = bestPlan
		c.LiveStatus = bestStatus
		c.ExpiresAt = bestExp
		c.CancelAt = bestCancel
		c.Store = bestStore

		switch bestStatus {
		case "active", "canceled_in_period":
			// Live source still considers them entitled — don't touch them.
			c.Verdict = "active_ok"
			c.NewActive = true
			c.NewPlan = orDefault(bestPlan, c.DBPlan)
		default:
			// expired / billing_issue / none → downgrade to free/inactive
			c.Verdict = "downgrade"
			c.NewActive = false
			c.NewPlan = "free"
		}

		didApply := false
		if c.Verdict == "downgrade" && !*dryRun {
			if err := applyDowngrade(ctx, users, events, doc.ID, doc.User.Email, c); err != nil {
				fmt.Fprintf(os.Stderr, "  ! apply failed for %s: %v\n", userID, err)
			} else {
				didApply = true
				applied++
			}
		}

		writeRow(w, c, didApply)
		processed++

		fmt.Printf("[%s] %s (%s) — db: %s/%s → live: %s/%s",
			c.Verdict, userID, c.Email, c.DBPlan, c.DBSource, c.LivePlan, c.LiveStatus)
		if didApply {
			fmt.Print(" — APPLIED")
		}
		fmt.Println()

		if processed%25 == 0 {
			fmt.Printf("--- progress: scanned %d, processed %d, applied %d\n", scanned, processed, applied)
		}
	}
	if err := cursor.Err(); err != nil {
		die("cursor: %v", err)
	}

	fmt.Printf("\nDone. Scanned %d, processed %d, applied %d. Report: %s\n",
		scanned, processed, applied, out.Name())
	if *dryRun {
		fmt.Println("\n(dry-run) Re-run with --apply to actually write the downgrades.")
	}
}

// pickBestRCState mirrors summarizeRCToAuthoritative from the admin
// handler, condensed: pick the most-relevant subscription entry and
// classify it. Returns ("", "none", nil, nil, "") if RC has no subs.
func pickBestRCState(rc *rcResponse, now time.Time) (plan, status string, expiresAt, cancelAt *time.Time, store string) {
	if rc == nil || len(rc.Subscriber.Subscriptions) == 0 {
		return "", "none", nil, nil, ""
	}
	var bestActive, bestNonActive *rcSubscription
	var bestActiveKey, bestNonActiveKey string
	for productID, s := range rc.Subscriber.Subscriptions {
		s := s
		isActive := s.ExpiresDate != nil && s.ExpiresDate.After(now) && s.BillingIssuesDetectedAt == nil
		if isActive {
			if bestActive == nil || (s.ExpiresDate != nil && bestActive.ExpiresDate != nil && s.ExpiresDate.After(*bestActive.ExpiresDate)) {
				bestActive = &s
				bestActiveKey = productID
			}
		} else {
			if bestNonActive == nil || (s.ExpiresDate != nil && bestNonActive.ExpiresDate != nil && s.ExpiresDate.After(*bestNonActive.ExpiresDate)) {
				bestNonActive = &s
				bestNonActiveKey = productID
			}
		}
	}
	var sub *rcSubscription
	var productID string
	if bestActive != nil {
		sub = bestActive
		productID = bestActiveKey
	} else if bestNonActive != nil {
		sub = bestNonActive
		productID = bestNonActiveKey
	} else {
		return "", "none", nil, nil, ""
	}

	plan = planFromProductID(productID)
	store = strings.ToUpper(sub.Store)
	expiresAt = sub.ExpiresDate
	if sub.UnsubscribeDetectedAt != nil {
		cancelAt = sub.ExpiresDate
	}

	switch {
	case sub.ExpiresDate != nil && sub.ExpiresDate.After(now) && sub.BillingIssuesDetectedAt == nil && sub.UnsubscribeDetectedAt == nil:
		status = "active"
	case sub.ExpiresDate != nil && sub.ExpiresDate.After(now) && sub.UnsubscribeDetectedAt != nil:
		status = "canceled_in_period"
	case sub.BillingIssuesDetectedAt != nil:
		status = "billing_issue"
	default:
		status = "expired"
	}
	return
}

func planFromProductID(productID string) string {
	id := strings.ToLower(strings.TrimSpace(productID))
	if i := strings.Index(id, ":"); i >= 0 {
		id = id[:i]
	}
	if i := strings.LastIndex(id, "."); i >= 0 {
		id = id[i+1:]
	}
	for _, suffix := range []string{"_annual", "_monthly"} {
		if strings.HasSuffix(id, suffix) {
			plan := strings.TrimSuffix(id, suffix)
			switch plan {
			case "base", "premium", "premium_plus":
				return plan
			}
		}
	}
	return ""
}

func applyDowngrade(
	ctx context.Context,
	users, events *mongo.Collection,
	userOID primitive.ObjectID,
	email string,
	c candidate,
) error {
	// Snapshot the current subscription doc so the audit row is reversible.
	var pre struct {
		User struct {
			Subscription map[string]interface{} `bson:"subscription"`
		} `bson:"user"`
	}
	_ = users.FindOne(ctx, bson.M{"_id": userOID}).Decode(&pre)

	now := time.Now().UTC()
	set := bson.M{
		"user.subscription.active":    false,
		"user.subscription.plan":      "free",
		"user.subscription.isAnnual":  false,
		"user.subscription.updatedAt": primitive.NewDateTimeFromTime(now),
	}
	if c.ExpiresAt != nil {
		set["user.subscription.expirationDate"] = c.ExpiresAt.Format(time.RFC3339)
		set["user.subscription.currentPeriodEnd"] = primitive.NewDateTimeFromTime(*c.ExpiresAt)
	}
	if c.CancelAt != nil {
		set["user.subscription.cancelAt"] = primitive.NewDateTimeFromTime(*c.CancelAt)
	}
	if _, err := users.UpdateOne(ctx, bson.M{"_id": userOID}, bson.M{"$set": set}); err != nil {
		return fmt.Errorf("user update: %v", err)
	}

	// Audit row so the downgrade is permanently traceable & reversible.
	evt := bson.M{
		"userId":               userOID.Hex(),
		"userEmail":            email,
		"provider":             "admin",
		"store":                c.Store,
		"eventType":            "admin_bulk_sync",
		"plan":                 "free",
		"isAnnual":             false,
		"expiresAt":            c.ExpiresAt,
		"previousSubscription": pre.User.Subscription,
		"rawPayload":           fmt.Sprintf(`{"verdict":"%s","liveStatus":"%s","livePlan":"%s","dbPlanBefore":"%s","dbSourceBefore":"%s"}`,
			c.Verdict, c.LiveStatus, c.LivePlan, c.DBPlan, c.DBSource),
		"processingStatus": "ok",
		"createdAt":        now,
	}
	if _, err := events.InsertOne(ctx, evt); err != nil {
		// Don't fail the whole op for the audit row — log and continue.
		fmt.Fprintf(os.Stderr, "  ! audit row insert failed for %s: %v\n", userOID.Hex(), err)
	}
	return nil
}

func writeRow(w *csv.Writer, c candidate, applied bool) {
	_ = w.Write([]string{
		c.UserID,
		c.Email,
		c.DBPlan,
		c.DBSource,
		c.Verdict,
		c.LiveStatus,
		c.LivePlan,
		timeStr(c.ExpiresAt),
		timeStr(c.CancelAt),
		c.Store,
		strconv.FormatBool(c.NewActive),
		c.NewPlan,
		strconv.FormatBool(applied),
	})
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

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
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
