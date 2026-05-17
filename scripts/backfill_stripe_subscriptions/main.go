// One-shot backfill: synthesize `subscription_events` rows for every Stripe
// subscription tied to a user in our Mongo database. Companion to
// scripts/backfill_subscription_events (which handles RevenueCat).
//
// Iterates every user with user.subscription.stripeCustomerId set, lists
// all Stripe subscriptions for that customer (including canceled — so we
// get historical state, not just active), and writes one row per
// subscription into subscription_events. Idempotent via
// providerEventId=backfill:stripe:<sub_id> + the unique index.
//
// Backfilled rows are clearly labelled:
//
//	provider:        "stripe"
//	store:           "STRIPE"
//	eventType:       "BACKFILL"
//	providerEventId: "backfill:stripe:<stripe_subscription_id>"
//
// READ-ONLY against Stripe. Only writes to the subscription_events
// collection. Never modifies the User document or any other state.
//
// Plan / isAnnual are derived from the Stripe price id by looking up the
// same STRIPE_*_PRICE_ID env vars the API uses. Subscriptions whose
// price id isn't mapped (e.g. retired promotion prices) still get a row
// with plan="" — they're still discoverable via the admin search.
//
// Usage:
//
//	DB_URI=mongodb+srv://... \
//	DB_NAME=lpc \
//	STRIPE_SECRET_KEY=sk_live_xxx \
//	STRIPE_BASE_MONTHLY_PRICE_ID=price_xxx \
//	STRIPE_BASE_ANNUAL_PRICE_ID=price_xxx \
//	STRIPE_PREMIUM_MONTHLY_PRICE_ID=price_xxx \
//	STRIPE_PREMIUM_ANNUAL_PRICE_ID=price_xxx \
//	STRIPE_PREMIUM_PLUS_MONTHLY_PRICE_ID=price_xxx \
//	STRIPE_PREMIUM_PLUS_ANNUAL_PRICE_ID=price_xxx \
//	go run ./scripts/backfill_stripe_subscriptions
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/subscription"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	mongoURI := mustEnv("DB_URI")
	dbName := mustEnv("DB_NAME")
	stripe.Key = mustEnv("STRIPE_SECRET_KEY")

	priceMap := buildPriceMap()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		die("connect mongo: %v", err)
	}
	defer client.Disconnect(ctx)

	users := client.Database(dbName).Collection("users")
	events := client.Database(dbName).Collection("subscription_events")

	// Only users we've ever associated with a Stripe customer. Anyone
	// without stripeCustomerId either paid via RC (handled by the other
	// backfill script) or never paid.
	filter := bson.M{
		"user.subscription.stripeCustomerId": bson.M{"$nin": []interface{}{"", nil}},
	}

	cursor, err := users.Find(ctx, filter, options.Find().SetBatchSize(200))
	if err != nil {
		die("query users: %v", err)
	}
	defer cursor.Close(ctx)

	scanned, backfilled, skippedDup, stripeMisses, stripeErrors := 0, 0, 0, 0, 0

	for cursor.Next(ctx) {
		scanned++
		var doc struct {
			ID   string `bson:"_id"`
			User struct {
				Email        string `bson:"email"`
				Subscription struct {
					StripeCustomerID string `bson:"stripeCustomerId"`
				} `bson:"subscription"`
			} `bson:"user"`
		}
		if err := cursor.Decode(&doc); err != nil {
			continue
		}
		customerID := doc.User.Subscription.StripeCustomerID
		if customerID == "" {
			continue
		}

		subs, err := listAllCustomerSubscriptions(customerID)
		if err != nil {
			stripeErrors++
			fmt.Fprintf(os.Stderr, "  ! Stripe list failed for %s (%s, customer=%s): %v\n",
				doc.ID, doc.User.Email, customerID, err)
			continue
		}
		if len(subs) == 0 {
			stripeMisses++
			continue
		}

		for _, sub := range subs {
			rawPayload, _ := json.Marshal(sub)
			priceID, plan, isAnnual, priceUSD, currency, productID := extractPriceInfo(sub, priceMap)

			row := bson.M{
				"providerEventId":  fmt.Sprintf("backfill:stripe:%s", sub.ID),
				"userId":           doc.ID,
				"userEmail":        doc.User.Email,
				"provider":         "stripe",
				"store":            "STRIPE",
				"eventType":        "BACKFILL",
				"plan":             plan,
				"isAnnual":         isAnnual,
				"productId":        productID,
				"transactionId":    sub.ID,
				"priceUsd":         priceUSD,
				"currency":         currency,
				"purchasedAt":      unixToTimePtr(sub.StartDate),
				"expiresAt":        currentPeriodEndPtr(sub),
				"environment":      stripeLiveModeEnv(sub.Livemode),
				"rawPayload":       string(rawPayload),
				"processingStatus": "ok",
				"createdAt":        time.Now().UTC(),
			}
			// Avoid storing zero priceUsd as 0 — leave omitted if we can't derive it.
			if priceUSD == 0 {
				delete(row, "priceUsd")
			}

			if _, err := events.InsertOne(ctx, row); err != nil {
				if mongo.IsDuplicateKeyError(err) {
					skippedDup++
					continue
				}
				fmt.Fprintf(os.Stderr, "  ! insert failed for %s/%s: %v\n", doc.ID, sub.ID, err)
				continue
			}
			backfilled++
			fmt.Printf("BACKFILLED %s (%s) — STRIPE %s [%s, status=%s, price=%s]\n",
				doc.ID, doc.User.Email, sub.ID, plan, sub.Status, priceID)
		}

		if scanned%50 == 0 {
			fmt.Printf("... scanned %d users, backfilled %d rows, skipped %d dup, %d no Stripe subs, %d Stripe errors\n",
				scanned, backfilled, skippedDup, stripeMisses, stripeErrors)
		}
	}
	if err := cursor.Err(); err != nil {
		die("cursor: %v", err)
	}
	fmt.Printf("Done. Scanned %d Stripe customers, backfilled %d rows, %d already-backfilled, %d customers had no Stripe subs, %d Stripe errors.\n",
		scanned, backfilled, skippedDup, stripeMisses, stripeErrors)
}

