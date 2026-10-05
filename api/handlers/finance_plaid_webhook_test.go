package handlers

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/plaid/plaid-go/v39/plaid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linesmerrill/police-cad-api/models"
)

// plaidCodeErr is a Plaid API error carrying an error_code.
type plaidCodeErr string

func (e plaidCodeErr) Error() string          { return "plaid: " + string(e) }
func (e plaidCodeErr) PlaidErrorCode() string { return string(e) }

// webhookSigner signs webhook bodies the way Plaid does.
type webhookSigner struct {
	key *ecdsa.PrivateKey
	kid string
}

func newWebhookSigner(t *testing.T, kid string) (webhookSigner, plaid.JWKPublicKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	pad := func(b []byte) []byte { // coordinates are 32 bytes, big-endian
		out := make([]byte, 32)
		copy(out[32-len(b):], b)
		return out
	}
	jwk := plaid.JWKPublicKey{
		Alg: "ES256", Crv: "P-256", Kid: kid, Kty: "EC", Use: "sig",
		X: base64.RawURLEncoding.EncodeToString(pad(key.PublicKey.X.Bytes())),
		Y: base64.RawURLEncoding.EncodeToString(pad(key.PublicKey.Y.Bytes())),
	}
	return webhookSigner{key: key, kid: kid}, jwk
}

func (s webhookSigner) sign(t *testing.T, body string, iat time.Time) string {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iat":                 iat.Unix(),
		"request_body_sha256": hex.EncodeToString(sum[:]),
	})
	tok.Header["kid"] = s.kid
	signed, err := tok.SignedString(s.key)
	require.NoError(t, err)
	return signed
}

func resetPlaidKeyCache() {
	plaidKeyMu.Lock()
	plaidKeyCache = map[string]plaidCachedKey{}
	plaidKeyMu.Unlock()
}

// ---------------------------------------------------------------------------
// Signature
// ---------------------------------------------------------------------------

func TestVerifyPlaidWebhook(t *testing.T) {
	resetPlaidKeyCache()
	signer, jwk := newWebhookSigner(t, "kid-verify")
	client := &fakePlaidClient{verifyKey: jwk}
	ctx := context.Background()
	now := time.Now()
	body := `{"webhook_type":"ITEM","webhook_code":"LOGIN_REPAIRED"}`

	assert.NoError(t, verifyPlaidWebhook(ctx, client, signer.sign(t, body, now), []byte(body), now))
	// The key is cached after the first fetch.
	assert.NoError(t, verifyPlaidWebhook(ctx, client, signer.sign(t, body, now), []byte(body), now))
	assert.Equal(t, 1, client.keyFetches)

	assert.Error(t, verifyPlaidWebhook(ctx, client, "", []byte(body), now), "missing header")
	assert.Error(t, verifyPlaidWebhook(ctx, client, signer.sign(t, body, now), []byte(body+" "), now), "tampered body")
	assert.Error(t, verifyPlaidWebhook(ctx, client, signer.sign(t, body, now.Add(-6*time.Minute)), []byte(body), now), "too old")

	// Signed by someone else's key under the same kid.
	other, _ := newWebhookSigner(t, "kid-verify")
	assert.Error(t, verifyPlaidWebhook(ctx, client, other.sign(t, body, now), []byte(body), now))

	// HS256 with the public key as a secret must never be accepted.
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iat": now.Unix()})
	hs.Header["kid"] = "kid-verify"
	hsSigned, _ := hs.SignedString([]byte("x"))
	assert.Error(t, verifyPlaidWebhook(ctx, client, hsSigned, []byte(body), now))
}

func TestVerifyPlaidWebhook_ExpiredKey(t *testing.T) {
	resetPlaidKeyCache()
	signer, jwk := newWebhookSigner(t, "kid-expired")
	past := int32(time.Now().Add(-time.Hour).Unix())
	jwk.ExpiredAt = *plaid.NewNullableInt32(&past)
	client := &fakePlaidClient{verifyKey: jwk}
	body := `{}`
	now := time.Now()
	assert.Error(t, verifyPlaidWebhook(context.Background(), client, signer.sign(t, body, now), []byte(body), now))
}

// ---------------------------------------------------------------------------
// Webhook handler
// ---------------------------------------------------------------------------

