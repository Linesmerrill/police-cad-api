package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// Tests for the console's editing surface. The properties worth holding here
// are the authority ones: editing a key must not be a way around the checks
// creation makes, and removing access must not be able to fail.

// ---- fakes ----

// consoleKeyDB answers by (_id, userId) and records what was written, which is
// what the update path needs: the ownership condition is the whole check.
type consoleKeyDB struct {
	databases.DeveloperAPIKeyDatabase
	keys    map[string]models.DeveloperAPIKey
	updates []bson.M
	// denyUser makes every lookup miss, standing in for a key owned by
	// somebody else.
	denyUser string
}

func (f *consoleKeyDB) FindOne(_ context.Context, filter interface{}) (*models.DeveloperAPIKey, error) {
	m, _ := filter.(bson.M)
	oid, ok := devFilterObjectID(m["_id"])
	if !ok {
		return nil, mongo.ErrNoDocuments
	}
	key, found := f.keys[oid.Hex()]
	if !found {
		return nil, mongo.ErrNoDocuments
	}
	// The handler always scopes by userId. A fake that ignored it would make
	// every ownership test pass for the wrong reason.
	if want, ok := devFilterString(m["userId"]); ok {
		if want != key.UserID || want == f.denyUser {
			return nil, mongo.ErrNoDocuments
		}
	}
	return &key, nil
}

func (f *consoleKeyDB) UpdateOne(_ context.Context, filter, update interface{}, _ ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	m, _ := update.(bson.M)
	f.updates = append(f.updates, m)
	fm, _ := filter.(bson.M)
	oid, ok := devFilterObjectID(fm["_id"])
	if !ok {
		return &mongo.UpdateResult{}, nil
	}
	key := f.keys[oid.Hex()]
	if set, ok := m["$set"].(bson.M); ok {
		if name, ok := set["name"].(string); ok {
			key.Name = name
		}
		if scopes, ok := set["scopes"].([]string); ok {
			key.Scopes = scopes
		}
	}
	f.keys[oid.Hex()] = key
	return &mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil
}

func (f *consoleKeyDB) Find(_ context.Context, _ interface{}, _ ...*options.FindOptions) (*databases.MongoCursor, error) {
	docs := make([]interface{}, 0, len(f.keys))
	for _, k := range f.keys {
		docs = append(docs, k)
	}
	cur, err := databases.NewMongoCursorFromDocuments(docs)
	if err != nil {
		return nil, err
	}
	return &cur, nil
}

func (f *consoleKeyDB) CountDocuments(_ context.Context, _ interface{}, _ ...*options.CountOptions) (int64, error) {
	return int64(len(f.keys)), nil
}

// consoleGrantDB is a grant store that can be read, written and counted, since
// every path under test turns on how many live grants a key has.
type consoleGrantDB struct {
	databases.DeveloperGrantDatabase
	grants []models.DeveloperCommunityGrant
}

func (f *consoleGrantDB) FindOne(_ context.Context, filter interface{}) (*models.DeveloperCommunityGrant, error) {
	m, _ := filter.(bson.M)
	if oid, ok := devFilterObjectID(m["_id"]); ok {
		for _, g := range f.grants {
			if g.ID == oid {
				found := g
				return &found, nil
			}
		}
		return nil, mongo.ErrNoDocuments
	}
	wantKey, _ := devFilterString(m["keyId"])
	wantCommunity, _ := devFilterString(m["communityId"])
	for _, g := range f.grants {
		if g.KeyID == wantKey && g.CommunityID == wantCommunity {
			found := g
			return &found, nil
		}
	}
	return nil, mongo.ErrNoDocuments
}

func (f *consoleGrantDB) Find(_ context.Context, _ interface{}, _ ...*options.FindOptions) (*databases.MongoCursor, error) {
	docs := make([]interface{}, 0, len(f.grants))
	for _, g := range f.grants {
		docs = append(docs, g)
	}
	cur, err := databases.NewMongoCursorFromDocuments(docs)
	if err != nil {
		return nil, err
	}
	return &cur, nil
}

func (f *consoleGrantDB) InsertOne(_ context.Context, grant models.DeveloperCommunityGrant) (databases.InsertOneResultHelper, error) {
	f.grants = append(f.grants, grant)
	return fakeInsertResult{id: grant.ID}, nil
}

