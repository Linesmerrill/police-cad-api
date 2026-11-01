package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// The authenticated edge of the Developer API.
//
// This file owns everything a third-party request passes through before a
// handler sees it: the key, the developer account behind it, the rate limits,
// and the usage counters. The scope check and the community grant check are
// separate, in developer_api_scope.go, because they depend on the route.
//
// The one rule that shapes all of it: a developer key must never satisfy any
// authentication the website or the app uses, and nothing the website or the
// app presents may satisfy this one. They share no code path on purpose, so a
// change to either cannot quietly widen the other.

// devAPIPathPrefix is the only prefix the developer surface answers on. It is
// also the prefix the generic gateway and write-auth middleware exempt, so it
// has to be exact and it has to be the only thing registered there.
const devAPIPathPrefix = "/api/v1/dev/"

// devAPIEnabledEnv gates the whole surface. Off by default: the feature ships
// dark, and nothing in the product changes until it is switched on.
const devAPIEnabledEnv = "DEV_API_ENABLED"

// devAPIGlobalRPMEnv caps the entire developer surface regardless of per-key
// limits, so one pathological integration cannot spend the database on
// everyone else's behalf. Zero or unset means no global cap.
const devAPIGlobalRPMEnv = "DEV_API_GLOBAL_RPM"

// devAPIGlobalRateKey is the counter id the global cap increments. It shares the
// rate collection with per-key counters; no real key id can collide with it
// because key ids are ObjectID hex.
const devAPIGlobalRateKey = "__global__"

// DeveloperAPIEnabled reports whether the developer surface is switched on.
func DeveloperAPIEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(devAPIEnabledEnv)), "true")
}

// IsDeveloperAPIPath reports whether a request path belongs to the developer
// surface.
//
// The generic gateway (gateway.go) and the write gate (api/write_auth.go) both
// call this to step aside, because neither can authenticate a developer key: a
// mod presents no first-party secret, no browser Origin and no bearer session,
// and its User-Agent may well look like a scripting tool. Stepping aside is
// only safe because RequireDeveloperAPIKey then applies a strictly higher bar
// than the gate it replaced — a valid unrevoked key, an active developer
// account, an active grant for the community in the path, and the scope the
// route needs — and because nothing except the developer routes is registered
// under this prefix.
func IsDeveloperAPIPath(path string) bool {
	return strings.HasPrefix(path, devAPIPathPrefix)
}

// DeveloperAPI holds what the developer surface needs from the database.
type DeveloperAPI struct {
	AccountDB   databases.DeveloperAccountDatabase
	KeyDB       databases.DeveloperAPIKeyDatabase
	GrantDB     databases.DeveloperGrantDatabase
	UsageDB     databases.DeveloperUsageDatabase
	RateDB      databases.DeveloperRateDatabase
	IdentityDB  databases.PlayerIdentityDatabase
	ClaimCodeDB databases.DeveloperClaimCodeDatabase
	CDB         databases.CommunityDatabase
	UDB         databases.UserDatabase
	ALDB        databases.AuditLogDatabase

	// Record stores, used by provisioning and by the claim transfer.
	CivDB  databases.CivilianDatabase
	VehDB  databases.VehicleDatabase
	FirDB  databases.FirearmDatabase
	LicDB  databases.LicenseDatabase
	CallDB databases.CallDatabase
	BoloDB databases.BoloDatabase
}

// ---- key material ----

// generateDeveloperKey mints a new key and returns the plaintext (shown to the
// developer exactly once) alongside what we store.
//
// 32 bytes from crypto/rand, hex-encoded, behind the lpc_live_ prefix. The
// prefix is not decoration: it makes a leaked key recognisable on sight in a
// log, a paste or a public repository, which is how most key leaks are actually
// caught.
func generateDeveloperKey() (plaintext, hash, displayPrefix, lastFour string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", "", "", err
	}
	secret := hex.EncodeToString(raw)
	plaintext = models.DeveloperAPIKeyPrefix + secret
	hash = hashDeveloperKey(plaintext)
	displayPrefix = models.DeveloperAPIKeyPrefix + secret[:8]
	lastFour = secret[len(secret)-4:]
	return plaintext, hash, displayPrefix, lastFour, nil
}