func postPlaidWebhook(t *testing.T, f Finance, signer webhookSigner, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/plaid", strings.NewReader(body))
	if signer.key != nil {
		req.Header.Set("Plaid-Verification", signer.sign(t, body, time.Now()))
	}
	rec := httptest.NewRecorder()
	f.PlaidWebhookHandler(rec, req)
	return rec
}

func webhookFixture(t *testing.T, state models.PlaidSyncState) (Finance, *fakePlaidStateDB, webhookSigner, *int) {
	t.Helper()
	resetPlaidKeyCache()
	signer, jwk := newWebhookSigner(t, "kid-handler")
	psdb := &fakePlaidStateDB{state: state, hasState: true}
	f := Finance{PSDB: psdb, BTDB: newFakeBankTxDB(), Plaid: &fakePlaidClient{verifyKey: jwk}}
	syncs := 0
	prev := plaidBackgroundSync
	plaidBackgroundSync = func(Finance) { syncs++ }
	t.Cleanup(func() { plaidBackgroundSync = prev })
	return f, psdb, signer, &syncs
}

func TestPlaidWebhook_RejectsUnsigned(t *testing.T) {
	f, psdb, _, _ := webhookFixture(t, models.PlaidSyncState{ItemID: "item-1"})
	rec := postPlaidWebhook(t, f, webhookSigner{}, `{"webhook_type":"ITEM","webhook_code":"ERROR","item_id":"item-1"}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, psdb.state.ItemStatus)
}

func TestPlaidWebhook_LoginRequiredThenRepaired(t *testing.T) {
	f, psdb, signer, _ := webhookFixture(t, models.PlaidSyncState{ItemID: "item-1"})

	rec := postPlaidWebhook(t, f, signer, `{"webhook_type":"ITEM","webhook_code":"ERROR","item_id":"item-1","error":{"error_code":"ITEM_LOGIN_REQUIRED"}}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, models.PlaidItemStatusLoginRequired, psdb.state.ItemStatus)
	assert.Equal(t, "ITEM_LOGIN_REQUIRED", psdb.state.ItemErrorCode)

	rec = postPlaidWebhook(t, f, signer, `{"webhook_type":"ITEM","webhook_code":"LOGIN_REPAIRED","item_id":"item-1"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, models.PlaidItemStatusOK, psdb.state.ItemStatus)
	assert.Empty(t, psdb.state.ItemErrorCode)
}

func TestPlaidWebhook_PendingExpirationKeepsTheDate(t *testing.T) {
	f, psdb, signer, _ := webhookFixture(t, models.PlaidSyncState{ItemID: "item-1"})
	rec := postPlaidWebhook(t, f, signer, `{"webhook_type":"ITEM","webhook_code":"PENDING_EXPIRATION","item_id":"item-1","consent_expiration_time":"2026-11-01T00:00:00Z"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, models.PlaidItemStatusPendingExpiration, psdb.state.ItemStatus)
	require.NotNil(t, psdb.state.ConsentExpiresAt)
	assert.Equal(t, "2026-11-01T00:00:00Z", psdb.state.ConsentExpiresAt.Format(time.RFC3339))
}

func TestPlaidWebhook_PendingDisconnectAndRevoked(t *testing.T) {
	f, psdb, signer, _ := webhookFixture(t, models.PlaidSyncState{ItemID: "item-1"})
	postPlaidWebhook(t, f, signer, `{"webhook_type":"ITEM","webhook_code":"PENDING_DISCONNECT","item_id":"item-1"}`)
	assert.Equal(t, models.PlaidItemStatusPendingDisconnect, psdb.state.ItemStatus)
	postPlaidWebhook(t, f, signer, `{"webhook_type":"ITEM","webhook_code":"USER_PERMISSION_REVOKED","item_id":"item-1"}`)
	assert.Equal(t, models.PlaidItemStatusRevoked, psdb.state.ItemStatus)
}

func TestPlaidWebhook_NewAccountsAvailable(t *testing.T) {
	f, psdb, signer, _ := webhookFixture(t, models.PlaidSyncState{ItemID: "item-1"})
	rec := postPlaidWebhook(t, f, signer, `{"webhook_type":"ITEM","webhook_code":"NEW_ACCOUNTS_AVAILABLE","item_id":"item-1"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, psdb.state.NewAccountsAvailable)
}

func TestPlaidWebhook_SyncUpdatesAvailableStartsASync(t *testing.T) {
	f, _, signer, syncs := webhookFixture(t, models.PlaidSyncState{ItemID: "item-1"})
	rec := postPlaidWebhook(t, f, signer, `{"webhook_type":"TRANSACTIONS","webhook_code":"SYNC_UPDATES_AVAILABLE","item_id":"item-1"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, *syncs)
}

func TestPlaidWebhook_IgnoresAnotherItem(t *testing.T) {
	f, psdb, signer, syncs := webhookFixture(t, models.PlaidSyncState{ItemID: "item-1"})
	rec := postPlaidWebhook(t, f, signer, `{"webhook_type":"ITEM","webhook_code":"ERROR","item_id":"old-item","error":{"error_code":"ITEM_LOGIN_REQUIRED"}}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, psdb.state.ItemStatus)
	assert.Equal(t, 0, *syncs)
}

// ---------------------------------------------------------------------------
// Update mode
// ---------------------------------------------------------------------------

func TestPlaidLinkToken_Modes(t *testing.T) {
	t.Setenv("PLAID_WEBHOOK_URL", "https://api.test/api/v1/webhooks/plaid")
	client := &fakePlaidClient{linkToken: "link-x"}
	f := plaidOwnerFixture(financeOwnerDoc(), client, newFakeBankTxDB(), &fakePlaidStateDB{})
	token := financeTestToken(t, financeTestSecret, nil)
	h := f.RequireOwner(http.HandlerFunc(f.PlaidLinkTokenHandler))

	// No bank yet: update mode has nothing to update.
	t.Setenv("PLAID_ACCESS_TOKEN", "")
	assert.Equal(t, http.StatusConflict, runPlaidRequest(t, f, h, http.MethodPost, "/x", `{"mode":"update"}`, token).Code)

	t.Setenv("PLAID_ACCESS_TOKEN", "access-1")
	assert.Equal(t, http.StatusBadRequest, runPlaidRequest(t, f, h, http.MethodPost, "/x", `{"mode":"nope"}`, token).Code)

	assert.Equal(t, http.StatusOK, runPlaidRequest(t, f, h, http.MethodPost, "/x", ``, token).Code)
	assert.Equal(t, http.StatusOK, runPlaidRequest(t, f, h, http.MethodPost, "/x", `{"mode":"update"}`, token).Code)
	assert.Equal(t, http.StatusOK, runPlaidRequest(t, f, h, http.MethodPost, "/x", `{"mode":"new_accounts"}`, token).Code)

	require.Len(t, client.linkOpts, 3)
	assert.Equal(t, plaidLinkOptions{WebhookURL: "https://api.test/api/v1/webhooks/plaid"}, client.linkOpts[0])
	assert.Equal(t, "access-1", client.linkOpts[1].AccessToken)
	assert.False(t, client.linkOpts[1].AccountSelection)
	assert.Equal(t, "access-1", client.linkOpts[2].AccessToken)
	assert.True(t, client.linkOpts[2].AccountSelection)
}

func TestPlaidUpdateComplete_ClearsPromptsAndSyncs(t *testing.T) {
	expires := time.Now().Add(48 * time.Hour)
	psdb := &fakePlaidStateDB{hasState: true, state: models.PlaidSyncState{
		ItemStatus: models.PlaidItemStatusPendingExpiration, ConsentExpiresAt: &expires, NewAccountsAvailable: true,
	}}
	syncs := 0
	prev := plaidBackgroundSync
	plaidBackgroundSync = func(Finance) { syncs++ }
	t.Cleanup(func() { plaidBackgroundSync = prev })

	f := plaidOwnerFixture(financeOwnerDoc(), &fakePlaidClient{}, newFakeBankTxDB(), psdb)
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidUpdateCompleteHandler)), http.MethodPost, "/x", `{}`, token)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, models.PlaidItemStatusOK, psdb.state.ItemStatus)
	assert.Nil(t, psdb.state.ConsentExpiresAt)
	assert.False(t, psdb.state.NewAccountsAvailable)
	assert.Equal(t, 1, syncs)
}

func TestPlaidStatus_AccountsGetCatchesBrokenLoginAndSetsWebhook(t *testing.T) {
	t.Setenv("PLAID_ACCESS_TOKEN", "access-1")
	t.Setenv("PLAID_WEBHOOK_URL", "https://api.test/api/v1/webhooks/plaid")
	client := &fakePlaidClient{accountsErr: plaidCodeErr("ITEM_LOGIN_REQUIRED")}
	psdb := &fakePlaidStateDB{hasState: true, state: models.PlaidSyncState{ItemID: "item-1"}}
	f := plaidOwnerFixture(financeOwnerDoc(), client, newFakeBankTxDB(), psdb)
	token := financeTestToken(t, financeTestSecret, nil)
	h := f.RequireOwner(http.HandlerFunc(f.PlaidStatusHandler))

	rec := runPlaidRequest(t, f, h, http.MethodGet, "/x", "", token)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"item_status":"login_required"`)
	assert.Equal(t, []string{"https://api.test/api/v1/webhooks/plaid"}, client.webhookURLs)

	// The login works again: the next check clears it, and the webhook isn't set twice.
	client.accountsErr = nil
	rec = runPlaidRequest(t, f, h, http.MethodGet, "/x", "", token)
	assert.Contains(t, rec.Body.String(), `"item_status":"ok"`)
	assert.Len(t, client.webhookURLs, 1)
}

func TestSyncAndSave_RestartsWhenDataChangesMidSync(t *testing.T) {
	psdb := &fakePlaidStateDB{hasState: true, state: models.PlaidSyncState{Cursor: "c0"}}
	client := &mutatingSyncClient{fakePlaidClient: &fakePlaidClient{}, failFirst: 1}
	f := Finance{PSDB: psdb, BTDB: newFakeBankTxDB(), Plaid: client}

	_, _, _, err := f.syncAndSave(context.Background(), client, "access-1")
	require.NoError(t, err)
	// First attempt failed mid-way; the restart began again from the stored cursor.
	assert.Equal(t, []string{"c0", "c0"}, client.cursors)
	assert.Equal(t, "c1", psdb.state.Cursor)
}

func TestSyncAndSave_LoginErrorMarksTheItem(t *testing.T) {
	psdb := &fakePlaidStateDB{hasState: true, state: models.PlaidSyncState{}}
	client := &fakePlaidClient{pageErr: plaidCodeErr("ITEM_LOGIN_REQUIRED")}
	f := Finance{PSDB: psdb, BTDB: newFakeBankTxDB(), Plaid: client}

	_, _, _, err := f.syncAndSave(context.Background(), client, "access-1")
	assert.Error(t, err)
	assert.Equal(t, models.PlaidItemStatusLoginRequired, psdb.state.ItemStatus)
}

// mutatingSyncClient fails the first failFirst syncs with
// TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION, then returns one page.
type mutatingSyncClient struct {
	*fakePlaidClient
	failFirst int
	cursors   []string
}

func (m *mutatingSyncClient) SyncTransactions(ctx context.Context, accessToken, cursor string) (plaidSyncPage, error) {
	m.cursors = append(m.cursors, cursor)
	if len(m.cursors) <= m.failFirst {
		return plaidSyncPage{}, plaidCodeErr("TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION")
	}
	return plaidSyncPage{NextCursor: "c1"}, nil
}

func TestPlaidExchange_NewItemStartsClean(t *testing.T) {
	psdb := &fakePlaidStateDB{hasState: true, state: models.PlaidSyncState{
		ItemID: "old-item", Cursor: "old-cursor", ItemStatus: models.PlaidItemStatusLoginRequired, NewAccountsAvailable: true,
	}}
	client := &fakePlaidClient{exchangeToken: "access-new", exchangeItem: "new-item"}
	f := plaidOwnerFixture(financeOwnerDoc(), client, newFakeBankTxDB(), psdb)
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidExchangeHandler)), http.MethodPost, "/x", `{"public_token":"public-x"}`, token)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "new-item", psdb.state.ItemID)
	assert.Empty(t, psdb.state.Cursor)
	assert.Equal(t, models.PlaidItemStatusOK, psdb.state.ItemStatus)
	assert.False(t, psdb.state.NewAccountsAvailable)
}

