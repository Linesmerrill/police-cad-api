package handlers

// Owner-only financial P&L endpoints (see FINANCE.md).
//
// Routes (registered in api.go, gated by RequireOwner):
//
//	GET  /api/v1/admin/finance/summary?from=YYYY-MM&to=YYYY-MM
//	POST /api/v1/admin/finance/plaid/link-token
//	POST /api/v1/admin/finance/plaid/exchange
//	POST /api/v1/admin/finance/plaid/sync
//	GET  /api/v1/admin/finance/plaid/status
//
// Plaid bank sync is the sole income/expense source for the P&L (cash
// basis). The subscription_events detail (Stripe + IAP gross/net) is kept as
// an earned-revenue complement. The Plaid access token is NEVER stored in
// the database — it comes from the PLAID_ACCESS_TOKEN env var.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Finance bundles the finance endpoints and their database dependencies.
type Finance struct {
	ADB   databases.AdminDatabase
	SEDB  databases.SubscriptionEventDatabase
	BTDB  databases.BankTransactionDatabase
	PSDB  databases.PlaidStateDatabase
	Plaid plaidSyncClient
	// TagDB and RuleDB hold the owner's transaction tags and merchant rules
	// (finance_tags.go). Optional: without them nothing is tagged.
	TagDB  databases.FinanceDocDatabase
	RuleDB databases.FinanceDocDatabase
}

// ---------------------------------------------------------------------------
// RequireOwner middleware
// ---------------------------------------------------------------------------

// financeCtxKey is the context key under which RequireOwner stores the
// verified owner admin for downstream handlers.
type financeCtxKey string

const financeAdminCtxKey = financeCtxKey("financeOwnerAdmin")

// financeAdminFromContext returns the owner admin previously verified by
// RequireOwner, or nil when the request did not pass through it.
func financeAdminFromContext(ctx context.Context) *models.AdminUser {
	admin, _ := ctx.Value(financeAdminCtxKey).(*models.AdminUser)
	return admin
}

func writeFinanceError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// RequireOwner is middleware that only lets LPC owner admins through.
//
// The checks, in order:
//  1. Authorization: Bearer <redacted> present and a valid HS256 JWT signed with JWT_SECRET
//     (missing / bad signature / expired -> 401).
//  2. The token's scope claim is "admin" (user tokens -> 401).
//  3. The admin_users document for sub exists and is active (unknown/inactive -> 401).
//  4. The document itself grants the owner role — "owner" in Roles or
//     Role == "owner" (authenticated but not an owner -> 403).
//
// The roles claim in the token is deliberately NOT trusted on its own: the
// document is always re-read so a revoked or tampered claim cannot escalate.
func (f Finance) RequireOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
			writeFinanceError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
			return
		}
		rawToken := parts[1]

		secret := os.Getenv("JWT_SECRET")
		if secret == "" {
			writeFinanceError(w, http.StatusInternalServerError, "server misconfigured")
			return
		}

		token, err := jwt.Parse(rawToken, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return []byte(secret), nil
		})
		if err != nil || !token.Valid {
			writeFinanceError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			writeFinanceError(w, http.StatusUnauthorized, "invalid token claims")
			return
		}
		if scope, _ := claims["scope"].(string); scope != "admin" {
			writeFinanceError(w, http.StatusUnauthorized, "token is not an admin token")
			return
		}
		sub, _ := claims["sub"].(string)
		adminID, err := primitive.ObjectIDFromHex(sub)
		if err != nil {
			writeFinanceError(w, http.StatusUnauthorized, "invalid token subject")
			return
		}

		// Never trust the roles claim alone: re-read the admin document.
		admin, err := f.ADB.FindOne(r.Context(), bson.M{"_id": adminID})
		if err != nil {
			writeFinanceError(w, http.StatusUnauthorized, "unknown admin")
			return
		}
		if !admin.Active {
			writeFinanceError(w, http.StatusUnauthorized, "admin account is inactive")
			return
		}
		isOwner := admin.Role == "owner"
		for _, role := range admin.Roles {
			if role == "owner" {
				isOwner = true
				break
			}
		}
		if !isOwner {
			writeFinanceError(w, http.StatusForbidden, "owner role required")
			return
		}

		ctx := context.WithValue(r.Context(), financeAdminCtxKey, admin)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ---------------------------------------------------------------------------
