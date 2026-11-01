package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// Webhooks: the same events, pushed instead of polled.
//
// This reverses an answer we gave on #277, and the reversal is worth being
// explicit about. The reasons for declining were real — outbound HTTP to
// developer-controlled URLs is an SSRF surface, and delivery brings signing,
// retries, replay protection and dead-letters with it. None of those arguments
// goes away by shipping. They are answered here rather than avoided:
//
//	SSRF           — https only, and the resolved address is checked against
//	                 every private, loopback, link-local and metadata range
//	                 before a request is made. Redirects are refused, because
//	                 a public URL that redirects to 169.254.169.254 is the
//	                 whole trick.
//	Signing        — HMAC-SHA256 over the timestamp and the body, with a
//	                 per-endpoint secret shown once and rotatable.
//	Replay         — the timestamp is inside the signed material and the
//	                 tolerance is published, so a captured delivery stops being
//	                 replayable five minutes later.
//	Retries        — a published, finite backoff, and an endpoint that has
//	                 stopped answering entirely is switched off rather than
//	                 retried forever at everyone else's expense.
//	Dead-lettering — nothing is lost: a failed delivery leaves the endpoint's
//	                 cursor where it was, so the event is still readable from
//	                 the polling endpoint for its retention window.
//
// The last line is the design's load-bearing part. Webhooks are a transport
// over the event log, not a second source of truth, so a missed delivery
// degrades to the polling case rather than to a gap.

// Env var holding the secret that keys the stored hash of a webhook secret.
//
// The same pepper the API keys use. One secret to configure, and one that
// already has to be set for the surface to run at all.
const webhookSecretPrefix = "whsec_"

// Header names on a delivery. Prefixed so a receiver with several integrations
// can tell whose request it is reading.
const (
	webhookSignatureHeader = "X-LPC-Signature"
	webhookEventIDHeader   = "X-LPC-Event-Id"
	webhookEventTypeHeader = "X-LPC-Event-Type"
	webhookDeliveryHeader  = "X-LPC-Delivery-Id"
	webhookAttemptHeader   = "X-LPC-Attempt"
)

// ---- signing ----

// generateWebhookSecret mints a signing secret and returns the plaintext
// alongside what we store.
//
// 32 bytes of crypto/rand behind a recognisable prefix, like the API keys and
// for the same reason: a secret that leaks into a log or a paste should be
// identifiable on sight.
func generateWebhookSecret() (plaintext, hash, displayPrefix string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", "", err
	}
	secret := hex.EncodeToString(raw)
	plaintext = webhookSecretPrefix + secret
	hash = hashWebhookSecret(plaintext)
	displayPrefix = webhookSecretPrefix + secret[:6]
	return plaintext, hash, displayPrefix, nil
}

