package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/models"
)

// Tagging and hiding bank transactions.
//
// The owner labels transactions (Steam, Google Ads, ...) to see where money
// comes from and goes, and hides the ones that don't belong in the business's
// books. One tag per transaction, so the by-tag pie slices add up to the P&L.
// A merchant rule tags every future transaction from a merchant on sync.
//
// All routes are owner-only (RequireOwner, finance.go).

const (
	untaggedID          = "untagged"
	untaggedName        = "Untagged"
	untaggedColor       = "#64748b"
	maxTagNameLength    = 40
	defaultTxPageLimit  = 25
	maxTxPageLimit      = 100
	maxTxSearchLength   = 100
	financeQueryTimeout = 30 * time.Second
)

// tagPalette colours new tags that arrive without one, in order.
var tagPalette = []string{
	"#38bdf8", "#a78bfa", "#34d399", "#fbbf24", "#f87171",
	"#f472b6", "#22d3ee", "#a3e635", "#fb923c", "#818cf8",
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func financeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// countsTowardPL is the one rule for what the P&L and the pies include:
// settled, not hidden by the owner, and not one end of a move between the
// owner's own accounts.
func countsTowardPL(tx models.BankTransaction, internal map[string]bool) bool {
	return !tx.Pending && !tx.Hidden && !internal[tx.TransactionID]
}

// tagTotals splits money in and money out over [start, end month] by tag,
// following the same exclusions as the P&L, so each pie adds up to the
// matching P&L total. A transaction whose tag has since been deleted is
// counted as Untagged.
func tagTotals(txs []models.BankTransaction, start, end time.Time, tags map[string]models.FinanceTag) models.FinanceByTag {
	rangeEnd := end.AddDate(0, 1, 0)
	internal := internalTransferIDs(txs)
	income := map[string]float64{}
	expenses := map[string]float64{}
	for _, tx := range txs {
		if !countsTowardPL(tx, internal) || tx.Date.Before(start) || !tx.Date.Before(rangeEnd) {
			continue
		}
		key := untaggedID
		if _, ok := tags[tx.TagID]; ok {
			key = tx.TagID
		}
		switch {
		case tx.Amount < 0:
			income[key] += -tx.Amount
		case tx.Amount > 0:
			expenses[key] += tx.Amount
		}
	}
	return models.FinanceByTag{
		Income:   tagSlices(income, tags),
		Expenses: tagSlices(expenses, tags),
	}
}

// tagSlices orders the slices largest first, with Untagged always last.
func tagSlices(totals map[string]float64, tags map[string]models.FinanceTag) []models.FinanceTagTotal {
	out := []models.FinanceTagTotal{}
	for id, amount := range totals {
		if id == untaggedID || amount <= 0 {
			continue
		}
		t := tags[id]
		out = append(out, models.FinanceTagTotal{TagID: id, Name: t.Name, Color: t.Color, Amount: round2(amount)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Amount != out[j].Amount {
			return out[i].Amount > out[j].Amount
		}
		return out[i].Name < out[j].Name
	})
	if amount := totals[untaggedID]; amount > 0 {
		out = append(out, models.FinanceTagTotal{Name: untaggedName, Color: untaggedColor, Amount: round2(amount)})
	}
	return out
}

// tagsByID loads every tag. Without a tag store, everything is Untagged.
func (f Finance) tagsByID(ctx context.Context) map[string]models.FinanceTag {
	out := map[string]models.FinanceTag{}
	for _, t := range f.allTags(ctx) {
		out[t.ID.Hex()] = t
	}
	return out
}

func (f Finance) allTags(ctx context.Context) []models.FinanceTag {
	tags := []models.FinanceTag{}
	if f.TagDB == nil {
		return tags
	}
	cur, err := f.TagDB.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "name_key", Value: 1}}))
	if err != nil {
		return tags
	}
	defer cur.Close(ctx)
	_ = cur.All(ctx, &tags)
	return tags
}

