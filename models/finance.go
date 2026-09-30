package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Expense sources recognized by the finance module. "plaid" is accepted but
// reserved for the v2 bank auto-sync (see FINANCE.md) — nothing writes it yet.
const (
	ExpenseSourceManual = "manual"
	ExpenseSourceCSV    = "csv"
	ExpenseSourcePlaid  = "plaid"
)

// FinanceExpense is one owner-tracked expense row in the finance_expenses
// collection. It is the counterpart to revenue aggregated from
// subscription_events.
type FinanceExpense struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Date      time.Time          `bson:"date" json:"date"`
	Amount    float64            `bson:"amount" json:"amount"`
	Currency  string             `bson:"currency" json:"currency"`
	Category  string             `bson:"category,omitempty" json:"category,omitempty"`
	Vendor    string             `bson:"vendor,omitempty" json:"vendor,omitempty"`
	Notes     string             `bson:"notes,omitempty" json:"notes,omitempty"`
	Source    string             `bson:"source" json:"source"`
	CreatedBy string             `bson:"createdBy" json:"createdBy"`
	CreatedAt time.Time          `bson:"createdAt" json:"createdAt"`
}

// FinanceExpenseDTO is the API representation of a FinanceExpense. The date
// is rendered as "YYYY-MM-DD" (UTC) so consumers never have to parse RFC3339.
type FinanceExpenseDTO struct {
	ID        string `json:"id"`
	Date      string `json:"date"`
	Amount    float64 `json:"amount"`
	Currency  string `json:"currency"`
	Category  string `json:"category,omitempty"`
	Vendor    string `json:"vendor,omitempty"`
	Notes     string `json:"notes,omitempty"`
	Source    string `json:"source"`
	CreatedBy string `json:"createdBy"`
	CreatedAt string `json:"createdAt"`
}

// FinanceSourceStatus reports whether a revenue source is connected for a month.
type FinanceSourceStatus struct {
	Connected bool `json:"connected"`
}

// FinanceMonthSources describes per-source connectivity for one month.
type FinanceMonthSources struct {
	Stripe     FinanceSourceStatus `json:"stripe"`
	RevenueCat FinanceSourceStatus `json:"revenuecat"`
	AdSense    FinanceSourceStatus `json:"adsense"`
	AdMob      FinanceSourceStatus `json:"admob"`
}

// FinanceMonthIncome is the income breakdown for one month. IAP figures come
// from RevenueCat webhook events in subscription_events; iap_net is an
// *estimate* of iap_gross minus the app-store cut (see IAP_NET_RATE).
type FinanceMonthIncome struct {
	Stripe   float64 `json:"stripe"`
	IAPGross float64 `json:"iap_gross"`
	IAPNet   float64 `json:"iap_net"`
	AdSense  float64 `json:"adsense"`
	AdMob    float64 `json:"admob"`
	// Total is income recognized for P&L: stripe + iap_net + adsense + admob.
	Total float64 `json:"total"`
}

// FinanceMonth is one month of the P&L. Field names are a contract with the
// web frontend (Worker B) — do not rename.
type FinanceMonth struct {
	Month    string              `json:"month"`
	Income   FinanceMonthIncome  `json:"income"`
	Expenses float64             `json:"expenses"`
	Profit   float64             `json:"profit"`
	Sources  FinanceMonthSources `json:"sources"`
}

// FinanceSummaryResponse is the GET /admin/finance/summary payload.
type FinanceSummaryResponse struct {
	Months   []FinanceMonth `json:"months"`
	Warnings []string       `json:"warnings,omitempty"`
}

// FinanceExpenseListResponse is the GET /admin/finance/expenses payload.
type FinanceExpenseListResponse struct {
	Expenses []FinanceExpenseDTO `json:"expenses"`
	Total    float64             `json:"total"`
}