// hashWebhookSecret is how a stored secret is kept.
//
// Keyed with the same server-side pepper the API keys use, so a database dump
// does not hand anyone the ability to forge a delivery a receiver would accept.
// The secret is 32 bytes of crypto/rand, so there is no dictionary to defend
// against and a keyed digest is the right tool rather than a slow KDF.
func hashWebhookSecret(plaintext string) string {
	mac := hmac.New(sha256.New, []byte(os.Getenv(devAPIKeyPepperEnv)))
	mac.Write([]byte(plaintext))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignWebhookPayload builds the signature header value for one delivery.
//
// The timestamp is signed along with the body, which is what makes a captured
// delivery stop being replayable: a receiver that checks the age of t has a
// window, and a value that is not inside the signed material would be one an
// attacker could simply rewrite.
//
// The `v1=` prefix is a version marker. When the scheme changes, a second
// value joins the header rather than replacing it, so a receiver can verify
// either while it migrates.
func SignWebhookPayload(secret string, at time.Time, body []byte) string {
	timestamp := strconv.FormatInt(at.UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "t=" + timestamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// ---- endpoint validation ----

var (
	errWebhookNotHTTPS    = errors.New("a webhook URL must be https")
	errWebhookPrivateHost = errors.New("that host is not reachable from the public internet, so we will not send to it")
	errWebhookNoHost      = errors.New("that URL has no host")
	errWebhookBadURL      = errors.New("that is not a URL we can send to")
)

// validateWebhookURL decides whether we are willing to make requests to a URL.
//
// This is the SSRF gate, and it runs in two places on purpose: here, when a
// developer saves the endpoint, and again in the delivery worker immediately
// before the request. Checking only at save time would be checkable-then-
// changeable — DNS is not ours, and a hostname that resolved publicly on
// Tuesday can point at 127.0.0.1 on Wednesday.
//
// https only. An http endpoint would ship the signature and the event body in
// clear, and a developer who cannot terminate TLS cannot safely receive these.
func validateWebhookURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 2048 {
		return "", errWebhookBadURL
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", errWebhookBadURL
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return "", errWebhookNotHTTPS
	}
	host := parsed.Hostname()
	if host == "" {
		return "", errWebhookNoHost
	}
	// Credentials in a webhook URL would end up in our logs and our database.
	// A receiver that needs to authenticate us has the signature.
	if parsed.User != nil {
		return "", errWebhookBadURL
	}
	if err := assertPublicHost(host); err != nil {
		return "", err
	}
	// Rebuilt from the parsed URL rather than returned as it arrived, the same
	// discipline every other outside value on this surface gets.
	rebuilt := url.URL{Scheme: "https", Host: parsed.Host, Path: parsed.Path, RawQuery: parsed.RawQuery}
	return rebuilt.String(), nil
}

// assertPublicHost refuses a host that resolves anywhere we should not reach.
//
// Every resolved address has to pass, not just the first. A hostname with two A
// records, one public and one loopback, would otherwise be a coin flip — and
// the attacker gets to flip it as many times as they like.
func assertPublicHost(host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return errWebhookPrivateHost
		}
		return nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil || len(addrs) == 0 {
		// A name we cannot resolve is one we will not send to. Saving it would
		// mean the first check a developer gets is a failed delivery.
		return errWebhookPrivateHost
	}
	for _, ip := range addrs {
		if !isPublicIP(ip) {
			return errWebhookPrivateHost
		}
	}
	return nil
}

// cloudMetadataIPs are the link-local addresses cloud providers answer
// instance credentials on. Covered by the link-local range below as well;
// named explicitly because they are the specific thing this gate exists to
// stop, and a reader should not have to work that out from a netmask.
var cloudMetadataIPs = []string{"169.254.169.254", "fd00:ec2::254"}

// isPublicIP reports whether an address is one on the public internet.
func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() ||
		ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, blocked := range cloudMetadataIPs {
		if ip.Equal(net.ParseIP(blocked)) {
			return false
		}
	}
	// Carrier-grade NAT and the IPv4 benchmarking range are neither private by
	// Go's definition nor anywhere a developer's endpoint lives.
	for _, cidr := range []string{"100.64.0.0/10", "198.18.0.0/15", "192.0.0.0/24", "::/128"} {
		if _, network, err := net.ParseCIDR(cidr); err == nil && network.Contains(ip) {
			return false
		}
	}
	return true
}

// ---- the console's endpoints ----

type devWebhookWriteRequest struct {
	URL string `json:"url"`
	// Events empty means every type, which is what most integrations want and
	// saves them keeping a list in step with ours.
	Events []string `json:"events"`
	// Communities empty means every community the key is granted, re-read per
	// delivery — so a community owner withdrawing their grant stops deliveries
	// about them immediately, exactly as it stops requests.
	Communities []string `json:"communities"`
}

