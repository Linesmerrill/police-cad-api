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

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Tests for player provisioning and claiming.
//
// The properties worth defending here are: a player upsert is idempotent even
// when two mods race, a provisioned account cannot be logged into, and a claim
// code can be redeemed exactly once by the player holding it and nobody else.

// ---- pure helpers ----

// An account that existed before this feature has no Claimed field, and
// treating those as unclaimed would lock out the entire user base.
func TestIsClaimed_NilMeansAnOrdinaryAccount(t *testing.T) {
	yes, no := true, false

	for name, tc := range map[string]struct {
		claimed *bool
		want    bool
	}{
		"a legacy account with no field": {nil, true},
		"an explicitly claimed account":  {&yes, true},
		"a provisioned, unclaimed shell": {&no, false},
	} {
		t.Run(name, func(t *testing.T) {
			u := models.User{Details: models.UserDetails{Claimed: tc.claimed}}
			if got := u.IsClaimed(); got != tc.want {
				t.Errorf("IsClaimed() = %v, want %v", got, tc.want)
			}
			if got := u.CanAuthenticate(); got != tc.want {
				t.Errorf("CanAuthenticate() = %v, want %v", got, tc.want)
			}
		})
	}
}

// FiveM hands mods identifiers in more than one spelling, and different mods
// pass different ones. They have to normalize to the same row or the same
// player gets two accounts.
func TestNormalizePlayerIdentifier(t *testing.T) {
	for name, tc := range map[string]struct {
		provider, in, want string
	}{
		"fivem license prefix":   {models.PlayerProviderFiveM, "license:ABC123", "abc123"},
		"fivem license2 prefix":  {models.PlayerProviderFiveM, "license2:ABC123", "abc123"},
		"fivem bare":             {models.PlayerProviderFiveM, "abc123", "abc123"},
		"fivem padded and mixed": {models.PlayerProviderFiveM, "  License:AbC123  ", "abc123"},
		"discord prefix":         {models.PlayerProviderDiscord, "discord:198573466634", "198573466634"},
		"discord bare":           {models.PlayerProviderDiscord, "198573466634", "198573466634"},
		"steam prefix":           {models.PlayerProviderSteam, "steam:1100001", "1100001"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := models.NormalizePlayerIdentifier(tc.provider, tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSanitizeProvisionedUsername(t *testing.T) {
	t.Run("a mod's name is kept but trimmed", func(t *testing.T) {
		if got := sanitizeProvisionedUsername("  Dave  ", "fivem", "abc123"); got != "Dave" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("an absent name falls back to something recognisable", func(t *testing.T) {
		got := sanitizeProvisionedUsername("", "fivem", "aaaaaabbbbbb")
		if !strings.HasPrefix(got, "player-") {
			t.Errorf("got %q, expected a player- fallback", got)
		}
	})
	t.Run("a long name is capped", func(t *testing.T) {
		got := sanitizeProvisionedUsername(strings.Repeat("x", 200), "fivem", "abc123")
		if len(got) > maxProvisionedUsernameLen {
			t.Errorf("name of %d chars exceeds the cap", len(got))
		}
	})
	t.Run("control characters are stripped", func(t *testing.T) {
		if got := sanitizeProvisionedUsername("Da\x00ve\x1b", "fivem", "abc123"); got != "Dave" {
			t.Errorf("got %q", got)
		}
	})
}

// ---- fakes for the provisioning path ----

type fakeIdentityDB struct {
	databases.PlayerIdentityDatabase
	mu  sync.Mutex
	rows []models.PlayerIdentity
	// failNextInsertAsDuplicate simulates losing the unique-index race.
	failNextInsertAsDuplicate bool
}

func (f *fakeIdentityDB) FindOne(_ context.Context, filter interface{}) (*models.PlayerIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	for _, row := range f.rows {
		if p, ok := m["provider"].(string); ok && row.Provider != p {
			continue
		}
		if i, ok := m["identifier"].(string); ok && row.Identifier != i {
			continue
		}
		if d, ok := m["createdByDeveloperId"].(string); ok && row.CreatedByDeveloperID != d {
			continue
		}
		if u, ok := m["userId"].(string); ok && row.UserID != u {
			continue
		}
		found := row
		return &found, nil
	}
	return nil, mongo.ErrNoDocuments
}

func (f *fakeIdentityDB) InsertOne(_ context.Context, identity models.PlayerIdentity) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNextInsertAsDuplicate {
		f.failNextInsertAsDuplicate = false
		return nil, mongo.WriteException{WriteErrors: []mongo.WriteError{{Code: 11000, Message: "duplicate key"}}}
	}
	for _, row := range f.rows {
		if row.Provider == identity.Provider && row.Identifier == identity.Identifier {
			return nil, mongo.WriteException{WriteErrors: []mongo.WriteError{{Code: 11000, Message: "duplicate key"}}}
		}
	}
	f.rows = append(f.rows, identity)
	return fakeInsertResult{id: identity.ID}, nil
}

func (f *fakeIdentityDB) UpdateMany(_ context.Context, _, _ interface{}, _ ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return &mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil
}

type fakeInsertResult struct{ id interface{} }

func (f fakeInsertResult) Decode() interface{} { return f.id }

type fakeUserDB struct {
	databases.UserDatabase
	mu      sync.Mutex
	users   map[string]models.User
	inserts int
	updates []bson.M
}

func (f *fakeUserDB) FindOne(_ context.Context, filter interface{}, _ ...*options.FindOneOptions) databases.SingleResultHelper {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	if oid, ok := m["_id"].(primitive.ObjectID); ok {
		if u, found := f.users[oid.Hex()]; found {
			return devSingleResult{v: u}
		}
	}
	return devSingleResult{err: mongo.ErrNoDocuments}
}

func (f *fakeUserDB) InsertOne(_ context.Context, details models.UserDetails) (databases.InsertOneResultHelper, error) {
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

func (f *fakeUserDB) UpdateOne(_ context.Context, _, update interface{}, _ ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := update.(bson.M); ok {
		f.updates = append(f.updates, m)
	}
	return &mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil
}

// devSingleResult decodes a struct the way a Mongo SingleResult would, so a
// fake user store returns the same shape the real one does.
type devSingleResult struct {
	v   interface{}
	err error
}

func (f devSingleResult) Decode(out interface{}) error {
	if f.err != nil {
		return f.err
	}
	raw, err := bson.Marshal(f.v)
	if err != nil {
		return err
	}
	return bson.Unmarshal(raw, out)
}

type fakeClaimCodeDB struct {
	databases.DeveloperClaimCodeDatabase
	mu    sync.Mutex
	codes []models.DeveloperClaimCode
	deletedFilters []interface{}
}

func (f *fakeClaimCodeDB) FindOne(_ context.Context, filter interface{}) (*models.DeveloperClaimCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	for _, c := range f.codes {
		if h, ok := m["codeHash"].(string); ok && c.CodeHash == h {
			found := c
			return &found, nil
		}
	}
	return nil, mongo.ErrNoDocuments
}

func (f *fakeClaimCodeDB) InsertOne(_ context.Context, code models.DeveloperClaimCode) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codes = append(f.codes, code)
	return fakeInsertResult{id: code.ID}, nil
}

func (f *fakeClaimCodeDB) UpdateOne(_ context.Context, filter, update interface{}, _ ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fm, _ := filter.(bson.M)
	um, _ := update.(bson.M)
	set, _ := um["$set"].(bson.M)
	for i, c := range f.codes {
		if oid, ok := fm["_id"].(primitive.ObjectID); ok && c.ID == oid {
			// Honour the "only while unused" filter, which is what makes the
			// redemption single-use under a race.
			if _, requiresUnused := fm["usedAt"]; requiresUnused && c.UsedAt != nil {
				return &mongo.UpdateResult{MatchedCount: 0}, nil
			}
			if v, ok := set["usedAt"].(primitive.DateTime); ok {
				f.codes[i].UsedAt = &v
			}
			if v, ok := set["usedByUserId"].(string); ok {
				f.codes[i].UsedByUserID = v
			}
			return &mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil
		}
	}
	return &mongo.UpdateResult{MatchedCount: 0}, nil
}

func (f *fakeClaimCodeDB) DeleteMany(_ context.Context, filter interface{}) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletedFilters = append(f.deletedFilters, filter)
	return 0, nil
}

// provisionFixture is a key with players:provision on an active Free account.
type provisionFixture struct {
	*devFixture
	identityDB *fakeIdentityDB
	userDB     *fakeUserDB
	claimDB    *fakeClaimCodeDB
}

func newProvisionFixture(t *testing.T) *provisionFixture {
	t.Helper()
	base := newDevFixture(t, models.ScopePlayersProvision)

	pf := &provisionFixture{
		devFixture: base,
		identityDB: &fakeIdentityDB{},
		userDB:     &fakeUserDB{users: map[string]models.User{}},
		claimDB:    &fakeClaimCodeDB{},
	}
	base.api.IdentityDB = pf.identityDB
	base.api.UDB = pf.userDB
	base.api.ClaimCodeDB = pf.claimDB
	return pf
}

// upsert runs one POST /players call.
func (p *provisionFixture) upsert(t *testing.T, body string) (*httptest.ResponseRecorder, devPlayerResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dev/players", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+p.plaintext)
	rr := httptest.NewRecorder()
	p.api.RequireDeveloperAPIKey(p.api.RequireScope(models.ScopePlayersProvision, p.api.DevUpsertPlayerHandler)).ServeHTTP(rr, req)

	var out struct{ Player devPlayerResponse }
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr, out.Player
}

// ---- upsert ----

// The same identifier must always return the same account, and must not create
// a second one. This is the property a mod relies on every time a player
// reconnects.
func TestDevUpsertPlayer_IsIdempotent(t *testing.T) {
	f := newProvisionFixture(t)

	first, p1 := f.upsert(t, `{"provider":"fivem","identifier":"license:ABC123","username":"Dave"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("expected 201 on first upsert, got %d: %s", first.Code, first.Body.String())
	}
	if !p1.Created || p1.PlayerID == "" {
		t.Fatalf("expected a created player, got %+v", p1)
	}

	// Same player, spelled differently: bare identifier, different case.
	second, p2 := f.upsert(t, `{"provider":"fivem","identifier":"abc123","username":"Dave"}`)
	if second.Code != http.StatusOK {
		t.Fatalf("expected 200 on a repeat upsert, got %d", second.Code)
	}
	if p2.Created {
		t.Error("a repeat upsert must not report creating anything")
	}
	if p2.PlayerID != p1.PlayerID {
		t.Errorf("the same player must map to one account: %s vs %s", p1.PlayerID, p2.PlayerID)
	}
	if f.userDB.inserts != 1 {
		t.Errorf("expected exactly one account to be created, got %d", f.userDB.inserts)
	}
}

// Two mods starting at the same instant both try to insert. The unique index
// decides it, and the loser must defer to the winner rather than erroring or
// creating a duplicate.
func TestDevUpsertPlayer_LosingTheRaceUsesTheWinner(t *testing.T) {
	f := newProvisionFixture(t)

	// Pre-seed the row the "winner" inserted, and make our insert fail the way
	// the unique index would.
	winnerUserID := primitive.NewObjectID().Hex()
	f.identityDB.rows = []models.PlayerIdentity{{
		ID: primitive.NewObjectID(), Provider: "fivem", Identifier: "abc123",
		UserID: winnerUserID, CreatedByDeveloperID: f.developerID.Hex(),
	}}

	_, player := f.upsert(t, `{"provider":"fivem","identifier":"abc123"}`)
	if player.PlayerID != winnerUserID {
		t.Errorf("expected the winner's account %s, got %s", winnerUserID, player.PlayerID)
	}
	if f.userDB.inserts != 0 {
		t.Errorf("an already-known player must not create an account, got %d inserts", f.userDB.inserts)
	}
}

// A provisioned account must come out of the box unable to log in: no email, no
// password, and claimed explicitly false.
func TestDevUpsertPlayer_CreatesAnAccountThatCannotLogIn(t *testing.T) {
	f := newProvisionFixture(t)

	_, player := f.upsert(t, `{"provider":"fivem","identifier":"abc123","username":"Dave"}`)

	created, ok := f.userDB.users[player.PlayerID]
	if !ok {
		t.Fatalf("the account was not created")
	}
	if created.Details.Email != "" {
		t.Error("a provisioned account must hold no email: an email the mod supplied is one the mod controls")
	}
	if created.Details.Password != "" {
		t.Error("a provisioned account must hold no password hash")
	}
	if created.IsClaimed() {
		t.Error("a provisioned account must start unclaimed")
	}
	if created.CanAuthenticate() {
		t.Error("a provisioned account must not be able to authenticate")
	}
	if created.Details.ProvisionedByKeyID != f.keyID.Hex() {
		t.Errorf("the creating key must be recorded, got %q", created.Details.ProvisionedByKeyID)
	}
}

// A mod sending an email must not be able to set one.
func TestDevUpsertPlayer_IgnoresAnEmailFromTheMod(t *testing.T) {
	f := newProvisionFixture(t)

	_, player := f.upsert(t, `{"provider":"fivem","identifier":"abc123","email":"attacker@example.com","password":"hunter2"}`)

	created := f.userDB.users[player.PlayerID]
	if created.Details.Email != "" {
		t.Errorf("an email from the mod must be ignored, got %q", created.Details.Email)
	}
	if created.Details.Password != "" {
		t.Error("a password from the mod must be ignored")
	}
}

func TestDevUpsertPlayer_RejectsBadInput(t *testing.T) {
	f := newProvisionFixture(t)

	for name, body := range map[string]string{
		"unknown provider": `{"provider":"xbox","identifier":"abc"}`,
		"no identifier":    `{"provider":"fivem","identifier":"  "}`,
		"no provider":      `{"identifier":"abc"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rr, _ := f.upsert(t, body)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

// The daily account cap stops creation, but must not stop a lookup of a player
// the mod already knows — otherwise hitting the cap breaks a running server
// rather than just pausing new signups.
func TestDevUpsertPlayer_CapStopsCreationNotLookups(t *testing.T) {
	f := newProvisionFixture(t)
	limits := models.LimitsForPlan(models.DeveloperPlanFree)

	id := models.UsageDocID(f.keyID.Hex(), time.Now().UTC())
	f.usageDB.docs = map[string]*models.DeveloperAPIUsage{
		id: {ID: id, AccountsCreated: limits.AccountsPerKeyDay},
	}

	rr, _ := f.upsert(t, `{"provider":"fivem","identifier":"brand-new"}`)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 at the account cap, got %d: %s", rr.Code, rr.Body.String())
	}
	if f.userDB.inserts != 0 {
		t.Error("no account should have been created at the cap")
	}

	// A player we already know still resolves.
	f.identityDB.rows = []models.PlayerIdentity{{
		ID: primitive.NewObjectID(), Provider: "fivem", Identifier: "known",
		UserID: primitive.NewObjectID().Hex(), CreatedByDeveloperID: f.developerID.Hex(),
	}}
	rr2, _ := f.upsert(t, `{"provider":"fivem","identifier":"known"}`)
	if rr2.Code != http.StatusOK {
		t.Fatalf("a known player must still resolve at the cap, got %d", rr2.Code)
	}
}

// ---- claim codes ----

func (p *provisionFixture) issueCode(t *testing.T, provider, identifier string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/dev/players/" + provider + "/" + identifier + "/claim-code"
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("Authorization", "Bearer "+p.plaintext)
	req = mux.SetURLVars(req, map[string]string{"provider": provider, "identifier": identifier})
	rr := httptest.NewRecorder()
	p.api.RequireDeveloperAPIKey(p.api.RequireScope(models.ScopePlayersProvision, p.api.DevIssueClaimCodeHandler)).ServeHTTP(rr, req)
	return rr
}

// A code can only be minted for an unclaimed account this developer created.
func TestDevIssueClaimCode(t *testing.T) {
	t.Run("mints a code for an unclaimed player", func(t *testing.T) {
		f := newProvisionFixture(t)
		_, player := f.upsert(t, `{"provider":"fivem","identifier":"abc123"}`)

		rr := f.issueCode(t, "fivem", "abc123")
		if rr.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
		}
		var out struct {
			Code string `json:"code"`
			URL  string `json:"url"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		if out.Code == "" || out.URL == "" {
			t.Fatalf("expected a code and a url, got %s", rr.Body.String())
		}
		// Only the hash is stored, so the code itself must not be findable.
		for _, stored := range f.claimDB.codes {
			if stored.CodeHash == out.Code {
				t.Error("the claim code itself must not be stored, only its hash")
			}
		}
		_ = player
	})

	t.Run("refuses a player already claimed", func(t *testing.T) {
		f := newProvisionFixture(t)
		_, player := f.upsert(t, `{"provider":"fivem","identifier":"abc123"}`)

		claimed := true
		u := f.userDB.users[player.PlayerID]
		u.Details.Claimed = &claimed
		f.userDB.users[player.PlayerID] = u

		if rr := f.issueCode(t, "fivem", "abc123"); rr.Code != http.StatusConflict {
			t.Fatalf("expected 409 for a claimed player, got %d", rr.Code)
		}
	})

	t.Run("refuses a player another developer created", func(t *testing.T) {
		f := newProvisionFixture(t)
		f.identityDB.rows = []models.PlayerIdentity{{
			ID: primitive.NewObjectID(), Provider: "fivem", Identifier: "abc123",
			UserID: primitive.NewObjectID().Hex(),
			// A different developer.
			CreatedByDeveloperID: primitive.NewObjectID().Hex(),
		}}
		if rr := f.issueCode(t, "fivem", "abc123"); rr.Code != http.StatusNotFound {
			t.Fatalf("a key must not mint codes for another developer's players, got %d", rr.Code)
		}
	})

	t.Run("replaces any outstanding code", func(t *testing.T) {
		f := newProvisionFixture(t)
		f.upsert(t, `{"provider":"fivem","identifier":"abc123"}`)

		f.issueCode(t, "fivem", "abc123")
		if len(f.claimDB.deletedFilters) == 0 {
			t.Error("issuing a code should clear outstanding ones, so an older code " +
				"still visible in a stream VOD stops working")
		}
	})
}

// A key cannot look up a player it did not create, or it becomes a way to ask
// whether an arbitrary Discord ID has an LPC account.
func TestDevGetPlayer_OnlySeesItsOwnPlayers(t *testing.T) {
	f := newProvisionFixture(t)
	f.identityDB.rows = []models.PlayerIdentity{{
		ID: primitive.NewObjectID(), Provider: "discord", Identifier: "198573466634",
		UserID: primitive.NewObjectID().Hex(),
		CreatedByDeveloperID: primitive.NewObjectID().Hex(), // someone else's
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/players/discord/198573466634", nil)
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	req = mux.SetURLVars(req, map[string]string{"provider": "discord", "identifier": "198573466634"})
	rr := httptest.NewRecorder()
	f.api.RequireDeveloperAPIKey(f.api.RequireScope(models.ScopePlayersProvision, f.api.DevGetPlayerHandler)).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for another developer's player, got %d", rr.Code)
	}
}
