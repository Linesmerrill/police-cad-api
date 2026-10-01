package handlers

import (
	"math"
	"sort"
	"time"

	"github.com/linesmerrill/police-cad-api/models"
)

// Money moving between the owner's own accounts.
//
// The bank feed reports every account separately, so moving $1,000 from
// checking to savings arrives as $1,000 out of one and $1,000 into the other.
// Counted as-is it inflates income and expenses alike. Paying off a linked
// credit card is worse: the purchases were already counted on the card, and
// the payment from checking counts them a second time.
//
// Plaid's categories are not a safe way to spot these. A card payment is the
// only record of card spending when the card is not linked, and Plaid labels
// some genuine revenue payouts as transfers. What reliably marks an internal
// move is that both ends are visible: the same amount leaves one linked
// account and arrives in another within a few days. Only those pairs are
// excluded. A transfer to an account that is not linked, such as the owner's
// personal account, still counts, because from the business's point of view
// the money did leave.

// internalTransferWindow is how far apart the two ends of a transfer may post.
// Same-bank moves usually land the same day; ACH between banks can take a few.
const internalTransferWindow = 4 * 24 * time.Hour

// internalTransferIDs returns the transaction IDs that are one end of a move
// between two linked accounts. Pending transactions never pair: their amounts
// and dates can still change.
func internalTransferIDs(txs []models.BankTransaction) map[string]bool {
	type leg struct {
		id      string
		account string
		cents   int64
		date    time.Time
	}
	var outs, ins []leg
	for _, tx := range txs {
		if tx.Pending || tx.TransactionID == "" || tx.Amount == 0 {
			continue
		}
		l := leg{
			id:      tx.TransactionID,
			account: tx.AccountID,
			cents:   int64(math.Round(math.Abs(tx.Amount) * 100)),
			date:    tx.Date,
		}
		// Plaid's sign convention: positive is money out of the account.
		if tx.Amount > 0 {
			outs = append(outs, l)
		} else {
			ins = append(ins, l)
		}
	}
	// Oldest first, so each outflow takes the earliest matching inflow and the
	// result does not depend on the order the database returned them in.
	sort.Slice(outs, func(i, j int) bool {
		if !outs[i].date.Equal(outs[j].date) {
			return outs[i].date.Before(outs[j].date)
		}
		return outs[i].id < outs[j].id
	})
	sort.Slice(ins, func(i, j int) bool {
		if !ins[i].date.Equal(ins[j].date) {
			return ins[i].date.Before(ins[j].date)
		}
		return ins[i].id < ins[j].id
	})

	paired := map[string]bool{}
	used := make([]bool, len(ins))
	for _, o := range outs {
		best := -1
		var bestGap time.Duration
		for i, in := range ins {
			if used[i] || in.cents != o.cents || in.account == o.account {
				continue
			}
			gap := in.date.Sub(o.date)
			if gap < 0 {
				gap = -gap
			}
			if gap > internalTransferWindow {
				continue
			}
			if best == -1 || gap < bestGap {
				best, bestGap = i, gap
			}
		}
		if best >= 0 {
			used[best] = true
			paired[o.id] = true
			paired[ins[best].id] = true
		}
	}
	return paired
}
