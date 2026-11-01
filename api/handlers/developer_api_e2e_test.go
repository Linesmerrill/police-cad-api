package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// The flow Hark ran against production on 1 November, as a test.
//
// Two of those steps failed and one was wrong, and none of it was covered end
// to end: unit status was broken on every community a key created, a dispatch
// upsert silently cleared the assignment it had not been asked to change, and
// whoami reported an empty plan beside Premium limits. Each one was reachable
// only by driving the surface the way a mod drives it, which is what this file
// does: real handlers, real middleware, in-memory stores.
//
// The community store here keeps BSON documents rather than Go structs, so a
// members field that is null stays null and a $set beneath it fails the way
// MongoDB fails it. That is the whole reason the blocker existed, and with a
// Go map in its place the bug cannot be reproduced at all.

// ---- a community store that behaves like the real one ----

type e2eCommunityDB struct {
	databases.CommunityDatabase
	docs map[string]bson.M
}

func (f *e2eCommunityDB) seed(community models.Community) error {
	raw, err := bson.Marshal(community)
	if err != nil {
		return err
	}
	var doc bson.M
	if err := bson.Unmarshal(raw, &doc); err != nil {
		return err
	}
	if f.docs == nil {
		f.docs = map[string]bson.M{}
	}
	f.docs[community.ID.Hex()] = doc
	return nil
}

func (f *e2eCommunityDB) InsertOne(_ context.Context, community models.Community, _ ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	if err := f.seed(community); err != nil {
		return nil, err
	}
	return fakeInsertResult{id: community.ID}, nil
}

