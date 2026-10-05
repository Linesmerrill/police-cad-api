package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/plaid/plaid-go/v39/plaid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// fakeDocDB is an in-memory databases.FinanceDocDatabase for tags and rules.
// It matches on equality only, which is all the handlers use.
type fakeDocDB struct {
	docs []bson.M
}

func toM(v interface{}) bson.M {
	b, _ := bson.Marshal(v)
	m := bson.M{}
	_ = bson.Unmarshal(b, &m)
	return m
}

// match handles equality plus the $ne and $in the rule code uses. A missing
// field compares as nil, the way Mongo matches {field: null}.
func (f *fakeDocDB) match(doc, filter bson.M) bool {
	for k, v := range filter {
		got, present := doc[k]
		if !present {
			got = nil
		}
		if ops, ok := v.(bson.M); ok {
			if ne, ok := ops["$ne"]; ok && fmt.Sprint(got) == fmt.Sprint(ne) {
				return false
			}
			if in, ok := ops["$in"].(bson.A); ok {
				found := false
				for _, want := range in {
					if (want == nil && (got == nil || got == "")) || (want != nil && fmt.Sprint(got) == fmt.Sprint(want)) {
						found = true
					}
				}
				if !found {
					return false
				}
			}
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(v) {
			return false
		}
	}
	return true
}

func (f *fakeDocDB) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) databases.SingleResultHelper {
	fm, _ := filter.(bson.M)
	for _, d := range f.docs {
		if f.match(d, fm) {
			return &fakeSingleResult{value: d}
		}
	}
	return &fakeSingleResult{err: mongo.ErrNoDocuments}
}

func (f *fakeDocDB) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*databases.MongoCursor, error) {
	fm, _ := filter.(bson.M)
	out := []interface{}{}
	for _, d := range f.docs {
		if f.match(d, fm) {
			out = append(out, d)
		}
	}
	cur, err := databases.NewMongoCursorFromDocuments(out)
	return &cur, err
}

func (f *fakeDocDB) InsertOne(ctx context.Context, doc interface{}, opts ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	f.docs = append(f.docs, toM(doc))
	return nil, nil
}

func (f *fakeDocDB) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	fm, _ := filter.(bson.M)
	um, _ := update.(bson.M)
	for _, d := range f.docs {
		if f.match(d, fm) {
			for k, v := range toM(um["$set"]) {
				d[k] = v
			}
			for k := range toM(um["$unset"]) {
				delete(d, k)
			}
			return &mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil
		}
	}
	for _, o := range opts {
		if o != nil && o.Upsert != nil && *o.Upsert {
			d := bson.M{"_id": primitive.NewObjectID()}
			for k, v := range toM(um["$set"]) {
				d[k] = v
			}
			for k, v := range toM(um["$setOnInsert"]) {
				d[k] = v
			}
			f.docs = append(f.docs, d)
			return &mongo.UpdateResult{UpsertedCount: 1}, nil
		}
	}
	return &mongo.UpdateResult{}, nil
}

func (f *fakeDocDB) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	fm, _ := filter.(bson.M)
	for i, d := range f.docs {
		if f.match(d, fm) {
			f.docs = append(f.docs[:i], f.docs[i+1:]...)
			return nil
		}
	}
	return nil
}

func (f *fakeDocDB) DeleteMany(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) (int64, error) {
	fm, _ := filter.(bson.M)
	kept, n := []bson.M{}, int64(0)
	for _, d := range f.docs {
		if f.match(d, fm) {
			n++
			continue
		}
		kept = append(kept, d)
	}
	f.docs = kept
	return n, nil
}

func mustTag(t *testing.T, db *fakeDocDB, name, color string) models.FinanceTag {
	t.Helper()
	tag := models.FinanceTag{ID: primitive.NewObjectID(), Name: name, NameKey: name, Color: color}
	db.docs = append(db.docs, toM(tag))
	return tag
}

func tx(id, account, merchant string, amount float64, date string) models.BankTransaction {
	d, _ := time.Parse("2006-01-02", date)
	return models.BankTransaction{
		TransactionID: id, AccountID: account, MerchantName: merchant,
		MerchantKey: merchantKey(merchant, ""), Amount: amount, Date: d,
	}
}

func financeWithTags() (Finance, *fakeBankTxDB, *fakeDocDB, *fakeDocDB) {
	btdb, tags, rules := newFakeBankTxDB(), &fakeDocDB{}, &fakeDocDB{}
	return Finance{BTDB: btdb, TagDB: tags, RuleDB: rules}, btdb, tags, rules
}