// devAPIKeyPepperEnv holds the server-side secret that keys the token hash.
//
// Required whenever the developer surface is enabled. The surface is off by
// default, so this costs nothing until it is turned on, and refusing to run
// without it is better than silently falling back to a weaker hash — a fallback
// would also mean every key minted before the secret was set stopped
// validating the moment it appeared.
const devAPIKeyPepperEnv = "DEV_API_KEY_PEPPER"

// hashDeveloperKey is how a presented key becomes the value we look up.
//
// HMAC-SHA256, keyed with a server-side secret, rather than bcrypt or a bare
// digest. Three separate decisions, each worth stating because each one looks
// wrong from a distance:
//
// Not bcrypt/argon2. Those exist to make guessing a LOW-ENTROPY secret
// expensive. This key is 32 bytes straight from crypto/rand, so there is no
// dictionary and no human pattern to exploit, and the search space is already
// beyond reach. What a slow hash would buy is nothing; what it would cost is
// real, because this runs on every single request and bcrypt's deliberate cost
// would become the request budget.
//
// Not a bare SHA-256 either, which is where this started. A bare digest is
// invertible-by-lookup for anyone who already holds a candidate key: an
// attacker with a database dump could confirm "is this key one of yours"
// offline, and could match a key leaked from somewhere else against the table.
// Keying the hash with a secret the database does not contain removes that:
// a dump alone proves nothing and verifies nothing.
//
// Deterministic, not salted per row, because this value is looked up by
// equality on every request. A per-row salt would mean scanning the table.
func hashDeveloperKey(plaintext string) string {
	mac := hmac.New(sha256.New, []byte(os.Getenv(devAPIKeyPepperEnv)))
	mac.Write([]byte(plaintext))
	return hex.EncodeToString(mac.Sum(nil))
}

// developerKeyPepperConfigured reports whether the hashing secret is set.
func developerKeyPepperConfigured() bool {
	return strings.TrimSpace(os.Getenv(devAPIKeyPepperEnv)) != ""
}

// claimCodeAlphabet is Crockford base32: no I, L, O or U, so a code read off a
// game screen and typed into a browser cannot be transcribed wrong.
const claimCodeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// generateClaimCode mints a one-time claim code and its hash.
//
// Ten characters of Crockford base32 is 50 bits. Short, because it is read off
// a screen other players may be able to see, which is also why it expires in
// half an hour and works once. The hash is what we store.
func generateClaimCode() (code, hash string, err error) {
	raw := make([]byte, 10)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	var b strings.Builder
	for i, v := range raw {
		if i == 4 || i == 8 {
			b.WriteByte('-')
		}
		b.WriteByte(claimCodeAlphabet[int(v)%len(claimCodeAlphabet)])
	}
	code = b.String()
	hash = hashClaimCode(code)
	return code, hash, nil
}

// hashClaimCode normalizes a claim code and hashes it. Normalizing here means a
// player can type it lowercase, or leave the dashes out, and it still works.
func hashClaimCode(code string) string {
	norm := strings.ToUpper(strings.TrimSpace(code))
	norm = strings.ReplaceAll(norm, "-", "")
	norm = strings.ReplaceAll(norm, " ", "")
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}

// ---- request context ----

// devAPIContextKey is the private key type for the authenticated developer
// context. Private so nothing outside this package can forge one.
type devAPIContextKey struct{}

// DevAPIContext is what the middleware proved about a request before any
// handler ran.
type DevAPIContext struct {
	Key     models.DeveloperAPIKey
	Account models.DeveloperAccount
	Limits  models.DeveloperLimits
	// Usage is the key's counters after this request was recorded, so a handler
	// can enforce a provisioning cap without a second read.
	Usage models.DeveloperAPIUsage
	// Entitlement is the tier in force and why — the developer's own
	// subscription, or a website Premium Plus plan that includes it.
	Entitlement models.DeveloperEntitlement
}

