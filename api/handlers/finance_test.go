package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// ---------------------------------------------------------------------------
// RequireOwner tests
// ---------------------------------------------------------------------------

const financeTestSecret = "finance-test-secret"

func financeTestToken(t *testing.T, secret string, mutate func(jwt.MapClaims)) string {
	t.Helper()
	adminID := primitive.NewObjectID()
	claims := jwt.MapClaims{
		"sub":   adminID.Hex(),
		"email": "owner@lpc.test",
		"roles": []string{"admin"},
		"scope": "admin",
		"typ":   "access",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	if mutate != nil {
		mutate(claims)
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	assert.NoError(t, err)
	return signed
}

// requireOwnerFixture builds a Finance whose admin DB returns admin (or err)
// for any FindOne, and returns a handler chain ending in a 200 marker.
func requireOwnerFixture(t *testing.T, admin *models.AdminUser, findErr error) (Finance, http.Handler) {
	adb := &mocks.AdminDatabase{}
	adb.On("FindOne", mock.Anything, mock.Anything).Return(admin, findErr)
	f := Finance{ADB: adb}
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The verified admin must be available to downstream handlers.
		assert.NotNil(t, financeAdminFromContext(r.Context()))
		w.WriteHeader(http.StatusTeapot) // 418 = reached downstream
	})
	return f, f.RequireOwner(downstream)
}

func runRequireOwner(t *testing.T, f Finance, h http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	t.Setenv("JWT_SECRET", financeTestSecret)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/finance/summary", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func financeOwnerDoc() *models.AdminUser {
	return &models.AdminUser{
		ID:     primitive.NewObjectID(),
		Email:  "owner@lpc.test",
		Role:   "admin",
		Roles:  []string{"owner", "admin"},
		Active: true,
	}
}

func TestRequireOwner_ValidOwnerPasses(t *testing.T) {
	f, h := requireOwnerFixture(t, financeOwnerDoc(), nil)
	token := financeTestToken(t, financeTestSecret, nil)
	assert.Equal(t, http.StatusTeapot, runRequireOwner(t, f, h, token).Code)
}

func TestRequireOwner_LegacyRoleOwnerPasses(t *testing.T) {
	admin := financeOwnerDoc()
	admin.Roles = []string{"admin"}
	admin.Role = "owner"
	f, h := requireOwnerFixture(t, admin, nil)
	token := financeTestToken(t, financeTestSecret, nil)
	assert.Equal(t, http.StatusTeapot, runRequireOwner(t, f, h, token).Code)
}

func TestRequireOwner_NonOwnerGets403(t *testing.T) {
	admin := financeOwnerDoc()
	admin.Roles = []string{"admin"}
	admin.Role = "admin"
	f, h := requireOwnerFixture(t, admin, nil)
	token := financeTestToken(t, financeTestSecret, nil)
	assert.Equal(t, http.StatusForbidden, runRequireOwner(t, f, h, token).Code)
}

func TestRequireOwner_BadSignatureGets401(t *testing.T) {
	f, h := requireOwnerFixture(t, financeOwnerDoc(), nil)
	token := financeTestToken(t, "wrong-secret", nil)
	assert.Equal(t, http.StatusUnauthorized, runRequireOwner(t, f, h, token).Code)
}

func TestRequireOwner_TamperedRolesClaimStill403(t *testing.T) {
	// The token claims owner, but the re-read document is not an owner.
	// This proves the middleware does not trust the claim alone.
	admin := financeOwnerDoc()
	admin.Roles = []string{"admin"}
	admin.Role = "admin"
	f, h := requireOwnerFixture(t, admin, nil)
	token := financeTestToken(t, financeTestSecret, func(c jwt.MapClaims) {
		c["roles"] = []string{"owner", "admin"}
	})
	assert.Equal(t, http.StatusForbidden, runRequireOwner(t, f, h, token).Code)
}

func TestRequireOwner_InactiveOwnerGets401(t *testing.T) {
	admin := financeOwnerDoc()
	admin.Active = false
	f, h := requireOwnerFixture(t, admin, nil)
	token := financeTestToken(t, financeTestSecret, nil)
	assert.Equal(t, http.StatusUnauthorized, runRequireOwner(t, f, h, token).Code)
}

func TestRequireOwner_ExpiredTokenGets401(t *testing.T) {
	f, h := requireOwnerFixture(t, financeOwnerDoc(), nil)
	token := financeTestToken(t, financeTestSecret, func(c jwt.MapClaims) {
		c["exp"] = time.Now().Add(-time.Hour).Unix()
	})
	assert.Equal(t, http.StatusUnauthorized, runRequireOwner(t, f, h, token).Code)
}

func TestRequireOwner_MissingHeaderGets401(t *testing.T) {
	f, h := requireOwnerFixture(t, financeOwnerDoc(), nil)
	assert.Equal(t, http.StatusUnauthorized, runRequireOwner(t, f, h, "").Code)
}