// Summary
// ---------------------------------------------------------------------------

// defaultIAPNetRate is the fraction of RevenueCat (IAP) gross revenue assumed
// to land in LPC's pocket after the app-store cut. Apple's and Google's
// standard cut is 15% for most subscriptions (30% in the first year on Apple
// for some programs), so 0.85 is a conservative estimate. This is a rough
// heuristic — the store never reports net per event — and can be overridden
// with the IAP_NET_RATE env var (e.g. 0.85).
const defaultIAPNetRate = 0.85

// revenueCatRefundEvent is the RevenueCat event type that subtracts from IAP gross.
const revenueCatRefundEvent = "REFUND"

// revenueCatPurchaseEvents are the RevenueCat event types that count toward
// IAP gross revenue.
var revenueCatPurchaseEvents = map[string]bool{
	"INITIAL_PURCHASE":      true,
	"RENEWAL":               true,
	"NON_RENEWING_PURCHASE": true,
}

// stripePaymentEvent is the Stripe event type that counts toward Stripe revenue.
const stripePaymentEvent = "invoice.payment_succeeded"

func monthKey(t time.Time) string {
	return t.UTC().Format("2006-01")
}

// parseSummaryRange parses ?from=YYYY-MM&to=YYYY-MM into the inclusive month
// range [start, end]. When both are empty it defaults to the last 12 months
// (current month plus the 11 before it). Returns 400-style errors for bad
// formats.
// maxSummaryMonths bounds one summary request, which reads every transaction
// and subscription event in the range.
const maxSummaryMonths = 60

func parseSummaryRange(from, to string) (start, end time.Time, err error) {
	now := time.Now().UTC()
	if from == "" && to == "" {
		end = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		start = end.AddDate(0, -11, 0)
		return start, end, nil
	}
	if from == "" || to == "" {
		return time.Time{}, time.Time{}, errors.New("both from and to must be provided (YYYY-MM)")
	}
	start, err = time.Parse("2006-01", from)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid from format, expected YYYY-MM")
	}
	end, err = time.Parse("2006-01", to)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid to format, expected YYYY-MM")
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, errors.New("to must not be before from")
	}
	if end.After(start.AddDate(0, maxSummaryMonths-1, 0)) {
		return time.Time{}, time.Time{}, fmt.Errorf("range is too long: at most %d months", maxSummaryMonths)
	}
	return start, end, nil
}

// iapNetRate reads IAP_NET_RATE from the environment, falling back to
// defaultIAPNetRate (0.85) when unset or invalid.
func iapNetRate() float64 {
	raw := strings.TrimSpace(os.Getenv("IAP_NET_RATE"))
	if raw == "" {
		return defaultIAPNetRate
	}
	rate, err := strconv.ParseFloat(raw, 64)
	if err != nil || rate <= 0 || rate > 1 {
		return defaultIAPNetRate
	}
	return rate
}

// financeMonthAccum accumulates raw (unrounded) totals for one month.
type financeMonthAccum struct {
	stripe      float64
	iapGross    float64
	bankIncome  float64
	bankExpense float64
}

