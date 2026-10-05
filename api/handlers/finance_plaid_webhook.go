package handlers

// Plaid webhooks and update mode for the owner's bank connection.
//
//	POST /api/v1/webhooks/plaid                          (public, signature-verified)
//	POST /api/v1/admin/finance/plaid/update-complete     (RequireOwner)
//	POST /api/v1/admin/finance/plaid/sandbox-webhook     (RequireOwner, Sandbox only)
//	POST /api/v1/admin/finance/plaid/disconnect          (RequireOwner)
//
// Plaid signs every webhook with a JWT in the Plaid-Verification header
// (ES256, key fetched by kid from /webhook_verification_key/get). The body
// hash in the JWT must match the body received, and the JWT must be under
// five minutes old, or the webhook is refused.
//
// What each webhook does:
//
//	TRANSACTIONS SYNC_UPDATES_AVAILABLE  run a sync in the background
//	ITEM ERROR (e.g. ITEM_LOGIN_REQUIRED) item_status=login_required, alert
//	ITEM PENDING_EXPIRATION              item_status=pending_expiration, alert
//	ITEM PENDING_DISCONNECT              item_status=pending_disconnect, alert
//	ITEM USER_PERMISSION_REVOKED,
//	     USER_ACCOUNT_REVOKED            item_status=revoked, alert
//	ITEM LOGIN_REPAIRED                  item_status=ok
//	ITEM NEW_ACCOUNTS_AVAILABLE          new_accounts_available=true
//
// The Finance tab reads item_status and offers Link in update mode (repair)
// or with account selection (new accounts). Finishing it calls
// update-complete, which clears the prompt and syncs.
//
// Env: PLAID_WEBHOOK_URL is the public URL of the webhook route. It is sent
// on new Link tokens and set on the existing item via /item/webhook/update.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/plaid/plaid-go/v39/plaid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api/scheduler"
	"github.com/linesmerrill/police-cad-api/models"
)

const (
	plaidWebhookMaxAge    = 5 * time.Minute
	plaidWebhookKeyTTL    = 24 * time.Hour
	plaidSyncMaxRestarts  = 3
	plaidWebhookBodyLimit = 1 << 20
)

var (
	// plaidSyncMu serialises syncs in this process: the Sync button, page
	// loads and webhooks all advance the same cursor.
	plaidSyncMu sync.Mutex

	errPlaidStatePersist = errors.New("failed to persist the Plaid sync state")

	// plaidBackgroundSync runs a sync without blocking the webhook response.
	// Tests replace it to run inline.
	plaidBackgroundSync = func(f Finance) {
		go f.syncInBackground()
	}
)

func plaidWebhookURL() string {
	return strings.TrimSpace(os.Getenv("PLAID_WEBHOOK_URL"))
}

// plaidRepairCodes are item errors only the owner can fix, in update mode.
var plaidRepairCodes = map[string]bool{
	"ITEM_LOGIN_REQUIRED": true,
	"ACCESS_NOT_GRANTED":  true,
	"INVALID_CREDENTIALS": true,
	"ITEM_LOCKED":         true,
	"USER_SETUP_REQUIRED": true,
}

// ---------------------------------------------------------------------------
// Sync shared by the Sync button, page loads and webhooks
// ---------------------------------------------------------------------------

// syncAndSave runs a sync from the stored cursor and saves the new cursor,
// last sync time and accounts. If Plaid reports the data changed mid-sync
// (TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION) it starts again from the
// stored cursor; the upserts are idempotent, so repeating pages is safe. An
// item error that needs the owner is recorded on the state.
func (f Finance) syncAndSave(ctx context.Context, c plaidSyncClient, accessToken string) (added, modified, removed int, err error) {
	plaidSyncMu.Lock()
	defer plaidSyncMu.Unlock()

	state, err := f.plaidSyncState(ctx)
	if err != nil {
		return 0, 0, 0, err
	}

	var accounts []models.PlaidAccountSnapshot
	var next string
	for attempt := 0; attempt < plaidSyncMaxRestarts; attempt++ {
		added, modified, removed, accounts, next, err = f.runPlaidSync(ctx, c, accessToken, state.Cursor)
		if plaidErrorCode(err) != "TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION" {
			break
		}
	}
	if err != nil {
		f.noteItemError(ctx, err)
		return added, modified, removed, err
	}

	now := time.Now().UTC()
	set := bson.M{"cursor": next, "last_sync_at": now, "updated_at": now}
	if len(accounts) > 0 {
		set["accounts"] = accounts
	}
	if _, uerr := f.PSDB.UpdateOne(ctx, bson.M{}, bson.M{"$set": set}, options.Update().SetUpsert(true)); uerr != nil {
		return added, modified, removed, errPlaidStatePersist
	}
	return added, modified, removed, nil
}

