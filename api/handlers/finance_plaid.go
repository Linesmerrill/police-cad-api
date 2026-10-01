package handlers

// Plaid bank sync for the owner-only finance dashboard (see FINANCE.md).
//
// Plaid bank sync is the SOLE income/expense source for the P&L (cash
// basis). All routes are gated by RequireOwner (no exceptions).
//
// Env vars (fixed names): PLAID_CLIENT_ID, PLAID_SECRET, PLAID_ENV
// (sandbox|development|production, default sandbox), PLAID_ACCESS_TOKEN.
//
// The access token is NEVER stored in Mongo — it comes from the
// PLAID_ACCESS_TOKEN env var. Sync state (cursor, last sync, accounts
// snapshot) lives in the finance_plaid_state collection; transactions live
// in bank_transactions (unique index on transaction_id).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/plaid/plaid-go/v39/plaid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/models"
)

// ---------------------------------------------------------------------------
// Plaid client (interface + real implementation)
// ---------------------------------------------------------------------------

// plaidSyncPage is one /transactions/sync response page, in SDK-independent
// terms. The real client maps the Plaid SDK types into this shape.
type plaidSyncPage struct {
	Added      []plaid.Transaction
	Modified   []plaid.Transaction
	Removed    []plaid.RemovedTransaction
	Accounts   []plaid.AccountBase
	NextCursor string
	HasMore    bool
}

// plaidSyncClient abstracts the Plaid API calls used by the finance module
// so sync logic is unit-testable with a mock.
type plaidSyncClient interface {
	// CreateLinkToken mints a Plaid Link token for the owner to connect a bank.
	CreateLinkToken(ctx context.Context) (linkToken string, expiration time.Time, err error)
	// ExchangePublicToken exchanges a Link public_token for an access token.
	// Callers must never log the returned access token.
	ExchangePublicToken(ctx context.Context, publicToken string) (accessToken, itemID string, err error)
	// SyncTransactions fetches one /transactions/sync page starting at
	// cursor (empty cursor = full history).
	SyncTransactions(ctx context.Context, accessToken, cursor string) (plaidSyncPage, error)
}

// plaidAPIClient is the production plaidSyncClient backed by the official
// Plaid Go SDK.
type plaidAPIClient struct {
	api *plaid.APIClient
}

func (c *plaidAPIClient) CreateLinkToken(ctx context.Context) (string, time.Time, error) {
	resp, _, err := c.api.PlaidApi.LinkTokenCreate(ctx).LinkTokenCreateRequest(plaid.LinkTokenCreateRequest{
		ClientName:   "Lines Police CAD",
		Language:     "en",
		CountryCodes: []plaid.CountryCode{plaid.COUNTRYCODE_US},
		User:         &plaid.LinkTokenCreateRequestUser{ClientUserId: "lpc-owner"},
		Products:     []plaid.Products{plaid.PRODUCTS_TRANSACTIONS},
	}).Execute()
	if err != nil {
		return "", time.Time{}, err
	}
	return resp.GetLinkToken(), resp.GetExpiration(), nil
}

func (c *plaidAPIClient) ExchangePublicToken(ctx context.Context, publicToken string) (string, string, error) {
	resp, _, err := c.api.PlaidApi.ItemPublicTokenExchange(ctx).ItemPublicTokenExchangeRequest(
		plaid.ItemPublicTokenExchangeRequest{PublicToken: publicToken},
	).Execute()
	if err != nil {
		return "", "", err
	}
	return resp.GetAccessToken(), resp.GetItemId(), nil
}

func (c *plaidAPIClient) SyncTransactions(ctx context.Context, accessToken, cursor string) (plaidSyncPage, error) {
	req := plaid.TransactionsSyncRequest{
		AccessToken: accessToken,
		Count:       plaid.PtrInt32(500),
	}
	if cursor != "" {
		req.Cursor = plaid.PtrString(cursor)
	}
	resp, _, err := c.api.PlaidApi.TransactionsSync(ctx).TransactionsSyncRequest(req).Execute()
	if err != nil {
		return plaidSyncPage{}, err
	}
	return plaidSyncPage{
		Added:      resp.GetAdded(),
		Modified:   resp.GetModified(),
		Removed:    resp.GetRemoved(),
		Accounts:   resp.GetAccounts(),
		NextCursor: resp.GetNextCursor(),
		HasMore:    resp.GetHasMore(),
	}, nil
}

