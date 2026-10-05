package handlers

// Plaid bank sync for the owner-only finance dashboard (see FINANCE.md).
//
// Plaid bank sync is the SOLE income/expense source for the P&L (cash
// basis). All routes are gated by RequireOwner (no exceptions).
//
// Env vars (fixed names): PLAID_CLIENT_ID, PLAID_SECRET, PLAID_ENV
// (sandbox|production, default sandbox), PLAID_ACCESS_TOKEN, and
// PLAID_WEBHOOK_URL (webhooks and update mode, finance_plaid_webhook.go).
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
	"go.uber.org/zap"

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
	// CreateLinkToken mints a Plaid Link token: for connecting a bank, or,
	// with opts.AccessToken, update mode on the existing connection.
	CreateLinkToken(ctx context.Context, opts plaidLinkOptions) (linkToken string, expiration time.Time, err error)
	// ExchangePublicToken exchanges a Link public_token for an access token.
	// Callers must never log the returned access token.
	ExchangePublicToken(ctx context.Context, publicToken string) (accessToken, itemID string, err error)
	// SyncTransactions fetches one /transactions/sync page starting at
	// cursor (empty cursor = full history).
	SyncTransactions(ctx context.Context, accessToken, cursor string) (plaidSyncPage, error)
	// GetAccounts calls /accounts/get. Its error carries the item's state,
	// e.g. ITEM_LOGIN_REQUIRED.
	GetAccounts(ctx context.Context, accessToken string) ([]plaid.AccountBase, error)
	// UpdateItemWebhook points an existing item's webhooks at url.
	UpdateItemWebhook(ctx context.Context, accessToken, url string) error
	// WebhookVerificationKey fetches the public key that signed a webhook.
	WebhookVerificationKey(ctx context.Context, keyID string) (plaid.JWKPublicKey, error)
	// FireSandboxWebhook asks Plaid to send a test webhook (Sandbox only).
	FireSandboxWebhook(ctx context.Context, accessToken, code string) error
	// RemoveItem calls /item/remove: the access token stops working and
	// Plaid stops billing for the item.
	RemoveItem(ctx context.Context, accessToken string) error
}

// plaidLinkOptions configures a Link token.
type plaidLinkOptions struct {
	// AccessToken switches Link to update mode on that item.
	AccessToken string
	// AccountSelection lets the owner add or remove accounts in update mode.
	AccountSelection bool
	// WebhookURL, when set, is where Plaid sends this item's webhooks.
	WebhookURL string
}

// plaidAPIClient is the production plaidSyncClient backed by the official
// Plaid Go SDK.
type plaidAPIClient struct {
	api *plaid.APIClient
}

