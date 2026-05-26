package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/linesmerrill/police-cad-api/api/scheduler"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/subscription"
	"go.uber.org/zap"
)

// stripeMigrateRequest controls the V1 → V2 price migration run.
type stripeMigrateRequest struct {
	DryRun bool `json:"dryRun"`
	Limit  int  `json:"limit,omitempty"`
}

// stripeMigrateResult is one row of the per-subscription summary.
type stripeMigrateResult struct {
	SubscriptionID string `json:"subscriptionId"`
	CustomerID     string `json:"customerId"`
	ItemID         string `json:"itemId"`
	OldPriceID     string `json:"oldPriceId"`
	NewPriceID     string `json:"newPriceId"`
	Tier           string `json:"tier"`
	Status         string `json:"status"` // "migrated" | "skipped" | "error"
	Reason         string `json:"reason,omitempty"`
	Error          string `json:"error,omitempty"`
}

// v1ToV2PriceMapping returns the lookup of V1 price IDs to their V2
// equivalents for user-tier subscriptions. Entries with empty V2 env vars
// are omitted (V2 prices not yet configured in Stripe).
func v1ToV2PriceMapping() map[string]struct {
	NewPriceID string
	Tier       string
} {
	type row struct {
		v1Env, v2Env, tier string
	}
	rows := []row{
		{"STRIPE_BASE_MONTHLY_PRICE_ID", "STRIPE_BASE_V2_MONTHLY_PRICE_ID", "base_monthly"},
		{"STRIPE_BASE_ANNUAL_PRICE_ID", "STRIPE_BASE_V2_ANNUAL_PRICE_ID", "base_annual"},
		{"STRIPE_PREMIUM_MONTHLY_PRICE_ID", "STRIPE_PREMIUM_V2_MONTHLY_PRICE_ID", "premium_monthly"},
		{"STRIPE_PREMIUM_ANNUAL_PRICE_ID", "STRIPE_PREMIUM_V2_ANNUAL_PRICE_ID", "premium_annual"},
		{"STRIPE_PREMIUM_PLUS_MONTHLY_PRICE_ID", "STRIPE_PREMIUM_PLUS_V2_MONTHLY_PRICE_ID", "premium_plus_monthly"},
		{"STRIPE_PREMIUM_PLUS_ANNUAL_PRICE_ID", "STRIPE_PREMIUM_PLUS_V2_ANNUAL_PRICE_ID", "premium_plus_annual"},
	}
	out := map[string]struct {
		NewPriceID string
		Tier       string
	}{}
	for _, r := range rows {
		v1 := os.Getenv(r.v1Env)
		v2 := os.Getenv(r.v2Env)
		if v1 == "" || v2 == "" || v1 == v2 {
			continue
		}
		out[v1] = struct {
			NewPriceID string
			Tier       string
		}{NewPriceID: v2, Tier: r.tier}
	}
	return out
}

// AdminStripeMigrateToV2Handler walks active Stripe subscriptions, finds any
// subscription items still on a V1 user-tier price, and updates them to the
// matching V2 price with proration_behavior=create_prorations. Stripe issues
// a credit on the next invoice for the unused portion of the old price minus
// the unused portion of the new price — for a price drop this is a net
// credit that automatically discounts the next renewal.
//
//	POST /api/v1/admin/subscription/stripe/migrate-to-v2
//	Body: { "dryRun": bool, "limit": int }
//	Query: ?dryRun=true (convenience override)
//
// Idempotent: subs already on V2 prices are skipped naturally because they
// don't match any V1 price ID in the lookup.
func (h Admin) AdminStripeMigrateToV2Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	rawBody, _ := io.ReadAll(r.Body)
	var req stripeMigrateRequest
	_ = json.Unmarshal(rawBody, &req)
	if v := r.URL.Query().Get("dryRun"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			req.DryRun = b
		}
	}
	if req.Limit <= 0 {
		req.Limit = 200
	}

	mapping := v1ToV2PriceMapping()
	if len(mapping) == 0 {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":    "no V1 → V2 price mappings configured (set STRIPE_*_V2_*_PRICE_ID env vars)",
			"results":  []stripeMigrateResult{},
			"migrated": 0,
		})
		return
	}

	results := []stripeMigrateResult{}
	migrated := 0
	skipped := 0
	scanned := 0

	listParams := &stripe.SubscriptionListParams{
		Status: stripe.String("active"),
	}
	listParams.Limit = stripe.Int64(int64(req.Limit))
	iter := subscription.List(listParams)
	for iter.Next() {
		sub := iter.Subscription()
		scanned++
		if sub == nil || sub.Items == nil {
			continue
		}
		for _, item := range sub.Items.Data {
			if item == nil || item.Price == nil {
				continue
			}
			oldPriceID := item.Price.ID
			mapped, ok := mapping[oldPriceID]
			if !ok {
				continue
			}

			result := stripeMigrateResult{
				SubscriptionID: sub.ID,
				CustomerID:     subCustomerID(sub),
				ItemID:         item.ID,
				OldPriceID:     oldPriceID,
				NewPriceID:     mapped.NewPriceID,
				Tier:           mapped.Tier,
			}

			if req.DryRun {
				result.Status = "skipped"
				result.Reason = "dry-run"
				results = append(results, result)
				skipped++
				continue
			}

			updateParams := &stripe.SubscriptionParams{
				Items: []*stripe.SubscriptionItemsParams{{
					ID:    stripe.String(item.ID),
					Price: stripe.String(mapped.NewPriceID),
				}},
				ProrationBehavior: stripe.String("create_prorations"),
			}
			if _, err := subscription.Update(sub.ID, updateParams); err != nil {
				result.Status = "error"
				result.Error = err.Error()
				zap.S().Warnw("stripe migrate: update failed", "subId", sub.ID, "error", err)
				scheduler.SendCronAlert("", "migrateStripeSubscriptionsToV2", err, map[string]string{
					"subId": sub.ID,
					"tier":  mapped.Tier,
				})
				results = append(results, result)
				continue
			}
			result.Status = "migrated"
			results = append(results, result)
			migrated++
		}
	}
	if err := iter.Err(); err != nil {
		zap.S().Errorw("stripe migrate: list iteration failed", "error", err)
		scheduler.SendCronAlert("", "migrateStripeSubscriptionsToV2", err, map[string]string{"phase": "list"})
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"dryRun":       req.DryRun,
		"limit":        req.Limit,
		"scannedSubs":  scanned,
		"migrated":     migrated,
		"skipped":      skipped,
		"mappingCount": len(mapping),
		"finishedAt":   time.Now().UTC().Format(time.RFC3339),
		"results":      results,
	})
}

// subCustomerID extracts the customer ID from a Stripe subscription. The
// customer field can be an embedded object or a string ID depending on
// expand parameters — the SDK exposes it as *Customer with an ID field.
func subCustomerID(sub *stripe.Subscription) string {
	if sub == nil || sub.Customer == nil {
		return ""
	}
	return sub.Customer.ID
}