func TestRequireOwner_NonAdminScopeGets401(t *testing.T) {
	f, h := requireOwnerFixture(t, financeOwnerDoc(), nil)
	token := financeTestToken(t, financeTestSecret, func(c jwt.MapClaims) {
		c["scope"] = "user"
	})
	assert.Equal(t, http.StatusUnauthorized, runRequireOwner(t, f, h, token).Code)
}

func TestRequireOwner_UnknownAdminGets401(t *testing.T) {
	f, h := requireOwnerFixture(t, nil, assert.AnError)
	token := financeTestToken(t, financeTestSecret, nil)
	assert.Equal(t, http.StatusUnauthorized, runRequireOwner(t, f, h, token).Code)
}

// ---------------------------------------------------------------------------
// Summary aggregation tests
// ---------------------------------------------------------------------------

func financeTime(year, month, day int) *time.Time {
	t := time.Date(year, time.Month(month), day, 12, 0, 0, 0, time.UTC)
	return &t
}

func TestBuildFinanceSummary_BasicAggregation(t *testing.T) {
	events := []models.SubscriptionEvent{
		{Provider: "stripe", EventType: "invoice.payment_succeeded", PriceUSD: 10.0, PurchasedAt: financeTime(2026, 8, 5)},
		// Wrong event type: not revenue.
		{Provider: "stripe", EventType: "checkout.session.completed", PriceUSD: 999.0, PurchasedAt: financeTime(2026, 8, 5)},
		{Provider: "revenuecat", EventType: "INITIAL_PURCHASE", PriceUSD: 20.0, PurchasedAt: financeTime(2026, 8, 10)},
		{Provider: "revenuecat", EventType: "RENEWAL", PriceUSD: 5.0, PurchasedAt: financeTime(2026, 8, 11)},
		{Provider: "revenuecat", EventType: "REFUND", PriceUSD: 7.0, PurchasedAt: financeTime(2026, 8, 12)},
		// Nil purchasedAt: skipped.
		{Provider: "stripe", EventType: "invoice.payment_succeeded", PriceUSD: 50.0, PurchasedAt: nil},
		// Other providers never count as revenue.
		{Provider: "mobile_app", EventType: "mobile_subscribe", PriceUSD: 30.0, PurchasedAt: financeTime(2026, 8, 13)},
	}
	expenses := []models.FinanceExpense{
		{Date: *financeTime(2026, 8, 20), Amount: 15.0},
	}

	start, _ := time.Parse("2006-01", "2026-08")
	end, _ := time.Parse("2006-01", "2026-08")
	resp := buildFinanceSummary(events, expenses, start, end, 0.85, nil, false)

	assert.Len(t, resp.Months, 1)
	m := resp.Months[0]
	assert.Equal(t, "2026-08", m.Month)
	// iap_gross = 20 + 5 - 7 = 18; iap_net = 18 * 0.85 = 15.30
	assert.Equal(t, 10.0, m.Income.Stripe)
	assert.Equal(t, 18.0, m.Income.IAPGross)
	assert.Equal(t, 15.3, m.Income.IAPNet)
	assert.Equal(t, 0.0, m.Income.AdSense)
	assert.Equal(t, 0.0, m.Income.AdMob)
	// total = stripe + iap_net (admob 0)
	assert.Equal(t, 25.3, m.Income.Total)
	assert.Equal(t, 15.0, m.Expenses)
	assert.Equal(t, 10.3, m.Profit)
	assert.True(t, m.Sources.Stripe.Connected)
	assert.True(t, m.Sources.RevenueCat.Connected)
	assert.False(t, m.Sources.AdSense.Connected)
	assert.False(t, m.Sources.AdMob.Connected)
}

func TestBuildFinanceSummary_MonthBucketingAndEmptyMonths(t *testing.T) {
	events := []models.SubscriptionEvent{
		{Provider: "stripe", EventType: "invoice.payment_succeeded", PriceUSD: 100.0, PurchasedAt: financeTime(2026, 6, 30)},
		{Provider: "stripe", EventType: "invoice.payment_succeeded", PriceUSD: 1.0, PurchasedAt: financeTime(2026, 8, 1)},
	}
	start, _ := time.Parse("2006-01", "2026-06")
	end, _ := time.Parse("2006-01", "2026-08")
	resp := buildFinanceSummary(events, nil, start, end, 0.85, nil, false)

	assert.Len(t, resp.Months, 3)
	assert.Equal(t, "2026-06", resp.Months[0].Month)
	assert.Equal(t, 100.0, resp.Months[0].Income.Stripe)
	assert.Equal(t, "2026-07", resp.Months[1].Month)
	assert.Equal(t, 0.0, resp.Months[1].Income.Total)
	assert.Equal(t, 0.0, resp.Months[1].Expenses)
	assert.Equal(t, 0.0, resp.Months[1].Profit)
	assert.Equal(t, "2026-08", resp.Months[2].Month)
	assert.Equal(t, 1.0, resp.Months[2].Income.Stripe)
}