// The owner's choices must survive Plaid reporting the same transaction as
// modified. Sync used to $set the whole document, which reset them.
func TestRunPlaidSync_KeepsHiddenTagAndCreatedAt(t *testing.T) {
	f, btdb, tags, _ := financeWithTags()
	steam := mustTag(t, tags, "Steam", "#38bdf8")

	first := &fakePlaidClient{pages: []plaidSyncPage{{
		Added:    []plaid.Transaction{plaidTestTx("a", "acc-1", "STEAM PAYOUT", -500, "2026-09-01", false)},
		Accounts: []plaid.AccountBase{plaidTestAccount()},
	}}}
	_, _, _, _, _, err := f.runPlaidSync(context.Background(), first, "tok", "")
	require.NoError(t, err)
	created := btdb.docs["a"].CreatedAt

	// The owner tags and hides it.
	_, err = btdb.UpdateOne(context.Background(), bson.M{"transaction_id": "a"},
		bson.M{"$set": bson.M{"tag_id": steam.ID.Hex(), "hidden": true}})
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)
	second := &fakePlaidClient{pages: []plaidSyncPage{{
		Modified: []plaid.Transaction{plaidTestTx("a", "acc-1", "STEAM PAYOUT", -550, "2026-09-01", false)},
		Accounts: []plaid.AccountBase{plaidTestAccount()},
	}}}
	_, _, _, _, _, err = f.runPlaidSync(context.Background(), second, "tok", "")
	require.NoError(t, err)

	got := btdb.docs["a"]
	assert.Equal(t, -550.0, got.Amount, "Plaid's fields update")
	assert.Equal(t, steam.ID.Hex(), got.TagID, "the owner's tag stays")
	assert.True(t, got.Hidden, "the owner's hide stays")
	assert.True(t, got.CreatedAt.Equal(created), "created_at is not reset")
}

// A merchant rule tags new transactions on arrival, and never re-tags one
// the owner has already changed.
func TestRunPlaidSync_AppliesMerchantRulesToNewTransactions(t *testing.T) {
	f, btdb, tags, rules := financeWithTags()
	steam := mustTag(t, tags, "Steam", "#38bdf8")
	rules.docs = append(rules.docs, toM(models.FinanceTagRule{ID: primitive.NewObjectID(), MerchantKey: "steam", TagID: steam.ID.Hex()}))

	steamTx := plaidTestTx("s1", "acc-1", "STEAM PURCHASE 123", -20, "2026-09-02", false)
	steamTx.MerchantName = *plaid.NewNullableString(plaid.PtrString("Steam"))
	client := &fakePlaidClient{pages: []plaidSyncPage{{
		Added:    []plaid.Transaction{steamTx, plaidTestTx("o1", "acc-1", "Coffee", 5, "2026-09-02", false)},
		Accounts: []plaid.AccountBase{plaidTestAccount()},
	}}}
	_, _, _, _, _, err := f.runPlaidSync(context.Background(), client, "tok", "")
	require.NoError(t, err)

	assert.Equal(t, steam.ID.Hex(), btdb.docs["s1"].TagID)
	assert.Empty(t, btdb.docs["o1"].TagID)
}

func TestMerchantKey(t *testing.T) {
	assert.Equal(t, "steam", merchantKey("  Steam ", "STEAM PURCHASE 123"))
	assert.Equal(t, "google ads", merchantKey("", "GOOGLE   ADS  99"))
	assert.Equal(t, "", merchantKey("", "  "))

	// What changes between occurrences of the same merchant is dropped.
	cases := map[string][]string{
		"interest earned in":         {"Interest earned in May 2026", "Interest earned in June 2026", "Interest earned in Sept 2026"},
		"transfer to bluevine taxes": {"Transfer to Bluevine Taxes 5815", "Transfer to Bluevine Taxes 5815 "},
		"steam games":                {"STEAM GAMES, 4029357733", "STEAM GAMES, 4029350001"},
		"google adsense":             {"GOOGLE, ADSENSE:63", "GOOGLE, ADSENSE:71"},
	}
	for want, names := range cases {
		for _, n := range names {
			assert.Equal(t, want, merchantKey("", n), n)
		}
	}
	// Different merchants stay different.
	assert.NotEqual(t, merchantKey("", "Transfer to Bluevine Taxes 5815"), merchantKey("", "Transfer to Bluevine Savings 1234"))
	// Only numbers: kept, so it still names something.
	assert.Equal(t, "12345", merchantKey("", "12345"))
	// "may" inside a word is untouched.
	assert.Equal(t, "maytag store", merchantKey("", "Maytag Store"))
}

