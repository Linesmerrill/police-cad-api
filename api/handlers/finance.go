package handlers

// Owner-only financial P&L endpoints (see FINANCE.md).
//
// Routes (registered in api.go, gated by RequireOwner):
//
//	GET  /api/v1/admin/finance/summary?from=YYYY-MM&to=YYYY-MM
//	GET  /api/v1/admin/finance/expenses?from=YYYY-MM-DD&to=YYYY-MM-DD
//	POST /api/v1/admin/finance/expenses
//	PUT  /api/v1/admin/finance/expenses/{id}
//	DELETE /api/v1/admin/finance/expenses/{id}
//	GET  /api/v1/admin/finance/adsense/oauth/start
//	GET  /api/v1/admin/finance/adsense/oauth/callback?code=&state=

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Finance bundles the finance endpoints and their database dependencies.
type Finance struct {
	ADB  databases.AdminDatabase
	SEDB databases.SubscriptionEventDatabase
	EDB  databases.FinanceExpenseDatabase
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
	"INITIAL_PURCHASE": true,
	"RENEWAL":          true,
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
	stripe   float64
	iapGross float64
	adsense  float64
	expenses float64
}

// buildFinanceSummary buckets subscription events and expenses into months
// between start and end (inclusive) and computes the P&L per month.
//
// Aggregation rules (pure — all inputs are arguments, so this is unit-testable):
//   - Month bucket = UTC month of the event's purchasedAt; events with nil
//     purchasedAt are skipped.
//   - stripe: sum priceUsd where provider="stripe" and
//     eventType="invoice.payment_succeeded".
//   - iap_gross: provider="revenuecat", eventType in {INITIAL_PURCHASE,
//     RENEWAL} summed, minus eventType="REFUND" sums.
//   - iap_net = iap_gross * netRate (see IAP_NET_RATE).
//   - income.total = stripe + iap_net + adsense + admob (admob always 0 in v1).
//   - expenses: sum of expense amounts by month of the expense date (UTC).
//   - profit = total - expenses. All money values rounded to 2 decimals.
func buildFinanceSummary(
	events []models.SubscriptionEvent,
	expenses []models.FinanceExpense,
	start, end time.Time,
	netRate float64,
	adsenseByMonth map[string]float64,
	adsenseConnected bool,
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

	for _, e := range events {
		if e.PurchasedAt == nil {
			continue
		}
		key := monthKey(*e.PurchasedAt)
		switch e.Provider {
		case "stripe":
			if e.EventType == stripePaymentEvent {
				get(key).stripe += e.PriceUSD
			}
		case "revenuecat":
			switch {
			case revenueCatPurchaseEvents[e.EventType]:
				get(key).iapGross += e.PriceUSD
			case e.EventType == revenueCatRefundEvent:
				get(key).iapGross -= e.PriceUSD
			}
		}
	}
	for _, ex := range expenses {
		get(monthKey(ex.Date)).expenses += ex.Amount
	}
	for key, v := range adsenseByMonth {
		get(key).adsense += v
	}

	var months []models.FinanceMonth
	for m := start; !m.After(end); m = m.AddDate(0, 1, 0) {
		key := m.Format("2006-01")
		a := acc[key]
		var stripe, iapGross, adsense, exp float64
		if a != nil {
			stripe, iapGross, adsense, exp = a.stripe, a.iapGross, a.adsense, a.expenses
		}
		iapNet := iapGross * netRate
		total := stripe + iapNet + adsense // admob is 0 in v1
		months = append(months, models.FinanceMonth{
			Month: key,
			Income: models.FinanceMonthIncome{
				Stripe:   round2(stripe),
				IAPGross: round2(iapGross),
				IAPNet:   round2(iapNet),
				AdSense:  round2(adsense),
				AdMob:    0,
				Total:    round2(total),
			},
			Expenses: round2(exp),
			Profit:   round2(total - exp),
			Sources: models.FinanceMonthSources{
				Stripe:     models.FinanceSourceStatus{Connected: true},
				RevenueCat: models.FinanceSourceStatus{Connected: true},
				AdSense:    models.FinanceSourceStatus{Connected: adsenseConnected},
				AdMob:      models.FinanceSourceStatus{Connected: false},
			},
		})
	}

	return models.FinanceSummaryResponse{Months: months}
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
	})
	var events []models.SubscriptionEvent
	if err == nil {
		defer cursor.Close(ctx)
		if allErr := cursor.All(ctx, &events); allErr != nil {
			events = nil
		}
	}
	if err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to read subscription events")
		return
	}

	var expenses []models.FinanceExpense
	expCursor, err := f.EDB.Find(ctx, bson.M{
		"date": bson.M{
			"$gte": start,
			"$lt":  end.AddDate(0, 1, 0),
		},
	})
	if err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to read expenses")
		return
	}
	defer expCursor.Close(ctx)
	if allErr := expCursor.All(ctx, &expenses); allErr != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to read expenses")
		return
	}

	var warnings []string
	adsenseByMonth, adsenseConnected, adsenseWarnings := fetchAdSenseEarnings(ctx, start, end)
	warnings = append(warnings, adsenseWarnings...)

	resp := buildFinanceSummary(events, expenses, start, end, iapNetRate(), adsenseByMonth, adsenseConnected)
	if len(warnings) > 0 {
		resp.Warnings = warnings
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// ---------------------------------------------------------------------------
// Expenses CRUD
// ---------------------------------------------------------------------------

func financeExpenseToDTO(e models.FinanceExpense) models.FinanceExpenseDTO {
	return models.FinanceExpenseDTO{
		ID:        e.ID.Hex(),
		Date:      e.Date.UTC().Format("2006-01-02"),
		Amount:    e.Amount,
		Currency:  e.Currency,
		Category:  e.Category,
		Vendor:    e.Vendor,
		Notes:     e.Notes,
		Source:    e.Source,
		CreatedBy: e.CreatedBy,
		CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// expenseInput is the writable subset of an expense accepted from the API.
type expenseInput struct {
	Date     string  `json:"date"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Category string  `json:"category"`
	Vendor   string  `json:"vendor"`
	Notes    string  `json:"notes"`
	Source   string  `json:"source"`
}

func validExpenseSource(s string) bool {
	switch s {
	case models.ExpenseSourceManual, models.ExpenseSourceCSV, models.ExpenseSourcePlaid:
		return true
	}
	return false
}

// parseExpenseDate accepts "YYYY-MM-DD" (preferred) or RFC3339.
func parseExpenseDate(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	return time.Time{}, errors.New("invalid date format, expected YYYY-MM-DD")
}

// expenseFilterFromRange parses ?from=YYYY-MM-DD&to=YYYY-MM-DD for the expenses
// endpoints. Empty values default to the current calendar month to date.
func expenseFilterFromRange(from, to string) (start, end time.Time, err error) {
	now := time.Now().UTC()
	if from == "" && to == "" {
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		end = now
		return start, end, nil
	}
	if from == "" || to == "" {
		return time.Time{}, time.Time{}, errors.New("both from and to must be provided (YYYY-MM-DD)")
	}
	start, err = parseExpenseDate(from)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid from format, expected YYYY-MM-DD")
	}
	end, err = parseExpenseDate(to)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid to format, expected YYYY-MM-DD")
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, errors.New("to must not be before from")
	}
	return start, end, nil
}

// ListExpensesHandler implements GET /api/v1/admin/finance/expenses.
func (f Finance) ListExpensesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	start, end, err := expenseFilterFromRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	cursor, err := f.EDB.Find(
		ctx,
		bson.M{"date": bson.M{"$gte": start, "$lte": end}},
		options.Find().SetSort(bson.M{"date": -1}),
	)
	if err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to read expenses")
		return
	}
	defer cursor.Close(ctx)

	var expenses []models.FinanceExpense
	if allErr := cursor.All(ctx, &expenses); allErr != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to read expenses")
		return
	}

	dtos := make([]models.FinanceExpenseDTO, 0, len(expenses))
	var total float64
	for _, e := range expenses {
		dtos = append(dtos, financeExpenseToDTO(e))
		total += e.Amount
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(models.FinanceExpenseListResponse{
		Expenses: dtos,
		Total:    round2(total),
	})
}

// CreateExpenseHandler implements POST /api/v1/admin/finance/expenses.
func (f Finance) CreateExpenseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var in expenseInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeFinanceError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	date, err := parseExpenseDate(in.Date)
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Amount <= 0 {
		writeFinanceError(w, http.StatusBadRequest, "amount must be greater than 0")
		return
	}
	source := strings.TrimSpace(strings.ToLower(in.Source))
	if source == "" {
		source = models.ExpenseSourceManual
	}
	if !validExpenseSource(source) {
		writeFinanceError(w, http.StatusBadRequest, "invalid source, expected one of: manual, csv, plaid")
		return
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "USD"
	}

	createdBy := ""
	if admin := financeAdminFromContext(r.Context()); admin != nil {
		createdBy = admin.ID.Hex() + "|" + admin.Email
	}

	expense := models.FinanceExpense{
		ID:        primitive.NewObjectID(),
		Date:      date.UTC(),
		Amount:    round2(in.Amount),
		Currency:  currency,
		Category:  strings.TrimSpace(in.Category),
		Vendor:    strings.TrimSpace(in.Vendor),
		Notes:     strings.TrimSpace(in.Notes),
		Source:    source,
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(),
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := f.EDB.InsertOne(ctx, expense); err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to create expense")
		return
	}

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(financeExpenseToDTO(expense))
}

// UpdateExpenseHandler implements PUT /api/v1/admin/finance/expenses/{id}.
// It replaces all editable fields (date, amount, currency, category, vendor,
// notes, source); createdBy/createdAt are preserved.
func (f Finance) UpdateExpenseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	idHex := mux.Vars(r)["id"]
	id, err := primitive.ObjectIDFromHex(idHex)
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, "invalid expense id")
		return
	}

	var in expenseInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeFinanceError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	date, err := parseExpenseDate(in.Date)
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Amount <= 0 {
		writeFinanceError(w, http.StatusBadRequest, "amount must be greater than 0")
		return
	}
	source := strings.TrimSpace(strings.ToLower(in.Source))
	if source == "" {
		source = models.ExpenseSourceManual
	}
	if !validExpenseSource(source) {
		writeFinanceError(w, http.StatusBadRequest, "invalid source, expected one of: manual, csv, plaid")
		return
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "USD"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	res, err := f.EDB.UpdateOne(
		ctx,
		bson.M{"_id": id},
		bson.M{"$set": bson.M{
			"date":     date.UTC(),
			"amount":   round2(in.Amount),
			"currency": currency,
			"category": strings.TrimSpace(in.Category),
			"vendor":   strings.TrimSpace(in.Vendor),
			"notes":    strings.TrimSpace(in.Notes),
			"source":   source,
		}},
	)
	if err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to update expense")
		return
	}
	if res.MatchedCount == 0 {
		writeFinanceError(w, http.StatusNotFound, "expense not found")
		return
	}

	var updated models.FinanceExpense
	if derr := f.EDB.FindOne(ctx, bson.M{"_id": id}).Decode(&updated); derr != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to read updated expense")
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(financeExpenseToDTO(updated))
}

// DeleteExpenseHandler implements DELETE /api/v1/admin/finance/expenses/{id}.
func (f Finance) DeleteExpenseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	idHex := mux.Vars(r)["id"]
	id, err := primitive.ObjectIDFromHex(idHex)
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, "invalid expense id")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := f.EDB.DeleteOne(ctx, bson.M{"_id": id}); err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to delete expense")
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