// listAllCustomerSubscriptions returns every Stripe subscription for a
// customer including canceled ones (Stripe's default omits canceled).
// Pagination is handled by the iterator.
func listAllCustomerSubscriptions(customerID string) ([]*stripe.Subscription, error) {
	params := &stripe.SubscriptionListParams{
		Customer: stripe.String(customerID),
		Status:   stripe.String("all"),
	}
	params.Limit = stripe.Int64(100)
	iter := subscription.List(params)
	var out []*stripe.Subscription
	for iter.Next() {
		out = append(out, iter.Subscription())
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// extractPriceInfo derives the plan / cadence / price metadata from a
// Stripe subscription. Returns the price id even when the plan is empty
// so the log line can surface unknown price ids for follow-up.
func extractPriceInfo(sub *stripe.Subscription, priceMap map[string]priceInfo) (
	priceID, plan string, isAnnual bool, priceUSD float64, currency, productID string,
) {
	if sub == nil || sub.Items == nil || len(sub.Items.Data) == 0 || sub.Items.Data[0].Price == nil {
		return "", "", false, 0, "", ""
	}
	p := sub.Items.Data[0].Price
	priceID = p.ID
	currency = string(p.Currency)
	if p.UnitAmount > 0 {
		priceUSD = float64(p.UnitAmount) / 100.0
	}
	if p.Product != nil {
		productID = p.Product.ID
	}
	if info, ok := priceMap[priceID]; ok {
		plan = info.plan
		isAnnual = info.isAnnual
		return
	}
	// Unmapped price id — fall back to recurring interval for isAnnual
	// and leave plan empty. Still useful for searchability.
	if p.Recurring != nil && p.Recurring.Interval == "year" {
		isAnnual = true
	}
	return
}

type priceInfo struct {
	plan     string
	isAnnual bool
}

// buildPriceMap reads the same env vars the API uses to map Stripe price
// ids to plans. Missing env vars are silently skipped — the script still
// runs, those subs just won't have plan populated.
func buildPriceMap() map[string]priceInfo {
	m := map[string]priceInfo{}
	add := func(envVar, plan string, isAnnual bool) {
		if v := os.Getenv(envVar); v != "" {
			m[v] = priceInfo{plan: plan, isAnnual: isAnnual}
		}
	}
	add("STRIPE_BASE_MONTHLY_PRICE_ID", "base", false)
	add("STRIPE_BASE_ANNUAL_PRICE_ID", "base", true)
	add("STRIPE_PREMIUM_MONTHLY_PRICE_ID", "premium", false)
	add("STRIPE_PREMIUM_ANNUAL_PRICE_ID", "premium", true)
	add("STRIPE_PREMIUM_PLUS_MONTHLY_PRICE_ID", "premium_plus", false)
	add("STRIPE_PREMIUM_PLUS_ANNUAL_PRICE_ID", "premium_plus", true)
	// Promotion prices map to the same plan tiers but the names differ.
	add("STRIPE_BASIC_PROMOTION_MONTHLY_PRICE_ID", "base", false)
	add("STRIPE_STANDARD_PROMOTION_MONTHLY_PRICE_ID", "premium", false)
	add("STRIPE_PREMIUM_PROMOTION_MONTHLY_PRICE_ID", "premium", false)
	add("STRIPE_ELITE_PROMOTION_MONTHLY_PRICE_ID", "premium_plus", false)
	if len(m) == 0 {
		fmt.Fprintln(os.Stderr, "WARNING: no STRIPE_*_PRICE_ID env vars set — every row will have plan=\"\"")
	}
	return m
}

func unixToTimePtr(unix int64) *time.Time {
	if unix == 0 {
		return nil
	}
	t := time.Unix(unix, 0).UTC()
	return &t
}

// currentPeriodEndPtr handles both old (top-level CurrentPeriodEnd) and
// newer (per-item billing_cycle_anchor) shapes from the Stripe SDK
// without panicking on unexpected nils.
func currentPeriodEndPtr(sub *stripe.Subscription) *time.Time {
	if sub == nil {
		return nil
	}
	if sub.CancelAt > 0 {
		t := time.Unix(sub.CancelAt, 0).UTC()
		return &t
	}
	if sub.Items != nil && len(sub.Items.Data) > 0 && sub.Items.Data[0].CurrentPeriodEnd > 0 {
		t := time.Unix(sub.Items.Data[0].CurrentPeriodEnd, 0).UTC()
		return &t
	}
	return nil
}

func stripeLiveModeEnv(livemode bool) string {
	if livemode {
		return "PRODUCTION"
	}
	return "SANDBOX"
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