func TestHideMerchant_HidesPastAndFuture(t *testing.T) {
	ruleDB := &fakeDocDB{}
	btdb := newFakeBankTxDB()
	key := merchantKey("", "Transfer to Bluevine Taxes 5815")
	btdb.docs["t1"] = models.BankTransaction{TransactionID: "t1", Name: "Transfer to Bluevine Taxes 5815", MerchantKey: key, MerchantKeyVersion: merchantKeyVersion, Source: "plaid"}
	btdb.docs["t2"] = models.BankTransaction{TransactionID: "t2", Name: "Transfer to Bluevine Taxes 5815", MerchantKey: key, MerchantKeyVersion: merchantKeyVersion, Source: "plaid"}
	btdb.docs["t3"] = models.BankTransaction{TransactionID: "t3", Name: "Heroku", MerchantKey: "heroku", MerchantKeyVersion: merchantKeyVersion, Source: "plaid"}
	f := Finance{BTDB: btdb, RuleDB: ruleDB}

	n, err := f.applyMerchantHide(context.Background(), key, "Transfer to Bluevine Taxes 5815", true)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
	assert.True(t, btdb.docs["t1"].Hidden)
	assert.True(t, btdb.docs["t2"].Hidden)
	assert.False(t, btdb.docs["t3"].Hidden)

	// A transaction arriving later is hidden as it's stored.
	rule := f.tagRulesByMerchant(context.Background())[key]
	assert.True(t, rule.Hide)
	up := plaidUpsert(models.BankTransaction{TransactionID: "t4", MerchantKey: key}, rule)
	assert.Equal(t, true, up["$setOnInsert"].(bson.M)["hidden"])

	// Turning it off unhides them and drops a hide-only rule.
	_, err = f.applyMerchantHide(context.Background(), key, "Transfer to Bluevine Taxes 5815", false)
	require.NoError(t, err)
	assert.False(t, btdb.docs["t1"].Hidden)
	assert.Empty(t, f.tagRulesByMerchant(context.Background()))
}

func TestMigrateMerchantKeys_MergesMonthlyRulesAndRetags(t *testing.T) {
	tagID := primitive.NewObjectID().Hex()
	ruleDB := &fakeDocDB{docs: []bson.M{
		toM(models.FinanceTagRule{ID: primitive.NewObjectID(), MerchantKey: "interest earned in may 2026", Merchant: "Interest earned in May 2026", TagID: tagID, CreatedAt: time.Now().Add(-time.Hour)}),
		toM(models.FinanceTagRule{ID: primitive.NewObjectID(), MerchantKey: "interest earned in june 2026", Merchant: "Interest earned in June 2026", TagID: tagID, CreatedAt: time.Now()}),
	}}
	btdb := newFakeBankTxDB()
	btdb.docs["i1"] = models.BankTransaction{TransactionID: "i1", Name: "Interest earned in July 2026", MerchantKey: "interest earned in july 2026", Source: "plaid"}
	f := Finance{BTDB: btdb, RuleDB: ruleDB}

	f.migrateMerchantKeys(context.Background())

	rules := f.tagRulesByMerchant(context.Background())
	require.Len(t, rules, 1)
	assert.Equal(t, tagID, rules["interest earned in"].TagID)
	assert.Equal(t, "interest earned in", btdb.docs["i1"].MerchantKey)
	assert.Equal(t, tagID, btdb.docs["i1"].TagID, "July's interest is tagged by the merged rule")
}