// ListDeveloperWebhooksHandler returns a key's endpoints.
//
// GET /api/v1/developer/keys/{keyId}/webhooks
func (d DeveloperAPI) ListDeveloperWebhooksHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	key, ok := d.requireOwnDeveloperKey(w, r)
	if !ok {
		return
	}
	if d.WebhookDB == nil {
		config.ErrorStatus("webhooks unavailable", http.StatusInternalServerError, w, errNoWebhookStore)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	hooks, err := d.WebhookDB.Find(ctx, bson.M{"keyId": devEq(key.ID.Hex())},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}}))
	if err != nil {
		config.ErrorStatus("failed to load webhooks", http.StatusInternalServerError, w, err)
		return
	}

	out := make([]map[string]interface{}, 0, len(hooks))
	for _, hook := range hooks {
		out = append(out, d.webhookView(ctx, hook))
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"webhooks": out,
		"limits": map[string]interface{}{
			"maxPerKey":                 models.MaxWebhooksPerKey,
			"maxAttempts":               models.WebhookMaxAttempts,
			"signatureToleranceSeconds": models.WebhookSignatureToleranceSeconds,
			"disableAfterFailures":      models.WebhookDisableAfterConsecutiveFailures,
			"deliveryRetentionDays":     models.DeveloperWebhookDeliveryRetentionDays,
		},
		"eventTypes": models.DeveloperEventTypes(),
	})
}