// ownerOf reads the LPC account a key belongs to.
//
// Returns nil on any failure, which resolves to the Free tier rather than an
// error: this decides throughput, not access, and a key that works at the free
// rate is a better outcome than a key that stops working because one read
// failed.
func (d DeveloperAPI) ownerOf(ctx context.Context, userID string) *models.User {
	if d.UDB == nil {
		return nil
	}
	_, oid, err := devSafeObjectID(userID)
	if err != nil {
		return nil
	}
	var owner models.User
	if err := d.UDB.FindOne(ctx, bson.M{"_id": devEq(oid)}).Decode(&owner); err != nil {
		return nil
	}
	return &owner
}

// DevContextFrom returns the authenticated developer context, if the request
// came through RequireDeveloperAPIKey.
func DevContextFrom(ctx context.Context) (*DevAPIContext, bool) {
	v, ok := ctx.Value(devAPIContextKey{}).(*DevAPIContext)
	return v, ok
}

func withDevContext(ctx context.Context, dc *DevAPIContext) context.Context {
	return context.WithValue(ctx, devAPIContextKey{}, dc)
}

// ---- error responses ----

// devError writes the developer surface's error shape.
//
// Its own shape, separate from config.ErrorStatus, for two reasons: the
// response has to name the scope or the limit a caller tripped so a mod author
// reading a log knows what to change, and it must never leak internals to a
// third party. Every message here is written for someone outside the company.
func devError(w http.ResponseWriter, status int, code, message string, extra map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]interface{}{"error": code, "message": message}
	for k, v := range extra {
		body[k] = v
	}
	_ = json.NewEncoder(w).Encode(body)
}

// ---- the middleware ----

