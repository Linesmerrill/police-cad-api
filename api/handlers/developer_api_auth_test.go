package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Tests for the authenticated edge of the Developer API. The properties under
// test here are the security ones: a key must not be guessable from our
// responses, must stop the moment it is revoked, must not reach a community
// nobody granted it, and must not be able to outrun its limits.

// ---- fakes ----
//
// Hand-rolled rather than mockery, matching api/handlers/economy_access_test.go:
// these interfaces have no generated mocks, and a fake that embeds the
// interface keeps compiling when the interface grows.

type fakeDevKeyDB struct {
	databases.DeveloperAPIKeyDatabase
	byHash map[string]models.DeveloperAPIKey
	byID   map[string]models.DeveloperAPIKey

	// lastUsedAt is stamped from a goroutine, so writes here are locked.
	mu      sync.Mutex
	updates []bson.M
}

func (f *fakeDevKeyDB) FindOne(_ context.Context, filter interface{}) (*models.DeveloperAPIKey, error) {
	m, _ := filter.(bson.M)
	if h, ok := m["keyHash"].(string); ok {
		if k, found := f.byHash[h]; found {
			return &k, nil
		}
		return nil, mongo.ErrNoDocuments
	}
	if oid, ok := m["_id"].(primitive.ObjectID); ok {
		if k, found := f.byID[oid.Hex()]; found {
			return &k, nil
		}
	}
	return nil, mongo.ErrNoDocuments
}

func (f *fakeDevKeyDB) UpdateOne(_ context.Context, _, update interface{}, _ ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := update.(bson.M); ok {
		f.updates = append(f.updates, m)
	}
	return &mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil
}

type fakeDevAccountDB struct {
	databases.DeveloperAccountDatabase
	byID map[string]models.DeveloperAccount
}

func (f *fakeDevAccountDB) FindOne(_ context.Context, filter interface{}) (*models.DeveloperAccount, error) {
	m, _ := filter.(bson.M)
	if oid, ok := m["_id"].(primitive.ObjectID); ok {
		if a, found := f.byID[oid.Hex()]; found {
			return &a, nil
		}
	}
	return nil, mongo.ErrNoDocuments
}

type fakeDevRateDB struct {
	databases.DeveloperRateDatabase
	counts map[string]int
	err    error
}

func (f *fakeDevRateDB) IncrementAndCount(_ context.Context, id string, _ interface{}) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	if f.counts == nil {
		f.counts = map[string]int{}
	}
	f.counts[id]++
	return f.counts[id], nil
}

type fakeDevUsageDB struct {
	databases.DeveloperUsageDatabase
	docs map[string]*models.DeveloperAPIUsage
	err  error
	// incs records every increment, so a test can assert what was counted.
	incs []bson.M
}

func (f *fakeDevUsageDB) FindOneAndIncrement(_ context.Context, id string, inc bson.M, _ bson.M) (*models.DeveloperAPIUsage, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.docs == nil {
		f.docs = map[string]*models.DeveloperAPIUsage{}
	}
	f.incs = append(f.incs, inc)
	doc, ok := f.docs[id]
	if !ok {
		doc = &models.DeveloperAPIUsage{ID: id}
		f.docs[id] = doc
	}
	for field, v := range inc {
		n, _ := v.(int)
		switch field {
		case "requests":
			doc.Requests += n
		case "allowed":
			doc.Allowed += n
		case "rejected429":
			doc.Rejected429 += n
		case "rejected401":
			doc.Rejected401 += n
		case "rejected403":
			doc.Rejected403 += n
		}
	}
	copied := *doc
	return &copied, nil
}

type fakeDevGrantDB struct {
	databases.DeveloperGrantDatabase
	grants []models.DeveloperCommunityGrant
}

func (f *fakeDevGrantDB) FindOne(_ context.Context, filter interface{}) (*models.DeveloperCommunityGrant, error) {
	m, _ := filter.(bson.M)
	for _, g := range f.grants {
		if g.KeyID == m["keyId"] && g.CommunityID == m["communityId"] {
			if want, ok := m["status"].(string); ok && g.Status != want {
				continue
			}
			found := g
			return &found, nil
		}
	}
	return nil, mongo.ErrNoDocuments
}

// ---- fixtures ----