// plaidEnvironment maps PLAID_ENV to a Plaid API environment: "production",
// or sandbox for anything else. Plaid retired its Development environment in
// 2024, so "development" is no longer an option; it falls back to sandbox
// rather than pointing at a host that no longer answers.
func plaidEnvironment() plaid.Environment {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PLAID_ENV"))) {
	case "production":
		return plaid.Production
	default:
		return plaid.Sandbox
	}
}

// newPlaidClientFromEnv builds a Plaid client from the current environment,
// or returns nil when PLAID_CLIENT_ID / PLAID_SECRET are not set.
func newPlaidClientFromEnv() plaidSyncClient {
	clientID := os.Getenv("PLAID_CLIENT_ID")
	secret := os.Getenv("PLAID_SECRET")
	if clientID == "" || secret == "" {
		return nil
	}
	cfg := plaid.NewConfiguration()
	cfg.AddDefaultHeader("PLAID-CLIENT-ID", clientID)
	cfg.AddDefaultHeader("PLAID-SECRET", secret)
	cfg.UseEnvironment(plaidEnvironment())
	return &plaidAPIClient{api: plaid.NewAPIClient(cfg)}
}

// plaidClient returns the injected Plaid client (unit tests) or builds one
// from the current environment.
func (f Finance) plaidClient() plaidSyncClient {
	if f.Plaid != nil {
		return f.Plaid
	}
	return newPlaidClientFromEnv()
}

// plaidAccessTokenConfigured reports whether a bank is connected (the access
// token is set in the environment).
func plaidAccessTokenConfigured() bool {
	return os.Getenv("PLAID_ACCESS_TOKEN") != ""
}