// RequireDeveloperAPIKey authenticates a developer key and applies its rate
// limits. It is the only entry point to the developer surface.
//
// Order matters and is deliberate:
//
//  1. the feature flag, so a disabled surface is indistinguishable from one
//     that was never built;
//  2. the key, so an unauthenticated caller never reaches a counter it could
//     use to probe which keys exist;
//  3. the developer account, so suspending a developer stops every key they
//     hold at once;
//  4. the limits, so an authenticated but runaway caller is stopped before it
//     reaches a handler.
//
// The key document is read from the database on every request, with no cache.
// That is one indexed read, and it buys a property worth more than the read:
// revoking a key stops it on the very next request rather than whenever a cache
// entry happened to expire.
func (d DeveloperAPI) RequireDeveloperAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !DeveloperAPIEnabled() {
			http.NotFound(w, r)
			return
		}

		// Enabled but unconfigured is a deployment mistake, not a request
		// error. Refusing loudly beats hashing every key with an empty secret
		// and having to re-issue them all later.
		if !developerKeyPepperConfigured() {
			zap.S().Errorw("developer api is enabled but has no key pepper configured",
				"env", devAPIKeyPepperEnv)
			devError(w, http.StatusServiceUnavailable, "unavailable",
				"The developer API is not available right now.", nil)
			return
		}

		presented, ok := bearerDeveloperKey(r)
		if !ok {
			devError(w, http.StatusUnauthorized, "unauthorized",
				"Send your API key as: Authorization: Bearer "+models.DeveloperAPIKeyPrefix+"...", nil)
			return
		}

		ctx, cancel := api.WithQueryTimeout(r.Context())
		defer cancel()

		key, err := d.KeyDB.FindOne(ctx, bson.M{"keyHash": hashDeveloperKey(presented)})
		if err != nil || key == nil {
			// Same answer whether the key is unknown or revoked. Telling a
			// caller which one would confirm that a key exists.
			devError(w, http.StatusUnauthorized, "invalid_key", "That API key is not valid.", nil)
			return
		}
		if key.Status != models.DeveloperKeyActive {
			d.recordUsage(ctx, *key, routeNameOf(r), http.StatusUnauthorized, nil)
			devError(w, http.StatusUnauthorized, "invalid_key", "That API key is not valid.", nil)
			return
		}

		account, err := d.AccountDB.FindOne(ctx, bson.M{"_id": objectIDOrZero(key.DeveloperID)})
		if err != nil || account == nil {
			d.recordUsage(ctx, *key, routeNameOf(r), http.StatusUnauthorized, nil)
			devError(w, http.StatusUnauthorized, "invalid_key", "That API key is not valid.", nil)
			return
		}
		if account.Status != models.DeveloperAccountActive {
			d.recordUsage(ctx, *key, routeNameOf(r), http.StatusForbidden, nil)
			devError(w, http.StatusForbidden, "developer_suspended",
				"This developer account is suspended. Open a ticket in our Discord to appeal.", nil)
			return
		}

		// The tier is the owner's website plan. That costs one indexed lookup
		// per request, on top of the key read this middleware already does on
		// every request. Worth it: the alternative is denormalizing the tier
		// onto the developer account and keeping it fresh from a subscription
		// webhook, which would hand a lapsed subscriber raised limits until
		// something remembered to refresh it.
		entitlement := models.DeveloperPlanFor(d.ownerOf(ctx, key.UserID))
		limits := models.LimitsForPlan(entitlement.Plan)

		// The global valve first: when the whole surface is saturated, there is
		// no point charging an individual key's budget for a request we are
		// about to refuse anyway.
		if over, retry := d.overGlobalLimit(ctx); over {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			d.recordUsage(ctx, *key, routeNameOf(r), http.StatusTooManyRequests, nil)
			devError(w, http.StatusTooManyRequests, "rate_limited",
				"The developer API is busy. Try again shortly.", map[string]interface{}{"retryAfterSeconds": retry})
			return
		}

		minuteCount, err := d.RateDB.IncrementAndCount(ctx, models.RateDocID(key.ID.Hex(), time.Now()), rateDocExpiry())
		if err != nil {
			// Fail closed on a counter we could not read. A limiter that opens
			// up when the database is unhappy is a limiter that disappears
			// exactly when it is needed.
			zap.S().Errorw("developer api: minute counter failed", "error", err)
			devError(w, http.StatusServiceUnavailable, "unavailable",
				"Could not check your rate limit. Try again shortly.", nil)
			return
		}

		usage, err := d.recordRequest(ctx, *key)
		if err != nil {
			zap.S().Errorw("developer api: usage counter failed", "error", err)
			devError(w, http.StatusServiceUnavailable, "unavailable",
				"Could not check your rate limit. Try again shortly.", nil)
			return
		}

		setRateLimitHeaders(w, limits, minuteCount, usage.Requests)

		if minuteCount > limits.RequestsPerMinute {
			retry := secondsToNextMinute()
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			d.recordUsage(ctx, *key, routeNameOf(r), http.StatusTooManyRequests, nil)
			devError(w, http.StatusTooManyRequests, "rate_limited",
				fmt.Sprintf("Over your burst limit of %d requests per minute.", limits.RequestsPerMinute),
				map[string]interface{}{"limit": limits.RequestsPerMinute, "scope": "minute", "retryAfterSeconds": retry})
			return
		}
		if usage.Requests > limits.RequestsPerDay {
			retry := secondsToNextUTCDay()
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			d.recordUsage(ctx, *key, routeNameOf(r), http.StatusTooManyRequests, nil)
			devError(w, http.StatusTooManyRequests, "rate_limited",
				fmt.Sprintf("Over your daily limit of %d requests. It resets at midnight UTC.", limits.RequestsPerDay),
				map[string]interface{}{"limit": limits.RequestsPerDay, "scope": "day", "retryAfterSeconds": retry})
			return
		}

		// Last-used is best effort and deliberately not awaited: it is support
		// information, not a decision input, and it must never slow a request
		// down or fail one.
		d.touchKeyLastUsed(key.ID)

		dc := &DevAPIContext{Key: *key, Account: *account, Limits: limits, Usage: *usage}
		next.ServeHTTP(w, r.WithContext(withDevContext(r.Context(), dc)))
	})
}