type devFixture struct {
	api         DeveloperAPI
	keyDB       *fakeDevKeyDB
	rateDB      *fakeDevRateDB
	usageDB     *fakeDevUsageDB
	grantDB     *fakeDevGrantDB
	plaintext   string
	keyID       primitive.ObjectID
	developerID primitive.ObjectID
}

// newDevFixture builds a live, active key on an active Free developer account.
func newDevFixture(t *testing.T, scopes ...string) *devFixture {
	t.Helper()
	t.Setenv(devAPIEnabledEnv, "true")
	t.Setenv(devAPIKeyPepperEnv, "test-pepper-not-a-real-secret")

	plaintext, hash, prefix, lastFour, err := generateDeveloperKey()
	if err != nil {
		t.Fatalf("generateDeveloperKey: %v", err)
	}

	keyID := primitive.NewObjectID()
	developerID := primitive.NewObjectID()
	now := primitive.NewDateTimeFromTime(time.Now())

	key := models.DeveloperAPIKey{
		ID: keyID, DeveloperID: developerID.Hex(), UserID: primitive.NewObjectID().Hex(),
		Name: "test key", KeyHash: hash, DisplayPrefix: prefix, LastFour: lastFour,
		Scopes: scopes, Status: models.DeveloperKeyActive, CreatedAt: now, UpdatedAt: now,
	}
	account := models.DeveloperAccount{
		ID: developerID, UserID: key.UserID, Plan: models.DeveloperPlanFree,
		Status: models.DeveloperAccountActive, CreatedAt: now, UpdatedAt: now,
	}

	f := &devFixture{
		keyDB:     &fakeDevKeyDB{byHash: map[string]models.DeveloperAPIKey{hash: key}, byID: map[string]models.DeveloperAPIKey{keyID.Hex(): key}},
		rateDB:    &fakeDevRateDB{},
		usageDB:   &fakeDevUsageDB{},
		grantDB:   &fakeDevGrantDB{},
		plaintext: plaintext, keyID: keyID, developerID: developerID,
	}
	f.api = DeveloperAPI{
		AccountDB: &fakeDevAccountDB{byID: map[string]models.DeveloperAccount{developerID.Hex(): account}},
		KeyDB:     f.keyDB,
		GrantDB:   f.grantDB,
		UsageDB:   f.usageDB,
		RateDB:    f.rateDB,
	}
	return f
}

// serve runs a request through the authenticating middleware.
func (f *devFixture) serve(t *testing.T, authHeader string, next http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	if next == nil {
		next = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/whoami", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rr := httptest.NewRecorder()
	f.api.RequireDeveloperAPIKey(next).ServeHTTP(rr, req)
	return rr
}

// ---- the flag ----

// With the flag off the surface is a 404, indistinguishable from one that was
// never built. A third party should not be able to tell we are working on it.
func TestDevAPI_DisabledIsNotFound(t *testing.T) {
	f := newDevFixture(t)
	t.Setenv(devAPIEnabledEnv, "")

	rr := f.serve(t, "Bearer "+f.plaintext, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when disabled, got %d", rr.Code)
	}
}

// Enabled but unconfigured is a deployment mistake. Refusing loudly beats
// hashing every key with an empty secret and having to re-issue them all later.
func TestDevAPI_RefusesWithoutAKeyPepper(t *testing.T) {
	f := newDevFixture(t)
	t.Setenv(devAPIKeyPepperEnv, "")

	rr := f.serve(t, "Bearer "+f.plaintext, nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with no pepper configured, got %d", rr.Code)
	}
}

// The pepper is what stops a database dump from confirming whether a key the
// attacker already holds is one of ours.
func TestHashDeveloperKey_DependsOnThePepper(t *testing.T) {
	const key = "lpc_live_0123456789abcdef"

	t.Setenv(devAPIKeyPepperEnv, "pepper-one")
	first := hashDeveloperKey(key)

	t.Setenv(devAPIKeyPepperEnv, "pepper-two")
	second := hashDeveloperKey(key)

	if first == second {
		t.Fatal("the same key must hash differently under a different pepper")
	}

	t.Setenv(devAPIKeyPepperEnv, "pepper-one")
	if hashDeveloperKey(key) != first {
		t.Error("hashing is deterministic under one pepper, because lookup is by equality")
	}

	// And it must not be a bare digest of the key, or the pepper is doing
	// nothing.
	bare := sha256.Sum256([]byte(key))
	if first == hex.EncodeToString(bare[:]) {
		t.Error("the stored hash must not be a plain SHA-256 of the key")
	}
}

// ---- credentials ----

func TestDevAPI_RejectsMissingAndMalformedCredentials(t *testing.T) {
	f := newDevFixture(t)

	for _, tc := range []struct{ name, header string }{
		{"no header", ""},
		{"not bearer", "Basic " + f.plaintext},
		{"empty bearer", "Bearer "},
		{"unknown key", "Bearer " + models.DeveloperAPIKeyPrefix + "deadbeef"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rr := f.serve(t, tc.header, nil); rr.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", rr.Code)
			}
		})
	}
}