func (f Finance) syncInBackground() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	accessToken := f.connectedPlaidToken(ctx)
	c := f.plaidClient()
	if accessToken == "" || c == nil {
		return
	}
	if _, _, _, err := f.syncAndSave(ctx, c, accessToken); err != nil {
		zap.S().Warnw("background Plaid sync failed", "error", err)
		scheduler.SendWarningAlert(os.Getenv("DYNO"), "Plaid bank sync", err, nil)
	}
}

// ---------------------------------------------------------------------------
// Item status
// ---------------------------------------------------------------------------

// setItemStatus records what Plaid said about the connection. A change into
// a state that needs the owner posts one Discord alert; repeats don't.
func (f Finance) setItemStatus(ctx context.Context, status, errorCode string, extra bson.M) {
	prev, _ := f.plaidSyncState(ctx)
	now := time.Now().UTC()
	set := bson.M{"item_status": status, "item_status_at": now, "updated_at": now}
	unset := bson.M{}
	if errorCode != "" {
		set["item_error_code"] = errorCode
	} else {
		unset["item_error_code"] = ""
	}
	for k, v := range extra {
		set[k] = v
	}
	if status == models.PlaidItemStatusOK {
		unset["consent_expires_at"] = ""
	}
	update := bson.M{"$set": set}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	if _, err := f.PSDB.UpdateOne(ctx, bson.M{}, update, options.Update().SetUpsert(true)); err != nil {
		zap.S().Warnw("failed to save Plaid item status", "status", status, "error", err)
		return
	}

	if status != models.PlaidItemStatusOK && status != prev.ItemStatus {
		fields := map[string]string{"Status": status}
		if errorCode != "" {
			fields["Plaid error"] = errorCode
		}
		scheduler.SendWarningAlert(os.Getenv("DYNO"), "Plaid bank connection",
			fmt.Errorf("the bank connection needs attention: open Finance and use Fix connection"), fields)
	}
}

// noteItemError marks the item as needing repair when an API call failed
// because of the item itself.
func (f Finance) noteItemError(ctx context.Context, err error) {
	if code := plaidErrorCode(err); plaidRepairCodes[code] {
		f.setItemStatus(ctx, models.PlaidItemStatusLoginRequired, code, nil)
	}
}

// refreshItem is the status check's look at the live item: it points the
// item's webhooks at PLAID_WEBHOOK_URL if they aren't already, and calls
// /accounts/get to refresh the accounts and catch a broken login. A login
// that works again clears login_required.
func (f Finance) refreshItem(ctx context.Context, c plaidSyncClient, accessToken string, state models.PlaidSyncState) models.PlaidSyncState {
	if url := plaidWebhookURL(); url != "" && state.WebhookURL != url {
		if err := c.UpdateItemWebhook(ctx, accessToken, url); err != nil {
			zap.S().Warnw("failed to set the Plaid item webhook", "error", err)
		} else {
			_, _ = f.PSDB.UpdateOne(ctx, bson.M{}, bson.M{"$set": bson.M{"webhook_url": url}}, options.Update().SetUpsert(true))
		}
	}

	accounts, err := c.GetAccounts(ctx, accessToken)
	if err != nil {
		f.noteItemError(ctx, err)
	} else {
		snap := make([]models.PlaidAccountSnapshot, 0, len(accounts))
		for _, a := range accounts {
			snap = append(snap, models.PlaidAccountSnapshot{
				AccountID: a.GetAccountId(),
				Name:      a.GetName(),
				Mask:      nullableStringValue(a.Mask),
				Type:      string(a.GetType()),
				Subtype:   nullableSubtypeValue(a.Subtype),
			})
		}
		if len(snap) > 0 {
			_, _ = f.PSDB.UpdateOne(ctx, bson.M{}, bson.M{"$set": bson.M{"accounts": snap}}, options.Update().SetUpsert(true))
		}
		if state.ItemStatus == models.PlaidItemStatusLoginRequired {
			f.setItemStatus(ctx, models.PlaidItemStatusOK, "", nil)
		}
	}

	if fresh, ferr := f.plaidSyncState(ctx); ferr == nil {
		return fresh
	}
	return state
}