func (f Finance) findTag(ctx context.Context, id string) (models.FinanceTag, error) {
	var tag models.FinanceTag
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil || f.TagDB == nil {
		return tag, errors.New("tag not found")
	}
	if err := f.TagDB.FindOne(ctx, bson.M{"_id": oid}).Decode(&tag); err != nil {
		return tag, errors.New("tag not found")
	}
	return tag, nil
}

// ---------------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------------

// transactionListFilter builds the query for the transaction list.
func transactionListFilter(start, end time.Time, tag, hidden, search string) bson.M {
	filter := bson.M{"date": bson.M{"$gte": start, "$lt": end.AddDate(0, 1, 0)}}
	switch tag {
	case "":
	case untaggedID:
		filter["tag_id"] = bson.M{"$in": bson.A{nil, ""}}
	default:
		filter["tag_id"] = tag
	}
	switch hidden {
	case "only":
		filter["hidden"] = true
	case "include":
	default:
		filter["hidden"] = bson.M{"$ne": true}
	}
	if search = strings.TrimSpace(search); search != "" {
		if len(search) > maxTxSearchLength {
			search = search[:maxTxSearchLength]
		}
		re := primitive.Regex{Pattern: regexp.QuoteMeta(search), Options: "i"}
		filter["$or"] = bson.A{bson.M{"name": re}, bson.M{"merchant_name": re}}
	}
	return filter
}

func pageParams(r *http.Request) (page, limit int64) {
	page, limit = 1, defaultTxPageLimit
	if p, err := strconv.ParseInt(r.URL.Query().Get("page"), 10, 64); err == nil && p > 0 {
		page = p
	}
	if l, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64); err == nil && l > 0 {
		limit = l
	}
	if limit > maxTxPageLimit {
		limit = maxTxPageLimit
	}
	return page, limit
}

// ListTransactionsHandler implements GET /api/v1/admin/finance/transactions.
// Query: from, to (YYYY-MM, default the last 12 months), page (1-based),
// limit (default 25, max 100), tag (an ID, or "untagged"), hidden
// ("exclude" default, "include", "only"), search.
func (f Finance) ListTransactionsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	start, end, err := parseSummaryRange(q.Get("from"), q.Get("to"))
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, limit := pageParams(r)

	ctx, cancel := context.WithTimeout(r.Context(), financeQueryTimeout)
	defer cancel()

	filter := transactionListFilter(start, end, q.Get("tag"), q.Get("hidden"), q.Get("search"))
	total, err := f.BTDB.CountDocuments(ctx, filter)
	if err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to count transactions")
		return
	}
	cur, err := f.BTDB.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "date", Value: -1}, {Key: "transaction_id", Value: -1}}).
		SetSkip((page-1)*limit).
		SetLimit(limit))
	if err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to read transactions")
		return
	}
	defer cur.Close(ctx)
	var txs []models.BankTransaction
	if err := cur.All(ctx, &txs); err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to read transactions")
		return
	}

	internal := f.internalTransfersAround(ctx, start, end)
	rules := f.tagRulesByMerchant(ctx)
	rows := make([]models.FinanceTransactionView, 0, len(txs))
	for _, tx := range txs {
		rows = append(rows, models.FinanceTransactionView{
			BankTransaction:  tx,
			InternalTransfer: internal[tx.TransactionID],
			MerchantHidden:   rules[tx.MerchantKey].Hide,
		})
	}
	financeJSON(w, http.StatusOK, map[string]interface{}{
		"data":       rows,
		"totalCount": total,
		"page":       page,
		"limit":      limit,
	})
}