// A website or mobile bearer session token must not authenticate here. The
// prefix check rejects it before it is hashed or looked up, so the two kinds of
// credential cannot be confused for one another.
func TestDevAPI_RejectsASessionTokenAsAKey(t *testing.T) {
	f := newDevFixture(t)

	// A go-guardian session token is a bare UUID, with no lpc_live_ prefix.
	rr := f.serve(t, "Bearer 3f8c1a62-9b7e-4f21-8a44-5c2d7e9b0a13", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("a session token must not authenticate the developer surface, got %d", rr.Code)
	}
}

// An unknown key and a revoked key get the same answer, so a caller cannot use
// the difference to work out which keys exist.
func TestDevAPI_RevokedKeyIsIndistinguishableFromUnknown(t *testing.T) {
	f := newDevFixture(t)

	unknown := f.serve(t, "Bearer "+models.DeveloperAPIKeyPrefix+"0123456789abcdef", nil)

	for hash, key := range f.keyDB.byHash {
		key.Status = models.DeveloperKeyRevoked
		f.keyDB.byHash[hash] = key
	}
	revoked := f.serve(t, "Bearer "+f.plaintext, nil)

	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("a revoked key must be refused, got %d", revoked.Code)
	}
	if revoked.Body.String() != unknown.Body.String() {
		t.Errorf("revoked and unknown keys must answer identically:\n revoked: %s\n unknown: %s",
			revoked.Body.String(), unknown.Body.String())
	}
}

// Revocation takes effect on the very next request. There is no cached decision
// to outlive it, which is the reason the key is read from the database every
// time.
func TestDevAPI_RevocationTakesEffectImmediately(t *testing.T) {
	f := newDevFixture(t)

	if rr := f.serve(t, "Bearer "+f.plaintext, nil); rr.Code != http.StatusOK {
		t.Fatalf("expected the key to work before revocation, got %d", rr.Code)
	}

	for hash, key := range f.keyDB.byHash {
		key.Status = models.DeveloperKeyRevoked
		f.keyDB.byHash[hash] = key
	}

	if rr := f.serve(t, "Bearer "+f.plaintext, nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on the next request after revocation, got %d", rr.Code)
	}
}

// Suspending a developer account stops every key it holds at once.
func TestDevAPI_SuspendedDeveloperIsRefused(t *testing.T) {
	f := newDevFixture(t)
	accounts := f.api.AccountDB.(*fakeDevAccountDB)
	acct := accounts.byID[f.developerID.Hex()]
	acct.Status = models.DeveloperAccountSuspended
	accounts.byID[f.developerID.Hex()] = acct

	rr := f.serve(t, "Bearer "+f.plaintext, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a suspended developer, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "developer_suspended") {
		t.Errorf("the response should say why: %s", rr.Body.String())
	}
}

// ---- key material ----

// The stored hash must not be the key, and the display fields must not be
// enough to reconstruct it.
func TestGenerateDeveloperKey_StoresNoUsableSecret(t *testing.T) {
	plaintext, hash, prefix, lastFour, err := generateDeveloperKey()
	if err != nil {
		t.Fatalf("generateDeveloperKey: %v", err)
	}

	if !strings.HasPrefix(plaintext, models.DeveloperAPIKeyPrefix) {
		t.Errorf("a key should be recognisable by its prefix, got %q", plaintext)
	}
	if strings.Contains(hash, plaintext) || hash == plaintext {
		t.Error("the stored hash must not contain the key")
	}
	if len(hash) != 64 {
		t.Errorf("expected a SHA-256 hex digest, got %d chars", len(hash))
	}
	if strings.Contains(prefix, strings.TrimPrefix(plaintext, models.DeveloperAPIKeyPrefix)) {
		t.Error("the display prefix must not be the whole secret")
	}
	if len(lastFour) != 4 {
		t.Errorf("expected 4 display characters, got %d", len(lastFour))
	}
	if hashDeveloperKey(plaintext) != hash {
		t.Error("hashing the key again should produce the stored hash")
	}

	// Two keys in a row must differ. A fixed or sequential key would be
	// catastrophic, and it is cheap to assert.
	other, _, _, _, _ := generateDeveloperKey()
	if other == plaintext {
		t.Error("two generated keys must not be equal")
	}
}

