package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/plaid/plaid-go/v39/plaid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakePlaidClient is a scripted plaidSyncClient.
type fakePlaidClient struct {
	pages         []plaidSyncPage
	pageErr       error
	syncCalls     []string // cursors received, in order
	linkToken     string
	linkErr       error
	exchangeToken string
	exchangeItem  string
	exchangeErr   error
}

func (f *fakePlaidClient) CreateLinkToken(ctx context.Context) (string, time.Time, error) {
	if f.linkErr != nil {
		return "", time.Time{}, f.linkErr
	}
	return f.linkToken, time.Now().Add(4 * time.Hour), nil
}

func (f *fakePlaidClient) ExchangePublicToken(ctx context.Context, publicToken string) (string, string, error) {
	if f.exchangeErr != nil {
		return "", "", f.exchangeErr
	}
	return f.exchangeToken, f.exchangeItem, nil
}

func (f *fakePlaidClient) SyncTransactions(ctx context.Context, accessToken, cursor string) (plaidSyncPage, error) {
	f.syncCalls = append(f.syncCalls, cursor)
	if f.pageErr != nil {
		return plaidSyncPage{}, f.pageErr
	}
	idx := len(f.syncCalls) - 1
	if idx >= len(f.pages) {
		return plaidSyncPage{HasMore: false, NextCursor: cursor}, nil
	}
	return f.pages[idx], nil
}

// fakeBankTxDB is an in-memory databases.BankTransactionDatabase.
type fakeBankTxDB struct {
	docs      map[string]models.BankTransaction
	updateErr error
	deleteErr error
}

func newFakeBankTxDB() *fakeBankTxDB { return &fakeBankTxDB{docs: map[string]models.BankTransaction{}} }

func (f *fakeBankTxDB) InsertOne(ctx context.Context, tx models.BankTransaction, opts ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	f.docs[tx.TransactionID] = tx
	return nil, nil
}

func (f *fakeBankTxDB) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	fm, ok := filter.(bson.M)
	if !ok {
		return nil, errors.New("fake only supports bson.M filters")
	}
	um, ok := update.(bson.M)
	if !ok {
		return nil, errors.New("fake only supports bson.M updates")
	}
	for id, doc := range f.docs {
		if fakeMatches(doc, fm) {
			f.docs[id] = fakeApply(doc, um, false)
			return &mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil
		}
	}
	upsert := false
	for _, o := range opts {
		if o != nil && o.Upsert != nil && *o.Upsert {
			upsert = true
		}
	}
	if !upsert {
		return &mongo.UpdateResult{}, nil
	}
	doc := fakeApply(models.BankTransaction{}, um, true)
	if doc.TransactionID == "" {
		doc.TransactionID, _ = fm["transaction_id"].(string)
	}
	f.docs[doc.TransactionID] = doc
	return &mongo.UpdateResult{UpsertedCount: 1}, nil
}

func (f *fakeBankTxDB) UpdateMany(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	fm, _ := filter.(bson.M)
	um, _ := update.(bson.M)
	res := &mongo.UpdateResult{}
	for id, doc := range f.docs {
		if fakeMatches(doc, fm) {
			f.docs[id] = fakeApply(doc, um, false)
			res.MatchedCount++
			res.ModifiedCount++
		}
	}
	return res, nil
}

// fakeApply runs $set, $unset and (on insert) $setOnInsert against a
// transaction by round-tripping it through BSON, the way Mongo would.
func fakeApply(doc models.BankTransaction, update bson.M, inserting bool) models.BankTransaction {
	raw, _ := bson.Marshal(doc)
	m := bson.M{}
	_ = bson.Unmarshal(raw, &m)
	merge := func(v interface{}) {
		b, _ := bson.Marshal(v)
		add := bson.M{}
		_ = bson.Unmarshal(b, &add)
		for k, val := range add {
			m[k] = val
		}
	}
	if v, ok := update["$set"]; ok {
		merge(v)
	}
	if v, ok := update["$setOnInsert"]; ok && inserting {
		merge(v)
	}
	if v, ok := update["$unset"].(bson.M); ok {
		for k := range v {
			delete(m, k)
		}
	}
	out := models.BankTransaction{}
	b, _ := bson.Marshal(m)
	_ = bson.Unmarshal(b, &out)
	return out
}