// internalTransfersAround finds the transfer pairs for a range, reading a few
// days either side so a pair that straddles the boundary still matches.
func (f Finance) internalTransfersAround(ctx context.Context, start, end time.Time) map[string]bool {
	cur, err := f.BTDB.Find(ctx, bson.M{"date": bson.M{
		"$gte": start.Add(-internalTransferWindow),
		"$lt":  end.AddDate(0, 1, 0).Add(internalTransferWindow),
	}}, options.Find().SetProjection(bson.M{"transaction_id": 1, "account_id": 1, "amount": 1, "date": 1, "pending": 1}))
	if err != nil {
		return map[string]bool{}
	}
	defer cur.Close(ctx)
	var txs []models.BankTransaction
	if err := cur.All(ctx, &txs); err != nil {
		return map[string]bool{}
	}
	return internalTransferIDs(txs)
}

// transactionPatch is the body of PATCH /admin/finance/transactions/{id}.
// TagID "" removes the tag. ApplyToMerchant also tags the merchant's other
// untagged transactions and every future one.
type transactionPatch struct {
	Hidden          *bool   `json:"hidden"`
	TagID           *string `json:"tag_id"`
	ApplyToMerchant bool    `json:"apply_to_merchant"`
	// HideMerchant true hides every transaction from this merchant, now and
	// as they sync; false stops hiding them and unhides them.
	HideMerchant *bool `json:"hide_merchant"`
}

// PatchTransactionHandler implements PATCH
// /api/v1/admin/finance/transactions/{transaction_id}.
func (f Finance) PatchTransactionHandler(w http.ResponseWriter, r *http.Request) {
	txID := mux.Vars(r)["transaction_id"]
	var in transactionPatch
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeFinanceError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if in.Hidden == nil && in.TagID == nil && in.HideMerchant == nil {
		writeFinanceError(w, http.StatusBadRequest, "nothing to change: send hidden, tag_id and/or hide_merchant")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), financeQueryTimeout)
	defer cancel()

	tx, err := f.findTransaction(ctx, txID)
	if err != nil {
		writeFinanceError(w, http.StatusNotFound, "transaction not found")
		return
	}

	set := bson.M{}
	unset := bson.M{}
	var tag models.FinanceTag
	if in.TagID != nil {
		if id := strings.TrimSpace(*in.TagID); id != "" {
			if tag, err = f.findTag(ctx, id); err != nil {
				writeFinanceError(w, http.StatusBadRequest, "that tag does not exist")
				return
			}
			set["tag_id"] = tag.ID.Hex()
		} else {
			unset["tag_id"] = ""
		}
	}
	if in.Hidden != nil {
		set["hidden"] = *in.Hidden
	}
	update := bson.M{}
	if len(set) > 0 {
		update["$set"] = set
	}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	if len(update) > 0 {
		if _, err := f.BTDB.UpdateOne(ctx, bson.M{"transaction_id": txID}, update); err != nil {
			writeFinanceError(w, http.StatusInternalServerError, "failed to update the transaction")
			return
		}
	}

	merchantHidden := int64(0)
	if in.HideMerchant != nil {
		key := tx.MerchantKey
		if key == "" {
			key = merchantKey(tx.MerchantName, tx.Name)
		}
		if key == "" {
			writeFinanceError(w, http.StatusBadRequest, "this transaction has no merchant to make a rule from")
			return
		}
		if merchantHidden, err = f.applyMerchantHide(ctx, key, displayMerchant(tx), *in.HideMerchant); err != nil {
			writeFinanceError(w, http.StatusInternalServerError, "failed to save the merchant rule")
			return
		}
	}

	alsoTagged := int64(0)
	if in.ApplyToMerchant && !tag.ID.IsZero() {
		key := tx.MerchantKey
		if key == "" {
			key = merchantKey(tx.MerchantName, tx.Name)
		}
		if key == "" {
			writeFinanceError(w, http.StatusBadRequest, "this transaction has no merchant to make a rule from")
			return
		}
		if alsoTagged, err = f.applyMerchantRule(ctx, key, displayMerchant(tx), tag.ID.Hex()); err != nil {
			writeFinanceError(w, http.StatusInternalServerError, "tagged the transaction, but failed to save the merchant rule")
			return
		}
	}

	updated, err := f.findTransaction(ctx, txID)
	if err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to read the updated transaction")
		return
	}
	financeJSON(w, http.StatusOK, map[string]interface{}{
		"transaction":     updated,
		"also_tagged":     alsoTagged,
		"merchant_hidden": merchantHidden,
	})
}