func TestPlaidSandboxWebhook_RefusedInProduction(t *testing.T) {
	t.Setenv("PLAID_ENV", "production")
	t.Setenv("PLAID_ACCESS_TOKEN", "access-1")
	client := &fakePlaidClient{}
	f := plaidOwnerFixture(financeOwnerDoc(), client, newFakeBankTxDB(), &fakePlaidStateDB{})
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidSandboxWebhookHandler)), http.MethodPost, "/x", `{}`, token)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, client.firedCodes)
}

func TestPlaidSandboxWebhook_FiresNewAccounts(t *testing.T) {
	t.Setenv("PLAID_ENV", "sandbox")
	t.Setenv("PLAID_ACCESS_TOKEN", "access-1")
	t.Setenv("PLAID_WEBHOOK_URL", "https://api.test/api/v1/webhooks/plaid")
	client := &fakePlaidClient{}
	f := plaidOwnerFixture(financeOwnerDoc(), client, newFakeBankTxDB(), &fakePlaidStateDB{})
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidSandboxWebhookHandler)), http.MethodPost, "/x", `{}`, token)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"NEW_ACCOUNTS_AVAILABLE"}, client.firedCodes)
	assert.Equal(t, []string{"https://api.test/api/v1/webhooks/plaid"}, client.webhookURLs)
}