// fakeMatches evaluates the subset of query operators the finance handlers
// use: equality, $in, $ne, $exists, $gte and $lt. $or is treated as a match.
func fakeMatches(doc models.BankTransaction, filter bson.M) bool {
	raw, _ := bson.Marshal(doc)
	m := bson.M{}
	_ = bson.Unmarshal(raw, &m)
	for key, want := range filter {
		if key == "$or" {
			continue
		}
		got, present := m[key]
		if s, ok := got.(string); ok && s == "" {
			got = nil
		}
		ops, isOps := want.(bson.M)
		if !isOps {
			if fmt.Sprint(got) != fmt.Sprint(want) {
				return false
			}
			continue
		}
		for op, arg := range ops {
			switch op {
			case "$in":
				found := false
				for _, a := range arg.(bson.A) {
					if (a == nil || a == "") && got == nil {
						found = true
					} else if a != nil && fmt.Sprint(a) == fmt.Sprint(got) {
						found = true
					}
				}
				if !found {
					return false
				}
			case "$ne":
				if fmt.Sprint(got) == fmt.Sprint(arg) {
					return false
				}
			case "$exists":
				if present != arg.(bool) {
					return false
				}
			case "$gte", "$lt":
				gt, ok1 := got.(primitive.DateTime)
				at, ok2 := arg.(time.Time)
				if !ok1 || !ok2 {
					continue
				}
				if op == "$gte" && gt.Time().Before(at) {
					return false
				}
				if op == "$lt" && !gt.Time().Before(at) {
					return false
				}
			}
		}
	}
	return true
}

func (f *fakeBankTxDB) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	fm, _ := filter.(bson.M)
	id, _ := fm["transaction_id"].(string)
	delete(f.docs, id)
	return nil
}

func (f *fakeBankTxDB) DeleteMany(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) (int64, error) {
	return 0, nil
}

func (f *fakeBankTxDB) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*databases.MongoCursor, error) {
	fm, _ := filter.(bson.M)
	docs := make([]interface{}, 0, len(f.docs))
	for _, d := range f.docs {
		if fakeMatches(d, fm) {
			docs = append(docs, d)
		}
	}
	cur, err := databases.NewMongoCursorFromDocuments(docs)
	if err != nil {
		return nil, err
	}
	return &cur, nil
}

func (f *fakeBankTxDB) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	fm, _ := filter.(bson.M)
	n := int64(0)
	for _, d := range f.docs {
		if fakeMatches(d, fm) {
			n++
		}
	}
	return n, nil
}

func (f *fakeBankTxDB) EnsureUniqueTransactionIDIndex(ctx context.Context) error { return nil }

// fakeSingleResult is a databases.SingleResultHelper over an optional value.
type fakeSingleResult struct {
	value interface{}
	err   error
}

func (f *fakeSingleResult) Decode(v interface{}) error {
	if f.err != nil {
		return f.err
	}
	raw, err := bson.Marshal(f.value)
	if err != nil {
		return err
	}
	return bson.Unmarshal(raw, v)
}

// fakePlaidStateDB is an in-memory databases.PlaidStateDatabase.
type fakePlaidStateDB struct {
	state    models.PlaidSyncState
	hasState bool
	findErr  error
}

func (f *fakePlaidStateDB) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) databases.SingleResultHelper {
	if f.findErr != nil {
		return &fakeSingleResult{err: f.findErr}
	}
	if !f.hasState {
		return &fakeSingleResult{err: mongo.ErrNoDocuments}
	}
	return &fakeSingleResult{value: f.state}
}

func (f *fakePlaidStateDB) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	um, ok := update.(bson.M)
	if !ok {
		return nil, errors.New("fake only supports bson.M updates")
	}
	set, _ := um["$set"].(bson.M)
	for k, v := range set {
		switch k {
		case "cursor":
			f.state.Cursor, _ = v.(string)
		case "item_id":
			f.state.ItemID, _ = v.(string)
		case "last_sync_at":
			if ts, ok := v.(time.Time); ok {
				f.state.LastSyncAt = ts
			}
		case "accounts":
			if accts, ok := v.([]models.PlaidAccountSnapshot); ok {
				f.state.Accounts = accts
			}
		case "updated_at":
			if ts, ok := v.(time.Time); ok {
				f.state.UpdatedAt = ts
			}
		}
	}
	f.hasState = true
	return &mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1, UpsertedCount: 1}, nil
}