func displayMerchant(tx models.BankTransaction) string {
	if m := strings.TrimSpace(tx.MerchantName); m != "" {
		return m
	}
	return strings.TrimSpace(tx.Name)
}

func (f Finance) findTransaction(ctx context.Context, txID string) (models.BankTransaction, error) {
	var tx models.BankTransaction
	if strings.TrimSpace(txID) == "" {
		return tx, errors.New("missing id")
	}
	cur, err := f.BTDB.Find(ctx, bson.M{"transaction_id": txID}, options.Find().SetLimit(1))
	if err != nil {
		return tx, err
	}
	defer cur.Close(ctx)
	var txs []models.BankTransaction
	if err := cur.All(ctx, &txs); err != nil || len(txs) == 0 {
		return tx, errors.New("not found")
	}
	return txs[0], nil
}

// applyMerchantRule saves the rule and tags the merchant's other untagged
// transactions. A transaction the owner already tagged differently keeps its
// tag. Returns how many were tagged.
func (f Finance) applyMerchantRule(ctx context.Context, key, merchant, tagID string) (int64, error) {
	if f.RuleDB == nil {
		return 0, errors.New("rules are not configured")
	}
	if _, err := f.RuleDB.UpdateOne(ctx,
		bson.M{"merchant_key": key},
		bson.M{
			"$set":         bson.M{"merchant_key": key, "merchant": merchant, "tag_id": tagID, "key_v": merchantKeyVersion},
			"$setOnInsert": bson.M{"created_at": time.Now().UTC()},
		},
		options.Update().SetUpsert(true),
	); err != nil {
		return 0, err
	}
	// Transactions synced before merchant keys existed have none yet.
	f.backfillMerchantKeys(ctx)
	res, err := f.BTDB.UpdateMany(ctx,
		bson.M{"merchant_key": key, "tag_id": bson.M{"$in": bson.A{nil, ""}}},
		bson.M{"$set": bson.M{"tag_id": tagID}})
	if err != nil {
		return 0, err
	}
	if res == nil {
		return 0, nil
	}
	return res.ModifiedCount, nil
}

// applyMerchantHide saves (hide) or clears (!hide) a rule that hides every
// transaction from a merchant, and applies it to the ones already synced.
// Returns how many transactions changed.
func (f Finance) applyMerchantHide(ctx context.Context, key, merchant string, hide bool) (int64, error) {
	if f.RuleDB == nil {
		return 0, errors.New("rules are not configured")
	}
	if hide {
		if _, err := f.RuleDB.UpdateOne(ctx,
			bson.M{"merchant_key": key},
			bson.M{
				"$set":         bson.M{"merchant_key": key, "merchant": merchant, "hide": true, "key_v": merchantKeyVersion},
				"$setOnInsert": bson.M{"created_at": time.Now().UTC()},
			},
			options.Update().SetUpsert(true),
		); err != nil {
			return 0, err
		}
	} else {
		// Keep a rule that still tags; drop one that only hid.
		if _, err := f.RuleDB.UpdateOne(ctx, bson.M{"merchant_key": key}, bson.M{"$unset": bson.M{"hide": ""}}); err != nil {
			return 0, err
		}
		if _, err := f.RuleDB.DeleteMany(ctx, bson.M{"merchant_key": key, "tag_id": bson.M{"$in": bson.A{nil, ""}}}); err != nil {
			return 0, err
		}
	}
	f.backfillMerchantKeys(ctx)
	res, err := f.BTDB.UpdateMany(ctx, bson.M{"merchant_key": key}, bson.M{"$set": bson.M{"hidden": hide}})
	if err != nil || res == nil {
		return 0, err
	}
	return res.ModifiedCount, nil
}