func (f *consoleGrantDB) UpdateOne(_ context.Context, filter, update interface{}, _ ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	fm, _ := filter.(bson.M)
	oid, _ := devFilterObjectID(fm["_id"])
	m, _ := update.(bson.M)
	set, _ := m["$set"].(bson.M)
	for i, g := range f.grants {
		if g.ID != oid {
			continue
		}
		if status, ok := set["status"].(string); ok {
			f.grants[i].Status = status
		}
		return &mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil
	}
	return &mongo.UpdateResult{}, nil
}

// CountDocuments counts live grants for a key, which is the only count any
// handler here asks for.
func (f *consoleGrantDB) CountDocuments(_ context.Context, filter interface{}, _ ...*options.CountOptions) (int64, error) {
	m, _ := filter.(bson.M)
	wantKey, _ := devFilterString(m["keyId"])
	var n int64
	for _, g := range f.grants {
		if g.KeyID == wantKey && g.Status != models.DeveloperKeyRevoked {
			n++
		}
	}
	return n, nil
}

// consoleUserDB adds the membership count the shared user fake does not have.
// isApprovedMemberOfCommunity asks the database rather than reading the user
// record, so a fake without it panics rather than returning the wrong answer.
type consoleUserDB struct {
	*fakeUserDB
}

func (f *consoleUserDB) CountDocuments(_ context.Context, filter interface{}, _ ...*options.CountOptions) (int64, error) {
	m, _ := filter.(bson.M)
	oid, ok := devFilterObjectID(m["_id"])
	if !ok {
		return 0, nil
	}
	user, found := f.users[oid.Hex()]
	if !found {
		return 0, nil
	}
	elem, _ := m["user.communities"].(bson.M)
	match, _ := elem["$elemMatch"].(bson.M)
	wantCommunity, _ := match["communityId"].(string)
	wantStatus, _ := match["status"].(string)
	for _, c := range user.Details.Communities {
		if c.CommunityID == wantCommunity && c.Status == wantStatus {
			return 1, nil
		}
	}
	return 0, nil
}

// consoleCommunityDB answers by _id, and reports a community as manageable or
// not depending on who the fixture made the owner.
type consoleCommunityDB struct {
	databases.CommunityDatabase
	communities map[string]models.Community
}

func (f *consoleCommunityDB) FindOne(_ context.Context, filter interface{}) (*models.Community, error) {
	m, _ := filter.(bson.M)
	oid, ok := devFilterObjectID(m["_id"])
	if !ok {
		return nil, mongo.ErrNoDocuments
	}
	c, found := f.communities[oid.Hex()]
	if !found {
		return nil, mongo.ErrNoDocuments
	}
	return &c, nil
}

func (f *consoleCommunityDB) FindOneIncludingPending(ctx context.Context, filter interface{}) (*models.Community, error) {
	return f.FindOne(ctx, filter)
}

// consoleUsageDB serves a fixed set of daily rows.
type consoleUsageDB struct {
	databases.DeveloperUsageDatabase
	rows []models.DeveloperAPIUsage
}

func (f *consoleUsageDB) Find(_ context.Context, filter interface{}, _ ...*options.FindOptions) (*databases.MongoCursor, error) {
	m, _ := filter.(bson.M)
	wantKey, _ := devFilterString(m["keyId"])
	docs := make([]interface{}, 0, len(f.rows))
	for _, r := range f.rows {
		if r.KeyID == wantKey {
			docs = append(docs, r)
		}
	}
	cur, err := databases.NewMongoCursorFromDocuments(docs)
	if err != nil {
		return nil, err
	}
	return &cur, nil
}

// ---- fixture ----

type consoleFixture struct {
	api       DeveloperAPI
	keyDB     *consoleKeyDB
	grantDB   *consoleGrantDB
	commDB    *consoleCommunityDB
	usageDB   *consoleUsageDB
	userDB    *consoleUserDB
	owner     models.User
	keyID     primitive.ObjectID
	accountID primitive.ObjectID
}