// plaidTestTx builds a minimal plaid.Transaction for tests.
func plaidTestTx(id, accountID, name string, amount float64, date string, pending bool) plaid.Transaction {
	return plaid.Transaction{
		TransactionId: id,
		AccountId:     accountID,
		Name:          name,
		Amount:        amount,
		Date:          date,
		Pending:       pending,
	}
}

func plaidTestAccount() plaid.AccountBase {
	return plaid.AccountBase{
		AccountId: "acc-1",
		Name:      "Everyday Checking",
		Mask:      *plaid.NewNullableString(plaid.PtrString("1234")),
		Type:      plaid.AccountType("depository"),
	}
}

// ---------------------------------------------------------------------------
// Direction + mapping
// ---------------------------------------------------------------------------

func TestPlaidDirection_SignConvention(t *testing.T) {
	assert.Equal(t, models.BankDirectionIn, plaidDirection(-10.0))  // Plaid: negative = inflow
	assert.Equal(t, models.BankDirectionOut, plaidDirection(10.0))  // Plaid: positive = outflow
	assert.Equal(t, models.BankDirectionNone, plaidDirection(0))    // zero-amount edge case
	assert.Equal(t, models.BankDirectionIn, plaidDirection(-0.01)) // tiny inflow stays "in"
}

func TestBankTransactionFromPlaid_Mapping(t *testing.T) {
	now := time.Now().UTC()
	tx := plaidTestTx("tx-1", "acc-1", "Starbucks", 5.25, "2026-09-05", false)
	doc := bankTransactionFromPlaid(tx, "Everyday Checking", "1234", now)

	assert.Equal(t, "tx-1", doc.TransactionID)
	assert.Equal(t, "acc-1", doc.AccountID)
	assert.Equal(t, "Everyday Checking", doc.AccountName)
	assert.Equal(t, "1234", doc.AccountMask)
	assert.Equal(t, "Starbucks", doc.Name)
	assert.Equal(t, 5.25, doc.Amount)
	assert.Equal(t, models.BankDirectionOut, doc.Direction)
	assert.Equal(t, "2026-09-05", doc.Date.UTC().Format("2006-01-02"))
	assert.False(t, doc.Pending)
	assert.Equal(t, "plaid", doc.Source)

	inflow := plaidTestTx("tx-2", "acc-1", "Paycheck", -2500.0, "2026-09-06", false)
	assert.Equal(t, models.BankDirectionIn, bankTransactionFromPlaid(inflow, "", "", now).Direction)
}

func TestBankTransactionFromPlaid_BadDateFallsBackToNow(t *testing.T) {
	now := time.Now().UTC()
	tx := plaidTestTx("tx-bad", "acc-1", "X", 1.0, "not-a-date", false)
	doc := bankTransactionFromPlaid(tx, "", "", now)
	assert.WithinDuration(t, now, doc.Date, time.Second)
}

// ---------------------------------------------------------------------------
// Sync logic against a mock Plaid client
// ---------------------------------------------------------------------------