// backfillMerchantKeys fills merchant_key on transactions stored before it
// existed. A no-op once they all have one.
func (f Finance) backfillMerchantKeys(ctx context.Context) {
	cur, err := f.BTDB.Find(ctx, bson.M{"merchant_key_v": bson.M{"$ne": merchantKeyVersion}},
		options.Find().SetProjection(bson.M{"transaction_id": 1, "name": 1, "merchant_name": 1}))
	if err != nil {
		return
	}
	defer cur.Close(ctx)
	var txs []models.BankTransaction
	if err := cur.All(ctx, &txs); err != nil {
		return
	}
	for _, tx := range txs {
		_, _ = f.BTDB.UpdateOne(ctx, bson.M{"transaction_id": tx.TransactionID},
			bson.M{"$set": bson.M{"merchant_key": merchantKey(tx.MerchantName, tx.Name), "merchant_key_v": merchantKeyVersion}})
	}
}

// migrateMerchantKeys rebuilds merchant keys made by an older merchantKey,
// on transactions and on rules, then re-applies the rules. Rules that now
// share a key (one per month of "Interest earned in ...") merge: the newest
// one's tag and hide win. Safe to run repeatedly; runs at startup.
func (f Finance) migrateMerchantKeys(ctx context.Context) {
	f.backfillMerchantKeys(ctx)
	if f.RuleDB == nil {
		return
	}
	cur, err := f.RuleDB.Find(ctx, bson.M{"key_v": bson.M{"$ne": merchantKeyVersion}},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}))
	if err != nil {
		return
	}
	var rules []models.FinanceTagRule
	if err := cur.All(ctx, &rules); err != nil {
		cur.Close(ctx)
		return
	}
	cur.Close(ctx)
	if len(rules) == 0 {
		return
	}
	for _, r := range rules {
		key := merchantKey(r.Merchant, "")
		if key == "" {
			key = r.MerchantKey
		}
		// A rule already on the new key (newer, or migrated just before)
		// absorbs this one.
		var existing models.FinanceTagRule
		if ferr := f.RuleDB.FindOne(ctx, bson.M{"merchant_key": key, "key_v": merchantKeyVersion}).Decode(&existing); ferr == nil && existing.ID != r.ID {
			set := bson.M{}
			if existing.TagID == "" && r.TagID != "" {
				set["tag_id"] = r.TagID
			}
			if !existing.Hide && r.Hide {
				set["hide"] = true
			}
			if len(set) > 0 {
				_, _ = f.RuleDB.UpdateOne(ctx, bson.M{"_id": existing.ID}, bson.M{"$set": set})
			}
			_ = f.RuleDB.DeleteOne(ctx, bson.M{"_id": r.ID})
			continue
		}
		if _, uerr := f.RuleDB.UpdateOne(ctx, bson.M{"_id": r.ID},
			bson.M{"$set": bson.M{"merchant_key": key, "key_v": merchantKeyVersion}}); uerr != nil {
			zap.S().Warnw("failed to migrate a merchant rule", "merchant", r.Merchant, "error", uerr)
		}
	}
	// Apply the rules to what they now match.
	for key, rule := range f.tagRulesByMerchant(ctx) {
		if rule.TagID != "" {
			_, _ = f.BTDB.UpdateMany(ctx,
				bson.M{"merchant_key": key, "tag_id": bson.M{"$in": bson.A{nil, ""}}},
				bson.M{"$set": bson.M{"tag_id": rule.TagID}})
		}
		if rule.Hide {
			_, _ = f.BTDB.UpdateMany(ctx, bson.M{"merchant_key": key}, bson.M{"$set": bson.M{"hidden": true}})
		}
	}
}

