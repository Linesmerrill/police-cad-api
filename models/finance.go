package models

import (
	"time"
)

// Bank sync direction values. Plaid signs transaction amounts so that
// positive = money OUT of the account and negative = money IN. The direction
// field on BankTransaction records this derivation.
const (
	BankDirectionIn   = "in"
	BankDirectionOut  = "out"
	BankDirectionNone = "none" // zero-amount edge case
)

// BankTransaction is one Plaid transaction row in the bank_transactions
// collection. Money amounts keep Plaid's sign convention: amount < 0 is an
// inflow (income), amount > 0 is an outflow (expense). The access token that
// produced the row is NEVER stored in the DB — it lives in the
// PLAID_ACCESS_TOKEN env var.
type BankTransaction struct {
	TransactionID          string    `bson:"transaction_id" json:"transaction_id"`
	AccountID              string    `bson:"account_id" json:"account_id"`
	AccountName            string    `bson:"account_name,omitempty" json:"account_name,omitempty"`
	AccountMask            string    `bson:"account_mask,omitempty" json:"account_mask,omitempty"`
	Name                   string    `bson:"name" json:"name"`
	MerchantName           string    `bson:"merchant_name,omitempty" json:"merchant_name,omitempty"`
	Amount                 float64   `bson:"amount" json:"amount"` // Plaid-signed
	Direction              string    `bson:"direction" json:"direction"` // "in" | "out" | "none"
	Date                   time.Time `bson:"date" json:"date"`           // posted date (UTC)
	Pending                bool      `bson:"pending" json:"pending"`
	Category               []string  `bson:"category,omitempty" json:"category,omitempty"`
	PersonalFinanceCategory  string    `bson:"personal_finance_category,omitempty" json:"personal_finance_category,omitempty"`
	Source                 string    `bson:"source" json:"source"` // always "plaid"
	CreatedAt              time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt              time.Time `bson:"updated_at" json:"updated_at"`
}

// PlaidAccountSnapshot is one linked account stored in finance_plaid_state.
type PlaidAccountSnapshot struct {
	AccountID string `bson:"account_id" json:"account_id"`
	Name      string `bson:"name" json:"name"`
	Mask      string `bson:"mask,omitempty" json:"mask,omitempty"`
	Type      string `bson:"type,omitempty" json:"type,omitempty"`
	Subtype   string `bson:"subtype,omitempty" json:"subtype,omitempty"`
}

// PlaidSyncState is the single document in finance_plaid_state that tracks a
// Plaid transactions/sync cursor. The access token is NEVER stored here — it
// comes from the PLAID_ACCESS_TOKEN env var.
type PlaidSyncState struct {
	ItemID    string                `bson:"item_id,omitempty" json:"item_id,omitempty"`
	Cursor    string                `bson:"cursor,omitempty" json:"cursor,omitempty"`
	LastSyncAt time.Time            `bson:"last_sync_at,omitempty" json:"last_sync_at,omitempty"`
	Accounts  []PlaidAccountSnapshot `bson:"accounts,omitempty" json:"accounts,omitempty"`
	UpdatedAt time.Time             `bson:"updated_at" json:"updated_at"`
}

// FinanceSourceStatus reports whether a revenue source is connected for a month.
type FinanceSourceStatus struct {
	// Connected means data has arrived from this source. For Stripe and
	// RevenueCat that is "at least one revenue event has ever been
	// recorded", not a constant: both used to read Connected while every
	// payment was missing.
	Connected bool `json:"connected"`
	// Events is how many revenue events (payments, purchases, renewals) fall
	// in the period. Set on the summary-level sources only.
	Events int `json:"events,omitempty"`
}

// FinanceMonthSources describes per-source connectivity for one month.
type FinanceMonthSources struct {
	Stripe     FinanceSourceStatus `json:"stripe"`
	RevenueCat FinanceSourceStatus `json:"revenuecat"`
	Bank       FinanceSourceStatus `json:"bank"`
}

// FinanceMonthIncome is the income breakdown for one month. Stripe and IAP
// figures come from subscription_events (earned-revenue complement); the
// P&L total itself is the bank income when a bank is connected (cash basis).
type FinanceMonthIncome struct {
	Stripe   float64 `json:"stripe"`
	IAPGross float64 `json:"iap_gross"`
	IAPNet   float64 `json:"iap_net"`
	// Total is the P&L income: bank income when connected, otherwise
	// stripe + iap_net.
	Total float64 `json:"total"`
}

// FinanceBankMonth is the bank-sourced income/expense breakdown for one
// month. Income = sum of |amount| where amount < 0 (inflows); Expenses =
// sum of amount where amount > 0 (outflows). Pending transactions are
// excluded.
type FinanceBankMonth struct {
	Connected bool    `json:"connected"`
	Income    float64 `json:"income"`
	Expenses  float64 `json:"expenses"`
}

// FinanceMonth is one month of the P&L. Field names are a contract with the
// web frontend (Worker B) — do not rename.
type FinanceMonth struct {
	Month    string              `json:"month"`
	Income   FinanceMonthIncome  `json:"income"`
	Expenses float64             `json:"expenses"`
	Profit   float64             `json:"profit"`
	Bank     FinanceBankMonth    `json:"bank"`
	Sources  FinanceMonthSources `json:"sources"`
}

// FinanceSummaryResponse is the GET /admin/finance/summary payload.
type FinanceSummaryResponse struct {
	Months        []FinanceMonth `json:"months"`
	BankConnected bool           `json:"bank_connected"`
	// Sources is the status of each source over the whole requested range,
	// with event counts, for the badges above the P&L.
	Sources  FinanceMonthSources `json:"sources"`
	Warnings []string            `json:"warnings,omitempty"`
}
