package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/linesmerrill/police-cad-api/api/scheduler"
	"github.com/linesmerrill/police-cad-api/models"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/subscription"
	"go.mongodb.org/mongo-driver/bson"
	"go.uber.org/zap"
)

// stripeMigrateRequest controls the V1 → V2 price migration run.
type stripeMigrateRequest struct {
	DryRun    bool `json:"dryRun"`
	Limit     int  `json:"limit,omitempty"`
	SendEmail bool `json:"sendEmail,omitempty"`
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
	EmailSent      bool   `json:"emailSent,omitempty"`
	EmailRecipient string `json:"emailRecipient,omitempty"`
}

// priceDropTierInfo holds the per-tier display copy used in the price-drop
// announcement email. Keys match the tier names in v1ToV2PriceMapping.
type priceDropTierInfo struct {
	PlanName     string // "Premium Plus"
	Interval     string // "monthly" | "annual"
	NewPriceText string // "$9.99/month"
	OldPriceText string // "$19.99/month"
}

var priceDropEmailTierInfo = map[string]priceDropTierInfo{
	"base_monthly":         {"Base", "monthly", "$2/month", "$3/month"},
	"base_annual":          {"Base", "annual", "$20/year", "$32/year"},
	"premium_monthly":      {"Premium", "monthly", "$5/month", "$8/month"},
	"premium_annual":       {"Premium", "annual", "$50/year", "$85/year"},
	"premium_plus_monthly": {"Premium Plus", "monthly", "$9.99/month", "$19.99/month"},
	"premium_plus_annual":  {"Premium Plus", "annual", "$99/year", "$209/year"},
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
	if v := r.URL.Query().Get("sendEmail"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			req.SendEmail = b
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
			migrated++

			if req.SendEmail {
				if email, username, ok := h.lookupStripeCustomerEmail(r, result.CustomerID); ok {
					if info, infoOK := priceDropEmailTierInfo[mapped.Tier]; infoOK {
						sendPriceDropAnnouncementEmailAsync(email, username, info)
						result.EmailSent = true
						result.EmailRecipient = email
					} else {
						zap.S().Warnw("stripe migrate: no email copy for tier", "tier", mapped.Tier, "subId", sub.ID)
					}
				} else {
					zap.S().Warnw("stripe migrate: could not look up customer email", "customerId", result.CustomerID, "subId", sub.ID)
				}
			}
			results = append(results, result)
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

// AdminStripeMigrateTestEmailHandler sends a single price-drop announcement
// email to a specified recipient without touching Stripe. Used to preview
// the email contents and deliverability before running the live migration.
//
//	POST /api/v1/admin/subscription/stripe/migrate-to-v2/test-email
//	Body: { "email": "you@example.com", "username": "yourname", "tier": "premium_plus_monthly" }
//
// `tier` must be one of the keys in priceDropEmailTierInfo. Returns the
// resolved tier copy alongside the recipient so you can sanity-check.
func (h Admin) AdminStripeMigrateTestEmailHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	rawBody, _ := io.ReadAll(r.Body)
	var req struct {
		Email    string `json:"email"`
		Username string `json:"username"`
		Tier     string `json:"tier"`
	}
	if err := json.Unmarshal(rawBody, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON body"})
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	req.Tier = strings.TrimSpace(req.Tier)
	if req.Email == "" || req.Tier == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "email and tier are required"})
		return
	}

	info, ok := priceDropEmailTierInfo[req.Tier]
	if !ok {
		validTiers := make([]string, 0, len(priceDropEmailTierInfo))
		for k := range priceDropEmailTierInfo {
			validTiers = append(validTiers, k)
		}
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":      "unknown tier",
			"validTiers": validTiers,
		})
		return
	}

	sendPriceDropAnnouncementEmailAsync(req.Email, req.Username, info)

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":         true,
		"email":      req.Email,
		"username":   req.Username,
		"tier":       req.Tier,
		"planName":   info.PlanName,
		"interval":   info.Interval,
		"newPrice":   info.NewPriceText,
		"oldPrice":   info.OldPriceText,
		"note":       "Email queued via SendGrid (fire-and-forget). Delivery typically within seconds.",
		"finishedAt": time.Now().UTC().Format(time.RFC3339),
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

// lookupStripeCustomerEmail finds the user document for a given Stripe
// customer ID and returns their email + username. Returns ok=false if no
// matching user is found or the email is empty.
func (h Admin) lookupStripeCustomerEmail(r *http.Request, customerID string) (email, username string, ok bool) {
	if customerID == "" || h.UDB == nil {
		return "", "", false
	}
	var user models.User
	filter := bson.M{"user.subscription.stripeCustomerId": customerID}
	if err := h.UDB.FindOne(r.Context(), filter).Decode(&user); err != nil {
		return "", "", false
	}
	if user.Details.Email == "" {
		return "", "", false
	}
	return user.Details.Email, user.Details.Username, true
}

// sendPriceDropAnnouncementEmailAsync notifies a migrated Stripe customer
// that their subscription price has been lowered. Sent fire-and-forget via
// SendGrid through the package's existing sensitiveEmail helper.
func sendPriceDropAnnouncementEmailAsync(email, username string, info priceDropTierInfo) {
	name := strings.TrimSpace(username)
	if name == "" {
		name = "there"
	}
	subject := "Your Lines Police CAD subscription price just dropped"
	plain := fmt.Sprintf(`Hi %s,

Just a heads-up: we lowered our subscription prices today, and since you're already a customer, your plan automatically goes to the new lower rate.

  Your plan: %s (%s)
  New price: %s — down from %s

You'll see a prorated credit on your next Stripe invoice for the unused portion of this month at the old price, so your next renewal will be close to free.

We know everything seems to keep getting more expensive lately. We wanted to do the opposite — and since you've been here supporting us, we wanted to make sure you got the new pricing too, not just new sign-ups.

Thanks for using Lines Police CAD.

— The Lines Police CAD Team`, name, info.PlanName, info.Interval, info.NewPriceText, info.OldPriceText)
	html := fmt.Sprintf(`<div style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;max-width:560px;margin:0 auto;padding:24px;color:#0f172a;line-height:1.6;">
<p>Hi %s,</p>
<p>Just a heads-up: we lowered our subscription prices today, and since you're already a customer, your plan automatically goes to the new lower rate.</p>
<div style="background:#f1f5f9;border-left:3px solid #38bdf8;padding:16px 20px;border-radius:4px;margin:20px 0;">
  <div style="margin-bottom:6px;">Your plan: <strong>%s</strong> (%s)</div>
  <div>New price: <strong style="color:#0ea5e9;">%s</strong> &nbsp;<span style="text-decoration:line-through;color:#64748b;">%s</span></div>
</div>
<p>You'll see a prorated credit on your next Stripe invoice for the unused portion of this month at the old price, so your next renewal will be close to free.</p>
<p>We know everything seems to keep getting more expensive lately. We wanted to do the opposite — and since you've been here supporting us, we wanted to make sure you got the new pricing too, not just new sign-ups.</p>
<p>Thanks for using Lines Police CAD.</p>
<p style="margin-top:32px;">— The Lines Police CAD Team</p>
</div>`, name, info.PlanName, info.Interval, info.NewPriceText, info.OldPriceText)
	sendSensitiveEmailAsync(sensitiveEmail{
		To:        email,
		Subject:   subject,
		PlainText: plain,
		HTML:      html,
	})
}
