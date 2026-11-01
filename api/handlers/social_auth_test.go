package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// ---- fakes ----

// socialUserDB answers the two lookups this surface makes: by id, and by
// address ignoring case. The second one matters: an exact match would miss an
// account registered with different capitalisation, and missing it is what
// would create a duplicate account instead of asking to link.
type socialUserDB struct {
	databases.UserDatabase
	mu      sync.Mutex
	users   map[string]models.User
	inserts int
}

func (f *socialUserDB) FindOne(_ context.Context, filter interface{}, _ ...*options.FindOneOptions) databases.SingleResultHelper {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)

	if oid, ok := devFilterObjectID(m["_id"]); ok {
		if u, found := f.users[oid.Hex()]; found {
			return devSingleResult{v: u}
		}
		return devSingleResult{err: mongo.ErrNoDocuments}
	}

	if raw, ok := m["user.email"]; ok {
		want := ""
		if cond, ok := raw.(bson.M); ok {
			if pattern, ok := cond["$regex"].(string); ok {
				want = strings.TrimSuffix(strings.TrimPrefix(pattern, "^"), "$")
				want = strings.ReplaceAll(want, `\`, "")
			}
		}
		for _, u := range f.users {
			if want != "" && strings.EqualFold(u.Details.Email, want) {
				return devSingleResult{v: u}
			}
		}
	}
	return devSingleResult{err: mongo.ErrNoDocuments}
}

func (f *socialUserDB) InsertOne(_ context.Context, details models.UserDetails) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	oid := primitive.NewObjectID()
	if f.users == nil {
		f.users = map[string]models.User{}
	}
	f.users[oid.Hex()] = models.User{ID: oid.Hex(), Details: details}
	f.inserts++
	return fakeInsertResult{id: oid}, nil
}

func (f *socialUserDB) add(u models.User) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.users == nil {
		f.users = map[string]models.User{}
	}
	f.users[u.ID] = u
}

// socialAuthIdentityDB enforces the unique (provider, subject) index in memory,
// because that uniqueness is the property the design rests on.
type socialAuthIdentityDB struct {
	mu         sync.Mutex
	rows       []models.AuthIdentity
	deletes    int
	insertFail error
}

// devStr drops the "was it present" flag from devFilterString, because every
// use here treats absent and empty the same way.
func devStr(raw interface{}) string {
	v, _ := devFilterString(raw)
	return v
}

func (f *socialAuthIdentityDB) key(provider, subject string) string { return provider + "|" + subject }

func (f *socialAuthIdentityDB) FindOne(_ context.Context, filter interface{}) (*models.AuthIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	provider := devStr(m["provider"])
	subject := devStr(m["subject"])
	userID := devStr(m["userId"])
	for i := range f.rows {
		row := f.rows[i]
		if provider != "" && subject != "" && row.Provider == provider && row.Subject == subject {
			return &row, nil
		}
		if userID != "" && provider != "" && row.UserID == userID && row.Provider == provider {
			return &row, nil
		}
	}
	return nil, mongo.ErrNoDocuments
}

func (f *socialAuthIdentityDB) Find(_ context.Context, filter interface{}, _ ...*options.FindOptions) (*databases.MongoCursor, error) {
	f.mu.Lock()
	m, _ := filter.(bson.M)
	userID := devStr(m["userId"])
	var docs []interface{}
	for _, row := range f.rows {
		if userID == "" || row.UserID == userID {
			docs = append(docs, row)
		}
	}
	f.mu.Unlock()
	cursor, err := databases.NewMongoCursorFromDocuments(docs)
	if err != nil {
		return nil, err
	}
	return &cursor, nil
}

func (f *socialAuthIdentityDB) InsertOne(_ context.Context, identity models.AuthIdentity) (databases.InsertOneResultHelper, error) {
	if f.insertFail != nil {
		return nil, f.insertFail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.rows {
		if f.key(row.Provider, row.Subject) == f.key(identity.Provider, identity.Subject) {
			return nil, mongo.WriteException{WriteErrors: []mongo.WriteError{{Code: 11000}}}
		}
	}
	f.rows = append(f.rows, identity)
	return fakeInsertResult{id: identity.ID}, nil
}

func (f *socialAuthIdentityDB) UpdateOne(_ context.Context, filter, update interface{}, _ ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	provider, subject := devStr(m["provider"]), devStr(m["subject"])
	set, _ := update.(bson.M)
	fields, _ := set["$set"].(bson.M)
	for i := range f.rows {
		if f.rows[i].Provider == provider && f.rows[i].Subject == subject {
			if v, ok := fields["displayName"].(string); ok {
				f.rows[i].DisplayName = v
			}
			if v, ok := fields["email"].(string); ok {
				f.rows[i].Email = v
			}
			return &mongo.UpdateResult{ModifiedCount: 1}, nil
		}
	}
	return &mongo.UpdateResult{}, nil
}

func (f *socialAuthIdentityDB) DeleteOne(_ context.Context, filter interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	provider, userID := devStr(m["provider"]), devStr(m["userId"])
	kept := f.rows[:0]
	for _, row := range f.rows {
		if row.Provider == provider && row.UserID == userID {
			f.deletes++
			continue
		}
		kept = append(kept, row)
	}
	f.rows = kept
	return nil
}

func (f *socialAuthIdentityDB) CountDocuments(_ context.Context, filter interface{}, _ ...*options.CountOptions) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	userID := devStr(m["userId"])
	var n int64
	for _, row := range f.rows {
		if userID == "" || row.UserID == userID {
			n++
		}
	}
	return n, nil
}

type socialPendingDB struct {
	databases.PendingVerificationDatabase
	mu   sync.Mutex
	rows []models.PendingVerification
}

func (f *socialPendingDB) FindOne(_ context.Context, filter interface{}) (*models.PendingVerification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	purpose := devStr(m["purpose"])
	oid, hasOID := devFilterObjectID(m["userID"])
	rowID, hasRowID := devFilterObjectID(m["_id"])
	for i := range f.rows {
		row := f.rows[i]
		if hasRowID && row.ID == rowID {
			return &row, nil
		}
		if hasOID && row.UserID == oid && (purpose == "" || row.Purpose == purpose) {
			return &row, nil
		}
	}
	return nil, mongo.ErrNoDocuments
}

func (f *socialPendingDB) InsertOne(_ context.Context, row models.PendingVerification, _ ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, row)
	return fakeInsertResult{id: row.ID}, nil
}

func (f *socialPendingDB) UpdateOne(_ context.Context, filter, update interface{}, _ ...*options.UpdateOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	oid, _ := devFilterObjectID(m["userID"])
	rowID, hasRowID := devFilterObjectID(m["_id"])
	u, _ := update.(bson.M)
	set, _ := u["$set"].(bson.M)
	inc, _ := u["$inc"].(bson.M)
	for i := range f.rows {
		if (hasRowID && f.rows[i].ID == rowID) || f.rows[i].UserID == oid {
			if code, ok := set["code"].(string); ok {
				f.rows[i].Code = code
			}
			if _, ok := inc["attempts"]; ok {
				f.rows[i].Attempts++
			}
			return nil
		}
	}
	return nil
}

func (f *socialPendingDB) DeleteOne(_ context.Context, filter interface{}, _ ...*options.DeleteOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	rowID, _ := devFilterObjectID(m["_id"])
	kept := f.rows[:0]
	for _, row := range f.rows {
		if row.ID == rowID {
			continue
		}
		kept = append(kept, row)
	}
	f.rows = kept
	return nil
}

// ---- fixture ----

type socialFixture struct {
	oidc    *oidcFixture
	api     SocialAuth
	userDB  *socialUserDB
	authDB  *socialAuthIdentityDB
	pending *socialPendingDB
	audit   *fakeAuditDB
}

func newSocialFixture(t *testing.T) *socialFixture {
	t.Helper()
	oidc := newOIDCFixture(t)
	f := &socialFixture{
		oidc:    oidc,
		userDB:  &socialUserDB{users: map[string]models.User{}},
		authDB:  &socialAuthIdentityDB{},
		pending: &socialPendingDB{},
		audit:   &fakeAuditDB{},
	}
	f.api = SocialAuth{
		UDB:      f.userDB,
		AuthDB:   f.authDB,
		PVDB:     f.pending,
		ALDB:     f.audit,
		Verifier: oidc.verifier,
	}
	return f
}

// post calls a handler with a JSON body, optionally as a given actor.
func (f *socialFixture) post(t *testing.T, h http.HandlerFunc, path, body, actorID string) *httptest.ResponseRecorder {
	t.Helper()
	url := path
	if actorID != "" {
		url += "?userId=" + actorID
	}
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr
}

func (f *socialFixture) token(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	return f.oidc.mint(t, claims)
}

func (f *socialFixture) verifyBody(t *testing.T, provider string, claims jwt.MapClaims) string {
	t.Helper()
	return `{"provider":"` + provider + `","idToken":"` + f.token(t, claims) + `","nonce":"` + testNonce + `"}`
}

func decodeVerify(t *testing.T, rr *httptest.ResponseRecorder) socialVerifyResponse {
	t.Helper()
	var out socialVerifyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding the response: %v (%s)", err, rr.Body.String())
	}
	return out
}

func (f *socialFixture) passwordUser(email string) models.User {
	user := models.User{
		ID: primitive.NewObjectID().Hex(),
		Details: models.UserDetails{
			Username: "existing", Email: email,
			Password: "$2a$10$abcdefghijklmnopqrstuv",
		},
	}
	f.userDB.add(user)
	return user
}

// ---- signing in and signing up ----

func TestSocialVerify_SignsInAKnownSubject(t *testing.T) {
	f := newSocialFixture(t)
	user := f.passwordUser("dev@example.com")
	f.authDB.rows = append(f.authDB.rows, models.AuthIdentity{
		ID: primitive.NewObjectID(), Provider: models.AuthProviderGoogle,
		Subject: "sub-known-1", UserID: user.ID,
	})

	rr := f.post(t, f.api.SocialVerifyHandler, "/auth/social/verify",
		f.verifyBody(t, models.AuthProviderGoogle, jwt.MapClaims{"sub": "sub-known-1"}), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeVerify(t, rr)
	if out.Outcome != socialOutcomeSignedIn {
		t.Errorf("outcome: got %q want %q", out.Outcome, socialOutcomeSignedIn)
	}
	if out.UserID != user.ID {
		t.Errorf("signed in as the wrong account: %q", out.UserID)
	}
	if f.userDB.inserts != 0 {
		t.Error("signing in must not create an account")
	}
}

func TestSocialVerify_MatchesOnSubjectNotEmail(t *testing.T) {
	f := newSocialFixture(t)
	user := f.passwordUser("dev@example.com")
	f.authDB.rows = append(f.authDB.rows, models.AuthIdentity{
		ID: primitive.NewObjectID(), Provider: models.AuthProviderGoogle,
		Subject: "sub-stable", UserID: user.ID, Email: "old-address@example.com",
	})

	// The person changed their Google address. The subject is unchanged, so
	// this is still the same sign-in.
	rr := f.post(t, f.api.SocialVerifyHandler, "/auth/social/verify",
		f.verifyBody(t, models.AuthProviderGoogle, jwt.MapClaims{
			"sub": "sub-stable", "email": "brand-new@example.com", "email_verified": true,
		}), "")
	out := decodeVerify(t, rr)
	if out.Outcome != socialOutcomeSignedIn || out.UserID != user.ID {
		t.Fatalf("a changed address must not change who signs in: %+v", out)
	}
}

func TestSocialVerify_SignsUpWhenNothingMatches(t *testing.T) {
	f := newSocialFixture(t)

	rr := f.post(t, f.api.SocialVerifyHandler, "/auth/social/verify",
		f.verifyBody(t, models.AuthProviderGoogle, jwt.MapClaims{
			"sub": "sub-new-1", "email": "newcomer@example.com",
			"email_verified": true, "name": "New Comer",
		}), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeVerify(t, rr)
	if out.Outcome != socialOutcomeSignedUp {
		t.Fatalf("outcome: got %q", out.Outcome)
	}
	if f.userDB.inserts != 1 {
		t.Fatalf("expected one account to be created, got %d", f.userDB.inserts)
	}
	created := f.userDB.users[out.UserID]
	if created.Details.Password != "" {
		t.Error("a social account must not be given a password")
	}
	if created.Details.Email != "newcomer@example.com" {
		t.Errorf("email: got %q", created.Details.Email)
	}
	if created.Details.EmailVerified == nil || !*created.Details.EmailVerified {
		t.Error("a provider-verified address should land verified")
	}
	// Claimed is left unset, which reads as claimed. That is what lets a social
	// account act as the claimant in the in-game claim flow.
	if !created.IsClaimed() || !created.CanAuthenticate() {
		t.Error("a social account must be a claimed, signable account")
	}
	if len(f.authDB.rows) != 1 || f.authDB.rows[0].Subject != "sub-new-1" {
		t.Errorf("expected the identity to be recorded, got %+v", f.authDB.rows)
	}
}

func TestSocialVerify_RefusesAnUnclaimedProvisionedAccount(t *testing.T) {
	f := newSocialFixture(t)
	claimedFalse := false
	shell := models.User{
		ID: primitive.NewObjectID().Hex(),
		Details: models.UserDetails{
			Username: "shell", DeveloperProvisioned: true, Claimed: &claimedFalse,
		},
	}
	f.userDB.add(shell)
	f.authDB.rows = append(f.authDB.rows, models.AuthIdentity{
		ID: primitive.NewObjectID(), Provider: models.AuthProviderGoogle,
		Subject: "sub-shell", UserID: shell.ID,
	})

	rr := f.post(t, f.api.SocialVerifyHandler, "/auth/social/verify",
		f.verifyBody(t, models.AuthProviderGoogle, jwt.MapClaims{"sub": "sub-shell"}), "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("an unclaimed account must not be signable, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSocialVerify_RefusesAnUnverifiableToken(t *testing.T) {
	f := newSocialFixture(t)

	for name, body := range map[string]string{
		"bad signature": `{"provider":"google","idToken":"` +
			f.oidc.mint(t, jwt.MapClaims{}, signedWithOtherKey(f.oidc)) + `","nonce":"` + testNonce + `"}`,
		"nonce mismatch": `{"provider":"google","idToken":"` +
			f.oidc.mint(t, jwt.MapClaims{"nonce": "another-flows-nonce-0001"}) + `","nonce":"` + testNonce + `"}`,
		"expired": `{"provider":"google","idToken":"` +
			f.oidc.mint(t, jwt.MapClaims{"exp": f.oidc.now.Add(-time.Hour).Unix()}) + `","nonce":"` + testNonce + `"}`,
		"wrong audience": `{"provider":"google","idToken":"` +
			f.oidc.mint(t, jwt.MapClaims{"aud": "someone-else.example"}) + `","nonce":"` + testNonce + `"}`,
		"unknown provider":          `{"provider":"facebook","idToken":"x","nonce":"` + testNonce + `"}`,
		"no nonce":                  `{"provider":"google","idToken":"x","nonce":""}`,
		"object for a string field": `{"provider":"google","idToken":{"$ne":""},"nonce":"` + testNonce + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rr := f.post(t, f.api.SocialVerifyHandler, "/auth/social/verify", body, "")
			if rr.Code == http.StatusOK {
				t.Fatalf("expected a refusal, got 200: %s", rr.Body.String())
			}
			if f.userDB.inserts != 0 {
				t.Error("a refused sign-in must not create an account")
			}
			if len(f.authDB.rows) != 0 {
				t.Error("a refused sign-in must not record an identity")
			}
		})
	}
}

// ---- linking ----

func TestSocialVerify_AsksToLinkWhenTheAddressIsTaken(t *testing.T) {
	f := newSocialFixture(t)
	owner := f.passwordUser("Taken@Example.com")

	rr := f.post(t, f.api.SocialVerifyHandler, "/auth/social/verify",
		f.verifyBody(t, models.AuthProviderGoogle, jwt.MapClaims{
			"sub": "sub-wants-link", "email": "taken@example.com", "email_verified": true,
		}), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeVerify(t, rr)
	if out.Outcome != socialOutcomeLinkRequired {
		t.Fatalf("outcome: got %q want %q", out.Outcome, socialOutcomeLinkRequired)
	}
	// Never a silent merge, and never the account id before ownership is proved.
	if out.UserID != "" {
		t.Error("the account id must not be returned before ownership is proved")
	}
	if len(f.authDB.rows) != 0 {
		t.Error("nothing may be linked at this point")
	}
	if f.userDB.inserts != 0 {
		t.Error("a second account must not be created for the same address")
	}
	if out.MaskedEmail == "" || strings.Contains(out.MaskedEmail, "taken@") {
		t.Errorf("the address should be masked, got %q", out.MaskedEmail)
	}
	_ = owner
}

func TestSocialLink_LinksWhenAlreadySignedIn(t *testing.T) {
	f := newSocialFixture(t)
	user := f.passwordUser("dev@example.com")

	body := `{"provider":"apple","idToken":"` +
		f.token(t, jwt.MapClaims{"sub": "001.apple.sub", "email": "dev@example.com", "email_verified": true}) +
		`","nonce":"` + testNonce + `","userId":"` + user.ID + `"}`
	rr := f.post(t, f.api.SocialLinkHandler, "/auth/social/link", body, user.ID)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(f.authDB.rows) != 1 || f.authDB.rows[0].UserID != user.ID {
		t.Fatalf("expected the identity on the signed-in account, got %+v", f.authDB.rows)
	}
}

func TestSocialLink_RefusesASubjectOnAnotherAccount(t *testing.T) {
	f := newSocialFixture(t)
	first := f.passwordUser("first@example.com")
	second := f.passwordUser("second@example.com")
	f.authDB.rows = append(f.authDB.rows, models.AuthIdentity{
		ID: primitive.NewObjectID(), Provider: models.AuthProviderGoogle,
		Subject: "sub-contested", UserID: first.ID,
	})

	body := `{"provider":"google","idToken":"` +
		f.token(t, jwt.MapClaims{"sub": "sub-contested"}) +
		`","nonce":"` + testNonce + `","userId":"` + second.ID + `"}`
	rr := f.post(t, f.api.SocialLinkHandler, "/auth/social/link", body, second.ID)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
	// The first account keeps its sign-in method.
	if len(f.authDB.rows) != 1 || f.authDB.rows[0].UserID != first.ID {
		t.Errorf("the existing link must not move, got %+v", f.authDB.rows)
	}
}

func TestSocialLink_IsIdempotent(t *testing.T) {
	f := newSocialFixture(t)
	user := f.passwordUser("dev@example.com")
	f.authDB.rows = append(f.authDB.rows, models.AuthIdentity{
		ID: primitive.NewObjectID(), Provider: models.AuthProviderGoogle,
		Subject: "sub-already", UserID: user.ID,
	})

	body := `{"provider":"google","idToken":"` +
		f.token(t, jwt.MapClaims{"sub": "sub-already"}) +
		`","nonce":"` + testNonce + `","userId":"` + user.ID + `"}`
	rr := f.post(t, f.api.SocialLinkHandler, "/auth/social/link", body, user.ID)
	if rr.Code != http.StatusOK {
		t.Fatalf("re-linking the same account should succeed quietly, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(f.authDB.rows) != 1 {
		t.Errorf("expected no duplicate row, got %d", len(f.authDB.rows))
	}
}

func TestSocialLink_EmailCodeProvesOwnership(t *testing.T) {
	f := newSocialFixture(t)
	owner := f.passwordUser("owner@example.com")

	claims := jwt.MapClaims{"sub": "sub-by-code", "email": "owner@example.com", "email_verified": true}
	body := f.verifyBody(t, models.AuthProviderGoogle, claims)

	rr := f.post(t, f.api.SocialLinkRequestCodeHandler, "/auth/social/link/request-code", body, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("requesting a code: %d %s", rr.Code, rr.Body.String())
	}
	if len(f.pending.rows) != 1 {
		t.Fatalf("expected a pending code, got %d", len(f.pending.rows))
	}
	code := f.pending.rows[0].Code
	if f.pending.rows[0].Purpose != PurposeSocialLink {
		t.Errorf("purpose: got %q", f.pending.rows[0].Purpose)
	}

	t.Run("a wrong code does not link", func(t *testing.T) {
		wrong := `{"provider":"google","idToken":"` + f.token(t, claims) +
			`","nonce":"` + testNonce + `","code":"000000"}`
		rr := f.post(t, f.api.SocialLinkConfirmHandler, "/auth/social/link/confirm", wrong, "")
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
		}
		if len(f.authDB.rows) != 0 {
			t.Error("a wrong code must not link anything")
		}
	})

	t.Run("the right code links", func(t *testing.T) {
		right := `{"provider":"google","idToken":"` + f.token(t, claims) +
			`","nonce":"` + testNonce + `","code":"` + code + `"}`
		rr := f.post(t, f.api.SocialLinkConfirmHandler, "/auth/social/link/confirm", right, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		if len(f.authDB.rows) != 1 || f.authDB.rows[0].UserID != owner.ID {
			t.Fatalf("expected the link on the owner, got %+v", f.authDB.rows)
		}
	})

	t.Run("the code is single use", func(t *testing.T) {
		again := `{"provider":"google","idToken":"` + f.token(t, claims) +
			`","nonce":"` + testNonce + `","code":"` + code + `"}`
		rr := f.post(t, f.api.SocialLinkConfirmHandler, "/auth/social/link/confirm", again, "")
		if rr.Code == http.StatusOK {
			t.Fatal("a used code must not work twice")
		}
	})
}

func TestSocialLink_CodePathRefusesARelayAddress(t *testing.T) {
	f := newSocialFixture(t)
	f.passwordUser("real@example.com")

	// A relay address is minted per application, so it is never the address
	// another account was registered with. There is nothing to prove here.
	body := f.verifyBody(t, models.AuthProviderApple, jwt.MapClaims{
		"sub": "001.relay", "email": "xyz@privaterelay.appleid.com",
		"email_verified": true, "is_private_email": true,
	})
	rr := f.post(t, f.api.SocialLinkRequestCodeHandler, "/auth/social/link/request-code", body, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(f.pending.rows) != 0 {
		t.Error("no code should be sent to a relay address for linking")
	}
}

// ---- Apple's awkward cases ----

func TestSocialVerify_RelayAddressNeverMatchesAnAccount(t *testing.T) {
	f := newSocialFixture(t)
	// An account that happens to hold the relay address. Matching on it would
	// be matching on a value only we ever see.
	f.passwordUser("xyz987@privaterelay.appleid.com")

	rr := f.post(t, f.api.SocialVerifyHandler, "/auth/social/verify",
		f.verifyBody(t, models.AuthProviderApple, jwt.MapClaims{
			"sub": "001.relay.sub", "email": "xyz987@privaterelay.appleid.com",
			"email_verified": true, "is_private_email": true,
		}), "")
	out := decodeVerify(t, rr)
	if out.Outcome != socialOutcomeSignedUp {
		t.Fatalf("a relay address must not trigger a link prompt, got %q", out.Outcome)
	}
	if f.userDB.inserts != 1 {
		t.Errorf("expected a new account, got %d inserts", f.userDB.inserts)
	}
	if !f.authDB.rows[0].PrivateRelay {
		t.Error("the relay flag should be recorded")
	}
}

func TestSocialVerify_SecondSignInKeepsTheNameWeAlreadyHave(t *testing.T) {
	f := newSocialFixture(t)
	user := f.passwordUser("dev@example.com")
	f.authDB.rows = append(f.authDB.rows, models.AuthIdentity{
		ID: primitive.NewObjectID(), Provider: models.AuthProviderApple,
		Subject: "001.named", UserID: user.ID, DisplayName: "Dev Example",
	})

	// Apple sends no name after the first authorization. Writing whatever
	// arrived would erase the name we already have.
	rr := f.post(t, f.api.SocialVerifyHandler, "/auth/social/verify",
		f.verifyBody(t, models.AuthProviderApple, jwt.MapClaims{"sub": "001.named"}), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("a returning Apple user must sign in, got %d: %s", rr.Code, rr.Body.String())
	}
	if f.authDB.rows[0].DisplayName != "Dev Example" {
		t.Errorf("the stored name must survive a nameless sign-in, got %q", f.authDB.rows[0].DisplayName)
	}
}

func TestSocialVerify_NewAccountWithNoNameAsksForOne(t *testing.T) {
	f := newSocialFixture(t)

	rr := f.post(t, f.api.SocialVerifyHandler, "/auth/social/verify",
		f.verifyBody(t, models.AuthProviderApple, jwt.MapClaims{
			"sub": "001.nameless", "email": "a@privaterelay.appleid.com",
			"email_verified": true, "is_private_email": true,
		}), "")
	out := decodeVerify(t, rr)
	if !out.NeedsProfile {
		t.Error("with no name from the provider the caller must be told to ask for one")
	}
	created := f.userDB.users[out.UserID]
	// Never derived from the address: a relay local part is meaningless, and a
	// real one would put part of somebody's email on a public profile.
	if strings.Contains(created.Details.Username, "@") ||
		strings.Contains(created.Details.Username, "privaterelay") {
		t.Errorf("the username must not come from the address, got %q", created.Details.Username)
	}
}

// ---- unlinking, and the last-method guard ----

func (f *socialFixture) unlink(t *testing.T, provider, actorID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/auth/social/link/"+provider+"?userId="+actorID, nil)
	req = mux.SetURLVars(req, map[string]string{"provider": provider})
	rr := httptest.NewRecorder()
	f.api.SocialUnlinkHandler(rr, req)
	return rr
}

func TestSocialUnlink_RefusesTheLastSignInMethod(t *testing.T) {
	f := newSocialFixture(t)
	// No password: Google is the only way in.
	user := models.User{ID: primitive.NewObjectID().Hex(),
		Details: models.UserDetails{Username: "social-only", Email: "only@example.com"}}
	f.userDB.add(user)
	f.authDB.rows = append(f.authDB.rows, models.AuthIdentity{
		ID: primitive.NewObjectID(), Provider: models.AuthProviderGoogle,
		Subject: "sub-only", UserID: user.ID,
	})

	rr := f.unlink(t, models.AuthProviderGoogle, user.ID)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
	if f.authDB.deletes != 0 || len(f.authDB.rows) != 1 {
		t.Error("the last way into an account must not be removable")
	}
}

func TestSocialUnlink_AllowsItWhenAPasswordRemains(t *testing.T) {
	f := newSocialFixture(t)
	user := f.passwordUser("dev@example.com")
	f.authDB.rows = append(f.authDB.rows, models.AuthIdentity{
		ID: primitive.NewObjectID(), Provider: models.AuthProviderGoogle,
		Subject: "sub-removable", UserID: user.ID,
	})

	rr := f.unlink(t, models.AuthProviderGoogle, user.ID)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(f.authDB.rows) != 0 {
		t.Error("the identity should be gone")
	}
}

func TestSocialUnlink_AllowsItWhenAnotherProviderRemains(t *testing.T) {
	f := newSocialFixture(t)
	// No password, but two providers: removing one still leaves a way in.
	user := models.User{ID: primitive.NewObjectID().Hex(),
		Details: models.UserDetails{Username: "two-providers", Email: "two@example.com"}}
	f.userDB.add(user)
	f.authDB.rows = append(f.authDB.rows,
		models.AuthIdentity{ID: primitive.NewObjectID(), Provider: models.AuthProviderGoogle,
			Subject: "sub-g", UserID: user.ID},
		models.AuthIdentity{ID: primitive.NewObjectID(), Provider: models.AuthProviderApple,
			Subject: "sub-a", UserID: user.ID},
	)

	if rr := f.unlink(t, models.AuthProviderApple, user.ID); rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// And now Google is the last one, so it is pinned.
	if rr := f.unlink(t, models.AuthProviderGoogle, user.ID); rr.Code != http.StatusConflict {
		t.Fatalf("the remaining provider must now be pinned, got %d", rr.Code)
	}
}

func TestSocialUnlink_RefusesAProviderThatIsNotLinked(t *testing.T) {
	f := newSocialFixture(t)
	user := f.passwordUser("dev@example.com")

	if rr := f.unlink(t, models.AuthProviderApple, user.ID); rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---- the settings list ----

func TestSocialMethods_ListsEveryWayIn(t *testing.T) {
	f := newSocialFixture(t)
	user := f.passwordUser("dev@example.com")
	f.authDB.rows = append(f.authDB.rows, models.AuthIdentity{
		ID: primitive.NewObjectID(), Provider: models.AuthProviderGoogle,
		Subject: "sub-listed", UserID: user.ID, Email: "dev@example.com",
	})

	req := httptest.NewRequest(http.MethodGet, "/auth/social/methods?userId="+user.ID, nil)
	rr := httptest.NewRecorder()
	f.api.SocialMethodsHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var out socialMethodsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(out.Methods) != 2 {
		t.Fatalf("expected a password and a provider, got %+v", out.Methods)
	}
	if !out.CanUnlink {
		t.Error("with two methods one should be removable")
	}
	var sawPassword, sawGoogle bool
	for _, m := range out.Methods {
		switch m.Kind {
		case "password":
			sawPassword = true
			if m.Removable {
				t.Error("the password is not removable through this surface")
			}
		case "provider":
			sawGoogle = m.Provider == models.AuthProviderGoogle
			if !m.Removable {
				t.Error("the provider should be removable here")
			}
		}
	}
	if !sawPassword || !sawGoogle {
		t.Errorf("expected both methods, got %+v", out.Methods)
	}
}

func TestSocialMethods_HidesARelayAddress(t *testing.T) {
	f := newSocialFixture(t)
	user := f.passwordUser("dev@example.com")
	f.authDB.rows = append(f.authDB.rows, models.AuthIdentity{
		ID: primitive.NewObjectID(), Provider: models.AuthProviderApple,
		Subject: "001.relay", UserID: user.ID,
		Email: "xyz@privaterelay.appleid.com", PrivateRelay: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/auth/social/methods?userId="+user.ID, nil)
	rr := httptest.NewRecorder()
	f.api.SocialMethodsHandler(rr, req)

	var out socialMethodsResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	for _, m := range out.Methods {
		if m.Provider == models.AuthProviderApple && m.Email != "" {
			t.Errorf("a relay address is ours, not theirs, and must not be shown as their address: %q", m.Email)
		}
	}
}

// ---- the email mask ----

func TestMaskEmail(t *testing.T) {
	for in, want := range map[string]string{
		"dev@example.com":     "d**@example.com",
		"ab@example.com":      "a*@example.com",
		"a@example.com":       "*@example.com",
		"merrill@example.com": "m*****l@example.com",
		"not-an-address":      "",
		"":                    "",
	} {
		if got := models.MaskEmail(in); got != want {
			t.Errorf("MaskEmail(%q) = %q, want %q", in, got, want)
		}
	}
}