// ---------------------------------------------------------------------------
// Webhook signature
// ---------------------------------------------------------------------------

type plaidCachedKey struct {
	key       *ecdsa.PublicKey
	expiredAt int64
	fetchedAt time.Time
}

var (
	plaidKeyMu    sync.Mutex
	plaidKeyCache = map[string]plaidCachedKey{}
)

func plaidWebhookKey(ctx context.Context, c plaidSyncClient, kid string, now time.Time) (*ecdsa.PublicKey, error) {
	plaidKeyMu.Lock()
	cached, ok := plaidKeyCache[kid]
	plaidKeyMu.Unlock()
	if !ok || now.Sub(cached.fetchedAt) > plaidWebhookKeyTTL {
		jwk, err := c.WebhookVerificationKey(ctx, kid)
		if err != nil {
			return nil, fmt.Errorf("fetching the webhook key: %w", err)
		}
		key, err := jwkToECDSA(jwk)
		if err != nil {
			return nil, err
		}
		cached = plaidCachedKey{key: key, fetchedAt: now}
		if v := jwk.ExpiredAt.Get(); v != nil {
			cached.expiredAt = int64(*v)
		}
		plaidKeyMu.Lock()
		plaidKeyCache[kid] = cached
		plaidKeyMu.Unlock()
	}
	if cached.expiredAt != 0 && cached.expiredAt < now.Unix() {
		return nil, errors.New("webhook key has expired")
	}
	return cached.key, nil
}

func jwkToECDSA(jwk plaid.JWKPublicKey) (*ecdsa.PublicKey, error) {
	if jwk.Kty != "EC" || jwk.Crv != "P-256" {
		return nil, fmt.Errorf("unsupported webhook key %s/%s", jwk.Kty, jwk.Crv)
	}
	xb, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(jwk.X, "="))
	if err != nil {
		return nil, errors.New("bad webhook key x")
	}
	yb, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(jwk.Y, "="))
	if err != nil {
		return nil, errors.New("bad webhook key y")
	}
	key := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xb), Y: new(big.Int).SetBytes(yb)}
	if !key.Curve.IsOnCurve(key.X, key.Y) {
		return nil, errors.New("webhook key is not on P-256")
	}
	return key, nil
}