func TestPlaidErrorCode(t *testing.T) {
	assert.Equal(t, "", plaidErrorCode(nil))
	assert.Equal(t, "", plaidErrorCode(errors.New("boom")))
	assert.Equal(t, "ITEM_LOGIN_REQUIRED", plaidErrorCode(plaidCodeErr("ITEM_LOGIN_REQUIRED")))
}

func disconnectFixture(t *testing.T, client *fakePlaidClient) (Finance, *fakePlaidStateDB, *fakeBankTxDB, string) {
	t.Helper()
	t.Setenv("PLAID_ACCESS_TOKEN", "access-1")
	psdb := &fakePlaidStateDB{hasState: true, state: models.PlaidSyncState{ItemID: "item-1", Cursor: "c9",
		Accounts: []models.PlaidAccountSnapshot{{AccountID: "a1", Name: "Checking"}}}}
	btdb := newFakeBankTxDB()
	btdb.docs["tx-1"] = models.BankTransaction{TransactionID: "tx-1", Source: "plaid"}
	btdb.docs["tx-2"] = models.BankTransaction{TransactionID: "tx-2", Source: "plaid"}
	f := plaidOwnerFixture(financeOwnerDoc(), client, btdb, psdb)
	return f, psdb, btdb, financeTestToken(t, financeTestSecret, nil)
}