// CreateDeveloperWebhookHandler registers an endpoint and returns its secret.
//
// POST /api/v1/developer/keys/{keyId}/webhooks
//
// The secret is shown exactly once, like the key itself. Rotating is how a
// developer who lost it carries on, and that keeps the endpoint and its
// subscription rather than making them rebuild it.
func (d DeveloperAPI) CreateDeveloperWebhookHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	key, ok := d.requireOwnDeveloperKey(w, r)
	if !ok {
		return
	}
	if d.WebhookDB == nil {
		config.ErrorStatus("webhooks unavailable", http.StatusInternalServerError, w, errNoWebhookStore)
		return
	}

	var req devWebhookWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.InfoStatus("invalid body", http.StatusBadRequest, w, err)
		return
	}

	endpoint, err := validateWebhookURL(req.URL)
	if err != nil {
		config.InfoStatus("invalid webhook url", http.StatusBadRequest, w, err)
		return
	}
	events, err := validateWebhookEvents(req.Events)
	if err != nil {
		config.InfoStatus("invalid events", http.StatusBadRequest, w, err)
		return
	}
	communities, err := d.validateWebhookCommunities(r, key, req.Communities)
	if err != nil {
		config.InfoStatus("invalid communities", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	existing, _ := d.WebhookDB.CountDocuments(ctx, bson.M{"keyId": devEq(key.ID.Hex())})
	if int(existing) >= models.MaxWebhooksPerKey {
		config.InfoStatus("webhook limit reached", http.StatusConflict, w,
			fmt.Errorf("a key may have %d webhooks; delete one first", models.MaxWebhooksPerKey))
		return
	}

	secret, hash, prefix, err := generateWebhookSecret()
	if err != nil {
		config.ErrorStatus("failed to generate a signing secret", http.StatusInternalServerError, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	hook := models.DeveloperWebhook{
		ID:           primitive.NewObjectID(),
		KeyID:        key.ID.Hex(),
		DeveloperID:  key.DeveloperID,
		URL:          endpoint,
		SecretHash:   hash,
		SecretPrefix: prefix,
		Events:       events,
		Communities:  communities,
		Status:       models.WebhookStatusActive,
		// Starts at now rather than at the beginning of the log. A new endpoint
		// that immediately received a week of history would be a surprise, and
		// a developer who wants that history can read it from the cursor
		// endpoint deliberately.
		Cursor:    primitive.NewObjectIDFromTimestamp(time.Now()).Hex(),
		CreatedAt: now,
		UpdatedAt: now,
	}

	if _, err := d.WebhookDB.InsertOne(ctx, hook); err != nil {
		config.ErrorStatus("failed to create that webhook", http.StatusInternalServerError, w, err)
		return
	}

	logAudit(d.ALDB, primitive.NilObjectID, "developer_webhook.created", "developer",
		key.UserID, resolveActorName(d.UDB, key.UserID), hook.ID.Hex(), endpoint,
		map[string]interface{}{"keyId": key.ID.Hex()})

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"webhook": d.webhookView(ctx, hook),
		// Once. We store a keyed hash and nothing else, so this cannot be
		// shown again — rotating issues a new one.
		"signingSecret": secret,
	})
}

// UpdateDeveloperWebhookHandler changes an endpoint's URL or subscription.
//
// PATCH /api/v1/developer/keys/{keyId}/webhooks/{webhookId}
//
// Also the way to switch a disabled endpoint back on: saving it clears the
// failure count, because a developer editing an endpoint has usually just
// fixed whatever made it fail.
func (d DeveloperAPI) UpdateDeveloperWebhookHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	key, ok := d.requireOwnDeveloperKey(w, r)
	if !ok {
		return
	}
	hook, ok := d.requireOwnWebhook(w, r, key)
	if !ok {
		return
	}

	var req struct {
		devWebhookWriteRequest
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.InfoStatus("invalid body", http.StatusBadRequest, w, err)
		return
	}

	set := bson.M{"updatedAt": primitive.NewDateTimeFromTime(time.Now())}

	if strings.TrimSpace(req.URL) != "" {
		endpoint, err := validateWebhookURL(req.URL)
		if err != nil {
			config.InfoStatus("invalid webhook url", http.StatusBadRequest, w, err)
			return
		}
		set["url"] = endpoint
	}
	if req.Events != nil {
		events, err := validateWebhookEvents(req.Events)
		if err != nil {
			config.InfoStatus("invalid events", http.StatusBadRequest, w, err)
			return
		}
		set["events"] = events
	}
	if req.Communities != nil {
		communities, err := d.validateWebhookCommunities(r, key, req.Communities)
		if err != nil {
			config.InfoStatus("invalid communities", http.StatusBadRequest, w, err)
			return
		}
		set["communities"] = communities
	}
	if req.Enabled != nil {
		if *req.Enabled {
			set["status"] = models.WebhookStatusActive
			set["consecutiveFailures"] = 0
			set["disabledReason"] = ""
		} else {
			set["status"] = models.WebhookStatusDisabled
			set["disabledReason"] = "Turned off by the developer."
		}
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	if _, err := d.WebhookDB.UpdateOne(ctx, bson.M{"_id": devEq(hook.ID)}, bson.M{"$set": set}); err != nil {
		config.ErrorStatus("failed to update that webhook", http.StatusInternalServerError, w, err)
		return
	}

	updated, err := d.WebhookDB.FindOne(ctx, bson.M{"_id": devEq(hook.ID)})
	if err != nil || updated == nil {
		config.ErrorStatus("failed to reload that webhook", http.StatusInternalServerError, w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"webhook": d.webhookView(ctx, *updated)})
}

// RotateDeveloperWebhookSecretHandler issues a new signing secret.
//
// POST /api/v1/developer/keys/{keyId}/webhooks/{webhookId}/rotate
//
// The endpoint, its subscription and its cursor are all kept, so rotating is
// not an outage — only the secret changes, and the next delivery is signed with
// it. A receiver that has not been updated will reject that delivery and it
// will retry, which is the behaviour to expect and is documented.
func (d DeveloperAPI) RotateDeveloperWebhookSecretHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	key, ok := d.requireOwnDeveloperKey(w, r)
	if !ok {
		return
	}
	hook, ok := d.requireOwnWebhook(w, r, key)
	if !ok {
		return
	}

	secret, hash, prefix, err := generateWebhookSecret()
	if err != nil {
		config.ErrorStatus("failed to generate a signing secret", http.StatusInternalServerError, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	now := primitive.NewDateTimeFromTime(time.Now())
	if _, err := d.WebhookDB.UpdateOne(ctx, bson.M{"_id": devEq(hook.ID)}, bson.M{"$set": bson.M{
		"secretHash":      hash,
		"secretPrefix":    prefix,
		"secretRotatedAt": now,
		"updatedAt":       now,
	}}); err != nil {
		config.ErrorStatus("failed to rotate that secret", http.StatusInternalServerError, w, err)
		return
	}

	logAudit(d.ALDB, primitive.NilObjectID, "developer_webhook.secret_rotated", "developer",
		key.UserID, resolveActorName(d.UDB, key.UserID), hook.ID.Hex(), hook.URL,
		map[string]interface{}{"keyId": key.ID.Hex()})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"signingSecret": secret})
}