// newConsoleFixture builds one live key owned by one user, with no communities
// and the provisioning scope, which is the simplest coherent key there is.
func newConsoleFixture(t *testing.T, scopes ...string) *consoleFixture {
	t.Helper()
	if len(scopes) == 0 {
		scopes = []string{models.ScopeCommunitiesProvision}
	}

	ownerID := primitive.NewObjectID()
	owner := models.User{ID: ownerID.Hex()}
	owner.Details.Subscription.Plan = models.TierPremium
	owner.Details.Subscription.Active = true

	accountID := primitive.NewObjectID()
	keyID := primitive.NewObjectID()
	now := primitive.NewDateTimeFromTime(time.Now())

	keyDB := &consoleKeyDB{keys: map[string]models.DeveloperAPIKey{
		keyID.Hex(): {
			ID:            keyID,
			DeveloperID:   accountID.Hex(),
			UserID:        owner.ID,
			Name:          "Dispatch bridge",
			Scopes:        scopes,
			Status:        models.DeveloperKeyActive,
			DisplayPrefix: "lpc_live_abcd",
			LastFour:      "wxyz",
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}}

	userDB := &consoleUserDB{fakeUserDB: &fakeUserDB{
		users: map[string]models.User{owner.ID: owner},
	}}

	f := &consoleFixture{
		keyDB:     keyDB,
		grantDB:   &consoleGrantDB{},
		commDB:    &consoleCommunityDB{communities: map[string]models.Community{}},
		usageDB:   &consoleUsageDB{},
		userDB:    userDB,
		owner:     owner,
		keyID:     keyID,
		accountID: accountID,
	}
	f.api = DeveloperAPI{
		KeyDB:   keyDB,
		GrantDB: f.grantDB,
		CDB:     f.commDB,
		UDB:     userDB,
		UsageDB: f.usageDB,
		AccountDB: &limitsAccountDB{account: &models.DeveloperAccount{
			ID:     accountID,
			UserID: owner.ID,
			Status: models.DeveloperAccountActive,
		}},
	}
	return f
}

// addCommunity registers a community, owned by the fixture's user when `mine`.
func (f *consoleFixture) addCommunity(name string, mine bool) primitive.ObjectID {
	id := primitive.NewObjectID()
	c := models.Community{ID: id}
	c.Details.Name = name
	if mine {
		c.Details.OwnerID = f.owner.ID
	} else {
		c.Details.OwnerID = primitive.NewObjectID().Hex()
	}
	f.commDB.communities[id.Hex()] = c
	return id
}

// joinCommunity makes the fixture's user an approved member of a community they
// do not own, which is the "can request, cannot approve" case.
func (f *consoleFixture) joinCommunity(id primitive.ObjectID) {
	user := f.userDB.users[f.owner.ID]
	user.Details.Communities = append(user.Details.Communities, models.UserCommunity{
		CommunityID: id.Hex(),
		Status:      models.CommunityMemberStatusApproved,
	})
	f.userDB.users[f.owner.ID] = user
}

// liveGrant puts an already-approved grant on the fixture's key.
func (f *consoleFixture) liveGrant(community primitive.ObjectID) {
	now := primitive.NewDateTimeFromTime(time.Now())
	f.grantDB.grants = append(f.grantDB.grants, models.DeveloperCommunityGrant{
		ID:          primitive.NewObjectID(),
		KeyID:       f.keyID.Hex(),
		DeveloperID: f.accountID.Hex(),
		CommunityID: community.Hex(),
		Status:      models.DeveloperKeyActive,
		Source:      models.DeveloperGrantOwnerApproved,
		RequestedAt: now,
		ApprovedAt:  &now,
	})
}

// request builds a routed request with the fixture's actor on it.
func (f *consoleFixture) request(method, path string, body interface{}, vars map[string]string) *http.Request {
	var reader *strings.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = strings.NewReader(string(raw))
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path+"?userId="+f.owner.ID, reader)
	if vars != nil {
		req = mux.SetURLVars(req, vars)
	}
	return req
}

// ---- editing a key ----