func TestPlaidDisconnect_RemovesTheItemAndDeletesData(t *testing.T) {
	client := &fakePlaidClient{}
	f, psdb, btdb, token := disconnectFixture(t, client)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidDisconnectHandler)), http.MethodPost, "/x", `{"delete_data":true}`, token)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"access-1"}, client.removed)
	assert.NotNil(t, psdb.state.DisconnectedAt)
	assert.Empty(t, psdb.state.Cursor)
	assert.Empty(t, psdb.state.Accounts)
	assert.Empty(t, btdb.docs)
	assert.Contains(t, rec.Body.String(), `"deleted_transactions":2`)

	// The old token is still in the environment, but nothing uses it now.
	assert.Equal(t, "", f.connectedPlaidToken(context.Background()))
	status := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidStatusHandler)), http.MethodGet, "/x", "", token)
	assert.Contains(t, status.Body.String(), `"connected":false`)
	sync := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidSyncHandler)), http.MethodPost, "/x", "", token)
	assert.Equal(t, http.StatusServiceUnavailable, sync.Code)
}

func TestPlaidDisconnect_KeepsDataWhenAsked(t *testing.T) {
	f, _, btdb, token := disconnectFixture(t, &fakePlaidClient{})
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidDisconnectHandler)), http.MethodPost, "/x", `{"delete_data":false}`, token)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, btdb.docs, 2)
}

func TestPlaidDisconnect_ItemAlreadyGoneStillDisconnects(t *testing.T) {
	f, psdb, _, token := disconnectFixture(t, &fakePlaidClient{removeErr: plaidCodeErr("ITEM_NOT_FOUND")})
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidDisconnectHandler)), http.MethodPost, "/x", `{}`, token)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotNil(t, psdb.state.DisconnectedAt)
}

func TestPlaidDisconnect_PlaidFailureChangesNothing(t *testing.T) {
	f, psdb, btdb, token := disconnectFixture(t, &fakePlaidClient{removeErr: plaidCodeErr("INTERNAL_SERVER_ERROR")})
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidDisconnectHandler)), http.MethodPost, "/x", `{"delete_data":true}`, token)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Nil(t, psdb.state.DisconnectedAt)
	assert.Len(t, btdb.docs, 2)
}