// DeleteDeveloperWebhookHandler removes an endpoint.
//
// DELETE /api/v1/developer/keys/{keyId}/webhooks/{webhookId}
func (d DeveloperAPI) DeleteDeveloperWebhookHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	key, ok := d.requireOwnDeveloperKey(w, r)
	if !ok {
		return
	}
	hook, ok := d.requireOwnWebhook(w, r, key)
	if !ok {
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	if err := d.WebhookDB.DeleteOne(ctx, bson.M{"_id": devEq(hook.ID)}); err != nil {
		config.ErrorStatus("failed to delete that webhook", http.StatusInternalServerError, w, err)
		return
	}

	logAudit(d.ALDB, primitive.NilObjectID, "developer_webhook.deleted", "developer",
		key.UserID, resolveActorName(d.UDB, key.UserID), hook.ID.Hex(), hook.URL,
		map[string]interface{}{"keyId": key.ID.Hex()})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"deleted": true})
}

// ListDeveloperWebhookDeliveriesHandler returns an endpoint's attempt log.
//
// GET /api/v1/developer/keys/{keyId}/webhooks/{webhookId}/deliveries
//
// This exists because "my webhook is not receiving anything" is the question
// every webhook integration eventually asks, and the answer is usually visible
// here: a 404 from their own router, a TLS failure, or an endpoint we switched
// off. The log holds our status and our error text, never the receiver's
// response body — their error page is their content.
func (d DeveloperAPI) ListDeveloperWebhookDeliveriesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	key, ok := d.requireOwnDeveloperKey(w, r)
	if !ok {
		return
	}
	hook, ok := d.requireOwnWebhook(w, r, key)
	if !ok {
		return
	}
	if d.WebhookDeliveryDB == nil {
		config.ErrorStatus("delivery log unavailable", http.StatusInternalServerError, w, errNoWebhookStore)
		return
	}

	page, limit := devPageParams(r, 100)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"webhookId": devEq(hook.ID.Hex())}
	total, _ := d.WebhookDeliveryDB.CountDocuments(ctx, filter)
	rows, err := d.WebhookDeliveryDB.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "at", Value: -1}, {Key: "_id", Value: -1}}).
		SetSkip(int64((page-1)*limit)).
		SetLimit(int64(limit)))
	if err != nil {
		config.ErrorStatus("failed to load deliveries", http.StatusInternalServerError, w, err)
		return
	}

	out := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]interface{}{
			"eventId":    row.EventID,
			"eventType":  row.EventType,
			"attempt":    row.Attempt,
			"status":     row.Status,
			"succeeded":  row.Succeeded,
			"error":      row.Error,
			"durationMs": row.DurationMS,
			"at":         formatDevDateTime(row.At),
		})
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"deliveries":    out,
		"totalCount":    total,
		"page":          page,
		"limit":         limit,
		"retentionDays": models.DeveloperWebhookDeliveryRetentionDays,
	})
}

// ---- shared ----

var errNoWebhookStore = errors.New("no webhook store configured")