// The pies follow the P&L's rules exactly, so each one adds up to its total.
func TestTagTotals_AddUpToThePL(t *testing.T) {
	steam := models.FinanceTag{ID: primitive.NewObjectID(), Name: "Steam", Color: "#38bdf8"}
	ads := models.FinanceTag{ID: primitive.NewObjectID(), Name: "Google Ads", Color: "#fbbf24"}
	tags := map[string]models.FinanceTag{steam.ID.Hex(): steam, ads.ID.Hex(): ads}

	txs := []models.BankTransaction{
		tx("p1", "checking", "Steam", -500, "2026-09-03"),
		tx("p2", "checking", "Patreon", -100, "2026-09-04"),
		tx("e1", "checking", "Google", 200, "2026-09-05"),
		tx("e2", "checking", "Heroku", 50, "2026-09-06"),
		tx("hidden", "checking", "Personal", 999, "2026-09-07"),
		tx("pending", "checking", "Pending", 77, "2026-09-07"),
		tx("out", "checking", "", 300, "2026-09-10"),
		tx("in", "savings", "", -300, "2026-09-10"),
		tx("deleted-tag", "checking", "Old", 10, "2026-09-11"),
		tx("outside", "checking", "Steam", -1000, "2026-10-02"),
	}
	txs[0].TagID = steam.ID.Hex()
	txs[2].TagID = ads.ID.Hex()
	txs[4].Hidden = true
	txs[5].Pending = true
	txs[8].TagID = primitive.NewObjectID().Hex()

	start, _ := time.Parse("2006-01", "2026-09")
	byTag := tagTotals(txs, start, start, tags)
	pl := buildFinanceSummary(nil, txs, start, start, 0.85, true).Months[0]

	assert.Equal(t, []models.FinanceTagTotal{
		{TagID: steam.ID.Hex(), Name: "Steam", Color: "#38bdf8", Amount: 500},
		{Name: "Untagged", Color: untaggedColor, Amount: 100},
	}, byTag.Income)
	assert.Equal(t, []models.FinanceTagTotal{
		{TagID: ads.ID.Hex(), Name: "Google Ads", Color: "#fbbf24", Amount: 200},
		{Name: "Untagged", Color: untaggedColor, Amount: 60},
	}, byTag.Expenses, "a deleted tag counts as Untagged")

	sum := func(s []models.FinanceTagTotal) (n float64) {
		for _, x := range s {
			n += x.Amount
		}
		return n
	}
	assert.Equal(t, pl.Bank.Income, sum(byTag.Income))
	assert.Equal(t, pl.Bank.Expenses, sum(byTag.Expenses), "hidden, pending and transfers are out of both")
}

func TestApplyMerchantRule_TagsOnlyUntaggedFromThatMerchant(t *testing.T) {
	f, btdb, tags, rules := financeWithTags()
	steam := mustTag(t, tags, "Steam", "#38bdf8")
	other := mustTag(t, tags, "Other", "#a78bfa")
	for _, x := range []models.BankTransaction{
		tx("s1", "c", "Steam", -10, "2026-09-01"),
		tx("s2", "c", "Steam", -20, "2026-09-02"),
		tx("s3", "c", "Steam", -30, "2026-09-03"),
		tx("x1", "c", "Coffee", 5, "2026-09-03"),
	} {
		btdb.docs[x.TransactionID] = x
	}
	s3 := btdb.docs["s3"]
	s3.TagID = other.ID.Hex()
	btdb.docs["s3"] = s3
	// Synced before merchant keys existed.
	legacy := tx("s4", "c", "Steam", -40, "2026-09-04")
	legacy.MerchantKey = ""
	btdb.docs["s4"] = legacy

	n, err := f.applyMerchantRule(context.Background(), "steam", "Steam", steam.ID.Hex())
	require.NoError(t, err)

	assert.Equal(t, int64(3), n)
	for _, id := range []string{"s1", "s2", "s4"} {
		assert.Equal(t, steam.ID.Hex(), btdb.docs[id].TagID, id)
	}
	assert.Equal(t, other.ID.Hex(), btdb.docs["s3"].TagID, "an existing tag stands")
	assert.Empty(t, btdb.docs["x1"].TagID)
	require.Len(t, rules.docs, 1)
	assert.Equal(t, "steam", rules.docs[0]["merchant_key"])
}

func TestDeleteTag_UntagsAndDropsItsRules(t *testing.T) {
	f, btdb, tags, rules := financeWithTags()
	steam := mustTag(t, tags, "Steam", "#38bdf8")
	keep := mustTag(t, tags, "Keep", "#a78bfa")
	a := tx("a", "c", "Steam", -10, "2026-09-01")
	a.TagID = steam.ID.Hex()
	b := tx("b", "c", "Other", -10, "2026-09-01")
	b.TagID = keep.ID.Hex()
	btdb.docs["a"], btdb.docs["b"] = a, b
	rules.docs = append(rules.docs,
		toM(models.FinanceTagRule{ID: primitive.NewObjectID(), MerchantKey: "steam", TagID: steam.ID.Hex()}),
		toM(models.FinanceTagRule{ID: primitive.NewObjectID(), MerchantKey: "other", TagID: keep.ID.Hex()}))

	req := mux.SetURLVars(httptest.NewRequest(http.MethodDelete, "/", nil), map[string]string{"id": steam.ID.Hex()})
	rr := httptest.NewRecorder()
	f.DeleteTagHandler(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Empty(t, btdb.docs["a"].TagID)
	assert.Equal(t, keep.ID.Hex(), btdb.docs["b"].TagID)
	assert.Len(t, rules.docs, 1)
	assert.Len(t, tags.docs, 1)
}

func TestCreateTag_ValidatesAndRejectsDuplicates(t *testing.T) {
	f, _, tags, _ := financeWithTags()
	post := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		f.CreateTagHandler(rr, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body)))
		return rr
	}

	rr := post(`{"name":"  Steam  "}`)
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	assert.Equal(t, "Steam", tags.docs[0]["name"])
	assert.Equal(t, tagPalette[0], tags.docs[0]["color"], "a palette colour when none is given")

	assert.Equal(t, http.StatusConflict, post(`{"name":"steam"}`).Code, "names are unique ignoring case")
	assert.Equal(t, http.StatusBadRequest, post(`{"name":""}`).Code)
	assert.Equal(t, http.StatusBadRequest, post(`{"name":"x","color":"red"}`).Code)
	assert.Equal(t, http.StatusBadRequest, post(`{"name":"`+string(bytes.Repeat([]byte("a"), 41))+`"}`).Code)
}

