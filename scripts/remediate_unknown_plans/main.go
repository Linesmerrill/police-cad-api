// One-off remediation: repair users whose subscription.plan was written as
// "unknown" by the (now-fixed) Stripe checkout handler. Before the fix, a
// V1-only inline price→plan switch in handleCheckoutSessionCompleted fell
// through to plan="unknown" for any V2 (post-price-drop) price id — e.g. the
// $9.99 Premium Plus monthly price. This restores the correct plan from the
// authoritative live Stripe subscription.
//
// For every user with user.subscription.plan == "unknown":
//   - If they have a stripeCustomerId, list their live Stripe subscriptions,
//     pick the most relevant one (active first, else most recent), and derive
//     the plan/cadence from the same STRIPE_*_PRICE_ID env vars the API uses
//     (V1 *and* V2 ids).
//   - In --apply mode, update user.subscription.{plan,isAnnual} and write an
//     audit row into subscription_events (eventType=REMEDIATE_UNKNOWN_PLAN,
//     idempotent via providerEventId).
//
// READ-ONLY by default. Pass --apply to actually write. Users without a Stripe
// customer id, or whose live price id still doesn't map to a known plan, are
// reported and left untouched (never blindly downgraded).
//
// Usage:
//
//	DB_URI=mongodb+srv://... \
//	DB_NAME=lpc \
//	STRIPE_SECRET_KEY=sk_live_xxx \
//	STRIPE_PREMIUM_PLUS_V2_MONTHLY_PRICE_ID=price_xxx  (+ the other STRIPE_*_PRICE_ID vars) \
//	go run ./scripts/remediate_unknown_plans            # dry-run preview
//	go run ./scripts/remediate_unknown_plans --apply    # write changes
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/subscription"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	dryRun := flag.Bool("dry-run", true, "preview only — no DB writes (default true)")
	apply := flag.Bool("apply", false, "actually write changes (overrides --dry-run)")
	limit := flag.Int("limit", 500, "max users to process this run (safety cap)")
	flag.Parse()
	if *apply {
		*dryRun = false
	}

	mongoURI := mustEnv("DB_URI")
	dbName := mustEnv("DB_NAME")
	stripe.Key = mustEnv("STRIPE_SECRET_KEY")

	priceMap := buildPriceMap()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		die("connect mongo: %v", err)
	}
	defer client.Disconnect(ctx)

	users := client.Database(dbName).Collection("users")
	events := client.Database(dbName).Collection("subscription_events")

	mode := "DRY-RUN (no writes)"
	if !*dryRun {
		mode = "APPLY (writing changes)"
	}
	fmt.Printf("remediate_unknown_plans — %s, limit=%d\n", mode, *limit)

	// Every user whose stored plan is the "unknown" sentinel.
	filter := bson.M{"user.subscription.plan": "unknown"}
	cursor, err := users.Find(ctx, filter, options.Find().SetBatchSize(200))
	if err != nil {
		die("query users: %v", err)
	}
	defer cursor.Close(ctx)

	scanned, fixed, noCustomer, stillUnmapped, noActiveSub, stripeErrors := 0, 0, 0, 0, 0, 0

	for cursor.Next(ctx) {
		if scanned >= *limit {
			fmt.Printf("Reached --limit=%d; stopping.\n", *limit)
			break
		}
		scanned++

		var doc struct {
			ID   primitive.ObjectID `bson:"_id"`
			User struct {
				Email        string `bson:"email"`
				Subscription struct {
					StripeCustomerID string `bson:"stripeCustomerId"`
					Plan             string `bson:"plan"`
				} `bson:"subscription"`
			} `bson:"user"`
		}
		if err := cursor.Decode(&doc); err != nil {
			continue
		}
		userID := doc.ID.Hex()

		customerID := doc.User.Subscription.StripeCustomerID
		if customerID == "" {
			noCustomer++
			fmt.Printf("SKIP    %s (%s) — plan=unknown but no stripeCustomerId (not a Stripe sub)\n", userID, doc.User.Email)
			continue
		}

		subs, err := listAllCustomerSubscriptions(customerID)
		if err != nil {
			stripeErrors++
			fmt.Fprintf(os.Stderr, "  ! Stripe list failed for %s (%s, customer=%s): %v\n", userID, doc.User.Email, customerID, err)
			continue
		}

		sub := pickRelevantSub(subs)
		if sub == nil {
			noActiveSub++
			fmt.Printf("SKIP    %s (%s) — no Stripe subscription found for customer %s\n", userID, doc.User.Email, customerID)
			continue
		}

		priceID, plan, isAnnual := derivePlan(sub, priceMap)
		if plan == "" {
			stillUnmapped++
			fmt.Printf("UNMAPPED %s (%s) — price id %q has no STRIPE_*_PRICE_ID env mapping (status=%s); left as unknown\n",
				userID, doc.User.Email, priceID, sub.Status)
			continue
		}

		fmt.Printf("FIX     %s (%s) — unknown -> %s (annual=%v) from sub %s [status=%s, price=%s]\n",
			userID, doc.User.Email, plan, isAnnual, sub.ID, sub.Status, priceID)

		if *dryRun {
			continue
		}

		update := bson.M{"$set": bson.M{
			"user.subscription.plan":      plan,
			"user.subscription.isAnnual":  isAnnual,
			"user.subscription.updatedAt": time.Now().UTC(),
		}}
		if _, err := users.UpdateOne(ctx, bson.M{"_id": doc.ID}, update); err != nil {
			fmt.Fprintf(os.Stderr, "  ! update failed for %s: %v\n", userID, err)
			continue
		}

		// Idempotent audit row — re-running won't duplicate it.
		raw, _ := json.Marshal(sub)
		evt := bson.M{
			"providerEventId":  fmt.Sprintf("remediate:unknown-plan:%s", sub.ID),
			"userId":           userID,
			"userEmail":        doc.User.Email,
			"provider":         "admin",
			"store":            "STRIPE",
			"eventType":        "REMEDIATE_UNKNOWN_PLAN",
			"plan":             plan,
			"isAnnual":         isAnnual,
			"transactionId":    sub.ID,
			"rawPayload":       string(raw),
			"processingStatus": "ok",
			"createdAt":        time.Now().UTC(),
		}
		if _, err := events.InsertOne(ctx, evt); err != nil && !mongo.IsDuplicateKeyError(err) {
			fmt.Fprintf(os.Stderr, "  ! audit insert failed for %s/%s: %v\n", userID, sub.ID, err)
		}
		fixed++
	}
	if err := cursor.Err(); err != nil {
		die("cursor: %v", err)
	}

	fmt.Printf("\nDone. Scanned %d unknown-plan users: %d fixed, %d no Stripe customer, %d still-unmapped price, %d no Stripe sub, %d Stripe errors.\n",
		scanned, fixed, noCustomer, stillUnmapped, noActiveSub, stripeErrors)
	if *dryRun {
		fmt.Println("(dry-run — re-run with --apply to write the FIX rows above.)")
	}
}

