package handlers

import (
	"encoding/json"
	"strings"
	"time"
)

// stripeInvoicePayment reads what an invoice.payment_succeeded event paid.
//
// The Stripe webhook used to record these events without a price or a date,
// so the Finance tab, which sums priceUsd by purchasedAt, showed $0 for every
// Stripe payment even though the invoice was sitting in the payload.
//
// ok is false when the payload is not an invoice, nothing was paid, or the
// invoice is not in US dollars: the Finance tab reports USD, and guessing an
// exchange rate would put a wrong number on the books.
func stripeInvoicePayment(raw []byte) (priceUSD float64, paidAt *time.Time, ok bool) {
	if len(raw) == 0 {
		return 0, nil, false
	}
	var inv struct {
		Object            string `json:"object"`
		AmountPaid        int64  `json:"amount_paid"`
		Currency          string `json:"currency"`
		Created           int64  `json:"created"`
		StatusTransitions struct {
			PaidAt int64 `json:"paid_at"`
		} `json:"status_transitions"`
	}
	if err := json.Unmarshal(raw, &inv); err != nil {
		return 0, nil, false
	}
	if inv.Object != "invoice" || inv.AmountPaid <= 0 || !strings.EqualFold(inv.Currency, "usd") {
		return 0, nil, false
	}
	ts := inv.StatusTransitions.PaidAt
	if ts == 0 {
		ts = inv.Created
	}
	if ts == 0 {
		return 0, nil, false
	}
	t := time.Unix(ts, 0).UTC()
	return float64(inv.AmountPaid) / 100, &t, true
}