func TestPlaidWebhook_IgnoredAfterDisconnect(t *testing.T) {
	now := time.Now()
	f, psdb, signer, syncs := webhookFixture(t, models.PlaidSyncState{ItemID: "item-1", DisconnectedAt: &now})
	rec := postPlaidWebhook(t, f, signer, `{"webhook_type":"TRANSACTIONS","webhook_code":"SYNC_UPDATES_AVAILABLE","item_id":"item-1"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 0, *syncs)
	assert.Empty(t, psdb.state.ItemStatus)
}

func TestPlaidExchange_SwitchingBanksRemovesTheOldItem(t *testing.T) {
	t.Setenv("PLAID_ACCESS_TOKEN", "access-old")
	psdb := &fakePlaidStateDB{hasState: true, state: models.PlaidSyncState{ItemID: "old-item"}}
	client := &fakePlaidClient{exchangeToken: "access-new", exchangeItem: "new-item"}
	f := plaidOwnerFixture(financeOwnerDoc(), client, newFakeBankTxDB(), psdb)
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidExchangeHandler)), http.MethodPost, "/x", `{"public_token":"public-x"}`, token)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"access-old"}, client.removed)
}

func TestPlaidExchange_ReconnectAfterDisconnectClearsIt(t *testing.T) {
	t.Setenv("PLAID_ACCESS_TOKEN", "access-old")
	now := time.Now()
	psdb := &fakePlaidStateDB{hasState: true, state: models.PlaidSyncState{ItemID: "old-item", DisconnectedAt: &now}}
	client := &fakePlaidClient{exchangeToken: "access-new", exchangeItem: "new-item"}
	f := plaidOwnerFixture(financeOwnerDoc(), client, newFakeBankTxDB(), psdb)
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidExchangeHandler)), http.MethodPost, "/x", `{"public_token":"public-x"}`, token)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, psdb.state.DisconnectedAt)
	// Already removed at disconnect: not removed twice.
	assert.Empty(t, client.removed)
}

func TestPlaidErrorReason_PlainErrorPassesThrough(t *testing.T) {
	assert.Equal(t, "", plaidErrorReason(nil))
	assert.Equal(t, "boom", plaidErrorReason(errors.New("boom")))
}

func TestPlaidUpdateComplete_DropsDeselectedAccounts(t *testing.T) {
	t.Setenv("PLAID_ACCESS_TOKEN", "access-1")
	checking := plaid.AccountBase{AccountId: "acc-checking", Name: "Checking"}
	client := &fakePlaidClient{accounts: []plaid.AccountBase{checking}}
	btdb := newFakeBankTxDB()
	btdb.docs["t1"] = models.BankTransaction{TransactionID: "t1", AccountID: "acc-checking", Source: "plaid"}
	btdb.docs["t2"] = models.BankTransaction{TransactionID: "t2", AccountID: "acc-taxes", Source: "plaid"}
	psdb := &fakePlaidStateDB{hasState: true, state: models.PlaidSyncState{ItemID: "item-1"}}
	prev := plaidBackgroundSync
	plaidBackgroundSync = func(Finance) {}
	t.Cleanup(func() { plaidBackgroundSync = prev })

	f := plaidOwnerFixture(financeOwnerDoc(), client, btdb, psdb)
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidUpdateCompleteHandler)), http.MethodPost, "/x", `{}`, token)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"removed_transactions":1`)
	assert.Contains(t, btdb.docs, "t1")
	assert.NotContains(t, btdb.docs, "t2")
	require.Len(t, psdb.state.Accounts, 1)
	assert.Equal(t, "Checking", psdb.state.Accounts[0].Name)
}

func TestPlaidUpdateComplete_NoAccountsAnswerDeletesNothing(t *testing.T) {
	t.Setenv("PLAID_ACCESS_TOKEN", "access-1")
	client := &fakePlaidClient{accountsErr: errors.New("timeout")}
	btdb := newFakeBankTxDB()
	btdb.docs["t1"] = models.BankTransaction{TransactionID: "t1", AccountID: "acc-checking", Source: "plaid"}
	prev := plaidBackgroundSync
	plaidBackgroundSync = func(Finance) {}
	t.Cleanup(func() { plaidBackgroundSync = prev })

	f := plaidOwnerFixture(financeOwnerDoc(), client, btdb, &fakePlaidStateDB{hasState: true})
	token := financeTestToken(t, financeTestSecret, nil)
	rec := runPlaidRequest(t, f, f.RequireOwner(http.HandlerFunc(f.PlaidUpdateCompleteHandler)), http.MethodPost, "/x", `{}`, token)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, btdb.docs, 1)
}