func (c *plaidAPIClient) CreateLinkToken(ctx context.Context, opts plaidLinkOptions) (string, time.Time, error) {
	req := plaid.LinkTokenCreateRequest{
		ClientName:   "Lines Police CAD",
		Language:     "en",
		CountryCodes: []plaid.CountryCode{plaid.COUNTRYCODE_US},
		User:         &plaid.LinkTokenCreateRequestUser{ClientUserId: "lpc-owner"},
	}
	if opts.WebhookURL != "" {
		req.Webhook = plaid.PtrString(opts.WebhookURL)
	}
	if opts.AccessToken != "" {
		// Update mode: the existing item, and no products (Plaid rejects
		// products on an update-mode token).
		req.AccessToken = *plaid.NewNullableString(plaid.PtrString(opts.AccessToken))
		if opts.AccountSelection {
			req.Update = &plaid.LinkTokenCreateRequestUpdate{AccountSelectionEnabled: plaid.PtrBool(true)}
		}
	} else {
		req.Products = []plaid.Products{plaid.PRODUCTS_TRANSACTIONS}
	}
	resp, _, err := c.api.PlaidApi.LinkTokenCreate(ctx).LinkTokenCreateRequest(req).Execute()
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

func (c *plaidAPIClient) GetAccounts(ctx context.Context, accessToken string) ([]plaid.AccountBase, error) {
	resp, _, err := c.api.PlaidApi.AccountsGet(ctx).AccountsGetRequest(plaid.AccountsGetRequest{AccessToken: accessToken}).Execute()
	if err != nil {
		return nil, err
	}
	return resp.GetAccounts(), nil
}

func (c *plaidAPIClient) UpdateItemWebhook(ctx context.Context, accessToken, url string) error {
	_, _, err := c.api.PlaidApi.ItemWebhookUpdate(ctx).ItemWebhookUpdateRequest(plaid.ItemWebhookUpdateRequest{
		AccessToken: accessToken,
		Webhook:     *plaid.NewNullableString(plaid.PtrString(url)),
	}).Execute()
	return err
}

func (c *plaidAPIClient) WebhookVerificationKey(ctx context.Context, keyID string) (plaid.JWKPublicKey, error) {
	resp, _, err := c.api.PlaidApi.WebhookVerificationKeyGet(ctx).WebhookVerificationKeyGetRequest(
		plaid.WebhookVerificationKeyGetRequest{KeyId: keyID},
	).Execute()
	if err != nil {
		return plaid.JWKPublicKey{}, err
	}
	return resp.GetKey(), nil
}

func (c *plaidAPIClient) FireSandboxWebhook(ctx context.Context, accessToken, code string) error {
	req := plaid.SandboxItemFireWebhookRequest{AccessToken: accessToken, WebhookCode: code}
	webhookType := plaid.WEBHOOKTYPE_ITEM
	if code == "SYNC_UPDATES_AVAILABLE" || code == "DEFAULT_UPDATE" {
		webhookType = plaid.WEBHOOKTYPE_TRANSACTIONS
	}
	req.WebhookType = &webhookType
	_, _, err := c.api.PlaidApi.SandboxItemFireWebhook(ctx).SandboxItemFireWebhookRequest(req).Execute()
	return err
}

func (c *plaidAPIClient) RemoveItem(ctx context.Context, accessToken string) error {
	_, _, err := c.api.PlaidApi.ItemRemove(ctx).ItemRemoveRequest(plaid.ItemRemoveRequest{AccessToken: accessToken}).Execute()
	return err
}

// plaidErrorReason describes a Plaid API error for the owner: Plaid's own
// error_message and error_code when the response carries them (the SDK's
// err.Error() is only the HTTP status, e.g. "400 Bad Request"), otherwise
// the plain error.
func plaidErrorReason(err error) string {
	if err == nil {
		return ""
	}
	if pe, perr := plaid.ToPlaidError(err); perr == nil && pe.ErrorCode != "" {
		msg := pe.ErrorMessage
		if dm := pe.DisplayMessage.Get(); dm != nil && *dm != "" {
			msg = *dm
		}
		return msg + " (" + pe.ErrorCode + ")"
	}
	return err.Error()
}

// plaidCodedError is any error that carries a Plaid error_code directly.
type plaidCodedError interface {
	PlaidErrorCode() string
}

// plaidErrorCode returns Plaid's error_code from an API error, or "".
func plaidErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var coded plaidCodedError
	if errors.As(err, &coded) {
		return coded.PlaidErrorCode()
	}
	pe, perr := plaid.ToPlaidError(err)
	if perr != nil {
		return ""
	}
	return pe.ErrorCode
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

// plaidAccessTokenConfigured reports whether an access token is set in the
// environment. A bank is connected when it is and the owner hasn't
// disconnected that item (see plaidConnectedToken).
func plaidAccessTokenConfigured() bool {
	return os.Getenv("PLAID_ACCESS_TOKEN") != ""
}

// plaidConnectedToken returns the access token of the connected bank, or ""
// when there is none or the owner disconnected it (the item is gone at Plaid
// even if the old token is still in the environment).
func plaidConnectedToken(state models.PlaidSyncState) string {
	if state.DisconnectedAt != nil {
		return ""
	}
	return os.Getenv("PLAID_ACCESS_TOKEN")
}

func (f Finance) connectedPlaidToken(ctx context.Context) string {
	state, err := f.plaidSyncState(ctx)
	if err != nil {
		return ""
	}
	return plaidConnectedToken(state)
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
// Body (optional): {"mode": "update" | "new_accounts"}. With no mode it mints
// a token to connect a bank. "update" opens Link on the existing connection
// to repair it (update mode), "new_accounts" does the same with account
// selection on so newly available accounts can be added. Returns
// {"link_token": ..., "expiration": ...}.
func (f Finance) PlaidLinkTokenHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	c := f.plaidClient()
	if c == nil {
		writeFinanceError(w, http.StatusServiceUnavailable,
			"Plaid is not configured: set PLAID_CLIENT_ID and PLAID_SECRET")
		return
	}

	var in struct {
		Mode string `json:"mode"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&in)
	}
	opts := plaidLinkOptions{WebhookURL: plaidWebhookURL()}
	switch in.Mode {
	case "":
	case "update", "new_accounts":
		accessToken := f.connectedPlaidToken(r.Context())
		if accessToken == "" {
			writeFinanceError(w, http.StatusConflict, "no bank is connected to update")
			return
		}
		opts.AccessToken = accessToken
		opts.AccountSelection = in.Mode == "new_accounts"
	default:
		writeFinanceError(w, http.StatusBadRequest, "mode must be update or new_accounts")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	linkToken, expiration, err := c.CreateLinkToken(ctx, opts)
	if err != nil {
		writeFinanceError(w, http.StatusBadGateway, "failed to create Plaid Link token: "+plaidErrorReason(err))
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
		writeFinanceError(w, http.StatusBadGateway, "failed to exchange public token: "+plaidErrorReason(err))
		return
	}
	if accessToken == "" {
		writeFinanceError(w, http.StatusBadGateway, "Plaid returned no access token")
		return
	}

	// Remember the item ID for /plaid/status (the access token itself is
	// never stored).
	// A new item starts clean: the old item's cursor would be rejected and
	// its status no longer applies. Switching banks also removes the old item
	// at Plaid, which would otherwise stay billed with nothing using it.
	if itemID != "" {
		update := bson.M{"$set": bson.M{"item_id": itemID, "updated_at": time.Now().UTC()}}
		if state, serr := f.plaidSyncState(ctx); serr == nil && state.ItemID != itemID {
			if oldToken := plaidConnectedToken(state); oldToken != "" && oldToken != accessToken {
				if rerr := c.RemoveItem(ctx, oldToken); rerr != nil {
					zap.S().Warnw("failed to remove the previous Plaid item", "error", plaidErrorReason(rerr))
				}
			}
			update["$set"].(bson.M)["item_status"] = models.PlaidItemStatusOK
			update["$unset"] = bson.M{
				"cursor": "", "item_error_code": "", "consent_expires_at": "",
				"new_accounts_available": "", "webhook_url": "", "accounts": "", "disconnected_at": "",
			}
		}
		_, _ = f.PSDB.UpdateOne(ctx, bson.M{}, update, options.Update().SetUpsert(true))
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
		MerchantKey:            merchantKey(t.GetMerchantName(), t.GetName()),
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
	// Merchant rules tag new transactions on arrival. Loaded once per sync.
	rules := f.tagRulesByMerchant(ctx)
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
				plaidUpsert(doc, rules[doc.MerchantKey]),
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
				plaidUpsert(doc, rules[doc.MerchantKey]),
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
// The same sync runs on its own when Plaid sends SYNC_UPDATES_AVAILABLE.
func (f Finance) PlaidSyncHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	c := f.plaidClient()
	if c == nil {
		writeFinanceError(w, http.StatusServiceUnavailable,
			"Plaid is not configured: set PLAID_CLIENT_ID and PLAID_SECRET")
		return
	}
	accessToken := f.connectedPlaidToken(r.Context())
	if accessToken == "" {
		writeFinanceError(w, http.StatusServiceUnavailable,
			"Plaid is not connected: set PLAID_ACCESS_TOKEN (run the Link flow, then POST /admin/finance/plaid/exchange)")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	added, modified, removed, err := f.syncAndSave(ctx, c, accessToken)
	if err != nil {
		if errors.Is(err, errPlaidStatePersist) {
			writeFinanceError(w, http.StatusInternalServerError, "sync succeeded but failed to persist the sync cursor")
			return
		}
		writeFinanceError(w, http.StatusBadGateway, "Plaid sync failed: "+plaidErrorReason(err))
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
// Returns {"connected", "last_sync", "accounts", "item_id", "item_status",
// "consent_expires_at", "new_accounts_available"}. When a bank is connected it
// also checks the item with /accounts/get, so a connection that broke without
// a webhook (or before webhooks were set up) still shows as needing a fix.
// It never 503s: this is how the owner learns the bank is not connected yet.
func (f Finance) PlaidStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	state, err := f.plaidSyncState(ctx)
	if err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "failed to read Plaid sync state")
		return
	}

	if accessToken := plaidConnectedToken(state); accessToken != "" {
		if c := f.plaidClient(); c != nil {
			state = f.refreshItem(ctx, c, accessToken, state)
		}
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

	itemStatus := state.ItemStatus
	if itemStatus == "" {
		itemStatus = models.PlaidItemStatusOK
	}
	resp := map[string]interface{}{
		"connected":              plaidConnectedToken(state) != "",
		"accounts":               accounts,
		"item_status":            itemStatus,
		"new_accounts_available": state.NewAccountsAvailable,
		"sandbox":                plaidEnvironment() == plaid.Sandbox,
	}
	if itemID := strings.TrimSpace(state.ItemID); itemID != "" {
		resp["item_id"] = itemID
	}
	if !state.LastSyncAt.IsZero() {
		resp["last_sync"] = state.LastSyncAt.UTC().Format(time.RFC3339)
	}
	if state.ConsentExpiresAt != nil {
		resp["consent_expires_at"] = state.ConsentExpiresAt.UTC().Format(time.RFC3339)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