func TestRunPlaidSync_AddModifyRemoveCursorAndAccounts(t *testing.T) {
	accounts := []plaid.AccountBase{plaidTestAccount()}
	client := &fakePlaidClient{pages: []plaidSyncPage{
		{
			Added: []plaid.Transaction{
				plaidTestTx("a", "acc-1", "Coffee", -50.0, "2026-09-01", false),
				plaidTestTx("b", "acc-1", "Pending Charge", 20.0, "2026-09-02", true),
			},
			Accounts:   accounts,
			NextCursor: "c2",
			HasMore:    true,
		},
		{
			Modified: []plaid.Transaction{
				plaidTestTx("a", "acc-1", "Coffee (updated)", -60.0, "2026-09-01", false),
			},
			Removed: []plaid.RemovedTransaction{
				{TransactionId: "b"},
			},
			Added: []plaid.Transaction{
				plaidTestTx("c", "acc-1", "Groceries", 10.0, "2026-09-03", false),
			},
			Accounts:   accounts,
			NextCursor: "c3",
			HasMore:    false,
		},
	}}

	btdb := newFakeBankTxDB()
	psdb := &fakePlaidStateDB{state: models.PlaidSyncState{Cursor: "c1"}, hasState: true}
	f := Finance{BTDB: btdb, PSDB: psdb, Plaid: client}

	added, modified, removed, accts, nextCursor, err := f.runPlaidSync(context.Background(), client, "access-123", "c1")
	assert.NoError(t, err)
	assert.Equal(t, 3, added)   // a, b, c
	assert.Equal(t, 1, modified) // a updated
	assert.Equal(t, 1, removed)  // b removed

	// Cursor advanced through the has_more loop: c1 -> c2 -> c3.
	assert.Equal(t, []string{"c1", "c2"}, client.syncCalls)
	assert.Equal(t, "c3", nextCursor)

	// Accounts snapshot came from the last page with accounts.
	assert.Len(t, accts, 1)
	assert.Equal(t, "acc-1", accts[0].AccountID)
	assert.Equal(t, "Everyday Checking", accts[0].Name)
	assert.Equal(t, "1234", accts[0].Mask)
	assert.Equal(t, "depository", accts[0].Type)

	// DB contents: "a" reflects the modification, "b" is gone, "c" is stored.
	assert.Len(t, btdb.docs, 2)
	assert.Equal(t, -60.0, btdb.docs["a"].Amount)
	assert.Equal(t, "Coffee (updated)", btdb.docs["a"].Name)
	assert.Equal(t, "Everyday Checking", btdb.docs["a"].AccountName)
	assert.Equal(t, 10.0, btdb.docs["c"].Amount)
	assert.Equal(t, models.BankDirectionOut, btdb.docs["c"].Direction)
	_, gone := btdb.docs["b"]
	assert.False(t, gone)
}

func TestRunPlaidSync_ErrorPropagates(t *testing.T) {
	client := &fakePlaidClient{pageErr: assert.AnError}
	btdb := newFakeBankTxDB()
	f := Finance{BTDB: btdb, Plaid: client}

	_, _, _, _, _, err := f.runPlaidSync(context.Background(), client, "access-123", "c1")
	assert.Error(t, err)
	assert.Equal(t, []string{"c1"}, client.syncCalls)
}