// requireOwnWebhook resolves an endpoint and proves it belongs to this key.
//
// Both ids in the filter, so an endpoint id from somebody else's key is a 404.
func (d DeveloperAPI) requireOwnWebhook(w http.ResponseWriter, r *http.Request, key *models.DeveloperAPIKey) (*models.DeveloperWebhook, bool) {
	if d.WebhookDB == nil {
		config.ErrorStatus("webhooks unavailable", http.StatusInternalServerError, w, errNoWebhookStore)
		return nil, false
	}
	_, oid, err := devSafeObjectID(mux.Vars(r)["webhookId"])
	if err != nil {
		config.InfoStatus("invalid webhook id", http.StatusBadRequest, w, err)
		return nil, false
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	hook, err := d.WebhookDB.FindOne(ctx, bson.M{
		"_id":   devEq(oid),
		"keyId": devEq(key.ID.Hex()),
	})
	if err != nil || hook == nil {
		config.InfoStatus("webhook not found", http.StatusNotFound, w, errKeyNotFound)
		return nil, false
	}
	return hook, true
}

// validateWebhookEvents checks a subscription against the types we emit.
//
// An unknown type is refused rather than ignored: a developer who subscribed
// to "call.close" and silently received nothing would have no way to find out
// why.
func validateWebhookEvents(events []string) ([]string, error) {
	if len(events) == 0 {
		return []string{}, nil
	}
	out := make([]string, 0, len(events))
	for _, raw := range events {
		wanted := strings.TrimSpace(raw)
		matched := ""
		for _, known := range models.DeveloperEventTypes() {
			if known == wanted {
				matched = known
				break
			}
		}
		if matched == "" {
			return nil, fmt.Errorf("%q is not an event type we emit", wanted)
		}
		if !containsString(out, matched) {
			out = append(out, matched)
		}
	}
	return out, nil
}

// validateWebhookCommunities checks a community filter against the key's grants.
//
// A developer cannot subscribe an endpoint to a community the key is not
// granted. Not because it would leak anything — the worker re-checks grants per
// delivery — but because a subscription that can never fire is a configuration
// mistake, and refusing it at save time is where it can still be fixed.
func (d DeveloperAPI) validateWebhookCommunities(r *http.Request, key *models.DeveloperAPIKey, raw []string) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	granted, err := d.ActiveGrantCommunityIDs(ctx, key.ID.Hex())
	if err != nil {
		return nil, fmt.Errorf("could not read this key's communities")
	}

	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		communityID, _, idErr := devSafeObjectID(entry)
		if idErr != nil {
			return nil, fmt.Errorf("that community id is not valid")
		}
		if !containsString(granted, communityID) {
			return nil, fmt.Errorf("this key is not granted %s", communityID)
		}
		if !containsString(out, communityID) {
			out = append(out, communityID)
		}
	}
	return out, nil
}

// webhookView shapes an endpoint for the console. Never the secret.
func (d DeveloperAPI) webhookView(ctx context.Context, hook models.DeveloperWebhook) map[string]interface{} {
	_ = ctx
	out := map[string]interface{}{
		"id":                  hook.ID.Hex(),
		"url":                 hook.URL,
		"events":              hook.Events,
		"communities":         hook.Communities,
		"status":              hook.Status,
		"secretPrefix":        hook.SecretPrefix,
		"consecutiveFailures": hook.ConsecutiveFailures,
		"lastStatus":          hook.LastStatus,
		"lastError":           hook.LastError,
		"disabledReason":      hook.DisabledReason,
		"createdAt":           formatDevDateTime(hook.CreatedAt),
	}
	if hook.LastDeliveryAt != nil {
		out["lastDeliveryAt"] = formatDevDateTime(*hook.LastDeliveryAt)
	}
	if hook.SecretRotatedAt != nil {
		out["secretRotatedAt"] = formatDevDateTime(*hook.SecretRotatedAt)
	}
	// Empty means "everything", which is not the same statement as "nothing"
	// and is the one a reader of this screen would otherwise get wrong.
	if len(hook.Events) == 0 {
		out["eventsLabel"] = "every event"
	}
	if len(hook.Communities) == 0 {
		out["communitiesLabel"] = "every community this key can reach"
	}
	return out
}

// ValidateWebhookURLForTest exposes the console's address gate so an external
// test can check it against the delivery worker's copy. See
// api/scheduler/developer_webhooks_sign_test.go for why there are two.
func ValidateWebhookURLForTest(raw string) error {
	_, err := validateWebhookURL(raw)
	return err
}