// A claim code is read off a game screen and typed into a browser, so it has to
// survive lowercase, missing dashes and stray spaces, and its alphabet must
// exclude the characters people transcribe wrong.
func TestGenerateClaimCode_IsForgivingAndUnambiguous(t *testing.T) {
	code, hash, err := generateClaimCode()
	if err != nil {
		t.Fatalf("generateClaimCode: %v", err)
	}

	for _, bad := range []string{"I", "L", "O", "U"} {
		if strings.Contains(strings.ToUpper(code), bad) {
			t.Errorf("claim codes must not contain %q, which is misread: %s", bad, code)
		}
	}

	for _, variant := range []string{
		code,
		strings.ToLower(code),
		strings.ReplaceAll(code, "-", ""),
		" " + strings.ToLower(strings.ReplaceAll(code, "-", "")) + " ",
	} {
		if hashClaimCode(variant) != hash {
			t.Errorf("a player typing %q should be accepted", variant)
		}
	}

	if hashClaimCode("NOTTHECODE") == hash {
		t.Error("a wrong code must not hash to the right value")
	}
}

// ---- limits ----

// The burst limit stops the request that exceeds it, not the one after.
func TestDevAPI_BurstLimitRefusesWithRetryAfter(t *testing.T) {
	f := newDevFixture(t)
	limit := models.LimitsForPlan(models.DeveloperPlanFree).RequestsPerMinute

	for i := 0; i < limit; i++ {
		if rr := f.serve(t, "Bearer "+f.plaintext, nil); rr.Code != http.StatusOK {
			t.Fatalf("request %d of %d should be allowed, got %d", i+1, limit, rr.Code)
		}
	}

	rr := f.serve(t, "Bearer "+f.plaintext, nil)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("request %d should be refused, got %d", limit+1, rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("a 429 must carry Retry-After")
	}
	if got := rr.Header().Get("X-RateLimit-Scope"); got != "minute" {
		t.Errorf("expected the minute scope to be reported, got %q", got)
	}
	if !strings.Contains(rr.Body.String(), "rate_limited") {
		t.Errorf("the body should name the problem: %s", rr.Body.String())
	}
}

// Headers tell a client where it stands before it hits a wall, which is what
// lets a well-written mod pace itself.
func TestDevAPI_RateLimitHeadersCountDown(t *testing.T) {
	f := newDevFixture(t)
	limits := models.LimitsForPlan(models.DeveloperPlanFree)

	first := f.serve(t, "Bearer "+f.plaintext, nil)
	if got := first.Header().Get("X-RateLimit-Limit"); got != strconv.Itoa(limits.RequestsPerMinute) {
		t.Errorf("expected the minute limit, got %q", got)
	}
	if got := first.Header().Get("X-RateLimit-Remaining"); got != strconv.Itoa(limits.RequestsPerMinute-1) {
		t.Errorf("after one request expected %d remaining, got %q", limits.RequestsPerMinute-1, got)
	}
	if first.Header().Get("X-RateLimit-Reset") == "" {
		t.Error("a reset time must be reported")
	}

	second := f.serve(t, "Bearer "+f.plaintext, nil)
	if got := second.Header().Get("X-RateLimit-Remaining"); got != strconv.Itoa(limits.RequestsPerMinute-2) {
		t.Errorf("after two requests expected %d remaining, got %q", limits.RequestsPerMinute-2, got)
	}
}

// The daily cap is enforced from the same counter the dashboard reads, so the
// number a developer sees is the number that stopped them.
func TestDevAPI_DailyLimitRefuses(t *testing.T) {
	f := newDevFixture(t)
	limits := models.LimitsForPlan(models.DeveloperPlanFree)

	// Pre-load today's counter to the cap.
	id := models.UsageDocID(f.keyID.Hex(), time.Now().UTC())
	f.usageDB.docs = map[string]*models.DeveloperAPIUsage{
		id: {ID: id, Requests: limits.RequestsPerDay},
	}

	rr := f.serve(t, "Bearer "+f.plaintext, nil)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 at the daily cap, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "\"scope\":\"day\"") {
		t.Errorf("the body should say the daily limit was the one hit: %s", rr.Body.String())
	}
}