func TestBuildFinanceSummary_NetRateMathAndRounding(t *testing.T) {
	events := []models.SubscriptionEvent{
		{Provider: "revenuecat", EventType: "RENEWAL", PriceUSD: 9.99, PurchasedAt: financeTime(2026, 9, 2)},
	}
	expenses := []models.FinanceExpense{
		{Date: *financeTime(2026, 9, 3), Amount: 0.005}, // rounds to 0.01
	}
	start, _ := time.Parse("2006-01", "2026-09")
	end, _ := time.Parse("2006-01", "2026-09")

	// Default rate 0.85: 9.99 * 0.85 = 8.4915 -> 8.49
	resp := buildFinanceSummary(events, expenses, start, end, 0.85, nil, false)
	m := resp.Months[0]
	assert.Equal(t, 8.49, m.Income.IAPNet)
	assert.Equal(t, 8.49, m.Income.Total)
	assert.Equal(t, 0.01, m.Expenses)
	assert.Equal(t, 8.49, m.Profit) // 9.99*0.85 - 0.005, rounded once at the end

	// Custom rate via env override path: 9.99 * 0.9 = 8.991 -> 8.99
	resp = buildFinanceSummary(events, expenses, start, end, 0.9, nil, false)
	assert.Equal(t, 8.99, resp.Months[0].Income.IAPNet)
}

func TestBuildFinanceSummary_AdSenseEarnings(t *testing.T) {
	adsense := map[string]float64{"2026-09": 42.5, "2026-08": 10.0}
	start, _ := time.Parse("2006-01", "2026-08")
	end, _ := time.Parse("2006-01", "2026-09")
	resp := buildFinanceSummary(nil, nil, start, end, 0.85, adsense, true)

	assert.Equal(t, 10.0, resp.Months[0].Income.AdSense)
	assert.Equal(t, 10.0, resp.Months[0].Income.Total)
	assert.True(t, resp.Months[0].Sources.AdSense.Connected)
	assert.Equal(t, 42.5, resp.Months[1].Income.AdSense)
	assert.True(t, resp.Months[1].Sources.AdSense.Connected)
}

func TestBuildFinanceSummary_UTCBucketing(t *testing.T) {
	// 2026-08-01 00:30 +02:00 is still July in UTC — bucket must use UTC.
	tm := time.Date(2026, 8, 1, 0, 30, 0, 0, time.FixedZone("CEST", 2*3600))
	events := []models.SubscriptionEvent{
		{Provider: "stripe", EventType: "invoice.payment_succeeded", PriceUSD: 5.0, PurchasedAt: &tm},
	}
	start, _ := time.Parse("2006-01", "2026-07")
	end, _ := time.Parse("2006-01", "2026-08")
	resp := buildFinanceSummary(events, nil, start, end, 0.85, nil, false)
	assert.Equal(t, 5.0, resp.Months[0].Income.Stripe)
	assert.Equal(t, 0.0, resp.Months[1].Income.Stripe)
}

func TestParseSummaryRange(t *testing.T) {
	start, end, err := parseSummaryRange("2026-01", "2026-03")
	assert.NoError(t, err)
	assert.Equal(t, "2026-01", start.Format("2006-01"))
	assert.Equal(t, "2026-03", end.Format("2006-01"))

	_, _, err = parseSummaryRange("2026-13", "2026-03")
	assert.Error(t, err)

	_, _, err = parseSummaryRange("09-2026", "2026-03")
	assert.Error(t, err)

	_, _, err = parseSummaryRange("2026-03", "2026-01")
	assert.Error(t, err)

	_, _, err = parseSummaryRange("2026-01", "")
	assert.Error(t, err)

	// Default: last 12 months ending with the current month.
	start, end, err = parseSummaryRange("", "")
	assert.NoError(t, err)
	now := time.Now().UTC()
	assert.Equal(t, now.Format("2006-01"), end.Format("2006-01"))
	assert.Equal(t, now.AddDate(0, -11, 0).Format("2006-01"), start.Format("2006-01"))
}

func TestIAPNetRate(t *testing.T) {
	t.Setenv("IAP_NET_RATE", "")
	assert.Equal(t, 0.85, iapNetRate())

	t.Setenv("IAP_NET_RATE", "0.9")
	assert.Equal(t, 0.9, iapNetRate())

	t.Setenv("IAP_NET_RATE", "bogus")
	assert.Equal(t, 0.85, iapNetRate())

	t.Setenv("IAP_NET_RATE", "1.5")
	assert.Equal(t, 0.85, iapNetRate())
}

func TestParseExpenseDate(t *testing.T) {
	d, err := parseExpenseDate("2026-09-15")
	assert.NoError(t, err)
	assert.Equal(t, "2026-09-15", d.Format("2006-01-02"))

	_, err = parseExpenseDate("not-a-date")
	assert.Error(t, err)
}

func TestValidExpenseSource(t *testing.T) {
	assert.True(t, validExpenseSource("manual"))
	assert.True(t, validExpenseSource("csv"))
	assert.True(t, validExpenseSource("plaid"))
	assert.False(t, validExpenseSource("bank"))
	assert.False(t, validExpenseSource(""))
}