func TestUpdateDeveloperKey_RenamesWithoutChangingTheSecret(t *testing.T) {
	f := newConsoleFixture(t)
	before := f.keyDB.keys[f.keyID.Hex()]

	req := f.request(http.MethodPatch, "/api/v1/developer/keys/"+f.keyID.Hex(),
		map[string]interface{}{"name": "FiveM bridge"},
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.UpdateDeveloperKeyHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	after := f.keyDB.keys[f.keyID.Hex()]
	if after.Name != "FiveM bridge" {
		t.Errorf("expected the new name, got %q", after.Name)
	}
	// The point of editing in place: the secret is untouched, so nothing holding
	// the key has to be redeployed.
	if after.KeyHash != before.KeyHash || after.DisplayPrefix != before.DisplayPrefix {
		t.Error("an edit must not change the key's secret or its prefix")
	}
}

func TestUpdateDeveloperKey_WidensAndNarrowsScopes(t *testing.T) {
	f := newConsoleFixture(t)
	community := f.addCommunity("My Server", true)
	f.liveGrant(community)

	req := f.request(http.MethodPatch, "/api/v1/developer/keys/"+f.keyID.Hex(),
		map[string]interface{}{"scopes": []string{models.ScopeCallsRead, models.ScopeCallsWrite}},
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.UpdateDeveloperKeyHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	got := f.keyDB.keys[f.keyID.Hex()].Scopes
	if len(got) != 2 || !containsString(got, models.ScopeCallsWrite) {
		t.Errorf("expected the new scope list, got %v", got)
	}
}

func TestUpdateDeveloperKey_RefusesAnUnknownScope(t *testing.T) {
	f := newConsoleFixture(t)
	community := f.addCommunity("My Server", true)
	f.liveGrant(community)

	req := f.request(http.MethodPatch, "/api/v1/developer/keys/"+f.keyID.Hex(),
		map[string]interface{}{"scopes": []string{"everything:always"}},
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.UpdateDeveloperKeyHandler(rr, req)

	// Refused, not silently dropped. A key that quietly does less than asked
	// reports itself as a 403 in production.
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if containsString(f.keyDB.keys[f.keyID.Hex()].Scopes, "everything:always") {
		t.Error("an unknown scope must never be stored")
	}
}

func TestUpdateDeveloperKey_RefusesLeavingAKeyAbleToDoNothing(t *testing.T) {
	// No communities, and the edit takes away the only scope that would let it
	// make one. The same rule creation applies, re-checked because an edit can
	// arrive at that state from the other direction.
	f := newConsoleFixture(t)

	req := f.request(http.MethodPatch, "/api/v1/developer/keys/"+f.keyID.Hex(),
		map[string]interface{}{"scopes": []string{models.ScopeCallsRead}},
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.UpdateDeveloperKeyHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "communities:provision") {
		t.Errorf("the message should name the missing permission, got %s", rr.Body.String())
	}
}

func TestUpdateDeveloperKey_AllowsDroppingProvisioningWhenACommunityRemains(t *testing.T) {
	f := newConsoleFixture(t)
	community := f.addCommunity("My Server", true)
	f.liveGrant(community)

	req := f.request(http.MethodPatch, "/api/v1/developer/keys/"+f.keyID.Hex(),
		map[string]interface{}{"scopes": []string{models.ScopeCallsRead}},
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.UpdateDeveloperKeyHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUpdateDeveloperKey_RefusesABlankName(t *testing.T) {
	f := newConsoleFixture(t)
	req := f.request(http.MethodPatch, "/api/v1/developer/keys/"+f.keyID.Hex(),
		map[string]interface{}{"name": "   "},
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.UpdateDeveloperKeyHandler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUpdateDeveloperKey_NotSomebodyElsesKey(t *testing.T) {
	f := newConsoleFixture(t)
	// The key exists; the actor does not own it. A 404 rather than a 403, so the
	// endpoint does not confirm that a key id someone guessed is real.
	f.keyDB.denyUser = f.owner.ID

	req := f.request(http.MethodPatch, "/api/v1/developer/keys/"+f.keyID.Hex(),
		map[string]interface{}{"name": "Mine now"},
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.UpdateDeveloperKeyHandler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUpdateDeveloperKey_RefusesARevokedKey(t *testing.T) {
	f := newConsoleFixture(t)
	key := f.keyDB.keys[f.keyID.Hex()]
	key.Status = models.DeveloperKeyRevoked
	f.keyDB.keys[f.keyID.Hex()] = key

	req := f.request(http.MethodPatch, "/api/v1/developer/keys/"+f.keyID.Hex(),
		map[string]interface{}{"name": "Back from the dead"},
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.UpdateDeveloperKeyHandler(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUpdateDeveloperKey_EmptyUpdateIsRefused(t *testing.T) {
	f := newConsoleFixture(t)
	req := f.request(http.MethodPatch, "/api/v1/developer/keys/"+f.keyID.Hex(),
		map[string]interface{}{}, map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.UpdateDeveloperKeyHandler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUpdateDeveloperKey_NoChangeIsNotAWrite(t *testing.T) {
	f := newConsoleFixture(t)
	req := f.request(http.MethodPatch, "/api/v1/developer/keys/"+f.keyID.Hex(),
		map[string]interface{}{"name": "Dispatch bridge"},
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.UpdateDeveloperKeyHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(f.keyDB.updates) != 0 {
		t.Errorf("a save that changes nothing should not write, got %v", f.keyDB.updates)
	}
	if !strings.Contains(rr.Body.String(), `"changed":false`) {
		t.Errorf("expected changed:false, got %s", rr.Body.String())
	}
}

// ---- several communities on one key ----

func TestAddKeyCommunity_LiveWhenYouCanManageIt(t *testing.T) {
	f := newConsoleFixture(t)
	community := f.addCommunity("My Server", true)

	req := f.request(http.MethodPost, "/api/v1/developer/keys/"+f.keyID.Hex()+"/communities",
		map[string]interface{}{"communityId": community.Hex()},
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.AddDeveloperKeyCommunityHandler(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(f.grantDB.grants) != 1 || f.grantDB.grants[0].Status != models.DeveloperKeyActive {
		t.Fatalf("expected one live grant, got %+v", f.grantDB.grants)
	}
	if f.grantDB.grants[0].ApprovedBy != f.owner.ID {
		t.Error("an owner approving their own community should be recorded as the approver")
	}
}

func TestAddKeyCommunity_PendingWhenYouAreOnlyAMember(t *testing.T) {
	f := newConsoleFixture(t)
	community := f.addCommunity("Someone Else's", false)
	f.joinCommunity(community)

	req := f.request(http.MethodPost, "/api/v1/developer/keys/"+f.keyID.Hex()+"/communities",
		map[string]interface{}{"communityId": community.Hex()},
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.AddDeveloperKeyCommunityHandler(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	// Adding a community to an existing key must not be an easier path than
	// binding one at creation, or it becomes the path people use.
	if f.grantDB.grants[0].Status != models.DeveloperKeyPending {
		t.Errorf("a plain member's grant must wait for approval, got %q", f.grantDB.grants[0].Status)
	}
	if f.grantDB.grants[0].ApprovedBy != "" {
		t.Error("a pending grant must not record an approver")
	}
}

func TestAddKeyCommunity_RefusedWhenYouAreNotAMember(t *testing.T) {
	f := newConsoleFixture(t)
	community := f.addCommunity("Strangers", false)

	req := f.request(http.MethodPost, "/api/v1/developer/keys/"+f.keyID.Hex()+"/communities",
		map[string]interface{}{"communityId": community.Hex()},
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.AddDeveloperKeyCommunityHandler(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(f.grantDB.grants) != 0 {
		t.Error("no grant should be written for a community you are not in")
	}
}

func TestAddKeyCommunity_SeveralCommunitiesOnOneKey(t *testing.T) {
	f := newConsoleFixture(t)
	for _, name := range []string{"Alpha", "Bravo", "Charlie"} {
		id := f.addCommunity(name, true)
		req := f.request(http.MethodPost, "/api/v1/developer/keys/"+f.keyID.Hex()+"/communities",
			map[string]interface{}{"communityId": id.Hex()},
			map[string]string{"keyId": f.keyID.Hex()})
		rr := httptest.NewRecorder()
		f.api.AddDeveloperKeyCommunityHandler(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("adding %s: expected 201, got %d: %s", name, rr.Code, rr.Body.String())
		}
	}
	if len(f.grantDB.grants) != 3 {
		t.Fatalf("expected three grants, got %d", len(f.grantDB.grants))
	}
	// One row per (key, community) and nothing on the key itself: the reason
	// this needed no migration.
	seen := map[string]bool{}
	for _, g := range f.grantDB.grants {
		if g.KeyID != f.keyID.Hex() {
			t.Errorf("grant %s is on the wrong key", g.ID.Hex())
		}
		if seen[g.CommunityID] {
			t.Errorf("duplicate grant for community %s", g.CommunityID)
		}
		seen[g.CommunityID] = true
	}
}

func TestAddKeyCommunity_AlreadyThereIsNotAnError(t *testing.T) {
	f := newConsoleFixture(t)
	community := f.addCommunity("My Server", true)
	f.liveGrant(community)

	req := f.request(http.MethodPost, "/api/v1/developer/keys/"+f.keyID.Hex()+"/communities",
		map[string]interface{}{"communityId": community.Hex()},
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.AddDeveloperKeyCommunityHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(f.grantDB.grants) != 1 {
		t.Errorf("asking for a grant you already have must not duplicate it, got %d", len(f.grantDB.grants))
	}
}

func TestAddKeyCommunity_RevivesARevokedGrantRatherThanDuplicating(t *testing.T) {
	f := newConsoleFixture(t)
	community := f.addCommunity("My Server", true)
	f.liveGrant(community)
	f.grantDB.grants[0].Status = models.DeveloperKeyRevoked

	req := f.request(http.MethodPost, "/api/v1/developer/keys/"+f.keyID.Hex()+"/communities",
		map[string]interface{}{"communityId": community.Hex()},
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.AddDeveloperKeyCommunityHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(f.grantDB.grants) != 1 {
		t.Fatalf("expected the existing row to be reused, got %d rows", len(f.grantDB.grants))
	}
	if f.grantDB.grants[0].Status != models.DeveloperKeyActive {
		t.Errorf("expected the grant to come back live, got %q", f.grantDB.grants[0].Status)
	}
}

func TestRemoveKeyCommunity_ImmediateAndUnconditional(t *testing.T) {
	f := newConsoleFixture(t)
	community := f.addCommunity("My Server", true)
	f.liveGrant(community)

	req := f.request(http.MethodDelete,
		"/api/v1/developer/keys/"+f.keyID.Hex()+"/communities/"+community.Hex(), nil,
		map[string]string{"keyId": f.keyID.Hex(), "communityId": community.Hex()})
	rr := httptest.NewRecorder()
	f.api.RemoveDeveloperKeyCommunityHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if f.grantDB.grants[0].Status != models.DeveloperKeyRevoked {
		t.Errorf("expected the grant revoked, got %q", f.grantDB.grants[0].Status)
	}
	// Revoked, not deleted: the usage rows and the audit trail point at this
	// pair, and a deleted row would take the record with it.
	if len(f.grantDB.grants) != 1 {
		t.Error("the grant row should survive its revocation")
	}
}

func TestRemoveKeyCommunity_LastOneIsStillAllowed(t *testing.T) {
	// Refusing this would leave a developer unable to take back access they no
	// longer want. The key is left permission-only and reported as inert.
	f := newConsoleFixture(t, models.ScopeCallsRead)
	community := f.addCommunity("My Server", true)
	f.liveGrant(community)

	req := f.request(http.MethodDelete,
		"/api/v1/developer/keys/"+f.keyID.Hex()+"/communities/"+community.Hex(), nil,
		map[string]string{"keyId": f.keyID.Hex(), "communityId": community.Hex()})
	rr := httptest.NewRecorder()
	f.api.RemoveDeveloperKeyCommunityHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"keyNowInert":true`) {
		t.Errorf("a key with nothing left to act on should say so, got %s", rr.Body.String())
	}
}

func TestRemoveKeyCommunity_ProvisioningKeyIsNotInert(t *testing.T) {
	f := newConsoleFixture(t, models.ScopeCommunitiesProvision)
	community := f.addCommunity("My Server", true)
	f.liveGrant(community)

	req := f.request(http.MethodDelete,
		"/api/v1/developer/keys/"+f.keyID.Hex()+"/communities/"+community.Hex(), nil,
		map[string]string{"keyId": f.keyID.Hex(), "communityId": community.Hex()})
	rr := httptest.NewRecorder()
	f.api.RemoveDeveloperKeyCommunityHandler(rr, req)

	if !strings.Contains(rr.Body.String(), `"keyNowInert":false`) {
		t.Errorf("a key that can make its own communities is not inert, got %s", rr.Body.String())
	}
}

func TestRemoveKeyCommunity_UnknownPairIs404(t *testing.T) {
	f := newConsoleFixture(t)
	other := primitive.NewObjectID()

	req := f.request(http.MethodDelete,
		"/api/v1/developer/keys/"+f.keyID.Hex()+"/communities/"+other.Hex(), nil,
		map[string]string{"keyId": f.keyID.Hex(), "communityId": other.Hex()})
	rr := httptest.NewRecorder()
	f.api.RemoveDeveloperKeyCommunityHandler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---- the account's usage ----

func TestGetDeveloperUsage_SumsKeysAndFillsEmptyDays(t *testing.T) {
	f := newConsoleFixture(t)
	today := time.Now().UTC().Format("2006-01-02")
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	f.usageDB.rows = []models.DeveloperAPIUsage{
		{KeyID: f.keyID.Hex(), Day: yesterday, Requests: 40, Rejected429: 2},
		{KeyID: f.keyID.Hex(), Day: today, Requests: 10, Rejected403: 1},
	}

	req := f.request(http.MethodGet, "/api/v1/developer/usage", nil, nil)
	rr := httptest.NewRecorder()
	f.api.GetDeveloperUsageHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Series []struct {
			Day      string `json:"day"`
			Requests int    `json:"requests"`
			Rejected int    `json:"rejected"`
		} `json:"series"`
		Keys []struct {
			Requests int `json:"requests"`
		} `json:"keys"`
		RequestsToday int    `json:"requestsToday"`
		RejectedToday int    `json:"rejectedToday"`
		PeakDay       int    `json:"peakDay"`
		Plan          string `json:"plan"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	// A quiet day is a zero, not a gap. A chart that skips empty days draws a
	// straight line through a quiet week, which reads as steady use.
	if len(out.Series) != 30 {
		t.Fatalf("expected 30 days of series, got %d", len(out.Series))
	}
	if out.Series[len(out.Series)-1].Day != today {
		t.Errorf("the series should end today, got %q", out.Series[len(out.Series)-1].Day)
	}
	if out.RequestsToday != 10 || out.RejectedToday != 1 {
		t.Errorf("today's totals are wrong: %+v", out)
	}
	if out.PeakDay != 40 {
		t.Errorf("expected a peak of 40, got %d", out.PeakDay)
	}
	if len(out.Keys) != 1 || out.Keys[0].Requests != 50 {
		t.Errorf("expected one key with 50 requests, got %+v", out.Keys)
	}
	// Premium, from the website subscription, not from anything on the account.
	if out.Plan != models.TierPremium {
		t.Errorf("expected the premium plan, got %q", out.Plan)
	}
}

func TestGetDeveloperUsage_RejectsAnUnsupportedRange(t *testing.T) {
	f := newConsoleFixture(t)
	req := f.request(http.MethodGet, "/api/v1/developer/usage", nil, nil)
	req.URL.RawQuery = fmt.Sprintf("userId=%s&days=365", f.owner.ID)
	rr := httptest.NewRecorder()
	f.api.GetDeveloperUsageHandler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestGetDeveloperUsage_RequiresAnActor(t *testing.T) {
	f := newConsoleFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/usage", nil)
	rr := httptest.NewRecorder()
	f.api.GetDeveloperUsageHandler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestGetDeveloperKey_ReturnsOneKeyWithItsCommunities(t *testing.T) {
	f := newConsoleFixture(t)
	community := f.addCommunity("My Server", true)
	f.liveGrant(community)

	req := f.request(http.MethodGet, "/api/v1/developer/keys/"+f.keyID.Hex(), nil,
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.GetDeveloperKeyHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "My Server") {
		t.Errorf("the community should be named, got %s", body)
	}
	// The scope catalog travels with the key, so the edit form needs one
	// request rather than two.
	if !strings.Contains(body, `"availableScopes"`) {
		t.Errorf("expected the scope catalog, got %s", body)
	}
	// A key response must never carry anything that could reconstruct the
	// secret.
	if strings.Contains(body, "keyHash") {
		t.Error("the key hash must not be in a response")
	}
}

func TestGetDeveloperKey_NotSomebodyElsesKey(t *testing.T) {
	f := newConsoleFixture(t)
	f.keyDB.denyUser = f.owner.ID
	req := f.request(http.MethodGet, "/api/v1/developer/keys/"+f.keyID.Hex(), nil,
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.GetDeveloperKeyHandler(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}