// buildFinanceSummary buckets subscription events and bank transactions into
// months between start and end (inclusive) and computes the P&L per month.
//
// Aggregation rules (pure — all inputs are arguments, so this is unit-testable):
//   - Month bucket = UTC month of the event's purchasedAt (subscription
//     events) or the bank transaction's posted date; events with nil
//     purchasedAt are skipped, and PENDING bank transactions are excluded.
//   - stripe: sum priceUsd where provider="stripe" and
//     eventType="invoice.payment_succeeded".
//   - iap_gross: provider="revenuecat", eventType in {INITIAL_PURCHASE,
//     RENEWAL} summed, minus eventType="REFUND" sums.
//   - iap_net = iap_gross * netRate (see IAP_NET_RATE).
//   - bank.income: sum of |amount| for bank transactions with amount < 0
//     (Plaid sign convention: negative = inflow).
//   - bank.expenses: sum of amount for bank transactions with amount > 0
//     (positive = outflow).
//   - When bankConnected: income.total = bank.income, expenses =
//     bank.expenses (cash basis — the bank is the single source of truth).
//     When not connected: income.total = stripe + iap_net, expenses = 0.
//   - profit = total - expenses. All money values rounded to 2 decimals.
func buildFinanceSummary(
	events []models.SubscriptionEvent,
	bankTxs []models.BankTransaction,
	start, end time.Time,
	netRate float64,
	bankConnected bool,
) models.FinanceSummaryResponse {
	acc := map[string]*financeMonthAccum{}
	get := func(key string) *financeMonthAccum {
		a, ok := acc[key]
		if !ok {
			a = &financeMonthAccum{}
			acc[key] = a
		}
		return a
	}

	// Revenue events seen in the range, per source, for the badges.
	var stripeEvents, revenueCatEvents int
	for _, e := range events {
		if e.PurchasedAt == nil {
			continue
		}
		key := monthKey(*e.PurchasedAt)
		switch e.Provider {
		case "stripe":
			if e.EventType == stripePaymentEvent {
				get(key).stripe += e.PriceUSD
				stripeEvents++
			}
		case "revenuecat":
			switch {
			case revenueCatPurchaseEvents[e.EventType]:
				get(key).iapGross += e.PriceUSD
				revenueCatEvents++
			case e.EventType == revenueCatRefundEvent:
				get(key).iapGross -= e.PriceUSD
			}
		}
	}
	// Moves between the owner's own linked accounts are neither income nor
	// expense (finance_transfers.go).
	internal := internalTransferIDs(bankTxs)
	for _, tx := range bankTxs {
		if !countsTowardPL(tx, internal) {
			continue
		}
		a := get(monthKey(tx.Date))
		switch {
		case tx.Amount < 0:
			a.bankIncome += -tx.Amount
		case tx.Amount > 0:
			a.bankExpense += tx.Amount
		}
	}

	var months []models.FinanceMonth
	for m := start; !m.After(end); m = m.AddDate(0, 1, 0) {
		key := m.Format("2006-01")
		a := acc[key]
		var stripe, iapGross, bInc, bExp float64
		if a != nil {
			stripe, iapGross, bInc, bExp = a.stripe, a.iapGross, a.bankIncome, a.bankExpense
		}
		iapNet := iapGross * netRate

		var total, exp float64
		var bank models.FinanceBankMonth
		if bankConnected {
			total = bInc
			exp = bExp
			bank = models.FinanceBankMonth{Connected: true, Income: round2(bInc), Expenses: round2(bExp)}
		} else {
			total = stripe + iapNet
			exp = 0
			bank = models.FinanceBankMonth{Connected: false}
		}

		months = append(months, models.FinanceMonth{
			Month: key,
			Income: models.FinanceMonthIncome{
				Stripe:   round2(stripe),
				IAPGross: round2(iapGross),
				IAPNet:   round2(iapNet),
				Total:    round2(total),
			},
			Expenses: round2(exp),
			Profit:   round2(total - exp),
			Bank:     bank,
			// SummaryHandler raises these to "ever received" (applySourceHistory).
			Sources: models.FinanceMonthSources{
				Stripe:     models.FinanceSourceStatus{Connected: stripeEvents > 0},
				RevenueCat: models.FinanceSourceStatus{Connected: revenueCatEvents > 0},
				Bank:       models.FinanceSourceStatus{Connected: bankConnected},
			},
		})
	}

	return models.FinanceSummaryResponse{
		Months:        months,
		BankConnected: bankConnected,
		Sources: models.FinanceMonthSources{
			Stripe:     models.FinanceSourceStatus{Connected: stripeEvents > 0, Events: stripeEvents},
			RevenueCat: models.FinanceSourceStatus{Connected: revenueCatEvents > 0, Events: revenueCatEvents},
			Bank:       models.FinanceSourceStatus{Connected: bankConnected},
		},
	}
}

