package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/models"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/invoice"
	"github.com/stripe/stripe-go/v82/subscription"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

const rcSubscriberBaseURL = "https://api.revenuecat.com/v1/subscribers/"

// rcSubscription mirrors the fields we read from RevenueCat's subscriber
// endpoint. The rest of the payload is preserved in rawSources.revenuecat
// so admins can inspect anything we don't normalize.
type rcSubscriptionRaw struct {
	ExpiresDate              *time.Time `json:"expires_date"`
	PurchaseDate             *time.Time `json:"purchase_date"`
	OriginalPurchaseDate     *time.Time `json:"original_purchase_date"`
	UnsubscribeDetectedAt    *time.Time `json:"unsubscribe_detected_at"`
	BillingIssuesDetectedAt  *time.Time `json:"billing_issues_detected_at"`
	Store                    string     `json:"store"`
	IsSandbox                bool       `json:"is_sandbox"`
	PeriodType               string     `json:"period_type"`
	PriceInPurchasedCurrency struct {
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
	} `json:"price_in_purchased_currency"`
}

// rcNonSubscriptionRaw is a one-time / consumable purchase under
// subscriber.non_subscriptions[productID][]. These don't have an
// expires_date — we infer an effective period from the product id
// suffix when possible (e.g. "*_1month" → 30 days).
type rcNonSubscriptionRaw struct {
	ID                       string     `json:"id"`
	PurchaseDate             *time.Time `json:"purchase_date"`
	Store                    string     `json:"store"`
	IsSandbox                bool       `json:"is_sandbox"`
	PriceInPurchasedCurrency struct {
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
	} `json:"price_in_purchased_currency"`
}

type rcSubscriberRaw struct {
	Subscriber struct {
		OriginalAppUserID string                            `json:"original_app_user_id"`
		Subscriptions     map[string]rcSubscriptionRaw      `json:"subscriptions"`
		NonSubscriptions  map[string][]rcNonSubscriptionRaw `json:"non_subscriptions"`
	} `json:"subscriber"`
}

// userSearchHit is a row in the email-search results list.
type userSearchHit struct {
	UserID       string `json:"userId"`
	Email        string `json:"email"`
	Username     string `json:"username,omitempty"`
	DBPlan       string `json:"dbPlan"`
	DBActive     bool   `json:"dbActive"`
	DBSource     string `json:"dbSource,omitempty"`
	LastEventAt  *time.Time `json:"lastEventAt,omitempty"`
}