// A limiter that opens up when the database is unhappy is a limiter that
// disappears exactly when it is needed, so a counter failure refuses the
// request rather than waving it through.
func TestDevAPI_CounterFailureFailsClosed(t *testing.T) {
	f := newDevFixture(t)
	f.rateDB.err = mongo.ErrClientDisconnected

	rr := f.serve(t, "Bearer "+f.plaintext, nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when the counter cannot be read, got %d", rr.Code)
	}
}

// An unauthenticated caller must not reach a counter, or the counters become a
// way to probe which keys exist.
func TestDevAPI_UnknownKeyTouchesNoCounters(t *testing.T) {
	f := newDevFixture(t)

	f.serve(t, "Bearer "+models.DeveloperAPIKeyPrefix+"0123456789abcdef", nil)

	if len(f.rateDB.counts) != 0 {
		t.Errorf("an unknown key should not increment a rate counter, got %v", f.rateDB.counts)
	}
	if len(f.usageDB.incs) != 0 {
		t.Errorf("an unknown key should not increment a usage counter, got %v", f.usageDB.incs)
	}
}

// ---- scopes ----

func TestDevAPI_RequireScope(t *testing.T) {
	f := newDevFixture(t, models.ScopeCallsRead)

	t.Run("a held scope passes", func(t *testing.T) {
		called := false
		handler := f.api.RequireScope(models.ScopeCallsRead, func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})
		rr := f.serve(t, "Bearer "+f.plaintext, handler.ServeHTTP)
		if rr.Code != http.StatusOK || !called {
			t.Fatalf("expected the handler to run, got %d called=%v", rr.Code, called)
		}
	})

	t.Run("a missing scope is refused and names itself", func(t *testing.T) {
		called := false
		handler := f.api.RequireScope(models.ScopeCallsWrite, func(w http.ResponseWriter, _ *http.Request) {
			called = true
		})
		rr := f.serve(t, "Bearer "+f.plaintext, handler.ServeHTTP)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}
		if called {
			t.Error("the handler must not run without the scope")
		}
		if !strings.Contains(rr.Body.String(), models.ScopeCallsWrite) {
			t.Errorf("the error should name the scope needed: %s", rr.Body.String())
		}
	})
}

// A read scope does not imply its write twin. Worth asserting explicitly: it is
// the kind of thing a future refactor could quietly introduce.
func TestDevAPI_ReadScopeDoesNotImplyWrite(t *testing.T) {
	for _, pair := range [][2]string{
		{models.ScopeCallsRead, models.ScopeCallsWrite},
		{models.ScopeCiviliansRead, models.ScopeCiviliansWrite},
		{models.ScopeUnitsRead, models.ScopeUnitsWrite},
		{models.ScopeMembersRead, models.ScopeMembersWrite},
	} {
		key := models.DeveloperAPIKey{Scopes: []string{pair[0]}}
		if key.HasScope(pair[1]) {
			t.Errorf("%s must not grant %s", pair[0], pair[1])
		}
	}
}

// ---- community grants ----

func TestDevAPI_ResolveCommunityRequiresAGrant(t *testing.T) {
	communityID := primitive.NewObjectID()

	t.Run("no grant is refused without revealing whether the community exists", func(t *testing.T) {
		f := newDevFixture(t, models.ScopeCommunityRead)
		rr := f.serveCommunity(t, communityID.Hex(), nil)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403 without a grant, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "community_not_granted") {
			t.Errorf("unexpected body: %s", rr.Body.String())
		}
	})

	t.Run("a pending grant is not yet access", func(t *testing.T) {
		f := newDevFixture(t, models.ScopeCommunityRead)
		f.grantDB.grants = []models.DeveloperCommunityGrant{{
			KeyID: f.keyID.Hex(), CommunityID: communityID.Hex(), Status: models.DeveloperKeyPending,
		}}
		if rr := f.serveCommunity(t, communityID.Hex(), nil); rr.Code != http.StatusForbidden {
			t.Fatalf("a key awaiting approval must not get in, got %d", rr.Code)
		}
	})

	t.Run("a revoked grant stops access immediately", func(t *testing.T) {
		f := newDevFixture(t, models.ScopeCommunityRead)
		f.grantDB.grants = []models.DeveloperCommunityGrant{{
			KeyID: f.keyID.Hex(), CommunityID: communityID.Hex(), Status: models.DeveloperKeyRevoked,
		}}
		if rr := f.serveCommunity(t, communityID.Hex(), nil); rr.Code != http.StatusForbidden {
			t.Fatalf("a revoked grant must not get in, got %d", rr.Code)
		}
	})

	t.Run("a malformed community id is a 400", func(t *testing.T) {
		f := newDevFixture(t, models.ScopeCommunityRead)
		if rr := f.serveCommunity(t, "not-an-id", nil); rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rr.Code)
		}
	})
}