// bearerDeveloperKey pulls a developer key out of the Authorization header.
//
// It requires the lpc_live_ prefix, which means a website or mobile bearer
// session token presented here is rejected before it is hashed or looked up.
// The two credential kinds cannot be confused for one another.
func bearerDeveloperKey(r *http.Request) (string, bool) {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if raw == "" {
		return "", false
	}
	const scheme = "bearer "
	if len(raw) <= len(scheme) || !strings.EqualFold(raw[:len(scheme)], scheme) {
		return "", false
	}
	token := strings.TrimSpace(raw[len(scheme):])
	if !strings.HasPrefix(token, models.DeveloperAPIKeyPrefix) {
		return "", false
	}
	return token, true
}

// ---- limits ----

// rateDocExpiry is when a minute counter may be swept. Two minutes, so the
// document outlives the window it measures but nothing accumulates.
func rateDocExpiry() primitive.DateTime {
	return primitive.NewDateTimeFromTime(time.Now().Add(2 * time.Minute))
}

func secondsToNextMinute() int {
	now := time.Now().UTC()
	return 60 - now.Second()
}

func secondsToNextUTCDay() int {
	now := time.Now().UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
	return int(next.Sub(now).Seconds())
}

// setRateLimitHeaders tells the caller where they stand on both windows.
//
// The tighter of the two is reported as the headline, because that is the one a
// client will hit first and therefore the one it should pace against.
func setRateLimitHeaders(w http.ResponseWriter, limits models.DeveloperLimits, minuteCount, dayCount int) {
	minuteRemaining := limits.RequestsPerMinute - minuteCount
	dayRemaining := limits.RequestsPerDay - dayCount
	if minuteRemaining < 0 {
		minuteRemaining = 0
	}
	if dayRemaining < 0 {
		dayRemaining = 0
	}

	scope, limit, remaining, reset := "minute", limits.RequestsPerMinute, minuteRemaining, secondsToNextMinute()
	if dayRemaining < minuteRemaining {
		scope, limit, remaining, reset = "day", limits.RequestsPerDay, dayRemaining, secondsToNextUTCDay()
	}

	w.Header().Set("X-RateLimit-Scope", scope)
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().UTC().Add(time.Duration(reset)*time.Second).Unix(), 10))
	w.Header().Set("X-RateLimit-Limit-Minute", strconv.Itoa(limits.RequestsPerMinute))
	w.Header().Set("X-RateLimit-Limit-Day", strconv.Itoa(limits.RequestsPerDay))
	w.Header().Set("X-RateLimit-Remaining-Day", strconv.Itoa(dayRemaining))
}

// overGlobalLimit applies the whole-surface valve.
//
// Best effort by design, and the opposite choice from the per-key limiter: this
// is a safety net over the top of limits that already hold, so a counter error
// here should not turn into an outage for well-behaved developers.
func (d DeveloperAPI) overGlobalLimit(ctx context.Context) (bool, int) {
	cap := 0
	if v := os.Getenv(devAPIGlobalRPMEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cap = n
		}
	}
	if cap == 0 {
		return false, 0
	}
	n, err := d.RateDB.IncrementAndCount(ctx, models.RateDocID(devAPIGlobalRateKey, time.Now()), rateDocExpiry())
	if err != nil {
		return false, 0
	}
	if n > cap {
		return true, secondsToNextMinute()
	}
	return false, 0
}

// ---- usage counters ----