// authoritativeState is what we believe the user's real subscription is,
// derived from RC and/or Stripe live calls.
type authoritativeState struct {
	Status         string     `json:"status"` // "active" | "canceled" | "expired" | "billing_issue" | "none"
	Source         string     `json:"source,omitempty"`
	Plan           string     `json:"plan,omitempty"`
	IsAnnual       bool       `json:"isAnnual,omitempty"`
	Store          string     `json:"store,omitempty"`
	ProductID      string     `json:"productId,omitempty"`
	SubscriptionID string     `json:"subscriptionId,omitempty"`
	PurchasedAt    *time.Time `json:"purchasedAt,omitempty"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	CancelAt       *time.Time `json:"cancelAt,omitempty"`
	PriceUSD       float64    `json:"priceUsd,omitempty"`
	Currency       string     `json:"currency,omitempty"`
}

type mismatchField struct {
	Field         string      `json:"field"`
	Authoritative interface{} `json:"authoritative"`
	DB            interface{} `json:"db"`
}

type mismatchReport struct {
	HasMismatch bool            `json:"hasMismatch"`
	Summary     string          `json:"summary,omitempty"`
	Fields      []mismatchField `json:"fields,omitempty"`
}

type paymentRow struct {
	Date           time.Time  `json:"date"`
	EffectiveUntil *time.Time `json:"effectiveUntil,omitempty"` // entitlement end (for non-sub & sub anchor rows)
	Amount         float64    `json:"amount"`
	Currency       string     `json:"currency"`
	Status         string     `json:"status"` // "paid" | "failed" | "refunded" | "purchase" | ...
	Source         string     `json:"source"` // "stripe" | "revenuecat"
	Reference      string     `json:"reference"` // invoice id / transaction id
	Plan           string     `json:"plan,omitempty"`
	ProductLabel   string     `json:"productLabel,omitempty"` // raw product id, e.g. "community_elite_1month"
}

// liveSourceDiagnostic explains the outcome of trying to reach each
// authoritative source. Without this, every failure mode (missing key,
// 404, network error, wrong project) collapses to status="none" and the
// dashboard contradicts itself when the DB says active but live says none.
type liveSourceDiagnostic struct {
	RevenueCat string `json:"revenuecat"` // "ok" | "no_api_key" | "user_not_found" | "http_error" | "parse_error"
	Stripe     string `json:"stripe"`     // "ok" | "no_customer_id" | "http_error"
	Note       string `json:"note,omitempty"`
}

type userDetailResponse struct {
	User struct {
		ID           string             `json:"id"`
		Email        string             `json:"email"`
		Username     string             `json:"username,omitempty"`
		Subscription models.Subscription `json:"subscription"`
	} `json:"user"`
	Authoritative authoritativeState     `json:"authoritative"`
	Mismatch      mismatchReport         `json:"mismatch"`
	Payments      []paymentRow           `json:"payments"`
	LiveSources   liveSourceDiagnostic   `json:"liveSources"`
	RawSources    map[string]interface{} `json:"rawSources"`
}

// AdminSubscriptionUserSearchHandler returns a list of users matching
// an email substring. Used by the staff dashboard's search box.
//
//	GET /api/v1/admin/subscription/users?q=jasey&limit=20
func (h Admin) AdminSubscriptionUserSearchHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if q == "" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": []userSearchHit{}})
		return
	}

	safe := regexp.QuoteMeta(q)
	filter := bson.M{
		"$or": []bson.M{
			{"user.email": bson.M{"$regex": safe, "$options": "i"}},
			{"user.username": bson.M{"$regex": safe, "$options": "i"}},
		},
	}

	findOpts := options.Find().
		SetLimit(int64(limit)).
		SetProjection(bson.M{
			"_id":               1,
			"user.email":        1,
			"user.username":     1,
			"user.subscription": 1,
		})

	cursor, err := h.UDB.Find(r.Context(), filter, findOpts)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "search failed"})
		return
	}

	hits := []userSearchHit{}
	for cursor.Next(r.Context()) {
		var u models.User
		if err := cursor.DecodeCurrent(&u); err != nil {
			continue
		}
		hits = append(hits, userSearchHit{
			UserID:   u.ID,
			Email:    u.Details.Email,
			Username: u.Details.Username,
			DBPlan:   u.Details.Subscription.Plan,
			DBActive: u.Details.Subscription.Active,
			DBSource: u.Details.Subscription.Source,
		})
	}

	// Best-effort: look up the most recent subscription_events row per hit
	// so the list can display "last activity". One round-trip per hit;
	// limit is 20 so worst case is 20 small queries.
	if h.SEDB != nil {
		for i, hit := range hits {
			var last models.SubscriptionEvent
			err := h.SEDB.FindOne(r.Context(),
				bson.M{"userId": hit.UserID},
				options.FindOne().SetSort(bson.D{{Key: "createdAt", Value: -1}}),
			).Decode(&last)
			if err == nil {
				t := last.CreatedAt
				hits[i].LastEventAt = &t
			}
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": hits})
}

// AdminSubscriptionUserDetailHandler returns the full picture for one user:
// DB state, live state from RC + Stripe, a mismatch report, and a merged
// payments timeline. Powers the staff dashboard's detail view.
//
//	GET /api/v1/admin/subscription/users/{user_id}
func (h Admin) AdminSubscriptionUserDetailHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	userID := mux.Vars(r)["user_id"]
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid user id"})
		return
	}

	var user models.User
	if err := h.UDB.FindOne(r.Context(), bson.M{"_id": uID}).Decode(&user); err != nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "user not found"})
		return
	}

	rcState, rcRaw, rcStatus := fetchRevenueCatStateForUser(r.Context(), userID)
	stripeSubs, stripeInvoices, stripeRaw := fetchStripeStateForUser(r.Context(), user.Details.Subscription.StripeCustomerID)

	stripeStatus := "ok"
	if user.Details.Subscription.StripeCustomerID == "" {
		stripeStatus = "no_customer_id"
	}

	auth := pickAuthoritativeState(rcState, stripeSubs)
	mm := computeMismatch(user.Details.Subscription, auth)
	payments := buildPaymentTimeline(rcState, stripeInvoices)

	diag := liveSourceDiagnostic{RevenueCat: rcStatus, Stripe: stripeStatus}
	switch rcStatus {
	case "no_api_key":
		diag.Note = "REVENUECAT_SECRET_API_KEY is not set on this API deployment — live RC data cannot be loaded."
	case "user_not_found":
		diag.Note = "RevenueCat has no record for this user id. They may have purchased under a different RC project, or never via mobile."
	case "http_error":
		diag.Note = "RevenueCat call failed. Check API logs for details."
	}

	resp := userDetailResponse{
		Authoritative: auth,
		Mismatch:      mm,
		Payments:      payments,
		LiveSources:   diag,
		RawSources: map[string]interface{}{
			"revenuecat": rcRaw,
			"stripe":     stripeRaw,
		},
	}
	resp.User.ID = user.ID
	resp.User.Email = user.Details.Email
	resp.User.Username = user.Details.Username
	resp.User.Subscription = user.Details.Subscription

	_ = json.NewEncoder(w).Encode(resp)
}

// AdminSubscriptionUserSyncHandler pulls the authoritative subscription
// state from RC or Stripe and writes it back to the User document. The
// "fix" button on the staff dashboard calls this.
//
//	POST /api/v1/admin/subscription/users/{user_id}/sync
//	Body: { "source": "revenuecat" | "stripe" | "auto" }   (auto = pick the active one)
func (h Admin) AdminSubscriptionUserSyncHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	userID := mux.Vars(r)["user_id"]
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid user id"})
		return
	}

	rawBody, _ := io.ReadAll(r.Body)
	var req struct {
		Source string `json:"source"` // "revenuecat" | "stripe" | "auto"
	}
	_ = json.Unmarshal(rawBody, &req)
	if req.Source == "" {
		req.Source = "auto"
	}

	var user models.User
	if err := h.UDB.FindOne(r.Context(), bson.M{"_id": uID}).Decode(&user); err != nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "user not found"})
		return
	}

	rcState, _, _ := fetchRevenueCatStateForUser(r.Context(), userID)
	stripeSubs, _, _ := fetchStripeStateForUser(r.Context(), user.Details.Subscription.StripeCustomerID)

	var chosen *authoritativeState
	switch req.Source {
	case "revenuecat":
		s := summarizeRCToAuthoritative(rcState)
		chosen = &s
	case "stripe":
		s := summarizeStripeToAuthoritative(stripeSubs)
		chosen = &s
	default:
		s := pickAuthoritativeState(rcState, stripeSubs)
		chosen = &s
	}
	if chosen == nil || chosen.Status == "none" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "no authoritative subscription found to sync from"})
		return
	}

	set := bson.M{"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now())}
	switch chosen.Status {
	case "active":
		set["user.subscription.active"] = true
	case "canceled":
		// Canceled but still inside the period — keep active, set cancelAt.
		set["user.subscription.active"] = chosen.ExpiresAt != nil && chosen.ExpiresAt.After(time.Now())
	default:
		set["user.subscription.active"] = false
	}
	if chosen.Plan != "" {
		set["user.subscription.plan"] = chosen.Plan
		set["user.subscription.isAnnual"] = chosen.IsAnnual
	}
	if chosen.Source != "" {
		set["user.subscription.source"] = chosen.Source
	}
	if chosen.SubscriptionID != "" {
		set["user.subscription.id"] = chosen.SubscriptionID
	}
	if chosen.PurchasedAt != nil {
		set["user.subscription.purchaseDate"] = chosen.PurchasedAt.Format(time.RFC3339)
	}
	if chosen.ExpiresAt != nil {
		set["user.subscription.expirationDate"] = chosen.ExpiresAt.Format(time.RFC3339)
		set["user.subscription.currentPeriodEnd"] = primitive.NewDateTimeFromTime(*chosen.ExpiresAt)
	}
	if chosen.CancelAt != nil {
		set["user.subscription.cancelAt"] = primitive.NewDateTimeFromTime(*chosen.CancelAt)
	} else {
		set["user.subscription.cancelAt"] = nil
	}

	if _, err := h.UDB.UpdateOne(r.Context(), bson.M{"_id": uID}, bson.M{"$set": set}); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to update user"})
		return
	}

	// Audit row so the sync is permanently traceable.
	if h.SEDB != nil {
		previous := map[string]interface{}{}
		if rawPrev, err := bson.Marshal(user.Details.Subscription); err == nil {
			_ = bson.Unmarshal(rawPrev, &previous)
		}
		evt := models.SubscriptionEvent{
			UserID:                user.ID,
			UserEmail:             user.Details.Email,
			Provider:              "admin",
			Store:                 chosen.Store,
			EventType:             "admin_sync_from_" + chosen.Source,
			Plan:                  chosen.Plan,
			IsAnnual:              chosen.IsAnnual,
			ProductID:             chosen.ProductID,
			TransactionID:         chosen.SubscriptionID,
			PriceUSD:              chosen.PriceUSD,
			Currency:              chosen.Currency,
			PurchasedAt:           chosen.PurchasedAt,
			ExpiresAt:             chosen.ExpiresAt,
			PreviousSubscription:  previous,
			RawPayload:            string(rawBody),
			ProcessingStatus:      "ok",
			SourceIP:              r.RemoteAddr,
			CreatedAt:             time.Now().UTC(),
		}
		if _, err := h.SEDB.InsertOne(r.Context(), evt); err != nil {
			zap.S().Warnw("admin sync: failed to write audit row", "userId", user.ID, "error", err)
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":            true,
		"syncedFrom":    chosen.Source,
		"authoritative": chosen,
	})
}

// fetchRevenueCatStateForUser calls RC's subscribers endpoint. Returns
// (nil, nil, status) on any failure so the caller can show staff *why*
// the live state came back empty (missing key, 404, etc.) instead of
// just "none".
func fetchRevenueCatStateForUser(ctx context.Context, appUserID string) (*rcSubscriberRaw, map[string]interface{}, string) {
	apiKey := os.Getenv("REVENUECAT_SECRET_API_KEY")
	if apiKey == "" {
		return nil, nil, "no_api_key"
	}
	if appUserID == "" {
		return nil, nil, "no_user_id"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", rcSubscriberBaseURL+appUserID, nil)
	if err != nil {
		return nil, nil, "http_error"
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		zap.S().Warnw("RC fetch failed", "userId", appUserID, "error", err)
		return nil, nil, "http_error"
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil, "user_not_found"
	}
	if resp.StatusCode/100 != 2 {
		zap.S().Warnw("RC non-2xx", "userId", appUserID, "status", resp.Status, "body", string(body))
		return nil, nil, "http_error"
	}
	var typed rcSubscriberRaw
	if err := json.Unmarshal(body, &typed); err != nil {
		return nil, nil, "parse_error"
	}
	var raw map[string]interface{}
	_ = json.Unmarshal(body, &raw)
	return &typed, raw, "ok"
}

// fetchStripeStateForUser pulls every subscription + every invoice for
// the given Stripe customer. customerID may be empty (user has no Stripe
// history) in which case we return zero values.
func fetchStripeStateForUser(ctx context.Context, customerID string) ([]*stripe.Subscription, []*stripe.Invoice, map[string]interface{}) {
	if customerID == "" {
		return nil, nil, nil
	}
	subs := []*stripe.Subscription{}
	subParams := &stripe.SubscriptionListParams{
		Customer: stripe.String(customerID),
		Status:   stripe.String("all"),
	}
	subParams.Limit = stripe.Int64(50)
	subIter := subscription.List(subParams)
	for subIter.Next() {
		subs = append(subs, subIter.Subscription())
	}
	if err := subIter.Err(); err != nil {
		zap.S().Warnw("Stripe sub list failed", "customerId", customerID, "error", err)
	}

	invoices := []*stripe.Invoice{}
	invParams := &stripe.InvoiceListParams{
		Customer: stripe.String(customerID),
	}
	invParams.Limit = stripe.Int64(50)
	invIter := invoice.List(invParams)
	for invIter.Next() {
		invoices = append(invoices, invIter.Invoice())
	}
	if err := invIter.Err(); err != nil {
		zap.S().Warnw("Stripe invoice list failed", "customerId", customerID, "error", err)
	}

	raw := map[string]interface{}{
		"customerId":    customerID,
		"subscriptions": subs,
		"invoices":      invoices,
	}
	return subs, invoices, raw
}

// summarizeRCToAuthoritative picks the most recent active subscription
// from RC (or the most recent canceled one if none active).
func summarizeRCToAuthoritative(rc *rcSubscriberRaw) authoritativeState {
	if rc == nil || len(rc.Subscriber.Subscriptions) == 0 {
		return authoritativeState{Status: "none"}
	}
	now := time.Now().UTC()
	var bestActive, bestNonActive *rcSubscriptionRaw
	var bestActiveKey, bestNonActiveKey string
	for productID, s := range rc.Subscriber.Subscriptions {
		s := s // copy
		isActive := s.ExpiresDate != nil && s.ExpiresDate.After(now) &&
			s.BillingIssuesDetectedAt == nil
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
	var sub *rcSubscriptionRaw
	var productID string
	if bestActive != nil {
		sub = bestActive
		productID = bestActiveKey
	} else if bestNonActive != nil {
		sub = bestNonActive
		productID = bestNonActiveKey
	} else {
		return authoritativeState{Status: "none"}
	}

	status := "expired"
	if sub.ExpiresDate != nil && sub.ExpiresDate.After(now) {
		if sub.UnsubscribeDetectedAt != nil {
			status = "canceled"
		} else if sub.BillingIssuesDetectedAt != nil {
			status = "billing_issue"
		} else {
			status = "active"
		}
	}

	plan, isAnnual, _ := parseProductID(productID)
	source := mapStoreToSource(sub.Store)
	auth := authoritativeState{
		Status:      status,
		Source:      source,
		Plan:        plan,
		IsAnnual:    isAnnual,
		Store:       strings.ToUpper(sub.Store),
		ProductID:   productID,
		PurchasedAt: sub.PurchaseDate,
		ExpiresAt:   sub.ExpiresDate,
		Currency:    sub.PriceInPurchasedCurrency.Currency,
	}
	if sub.UnsubscribeDetectedAt != nil {
		auth.CancelAt = sub.ExpiresDate
	}
	if sub.PriceInPurchasedCurrency.Currency == "USD" {
		auth.PriceUSD = sub.PriceInPurchasedCurrency.Amount
	}
	return auth
}

// summarizeStripeToAuthoritative picks the most recent active Stripe sub
// (or the most recent non-active one if none active).
func summarizeStripeToAuthoritative(subs []*stripe.Subscription) authoritativeState {
	if len(subs) == 0 {
		return authoritativeState{Status: "none"}
	}
	var pick *stripe.Subscription
	for _, s := range subs {
		if s.Status == stripe.SubscriptionStatusActive || s.Status == stripe.SubscriptionStatusTrialing {
			if pick == nil || s.Created > pick.Created {
				pick = s
			}
		}
	}
	if pick == nil {
		// No active — pick most recent regardless of status.
		for _, s := range subs {
			if pick == nil || s.Created > pick.Created {
				pick = s
			}
		}
	}
	if pick == nil {
		return authoritativeState{Status: "none"}
	}

	auth := authoritativeState{
		Source:         "stripe",
		Store:          "STRIPE",
		SubscriptionID: pick.ID,
	}
	switch pick.Status {
	case stripe.SubscriptionStatusActive, stripe.SubscriptionStatusTrialing:
		auth.Status = "active"
	case stripe.SubscriptionStatusCanceled:
		auth.Status = "canceled"
	case stripe.SubscriptionStatusPastDue, stripe.SubscriptionStatusUnpaid:
		auth.Status = "billing_issue"
	case stripe.SubscriptionStatusIncomplete, stripe.SubscriptionStatusIncompleteExpired:
		auth.Status = "expired"
	default:
		auth.Status = "expired"
	}

	if pick.StartDate > 0 {
		t := time.Unix(pick.StartDate, 0).UTC()
		auth.PurchasedAt = &t
	}
	if pick.CancelAt > 0 {
		t := time.Unix(pick.CancelAt, 0).UTC()
		auth.CancelAt = &t
	}
	if pick.Items != nil && len(pick.Items.Data) > 0 {
		item := pick.Items.Data[0]
		if item.CurrentPeriodEnd > 0 {
			t := time.Unix(item.CurrentPeriodEnd, 0).UTC()
			auth.ExpiresAt = &t
		}
		if item.Price != nil {
			auth.ProductID = item.Price.ID
			auth.Currency = string(item.Price.Currency)
			if item.Price.UnitAmount > 0 {
				auth.PriceUSD = float64(item.Price.UnitAmount) / 100.0
			}
			plan, isAnnual := planFromStripePriceID(item.Price.ID)
			auth.Plan = plan
			auth.IsAnnual = isAnnual
		}
	}
	return auth
}

// pickAuthoritativeState merges RC + Stripe summaries. If both have
// active subs (rare but possible), prefer whichever started more
// recently. If only one has an active sub, prefer that one. If neither
// is active, prefer the one with the latest expiresAt.
func pickAuthoritativeState(rc *rcSubscriberRaw, stripeSubs []*stripe.Subscription) authoritativeState {
	rcSum := summarizeRCToAuthoritative(rc)
	stripeSum := summarizeStripeToAuthoritative(stripeSubs)

	rcActive := rcSum.Status == "active"
	stripeActive := stripeSum.Status == "active"

	switch {
	case rcActive && !stripeActive:
		return rcSum
	case stripeActive && !rcActive:
		return stripeSum
	case rcActive && stripeActive:
		// Both active — pick the most recently started.
		if rcSum.PurchasedAt != nil && stripeSum.PurchasedAt != nil &&
			stripeSum.PurchasedAt.After(*rcSum.PurchasedAt) {
			return stripeSum
		}
		return rcSum
	default:
		// Neither active — pick the one with the later expiresAt.
		if rcSum.Status == "none" {
			return stripeSum
		}
		if stripeSum.Status == "none" {
			return rcSum
		}
		if rcSum.ExpiresAt != nil && stripeSum.ExpiresAt != nil &&
			stripeSum.ExpiresAt.After(*rcSum.ExpiresAt) {
			return stripeSum
		}
		return rcSum
	}
}

// computeMismatch compares the User doc's subscription to authoritative
// state and surfaces actionable differences for the staff UI.
func computeMismatch(db models.Subscription, auth authoritativeState) mismatchReport {
	if auth.Status == "none" {
		// No authoritative source — nothing to compare against. Don't
		// flag a mismatch in this case; it just means the user has
		// never paid via RC or Stripe (or RC key is missing).
		return mismatchReport{HasMismatch: false}
	}

	authActive := auth.Status == "active" ||
		(auth.Status == "canceled" && auth.ExpiresAt != nil && auth.ExpiresAt.After(time.Now()))

	var fields []mismatchField

	if db.Active != authActive {
		fields = append(fields, mismatchField{
			Field: "active", Authoritative: authActive, DB: db.Active,
		})
	}
	dbPlan := db.Plan
	if dbPlan == "" {
		dbPlan = "free"
	}
	authPlanForCompare := auth.Plan
	if !authActive {
		authPlanForCompare = "free"
	}
	if authPlanForCompare != "" && dbPlan != authPlanForCompare {
		fields = append(fields, mismatchField{
			Field: "plan", Authoritative: authPlanForCompare, DB: dbPlan,
		})
	}
	if auth.Source != "" && db.Source != auth.Source {
		fields = append(fields, mismatchField{
			Field: "source", Authoritative: auth.Source, DB: db.Source,
		})
	}
	// Period end / cancel-at drift. We compare on day granularity to
	// avoid flagging trivial timestamp jitter (e.g. RC reports a slightly
	// different second than what we stored). Any present authoritative
	// value vs missing/different DB value triggers the flag.
	if auth.ExpiresAt != nil {
		dbDay := subscriptionDateDay(db.CurrentPeriodEnd, db.ExpirationDate)
		authDay := auth.ExpiresAt.UTC().Format("2006-01-02")
		if dbDay != authDay {
			fields = append(fields, mismatchField{
				Field: "periodEnd", Authoritative: authDay,
				DB: orDash(dbDay),
			})
		}
	}
	authCancelDay := ""
	if auth.CancelAt != nil {
		authCancelDay = auth.CancelAt.UTC().Format("2006-01-02")
	}
	dbCancelDay := subscriptionDateDay(db.CancelAt, "")
	if authCancelDay != dbCancelDay {
		fields = append(fields, mismatchField{
			Field: "cancelAt", Authoritative: orDash(authCancelDay),
			DB: orDash(dbCancelDay),
		})
	}

	if len(fields) == 0 {
		return mismatchReport{HasMismatch: false}
	}

	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, fmt.Sprintf("%s: %v → %v", f.Field, f.DB, f.Authoritative))
	}
	return mismatchReport{
		HasMismatch: true,
		Summary:     "DB out of sync with " + auth.Source + " — " + strings.Join(parts, ", "),
		Fields:      fields,
	}
}

// buildPaymentTimeline merges Stripe invoices and RC subscription anchors
// into a single time-sorted list. Stripe is rich (every invoice ever) so
// it carries most of the load; RC contributes one row per known
// subscription (purchase_date) since RC's free API doesn't expose
// individual renewals.
func buildPaymentTimeline(rc *rcSubscriberRaw, stripeInvoices []*stripe.Invoice) []paymentRow {
	rows := []paymentRow{}

	for _, inv := range stripeInvoices {
		if inv == nil {
			continue
		}
		amount := float64(inv.AmountPaid) / 100.0
		if amount == 0 && inv.AmountDue > 0 {
			amount = float64(inv.AmountDue) / 100.0
		}
		date := time.Unix(inv.Created, 0).UTC()
		status := string(inv.Status)
		if status == "" {
			status = "unknown"
		}
		plan := ""
		if inv.Lines != nil && len(inv.Lines.Data) > 0 && inv.Lines.Data[0].Pricing != nil && inv.Lines.Data[0].Pricing.PriceDetails != nil {
			p, _ := planFromStripePriceID(inv.Lines.Data[0].Pricing.PriceDetails.Price)
			plan = p
		}
		rows = append(rows, paymentRow{
			Date:      date,
			Amount:    amount,
			Currency:  strings.ToUpper(string(inv.Currency)),
			Status:    status,
			Source:    "stripe",
			Reference: inv.ID,
			Plan:      plan,
		})
	}

	if rc != nil {
		for productID, s := range rc.Subscriber.Subscriptions {
			if s.PurchaseDate == nil {
				continue
			}
			plan, _, _ := parseProductID(productID)
			// RC's /v1/subscribers endpoint doesn't surface price for
			// subscriptions, so fall back to our known SKU pricing.
			amount := s.PriceInPurchasedCurrency.Amount
			currency := s.PriceInPurchasedCurrency.Currency
			if amount == 0 {
				if p, ok := knownUSDPrice(productID); ok {
					amount = p
					if currency == "" {
						currency = "USD"
					}
				}
			}
			rows = append(rows, paymentRow{
				Date:           *s.PurchaseDate,
				EffectiveUntil: s.ExpiresDate,
				Amount:         amount,
				Currency:       currency,
				Status:         "purchase",
				Source:         "revenuecat",
				Reference:      productID,
				Plan:           plan,
				ProductLabel:   productID,
			})
		}
		for productID, purchases := range rc.Subscriber.NonSubscriptions {
			for _, p := range purchases {
				if p.PurchaseDate == nil {
					continue
				}
				var endPtr *time.Time
				if d := nonSubDuration(productID); d > 0 {
					end := p.PurchaseDate.Add(d)
					endPtr = &end
				}
				ref := p.ID
				if ref == "" {
					ref = productID
				}
				amount := p.PriceInPurchasedCurrency.Amount
				currency := p.PriceInPurchasedCurrency.Currency
				if amount == 0 {
					if px, ok := knownUSDPrice(productID); ok {
						amount = px
						if currency == "" {
							currency = "USD"
						}
					}
				}
				rows = append(rows, paymentRow{
					Date:           *p.PurchaseDate,
					EffectiveUntil: endPtr,
					Amount:         amount,
					Currency:       currency,
					Status:         "purchase",
					Source:         "revenuecat",
					Reference:      ref,
					ProductLabel:   productID,
				})
			}
		}
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].Date.After(rows[j].Date) })
	return rows
}

// subscriptionDateDay normalizes a user.subscription date field — which
// can come back from Mongo as primitive.DateTime, time.Time, RFC3339
// string, or nil — into a YYYY-MM-DD string for day-granularity
// comparison. Falls back to the second argument (an RFC3339 string) if
// the first value is empty/unparseable. Returns "" when both are absent.
func subscriptionDateDay(primary interface{}, fallbackRFC string) string {
	if t, ok := coerceTime(primary); ok {
		return t.UTC().Format("2006-01-02")
	}
	if fallbackRFC != "" {
		if t, err := time.Parse(time.RFC3339, fallbackRFC); err == nil {
			return t.UTC().Format("2006-01-02")
		}
	}
	return ""
}

func coerceTime(v interface{}) (time.Time, bool) {
	switch x := v.(type) {
	case nil:
		return time.Time{}, false
	case primitive.DateTime:
		if x == 0 {
			return time.Time{}, false
		}
		return x.Time(), true
	case time.Time:
		if x.IsZero() {
			return time.Time{}, false
		}
		return x, true
	case string:
		if x == "" {
			return time.Time{}, false
		}
		if t, err := time.Parse(time.RFC3339, x); err == nil {
			return t, true
		}
		return time.Time{}, false
	}
	return time.Time{}, false
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// nonSubDuration infers an entitlement window for one-time RC purchases
// from the product id naming convention (e.g. "community_elite_1month").
// Returns 0 when the id has no recognizable duration suffix — the UI then
// renders the row as a single purchase date with no end.
func nonSubDuration(productID string) time.Duration {
	lower := strings.ToLower(productID)
	switch {
	case strings.Contains(lower, "1month"), strings.Contains(lower, "_monthly"), strings.HasSuffix(lower, "_month"):
		return 30 * 24 * time.Hour
	case strings.Contains(lower, "1year"), strings.Contains(lower, "_annual"), strings.Contains(lower, "_yearly"):
		return 365 * 24 * time.Hour
	case strings.Contains(lower, "1week"), strings.Contains(lower, "_weekly"):
		return 7 * 24 * time.Hour
	}
	return 0
}

// planFromStripePriceID mirrors the API's own price-id → plan mapping.
// Returns ("", false) for unknown ids so the UI can show the raw price id.
func planFromStripePriceID(priceID string) (string, bool) {
	switch priceID {
	case os.Getenv("STRIPE_BASE_MONTHLY_PRICE_ID"):
		return "base", false
	case os.Getenv("STRIPE_BASE_ANNUAL_PRICE_ID"):
		return "base", true
	case os.Getenv("STRIPE_PREMIUM_MONTHLY_PRICE_ID"):
		return "premium", false
	case os.Getenv("STRIPE_PREMIUM_ANNUAL_PRICE_ID"):
		return "premium", true
	case os.Getenv("STRIPE_PREMIUM_PLUS_MONTHLY_PRICE_ID"):
		return "premium_plus", false
	case os.Getenv("STRIPE_PREMIUM_PLUS_ANNUAL_PRICE_ID"):
		return "premium_plus", true
	}
	return "", false
}
