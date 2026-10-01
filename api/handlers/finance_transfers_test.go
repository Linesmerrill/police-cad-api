package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/linesmerrill/police-cad-api/models"
)

func bankTx(id, account string, amount float64, date string) models.BankTransaction {
	d, _ := time.Parse("2006-01-02", date)
	return models.BankTransaction{TransactionID: id, AccountID: account, Amount: amount, Date: d}
}

// Checking to savings: both ends are skipped.
func TestInternalTransferIDs_PairsAMoveBetweenLinkedAccounts(t *testing.T) {
	got := internalTransferIDs([]models.BankTransaction{
		bankTx("out", "checking", 1000, "2026-09-10"),
		bankTx("in", "savings", -1000, "2026-09-11"),
	})
	assert.Equal(t, map[string]bool{"out": true, "in": true}, got)
}

// Paying a linked card: the purchases were already counted on the card, so
// the payment and its arrival on the card are skipped, the purchase is not.
func TestInternalTransferIDs_PairsALinkedCardPayment(t *testing.T) {
	got := internalTransferIDs([]models.BankTransaction{
		bankTx("purchase", "card", 250, "2026-09-02"),
		bankTx("payment", "checking", 250, "2026-09-20"),
		bankTx("payment-received", "card", -250, "2026-09-21"),
	})
	assert.Equal(t, map[string]bool{"payment": true, "payment-received": true}, got)
}

// What must still count: one end not linked, a refund to the same account,
// amounts a cent apart, ends too far apart, and pending transactions.
func TestInternalTransferIDs_LeavesRealMoneyAlone(t *testing.T) {
	cases := map[string][]models.BankTransaction{
		"transfer to an unlinked account": {
			bankTx("draw", "checking", 500, "2026-09-10"),
		},
		"refund to the same account": {
			bankTx("buy", "checking", 40, "2026-09-10"),
			bankTx("refund", "checking", -40, "2026-09-11"),
		},
		"amounts a cent apart": {
			bankTx("out", "checking", 100.00, "2026-09-10"),
			bankTx("in", "savings", -100.01, "2026-09-10"),
		},
		"ends too far apart": {
			bankTx("out", "checking", 100, "2026-09-01"),
			bankTx("in", "savings", -100, "2026-09-20"),
		},
	}
	for name, txs := range cases {
		assert.Empty(t, internalTransferIDs(txs), name)
	}

	pending := []models.BankTransaction{
		bankTx("out", "checking", 100, "2026-09-10"),
		bankTx("in", "savings", -100, "2026-09-10"),
	}
	pending[1].Pending = true
	assert.Empty(t, internalTransferIDs(pending), "pending")
}

// Two identical transfers pair one-to-one; a third, unmatched inflow of the
// same amount is real income and must still count.
func TestInternalTransferIDs_PairsOneToOne(t *testing.T) {
	got := internalTransferIDs([]models.BankTransaction{
		bankTx("out1", "checking", 200, "2026-09-01"),
		bankTx("out2", "checking", 200, "2026-09-05"),
		bankTx("in1", "savings", -200, "2026-09-01"),
		bankTx("in2", "savings", -200, "2026-09-05"),
		bankTx("deposit", "savings", -200, "2026-09-06"),
	})
	assert.Len(t, got, 4)
	assert.False(t, got["deposit"])
	assert.True(t, got["out2"] && got["in2"], "each outflow takes its nearest match")
}

// End to end through the summary: the transfer disappears from both columns,
// real income and spending stay.
func TestBuildFinanceSummary_ExcludesInternalTransfers(t *testing.T) {
	txs := []models.BankTransaction{
		bankTx("payout", "checking", -1200, "2026-09-03"),    // revenue in
		bankTx("hosting", "checking", 300, "2026-09-05"),     // real expense
		bankTx("to-savings", "checking", 5000, "2026-09-10"), // own money moving
		bankTx("from-checking", "savings", -5000, "2026-09-10"),
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	resp := buildFinanceSummary(nil, txs, start, start, 0.85, true)

	assert.Len(t, resp.Months, 1)
	m := resp.Months[0]
	assert.Equal(t, 1200.0, m.Income.Total)
	assert.Equal(t, 300.0, m.Expenses)
	assert.Equal(t, 900.0, m.Profit)
}

func TestParseSummaryRange_IsBounded(t *testing.T) {
	_, _, err := parseSummaryRange("2020-01", "2024-12")
	assert.NoError(t, err, "60 months is allowed")
	_, _, err = parseSummaryRange("1900-01", "2999-12")
	assert.Error(t, err)
}