// verifyPlaidWebhook checks the Plaid-Verification JWT against the body.
func verifyPlaidWebhook(ctx context.Context, c plaidSyncClient, token string, body []byte, now time.Time) error {
	if token == "" {
		return errors.New("missing Plaid-Verification header")
	}
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"ES256"}), jwt.WithTimeFunc(func() time.Time { return now }))
	if _, err := parser.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing kid")
		}
		return plaidWebhookKey(ctx, c, kid, now)
	}); err != nil {
		return fmt.Errorf("bad signature: %w", err)
	}

	iat, ok := claims["iat"].(float64)
	if !ok {
		return errors.New("missing iat")
	}
	issued := time.Unix(int64(iat), 0)
	if now.Sub(issued) > plaidWebhookMaxAge || issued.Sub(now) > time.Minute {
		return errors.New("webhook is too old")
	}

	want, _ := claims["request_body_sha256"].(string)
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(want)), []byte(got)) != 1 {
		return errors.New("body does not match the signed hash")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

type plaidWebhookEvent struct {
	WebhookType           string  `json:"webhook_type"`
	WebhookCode           string  `json:"webhook_code"`
	ItemID                string  `json:"item_id"`
	ConsentExpirationTime *string `json:"consent_expiration_time"`
	Error                 *struct {
		ErrorCode string `json:"error_code"`
	} `json:"error"`
}

// PlaidWebhookHandler implements POST /api/v1/webhooks/plaid.
func (f Finance) PlaidWebhookHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	body, err := io.ReadAll(io.LimitReader(r.Body, plaidWebhookBodyLimit))
	if err != nil {
		writeFinanceError(w, http.StatusBadRequest, "could not read body")
		return
	}
	c := f.plaidClient()
	if c == nil {
		writeFinanceError(w, http.StatusServiceUnavailable, "Plaid is not configured")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := verifyPlaidWebhook(ctx, c, r.Header.Get("Plaid-Verification"), body, time.Now()); err != nil {
		zap.S().Warnw("Plaid webhook refused", "error", err)
		writeFinanceError(w, http.StatusUnauthorized, "webhook verification failed")
		return
	}

	var ev plaidWebhookEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		writeFinanceError(w, http.StatusBadRequest, "invalid webhook body")
		return
	}

	state, _ := f.plaidSyncState(ctx)
	// A webhook for an item that has since been replaced, or for a bank the
	// owner disconnected, is old news.
	if state.DisconnectedAt != nil || (state.ItemID != "" && ev.ItemID != "" && ev.ItemID != state.ItemID) {
		writeAdminJSON(w, http.StatusOK, map[string]string{"status": "ignored: different item"})
		return
	}
	_, _ = f.PSDB.UpdateOne(ctx, bson.M{}, bson.M{"$set": bson.M{"last_webhook_at": time.Now().UTC()}}, options.Update().SetUpsert(true))

	switch ev.WebhookType + "/" + ev.WebhookCode {
	case "TRANSACTIONS/SYNC_UPDATES_AVAILABLE":
		plaidBackgroundSync(f)
	case "ITEM/ERROR":
		code := ""
		if ev.Error != nil {
			code = ev.Error.ErrorCode
		}
		if plaidRepairCodes[code] || code == "" {
			f.setItemStatus(ctx, models.PlaidItemStatusLoginRequired, code, nil)
		} else {
			zap.S().Warnw("Plaid item error", "error_code", code)
		}
	case "ITEM/LOGIN_REPAIRED":
		f.setItemStatus(ctx, models.PlaidItemStatusOK, "", nil)
	case "ITEM/PENDING_EXPIRATION":
		extra := bson.M{}
		if ev.ConsentExpirationTime != nil {
			if t, err := time.Parse(time.RFC3339, *ev.ConsentExpirationTime); err == nil {
				extra["consent_expires_at"] = t.UTC()
			}
		}
		f.setItemStatus(ctx, models.PlaidItemStatusPendingExpiration, "", extra)
	case "ITEM/PENDING_DISCONNECT":
		f.setItemStatus(ctx, models.PlaidItemStatusPendingDisconnect, "", nil)
	case "ITEM/USER_PERMISSION_REVOKED", "ITEM/USER_ACCOUNT_REVOKED":
		f.setItemStatus(ctx, models.PlaidItemStatusRevoked, ev.WebhookCode, nil)
	case "ITEM/NEW_ACCOUNTS_AVAILABLE":
		_, _ = f.PSDB.UpdateOne(ctx, bson.M{}, bson.M{"$set": bson.M{"new_accounts_available": true}}, options.Update().SetUpsert(true))
	default:
		zap.S().Infow("Plaid webhook not acted on", "type", ev.WebhookType, "code", ev.WebhookCode)
	}

	writeAdminJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// PlaidUpdateCompleteHandler implements POST
// /api/v1/admin/finance/plaid/update-complete, called when the owner finishes
// Link in update mode. The connection is repaired (and any new accounts
// picked), so the prompts clear and a sync picks up what was missed.
func (f Finance) PlaidUpdateCompleteHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	f.setItemStatus(ctx, models.PlaidItemStatusOK, "", bson.M{"new_accounts_available": false})
	plaidBackgroundSync(f)
	writeAdminJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// plaidSandboxWebhookCodes are the test webhooks the sandbox route may fire.
var plaidSandboxWebhookCodes = map[string]bool{
	"NEW_ACCOUNTS_AVAILABLE": true,
	"SYNC_UPDATES_AVAILABLE": true,
	"LOGIN_REPAIRED":         true,
	"PENDING_DISCONNECT":     true,
}

// PlaidSandboxWebhookHandler implements POST
// /api/v1/admin/finance/plaid/sandbox-webhook {"code": ...}: asks Plaid to fire
// a test webhook at this item. Sandbox only; refused in production.
func (f Finance) PlaidSandboxWebhookHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if plaidEnvironment() != plaid.Sandbox {
		writeFinanceError(w, http.StatusForbidden, "test webhooks are only available in the Plaid Sandbox")
		return
	}
	c := f.plaidClient()
	accessToken := f.connectedPlaidToken(r.Context())
	if c == nil || accessToken == "" {
		writeFinanceError(w, http.StatusConflict, "connect a Sandbox bank first")
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.Code == "" {
		in.Code = "NEW_ACCOUNTS_AVAILABLE"
	}
	if !plaidSandboxWebhookCodes[in.Code] {
		writeFinanceError(w, http.StatusBadRequest, "unsupported test webhook code")
		return
	}
	url := plaidWebhookURL()
	if url == "" {
		writeFinanceError(w, http.StatusConflict, "set PLAID_WEBHOOK_URL first")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	// The item has to know where to send it.
	if err := c.UpdateItemWebhook(ctx, accessToken, url); err != nil {
		writeFinanceError(w, http.StatusBadGateway, "failed to set the item webhook: "+err.Error())
		return
	}
	_, _ = f.PSDB.UpdateOne(ctx, bson.M{}, bson.M{"$set": bson.M{"webhook_url": url}}, options.Update().SetUpsert(true))
	if err := c.FireSandboxWebhook(ctx, accessToken, in.Code); err != nil {
		writeFinanceError(w, http.StatusBadGateway, "failed to fire the test webhook: "+err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"status": "fired", "code": in.Code})
}

// plaidGoneCodes mean the item no longer exists at Plaid, so removing it
// again has nothing left to do.
var plaidGoneCodes = map[string]bool{
	"ITEM_NOT_FOUND":       true,
	"INVALID_ACCESS_TOKEN": true,
}

// PlaidDisconnectHandler implements POST /api/v1/admin/finance/plaid/disconnect
// {"delete_data": bool}. It calls /item/remove, so the access token stops
// working and Plaid stops billing for the item, and marks the bank as
// disconnected. With delete_data (the Finance tab's default) it also deletes
// every synced bank transaction: once the bank is gone there's no reason to
// keep its data. Tags and merchant rules are the owner's own and are kept.
func (f Finance) PlaidDisconnectHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	c := f.plaidClient()
	if c == nil {
		writeFinanceError(w, http.StatusServiceUnavailable, "Plaid is not configured")
		return
	}
	var in struct {
		DeleteData bool `json:"delete_data"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	accessToken := f.connectedPlaidToken(ctx)
	if accessToken == "" {
		writeFinanceError(w, http.StatusConflict, "no bank is connected")
		return
	}

	plaidSyncMu.Lock()
	defer plaidSyncMu.Unlock()

	if err := c.RemoveItem(ctx, accessToken); err != nil && !plaidGoneCodes[plaidErrorCode(err)] {
		writeFinanceError(w, http.StatusBadGateway, "Plaid could not remove the connection: "+err.Error())
		return
	}

	now := time.Now().UTC()
	if _, err := f.PSDB.UpdateOne(ctx, bson.M{}, bson.M{
		"$set": bson.M{"disconnected_at": now, "item_status": models.PlaidItemStatusOK, "updated_at": now},
		"$unset": bson.M{
			"cursor": "", "accounts": "", "item_error_code": "", "consent_expires_at": "",
			"new_accounts_available": "", "webhook_url": "",
		},
	}, options.Update().SetUpsert(true)); err != nil {
		writeFinanceError(w, http.StatusInternalServerError, "the bank was removed at Plaid but the state could not be saved")
		return
	}

	var deleted int64
	if in.DeleteData {
		n, err := f.BTDB.DeleteMany(ctx, bson.M{"source": "plaid"})
		if err != nil {
			writeFinanceError(w, http.StatusInternalServerError, "the bank was disconnected but its transactions could not be deleted")
			return
		}
		deleted = n
	}

	writeAdminJSON(w, http.StatusOK, map[string]interface{}{
		"disconnected":         true,
		"deleted_transactions": deleted,
		"message":              "Disconnected. You can now remove PLAID_ACCESS_TOKEN from the API's Heroku config.",
	})
}