// A key granted community A cannot reach community B. This is the single most
// important property of the whole surface.
func TestDevAPI_GrantForOneCommunityDoesNotReachAnother(t *testing.T) {
	granted := primitive.NewObjectID()
	other := primitive.NewObjectID()

	f := newDevFixture(t, models.ScopeCommunityRead)
	f.grantDB.grants = []models.DeveloperCommunityGrant{{
		KeyID: f.keyID.Hex(), CommunityID: granted.Hex(), Status: models.DeveloperKeyActive,
	}}
	f.api.CDB = &fakeDevCommunityDB{communities: map[string]models.Community{
		granted.Hex(): {ID: granted},
		other.Hex():   {ID: other},
	}}

	if rr := f.serveCommunity(t, granted.Hex(), nil); rr.Code != http.StatusOK {
		t.Fatalf("the granted community should be reachable, got %d", rr.Code)
	}
	if rr := f.serveCommunity(t, other.Hex(), nil); rr.Code != http.StatusForbidden {
		t.Fatalf("another community must not be reachable, got %d", rr.Code)
	}
}

// A community in its 30-day pending-deletion window is read-only. Letting a mod
// keep writing would lose the writes, and worse, would look to a player like
// their records had been saved.
func TestDevAPI_PendingDeletionCommunityIsRefused(t *testing.T) {
	communityID := primitive.NewObjectID()
	pending := primitive.NewDateTimeFromTime(time.Now())

	f := newDevFixture(t, models.ScopeCommunityRead)
	f.grantDB.grants = []models.DeveloperCommunityGrant{{
		KeyID: f.keyID.Hex(), CommunityID: communityID.Hex(), Status: models.DeveloperKeyActive,
	}}
	f.api.CDB = &fakeDevCommunityDB{communities: map[string]models.Community{
		communityID.Hex(): {ID: communityID, Details: models.CommunityDetails{PendingDeletionAt: &pending}},
	}}

	rr := f.serveCommunity(t, communityID.Hex(), nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a community being deleted, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "community_pending_deletion") {
		t.Errorf("the reason should be specific: %s", rr.Body.String())
	}
}

// ---- helpers used by the community tests ----

type fakeDevCommunityDB struct {
	databases.CommunityDatabase
	communities map[string]models.Community
}

func (f *fakeDevCommunityDB) FindOneIncludingPending(_ context.Context, filter interface{}) (*models.Community, error) {
	m, _ := filter.(bson.M)
	if oid, ok := m["_id"].(primitive.ObjectID); ok {
		if c, found := f.communities[oid.Hex()]; found {
			return &c, nil
		}
	}
	return nil, mongo.ErrNoDocuments
}

// serveCommunity runs a request at a community-scoped path through the
// middleware and then through ResolveCommunity.
func (f *devFixture) serveCommunity(t *testing.T, communityID string, next http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	if f.api.CDB == nil {
		f.api.CDB = &fakeDevCommunityDB{communities: map[string]models.Community{}}
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		cc, ok := f.api.ResolveCommunity(w, r)
		if !ok {
			return
		}
		if next != nil {
			next(w, r)
			return
		}
		_ = cc
		w.WriteHeader(http.StatusOK)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/communities/"+communityID, nil)
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	req = mux.SetURLVars(req, map[string]string{"communityId": communityID})

	rr := httptest.NewRecorder()
	f.api.RequireDeveloperAPIKey(http.HandlerFunc(handler)).ServeHTTP(rr, req)
	return rr
}