// plaidDirection derives the money direction from Plaid's sign convention:
// positive amounts are money OUT of the account, negative amounts are money
// IN. A zero amount is a "none" edge case (no money moved).
func plaidDirection(amount float64) string {
	switch {
	case amount < 0:
		return models.BankDirectionIn
	case amount > 0:
		return models.BankDirectionOut
	default:
		return models.BankDirectionNone
	}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// PlaidLinkTokenHandler implements POST /api/v1/admin/finance/plaid/link-token.
// Returns {"link_token": ..., "expiration": ...} for the owner to initialize
// Plaid Link (e.g. in the admin console).
func (f Finance) PlaidLinkTokenHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	c := f.plaidClient()
	if c == nil {
		writeFinanceError(w, http.StatusServiceUnavailable,
			"Plaid is not configured: set PLAID_CLIENT_ID and PLAID_SECRET")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	linkToken, expiration, err := c.CreateLinkToken(ctx)
	if err != nil {
		writeFinanceError(w, http.StatusBadGateway, "failed to create Plaid Link token: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"link_token": linkToken,
		"expiration": expiration.UTC().Format(time.RFC3339),
	})
}

// PlaidExchangeHandler implements POST /api/v1/admin/finance/plaid/exchange.
// Body: {"public_token": "..."}. Exchanges the Link public_token for an
// access token and returns it ONCE with instructions to store it as the
// Heroku config var PLAID_ACCESS_TOKEN. The token is never logged and never
// stored in the database.
func (f Finance) PlaidExchangeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	c := f.plaidClient()
	if c == nil {
		writeFinanceError(w, http.StatusServiceUnavailable,
			"Plaid is not configured: set PLAID_CLIENT_ID and PLAID_SECRET")
		return
	}

	var in struct {
		PublicToken string `json:"public_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.PublicToken) == "" {
		writeFinanceError(w, http.StatusBadRequest, "invalid request body: public_token is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	accessToken, itemID, err := c.ExchangePublicToken(ctx, strings.TrimSpace(in.PublicToken))
	if err != nil {
		writeFinanceError(w, http.StatusBadGateway, "failed to exchange public token: "+err.Error())
		return
	}
	if accessToken == "" {
		writeFinanceError(w, http.StatusBadGateway, "Plaid returned no access token")
		return
	}

	// Remember the item ID for /plaid/status (the access token itself is
	// never stored).
	if itemID != "" {
		_, _ = f.PSDB.UpdateOne(ctx,
			bson.M{},
			bson.M{"$set": bson.M{"item_id": itemID, "updated_at": time.Now().UTC()}},
			options.Update().SetUpsert(true),
		)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"access_token": accessToken,
		"item_id":      itemID,
		"message":      "Copy this access token now — it is displayed only once. Set it as the Heroku config var PLAID_ACCESS_TOKEN (never commit it to code).",
	})
}

// bankTransactionFromPlaid converts one Plaid transaction into the
// bank_transactions document shape. acctName/acctMask come from the sync
// page's accounts snapshot.
func bankTransactionFromPlaid(t plaid.Transaction, acctName, acctMask string, now time.Time) models.BankTransaction {
	date, err := time.Parse("2006-01-02", t.GetDate())
	if err != nil {
		date = now
	}
	var pfcPrimary string
	if pfc, ok := t.GetPersonalFinanceCategoryOk(); ok && pfc != nil {
		pfcPrimary = pfc.GetPrimary()
	}
	return models.BankTransaction{
		TransactionID:          t.GetTransactionId(),
		AccountID:              t.GetAccountId(),
		AccountName:            acctName,
		AccountMask:            acctMask,
		Name:                   t.GetName(),
		MerchantName:           t.GetMerchantName(),
		Amount:                 t.GetAmount(),
		Direction:              plaidDirection(t.GetAmount()),
		Date:                   date.UTC(),
		Pending:                t.GetPending(),
		Category:               t.GetCategory(),
		PersonalFinanceCategory:  pfcPrimary,
		Source:                 "plaid",
		CreatedAt:              now,
		UpdatedAt:              now,
	}
}

// runPlaidSync drives the /transactions/sync pagination loop starting at
// startCursor, upserts added/modified transactions by transaction_id, deletes
// removed ones, and returns the counts plus the new cursor and a fresh
// accounts snapshot. It is the unit-testable core of PlaidSyncHandler.
func (f Finance) runPlaidSync(ctx context.Context, c plaidSyncClient, accessToken, startCursor string) (added, modified, removed int, accounts []models.PlaidAccountSnapshot, nextCursor string, err error) {
	cursor := startCursor
	for {
		page, pageErr := c.SyncTransactions(ctx, accessToken, cursor)
		if pageErr != nil {
			return added, modified, removed, accounts, cursor, pageErr
		}

		now := time.Now().UTC()
		acctInfo := map[string]plaid.AccountBase{}
		for _, a := range page.Accounts {
			acctInfo[a.GetAccountId()] = a
		}

		for _, t := range page.Added {
			acct := acctInfo[t.GetAccountId()]
			doc := bankTransactionFromPlaid(t, acct.GetName(), nullableStringValue(acct.Mask), now)
			if _, uerr := f.BTDB.UpdateOne(ctx,
				bson.M{"transaction_id": doc.TransactionID},
				bson.M{"$set": doc},
				options.Update().SetUpsert(true),
			); uerr != nil {
				return added, modified, removed, accounts, cursor, uerr
			}
			added++
		}
		for _, t := range page.Modified {
			acct := acctInfo[t.GetAccountId()]
			doc := bankTransactionFromPlaid(t, acct.GetName(), nullableStringValue(acct.Mask), now)
			if _, uerr := f.BTDB.UpdateOne(ctx,
				bson.M{"transaction_id": doc.TransactionID},
				bson.M{"$set": doc},
				options.Update().SetUpsert(true),
			); uerr != nil {
				return added, modified, removed, accounts, cursor, uerr
			}
			modified++
		}
		for _, t := range page.Removed {
			if derr := f.BTDB.DeleteOne(ctx, bson.M{"transaction_id": t.GetTransactionId()}); derr != nil {
				return added, modified, removed, accounts, cursor, derr
			}
			removed++
		}

		cursor = page.NextCursor
		if len(page.Accounts) > 0 {
			accounts = make([]models.PlaidAccountSnapshot, 0, len(page.Accounts))
			for _, a := range page.Accounts {
				accounts = append(accounts, models.PlaidAccountSnapshot{
					AccountID: a.GetAccountId(),
					Name:      a.GetName(),
					Mask:      nullableStringValue(a.Mask),
					Type:      string(a.GetType()),
					Subtype:   nullableSubtypeValue(a.Subtype),
				})
			}
		}
		if !page.HasMore {
			break
		}
	}
	return added, modified, removed, accounts, cursor, nil
}

// nullableStringValue unwraps a Plaid NullableString, returning "" when unset.
func nullableStringValue(v plaid.NullableString) string {
	if s := v.Get(); s != nil {
		return *s
	}
	return ""
}

// nullableSubtypeValue unwraps a Plaid NullableAccountSubtype.
func nullableSubtypeValue(v plaid.NullableAccountSubtype) string {
	if s := v.Get(); s != nil {
		return string(*s)
	}
	return ""
}

// plaidSyncState loads the finance_plaid_state document, or an empty state
// when none has been stored yet.
func (f Finance) plaidSyncState(ctx context.Context) (models.PlaidSyncState, error) {
	var state models.PlaidSyncState
	if err := f.PSDB.FindOne(ctx, bson.M{}).Decode(&state); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.PlaidSyncState{}, nil
		}
		return models.PlaidSyncState{}, err
	}
	return state, nil
}

// PlaidSyncHandler implements POST /api/v1/admin/finance/plaid/sync. It runs
// /transactions/sync from the stored cursor (looping while has_more),
// upserts added/modified transactions, deletes removed ones, persists the
// new cursor + last_sync_at + accounts snapshot, and returns the counts.
func (f Finance) PlaidSyncHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	c := f.plaidClient()
	if c == nil {
		writeFinanceError(w, http.StatusServiceUnavailable,
			"Plaid is not configured: set PLAID_CLIENT_ID and PLAID_SECRET")
		return
	}
	accessToken := os.Getenv("PLAID_ACCESS_TOKEN")
	if accessToken == "" {
		writeFinanceError(w, http.StatusServiceUnavailable,
			"Plaid is not connected: set PLAID_ACCESS_TOKEN (run the Link flow, then POST /admin/finance/plaid/exchange)")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	state, err := f.plaidSyncState(ctx)
	if err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to read Plaid sync state")
		return
	}

	added, modified, removed, accounts, nextCursor, err := f.runPlaidSync(ctx, c, accessToken, state.Cursor)
	if err != nil {
		writeFinanceError(w, http.StatusBadGateway, "Plaid sync failed: "+err.Error())
		return
	}

	if _, err := f.PSDB.UpdateOne(ctx,
		bson.M{},
		bson.M{"$set": bson.M{
			"cursor":       nextCursor,
			"last_sync_at": time.Now().UTC(),
			"accounts":     accounts,
			"updated_at":   time.Now().UTC(),
		}},
		options.Update().SetUpsert(true),
	); err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "sync succeeded but failed to persist the sync cursor")
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"added":    added,
		"modified": modified,
		"removed":  removed,
		"has_more": false,
	})
}

// PlaidStatusHandler implements GET /api/v1/admin/finance/plaid/status.
// Returns {"connected": ..., "last_sync": ..., "accounts": [...], "item_id": ...}.
// It reports connection state and never 503s — this is how the owner learns
// the bank is not connected yet.
func (f Finance) PlaidStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	state, err := f.plaidSyncState(ctx)
	if err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to read Plaid sync state")
		return
	}

	type accountView struct {
		Name string `json:"name"`
		Mask string `json:"mask,omitempty"`
		Type string `json:"type,omitempty"`
	}
	accounts := make([]accountView, 0, len(state.Accounts))
	for _, a := range state.Accounts {
		accounts = append(accounts, accountView{Name: a.Name, Mask: a.Mask, Type: a.Type})
	}

	resp := map[string]interface{}{
		"connected": plaidAccessTokenConfigured(),
		"accounts":  accounts,
	}
	if itemID := strings.TrimSpace(state.ItemID); itemID != "" {
		resp["item_id"] = itemID
	}
	if !state.LastSyncAt.IsZero() {
		resp["last_sync"] = state.LastSyncAt.UTC().Format(time.RFC3339)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
