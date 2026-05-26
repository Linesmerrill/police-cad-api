package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/linesmerrill/police-cad-api/api/scheduler"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
)

// kickbackNewMonthlyPriceUSD is the post-price-drop monthly price per user tier,
// used to compute "free months" credit for app-store-billed users who paid the
// old higher prices within the lookback window. See tasks/todo-price-drop.md.
var kickbackNewMonthlyPriceUSD = map[string]float64{
	"base":         2.00,
	"premium":      5.00,
	"premium_plus": 9.99,
}

const (
	defaultKickbackLookbackDays = 30
	defaultKickbackMonthCap     = 12
	kickbackBannerTTLDays       = 30
)

// kickbackRequest controls the kickback application run.
type kickbackRequest struct {
	DryRun       bool `json:"dryRun"`
	LookbackDays int  `json:"lookbackDays,omitempty"`
	MonthCap     int  `json:"monthCap,omitempty"`
}

// kickbackUserResult is one row of the per-user summary returned by the handler.
type kickbackUserResult struct {
	UserID         string  `json:"userId"`
	UserEmail      string  `json:"userEmail,omitempty"`
	Tier           string  `json:"tier"`
	TotalPaidUSD   float64 `json:"totalPaidUsd"`
	MonthsGranted  int     `json:"monthsGranted"`
	EventCount     int     `json:"eventCount"`
	PreviousExpiry string  `json:"previousExpiry,omitempty"`
	NewExpiry      string  `json:"newExpiry"`
	EmailSent      bool    `json:"emailSent,omitempty"`
	Error          string  `json:"error,omitempty"`
}

// kickbackPendingEvent is the small projection we keep in memory per
// eligible event while grouping by user.
type kickbackPendingEvent struct {
	ID       primitive.ObjectID
	Tier     string
	PriceUSD float64
}