// ---------------------------------------------------------------------------
// Tags
// ---------------------------------------------------------------------------

type tagInput struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

func cleanTagName(raw string) (string, error) {
	name := strings.Join(strings.Fields(raw), " ")
	if name == "" {
		return "", errors.New("a tag needs a name")
	}
	if len([]rune(name)) > maxTagNameLength {
		return "", fmt.Errorf("tag names are at most %d characters", maxTagNameLength)
	}
	return name, nil
}

// tagNameTaken reports whether another tag already has this name, ignoring
// case. exceptID excludes the tag being renamed.
func (f Finance) tagNameTaken(ctx context.Context, nameKey, exceptID string) bool {
	for _, t := range f.allTags(ctx) {
		if t.NameKey == nameKey && t.ID.Hex() != exceptID {
			return true
		}
	}
	return false
}

// ListTagsHandler implements GET /api/v1/admin/finance/tags.
func (f Finance) ListTagsHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), financeQueryTimeout)
	defer cancel()
	financeJSON(w, http.StatusOK, map[string]interface{}{"tags": f.allTags(ctx)})
}

// CreateTagHandler implements POST /api/v1/admin/finance/tags
// {name, color?}. Without a colour the next one from the palette is used.
func (f Finance) CreateTagHandler(w http.ResponseWriter, r *http.Request) {
	var in tagInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == nil {
		writeFinanceError(w, http.StatusBadRequest, "a tag needs a name")
		return
	}
	name, err := cleanTagName(*in.Name)
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), financeQueryTimeout)
	defer cancel()
	if f.TagDB == nil {
		writeFinanceError(w, http.StatusServiceUnavailable, "tags are not configured")
		return
	}

	existing := f.allTags(ctx)
	color := tagPalette[len(existing)%len(tagPalette)]
	if in.Color != nil && *in.Color != "" {
		if !hexColor.MatchString(*in.Color) {
			writeFinanceError(w, http.StatusBadRequest, "colour must look like #38bdf8")
			return
		}
		color = strings.ToLower(*in.Color)
	}
	nameKey := strings.ToLower(name)
	if f.tagNameTaken(ctx, nameKey, "") {
		writeFinanceError(w, http.StatusConflict, "a tag with that name already exists")
		return
	}

	tag := models.FinanceTag{ID: primitive.NewObjectID(), Name: name, NameKey: nameKey, Color: color, CreatedAt: time.Now().UTC()}
	if _, err := f.TagDB.InsertOne(ctx, tag); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			writeFinanceError(w, http.StatusConflict, "a tag with that name already exists")
			return
		}
		writeFinanceError(w, http.StatusInternalServerError, "failed to create the tag")
		return
	}
	financeJSON(w, http.StatusCreated, map[string]interface{}{"tag": tag})
}

// PatchTagHandler implements PATCH /api/v1/admin/finance/tags/{id}
// {name?, color?}.
func (f Finance) PatchTagHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var in tagInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || (in.Name == nil && in.Color == nil) {
		writeFinanceError(w, http.StatusBadRequest, "send a name and/or a colour")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), financeQueryTimeout)
	defer cancel()
	tag, err := f.findTag(ctx, id)
	if err != nil {
		writeFinanceError(w, http.StatusNotFound, "tag not found")
		return
	}

	set := bson.M{}
	if in.Name != nil {
		name, err := cleanTagName(*in.Name)
		if err != nil {
			writeFinanceError(w, http.StatusBadRequest, err.Error())
			return
		}
		nameKey := strings.ToLower(name)
		if f.tagNameTaken(ctx, nameKey, id) {
			writeFinanceError(w, http.StatusConflict, "a tag with that name already exists")
			return
		}
		set["name"], set["name_key"] = name, nameKey
		tag.Name, tag.NameKey = name, nameKey
	}
	if in.Color != nil {
		if !hexColor.MatchString(*in.Color) {
			writeFinanceError(w, http.StatusBadRequest, "colour must look like #38bdf8")
			return
		}
		set["color"] = strings.ToLower(*in.Color)
		tag.Color = strings.ToLower(*in.Color)
	}
	if _, err := f.TagDB.UpdateOne(ctx, bson.M{"_id": tag.ID}, bson.M{"$set": set}); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			writeFinanceError(w, http.StatusConflict, "a tag with that name already exists")
			return
		}
		writeFinanceError(w, http.StatusInternalServerError, "failed to update the tag")
		return
	}
	financeJSON(w, http.StatusOK, map[string]interface{}{"tag": tag})
}

