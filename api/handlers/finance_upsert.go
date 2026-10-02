package handlers

import (
	"context"
	"strings"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/linesmerrill/police-cad-api/models"
)

// plaidUpsert is the update for one transaction from Plaid.
//
// Only the fields Plaid owns are $set. Sync used to $set the whole document,
// which reset created_at on every sync and would have wiped the owner's
// hidden flag and tag whenever Plaid reported a transaction as modified.
// created_at, hidden, and a merchant rule's tag are written only when the
// transaction is first stored, so a tag the owner changed later stands.
func plaidUpsert(doc models.BankTransaction, ruleTagID string) bson.M {
	onInsert := bson.M{
		"created_at": doc.CreatedAt,
		"hidden":     false,
	}
	if ruleTagID != "" {
		onInsert["tag_id"] = ruleTagID
	}
	return bson.M{
		"$set": bson.M{
			"transaction_id":            doc.TransactionID,
			"account_id":                doc.AccountID,
			"account_name":              doc.AccountName,
			"account_mask":              doc.AccountMask,
			"name":                      doc.Name,
			"merchant_name":             doc.MerchantName,
			"merchant_key":              doc.MerchantKey,
			"amount":                    doc.Amount,
			"direction":                 doc.Direction,
			"date":                      doc.Date,
			"pending":                   doc.Pending,
			"category":                  doc.Category,
			"personal_finance_category": doc.PersonalFinanceCategory,
			"source":                    doc.Source,
			"updated_at":                doc.UpdatedAt,
		},
		"$setOnInsert": onInsert,
	}
}

// merchantKey is what a merchant rule matches: the merchant name, or the
// description when Plaid could not name a merchant, lowercased with its
// whitespace collapsed.
func merchantKey(merchantName, name string) string {
	s := strings.TrimSpace(merchantName)
	if s == "" {
		s = strings.TrimSpace(name)
	}
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// tagRulesByMerchant loads the merchant rules as merchant key -> tag ID.
// A failure to read them only means nothing is tagged this sync.
func (f Finance) tagRulesByMerchant(ctx context.Context) map[string]string {
	out := map[string]string{}
	if f.RuleDB == nil {
		return out
	}
	cur, err := f.RuleDB.Find(ctx, bson.M{})
	if err != nil {
		return out
	}
	defer cur.Close(ctx)
	var rules []models.FinanceTagRule
	if err := cur.All(ctx, &rules); err != nil {
		return out
	}
	for _, r := range rules {
		if r.MerchantKey != "" && r.TagID != "" {
			out[r.MerchantKey] = r.TagID
		}
	}
	return out
}