// applySourceHistory marks a source connected when it has ever recorded a
// revenue event, even if none fall in the requested range. A quiet month is
// not a broken integration; a source that has never sent a payment is.
func applySourceHistory(resp *models.FinanceSummaryResponse, stripeEver, revenueCatEver bool) {
	resp.Sources.Stripe.Connected = resp.Sources.Stripe.Connected || stripeEver
	resp.Sources.RevenueCat.Connected = resp.Sources.RevenueCat.Connected || revenueCatEver
	for i := range resp.Months {
		resp.Months[i].Sources.Stripe.Connected = resp.Sources.Stripe.Connected
		resp.Months[i].Sources.RevenueCat.Connected = resp.Sources.RevenueCat.Connected
	}
}

// revenueEventFilter matches the events that count as revenue for one
// provider, outside sandbox.
func revenueEventFilter(provider string) bson.M {
	f := bson.M{"provider": provider, "environment": bson.M{"$ne": "SANDBOX"}, "priceUsd": bson.M{"$gt": 0}}
	if provider == "stripe" {
		f["eventType"] = stripePaymentEvent
	} else {
		types := make([]string, 0, len(revenueCatPurchaseEvents))
		for t := range revenueCatPurchaseEvents {
			types = append(types, t)
		}
		f["eventType"] = bson.M{"$in": types}
	}
	return f
}

// SummaryHandler implements GET /api/v1/admin/finance/summary.
func (f Finance) SummaryHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	start, end, err := parseSummaryRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Pull the events needed for revenue in the window. purchasedAt bounds the
	// query; docs with nil purchasedAt are dropped by the aggregator anyway.
	cursor, err := f.SEDB.Find(ctx, bson.M{
		"purchasedAt": bson.M{
			"$gte": start,
			"$lt":  end.AddDate(0, 1, 0),
		},
		// Test-mode Stripe payments and RevenueCat sandbox purchases are not
		// revenue.
		"environment": bson.M{"$ne": "SANDBOX"},
	})
	var events []models.SubscriptionEvent
	var warnings []string
	if err == nil {
		defer cursor.Close(ctx)
		if allErr := cursor.All(ctx, &events); allErr != nil {
			// Say so: silently dropping them showed $0 subscription revenue
			// as if it were real.
			events = nil
			warnings = append(warnings, "subscriptions: failed to read subscription events ("+allErr.Error()+")")
		}
	}
	if err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to read subscription events")
		return
	}

	bankConnected := plaidAccessTokenConfigured()
	var bankTxs []models.BankTransaction
	if bankConnected {
		// Read a few days either side of the range so a transfer whose two
		// ends straddle the boundary still pairs up. Only months inside the
		// range are reported.
		txCursor, err := f.BTDB.Find(ctx, bson.M{
			"date": bson.M{
				"$gte": start.Add(-internalTransferWindow),
				"$lt":  end.AddDate(0, 1, 0).Add(internalTransferWindow),
			},
		})
		if err != nil {
			// Degrade gracefully: summary still renders, bank shows 0 and a
			// warning explains why.
			warnings = append(warnings, "bank: failed to read transactions ("+err.Error()+")")
		} else {
			defer txCursor.Close(ctx)
			if allErr := txCursor.All(ctx, &bankTxs); allErr != nil {
				warnings = append(warnings, "bank: failed to read transactions ("+allErr.Error()+")")
				bankTxs = nil
			}
		}
	}

	resp := buildFinanceSummary(events, bankTxs, start, end, iapNetRate(), bankConnected)

	// Has each source ever sent a payment? Distinguishes a quiet range from
	// an integration that has never delivered one.
	stripeEver, _ := f.SEDB.CountDocuments(ctx, revenueEventFilter("stripe"), options.Count().SetLimit(1))
	revenueCatEver, _ := f.SEDB.CountDocuments(ctx, revenueEventFilter("revenuecat"), options.Count().SetLimit(1))
	applySourceHistory(&resp, stripeEver > 0, revenueCatEver > 0)

	// Where the money came from and went, by tag (finance_tags.go).
	if bankConnected {
		resp.ByTag = tagTotals(bankTxs, start, end, f.tagsByID(ctx))
	}
	if len(warnings) > 0 {
		resp.Warnings = warnings
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
