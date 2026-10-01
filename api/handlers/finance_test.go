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

func financeBankTx(id string, amount float64, date time.Time, pending bool) models.BankTransaction {
	return models.BankTransaction{
		TransactionID: id,
		AccountID:     "acc-1",
		Name:          "tx " + id,
		Amount:        amount,
		Direction:     plaidDirection(amount),
		Date:          date,
		Pending:       pending,
		Source:        "plaid",
	}
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

	start, _ := time.Parse("2006-01", "2026-08")
	end, _ := time.Parse("2006-01", "2026-08")
	// Bank not connected: total = stripe + iap_net, expenses = 0.
	resp := buildFinanceSummary(events, nil, start, end, 0.85, false)

	assert.Len(t, resp.Months, 1)
	assert.False(t, resp.BankConnected)
	m := resp.Months[0]
	assert.Equal(t, "2026-08", m.Month)
	// iap_gross = 20 + 5 - 7 = 18; iap_net = 18 * 0.85 = 15.30
	assert.Equal(t, 10.0, m.Income.Stripe)
	assert.Equal(t, 18.0, m.Income.IAPGross)
	assert.Equal(t, 15.3, m.Income.IAPNet)
	assert.Equal(t, 25.3, m.Income.Total)
	assert.Equal(t, 0.0, m.Expenses)
	assert.Equal(t, 25.3, m.Profit)
	assert.False(t, m.Bank.Connected)
	assert.Equal(t, 0.0, m.Bank.Income)
	assert.Equal(t, 0.0, m.Bank.Expenses)
	assert.True(t, m.Sources.Stripe.Connected)
	assert.True(t, m.Sources.RevenueCat.Connected)
	assert.False(t, m.Sources.Bank.Connected)
}

func TestBuildFinanceSummary_MonthBucketingAndEmptyMonths(t *testing.T) {
	events := []models.SubscriptionEvent{
		{Provider: "stripe", EventType: "invoice.payment_succeeded", PriceUSD: 100.0, PurchasedAt: financeTime(2026, 6, 30)},
		{Provider: "stripe", EventType: "invoice.payment_succeeded", PriceUSD: 1.0, PurchasedAt: financeTime(2026, 8, 1)},
	}
	start, _ := time.Parse("2006-01", "2026-06")
	end, _ := time.Parse("2006-01", "2026-08")
	resp := buildFinanceSummary(events, nil, start, end, 0.85, false)

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
	start, _ := time.Parse("2006-01", "2026-09")
	end, _ := time.Parse("2006-01", "2026-09")

	// Default rate 0.85: 9.99 * 0.85 = 8.4915 -> 8.49
	resp := buildFinanceSummary(events, nil, start, end, 0.85, false)
	m := resp.Months[0]
	assert.Equal(t, 8.49, m.Income.IAPNet)
	assert.Equal(t, 8.49, m.Income.Total)
	assert.Equal(t, 0.0, m.Expenses)
	assert.Equal(t, 8.49, m.Profit)

	// Custom rate via env override path: 9.99 * 0.9 = 8.991 -> 8.99
	resp = buildFinanceSummary(events, nil, start, end, 0.9, false)
	assert.Equal(t, 8.99, resp.Months[0].Income.IAPNet)
}

func TestBuildFinanceSummary_BankConnectedUsesCashBasis(t *testing.T) {
	events := []models.SubscriptionEvent{
		{Provider: "stripe", EventType: "invoice.payment_succeeded", PriceUSD: 10.0, PurchasedAt: financeTime(2026, 9, 2)},
		{Provider: "revenuecat", EventType: "RENEWAL", PriceUSD: 100.0, PurchasedAt: financeTime(2026, 9, 3)},
	}
	txs := []models.BankTransaction{
		financeBankTx("in-1", -250.75, time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), false),  // income
		financeBankTx("out-1", 80.10, time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), false),   // expense
		financeBankTx("pend-1", -999.99, time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC), true), // pending: excluded
	}
	start, _ := time.Parse("2006-01", "2026-09")
	end, _ := time.Parse("2006-01", "2026-09")
	resp := buildFinanceSummary(events, txs, start, end, 0.85, true)

	assert.True(t, resp.BankConnected)
	m := resp.Months[0]
	// The subscription detail is still computed (earned-revenue complement)…
	assert.Equal(t, 10.0, m.Income.Stripe)
	assert.Equal(t, 85.0, m.Income.IAPNet) // 100 * 0.85
	// …but the P&L itself is cash basis from the bank.
	assert.Equal(t, 250.75, m.Income.Total)
	assert.Equal(t, 80.10, m.Expenses)
	assert.Equal(t, 170.65, m.Profit)
	assert.True(t, m.Bank.Connected)
	assert.Equal(t, 250.75, m.Bank.Income)
	assert.Equal(t, 80.10, m.Bank.Expenses)
	assert.True(t, m.Sources.Bank.Connected)
}

func TestBuildFinanceSummary_BankUTCBucketing(t *testing.T) {
	// 2026-09-01 00:30 +02:00 is still August in UTC — bucket must use UTC.
	tm := time.Date(2026, 9, 1, 0, 30, 0, 0, time.FixedZone("CEST", 2*3600))
	txs := []models.BankTransaction{
		financeBankTx("edge-1", -5.0, tm, false),
	}
	start, _ := time.Parse("2006-01", "2026-08")
	end, _ := time.Parse("2006-01", "2026-09")
	resp := buildFinanceSummary(nil, txs, start, end, 0.85, true)
	assert.Equal(t, 5.0, resp.Months[0].Bank.Income)
	assert.Equal(t, 0.0, resp.Months[1].Bank.Income)
}

func TestBuildFinanceSummary_ZeroAmountExcluded(t *testing.T) {
	txs := []models.BankTransaction{
		financeBankTx("zero-1", 0, time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), false),
	}
	start, _ := time.Parse("2006-01", "2026-09")
	end, _ := time.Parse("2006-01", "2026-09")
	resp := buildFinanceSummary(nil, txs, start, end, 0.85, true)
	m := resp.Months[0]
	assert.Equal(t, 0.0, m.Bank.Income)
	assert.Equal(t, 0.0, m.Bank.Expenses)
	assert.Equal(t, 0.0, m.Income.Total)
}

func TestBuildFinanceSummary_UTCBucketing(t *testing.T) {
	// 2026-08-01 00:30 +02:00 is still July in UTC — bucket must use UTC.
	tm := time.Date(2026, 8, 1, 0, 30, 0, 0, time.FixedZone("CEST", 2*3600))
	events := []models.SubscriptionEvent{
		{Provider: "stripe", EventType: "invoice.payment_succeeded", PriceUSD: 5.0, PurchasedAt: &tm},
	}
	start, _ := time.Parse("2006-01", "2026-07")
	end, _ := time.Parse("2006-01", "2026-08")
	resp := buildFinanceSummary(events, nil, start, end, 0.85, false)
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