func TestRunPlaidSync_UpsertErrorPropagates(t *testing.T) {
	client := &fakePlaidClient{pages: []plaidSyncPage{{
		Added:      []plaid.Transaction{plaidTestTx("a", "acc-1", "X", 1.0, "2026-09-01", false)},
		NextCursor: "c2",
	}}}
	btdb := newFakeBankTxDB()
	btdb.updateErr = assert.AnError
	f := Finance{BTDB: btdb, Plaid: client}

	_, _, _, _, _, err := f.runPlaidSync(context.Background(), client, "access-123", "")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// HTTP-level handler tests
// ---------------------------------------------------------------------------

// plaidOwnerFixture builds a Finance with an owner admin doc, the given Plaid
// client, and in-memory DB fakes.
func plaidOwnerFixture(admin *models.AdminUser, client plaidSyncClient, btdb databases.BankTransactionDatabase, psdb databases.PlaidStateDatabase) Finance {
	adb := &mocks.AdminDatabase{}
	adb.On("FindOne", mock.Anything, mock.Anything).Return(admin, nil)
	return Finance{ADB: adb, BTDB: btdb, PSDB: psdb, Plaid: client}
}

func runPlaidRequest(t *testing.T, f Finance, h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	t.Setenv("JWT_SECRET", financeTestSecret)
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPlaidRoutes_RequireOwnerEnforced(t *testing.T) {
	handlers := map[string]func(Finance) http.Handler{
		"link-token": func(f Finance) http.Handler { return http.HandlerFunc(f.PlaidLinkTokenHandler) },
		"exchange":   func(f Finance) http.Handler { return http.HandlerFunc(f.PlaidExchangeHandler) },
		"sync":       func(f Finance) http.Handler { return http.HandlerFunc(f.PlaidSyncHandler) },
		"status":     func(f Finance) http.Handler { return http.HandlerFunc(f.PlaidStatusHandler) },
	}
	for name, build := range handlers {
		t.Run(name+"/401_without_token", func(t *testing.T) {
			f := plaidOwnerFixture(financeOwnerDoc(), &fakePlaidClient{}, newFakeBankTxDB(), &fakePlaidStateDB{})
			rec := runPlaidRequest(t, f, f.RequireOwner(build(f)), http.MethodPost, "/x", "", "")
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
		})
		t.Run(name+"/403_non_owner", func(t *testing.T) {
			nonOwner := financeOwnerDoc()
			nonOwner.Roles = []string{"admin"}
			nonOwner.Role = "admin"
			f := plaidOwnerFixture(nonOwner, &fakePlaidClient{}, newFakeBankTxDB(), &fakePlaidStateDB{})
			token := financeTestToken(t, financeTestSecret, nil)
			rec := runPlaidRequest(t, f, f.RequireOwner(build(f)), http.MethodPost, "/x", "", token)
			assert.Equal(t, http.StatusForbidden, rec.Code)
		})
	}
}

func TestPlaidLinkToken_503WhenUnconfigured(t *testing.T) {
	t.Setenv("PLAID_CLIENT_ID", "")
	t.Setenv("PLAID_SECRET", "")
	f := plaidOwnerFixture(financeOwnerDoc(), nil, newFakeBankTxDB(), &fakePlaidStateDB{})
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidLinkTokenHandler)), http.MethodPost, "/x", "", token)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var body map[string]string
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "PLAID_CLIENT_ID")
	assert.Contains(t, body["error"], "PLAID_SECRET")
}

func TestPlaidLinkToken_ReturnsToken(t *testing.T) {
	client := &fakePlaidClient{linkToken: "link-sandbox-123"}
	f := plaidOwnerFixture(financeOwnerDoc(), client, newFakeBankTxDB(), &fakePlaidStateDB{})
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidLinkTokenHandler)), http.MethodPost, "/x", "", token)

	assert.Equal(t, http.StatusOK, rec.Code)
	var body map[string]string
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "link-sandbox-123", body["link_token"])
	assert.NotEmpty(t, body["expiration"])
}

func TestPlaidExchange_400OnMissingPublicToken(t *testing.T) {
	f := plaidOwnerFixture(financeOwnerDoc(), &fakePlaidClient{}, newFakeBankTxDB(), &fakePlaidStateDB{})
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidExchangeHandler)), http.MethodPost, "/x", `{}`, token)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPlaidExchange_ReturnsAccessTokenOnce(t *testing.T) {
	psdb := &fakePlaidStateDB{}
	client := &fakePlaidClient{exchangeToken: "access-secret-xyz", exchangeItem: "item-1"}
	f := plaidOwnerFixture(financeOwnerDoc(), client, newFakeBankTxDB(), psdb)
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidExchangeHandler)), http.MethodPost, "/x", `{"public_token":"public-sandbox-abc"}`, token)

	assert.Equal(t, http.StatusOK, rec.Code)
	var body map[string]string
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "access-secret-xyz", body["access_token"])
	assert.Equal(t, "item-1", body["item_id"])
	assert.Contains(t, body["message"], "PLAID_ACCESS_TOKEN")
	// The item ID is persisted for /plaid/status; the access token is not.
	assert.True(t, psdb.hasState)
	assert.Equal(t, "item-1", psdb.state.ItemID)
}

func TestPlaidSync_503WhenNoAccessToken(t *testing.T) {
	t.Setenv("PLAID_ACCESS_TOKEN", "")
	f := plaidOwnerFixture(financeOwnerDoc(), &fakePlaidClient{}, newFakeBankTxDB(), &fakePlaidStateDB{})
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidSyncHandler)), http.MethodPost, "/x", "", token)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var body map[string]string
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "PLAID_ACCESS_TOKEN")
}