// pickRelevantSub chooses the subscription that best reflects the user's
// current entitlement: the first active one, otherwise the most recently
// started. Returns nil when the customer has no subscriptions.
func pickRelevantSub(subs []*stripe.Subscription) *stripe.Subscription {
	var best *stripe.Subscription
	for _, s := range subs {
		if s == nil {
			continue
		}
		if s.Status == stripe.SubscriptionStatusActive || s.Status == stripe.SubscriptionStatusTrialing {
			return s
		}
		if best == nil || s.StartDate > best.StartDate {
			best = s
		}
	}
	return best
}

// derivePlan resolves the plan/cadence from the subscription's price id using
// the V1+V2-aware price map. Returns ("", false) when the price id is unmapped
// so the caller can leave the record untouched rather than guess.
func derivePlan(sub *stripe.Subscription, priceMap map[string]priceInfo) (priceID, plan string, isAnnual bool) {
	if sub == nil || sub.Items == nil || len(sub.Items.Data) == 0 || sub.Items.Data[0].Price == nil {
		return "", "", false
	}
	priceID = sub.Items.Data[0].Price.ID
	if info, ok := priceMap[priceID]; ok {
		return priceID, info.plan, info.isAnnual
	}
	return priceID, "", false
}

type priceInfo struct {
	plan     string
	isAnnual bool
}

// buildPriceMap reads the same env vars the API uses, including the V2
// (post-price-drop) ids — the absence of which caused the original bug.
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
	// V2 (post-price-drop) ids — the ones the buggy checkout switch missed.
	add("STRIPE_BASE_V2_MONTHLY_PRICE_ID", "base", false)
	add("STRIPE_BASE_V2_ANNUAL_PRICE_ID", "base", true)
	add("STRIPE_PREMIUM_V2_MONTHLY_PRICE_ID", "premium", false)
	add("STRIPE_PREMIUM_V2_ANNUAL_PRICE_ID", "premium", true)
	add("STRIPE_PREMIUM_PLUS_V2_MONTHLY_PRICE_ID", "premium_plus", false)
	add("STRIPE_PREMIUM_PLUS_V2_ANNUAL_PRICE_ID", "premium_plus", true)
	if len(m) == 0 {
		fmt.Fprintln(os.Stderr, "WARNING: no STRIPE_*_PRICE_ID env vars set — nothing can be remediated")
	}
	return m
}

// listAllCustomerSubscriptions returns every Stripe subscription for a
// customer including canceled ones (Stripe's default omits canceled).
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
