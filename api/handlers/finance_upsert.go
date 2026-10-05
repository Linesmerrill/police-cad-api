package handlers

import (
	"context"
	"regexp"
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
func plaidUpsert(doc models.BankTransaction, rule models.FinanceTagRule) bson.M {
	onInsert := bson.M{
		"created_at": doc.CreatedAt,
		"hidden":     rule.Hide,
	}
	if rule.TagID != "" {
		onInsert["tag_id"] = rule.TagID
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
			"merchant_key_v":            merchantKeyVersion,
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

// merchantKeyVersion changes whenever merchantKey's output does, so stored
// keys can be rebuilt (migrateMerchantKeys).
const merchantKeyVersion = 2

var (
	merchantMonthWords = regexp.MustCompile(`\b(jan(uary)?|feb(ruary)?|mar(ch)?|apr(il)?|may|june?|july?|aug(ust)?|sept?(ember)?|oct(ober)?|nov(ember)?|dec(ember)?)\b`)
	merchantDigits     = regexp.MustCompile(`[0-9]+`)
	merchantPunct      = regexp.MustCompile(`[^a-z&' ]+`)
)

// merchantKey is what rules match on: the merchant name (or the description
// when Plaid has none), lowercased, without the parts that change from one
// transaction to the next. Month names, years, dates and reference numbers
// are dropped, so "Interest earned in May 2026" and "Interest earned in
// June 2026" are one merchant, as are "Transfer to Bluevine Taxes 5815" and
// "STEAM GAMES, 4029357733" each time they appear.
func merchantKey(merchantName, name string) string {
	s := strings.TrimSpace(merchantName)
	if s == "" {
		s = strings.TrimSpace(name)
	}
	s = strings.ToLower(s)
	s = merchantDigits.ReplaceAllString(s, " ")
	s = merchantMonthWords.ReplaceAllString(s, " ")
	s = merchantPunct.ReplaceAllString(s, " ")
	key := strings.Join(strings.Fields(s), " ")
	if key == "" {
		// Nothing but numbers or dates: keep the original so it still
		// identifies something.
		return strings.ToLower(strings.Join(strings.Fields(merchantName+" "+name), " "))
	}
	return key
}

// tagRulesByMerchant loads the merchant rules by merchant key. A failure to
// read them only means no rule applies this sync.
func (f Finance) tagRulesByMerchant(ctx context.Context) map[string]models.FinanceTagRule {
	out := map[string]models.FinanceTagRule{}
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
		if r.MerchantKey != "" && (r.TagID != "" || r.Hide) {
			out[r.MerchantKey] = r
		}
	}
	return out
}