func TestPlaidSyncHandler_PersistsCursorAndReturnsCounts(t *testing.T) {
	t.Setenv("PLAID_ACCESS_TOKEN", "access-test")
	client := &fakePlaidClient{pages: []plaidSyncPage{{
		Added:      []plaid.Transaction{plaidTestTx("a", "acc-1", "Coffee", -50.0, "2026-09-01", false)},
		Accounts:   []plaid.AccountBase{plaidTestAccount()},
		NextCursor: "c2",
		HasMore:    false,
	}}}
	btdb := newFakeBankTxDB()
	psdb := &fakePlaidStateDB{}
	f := plaidOwnerFixture(financeOwnerDoc(), client, btdb, psdb)
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidSyncHandler)), http.MethodPost, "/x", "", token)

	assert.Equal(t, http.StatusOK, rec.Code)
	var body map[string]interface{}
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, float64(1), body["added"])
	assert.Equal(t, float64(0), body["modified"])
	assert.Equal(t, float64(0), body["removed"])
	assert.Equal(t, false, body["has_more"])

	// Cursor + accounts snapshot persisted.
	assert.True(t, psdb.hasState)
	assert.Equal(t, "c2", psdb.state.Cursor)
	assert.False(t, psdb.state.LastSyncAt.IsZero())
	assert.Len(t, psdb.state.Accounts, 1)
	assert.Equal(t, "Everyday Checking", psdb.state.Accounts[0].Name)
}

func TestPlaidStatus_Disconnected(t *testing.T) {
	t.Setenv("PLAID_ACCESS_TOKEN", "")
	f := plaidOwnerFixture(financeOwnerDoc(), &fakePlaidClient{}, newFakeBankTxDB(), &fakePlaidStateDB{})
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidStatusHandler)), http.MethodGet, "/x", "", token)

	assert.Equal(t, http.StatusOK, rec.Code)
	var body map[string]interface{}
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, false, body["connected"])
	assert.Empty(t, body["accounts"])
}

func TestPlaidStatus_ConnectedWithAccounts(t *testing.T) {
	t.Setenv("PLAID_ACCESS_TOKEN", "access-test")
	psdb := &fakePlaidStateDB{
		state: models.PlaidSyncState{
			ItemID:     "item-1",
			LastSyncAt: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC),
			Accounts: []models.PlaidAccountSnapshot{
				{AccountID: "acc-1", Name: "Everyday Checking", Mask: "1234", Type: "depository"},
			},
		},
		hasState: true,
	}
	f := plaidOwnerFixture(financeOwnerDoc(), &fakePlaidClient{}, newFakeBankTxDB(), psdb)
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidStatusHandler)), http.MethodGet, "/x", "", token)

	assert.Equal(t, http.StatusOK, rec.Code)
	var body map[string]interface{}
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, true, body["connected"])
	assert.Equal(t, "item-1", body["item_id"])
	assert.Equal(t, "2026-09-29T12:00:00Z", body["last_sync"])
	accounts, ok := body["accounts"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, accounts, 1)
	acct := accounts[0].(map[string]interface{})
	assert.Equal(t, "Everyday Checking", acct["name"])
	assert.Equal(t, "1234", acct["mask"])
	assert.Equal(t, "depository", acct["type"])
}

func TestPlaidEnvironment_DefaultsToSandbox(t *testing.T) {
	t.Setenv("PLAID_ENV", "")
	assert.Equal(t, plaid.Sandbox, plaidEnvironment())

	t.Setenv("PLAID_ENV", "production")
	assert.Equal(t, plaid.Production, plaidEnvironment())

	// Plaid retired Development in 2024; it falls back to sandbox.
	t.Setenv("PLAID_ENV", "development")
	assert.Equal(t, plaid.Sandbox, plaidEnvironment())

	t.Setenv("PLAID_ENV", "bogus")
	assert.Equal(t, plaid.Sandbox, plaidEnvironment())
}

func TestNewPlaidClientFromEnv_NilWhenUnconfigured(t *testing.T) {
	t.Setenv("PLAID_CLIENT_ID", "")
	t.Setenv("PLAID_SECRET", "")
	assert.Nil(t, newPlaidClientFromEnv())

	t.Setenv("PLAID_CLIENT_ID", "id")
	t.Setenv("PLAID_SECRET", "secret")
	assert.NotNil(t, newPlaidClientFromEnv())
}