func (f *e2eCommunityDB) FindOne(_ context.Context, filter interface{}) (*models.Community, error) {
	m, _ := filter.(bson.M)
	oid, ok := devFilterObjectID(m["_id"])
	if !ok {
		return nil, mongo.ErrNoDocuments
	}
	doc, found := f.docs[oid.Hex()]
	if !found {
		return nil, mongo.ErrNoDocuments
	}
	raw, err := bson.Marshal(doc)
	if err != nil {
		return nil, err
	}
	out := &models.Community{}
	if err := bson.Unmarshal(raw, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (f *e2eCommunityDB) FindOneIncludingPending(ctx context.Context, filter interface{}) (*models.Community, error) {
	return f.FindOne(ctx, filter)
}

func (f *e2eCommunityDB) CountDocuments(_ context.Context, _ interface{}, _ ...*options.CountOptions) (int64, error) {
	return int64(len(f.docs)), nil
}

// UpdateOne applies $set, dotted paths included, and refuses to create a field
// underneath a null — which is what MongoDB does and what broke unit status.
func (f *e2eCommunityDB) UpdateOne(_ context.Context, filter, update interface{}, _ ...*options.UpdateOptions) error {
	fm, _ := filter.(bson.M)
	um, _ := update.(bson.M)

	oid, ok := devFilterObjectID(fm["_id"])
	if !ok {
		return nil
	}
	doc, found := f.docs[oid.Hex()]
	if !found {
		return nil
	}
	if !e2eFilterMatches(doc, fm) {
		// No document matched. Not an error in the driver, and the heal step
		// relies on that: once a community is healed its filter stops matching.
		return nil
	}

	set, _ := um["$set"].(bson.M)
	for path, value := range set {
		if err := e2eSetPath(doc, strings.Split(path, "."), value); err != nil {
			return err
		}
	}
	return nil
}

// e2eFilterMatches handles the small filter vocabulary these tests use: the
// $or / $exists / null form the heal step is built on.
func e2eFilterMatches(doc bson.M, filter bson.M) bool {
	ors, ok := filter["$or"].([]bson.M)
	if !ok {
		return true
	}
	for _, clause := range ors {
		for path, want := range clause {
			got, present := e2eLookup(doc, strings.Split(path, "."))
			if cond, isCond := want.(bson.M); isCond {
				if exists, has := cond["$exists"].(bool); has && exists == present {
					return true
				}
				continue
			}
			if want == nil && present && got == nil {
				return true
			}
		}
	}
	return false
}

func e2eLookup(doc bson.M, path []string) (interface{}, bool) {
	var cur interface{} = doc
	for _, part := range path {
		m, ok := cur.(bson.M)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// e2eSetPath walks a dotted path, creating intermediate documents, and errors
// when a segment on the way is null — the real failure mode.
func e2eSetPath(doc bson.M, path []string, value interface{}) error {
	cur := doc
	for i, part := range path[:len(path)-1] {
		next, present := cur[part]
		if present && next == nil {
			return &e2eNullPathError{path: strings.Join(path[:i+1], ".")}
		}
		child, ok := next.(bson.M)
		if !ok {
			child = bson.M{}
			cur[part] = child
		}
		cur = child
	}
	cur[path[len(path)-1]] = value
	return nil
}

type e2eNullPathError struct{ path string }

func (e *e2eNullPathError) Error() string {
	return "cannot create field in element: " + e.path + " is null"
}

// Find completes the grant fake for the whoami path, which lists every grant a
// key holds rather than looking one up.
func (f *fakeDevGrantDB) Find(_ context.Context, filter interface{}, _ ...*options.FindOptions) (*databases.MongoCursor, error) {
	f.seenFilters = append(f.seenFilters, filter)
	m, _ := filter.(bson.M)
	wantKey, hasKey := devFilterString(m["keyId"])

	docs := []interface{}{}
	for _, g := range f.grants {
		if hasKey && g.KeyID != wantKey {
			continue
		}
		docs = append(docs, g)
	}
	cur, err := databases.NewMongoCursorFromDocuments(docs)
	if err != nil {
		return nil, err
	}
	return &cur, nil
}

// InsertOne completes the grant fake for the create-community path, which
// grants the key access to what it just made.
func (f *fakeDevGrantDB) InsertOne(_ context.Context, grant models.DeveloperCommunityGrant) (databases.InsertOneResultHelper, error) {
	f.grants = append(f.grants, grant)
	return fakeInsertResult{id: grant.ID}, nil
}

// ---- the stack ----

// e2eFixture is the dispatch fixture with the document-backed community store
// in place of the struct-backed one, every scope Hark's key held, and a
// ten-code to set a status to.
type e2eFixture struct {
	*dispatchFixture
	communityDB *e2eCommunityDB
	tenCodeID   primitive.ObjectID
	// ownerPlan is the plan on the key owner's user record, which is where
	// whoami's limits come from.
	ownerPlan string
}

func newE2EFixture(t *testing.T) *e2eFixture {
	t.Helper()
	base := newDispatchFixture(t)
	base.keyDB.byHash = upgradeKeyScopes(base.keyDB.byHash, allDevScopeNames()...)

	// The community, with a ten-code, in a store that marshals like Mongo.
	tenCodeID := primitive.NewObjectID()
	community := base.community
	community.Details.TenCodes = []models.TenCodes{{ID: tenCodeID, Code: "10-8", Description: "In service"}}
	community.Details.Members = map[string]models.MemberDetail{}
	base.community = community

	cdb := &e2eCommunityDB{docs: map[string]bson.M{}}
	if err := cdb.seed(community); err != nil {
		t.Fatalf("seeding the community: %v", err)
	}
	base.api.CDB = cdb

	// The key's owner, on Premium, so whoami has a plan to report. The member
	// fixture's own users are players; this one is the developer.
	base.keyDB.byHash = setKeyOwner(base.keyDB.byHash, base.ownerID)
	owner := models.User{ID: base.ownerID}
	owner.Details.Username = "owner"
	owner.Details.Subscription.Plan = models.TierPremium
	owner.Details.Subscription.Active = true
	base.userDB.mu.Lock()
	base.userDB.users[base.ownerID] = owner
	base.userDB.mu.Unlock()

	return &e2eFixture{
		dispatchFixture: base,
		communityDB:     cdb,
		tenCodeID:       tenCodeID,
		ownerPlan:       models.TierPremium,
	}
}

// allDevScopeNames is every scope we issue, which is what Hark's key held.
func allDevScopeNames() []string {
	all := models.DeveloperScopes()
	names := make([]string, 0, len(all))
	for _, s := range all {
		names = append(names, s.Scope)
	}
	return names
}

// fivemRef is the reference form for a player seeded by seedPlayer, which
// derives each identity's licence from the player's name.
func fivemRef(name string) string {
	return "fivem:" + hex.EncodeToString([]byte(name))
}

// ---- whoami ----

// Hark saw plan "" beside Premium-level limits. The limits were right; the
// plan was computed to derive them and then dropped on the floor.
func TestE2E_WhoamiReportsScopesLimitsAndPlan(t *testing.T) {
	f := newE2EFixture(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	f.api.RequireDeveloperAPIKey(http.HandlerFunc(f.api.DevWhoamiHandler)).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var out struct {
		Key struct {
			Scopes []string `json:"scopes"`
		} `json:"key"`
		Plan        string                 `json:"plan"`
		Limits      models.DeveloperLimits `json:"limits"`
		Communities []devCommunitySummary  `json:"communities"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if out.Plan == "" {
		t.Error("whoami reported an empty plan; it must name the tier the limits came from")
	}
	// The plan and the limits have to agree. Disagreeing is what looked wrong
	// from outside, whichever of the two was actually at fault.
	if out.Limits.RequestsPerMinute == 0 || out.Limits.RequestsPerDay == 0 {
		t.Errorf("whoami reported no limits: %+v", out.Limits)
	}
	if len(out.Key.Scopes) != len(allDevScopeNames()) {
		t.Errorf("whoami listed %d scopes, want all %d",
			len(out.Key.Scopes), len(allDevScopeNames()))
	}
	if len(out.Communities) != 1 || out.Communities[0].ID != f.community.ID.Hex() {
		t.Errorf("whoami should list the granted community, got %+v", out.Communities)
	}

	// The headers a client is told to pace itself by.
	for _, h := range []string{"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if rr.Header().Get(h) == "" {
			t.Errorf("missing %s header", h)
		}
	}
}

// A revoked key stops working at once, with no grace period.
func TestE2E_RevokedKeyIsRefused(t *testing.T) {
	f := newE2EFixture(t)
	for hash, key := range f.keyDB.byHash {
		key.Status = models.DeveloperKeyRevoked
		f.keyDB.byHash[hash] = key
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	f.api.RequireDeveloperAPIKey(http.HandlerFunc(f.api.DevWhoamiHandler)).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("a revoked key must be refused, got %d: %s", rr.Code, rr.Body.String())
	}
}

// A key may only reach a community someone granted it. This passed live and is
// the boundary that matters most, so it stays covered.
func TestE2E_AnotherCommunityIsRefused(t *testing.T) {
	f := newE2EFixture(t)
	other := primitive.NewObjectID()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/communities/"+other.Hex()+"/units", nil)
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	req = mux.SetURLVars(req, map[string]string{"communityId": other.Hex()})
	f.api.RequireDeveloperAPIKey(
		f.api.RequireScope(models.ScopeUnitsRead, f.api.DevListUnitsHandler),
	).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for an ungranted community, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "community_not_granted") {
		t.Errorf("expected community_not_granted, got %s", rr.Body.String())
	}
}

// ---- creating a community with the key ----

// A community a key makes starts private, belongs to the key's owner, and the
// key can see it straight afterwards without anyone approving anything.
func TestE2E_CreateCommunityStartsPrivateAndShowsInWhoami(t *testing.T) {
	f := newE2EFixture(t)

	created := f.createCommunity(t, `{"name":"Hark E2E","description":"end to end"}`)

	if created.Visibility != "private" {
		t.Errorf("a key-created community must start private, got %q", created.Visibility)
	}
	if created.OwnerID != f.ownerID {
		t.Errorf("ownerId = %q, want the key's owner %q", created.OwnerID, f.ownerID)
	}

	// The grant is written as part of creation, so whoami shows it at once.
	whoami := httptest.NewRecorder()
	wreq := httptest.NewRequest(http.MethodGet, "/api/v1/dev/whoami", nil)
	wreq.Header.Set("Authorization", "Bearer "+f.plaintext)
	f.api.RequireDeveloperAPIKey(http.HandlerFunc(f.api.DevWhoamiHandler)).ServeHTTP(whoami, wreq)

	var out struct {
		Communities []devCommunitySummary `json:"communities"`
	}
	if err := json.Unmarshal(whoami.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding whoami: %v", err)
	}
	var found *devCommunitySummary
	for i := range out.Communities {
		if out.Communities[i].ID == created.ID {
			found = &out.Communities[i]
		}
	}
	if found == nil {
		t.Fatalf("the new community is missing from whoami: %+v", out.Communities)
	}
	if found.GrantSource != models.DeveloperGrantCreatedByKey {
		t.Errorf("grantSource = %q, want %q", found.GrantSource, models.DeveloperGrantCreatedByKey)
	}
	if found.GrantStatus != models.DeveloperKeyActive {
		t.Errorf("a key's own community needs no approval; grantStatus = %q", found.GrantStatus)
	}
}

// A community a key creates must be born with a members map. Without it the
// field marshals to BSON null and every status write on that community fails.
func TestE2E_CreateCommunityWritesAMembersMap(t *testing.T) {
	f := newE2EFixture(t)
	created := f.createCommunity(t, `{"name":"Members Map"}`)

	doc := f.communityDB.docs[created.ID]
	details, _ := doc["community"].(bson.M)
	if details == nil {
		t.Fatal("the community was not stored")
	}
	if _, present := details["members"]; !present {
		t.Fatal("members was not written at all")
	}
	if details["members"] == nil {
		t.Fatal("members was stored as null; a $set beneath it would fail")
	}
}

type e2eCreatedCommunity struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	OwnerID    string `json:"ownerId"`
	Visibility string `json:"visibility"`
}

// createCommunity runs the provisioning endpoint and returns what it answered.
func (f *e2eFixture) createCommunity(t *testing.T, body string) e2eCreatedCommunity {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dev/communities", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	f.api.RequireDeveloperAPIKey(
		f.api.RequireScope(models.ScopeCommunitiesProvision, f.api.DevCreateCommunityHandler),
	).ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("creating a community: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Community e2eCreatedCommunity `json:"community"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding the created community: %v", err)
	}
	return out.Community
}

// ---- BUG 1: unit status on a key-created community ----

// The blocker Hark hit. Every status write on a community the key had created
// came back as a failure, and only on those communities — so the community
// here is made through the provisioning endpoint rather than seeded by hand.
func TestE2E_SetUnitStatusOnAKeyCreatedCommunity(t *testing.T) {
	f := newE2EFixture(t)

	created := f.createCommunity(t, `{"name":"Hark Test Server"}`)
	community, err := f.communityDB.FindOne(context.Background(),
		bson.M{"_id": devEq(objectIDOrZero(created.ID))})
	if err != nil {
		t.Fatalf("reading back the new community: %v", err)
	}
	if len(community.Details.TenCodes) == 0 {
		t.Fatal("a provisioned community should carry the default ten-codes")
	}
	tenCode := community.Details.TenCodes[0].ID.Hex()

	// The owner is its only member, which is who a mod sets a status for first.
	rr := f.runOn(t, created.ID, http.MethodPut,
		"/units/"+fivemRef("owner")+"/status",
		`{"tenCodeID":"`+tenCode+`","location":"Route 68"}`,
		map[string]string{"playerRef": fivemRef("owner")},
		f.api.RequireScope(models.ScopeUnitsWrite, f.api.DevSetUnitStatusHandler))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Unit map[string]interface{} `json:"unit"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if ok, _ := out.Unit["ok"].(bool); !ok {
		t.Fatalf("the status write failed: %v", out.Unit)
	}
}

// runOn is run() against a community other than the fixture's own, which is
// how a test reaches one the key created during the test.
func (f *e2eFixture) runOn(t *testing.T, communityID, method, suffix, body string, extraVars map[string]string, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/dev/communities/" + communityID + suffix
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	vars := map[string]string{"communityId": communityID}
	for k, v := range extraVars {
		vars[k] = v
	}
	req = mux.SetURLVars(req, vars)
	rr := httptest.NewRecorder()
	f.api.RequireDeveloperAPIKey(h).ServeHTTP(rr, req)
	return rr
}

// A community created before the fix still holds null, and a migration would
// only cover the ones that exist today. The write path repairs it in passing,
// so Hark's own test community starts working with nothing run by hand.
func TestE2E_SetUnitStatusHealsANullMembersMap(t *testing.T) {
	f := newE2EFixture(t)

	// Put the community back in the broken state, exactly as provisioning left
	// it before the fix: members present and null.
	details := f.communityDetails(t)
	details["members"] = nil

	rr := f.setStatus(t, fivemRef("owner"), `{"tenCodeID":"`+f.tenCodeID.Hex()+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 after the heal, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Unit map[string]interface{} `json:"unit"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if ok, _ := out.Unit["ok"].(bool); !ok {
		t.Fatalf("the status write failed on a healed community: %v", out.Unit)
	}
	if details["members"] == nil {
		t.Error("members is still null; the heal did not run")
	}
}

// Proof the store still models the failure. Without this the heal test above
// could pass for the wrong reason: a fake that never breaks proves nothing.
func TestE2E_TheStoreStillRejectsASetBeneathNull(t *testing.T) {
	f := newE2EFixture(t)
	details := f.communityDetails(t)
	details["members"] = nil

	err := f.api.CDB.UpdateOne(context.Background(),
		bson.M{"_id": devEq(f.community.ID)},
		bson.M{"$set": bson.M{"community.members." + f.ownerID + ".tenCodeID": "x"}})
	if err == nil {
		t.Fatal("expected the store to refuse a $set beneath null, as Mongo does")
	}
}

// The heal runs on every status write, so it must leave a healthy community
// alone rather than emptying its roster.
func TestE2E_HealLeavesAnExistingRosterAlone(t *testing.T) {
	f := newE2EFixture(t)

	details := f.communityDetails(t)
	details["members"] = bson.M{f.plainID: bson.M{"callSign": "1-ADAM-12"}}

	if err := f.api.ensureMembersMap(context.Background(), f.community.ID); err != nil {
		t.Fatalf("heal: %v", err)
	}

	members, _ := details["members"].(bson.M)
	if _, ok := members[f.plainID]; !ok {
		t.Error("the heal removed an existing member; it must only fill in a null")
	}
}

// communityDetails returns the stored community subdocument, so a test can put
// it into a state the Go model cannot express.
func (f *e2eFixture) communityDetails(t *testing.T) bson.M {
	t.Helper()
	doc, ok := f.communityDB.docs[f.community.ID.Hex()]
	if !ok {
		t.Fatal("the fixture community is not in the store")
	}
	details, ok := doc["community"].(bson.M)
	if !ok {
		t.Fatal("the stored community has no details subdocument")
	}
	return details
}

// ---- unit status: applied counts and per-unit errors ----

// A game server cannot usefully retry a whole tick, so one unknown player must
// not discard the rest of the batch. Each entry reports its own outcome.
func TestE2E_BulkUnitStatusIsPartial(t *testing.T) {
	f := newE2EFixture(t)

	body := `{"units":[
		{"player":"` + fivemRef("owner") + `","tenCodeID":"` + f.tenCodeID.Hex() + `"},
		{"player":"` + fivemRef("member") + `","tenCodeID":"` + f.tenCodeID.Hex() + `"},
		{"player":"fivem:ffffffffffffffff","tenCodeID":"` + f.tenCodeID.Hex() + `"}
	]}`

	rr := f.run(t, http.MethodPut, "/units/status/bulk", body, nil,
		f.api.RequireScope(models.ScopeUnitsWrite, f.api.DevBulkSetUnitStatusHandler))
	if rr.Code != http.StatusOK {
		t.Fatalf("a partly-good batch is a 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var out struct {
		Units   []map[string]interface{} `json:"units"`
		Applied int                      `json:"applied"`
		Failed  int                      `json:"failed"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if out.Applied != 2 || out.Failed != 1 {
		t.Errorf("applied/failed = %d/%d, want 2/1: %+v", out.Applied, out.Failed, out.Units)
	}
	if len(out.Units) != 3 {
		t.Fatalf("every entry needs its own result, got %d", len(out.Units))
	}
	// The unknown player is named in its own entry, so a mod can log which
	// licence it was rather than retrying the tick blind.
	last := out.Units[2]
	if ok, _ := last["ok"].(bool); ok {
		t.Errorf("the unknown player should have failed: %v", last)
	}
	if last["error"] == nil || last["message"] == nil {
		t.Errorf("a failed unit needs an error and a message: %v", last)
	}
}

// A single-unit call is not a batch: a failure is the whole request, and
// burying it in a 200 would let a mod treat a no-op as a success.
func TestE2E_SingleUnitStatusFailureIsNotA200(t *testing.T) {
	f := newE2EFixture(t)

	rr := f.setStatus(t, "fivem:ffffffffffffffff", `{"tenCodeID":"`+f.tenCodeID.Hex()+`"}`)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for an unknown player, got %d: %s", rr.Code, rr.Body.String())
	}
}

// A ten-code has to be one of this community's own, or the status lands on the
// board in a state no client can render and nobody can clear.
func TestE2E_UnitStatusRejectsAForeignTenCode(t *testing.T) {
	f := newE2EFixture(t)

	rr := f.setStatus(t, fivemRef("owner"), `{"tenCodeID":"`+primitive.NewObjectID().Hex()+`"}`)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "ten_code_not_found") {
		t.Errorf("expected ten_code_not_found, got %s", rr.Body.String())
	}
}

// setStatus runs the single-unit status endpoint for one player reference.
func (f *e2eFixture) setStatus(t *testing.T, playerRef, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.run(t, http.MethodPut, "/units/"+playerRef+"/status", body,
		map[string]string{"playerRef": playerRef},
		f.api.RequireScope(models.ScopeUnitsWrite, f.api.DevSetUnitStatusHandler))
}

// ---- players and members ----

// A mod calls this on every join, so it has to be safe to call forever: the
// first call creates the account, every later one returns the same player.
func TestE2E_PlayerUpsertIsIdempotent(t *testing.T) {
	f := newE2EFixture(t)
	body := `{"provider":"fivem","identifier":"a1b2c3d4e5f60718","username":"Hark"}`

	rr1, first := f.upsertPlayer(t, body)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("first upsert: expected 201, got %d: %s", rr1.Code, rr1.Body.String())
	}
	rr2, second := f.upsertPlayer(t, body)
	if rr2.Code != http.StatusOK {
		t.Fatalf("second upsert: expected 200, got %d: %s", rr2.Code, rr2.Body.String())
	}
	if first.PlayerID == "" || first.PlayerID != second.PlayerID {
		t.Errorf("the same identifier must map to one player: %q then %q",
			first.PlayerID, second.PlayerID)
	}
}

// A FiveM licence is hexadecimal. Anything else is refused at the door rather
// than stored, so a mod's typo cannot become a player nobody can find again.
func TestE2E_InvalidFiveMLicenceIsA400(t *testing.T) {
	f := newE2EFixture(t)

	for name, identifier := range map[string]string{
		"not hexadecimal": "abc-xyz",
		"punctuation":     "a1b2!c3d4",
		"empty":           "   ",
	} {
		t.Run(name, func(t *testing.T) {
			rr, _ := f.upsertPlayer(t,
				`{"provider":"fivem","identifier":"`+identifier+`","username":"Nope"}`)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

// upsertPlayer runs the player upsert endpoint.
func (f *e2eFixture) upsertPlayer(t *testing.T, body string) (*httptest.ResponseRecorder, devPlayerResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dev/players", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	rr := httptest.NewRecorder()
	f.api.RequireDeveloperAPIKey(
		f.api.RequireScope(models.ScopePlayersProvision, f.api.DevUpsertPlayerHandler),
	).ServeHTTP(rr, req)

	var out struct {
		Player devPlayerResponse `json:"player"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr, out.Player
}

// Adding someone who is already in is not an error. A mod replaying its queue
// after a reconnect would otherwise fill a server's logs with failures for
// work that was already done.
func TestE2E_AddMemberIsIdempotent(t *testing.T) {
	f := newE2EFixture(t)

	body := `{"actingPlayer":"` + fivemRef("owner") + `","player":"` + fivemRef("member") + `"}`
	first := f.addMember(t, body)
	if first.Code >= 400 {
		t.Fatalf("adding an existing member should be accepted, got %d: %s",
			first.Code, first.Body.String())
	}

	again := f.addMember(t, body)
	if again.Code != first.Code {
		t.Errorf("a repeat add answered %d then %d; it must be stable",
			first.Code, again.Code)
	}
}

// Adding a player the CAD has never seen provisions an account for them, so a
// mod can invite someone straight from the game without them signing up first.
func TestE2E_AddMemberProvisionsANewPlayer(t *testing.T) {
	f := newE2EFixture(t)

	before := len(f.identityDB.rows)
	rr := f.addMember(t, `{"actingPlayer":"`+fivemRef("owner")+
		`","player":"fivem:0f0e0d0c0b0a0908","username":"Newcomer"}`)
	if rr.Code >= 400 {
		t.Fatalf("expected the player to be provisioned, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(f.identityDB.rows) <= before {
		t.Error("no identity was written for the new player")
	}
}

// ---- BUG 2: dispatch upsert field semantics ----

// Hark's second bug. A follow-up upsert that sent only externalId, title and
// priority came back with assignedUnits emptied: the update wrote every field
// in the struct, so a field the caller never mentioned was written as its zero
// value. Omitted now means "leave it alone".
func TestE2E_DispatchUpsertLeavesOmittedFieldsAlone(t *testing.T) {
	f := newE2EFixture(t)
	unit := fivemRef("member")

	rr, call, created := f.upsert(t, `{"externalId":"CAD-1","source":"SixPD","title":"Traffic stop",`+
		`"location":"Route 68","priority":"routine","assignedUnits":["`+unit+`"]}`)
	if rr.Code != http.StatusCreated || !created {
		t.Fatalf("create: expected 201 created, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(call.AssignedUnits) != 1 {
		t.Fatalf("the unit was not assigned: %+v", call.AssignedUnits)
	}

	// The follow-up Hark sent: a title and a priority, nothing else.
	rr2, updated, created2 := f.upsert(t,
		`{"externalId":"CAD-1","source":"SixPD","title":"Traffic stop - backup","priority":"high"}`)
	if rr2.Code != http.StatusOK || created2 {
		t.Fatalf("update: expected 200 not-created, got %d: %s", rr2.Code, rr2.Body.String())
	}

	if len(updated.AssignedUnits) != 1 {
		t.Errorf("assignedUnits was cleared by an update that never mentioned it: %+v",
			updated.AssignedUnits)
	}
	if updated.Location != "Route 68" {
		t.Errorf("location was cleared by an update that never mentioned it: %q", updated.Location)
	}
	if updated.Title != "Traffic stop - backup" {
		t.Errorf("title = %q, want the updated one", updated.Title)
	}
}

// Clearing has to stay possible, so an explicit empty list is not the same as
// an absent one. Without the distinction a mod could never unassign a call.
func TestE2E_DispatchUpsertExplicitEmptyClears(t *testing.T) {
	f := newE2EFixture(t)
	unit := fivemRef("member")

	if rr, _, _ := f.upsert(t, `{"externalId":"CAD-2","source":"SixPD","title":"Shots fired",`+
		`"assignedUnits":["`+unit+`"]}`); rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d: %s", rr.Code, rr.Body.String())
	}

	rr, updated, _ := f.upsert(t,
		`{"externalId":"CAD-2","source":"SixPD","title":"Shots fired","assignedUnits":[]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: got %d: %s", rr.Code, rr.Body.String())
	}
	if len(updated.AssignedUnits) != 0 {
		t.Errorf("an explicit [] must clear the assignment, got %+v", updated.AssignedUnits)
	}
}

// Description had the same latent flaw, found while fixing the assignment: it
// was written on every update, so an upsert that did not mention it blanked
// it. Omitted leaves it; an explicit empty string clears it.
func TestE2E_DispatchUpsertDescriptionFollowsTheSameRule(t *testing.T) {
	f := newE2EFixture(t)

	if rr, _, _ := f.upsert(t, `{"externalId":"CAD-6","source":"SixPD","title":"Alarm",`+
		`"description":"rear door"}`); rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d: %s", rr.Code, rr.Body.String())
	}

	_, kept, _ := f.upsert(t, `{"externalId":"CAD-6","source":"SixPD","title":"Alarm - confirmed"}`)
	if kept.Description != "rear door" {
		t.Errorf("description was cleared by an update that never mentioned it: %q",
			kept.Description)
	}

	_, cleared, _ := f.upsert(t,
		`{"externalId":"CAD-6","source":"SixPD","title":"Alarm - confirmed","description":""}`)
	if cleared.Description != "" {
		t.Errorf("an explicit empty description must clear it, got %q", cleared.Description)
	}
}

// The same external id twice is one call, updated. This is what makes a
// reconnect and a queue replay safe.
func TestE2E_DispatchUpsertIsIdempotentOnExternalID(t *testing.T) {
	f := newE2EFixture(t)
	body := `{"externalId":"CAD-3","source":"SixPD","title":"Welfare check","priority":"routine"}`

	rr1, call1, created1 := f.upsert(t, body)
	if rr1.Code != http.StatusCreated || !created1 {
		t.Fatalf("first: expected 201 created, got %d: %s", rr1.Code, rr1.Body.String())
	}
	rr2, call2, created2 := f.upsert(t, body)
	if rr2.Code != http.StatusOK || created2 {
		t.Fatalf("second: expected 200 not-created, got %d: %s", rr2.Code, rr2.Body.String())
	}
	if call1.ID != call2.ID {
		t.Errorf("one external id must mean one call: %q then %q", call1.ID, call2.ID)
	}
	if f.callDB.inserts != 1 {
		t.Errorf("inserted %d calls, want 1", f.callDB.inserts)
	}
}

// A mod reads back by its own id, not ours, so it never has to store ours.
func TestE2E_CallReadbackByExternalID(t *testing.T) {
	f := newE2EFixture(t)
	if rr, _, _ := f.upsert(t,
		`{"externalId":"CAD-4","source":"SixPD","title":"Pursuit","location":"I-10"}`); rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d: %s", rr.Code, rr.Body.String())
	}

	// source scopes the id space, so a read has to name the same one the write
	// used. It defaults to the key's name, not to whatever was last written.
	rr := f.run(t, http.MethodGet, "/dispatch/calls/CAD-4?source=SixPD", "",
		map[string]string{"externalId": "CAD-4"},
		f.api.RequireScope(models.ScopeCallsRead, f.api.DevGetCallByExternalIDHandler))
	if rr.Code != http.StatusOK {
		t.Fatalf("readback: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Call devCallView `json:"call"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if out.Call.Title != "Pursuit" || out.Call.Location != "I-10" {
		t.Errorf("read back the wrong call: %+v", out.Call)
	}
}

// Closing a call that is already closed is a success, not an error. A mod that
// loses its reply and retries must not have to tell the two apart.
func TestE2E_CloseCallTwiceIsNotAnError(t *testing.T) {
	f := newE2EFixture(t)
	if rr, _, _ := f.upsert(t,
		`{"externalId":"CAD-5","source":"SixPD","title":"Noise complaint"}`); rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d: %s", rr.Code, rr.Body.String())
	}

	for attempt := 1; attempt <= 2; attempt++ {
		rr := f.run(t, http.MethodPost, "/dispatch/calls/CAD-5/close",
			`{"source":"SixPD","disposition":"cleared"}`, map[string]string{"externalId": "CAD-5"},
			f.api.RequireScope(models.ScopeCallsWrite, f.api.DevCloseCallByExternalIDHandler))
		if rr.Code != http.StatusOK {
			t.Fatalf("close attempt %d: expected 200, got %d: %s", attempt, rr.Code, rr.Body.String())
		}
		var out struct {
			Call   devCallView `json:"call"`
			Closed bool        `json:"closed"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding attempt %d: %v", attempt, err)
		}
		// The first close did the work; the second reports that nothing
		// changed. Either way the call reads as closed, so a mod that lost its
		// reply and retried does not have to tell the two apart.
		if wantClosed := attempt == 1; out.Closed != wantClosed {
			t.Errorf("attempt %d: closed = %v, want %v", attempt, out.Closed, wantClosed)
		}
		if out.Call.Status != "closed" {
			t.Errorf("attempt %d: status = %q, want closed", attempt, out.Call.Status)
		}
	}
}