func TestPatchTransaction(t *testing.T) {
	f, btdb, tags, _ := financeWithTags()
	steam := mustTag(t, tags, "Steam", "#38bdf8")
	btdb.docs["a"] = tx("a", "c", "Steam", -10, "2026-09-01")
	btdb.docs["b"] = tx("b", "c", "Steam", -20, "2026-09-02")

	patch := func(id, body string) *httptest.ResponseRecorder {
		req := mux.SetURLVars(httptest.NewRequest(http.MethodPatch, "/", bytes.NewBufferString(body)), map[string]string{"transaction_id": id})
		rr := httptest.NewRecorder()
		f.PatchTransactionHandler(rr, req)
		return rr
	}

	rr := patch("a", `{"tag_id":"`+steam.ID.Hex()+`","apply_to_merchant":true}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var out struct {
		AlsoTagged int64 `json:"also_tagged"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	assert.Equal(t, int64(1), out.AlsoTagged)
	assert.Equal(t, steam.ID.Hex(), btdb.docs["b"].TagID)

	require.Equal(t, http.StatusOK, patch("a", `{"hidden":true}`).Code)
	assert.True(t, btdb.docs["a"].Hidden)
	require.Equal(t, http.StatusOK, patch("a", `{"tag_id":""}`).Code)
	assert.Empty(t, btdb.docs["a"].TagID, "an empty tag_id removes the tag")

	assert.Equal(t, http.StatusNotFound, patch("nope", `{"hidden":true}`).Code)
	assert.Equal(t, http.StatusBadRequest, patch("a", `{}`).Code)
	assert.Equal(t, http.StatusBadRequest, patch("a", `{"tag_id":"`+primitive.NewObjectID().Hex()+`"}`).Code, "unknown tag")
}

func TestListTransactions_FiltersAndPages(t *testing.T) {
	f, btdb, tags, _ := financeWithTags()
	steam := mustTag(t, tags, "Steam", "#38bdf8")
	for i := 1; i <= 5; i++ {
		x := tx(fmt.Sprintf("t%d", i), "c", "Shop", float64(i), fmt.Sprintf("2026-09-%02d", i))
		if i == 1 {
			x.TagID = steam.ID.Hex()
		}
		if i == 2 {
			x.Hidden = true
		}
		btdb.docs[x.TransactionID] = x
	}
	list := func(q string) (int64, int) {
		rr := httptest.NewRecorder()
		f.ListTransactionsHandler(rr, httptest.NewRequest(http.MethodGet, "/?from=2026-09&to=2026-09&"+q, nil))
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		var out struct {
			Data       []models.FinanceTransactionView `json:"data"`
			TotalCount int64                           `json:"totalCount"`
		}
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
		return out.TotalCount, len(out.Data)
	}

	total, _ := list("")
	assert.Equal(t, int64(4), total, "hidden are excluded by default")
	total, _ = list("hidden=include")
	assert.Equal(t, int64(5), total)
	total, _ = list("hidden=only")
	assert.Equal(t, int64(1), total)
	total, _ = list("tag=" + steam.ID.Hex())
	assert.Equal(t, int64(1), total)
	total, _ = list("tag=untagged")
	assert.Equal(t, int64(3), total)
}

func TestTransactionListFilter(t *testing.T) {
	start, _ := time.Parse("2006-01", "2026-09")
	f := transactionListFilter(start, start, "", "", "a.b(c")
	or := f["$or"].(bson.A)
	assert.Equal(t, `a\.b\(c`, or[0].(bson.M)["name"].(primitive.Regex).Pattern, "search is matched literally")
	assert.Equal(t, bson.M{"$ne": true}, f["hidden"])

	long := transactionListFilter(start, start, "", "", string(bytes.Repeat([]byte("x"), 500)))
	assert.Len(t, long["$or"].(bson.A)[0].(bson.M)["name"].(primitive.Regex).Pattern, maxTxSearchLength)
}