// DeleteTagHandler implements DELETE /api/v1/admin/finance/tags/{id}. Its
// transactions become untagged and its merchant rules go with it.
func (f Finance) DeleteTagHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	ctx, cancel := context.WithTimeout(r.Context(), financeQueryTimeout)
	defer cancel()
	tag, err := f.findTag(ctx, id)
	if err != nil {
		writeFinanceError(w, http.StatusNotFound, "tag not found")
		return
	}
	res, err := f.BTDB.UpdateMany(ctx, bson.M{"tag_id": id}, bson.M{"$unset": bson.M{"tag_id": ""}})
	if err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to untag its transactions")
		return
	}
	rulesRemoved := int64(0)
	if f.RuleDB != nil {
		// A rule that also hides keeps hiding; it just stops tagging.
		if hideCur, ferr := f.RuleDB.Find(ctx, bson.M{"tag_id": id, "hide": true}); ferr == nil {
			var hideRules []models.FinanceTagRule
			_ = hideCur.All(ctx, &hideRules)
			hideCur.Close(ctx)
			for _, hr := range hideRules {
				_, _ = f.RuleDB.UpdateOne(ctx, bson.M{"_id": hr.ID}, bson.M{"$unset": bson.M{"tag_id": ""}})
			}
		}
		if rulesRemoved, err = f.RuleDB.DeleteMany(ctx, bson.M{"tag_id": id}); err != nil {
			writeFinanceError(w, http.StatusInternalServerError, "failed to remove its merchant rules")
			return
		}
	}
	if err := f.TagDB.DeleteOne(ctx, bson.M{"_id": tag.ID}); err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to delete the tag")
		return
	}
	untagged := int64(0)
	if res != nil {
		untagged = res.ModifiedCount
	}
	financeJSON(w, http.StatusOK, map[string]interface{}{
		"deleted":       true,
		"untagged":      untagged,
		"rules_removed": rulesRemoved,
	})
}

// ---------------------------------------------------------------------------
// Merchant rules
// ---------------------------------------------------------------------------

// ListTagRulesHandler implements GET /api/v1/admin/finance/tag-rules.
func (f Finance) ListTagRulesHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), financeQueryTimeout)
	defer cancel()
	rules := []models.FinanceTagRule{}
	if f.RuleDB != nil {
		if cur, err := f.RuleDB.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "merchant_key", Value: 1}})); err == nil {
			defer cur.Close(ctx)
			_ = cur.All(ctx, &rules)
		}
	}
	financeJSON(w, http.StatusOK, map[string]interface{}{"rules": rules})
}

// DeleteTagRuleHandler implements DELETE /api/v1/admin/finance/tag-rules/{id}.
// Transactions it already tagged keep their tag.
func (f Finance) DeleteTagRuleHandler(w http.ResponseWriter, r *http.Request) {
	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil || f.RuleDB == nil {
		writeFinanceError(w, http.StatusNotFound, "rule not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), financeQueryTimeout)
	defer cancel()
	if err := f.RuleDB.DeleteOne(ctx, bson.M{"_id": oid}); err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to delete the rule")
		return
	}
	financeJSON(w, http.StatusOK, map[string]interface{}{"deleted": true})
}
