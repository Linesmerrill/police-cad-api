package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/linesmerrill/police-cad-api/models"
)

func TestStripeInvoicePayment(t *testing.T) {
	paid := []byte(`{"object":"invoice","amount_paid":499,"currency":"usd","created":1700000000,"status_transitions":{"paid_at":1700000300}}`)
	price, at, ok := stripeInvoicePayment(paid)
	assert.True(t, ok)
	assert.Equal(t, 4.99, price)
	assert.Equal(t, time.Unix(1700000300, 0).UTC(), *at, "paid_at wins over created")

	noPaidAt := []byte(`{"object":"invoice","amount_paid":1000,"currency":"USD","created":1700000000}`)
	price, at, ok = stripeInvoicePayment(noPaidAt)
	assert.True(t, ok, "currency is case-insensitive")
	assert.Equal(t, 10.0, price)
	assert.Equal(t, time.Unix(1700000000, 0).UTC(), *at, "falls back to created")

	for name, raw := range map[string]string{
		"not usd":        `{"object":"invoice","amount_paid":1000,"currency":"eur","created":1700000000}`,
		"nothing paid":   `{"object":"invoice","amount_paid":0,"currency":"usd","created":1700000000}`,
		"not an invoice": `{"object":"subscription","amount_paid":1000,"currency":"usd","created":1700000000}`,
		"no date":        `{"object":"invoice","amount_paid":1000,"currency":"usd"}`,
		"not json":       `nope`,
		"empty":          ``,
	} {
		_, _, ok := stripeInvoicePayment([]byte(raw))
		assert.False(t, ok, name)
	}
}

// Badges come from the data: a source with no revenue events in the range,
// and none ever, reads disconnected.
func TestBuildFinanceSummary_CountsRevenueEventsPerSource(t *testing.T) {
	events := []models.SubscriptionEvent{
		{Provider: "stripe", EventType: "invoice.payment_succeeded", PriceUSD: 4.99, PurchasedAt: financeTime(2026, 8, 3)},
		{Provider: "stripe", EventType: "invoice.payment_succeeded", PriceUSD: 4.99, PurchasedAt: financeTime(2026, 8, 9)},
		{Provider: "stripe", EventType: "invoice.payment_failed", PurchasedAt: financeTime(2026, 8, 9)},
		{Provider: "revenuecat", EventType: "CANCELLATION", PurchasedAt: financeTime(2026, 8, 9)},
	}
	start, _ := time.Parse("2006-01", "2026-08")
	resp := buildFinanceSummary(events, nil, start, start, 0.85, false)

	assert.Equal(t, models.FinanceSourceStatus{Connected: true, Events: 2}, resp.Sources.Stripe)
	assert.Equal(t, models.FinanceSourceStatus{}, resp.Sources.RevenueCat,
		"cancellations are not revenue: this is the state that read 'Connected' at $0")
	assert.False(t, resp.Months[0].Sources.RevenueCat.Connected)
}

// A non-renewing purchase (lifetime unlock, one-off) is revenue too.
func TestBuildFinanceSummary_CountsNonRenewingPurchases(t *testing.T) {
	events := []models.SubscriptionEvent{
		{Provider: "revenuecat", EventType: "NON_RENEWING_PURCHASE", PriceUSD: 20, PurchasedAt: financeTime(2026, 8, 3)},
	}
	start, _ := time.Parse("2006-01", "2026-08")
	resp := buildFinanceSummary(events, nil, start, start, 1, false)
	assert.Equal(t, 20.0, resp.Months[0].Income.IAPGross)
	assert.Equal(t, 1, resp.Sources.RevenueCat.Events)
}

// A quiet range is not a broken integration.
func TestApplySourceHistory(t *testing.T) {
	start, _ := time.Parse("2006-01", "2026-08")
	resp := buildFinanceSummary(nil, nil, start, start.AddDate(0, 1, 0), 0.85, false)
	applySourceHistory(&resp, true, false)

	assert.True(t, resp.Sources.Stripe.Connected)
	assert.Equal(t, 0, resp.Sources.Stripe.Events)
	assert.False(t, resp.Sources.RevenueCat.Connected)
	for _, m := range resp.Months {
		assert.True(t, m.Sources.Stripe.Connected)
		assert.False(t, m.Sources.RevenueCat.Connected)
	}
}

func TestRevenueEventFilter(t *testing.T) {
	f := revenueEventFilter("stripe")
	assert.Equal(t, "invoice.payment_succeeded", f["eventType"])
	assert.NotNil(t, f["environment"], "sandbox is excluded")

	rc := revenueEventFilter("revenuecat")
	assert.ElementsMatch(t,
		[]string{"INITIAL_PURCHASE", "RENEWAL", "NON_RENEWING_PURCHASE"},
		rc["eventType"].(bson.M)["$in"])
}