// recordRequest counts one request against the key's day and returns the
// counters after the increment.
//
// One round trip that both records and reports. Reading the count and then
// writing it would let two dynos see the same number and both conclude the
// request was inside the limit, which is the exact race this store exists to
// avoid.
func (d DeveloperAPI) recordRequest(ctx context.Context, key models.DeveloperAPIKey) (*models.DeveloperAPIUsage, error) {
	now := time.Now().UTC()
	id := models.UsageDocID(key.ID.Hex(), now)
	return d.UsageDB.FindOneAndIncrement(ctx, id,
		bson.M{"requests": 1},
		bson.M{
			"keyId":         key.ID.Hex(),
			"developerId":   key.DeveloperID,
			"day":           now.Format("2006-01-02"),
			"lastRequestAt": primitive.NewDateTimeFromTime(now),
			// 400 days, so the dashboard can show a year and a bit without the
			// collection growing without end.
			"expiresAt": primitive.NewDateTimeFromTime(now.Add(400 * 24 * time.Hour)),
		})
}

// recordUsage records the outcome of a request: which route, and whether it was
// allowed or refused.
//
// Outcomes are what the dashboard's "recent errors" panel is built from, so a
// developer can see their own 401s and 429s without asking us. Best effort:
// losing a counter must never change what the caller is told.
func (d DeveloperAPI) recordUsage(ctx context.Context, key models.DeveloperAPIKey, route string, status int, extraInc bson.M) {
	inc := bson.M{}
	for k, v := range extraInc {
		inc[k] = v
	}
	switch status {
	case http.StatusTooManyRequests:
		inc["rejected429"] = 1
	case http.StatusUnauthorized:
		inc["rejected401"] = 1
	case http.StatusForbidden:
		inc["rejected403"] = 1
	default:
		inc["allowed"] = 1
	}
	if route != "" {
		inc["byRoute."+route] = 1
	}
	if len(inc) == 0 {
		return
	}

	now := time.Now().UTC()
	id := models.UsageDocID(key.ID.Hex(), now)
	set := bson.M{
		"keyId":       key.ID.Hex(),
		"developerId": key.DeveloperID,
		"day":         now.Format("2006-01-02"),
		"expiresAt":   primitive.NewDateTimeFromTime(now.Add(400 * 24 * time.Hour)),
	}
	if _, err := d.UsageDB.FindOneAndIncrement(ctx, id, inc, set); err != nil {
		zap.S().Warnw("developer api: could not record usage", "keyId", key.ID.Hex(), "error", err)
	}
}

// RecordOutcome lets a handler record its own result once it knows it.
func (d DeveloperAPI) RecordOutcome(r *http.Request, status int) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d.recordUsage(ctx, dc.Key, routeNameOf(r), status, nil)
}

// routeNameOf is the bounded label a request is counted under.
//
// The mux route template, not the raw path: a path carries community and player
// ids, and counting by those would turn one map on one document into an
// unbounded set of keys.
func routeNameOf(r *http.Request) string {
	if route := mux.CurrentRoute(r); route != nil {
		if tpl, err := route.GetPathTemplate(); err == nil {
			return r.Method + " " + strings.TrimPrefix(tpl, "/api/v1/dev")
		}
	}
	return r.Method
}

// touchKeyLastUsed stamps a key's last use without making the request wait.
func (d DeveloperAPI) touchKeyLastUsed(keyID primitive.ObjectID) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		now := primitive.NewDateTimeFromTime(time.Now())
		if _, err := d.KeyDB.UpdateOne(ctx, bson.M{"_id": keyID}, bson.M{"$set": bson.M{"lastUsedAt": now}}); err != nil {
			zap.S().Debugw("developer api: could not stamp lastUsedAt", "keyId", keyID.Hex(), "error", err)
		}
	}()
}

// ---- small shared helpers ----

// objectIDOrZero parses a hex id, returning the zero ObjectID on anything
// unparseable so a malformed id matches nothing instead of erroring.
func objectIDOrZero(hexID string) primitive.ObjectID {
	oid, err := primitive.ObjectIDFromHex(hexID)
	if err != nil {
		return primitive.NilObjectID
	}
	return oid
}
