package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// refreshUserDB answers the one lookup the refresh does: by account id.
type refreshUserDB struct {
	databases.UserDatabase
	users map[string]models.User
}

func (f *refreshUserDB) FindOne(_ context.Context, filter interface{}, _ ...*options.FindOneOptions) databases.SingleResultHelper {
	m, _ := filter.(bson.M)
	oid, ok := m["_id"].(primitive.ObjectID)
	if !ok {
		return &refreshResult{err: mongo.ErrNoDocuments}
	}
	if u, found := f.users[oid.Hex()]; found {
		return &refreshResult{user: u}
	}
	return &refreshResult{err: mongo.ErrNoDocuments}
}

type refreshResult struct {
	user models.User
	err  error
}

func (r *refreshResult) Decode(v interface{}) error {
	if r.err != nil {
		return r.err
	}
	if u, ok := v.(*models.User); ok {
		*u = r.user
	}
	return nil
}

// newRefreshFixture wires a MiddlewareDB whose token store and authenticator
// are real, backed by the package's fake collection, so a refresh issues a
// token that could actually be loaded back.
func newRefreshFixture(t *testing.T, users ...models.User) MiddlewareDB {
	t.Helper()
	byID := map[string]models.User{}
	for _, u := range users {
		byID[u.ID] = u
	}
	m := MiddlewareDB{DB: &refreshUserDB{users: byID}}
	m.SetupGoGuardian(&fakeDB{coll: &fakeCollection{}})
	return m
}

func refreshUser() models.User {
	return models.User{
		ID: primitive.NewObjectID().Hex(),
		Details: models.UserDetails{
			Username: "player", Email: "player@example.com",
			// No password: this is exactly the account that cannot use
			// /auth/token and is the reason this route exists.
			Password: "",
		},
	}
}

// callRefresh runs the handler as the given account, the way the middleware
// would after verifying a bearer token. An empty actor stands for a request
// whose token did not resolve.
func callRefresh(m MiddlewareDB, actorID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/session/refresh", nil)
	if actorID != "" {
		req = req.WithContext(withAuthenticatedUserID(req.Context(), actorID))
	}
	rr := httptest.NewRecorder()
	m.RefreshToken(rr, req)
	return rr
}

func TestRefreshToken_IssuesAFreshSession(t *testing.T) {
	user := refreshUser()
	m := newRefreshFixture(t, user)

	rr := callRefresh(m, user.ID)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out refreshResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v (%s)", err, rr.Body.String())
	}
	if out.Token == "" {
		t.Error("no token was issued")
	}
	if out.ID != user.ID {
		t.Errorf("_id: got %q want %q", out.ID, user.ID)
	}
}

func TestRefreshToken_RefusesARequestWithNoVerifiedToken(t *testing.T) {
	user := refreshUser()
	m := newRefreshFixture(t, user)

	// The middleware resolves a bearer into the context and nothing else does.
	// A request that arrives without one has proved nothing.
	rr := callRefresh(m, "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "token") && strings.Contains(rr.Body.String(), user.ID) {
		t.Error("the refusal should not name an account")
	}
}

func TestRefreshToken_RefusesAnAccountThatIsGone(t *testing.T) {
	m := newRefreshFixture(t)

	rr := callRefresh(m, primitive.NewObjectID().Hex())
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRefreshToken_RefusesAnUnusableAccountID(t *testing.T) {
	m := newRefreshFixture(t)

	rr := callRefresh(m, "not-an-object-id")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRefreshToken_RefusesASuspendedAccount(t *testing.T) {
	user := refreshUser()
	until := primitive.NewDateTimeFromTime(time.Now().Add(72 * time.Hour))
	user.Details.Suspension = &models.Suspension{Until: &until}
	m := newRefreshFixture(t, user)

	// This is what makes the ten-minute renewal cycle enforce a suspension
	// applied mid-session, instead of it waiting for the token's own expiry.
	rr := callRefresh(m, user.ID)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "suspended") {
		t.Errorf("the refusal should say it is a suspension: %s", rr.Body.String())
	}
}

func TestRefreshToken_RefusesADeactivatedAccount(t *testing.T) {
	user := refreshUser()
	user.Details.IsDeactivated = true
	m := newRefreshFixture(t, user)

	rr := callRefresh(m, user.ID)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "deactivated") {
		t.Errorf("the refusal should say it is deactivated: %s", rr.Body.String())
	}
}

func TestRefreshToken_RefusesAnUnclaimedProvisionedAccount(t *testing.T) {
	notClaimed := false
	user := models.User{
		ID:      primitive.NewObjectID().Hex(),
		Details: models.UserDetails{Claimed: &notClaimed},
	}
	m := newRefreshFixture(t, user)

	rr := callRefresh(m, user.ID)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---- the minting itself ----

func TestIssueBearerToken_StoresATokenThatLoadsBack(t *testing.T) {
	m := MiddlewareDB{}
	m.SetupGoGuardian(&fakeDB{coll: &fakeCollection{}})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", nil)
	token, err := IssueBearerToken(req, "player@example.com", "uid-9")
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}
	if token == "" {
		t.Fatal("no token")
	}
	info, ok, err := tokenCache.Load(token, req)
	if err != nil || !ok {
		t.Fatalf("the token did not load back: ok=%v err=%v", ok, err)
	}
	if got := info.(interface{ ID() string }).ID(); got != "uid-9" {
		t.Errorf("the token names the wrong account: %q", got)
	}
}

func TestIssueBearerToken_RefusesToMintWithoutAnAccount(t *testing.T) {
	m := MiddlewareDB{}
	m.SetupGoGuardian(&fakeDB{coll: &fakeCollection{}})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", nil)
	if _, err := IssueBearerToken(req, "player@example.com", "   "); err == nil {
		t.Fatal("a token with no account behind it must not be issued")
	}
}