// AdminKickbackApplyHandler scans recently-purchased non-Stripe subscription
// events and credits each affected user with "free time" equal to
// floor(amount_paid / new_monthly_price) months (summed across their events,
// capped at MonthCap). Stripe-billed users are intentionally excluded because
// they receive a Stripe proration credit via the V2 migration instead.
//
//	POST /api/v1/admin/subscription/kickback/apply
//	Body: { "dryRun": bool, "lookbackDays": int, "monthCap": int }
//	Query: ?dryRun=true (convenience override)
//
// Idempotent: each event row is marked kickbackApplied=true once processed,
// so re-running the endpoint will not double-credit.
func (h Admin) AdminKickbackApplyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	rawBody, _ := io.ReadAll(r.Body)
	var req kickbackRequest
	_ = json.Unmarshal(rawBody, &req)
	if v := r.URL.Query().Get("dryRun"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			req.DryRun = b
		}
	}
	if req.LookbackDays <= 0 {
		req.LookbackDays = defaultKickbackLookbackDays
	}
	if req.MonthCap <= 0 {
		req.MonthCap = defaultKickbackMonthCap
	}

	cutoff := time.Now().UTC().Add(-time.Duration(req.LookbackDays) * 24 * time.Hour)

	// Eligible events: app-store-billed (not Stripe, not admin), within window,
	// not yet credited, with a positive USD amount.
	filter := bson.M{
		"purchasedAt":     bson.M{"$gte": cutoff},
		"provider":        bson.M{"$nin": []string{"stripe", "admin"}},
		"eventType":       bson.M{"$in": []string{"INITIAL_PURCHASE", "RENEWAL", "mobile_subscribe"}},
		"kickbackApplied": bson.M{"$ne": true},
		"priceUsd":        bson.M{"$gt": 0},
	}

	cursor, err := h.SEDB.Find(r.Context(), filter)
	if err != nil {
		zap.S().Errorw("kickback: find events failed", "error", err)
		scheduler.SendCronAlert("", "applyPriceDropKickback", err, map[string]string{"phase": "find"})
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to query events"})
		return
	}
	defer cursor.Close(r.Context())

	byUser := map[string][]kickbackPendingEvent{}
	userEmails := map[string]string{}
	for cursor.Next(r.Context()) {
		var evt models.SubscriptionEvent
		// DecodeCurrent avoids the All() footgun in the wrapper.
		if err := cursor.DecodeCurrent(&evt); err != nil {
			zap.S().Warnw("kickback: decode event failed", "error", err)
			continue
		}
		if evt.UserID == "" || evt.PriceUSD <= 0 {
			continue
		}
		tier := strings.ToLower(evt.Plan)
		if _, ok := kickbackNewMonthlyPriceUSD[tier]; !ok {
			continue
		}
		byUser[evt.UserID] = append(byUser[evt.UserID], kickbackPendingEvent{
			ID:       evt.ID,
			Tier:     tier,
			PriceUSD: evt.PriceUSD,
		})
		if evt.UserEmail != "" {
			userEmails[evt.UserID] = evt.UserEmail
		}
	}

	results := make([]kickbackUserResult, 0, len(byUser))
	for userID, events := range byUser {
		totalMonths := 0
		totalPaid := 0.0
		primaryTier := ""
		for _, e := range events {
			mPrice := kickbackNewMonthlyPriceUSD[e.Tier]
			months := int(math.Floor(e.PriceUSD / mPrice))
			if months <= 0 {
				continue
			}
			totalMonths += months
			totalPaid += e.PriceUSD
			if primaryTier == "" {
				primaryTier = e.Tier
			}
		}
		if totalMonths > req.MonthCap {
			totalMonths = req.MonthCap
		}
		if totalMonths <= 0 {
			continue
		}

		uID, err := primitive.ObjectIDFromHex(userID)
		if err != nil {
			results = append(results, kickbackUserResult{UserID: userID, Error: "invalid user id"})
			continue
		}
		var user models.User
		if err := h.UDB.FindOne(r.Context(), bson.M{"_id": uID}).Decode(&user); err != nil {
			results = append(results, kickbackUserResult{UserID: userID, Error: "user not found"})
			continue
		}

		now := time.Now().UTC()
		baseDate := now
		prevExpiryStr := user.Details.Subscription.ExpirationDate
		if prevExpiryStr != "" {
			if t, err := time.Parse(time.RFC3339, prevExpiryStr); err == nil && t.After(now) {
				baseDate = t
			}
		}
		newExpiry := baseDate.AddDate(0, totalMonths, 0)

		result := kickbackUserResult{
			UserID:         user.ID,
			UserEmail:      user.Details.Email,
			Tier:           primaryTier,
			TotalPaidUSD:   round2(totalPaid),
			MonthsGranted:  totalMonths,
			EventCount:     len(events),
			PreviousExpiry: prevExpiryStr,
			NewExpiry:      newExpiry.Format(time.RFC3339),
		}

		if req.DryRun {
			results = append(results, result)
			continue
		}

		bannerExpiry := now.Add(time.Duration(kickbackBannerTTLDays) * 24 * time.Hour)
		set := bson.M{
			"user.subscription.expirationDate":   newExpiry.Format(time.RFC3339),
			"user.subscription.currentPeriodEnd": primitive.NewDateTimeFromTime(newExpiry),
			"user.subscription.updatedAt":        primitive.NewDateTimeFromTime(now),
			"user.subscription.kickbackBanner": bson.M{
				"months":         totalMonths,
				"originalAmount": round2(totalPaid),
				"expiresAt":      bannerExpiry.Format(time.RFC3339),
				"createdAt":      now.Format(time.RFC3339),
			},
		}
		if _, err := h.UDB.UpdateOne(r.Context(), bson.M{"_id": uID}, bson.M{"$set": set}); err != nil {
			result.Error = "update failed: " + err.Error()
			scheduler.SendCronAlert("", "applyPriceDropKickback", err, map[string]string{
				"userId": userID,
				"months": fmt.Sprintf("%d", totalMonths),
			})
			results = append(results, result)
			continue
		}

		for _, e := range events {
			update := bson.M{"$set": bson.M{
				"kickbackApplied":       true,
				"kickbackMonthsGranted": totalMonths,
				"kickbackAppliedAt":     now,
			}}
			if err := h.SEDB.UpdateOne(r.Context(), bson.M{"_id": e.ID}, update); err != nil {
				zap.S().Warnw("kickback: failed to mark event applied", "eventId", e.ID.Hex(), "error", err)
			}
		}

		if user.Details.Email != "" {
			sendKickbackEmailAsync(user.Details.Email, user.Details.Username, totalMonths, newExpiry)
			result.EmailSent = true
		}
		results = append(results, result)
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"dryRun":          req.DryRun,
		"lookbackDays":    req.LookbackDays,
		"monthCap":        req.MonthCap,
		"cutoff":          cutoff.Format(time.RFC3339),
		"usersConsidered": len(byUser),
		"usersCredited":   len(results),
		"results":         results,
	})
}

// sendKickbackEmailAsync sends a one-time SendGrid notification thanking the
// user for their recent purchase and confirming the credited months. Reuses
// the package-local sensitiveEmail / sendSensitiveEmailAsync helper.
func sendKickbackEmailAsync(email, username string, months int, newExpiry time.Time) {
	name := strings.TrimSpace(username)
	if name == "" {
		name = "there"
	}
	expiryDisplay := newExpiry.Format("January 2, 2006")
	subject := fmt.Sprintf("We dropped our prices — here are %d free months on us", months)
	plain := fmt.Sprintf(`Hi %s,

We just slashed Lines Police CAD subscription prices, and since you paid the old rate within the last %d days, we've added %d months of free time to your account.

Your new renewal date: %s

No action needed — the credit is already on your account. Thanks for sticking with us.

— Lines Police CAD`, name, defaultKickbackLookbackDays, months, expiryDisplay)
	html := fmt.Sprintf(`<html><body style="font-family:Arial,Helvetica,sans-serif;color:#0f172a;line-height:1.6;max-width:560px;margin:0 auto;padding:24px;">
<p>Hi %s,</p>
<p>We just slashed Lines Police CAD subscription prices, and since you paid the old rate within the last %d days, we've added <strong>%d months</strong> of free time to your account.</p>
<p>Your new renewal date: <strong>%s</strong></p>
<p>No action needed — the credit is already on your account. Thanks for sticking with us.</p>
<p style="margin-top:32px;">— Lines Police CAD</p>
</body></html>`, name, defaultKickbackLookbackDays, months, expiryDisplay)
	sendSensitiveEmailAsync(sensitiveEmail{
		To:        email,
		Subject:   subject,
		PlainText: plain,
		HTML:      html,
	})
}

// round2 rounds a float to 2 decimal places for response display.
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
