package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// FindOneAndUpdate is the write the member-department path depends on, so this
// fake implements what that path actually needs: a filter that reaches into an
// array of subdocuments by their real field names, $set through arrayFilters,
// $addToSet through the positional operator, and mongo.ErrNoDocuments when
// nothing matched.
//
// That last return is the whole point of implementing it here. The handler
// decides between replacing a member's row and appending one by whether this
// matched, and the member fixture's fake is simply *told* whether to match by a
// flag. So a filter naming "community.departments.id", a field models.Department
// does not have (its bson tag is "_id"), matched nothing in production, the
// append matched nothing too, and the endpoint still answered 200 with the
// player in no department at all. A fake that resolves these paths by their
// real names cannot hide that.
func (f *e2eCommunityDB) FindOneAndUpdate(_ context.Context, filter, update interface{}, opts ...*options.FindOneAndUpdateOptions) (*models.Community, error) {
	fm, _ := filter.(bson.M)
	um, _ := update.(bson.M)

	oid, ok := devFilterObjectID(fm["_id"])
	if !ok {
		return nil, mongo.ErrNoDocuments
	}
	doc, found := f.docs[oid.Hex()]
	if !found {
		return nil, mongo.ErrNoDocuments
	}
	if !e2eFilterMatches(doc, fm) || !e2eArrayFiltersMatch(doc, fm) {
		return nil, mongo.ErrNoDocuments
	}

	dept, deptFound := e2eDeptFromFilter(doc, fm)

	if set, has := um["$set"].(bson.M); has {
		for path, value := range set {
			parts := strings.Split(path, ".")
			// community.departments.$[dept].members.$[member].<field>
			if len(parts) == 6 && parts[2] == "$[dept]" && parts[4] == "$[member]" {
				if !deptFound {
					return nil, mongo.ErrNoDocuments
				}
				member, memberFound := e2eMemberByUserID(dept, e2eWantedMemberUserID(opts...))
				if !memberFound {
					return nil, mongo.ErrNoDocuments
				}
				member[parts[5]] = value
				continue
			}
			if err := e2eSetPath(doc, parts, value); err != nil {
				return nil, err
			}
		}
	}

	if add, has := um["$addToSet"].(bson.M); has {
		for path, value := range add {
			// community.departments.$.members
			if path != "community.departments.$.members" {
				return nil, mongo.ErrNoDocuments
			}
			if !deptFound {
				return nil, mongo.ErrNoDocuments
			}
			raw, err := bson.Marshal(value)
			if err != nil {
				return nil, err
			}
			entry := bson.M{}
			if err := bson.Unmarshal(raw, &entry); err != nil {
				return nil, err
			}
			existing, _ := dept["members"].(bson.A)
			dept["members"] = append(existing, entry)
		}
	}

	return f.FindOne(context.Background(), bson.M{"_id": oid})
}

// e2eArrayFiltersMatch resolves the dotted paths that cross an array, by the
// field names a real document has.
func e2eArrayFiltersMatch(doc bson.M, filter bson.M) bool {
	if want, has := filter["community.departments._id"]; has {
		if _, found := e2eDeptByID(doc, want); !found {
			return false
		}
	}
	if want, has := filter["community.departments.members.userID"]; has {
		wanted, _ := devFilterString(want)
		anyone := false
		for _, dept := range e2eDepartments(doc) {
			if _, found := e2eMemberByUserID(dept, wanted); found {
				anyone = true
				break
			}
		}
		if !anyone {
			return false
		}
	}
	return true
}

// e2eDepartments returns the community's departments as documents.
func e2eDepartments(doc bson.M) []bson.M {
	community, _ := doc["community"].(bson.M)
	raw, _ := community["departments"].(bson.A)
	out := make([]bson.M, 0, len(raw))
	for _, entry := range raw {
		if m, ok := entry.(bson.M); ok {
			out = append(out, m)
		}
	}
	return out
}

// e2eDeptByID finds a department by its real "_id", which is the thing the bug
// turned on.
func e2eDeptByID(doc bson.M, want interface{}) (bson.M, bool) {
	wanted, ok := devFilterObjectID(want)
	if !ok {
		return nil, false
	}
	for _, dept := range e2eDepartments(doc) {
		if id, isID := dept["_id"].(primitive.ObjectID); isID && id == wanted {
			return dept, true
		}
	}
	return nil, false
}

func e2eDeptFromFilter(doc bson.M, filter bson.M) (bson.M, bool) {
	want, has := filter["community.departments._id"]
	if !has {
		return nil, false
	}
	return e2eDeptByID(doc, want)
}

func e2eMemberByUserID(dept bson.M, userID string) (bson.M, bool) {
	if userID == "" {
		return nil, false
	}
	raw, _ := dept["members"].(bson.A)
	for _, entry := range raw {
		m, ok := entry.(bson.M)
		if !ok {
			continue
		}
		if id, isStr := m["userID"].(string); isStr && id == userID {
			return m, true
		}
	}
	return nil, false
}

// e2eWantedMemberUserID reads the member the arrayFilters single out, by the
// real "member.userID" key the handler passes.
func e2eWantedMemberUserID(opts ...*options.FindOneAndUpdateOptions) string {
	for _, opt := range opts {
		if opt == nil || opt.ArrayFilters == nil {
			continue
		}
		for _, raw := range opt.ArrayFilters.Filters {
			m, ok := raw.(bson.M)
			if !ok {
				continue
			}
			if id, has := m["member.userID"].(string); has {
				return id
			}
		}
	}
	return ""
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

// ============================================================================
// Phase 3: report drafts, warrant requests, simulated characters.
//
// Hiceguy's follow-up on #277. Each of these is a surface a third party asked
// for, and the tests below are mostly about what it cannot do: a key cannot
// file a report, cannot approve a warrant, cannot act for a player who is not
// entitled, and cannot reach another community's anything.
// ============================================================================

// ---- fakes for the phase-3 stores ----

type fakeReportDraftDB struct {
	databases.DeveloperReportDraftDatabase
	mu      sync.Mutex
	drafts  []models.DeveloperReportDraft
	inserts int
	deletes int
}

func (f *fakeReportDraftDB) Find(_ context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.DeveloperReportDraft, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)

	out := []models.DeveloperReportDraft{}
	for _, draft := range f.drafts {
		if v, ok := devFilterString(m["communityId"]); ok && draft.CommunityID != v {
			continue
		}
		if v, ok := devFilterString(m["sourceName"]); ok && draft.SourceName != v {
			continue
		}
		if v, ok := devFilterString(m["externalId"]); ok && draft.ExternalID != v {
			continue
		}
		if v, ok := devFilterString(m["status"]); ok && draft.Status != v {
			continue
		}
		if v, ok := devFilterString(m["kind"]); ok && draft.Kind != v {
			continue
		}
		if v, ok := devFilterString(m["source.keyId"]); ok {
			if draft.Source == nil || draft.Source.KeyID != v {
				continue
			}
		}
		if oid, ok := devFilterObjectID(m["_id"]); ok && draft.ID != oid {
			continue
		}
		out = append(out, draft)
	}
	return out, nil
}

func (f *fakeReportDraftDB) InsertOne(_ context.Context, draft models.DeveloperReportDraft) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drafts = append(f.drafts, draft)
	f.inserts++
	return fakeInsertResult{id: draft.ID}, nil
}

func (f *fakeReportDraftDB) UpdateOne(_ context.Context, filter, update interface{}, _ ...*options.UpdateOptions) (*models.DeveloperReportDraft, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fm, _ := filter.(bson.M)
	um, _ := update.(bson.M)
	set, _ := um["$set"].(bson.M)
	push, _ := um["$push"].(bson.M)
	pull, _ := um["$pull"].(bson.M)

	oid, ok := devFilterObjectID(fm["_id"])
	if !ok {
		return nil, nil
	}
	for i := range f.drafts {
		if f.drafts[i].ID != oid {
			continue
		}
		// Attachments arrive as $push and leave as $pull, so the fake has to
		// understand both. Without them an attachment write looks like it
		// worked and the list stays empty, which would make the idempotency
		// and count-cap tests pass while proving nothing.
		if v, has := push["attachments"].(models.DeveloperReportAttachment); has {
			f.drafts[i].Attachments = append(f.drafts[i].Attachments, v)
		}
		if cond, has := pull["attachments"].(bson.M); has {
			if wanted, found := devFilterString(cond["id"]); found {
				kept := f.drafts[i].Attachments[:0:0]
				for _, a := range f.drafts[i].Attachments {
					if a.ID == wanted {
						continue
					}
					kept = append(kept, a)
				}
				f.drafts[i].Attachments = kept
			}
		}
		if v, has := set["status"].(string); has {
			f.drafts[i].Status = v
		}
		if v, has := set["kind"].(string); has {
			f.drafts[i].Kind = v
		}
		if v, has := set["callId"].(string); has {
			f.drafts[i].CallID = v
		}
		if v, has := set["callExternalId"].(string); has {
			f.drafts[i].CallExternalID = v
		}
		if v, has := set["templateSlug"].(string); has {
			f.drafts[i].TemplateSlug = v
		}
		if v, has := set["departmentId"].(string); has {
			f.drafts[i].DepartmentID = v
		}
		if v, has := set["subject"].(models.DeveloperReportSubject); has {
			f.drafts[i].Subject = v
		}
		if v, has := set["facts"].(models.DeveloperReportFacts); has {
			f.drafts[i].Facts = v
		}
		if v, has := set["narrative"].(models.DeveloperReportNarrative); has {
			f.drafts[i].Narrative = v
		}
		if v, has := set["charges"].([]models.ArrestCharge); has {
			f.drafts[i].Charges = v
		}
		if v, has := set["reportingOfficerId"].(string); has {
			f.drafts[i].ReportingOfficerID = v
		}
		if v, has := set["reportingOfficerName"].(string); has {
			f.drafts[i].ReportingOfficerName = v
		}
		// The review half, written by the website rather than by a key.
		if v, has := set["reviewedBy"].(string); has {
			f.drafts[i].ReviewedBy = v
		}
		if v, has := set["reviewedByName"].(string); has {
			f.drafts[i].ReviewedByName = v
		}
		if v, has := set["reviewedAt"].(primitive.DateTime); has {
			at := v
			f.drafts[i].ReviewedAt = &at
		}
		if v, has := set["reviewNotes"].(string); has {
			f.drafts[i].ReviewNotes = v
		}
		if v, has := set["filedRecordId"].(string); has {
			f.drafts[i].FiledRecordID = v
		}
		if v, has := set["filedRecordKind"].(string); has {
			f.drafts[i].FiledRecordKind = v
		}
		found := f.drafts[i]
		return &found, nil
	}
	return nil, nil
}

func (f *fakeReportDraftDB) DeleteMany(_ context.Context, filter interface{}, _ ...*options.DeleteOptions) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	kept := f.drafts[:0:0]
	var removed int64
	for _, draft := range f.drafts {
		match := true
		if v, ok := devFilterString(m["communityId"]); ok && draft.CommunityID != v {
			match = false
		}
		if v, ok := devFilterString(m["status"]); ok && draft.Status != v {
			match = false
		}
		if v, ok := devFilterString(m["source.keyId"]); ok {
			if draft.Source == nil || draft.Source.KeyID != v {
				match = false
			}
		}
		if match {
			removed++
			continue
		}
		kept = append(kept, draft)
	}
	f.drafts = kept
	f.deletes += int(removed)
	return removed, nil
}

func (f *fakeReportDraftDB) CountDocuments(ctx context.Context, filter interface{}, _ ...*options.CountOptions) (int64, error) {
	found, err := f.Find(ctx, filter)
	return int64(len(found)), err
}

type fakeWarrantDB struct {
	databases.WarrantDatabase
	mu       sync.Mutex
	warrants []models.Warrant
	inserts  int
}

func (f *fakeWarrantDB) Find(_ context.Context, filter interface{}, _ ...*options.FindOptions) ([]models.Warrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)

	out := []models.Warrant{}
	for _, warrant := range f.warrants {
		d := warrant.Details
		if v, ok := devFilterString(m["warrant.activeCommunityID"]); ok && d.ActiveCommunityID != v {
			continue
		}
		if v, ok := devFilterString(m["warrant.source"]); ok && d.Source != v {
			continue
		}
		if v, ok := devFilterString(m["warrant.externalId"]); ok && d.ExternalID != v {
			continue
		}
		if v, ok := devFilterString(m["warrant.status"]); ok && d.Status != v {
			continue
		}
		if v, ok := devFilterString(m["warrant.accusedID"]); ok && d.AccusedID != v {
			continue
		}
		out = append(out, warrant)
	}
	return out, nil
}

func (f *fakeWarrantDB) FindOne(ctx context.Context, filter interface{}, _ ...*options.FindOneOptions) (*models.Warrant, error) {
	f.mu.Lock()
	m, _ := filter.(bson.M)
	wantID, hasID := devFilterObjectID(m["_id"])
	wantCommunity, hasCommunity := devFilterString(m["warrant.activeCommunityID"])
	warrants := append([]models.Warrant{}, f.warrants...)
	f.mu.Unlock()
	_ = ctx

	for _, warrant := range warrants {
		if hasID && warrant.ID != wantID {
			continue
		}
		if hasCommunity && warrant.Details.ActiveCommunityID != wantCommunity {
			continue
		}
		found := warrant
		return &found, nil
	}
	return nil, mongo.ErrNoDocuments
}

func (f *fakeWarrantDB) InsertOne(_ context.Context, doc interface{}, _ ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if warrant, ok := doc.(models.Warrant); ok {
		f.warrants = append(f.warrants, warrant)
		f.inserts++
	}
	return fakeInsertResult{id: primitive.NewObjectID()}, nil
}

func (f *fakeWarrantDB) CountDocuments(ctx context.Context, filter interface{}, _ ...*options.CountOptions) (int64, error) {
	found, err := f.Find(ctx, filter)
	return int64(len(found)), err
}

type fakeFormTemplateDB struct {
	databases.FormTemplateDatabase
	templates []models.FormTemplate
}

func (f *fakeFormTemplateDB) FindOne(_ context.Context, filter interface{}, _ ...*options.FindOneOptions) (*models.FormTemplate, error) {
	m, _ := filter.(bson.M)
	for _, template := range f.templates {
		if v, ok := devFilterString(m["formTemplate.communityID"]); ok && template.Details.CommunityID != v {
			continue
		}
		if v, ok := devFilterString(m["formTemplate.slug"]); ok && template.Details.Slug != v {
			continue
		}
		found := template
		return &found, nil
	}
	return nil, mongo.ErrNoDocuments
}

type fakeDeptToggleDB struct {
	databases.DepartmentFormToggleDatabase
	toggles []models.DepartmentFormToggle
}

func (f *fakeDeptToggleDB) Find(_ context.Context, filter interface{}, _ ...*options.FindOptions) ([]models.DepartmentFormToggle, error) {
	m, _ := filter.(bson.M)
	out := []models.DepartmentFormToggle{}
	for _, toggle := range f.toggles {
		d := toggle.Details
		if v, ok := devFilterString(m["departmentFormToggle.communityID"]); ok && d.CommunityID != v {
			continue
		}
		if v, ok := devFilterString(m["departmentFormToggle.departmentId"]); ok && d.DepartmentID != v {
			continue
		}
		if v, ok := devFilterString(m["departmentFormToggle.formTemplateSlug"]); ok && d.FormTemplateSlug != v {
			continue
		}
		out = append(out, toggle)
	}
	return out, nil
}

// ---- the phase-3 stack ----

// reportFixture is the e2e fixture with the phase-3 stores, a department the
// officers actually belong to, and a penal code to charge people under.
//
// The department membership matters: writing a report is something an officer
// in a department does, so a fixture whose community has an empty department
// would make every one of these tests pass for the wrong reason.
type reportFixture struct {
	*e2eFixture
	draftDB    *fakeReportDraftDB
	warrantDB  *fakeWarrantDB
	templateDB *fakeFormTemplateDB
	toggleDB   *fakeDeptToggleDB
	// gatedSlug is a form restricted to a rank nobody in the fixture holds.
	gatedSlug string
	openSlug  string
}

func newReportFixture(t *testing.T) *reportFixture {
	t.Helper()
	base := newE2EFixture(t)

	// The admin and the plain member are in BCSO; the outsider is in nothing.
	community := base.community
	community.Details.Departments[0].Members = []models.MemberStatus{
		{UserID: base.adminID, Status: "active"},
		{UserID: base.plainID, Status: "active"},
	}
	community.Details.PenalCodes = models.CommunityPenalCode{
		Categories: []models.PenalCodeCategory{{
			ID:   "traffic",
			Name: "Traffic Violations",
			Violations: []models.PenalCodeViolation{
				{Name: "Reckless Driving", JailTime: "30 seconds", Fine: 1000},
				{Name: "Speeding 10-19", JailTime: "N/A", Fine: 150},
			},
		}},
	}
	base.community = community
	if err := base.communityDB.seed(community); err != nil {
		t.Fatalf("re-seeding the community: %v", err)
	}

	draftDB := &fakeReportDraftDB{}
	warrantDB := &fakeWarrantDB{}

	// Two forms: one open to everyone, one gated to a rank nobody holds.
	unreachableRank := primitive.NewObjectID()
	templateDB := &fakeFormTemplateDB{templates: []models.FormTemplate{
		{
			ID: primitive.NewObjectID(),
			Details: models.FormTemplateDetails{
				CommunityID: community.ID.Hex(), Slug: "incident", Name: "Incident Report",
			},
		},
		{
			ID: primitive.NewObjectID(),
			Details: models.FormTemplateDetails{
				CommunityID: community.ID.Hex(), Slug: "command-review", Name: "Command Review",
				RankGates: []models.DepartmentRankGate{{
					DepartmentID: community.Details.Departments[0].ID.Hex(),
					EditableByRankRule: &models.RankRule{
						Mode: "exact", AnchorRankIDs: []string{unreachableRank.Hex()},
					},
				}},
			},
		},
	}}
	toggleDB := &fakeDeptToggleDB{}

	base.api.ReportDraftDB = draftDB
	base.api.WarrantDB = warrantDB
	base.api.FormTemplateDB = templateDB
	base.api.DeptFormToggleDB = toggleDB

	return &reportFixture{
		e2eFixture: base,
		draftDB:    draftDB,
		warrantDB:  warrantDB,
		templateDB: templateDB,
		toggleDB:   toggleDB,
		gatedSlug:  "command-review",
		openSlug:   "incident",
	}
}

// The live bug Hark found: PATCH members with a departmentId answered 200 and
// did nothing, so the very next call, creating a report draft for that officer,
// failed with reporting_officer_has_no_department. Two writes in that handler
// filtered on "community.departments.id" while models.Department stores its id
// as bson "_id", so neither matched, and UpdateOne cannot report a no-match.
//
// This test is deliberately the whole chain rather than an assertion about a
// filter: set the department through the real endpoint, read the community back
// through the real decoder, then write the report. Each of the three steps
// failed in production and only the third one was visible.
func TestE2E_SettingADepartmentLetsThatOfficerWriteAReport(t *testing.T) {
	f := newReportFixture(t)

	// An approved member of the community who is in no department, which is
	// every player's state right after they join.
	recruitID := primitive.NewObjectID().Hex()
	f.seedPlayer(recruitID, "recruit", f.community.ID.Hex(), models.CommunityMemberStatusApproved)
	recruit := fivemRef("recruit")

	// Writing a report is refused while they are in no department. If this
	// passed, the rest of the test would prove nothing.
	rr, _ := f.upsertDraft(t, `{"kind":"incident","externalId":"pre-dept-1",`+
		`"reportingOfficer":"`+recruit+`","narrative":{"summary":"before"}}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("a departmentless officer should be refused: got %d: %s", rr.Code, rr.Body.String())
	}

	// Now put them in one, through the endpoint under test.
	rr = f.run(t, http.MethodPatch, "/members/"+recruit,
		`{"actingPlayer":"`+fivemRef("admin")+`","departmentId":"`+f.deptID.Hex()+`"}`,
		map[string]string{"playerRef": recruit},
		f.api.RequireScope(models.ScopeMembersWrite, f.api.DevUpdateMemberHandler))
	if rr.Code != http.StatusOK {
		t.Fatalf("setting the department: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// The 200 has to mean it happened. Read the community back the way the API
	// reads it, so this checks the stored field names and not the fake's.
	stored, err := f.communityDB.FindOne(context.Background(), bson.M{"_id": f.community.ID})
	if err != nil {
		t.Fatalf("reading the community back: %v", err)
	}
	if !playerIsInAnyDepartment(stored, recruitID) {
		t.Fatalf("the endpoint answered 200 but the player is in no department: %+v",
			stored.Details.Departments)
	}
	inTheRightOne := false
	for _, dept := range stored.Details.Departments {
		if dept.ID != f.deptID {
			continue
		}
		for _, member := range dept.Members {
			if member.UserID == recruitID {
				inTheRightOne = true
			}
		}
	}
	if !inTheRightOne {
		t.Errorf("the player is in a department, but not the one that was asked for")
	}

	// And the call that was failing now works.
	rr, draft := f.upsertDraft(t, `{"kind":"incident","externalId":"post-dept-1",`+
		`"reportingOfficer":"`+recruit+`","narrative":{"summary":"after"}}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("writing a report after joining a department: expected 201, got %d: %s",
			rr.Code, rr.Body.String())
	}
	if draft.Draft.ID == "" {
		t.Error("the draft came back without an id")
	}
}

// The same chain for a warrant, which shares the officer check and used to
// borrow the report wording with it.
func TestE2E_WarrantRequestSaysWarrantNotReport(t *testing.T) {
	f := newReportFixture(t)

	recruitID := primitive.NewObjectID().Hex()
	f.seedPlayer(recruitID, "recruit", f.community.ID.Hex(), models.CommunityMemberStatusApproved)
	recruit := fivemRef("recruit")

	rr, _ := f.requestWarrant(t, `{"accused":"`+fivemRef("member")+`","externalId":"w-1",`+
		`"requestingOfficer":"`+recruit+`","probableCause":"Failed to appear in court."}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, "write a report") {
		t.Errorf("the warrant path should not talk about writing a report: %s", body)
	}
	if !strings.Contains(body, "request a warrant") || !strings.Contains(body, "requesting officer") {
		t.Errorf("the warrant path should say what it is: %s", body)
	}
	// The code is part of the contract, so it does not change with the wording.
	if !strings.Contains(body, "reporting_officer_has_no_department") {
		t.Errorf("the error code must stay the same for integrations: %s", body)
	}
}

type draftEnvelope struct {
	Draft   devReportDraftView `json:"draft"`
	Created bool               `json:"created"`
}

func (f *reportFixture) upsertDraft(t *testing.T, body string) (*httptest.ResponseRecorder, draftEnvelope) {
	t.Helper()
	rr := f.run(t, http.MethodPost, "/reports/drafts", body, nil,
		f.api.RequireScope(models.ScopeReportsWrite, f.api.DevUpsertReportDraftHandler))
	var out draftEnvelope
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr, out
}

type warrantEnvelope struct {
	Warrant devWarrantView `json:"warrant"`
	Created bool           `json:"created"`
}

// setWarrantMode puts the community on one of its three approval modes, which
// is what decides a new warrant whoever raised it.
func (f *reportFixture) setWarrantMode(t *testing.T, mode string, randomRate int) {
	t.Helper()
	community := f.community
	community.Details.WarrantApprovalMode = mode
	community.Details.WarrantRandomApprovalRate = randomRate
	f.community = community
	if err := f.communityDB.seed(community); err != nil {
		t.Fatalf("re-seeding the community: %v", err)
	}
}

func (f *reportFixture) requestWarrant(t *testing.T, body string) (*httptest.ResponseRecorder, warrantEnvelope) {
	t.Helper()
	rr := f.run(t, http.MethodPost, "/warrants/requests", body, nil,
		f.api.RequireScope(models.ScopeWarrantsWrite, f.api.DevRequestWarrantHandler))
	var out warrantEnvelope
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr, out
}

// ---- report drafts: what they are ----

// A draft is a draft. The response has to say so plainly, because the one
// conclusion an integration must never draw from a 201 here is that a report
// now exists in the community.
func TestE2E_ReportDraftIsNotAFiledReport(t *testing.T) {
	f := newReportFixture(t)

	rr, out := f.upsertDraft(t, `{
		"externalId":"sixpd-incident-900","source":"SixPD","kind":"arrest",
		"reportingOfficer":"`+fivemRef("admin")+`",
		"subject":{"player":"`+fivemRef("member")+`"},
		"facts":{"summary":"Pursuit ended on Route 68","location":"Route 68"},
		"narrative":{"text":"The suspect fled.","generated":true,"generatedBy":"sixpd-llm"},
		"charges":["Reckless Driving"]
	}`)

	if rr.Code != http.StatusCreated || !out.Created {
		t.Fatalf("expected 201 created, got %d created=%v: %s", rr.Code, out.Created, rr.Body.String())
	}
	if out.Draft.Status != models.ReportDraftStatusDraft {
		t.Errorf("a key may only ever produce a draft, got status %q", out.Draft.Status)
	}
	if out.Draft.Filed {
		t.Error("nothing a key calls files a report; filed must be false")
	}
	if out.Draft.FiledRecordID != "" {
		t.Errorf("a new draft cannot point at a filed record, got %q", out.Draft.FiledRecordID)
	}
	if out.Draft.ReviewLink == "" {
		t.Error("a draft needs a link a person can open to review it")
	}
}

// The review link has to be a path the website actually serves. It was not: it
// pointed at /community/{id}/report-drafts/{draftId} while the only route that
// existed was the list, so the one URL a game server could put in a
// supervisor's chat was a 404.
//
// Asserting the path and not just that the string is non-empty, which is what
// the test above did and why nobody noticed. The matching website route is in
// police-cad app/routes.js, and its own spec opens exactly this shape.
func TestE2E_ReviewLinkPointsAtARouteTheWebsiteServes(t *testing.T) {
	f := newReportFixture(t)

	_, created := f.upsertDraft(t, `{"kind":"incident","externalId":"link-1","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`","facts":{"summary":"As observed"}}`)

	link := created.Draft.ReviewLink
	if link == "" {
		t.Fatal("no review link")
	}
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("the review link does not parse: %v", err)
	}
	if parsed.Scheme != "https" {
		t.Errorf("a link handed to a supervisor should be https, got %q", parsed.Scheme)
	}

	// The website's /community/... routes decode their first path segment, so
	// the link carries the encoded community, never the raw ObjectId. A raw id
	// decodes to nonsense there and the page answers "Community not found",
	// which is exactly what the links handed out before this did.
	encoded := base64.RawURLEncoding.EncodeToString([]byte(f.community.ID.Hex()))
	if encoded == f.community.ID.Hex() {
		t.Fatal("the fixture community id encodes to itself, so this proves nothing")
	}
	want := "/community/" + encoded + "/report-drafts/" + created.Draft.ID
	if parsed.Path != want {
		t.Errorf("the review link path must match the website's route\n got: %s\nwant: %s",
			parsed.Path, want)
	}
	// And it decodes back to the community it is about.
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	decoded, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		t.Fatalf("the community segment is not the website's encoding: %v", err)
	}
	if string(decoded) != f.community.ID.Hex() {
		t.Errorf("the link points at %q, not this community", string(decoded))
	}
}

// The dispatch call deep link is spelled the same way, for the same reason.
func TestE2E_DispatchCallLinkUsesTheWebsiteEncoding(t *testing.T) {
	communityID := "6803ecda576732cfdd465db9"
	link := dispatchCallLink(communityID, primitive.NewObjectID().Hex())
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("the call link does not parse: %v", err)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) != 2 || segments[0] != "community" {
		t.Fatalf("unexpected call link path %q", parsed.Path)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		t.Fatalf("the community segment is not the website's encoding: %v", err)
	}
	if string(decoded) != communityID {
		t.Errorf("the link points at %q, not %q", string(decoded), communityID)
	}
}

// Facts and narrative are separate fields and stay separate. A reviewer has to
// be able to see what was observed apart from what a model wrote about it.
func TestE2E_ReportDraftKeepsFactsApartFromNarrative(t *testing.T) {
	f := newReportFixture(t)

	_, out := f.upsertDraft(t, `{
		"externalId":"sixpd-incident-901","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`",
		"facts":{"summary":"Vehicle failed to stop","location":"Route 68","forceUsed":true,
			"fields":{"weather":"clear","visibility":"good"}},
		"narrative":{"text":"In my opinion the driver was impaired."}
	}`)

	if out.Draft.Facts.Summary != "Vehicle failed to stop" {
		t.Errorf("the structured summary did not survive: %+v", out.Draft.Facts)
	}
	if !out.Draft.Facts.ForceUsed {
		t.Error("forceUsed is a fact and must be recorded as one")
	}
	if out.Draft.Facts.Fields["weather"] != "clear" {
		t.Errorf("template answers belong in facts.fields, got %+v", out.Draft.Facts.Fields)
	}
	if strings.Contains(out.Draft.Facts.Summary, "impaired") {
		t.Error("narrative text must never land in the fact sheet")
	}
	if out.Draft.Narrative.Text == "" {
		t.Error("the narrative was dropped")
	}
}

// A narrative with no claim either way is recorded as generated. Of the two
// possible mislabels, this is the one that makes a reviewer read more
// carefully rather than less.
func TestE2E_ReportNarrativeDefaultsToGenerated(t *testing.T) {
	f := newReportFixture(t)

	_, out := f.upsertDraft(t, `{
		"externalId":"sixpd-incident-902","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`",
		"narrative":{"text":"A vehicle was stopped."}
	}`)

	if !out.Draft.Narrative.Generated {
		t.Error("a narrative that did not say must be treated as generated")
	}
}

// The integration can say a person wrote it, and that claim is kept.
func TestE2E_ReportNarrativeCanBeDeclaredHumanWritten(t *testing.T) {
	f := newReportFixture(t)

	_, out := f.upsertDraft(t, `{
		"externalId":"sixpd-incident-903","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`",
		"narrative":{"text":"I stopped the vehicle.","generated":false}
	}`)

	if out.Draft.Narrative.Generated {
		t.Error("an explicit generated:false must be honoured")
	}
	if out.Draft.Narrative.GeneratedBy != "" {
		t.Error("a human-written narrative must not carry a generator name")
	}
}

// ---- report drafts: idempotency ----

// The same external id twice is one draft, updated. This is what makes a
// reconnect and a queue replay safe, and the reason it matters more here than
// on calls: a duplicate draft is a duplicate thing in a person's queue, and
// somebody would file both.
func TestE2E_ReportDraftUpsertIsIdempotent(t *testing.T) {
	f := newReportFixture(t)

	rr1, first := f.upsertDraft(t, `{
		"externalId":"sixpd-incident-910","source":"SixPD","kind":"incident",
		"reportingOfficer":"`+fivemRef("admin")+`",
		"facts":{"summary":"First pass"}
	}`)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr1.Code, rr1.Body.String())
	}

	rr2, second := f.upsertDraft(t, `{
		"externalId":"sixpd-incident-910","source":"SixPD","kind":"arrest",
		"reportingOfficer":"`+fivemRef("admin")+`",
		"facts":{"summary":"Second pass"}
	}`)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 on the repeat, got %d: %s", rr2.Code, rr2.Body.String())
	}
	if second.Created {
		t.Error("a repeat must not report creating anything")
	}
	if first.Draft.ID != second.Draft.ID {
		t.Errorf("one external id must be one draft: %s vs %s", first.Draft.ID, second.Draft.ID)
	}
	if f.draftDB.inserts != 1 {
		t.Errorf("expected exactly one draft written, got %d", f.draftDB.inserts)
	}
	if second.Draft.Facts.Summary != "Second pass" || second.Draft.Kind != "arrest" {
		t.Errorf("the repeat should have updated the draft, got %+v", second.Draft)
	}
}

// Source scopes the id space, as it does everywhere else on this surface, so a
// developer's staging and production instances can use the same incident
// numbers.
func TestE2E_ReportDraftSourceScopesTheIdSpace(t *testing.T) {
	f := newReportFixture(t)

	_, first := f.upsertDraft(t, `{"externalId":"incident-1","source":"sixpd-prod",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)
	_, second := f.upsertDraft(t, `{"externalId":"incident-1","source":"sixpd-dev",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)

	if first.Draft.ID == second.Draft.ID {
		t.Error("the same id under two sources must not collide")
	}
	if f.draftDB.inserts != 2 {
		t.Errorf("expected two drafts, got %d", f.draftDB.inserts)
	}
}

// Once a person has acted on a draft, a late retry must not overwrite what they
// decided. That is the one case where idempotency gives way to the human.
func TestE2E_ReportDraftRefusesToOverwriteAReview(t *testing.T) {
	f := newReportFixture(t)

	_, created := f.upsertDraft(t, `{"externalId":"incident-920","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`","facts":{"summary":"As observed"}}`)

	// The website files it.
	f.draftDB.mu.Lock()
	for i := range f.draftDB.drafts {
		if f.draftDB.drafts[i].ID.Hex() == created.Draft.ID {
			f.draftDB.drafts[i].Status = models.ReportDraftStatusSubmitted
			f.draftDB.drafts[i].FiledRecordID = primitive.NewObjectID().Hex()
			f.draftDB.drafts[i].FiledRecordKind = "arrestReport"
		}
	}
	f.draftDB.mu.Unlock()

	rr, _ := f.upsertDraft(t, `{"externalId":"incident-920","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`","facts":{"summary":"Rewritten after the fact"}}`)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 once a person has filed it, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "draft_already_reviewed") {
		t.Errorf("expected draft_already_reviewed, got %s", rr.Body.String())
	}

	f.draftDB.mu.Lock()
	defer f.draftDB.mu.Unlock()
	for _, draft := range f.draftDB.drafts {
		if draft.ID.Hex() == created.Draft.ID && draft.Facts.Summary != "As observed" {
			t.Errorf("the filed draft was rewritten: %q", draft.Facts.Summary)
		}
	}
}

// ---- report drafts: who may write one ----

// The key's scope is not the permission. A draft has to be attributed to a
// player, and that player has to be entitled to write one in this community.
func TestE2E_ReportDraftNeedsAReportingOfficer(t *testing.T) {
	f := newReportFixture(t)

	rr, _ := f.upsertDraft(t, `{"externalId":"incident-930","source":"SixPD","facts":{"summary":"x"}}`)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 with no reporting officer, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "reporting_officer_required") {
		t.Errorf("got %s", rr.Body.String())
	}
	if f.draftDB.inserts != 0 {
		t.Error("nothing should have been written")
	}
}

// Someone who is not in the community cannot be named as the officer, whatever
// scopes the key holds.
func TestE2E_ReportDraftRefusesAnOutsiderAsOfficer(t *testing.T) {
	f := newReportFixture(t)

	rr, _ := f.upsertDraft(t, `{"externalId":"incident-931","source":"SixPD",
		"reportingOfficer":"`+fivemRef("outsider")+`"}`)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for an outsider, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "reporting_officer_not_a_member") {
		t.Errorf("got %s", rr.Body.String())
	}
}

// A member with no department cannot write a report, because they cannot open
// a report form in the CAD either.
func TestE2E_ReportDraftRefusesAMemberWithNoDepartment(t *testing.T) {
	f := newReportFixture(t)

	// Take the plain member out of BCSO, leaving them an approved member of the
	// community and nothing else.
	community := f.community
	community.Details.Departments[0].Members = []models.MemberStatus{{UserID: f.adminID, Status: "active"}}
	f.community = community
	if err := f.communityDB.seed(community); err != nil {
		t.Fatalf("re-seeding: %v", err)
	}

	rr, _ := f.upsertDraft(t, `{"externalId":"incident-932","source":"SixPD",
		"reportingOfficer":"`+fivemRef("member")+`"}`)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 with no department, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "reporting_officer_has_no_department") {
		t.Errorf("got %s", rr.Body.String())
	}
}

// The community's own form gates apply to the officer, checked with the same
// helper the website uses. A key never grants more than the player it reports
// for.
func TestE2E_ReportDraftHonoursAFormRankGate(t *testing.T) {
	f := newReportFixture(t)

	t.Run("an ungated form is fine", func(t *testing.T) {
		rr, out := f.upsertDraft(t, `{"externalId":"incident-940","source":"SixPD",
			"reportingOfficer":"`+fivemRef("member")+`","templateSlug":"`+f.openSlug+`"}`)
		if rr.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
		}
		if out.Draft.TemplateSlug != f.openSlug {
			t.Errorf("the template was not recorded, got %q", out.Draft.TemplateSlug)
		}
	})

	t.Run("a rank-gated form the officer cannot use is refused", func(t *testing.T) {
		rr, _ := f.upsertDraft(t, `{"externalId":"incident-941","source":"SixPD",
			"reportingOfficer":"`+fivemRef("member")+`","templateSlug":"`+f.gatedSlug+`"}`)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for a gated form, got %d: %s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "template_not_permitted") {
			t.Errorf("got %s", rr.Body.String())
		}
	})

	t.Run("a form this community does not have is refused", func(t *testing.T) {
		rr, _ := f.upsertDraft(t, `{"externalId":"incident-942","source":"SixPD",
			"reportingOfficer":"`+fivemRef("admin")+`","templateSlug":"not-a-form"}`)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "template_not_found") {
			t.Errorf("got %s", rr.Body.String())
		}
	})
}

// A form an admin turned off for a department cannot be used by that
// department, which is the setting's whole purpose.
func TestE2E_ReportDraftHonoursADepartmentFormToggle(t *testing.T) {
	f := newReportFixture(t)
	f.toggleDB.toggles = []models.DepartmentFormToggle{{
		ID: primitive.NewObjectID(),
		Details: models.DepartmentFormToggleDetails{
			CommunityID:      f.community.ID.Hex(),
			DepartmentID:     f.deptID.Hex(),
			FormTemplateSlug: f.openSlug,
			IsEnabled:        false,
		},
	}}

	rr, _ := f.upsertDraft(t, `{"externalId":"incident-950","source":"SixPD",
		"reportingOfficer":"`+fivemRef("member")+`","departmentId":"`+f.deptID.Hex()+`",
		"templateSlug":"`+f.openSlug+`"}`)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a disabled form, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "template_disabled_for_department") {
		t.Errorf("got %s", rr.Body.String())
	}
}

// ---- report drafts: charges come from the community ----

// A key names a charge; the community prices it. This is what stops an
// integration inventing a fine or a jail term, and it means a community that
// retunes its penal code sees new drafts priced the new way.
func TestE2E_ReportChargesArePricedFromThePenalCode(t *testing.T) {
	f := newReportFixture(t)

	_, out := f.upsertDraft(t, `{"externalId":"incident-960","source":"SixPD","kind":"arrest",
		"reportingOfficer":"`+fivemRef("admin")+`",
		"charges":["Reckless Driving","Speeding 10-19"]}`)

	if len(out.Draft.Charges) != 2 {
		t.Fatalf("expected two charges, got %+v", out.Draft.Charges)
	}
	first := out.Draft.Charges[0]
	if first.Amount != 1000 {
		t.Errorf("the fine must come from the community's penal code, got %v", first.Amount)
	}
	if first.JailTime != "30 seconds" {
		t.Errorf("the jail time must come from the penal code, got %q", first.JailTime)
	}
	if first.Category != "Traffic Violations" {
		t.Errorf("the category must come from the penal code, got %q", first.Category)
	}
}

// A charge the community does not have is refused rather than recorded with a
// made-up amount.
func TestE2E_ReportRefusesAChargeNotInThePenalCode(t *testing.T) {
	f := newReportFixture(t)

	rr, _ := f.upsertDraft(t, `{"externalId":"incident-961","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`","charges":["Jaywalking On A Tuesday"]}`)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "charge_not_in_penal_code") {
		t.Errorf("got %s", rr.Body.String())
	}
	if f.draftDB.inserts != 0 {
		t.Error("nothing should have been written")
	}
}

// ---- report drafts: reading back and withdrawing ----

// Reconciliation after a restart: the integration knows its own incident id
// and nothing else, and needs to find out what a person decided.
func TestE2E_ReportDraftReadbackByExternalID(t *testing.T) {
	f := newReportFixture(t)

	_, created := f.upsertDraft(t, `{"externalId":"incident-970","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`","facts":{"summary":"As observed"}}`)

	rr := f.run(t, http.MethodGet, "/reports/drafts/incident-970?source=SixPD", "",
		map[string]string{"externalId": "incident-970"},
		f.api.RequireScope(models.ScopeReportsRead, f.api.DevGetReportDraftHandler))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out draftEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if out.Draft.ID != created.Draft.ID {
		t.Errorf("read back a different draft: %s vs %s", out.Draft.ID, created.Draft.ID)
	}
}

// An id that was never written is a 404, not an empty draft.
func TestE2E_ReportDraftReadbackMissingIsNotFound(t *testing.T) {
	f := newReportFixture(t)

	rr := f.run(t, http.MethodGet, "/reports/drafts/nope?source=SixPD", "",
		map[string]string{"externalId": "nope"},
		f.api.RequireScope(models.ScopeReportsRead, f.api.DevGetReportDraftHandler))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// Withdrawing takes a draft out of the queue, and doing it twice is a no-op
// rather than an error — a retry after a timeout must not need special-casing.
func TestE2E_ReportDraftWithdrawIsIdempotent(t *testing.T) {
	f := newReportFixture(t)

	f.upsertDraft(t, `{"externalId":"incident-980","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)

	first := f.run(t, http.MethodDelete, "/reports/drafts/incident-980?source=SixPD", "",
		map[string]string{"externalId": "incident-980"},
		f.api.RequireScope(models.ScopeReportsWrite, f.api.DevWithdrawReportDraftHandler))
	if first.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", first.Code, first.Body.String())
	}
	var out struct {
		Draft     devReportDraftView `json:"draft"`
		Withdrawn bool               `json:"withdrawn"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &out)
	if !out.Withdrawn || out.Draft.Status != models.ReportDraftStatusWithdrawn {
		t.Errorf("expected a withdrawn draft, got %+v", out)
	}

	second := f.run(t, http.MethodDelete, "/reports/drafts/incident-980?source=SixPD", "",
		map[string]string{"externalId": "incident-980"},
		f.api.RequireScope(models.ScopeReportsWrite, f.api.DevWithdrawReportDraftHandler))
	if second.Code != http.StatusOK {
		t.Fatalf("a repeat withdraw must not be an error, got %d: %s", second.Code, second.Body.String())
	}
	_ = json.Unmarshal(second.Body.Bytes(), &out)
	if out.Withdrawn {
		t.Error("the second withdraw must report withdrawing nothing")
	}
}

// A draft a person has filed cannot be withdrawn. Their decision is the record.
func TestE2E_ReportDraftCannotWithdrawAFiledOne(t *testing.T) {
	f := newReportFixture(t)

	_, created := f.upsertDraft(t, `{"externalId":"incident-981","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)
	f.draftDB.mu.Lock()
	for i := range f.draftDB.drafts {
		if f.draftDB.drafts[i].ID.Hex() == created.Draft.ID {
			f.draftDB.drafts[i].Status = models.ReportDraftStatusSubmitted
		}
	}
	f.draftDB.mu.Unlock()

	rr := f.run(t, http.MethodDelete, "/reports/drafts/incident-981?source=SixPD", "",
		map[string]string{"externalId": "incident-981"},
		f.api.RequireScope(models.ScopeReportsWrite, f.api.DevWithdrawReportDraftHandler))

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

// A key's list is its own output, not a window onto the community's reports or
// another integration's drafts.
func TestE2E_ReportDraftListIsScopedToTheKey(t *testing.T) {
	f := newReportFixture(t)

	f.upsertDraft(t, `{"externalId":"mine-1","source":"SixPD","reportingOfficer":"`+fivemRef("admin")+`"}`)

	// A draft from a different key, in the same community.
	f.draftDB.mu.Lock()
	f.draftDB.drafts = append(f.draftDB.drafts, models.DeveloperReportDraft{
		ID: primitive.NewObjectID(), CommunityID: f.community.ID.Hex(),
		Kind: models.ReportDraftKindIncident, Status: models.ReportDraftStatusDraft,
		SourceName: "SomeoneElse", ExternalID: "theirs-1",
		Source: &models.RecordSource{Type: models.RecordSourceDeveloperAPI, KeyID: primitive.NewObjectID().Hex()},
	})
	f.draftDB.mu.Unlock()

	rr := f.run(t, http.MethodGet, "/reports/drafts", "", nil,
		f.api.RequireScope(models.ScopeReportsRead, f.api.DevListReportDraftsHandler))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var out struct {
		Data       []devReportDraftView `json:"data"`
		TotalCount int64                `json:"totalCount"`
		Page       int                  `json:"page"`
		Limit      int                  `json:"limit"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(out.Data) != 1 || out.Data[0].ExternalID != "mine-1" {
		t.Errorf("a key must see only its own drafts, got %+v", out.Data)
	}
	if out.TotalCount != 1 {
		t.Errorf("totalCount must match the scoped filter, got %d", out.TotalCount)
	}
	// 1-based, like every other list in this product.
	if out.Page != 1 {
		t.Errorf("pages are 1-based, got %d", out.Page)
	}
}

// ---- report drafts: the boundaries every endpoint shares ----

func TestE2E_ReportDraftsRefuseAnotherCommunity(t *testing.T) {
	f := newReportFixture(t)
	other := primitive.NewObjectID()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/dev/communities/"+other.Hex()+"/reports/drafts",
		strings.NewReader(`{"externalId":"x","reportingOfficer":"`+fivemRef("admin")+`"}`))
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	req = mux.SetURLVars(req, map[string]string{"communityId": other.Hex()})
	f.api.RequireDeveloperAPIKey(
		f.api.RequireScope(models.ScopeReportsWrite, f.api.DevUpsertReportDraftHandler),
	).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for an ungranted community, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "community_not_granted") {
		t.Errorf("got %s", rr.Body.String())
	}
}

func TestE2E_ReportDraftsRefuseARevokedKey(t *testing.T) {
	f := newReportFixture(t)
	for hash, key := range f.keyDB.byHash {
		key.Status = models.DeveloperKeyRevoked
		f.keyDB.byHash[hash] = key
	}

	rr, _ := f.upsertDraft(t, `{"externalId":"x","reportingOfficer":"`+fivemRef("admin")+`"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("a revoked key must be refused, got %d: %s", rr.Code, rr.Body.String())
	}
}

// Holding reports:read is not holding reports:write. The scopes are separate
// so an integration that only reconciles can be issued a key that cannot
// write.
func TestE2E_ReportDraftsRefuseAKeyWithoutTheScope(t *testing.T) {
	f := newReportFixture(t)
	f.keyDB.byHash = upgradeKeyScopes(f.keyDB.byHash, models.ScopeReportsRead)

	rr, _ := f.upsertDraft(t, `{"externalId":"x","reportingOfficer":"`+fivemRef("admin")+`"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without reports:write, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "missing_scope") {
		t.Errorf("got %s", rr.Body.String())
	}
}

// ---- warrants ----

// A request follows the community's own approval mode, the same way a warrant
// raised on the website does. The key never decides anything: there is no
// approve or review route under the developer prefix, and the outcome below
// comes from the community's rule.
func TestE2E_WarrantRequestFollowsTheCommunityMode(t *testing.T) {
	t.Run("require-judge lands pending", func(t *testing.T) {
		f := newReportFixture(t)
		f.setWarrantMode(t, "require-judge", 0)

		rr, out := f.requestWarrant(t, `{"externalId":"w-judge","source":"SixPD",
			"requestingOfficer":"`+fivemRef("admin")+`","accused":"`+fivemRef("member")+`",
			"probableCause":"Fled a lawful traffic stop."}`)

		if rr.Code != http.StatusCreated || !out.Created {
			t.Fatalf("expected 201 created, got %d: %s", rr.Code, rr.Body.String())
		}
		if out.Warrant.Status != models.WarrantStatusPending {
			t.Errorf("require-judge must leave it pending, got %q", out.Warrant.Status)
		}
		if !out.Warrant.AwaitingReview {
			t.Error("the response must say a person has yet to look at it")
		}
		if out.Warrant.JudgeName != "" {
			t.Errorf("no judge has seen it yet, got %q", out.Warrant.JudgeName)
		}
	})

	t.Run("auto-approve approves, and so does an unset mode", func(t *testing.T) {
		for _, mode := range []string{"auto-approve", ""} {
			name := mode
			if name == "" {
				name = "(unset)"
			}
			t.Run(name, func(t *testing.T) {
				f := newReportFixture(t)
				f.setWarrantMode(t, mode, 0)

				rr, out := f.requestWarrant(t, `{"externalId":"w-auto","source":"SixPD",
					"requestingOfficer":"`+fivemRef("admin")+`","accused":"`+fivemRef("member")+`",
					"probableCause":"Fled a lawful traffic stop."}`)

				if rr.Code != http.StatusCreated {
					t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
				}
				if out.Warrant.Status != models.WarrantStatusApproved {
					t.Errorf("%s must approve, got %q", name, out.Warrant.Status)
				}
				if !out.Warrant.Approved {
					t.Error("the view should report it as approved")
				}
				if out.Warrant.AwaitingReview {
					t.Error("nothing is awaiting review once the community has decided")
				}
				if out.Warrant.JudgeName != "Auto-Approved" {
					t.Errorf("the decision should name itself, got %q", out.Warrant.JudgeName)
				}
			})
		}
	})

	t.Run("random decides immediately, either way", func(t *testing.T) {
		// Both outcomes are correct, so this asserts what is true of both:
		// decided, with a judge name and a reason. A rate of 50 over several
		// requests also makes a mode that silently always approved visible.
		f := newReportFixture(t)
		f.setWarrantMode(t, "random", 50)

		approved, denied := 0, 0
		for i := 0; i < 20; i++ {
			_, out := f.requestWarrant(t, `{"externalId":"w-random-`+strconv.Itoa(i)+`","source":"SixPD",
				"requestingOfficer":"`+fivemRef("admin")+`","accused":"`+fivemRef("member")+`",
				"probableCause":"Fled a lawful traffic stop."}`)

			switch out.Warrant.Status {
			case models.WarrantStatusApproved:
				approved++
			case models.WarrantStatusDenied:
				denied++
			default:
				t.Fatalf("random must decide, got %q", out.Warrant.Status)
			}
			if out.Warrant.AwaitingReview {
				t.Error("a decided warrant is not awaiting review")
			}
			if !strings.HasPrefix(out.Warrant.JudgeName, "Judge ") {
				t.Errorf("random mode names a judge, got %q", out.Warrant.JudgeName)
			}
		}
		if approved == 0 || denied == 0 {
			t.Errorf("at a 50%% rate over 20 requests both outcomes should appear, got %d approved and %d denied",
				approved, denied)
		}
	})
}

// The outcome is its own event when the community settles it on the spot, so a
// subscriber waiting to hear does not have to infer it from the request.
func TestE2E_WarrantEventsCoverTheOutcome(t *testing.T) {
	f := newReportFixture(t)
	events := &fakeEventDB{}
	f.api.EventDB = events

	f.setWarrantMode(t, "auto-approve", 0)
	f.requestWarrant(t, `{"externalId":"w-events","source":"SixPD",
		"requestingOfficer":"`+fivemRef("admin")+`","accused":"`+fivemRef("member")+`",
		"probableCause":"Fled a lawful traffic stop."}`)

	seen := events.waitForEvents(t, 2)
	kinds := map[string]string{}
	for _, event := range seen {
		kinds[event.Type] = event.Data["status"]
	}
	if _, has := kinds[models.EventWarrantRequested]; !has {
		t.Errorf("the request itself must be an event, got %v", kinds)
	}
	if status, has := kinds[models.EventWarrantReviewed]; !has {
		t.Errorf("an immediate decision must emit the reviewed event, got %v", kinds)
	} else if status != models.WarrantStatusApproved {
		t.Errorf("the reviewed event should carry the outcome, got %q", status)
	}

	// And a mode that leaves it for a judge emits only the request.
	f2 := newReportFixture(t)
	events2 := &fakeEventDB{}
	f2.api.EventDB = events2
	f2.setWarrantMode(t, "require-judge", 0)
	f2.requestWarrant(t, `{"externalId":"w-events-2","source":"SixPD",
		"requestingOfficer":"`+fivemRef("admin")+`","accused":"`+fivemRef("member")+`",
		"probableCause":"Fled a lawful traffic stop."}`)

	for _, event := range events2.waitForEvents(t, 1) {
		if event.Type == models.EventWarrantReviewed {
			t.Error("nothing has reviewed it, so there is no reviewed event")
		}
	}
}

// The stored warrant records that a script raised it, so a judge reading the
// history is not left thinking the officer filed it by hand.
func TestE2E_WarrantRequestRecordsTheIntegration(t *testing.T) {
	f := newReportFixture(t)

	f.requestWarrant(t, `{"externalId":"sixpd-warrant-2","source":"SixPD",
		"requestingOfficer":"`+fivemRef("admin")+`","accusedFirstName":"John","accusedLastName":"Doe",
		"probableCause":"Observed leaving the scene."}`)

	f.warrantDB.mu.Lock()
	defer f.warrantDB.mu.Unlock()
	if len(f.warrantDB.warrants) != 1 {
		t.Fatalf("expected one warrant, got %d", len(f.warrantDB.warrants))
	}
	stored := f.warrantDB.warrants[0].Details
	if stored.CreatedByKeyID != f.keyID.Hex() {
		t.Errorf("the key that raised it must be recorded, got %q", stored.CreatedByKeyID)
	}
	if stored.RecordSource == nil || stored.RecordSource.Type != models.RecordSourceDeveloperAPI {
		t.Errorf("the record source stamp is missing: %+v", stored.RecordSource)
	}
	if len(stored.History) == 0 || stored.History[0].Action != "created" {
		t.Fatalf("the first history entry should be the request, got %+v", stored.History)
	}
	if !strings.Contains(stored.History[0].Notes, "Developer API") {
		t.Errorf("the history must say a script raised this, got %q", stored.History[0].Notes)
	}
	// The fixture community has no mode set, so it auto-approves, and the
	// decision is the entry after the request. That is the same trail the
	// website leaves.
	if len(stored.History) != 2 || stored.History[1].Action != models.WarrantStatusApproved {
		t.Fatalf("expected the decision to follow the request, got %+v", stored.History)
	}
	if stored.History[1].UserName != "Auto-Approved" {
		t.Errorf("the decision should name itself, got %q", stored.History[1].UserName)
	}
}

// A retried request returns the one already in the queue. A judge must not get
// five copies because a game server reconnected five times.
func TestE2E_WarrantRequestIsIdempotent(t *testing.T) {
	f := newReportFixture(t)

	body := `{"externalId":"sixpd-warrant-3","source":"SixPD",
		"requestingOfficer":"` + fivemRef("admin") + `","accused":"` + fivemRef("member") + `",
		"probableCause":"Repeated failure to appear."}`

	rr1, first := f.requestWarrant(t, body)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr1.Code, rr1.Body.String())
	}
	rr2, second := f.requestWarrant(t, body)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 on the repeat, got %d: %s", rr2.Code, rr2.Body.String())
	}
	if second.Created {
		t.Error("a repeat must not report creating anything")
	}
	if first.Warrant.ID != second.Warrant.ID {
		t.Errorf("one external id must be one warrant: %s vs %s", first.Warrant.ID, second.Warrant.ID)
	}
	if f.warrantDB.inserts != 1 {
		t.Errorf("expected exactly one warrant written, got %d", f.warrantDB.inserts)
	}
}

// A retry after a judge has approved it returns the approved warrant rather
// than raising a second request.
func TestE2E_WarrantRetryAfterApprovalReturnsTheDecision(t *testing.T) {
	f := newReportFixture(t)

	body := `{"externalId":"sixpd-warrant-4","source":"SixPD",
		"requestingOfficer":"` + fivemRef("admin") + `","accusedFirstName":"Jane","accusedLastName":"Roe",
		"probableCause":"Burglary in progress."}`
	f.requestWarrant(t, body)

	f.warrantDB.mu.Lock()
	f.warrantDB.warrants[0].Details.Status = models.WarrantStatusApproved
	f.warrantDB.warrants[0].Details.JudgeName = "Judge Carter"
	f.warrantDB.mu.Unlock()

	rr, out := f.requestWarrant(t, body)
	if rr.Code != http.StatusOK || out.Created {
		t.Fatalf("expected 200 and created=false, got %d created=%v", rr.Code, out.Created)
	}
	if !out.Warrant.Approved || out.Warrant.AwaitingReview {
		t.Errorf("the decision must be reported back, got %+v", out.Warrant)
	}
	if f.warrantDB.inserts != 1 {
		t.Errorf("a retry after approval must not raise a second request, got %d", f.warrantDB.inserts)
	}
}

// A request with nothing in probableCause cannot be reviewed, so it is refused
// rather than left in a queue for somebody to delete.
func TestE2E_WarrantRequestNeedsProbableCause(t *testing.T) {
	f := newReportFixture(t)

	rr, _ := f.requestWarrant(t, `{"externalId":"sixpd-warrant-5","source":"SixPD",
		"requestingOfficer":"`+fivemRef("admin")+`","accusedFirstName":"John"}`)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "probable_cause_required") {
		t.Errorf("got %s", rr.Body.String())
	}
	if f.warrantDB.inserts != 0 {
		t.Error("nothing should have been written")
	}
}

// The officer asking has to be entitled to ask, exactly as for a report draft.
func TestE2E_WarrantRequestRefusesAnOutsider(t *testing.T) {
	f := newReportFixture(t)

	rr, _ := f.requestWarrant(t, `{"externalId":"sixpd-warrant-6","source":"SixPD",
		"requestingOfficer":"`+fivemRef("outsider")+`","accusedFirstName":"John",
		"probableCause":"Something happened."}`)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
	if f.warrantDB.inserts != 0 {
		t.Error("nothing should have been written")
	}
}

// A request has to name somebody.
func TestE2E_WarrantRequestNeedsAnAccused(t *testing.T) {
	f := newReportFixture(t)

	rr, _ := f.requestWarrant(t, `{"externalId":"sixpd-warrant-7","source":"SixPD",
		"requestingOfficer":"`+fivemRef("admin")+`","probableCause":"Something happened."}`)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "accused_required") {
		t.Errorf("got %s", rr.Body.String())
	}
}

// "Is there a warrant out for this person" is the question a game server asks
// on every stop, and it is one request.
func TestE2E_WarrantLookupByAccusedAndStatus(t *testing.T) {
	f := newReportFixture(t)

	// status=pending below, so the community has to be one that leaves them
	// pending. Without this the fixture auto-approves and the filter is
	// vacuously satisfied by an empty list.
	f.setWarrantMode(t, "require-judge", 0)

	f.requestWarrant(t, `{"externalId":"w-a","source":"SixPD",
		"requestingOfficer":"`+fivemRef("admin")+`","accused":"`+fivemRef("member")+`",
		"probableCause":"One."}`)
	f.requestWarrant(t, `{"externalId":"w-b","source":"SixPD",
		"requestingOfficer":"`+fivemRef("admin")+`","accusedFirstName":"Someone","accusedLastName":"Else",
		"probableCause":"Two."}`)

	rr := f.run(t, http.MethodGet,
		"/warrants?accused="+fivemRef("member")+"&status=pending", "", nil,
		f.api.RequireScope(models.ScopeWarrantsRead, f.api.DevListWarrantsHandler))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var out struct {
		Data       []devWarrantView `json:"data"`
		TotalCount int64            `json:"totalCount"`
		Page       int              `json:"page"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(out.Data) != 1 || out.Data[0].ExternalID != "w-a" {
		t.Errorf("expected only the member's warrant, got %+v", out.Data)
	}
	if out.Page != 1 {
		t.Errorf("pages are 1-based, got %d", out.Page)
	}
}

// A warrant id from another community is a 404, not a 403 after a read: the
// community is in the filter, so its existence is never confirmed.
func TestE2E_WarrantFromAnotherCommunityIsNotFound(t *testing.T) {
	f := newReportFixture(t)

	foreign := models.Warrant{ID: primitive.NewObjectID()}
	foreign.Details.ActiveCommunityID = primitive.NewObjectID().Hex()
	foreign.Details.Status = models.WarrantStatusApproved
	f.warrantDB.warrants = append(f.warrantDB.warrants, foreign)

	rr := f.run(t, http.MethodGet, "/warrants/"+foreign.ID.Hex(), "",
		map[string]string{"warrantId": foreign.ID.Hex()},
		f.api.RequireScope(models.ScopeWarrantsRead, f.api.DevGetWarrantHandler))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestE2E_WarrantsRefuseAnotherCommunity(t *testing.T) {
	f := newReportFixture(t)
	other := primitive.NewObjectID()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/dev/communities/"+other.Hex()+"/warrants", nil)
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	req = mux.SetURLVars(req, map[string]string{"communityId": other.Hex()})
	f.api.RequireDeveloperAPIKey(
		f.api.RequireScope(models.ScopeWarrantsRead, f.api.DevListWarrantsHandler),
	).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestE2E_WarrantsRefuseARevokedKey(t *testing.T) {
	f := newReportFixture(t)
	for hash, key := range f.keyDB.byHash {
		key.Status = models.DeveloperKeyRevoked
		f.keyDB.byHash[hash] = key
	}

	rr := f.run(t, http.MethodGet, "/warrants", "", nil,
		f.api.RequireScope(models.ScopeWarrantsRead, f.api.DevListWarrantsHandler))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("a revoked key must be refused, got %d: %s", rr.Code, rr.Body.String())
	}
}

// Reading warrants is not requesting one.
func TestE2E_WarrantRequestRefusesAReadOnlyKey(t *testing.T) {
	f := newReportFixture(t)
	f.keyDB.byHash = upgradeKeyScopes(f.keyDB.byHash, models.ScopeWarrantsRead)

	rr, _ := f.requestWarrant(t, `{"externalId":"x","requestingOfficer":"`+fivemRef("admin")+`",
		"accusedFirstName":"John","probableCause":"Something."}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without warrants:write, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "missing_scope") {
		t.Errorf("got %s", rr.Body.String())
	}
}

// ---- simulated characters ----

// The synthetic filter has to be "not explicitly synthetic", not "synthetic is
// false". Every record in production predates the field, so a filter on false
// would have emptied every search on the day it shipped.
func TestE2E_SyntheticFilterKeepsRecordsThatPredateTheField(t *testing.T) {
	filter := applySyntheticScope(bson.M{"civilian.activeCommunityID": devEq("c1")},
		"civilian.source.synthetic", false)

	cond, ok := filter["civilian.source.synthetic"].(bson.M)
	if !ok {
		t.Fatalf("expected a condition on the synthetic field, got %+v", filter)
	}
	if ne, has := cond["$ne"]; !has || ne != true {
		t.Errorf("the filter must be $ne:true so records with no field still match, got %+v", cond)
	}
	if _, has := cond["$eq"]; has {
		t.Error("an equality on false would exclude every record that predates the field")
	}
}

// Asking for them turns the filter off entirely rather than inverting it: a
// caller that wants NPCs wants the people too.
func TestE2E_SyntheticOptInReturnsEverything(t *testing.T) {
	filter := applySyntheticScope(bson.M{"civilian.activeCommunityID": devEq("c1")},
		"civilian.source.synthetic", true)

	if _, has := filter["civilian.source.synthetic"]; has {
		t.Error("includeSynthetic must widen the result, not narrow it to NPCs")
	}
}

// An integration declares an NPC once, at provisioning, and the account and
// the identity row both carry it.
func TestE2E_SyntheticPlayerIsDeclaredAtProvisioning(t *testing.T) {
	f := newReportFixture(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dev/players",
		strings.NewReader(`{"provider":"fivem","identifier":"deadbeefcafe01","username":"NPC Vendor","synthetic":true}`))
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	f.api.RequireDeveloperAPIKey(
		f.api.RequireScope(models.ScopePlayersProvision, f.api.DevUpsertPlayerHandler),
	).ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Player devPlayerResponse `json:"player"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if !out.Player.Synthetic {
		t.Errorf("the declaration must be reported back, got %+v", out.Player)
	}

	f.identityDB.mu.Lock()
	defer f.identityDB.mu.Unlock()
	found := false
	for _, row := range f.identityDB.rows {
		if row.Identifier == "deadbeefcafe01" {
			found = true
			if !row.Synthetic {
				t.Error("the identity row must carry the synthetic flag; the record-write path reads it")
			}
		}
	}
	if !found {
		t.Fatal("the identity row was not written")
	}
}

// Marking an NPC once is enough: its records inherit the flag whether or not
// the integration repeated it. The alternative is an integration that gets its
// people right and forgets on one of four record types.
func TestE2E_SyntheticIsInheritedByRecords(t *testing.T) {
	f := newReportFixture(t)
	dc := &DevAPIContext{Key: f.keyDB.byHash[hashDeveloperKey(f.plaintext)]}

	f.identityDB.mu.Lock()
	f.identityDB.rows = append(f.identityDB.rows, models.PlayerIdentity{
		ID: primitive.NewObjectID(), Provider: models.PlayerProviderFiveM,
		Identifier: "npc0001", UserID: primitive.NewObjectID().Hex(),
		CreatedByKeyID: f.keyID.Hex(), Synthetic: true, Claimed: false,
	})
	f.identityDB.mu.Unlock()

	npc := &resolvedPlayer{
		UserID: "u1", Provider: models.PlayerProviderFiveM, Identifier: "npc0001",
	}
	stamp := f.api.recordStamp(context.Background(), dc, npc, false, "SixPD", "npc-car-1")
	if !stamp.Synthetic {
		t.Error("a record owned by a synthetic character must be synthetic without being told again")
	}
	if stamp.ExternalID != "npc-car-1" || stamp.SourceName != "SixPD" {
		t.Errorf("the integration's own reference must be kept for cleanup, got %+v", stamp)
	}
}

// A claimed account is a person, whatever the row was created as. Leaving it
// synthetic would hide a real player from their own community's roster.
func TestE2E_ClaimingAnAccountClearsSynthetic(t *testing.T) {
	f := newReportFixture(t)
	dc := &DevAPIContext{Key: f.keyDB.byHash[hashDeveloperKey(f.plaintext)]}

	f.identityDB.mu.Lock()
	f.identityDB.rows = append(f.identityDB.rows, models.PlayerIdentity{
		ID: primitive.NewObjectID(), Provider: models.PlayerProviderFiveM,
		Identifier: "npc0002", UserID: primitive.NewObjectID().Hex(),
		CreatedByKeyID: f.keyID.Hex(), Synthetic: true,
		// The person signed in and took it over.
		Claimed: true,
	})
	f.identityDB.mu.Unlock()

	player := &resolvedPlayer{UserID: "u2", Provider: models.PlayerProviderFiveM, Identifier: "npc0002"}
	if f.api.playerIsSyntheticForKey(context.Background(), dc, player) {
		t.Error("a claimed account must never be treated as synthetic")
	}
}

// One integration must not be able to write records about another's NPCs, so
// the synthetic exemption is scoped to the key that created the identity.
func TestE2E_SyntheticExemptionIsScopedToTheKey(t *testing.T) {
	f := newReportFixture(t)
	dc := &DevAPIContext{Key: f.keyDB.byHash[hashDeveloperKey(f.plaintext)]}

	f.identityDB.mu.Lock()
	f.identityDB.rows = append(f.identityDB.rows, models.PlayerIdentity{
		ID: primitive.NewObjectID(), Provider: models.PlayerProviderFiveM,
		Identifier: "npc0003", UserID: primitive.NewObjectID().Hex(),
		// Somebody else's key.
		CreatedByKeyID: primitive.NewObjectID().Hex(), Synthetic: true,
	})
	f.identityDB.mu.Unlock()

	player := &resolvedPlayer{UserID: "u3", Provider: models.PlayerProviderFiveM, Identifier: "npc0003"}
	if f.api.playerIsSyntheticForKey(context.Background(), dc, player) {
		t.Error("another key's NPC must not be exempt from this key's membership check")
	}
}

// Cleanup will not run without the caller naming the community it is deleting
// from. A stale config value pointed at the wrong community is exactly the
// mistake this guards.
func TestE2E_SyntheticCleanupRequiresConfirmation(t *testing.T) {
	f := newReportFixture(t)

	rr := f.run(t, http.MethodDelete, "/synthetic-records", `{}`, nil,
		f.api.RequireScope(models.ScopePlayersProvision, f.api.DevCleanupSyntheticRecordsHandler))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 with no confirmation, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "confirm_required") {
		t.Errorf("got %s", rr.Body.String())
	}

	wrong := f.run(t, http.MethodDelete, "/synthetic-records",
		`{"confirm":"`+primitive.NewObjectID().Hex()+`"}`, nil,
		f.api.RequireScope(models.ScopePlayersProvision, f.api.DevCleanupSyntheticRecordsHandler))
	if wrong.Code != http.StatusBadRequest {
		t.Fatalf("a confirmation for a different community must be refused, got %d", wrong.Code)
	}
}

// With the right confirmation it runs, and the filter it builds is the whole
// safety of the endpoint: synthetic only, this key only, this community only.
func TestE2E_SyntheticCleanupIsScopedThreeWays(t *testing.T) {
	f := newReportFixture(t)
	civDB := &fakeDeleteCivDB{}
	f.api.CivDB = civDB

	rr := f.run(t, http.MethodDelete, "/synthetic-records",
		`{"confirm":"`+f.community.ID.Hex()+`","source":"SixPD"}`, nil,
		f.api.RequireScope(models.ScopePlayersProvision, f.api.DevCleanupSyntheticRecordsHandler))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	if len(civDB.filters) != 1 {
		t.Fatalf("expected one delete against civilians, got %d", len(civDB.filters))
	}
	filter := civDB.filters[0]
	if v, _ := devFilterString(filter["civilian.activeCommunityID"]); v != f.community.ID.Hex() {
		t.Errorf("the delete must be scoped to this community, got %+v", filter["civilian.activeCommunityID"])
	}
	if v, _ := devFilterString(filter["civilian.source.keyId"]); v != f.keyID.Hex() {
		t.Errorf("the delete must be scoped to this key, got %+v", filter["civilian.source.keyId"])
	}
	cond, ok := filter["civilian.source.synthetic"].(bson.M)
	if !ok || cond["$eq"] != true {
		t.Errorf("the delete must only touch synthetic records, got %+v", filter["civilian.source.synthetic"])
	}
	if v, _ := devFilterString(filter["civilian.source.sourceName"]); v != "SixPD" {
		t.Errorf("the source narrowing was dropped, got %+v", filter["civilian.source.sourceName"])
	}
}

func TestE2E_SyntheticCleanupRefusesARevokedKey(t *testing.T) {
	f := newReportFixture(t)
	for hash, key := range f.keyDB.byHash {
		key.Status = models.DeveloperKeyRevoked
		f.keyDB.byHash[hash] = key
	}

	rr := f.run(t, http.MethodDelete, "/synthetic-records",
		`{"confirm":"`+f.community.ID.Hex()+`"}`, nil,
		f.api.RequireScope(models.ScopePlayersProvision, f.api.DevCleanupSyntheticRecordsHandler))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("a revoked key must be refused, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestE2E_SyntheticCleanupRefusesAnotherCommunity(t *testing.T) {
	f := newReportFixture(t)
	other := primitive.NewObjectID()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete,
		"/api/v1/dev/communities/"+other.Hex()+"/synthetic-records",
		strings.NewReader(`{"confirm":"`+other.Hex()+`"}`))
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	req = mux.SetURLVars(req, map[string]string{"communityId": other.Hex()})
	f.api.RequireDeveloperAPIKey(
		f.api.RequireScope(models.ScopePlayersProvision, f.api.DevCleanupSyntheticRecordsHandler),
	).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// fakeDeleteCivDB records the filters a delete is issued with, which is what
// the cleanup tests assert on: the rows are the easy part, the scoping is the
// part that has to be right.
type fakeDeleteCivDB struct {
	databases.CivilianDatabase
	mu      sync.Mutex
	filters []bson.M
}

func (f *fakeDeleteCivDB) DeleteMany(_ context.Context, filter interface{}, _ ...*options.DeleteOptions) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := filter.(bson.M); ok {
		f.filters = append(f.filters, m)
	}
	return 0, nil
}

// ---- the new scopes ----

// A scope the console can offer has to exist in the model's own list, which is
// the single source of truth the key-creation endpoint validates against.
func TestE2E_Phase3ScopesAreIssuable(t *testing.T) {
	for _, scope := range []string{
		models.ScopeReportsRead, models.ScopeReportsWrite,
		models.ScopeWarrantsRead, models.ScopeWarrantsWrite,
	} {
		if !models.IsValidDeveloperScope(scope) {
			t.Errorf("%s is used by a route but cannot be issued", scope)
		}
		described := false
		for _, s := range models.DeveloperScopes() {
			if s.Scope == scope {
				described = true
				if strings.TrimSpace(s.Description) == "" {
					t.Errorf("%s has no description, so the console would render a blank row", scope)
				}
			}
		}
		if !described {
			t.Errorf("%s is missing from DeveloperScopes()", scope)
		}
	}
}

// There is no scope that files a report or approves a warrant, and there is not
// meant to be. This test is the tripwire: adding one makes it fail, which is
// the point at which somebody should be asked whether they meant to.
func TestE2E_NoScopeFilesOrApproves(t *testing.T) {
	forbidden := []string{
		"reports:file", "reports:submit", "reports:approve",
		"warrants:approve", "warrants:issue", "warrants:execute",
		"arrests:write", "citations:file",
	}
	for _, scope := range forbidden {
		if models.IsValidDeveloperScope(scope) {
			t.Errorf("%s is issuable: a key must never be able to commit a legal record", scope)
		}
	}
}

// ---- the other half: a person reviewing a draft and filing it ----
//
// These routes are on the first-party surface, so they are driven here the way
// the website drives them: the gateway secret in the header, and the acting
// user named in the query. That the developer key cannot reach them at all is
// not something a test can assert directly — they are simply not registered
// under the developer prefix — so what is tested is the gate that does apply.

type fakeArrestReportDB struct {
	databases.ArrestReportDatabase
	mu      sync.Mutex
	reports []models.ArrestReport
}

func (f *fakeArrestReportDB) InsertOne(_ context.Context, report models.ArrestReport, _ ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reports = append(f.reports, report)
	return fakeInsertResult{id: report.ID}, nil
}

// fakeFormSubmissionDB stands in for the formSubmissions collection, including
// its unique index on (communityID, reportNumber).
//
// The index is the point. Without it this fake accepted every insert, so a
// filing path that never allocated a report number looked correct here and then
// failed in production on the second report in a community. A fake that is
// cheerier than the collection it stands for is worse than no fake at all.
type fakeFormSubmissionDB struct {
	databases.FormSubmissionDatabase
	mu          sync.Mutex
	submissions []models.FormSubmission
}

func (f *fakeFormSubmissionDB) InsertOne(_ context.Context, doc interface{}, _ ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	submission, ok := doc.(models.FormSubmission)
	if !ok {
		return fakeInsertResult{id: primitive.NewObjectID()}, nil
	}
	for _, existing := range f.submissions {
		if existing.Details.CommunityID == submission.Details.CommunityID &&
			existing.Details.ReportNumber == submission.Details.ReportNumber {
			return nil, fakeDuplicateKeyError(submission.Details.ReportNumber)
		}
	}
	f.submissions = append(f.submissions, submission)
	return fakeInsertResult{id: submission.ID}, nil
}

// FindOne answers the one query reportNumbering asks it: the highest
// reportNumber used in a community, when it is recovering from a collision.
func (f *fakeFormSubmissionDB) FindOne(_ context.Context, _ interface{}, _ ...*options.FindOneOptions) (*models.FormSubmission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var top *models.FormSubmission
	for i := range f.submissions {
		if top == nil || f.submissions[i].Details.ReportNumber > top.Details.ReportNumber {
			top = &f.submissions[i]
		}
	}
	if top == nil {
		return nil, mongo.ErrNoDocuments
	}
	out := *top
	return &out, nil
}

// fakeDuplicateKeyError is shaped so mongo.IsDuplicateKeyError recognises it,
// which is what the retry in reportNumbering.Insert keys off.
func fakeDuplicateKeyError(reportNumber string) error {
	return mongo.WriteException{WriteErrors: mongo.WriteErrors{{
		Code:    11000,
		Message: "E11000 duplicate key error: reportNumber " + reportNumber,
	}}}
}

// fakeFormCounterDB is the per-(community, slug, year) sequence, with the same
// $max catch-up the real one has.
type fakeFormCounterDB struct {
	mu   sync.Mutex
	seqs map[string]int64
}

func newFakeFormCounterDB() *fakeFormCounterDB {
	return &fakeFormCounterDB{seqs: map[string]int64{}}
}

func (f *fakeFormCounterDB) key(communityID, slug string, year int) string {
	return communityID + "|" + slug + "|" + strconv.Itoa(year)
}

func (f *fakeFormCounterDB) NextSeq(_ context.Context, communityID, slug string, year int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := f.key(communityID, slug, year)
	f.seqs[key]++
	return f.seqs[key], nil
}

func (f *fakeFormCounterDB) CatchUp(_ context.Context, communityID, slug string, year int, minSeq int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := f.key(communityID, slug, year)
	if f.seqs[key] < minSeq {
		f.seqs[key] = minSeq
	}
	return nil
}

// reviewFixture is the report fixture with the stores a reviewer's decision
// writes to, and the gateway secret configured so the first-party gate is
// actually exercised rather than skipped.
type reviewFixture struct {
	*reportFixture
	arrestDB  *fakeArrestReportDB
	formDB    *fakeFormSubmissionDB
	counterDB *fakeFormCounterDB
}

func newReviewFixture(t *testing.T) *reviewFixture {
	t.Helper()
	base := newReportFixture(t)
	t.Setenv(apiGatewayKeyEnv, "test-gateway-secret")

	arrestDB := &fakeArrestReportDB{}
	formDB := &fakeFormSubmissionDB{}
	counterDB := newFakeFormCounterDB()
	base.api.ArrestReportDB = arrestDB
	base.api.FormSubmissionDB = formDB
	base.api.FormCounterDB = counterDB
	return &reviewFixture{
		reportFixture: base, arrestDB: arrestDB, formDB: formDB, counterDB: counterDB,
	}
}

// reviewRun drives a first-party review route: gateway secret in the header,
// the acting user in the query, the way the website's own server route calls it.
func (f *reviewFixture) reviewRun(t *testing.T, method, path, body string, vars map[string]string, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.Header.Set(apiGatewayHeader, "test-gateway-secret")
	req = mux.SetURLVars(req, vars)
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr
}

func (f *reviewFixture) draftIDOf(t *testing.T, externalID string) string {
	t.Helper()
	f.draftDB.mu.Lock()
	defer f.draftDB.mu.Unlock()
	for _, draft := range f.draftDB.drafts {
		if draft.ExternalID == externalID {
			return draft.ID.Hex()
		}
	}
	t.Fatalf("no draft with external id %q", externalID)
	return ""
}

// Without the gateway secret these routes refuse, because the permission check
// underneath them resolves the acting user from a query parameter the caller
// controls. On an endpoint that files an arrest report, that parameter alone is
// not a basis for anything.
func TestE2E_ReviewRoutesRequireTheFirstPartySecret(t *testing.T) {
	f := newReviewFixture(t)
	f.upsertDraft(t, `{"externalId":"rev-1","source":"SixPD","kind":"arrest",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)
	draftID := f.draftIDOf(t, "rev-1")

	// No X-API-Key header.
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/developer/community/"+f.community.ID.Hex()+"/report-drafts/"+draftID+"/file?userId="+f.ownerID,
		strings.NewReader(`{}`))
	req = mux.SetURLVars(req, map[string]string{
		"communityId": f.community.ID.Hex(), "draftId": draftID,
	})
	rr := httptest.NewRecorder()
	f.api.FileCommunityReportDraftHandler(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without the first-party secret, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(f.arrestDB.reports) != 0 {
		t.Error("nothing should have been filed")
	}
}

// Reviewing is gated on the community's own "manage records", so a plain member
// cannot file a report however they reach the route.
func TestE2E_ReviewRefusesSomeoneWithoutManageRecords(t *testing.T) {
	f := newReviewFixture(t)
	f.upsertDraft(t, `{"externalId":"rev-2","source":"SixPD","kind":"arrest",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)
	draftID := f.draftIDOf(t, "rev-2")

	rr := f.reviewRun(t, http.MethodPost,
		"/api/v1/developer/community/"+f.community.ID.Hex()+"/report-drafts/"+draftID+"/file?userId="+f.plainID,
		`{}`, map[string]string{"communityId": f.community.ID.Hex(), "draftId": draftID},
		f.api.FileCommunityReportDraftHandler)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a plain member, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(f.arrestDB.reports) != 0 {
		t.Error("nothing should have been filed")
	}
}

// Filing an arrest draft writes into the collection the CAD itself reads, so
// the result behaves like a report an officer typed.
func TestE2E_FilingAnArrestDraftWritesARealArrestReport(t *testing.T) {
	f := newReviewFixture(t)
	f.upsertDraft(t, `{
		"externalId":"rev-3","source":"SixPD","kind":"arrest",
		"reportingOfficer":"`+fivemRef("admin")+`",
		"subject":{"player":"`+fivemRef("member")+`","displayName":"John Doe"},
		"facts":{"location":"Route 68","summary":"Pursuit ended at the rail crossing."},
		"narrative":{"text":"The driver accelerated away.","generated":true,"generatedBy":"sixpd-llm"},
		"charges":["Reckless Driving"]
	}`)
	draftID := f.draftIDOf(t, "rev-3")

	rr := f.reviewRun(t, http.MethodPost,
		"/api/v1/developer/community/"+f.community.ID.Hex()+"/report-drafts/"+draftID+"/file?userId="+f.ownerID,
		`{"notes":"Checked the clip, looks right."}`,
		map[string]string{"communityId": f.community.ID.Hex(), "draftId": draftID},
		f.api.FileCommunityReportDraftHandler)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	f.arrestDB.mu.Lock()
	defer f.arrestDB.mu.Unlock()
	if len(f.arrestDB.reports) != 1 {
		t.Fatalf("expected one arrest report, got %d", len(f.arrestDB.reports))
	}
	filed := f.arrestDB.reports[0].Details
	if filed.ActiveCommunityID != f.community.ID.Hex() {
		t.Errorf("the report must belong to the community, got %q", filed.ActiveCommunityID)
	}
	// The officer on the report is the one who observed it, not the reviewer.
	// Putting the reviewer there would misattribute the arrest.
	if filed.OfficerID != f.adminID {
		t.Errorf("the reporting officer must be the officer on the report, got %q", filed.OfficerID)
	}
	if len(filed.ChargesList) != 1 || filed.ChargesList[0].Amount != 1000 {
		t.Errorf("the priced charges must carry across, got %+v", filed.ChargesList)
	}
	// The generated label travels into the record. Whoever reads this in three
	// months should be able to see a model wrote it.
	if !strings.Contains(filed.Narrative, "generated by") {
		t.Errorf("a generated narrative must stay labelled in the filed report, got %q", filed.Narrative)
	}
	if !strings.Contains(filed.Narrative, "Not evidence") {
		t.Errorf("the filed narrative must not read as evidence, got %q", filed.Narrative)
	}
	if !strings.Contains(filed.Narrative, "Pursuit ended") {
		t.Errorf("the observed summary must survive filing, got %q", filed.Narrative)
	}
}

// The draft then points at what was filed, which is how the integration finds
// out without being told.
func TestE2E_FilingClosesTheLoopForTheIntegration(t *testing.T) {
	f := newReviewFixture(t)
	f.upsertDraft(t, `{"externalId":"rev-4","source":"SixPD","kind":"arrest",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)
	draftID := f.draftIDOf(t, "rev-4")

	f.reviewRun(t, http.MethodPost,
		"/api/v1/developer/community/"+f.community.ID.Hex()+"/report-drafts/"+draftID+"/file?userId="+f.ownerID,
		`{}`, map[string]string{"communityId": f.community.ID.Hex(), "draftId": draftID},
		f.api.FileCommunityReportDraftHandler)

	// Now read it back the way the integration does.
	rr := f.run(t, http.MethodGet, "/reports/drafts/rev-4?source=SixPD", "",
		map[string]string{"externalId": "rev-4"},
		f.api.RequireScope(models.ScopeReportsRead, f.api.DevGetReportDraftHandler))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out draftEnvelope
	_ = json.Unmarshal(rr.Body.Bytes(), &out)

	if out.Draft.Status != models.ReportDraftStatusSubmitted {
		t.Errorf("expected submitted, got %q", out.Draft.Status)
	}
	if !out.Draft.Filed {
		t.Error("filed must be true once a person has filed it")
	}
	if out.Draft.FiledRecordID == "" || out.Draft.FiledRecordKind != "arrestReport" {
		t.Errorf("the draft must point at what was filed, got %+v", out.Draft)
	}
	if out.Draft.ReviewedByName == "" {
		t.Error("the reviewer's name should be reported back")
	}
}

// A double-click must not produce two arrest reports.
func TestE2E_FilingTwiceDoesNotDuplicateTheRecord(t *testing.T) {
	f := newReviewFixture(t)
	f.upsertDraft(t, `{"externalId":"rev-5","source":"SixPD","kind":"arrest",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)
	draftID := f.draftIDOf(t, "rev-5")
	vars := map[string]string{"communityId": f.community.ID.Hex(), "draftId": draftID}
	path := "/api/v1/developer/community/" + f.community.ID.Hex() + "/report-drafts/" + draftID + "/file?userId=" + f.ownerID

	first := f.reviewRun(t, http.MethodPost, path, `{}`, vars, f.api.FileCommunityReportDraftHandler)
	second := f.reviewRun(t, http.MethodPost, path, `{}`, vars, f.api.FileCommunityReportDraftHandler)

	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("both calls should answer 200, got %d and %d", first.Code, second.Code)
	}
	if len(f.arrestDB.reports) != 1 {
		t.Fatalf("expected one arrest report, got %d", len(f.arrestDB.reports))
	}
	var out struct {
		Filed  bool                   `json:"filed"`
		Record map[string]interface{} `json:"record"`
	}
	_ = json.Unmarshal(second.Body.Bytes(), &out)
	if out.Filed {
		t.Error("the second call must report filing nothing")
	}
	if out.Record["id"] == "" {
		t.Error("the second call must still hand back the record that exists")
	}
}

// An incident draft files as a form submission against the community's form,
// or the built-in Incident Report when it named none.
func TestE2E_FilingAnIncidentDraftWritesAFormSubmission(t *testing.T) {
	f := newReviewFixture(t)
	f.upsertDraft(t, `{"externalId":"rev-6","source":"SixPD","kind":"incident",
		"reportingOfficer":"`+fivemRef("admin")+`",
		"facts":{"location":"Mirror Park","summary":"Shots heard.","fields":{"weather":"clear"}}}`)
	draftID := f.draftIDOf(t, "rev-6")

	rr := f.reviewRun(t, http.MethodPost,
		"/api/v1/developer/community/"+f.community.ID.Hex()+"/report-drafts/"+draftID+"/file?userId="+f.ownerID,
		`{}`, map[string]string{"communityId": f.community.ID.Hex(), "draftId": draftID},
		f.api.FileCommunityReportDraftHandler)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	f.formDB.mu.Lock()
	defer f.formDB.mu.Unlock()
	if len(f.formDB.submissions) != 1 {
		t.Fatalf("expected one form submission, got %d", len(f.formDB.submissions))
	}
	filed := f.formDB.submissions[0].Details
	if filed.FormTemplateSlug != "incident-report" {
		t.Errorf("a draft with no template should file against the built-in form, got %q", filed.FormTemplateSlug)
	}
	if filed.Status != "submitted" {
		t.Errorf("expected submitted, got %q", filed.Status)
	}
	// The template's own answers survive, and so does the draft's structure,
	// so nothing is lost when a community's form has no matching question.
	if filed.Data["weather"] != "clear" {
		t.Errorf("the template answers were dropped: %+v", filed.Data)
	}
	if filed.Data["lpcReportedSummary"] != "Shots heard." {
		t.Errorf("the observed summary was dropped: %+v", filed.Data)
	}
	if filed.ReportNumber == "" {
		t.Error("a filed report needs a report number; the collection's unique index requires it")
	}
	if len(f.arrestDB.reports) != 0 {
		t.Error("an incident draft must not produce an arrest report")
	}
}

// Two drafts filed in the same community both become records.
//
// This is the production regression. formSubmissions carries a unique index on
// (communityID, reportNumber) and the review path allocated no number, so the
// first filing in a community wrote reportNumber:"" and succeeded, and the
// second collided and answered 500 "failed to file report draft". A reviewer
// saw a banner, the draft stayed open, and nothing they did helped.
func TestE2E_TwoDraftsFiledInOneCommunityBothBecomeRecords(t *testing.T) {
	f := newReviewFixture(t)

	for _, externalID := range []string{"rev-dup-1", "rev-dup-2"} {
		f.upsertDraft(t, `{"externalId":"`+externalID+`","source":"SixPD","kind":"incident",
			"reportingOfficer":"`+fivemRef("admin")+`",
			"facts":{"summary":"Shots heard."}}`)
		draftID := f.draftIDOf(t, externalID)
		rr := f.reviewRun(t, http.MethodPost,
			"/api/v1/developer/community/"+f.community.ID.Hex()+"/report-drafts/"+draftID+"/file?userId="+f.ownerID,
			`{}`, map[string]string{"communityId": f.community.ID.Hex(), "draftId": draftID},
			f.api.FileCommunityReportDraftHandler)
		if rr.Code != http.StatusOK {
			t.Fatalf("filing %s: expected 200, got %d: %s", externalID, rr.Code, rr.Body.String())
		}
	}

	f.formDB.mu.Lock()
	defer f.formDB.mu.Unlock()
	if len(f.formDB.submissions) != 2 {
		t.Fatalf("expected two form submissions, got %d", len(f.formDB.submissions))
	}
	first := f.formDB.submissions[0].Details.ReportNumber
	second := f.formDB.submissions[1].Details.ReportNumber
	if first == "" || second == "" {
		t.Fatalf("both filings need a report number, got %q and %q", first, second)
	}
	if first == second {
		t.Errorf("two reports cannot share a number, both are %q", first)
	}
	// Numbered the way the website's own form handler numbers them, because a
	// filed draft is meant to be indistinguishable from a typed report.
	for _, number := range []string{first, second} {
		if !strings.HasPrefix(number, "RR-") {
			t.Errorf("expected the built-in Incident Report format, got %q", number)
		}
	}
}

// A number the counter hands out that is already taken is renumbered, not
// failed. The counter is per (community, slug, year) while the index is
// community-wide, so a community whose numbers came from another template can
// hand out one that already exists.
func TestE2E_FilingRenumbersRoundACollision(t *testing.T) {
	f := newReviewFixture(t)

	// Whatever the counter is about to produce, already used by something else
	// in this community.
	taken, err := reportNumbering{
		Subs: f.formDB, Counters: f.counterDB, Templates: f.api.FormTemplateDB,
	}.NextNumber(context.Background(), f.community.ID.Hex(), "incident-report")
	if err != nil {
		t.Fatalf("allocating the colliding number: %v", err)
	}
	f.counterDB.mu.Lock()
	f.counterDB.seqs = map[string]int64{}
	f.counterDB.mu.Unlock()
	f.formDB.mu.Lock()
	f.formDB.submissions = append(f.formDB.submissions, models.FormSubmission{
		ID: primitive.NewObjectID(),
		Details: models.FormSubmissionDetails{
			CommunityID:  f.community.ID.Hex(),
			ReportNumber: taken,
		},
	})
	f.formDB.mu.Unlock()

	f.upsertDraft(t, `{"externalId":"rev-collide","source":"SixPD","kind":"incident",
		"reportingOfficer":"`+fivemRef("admin")+`","facts":{"summary":"Shots heard."}}`)
	draftID := f.draftIDOf(t, "rev-collide")
	rr := f.reviewRun(t, http.MethodPost,
		"/api/v1/developer/community/"+f.community.ID.Hex()+"/report-drafts/"+draftID+"/file?userId="+f.ownerID,
		`{}`, map[string]string{"communityId": f.community.ID.Hex(), "draftId": draftID},
		f.api.FileCommunityReportDraftHandler)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	f.formDB.mu.Lock()
	defer f.formDB.mu.Unlock()
	if len(f.formDB.submissions) != 2 {
		t.Fatalf("expected the seeded report plus the filed one, got %d", len(f.formDB.submissions))
	}
	filed := f.formDB.submissions[1].Details.ReportNumber
	if filed == taken {
		t.Errorf("the filed report reused the taken number %q", filed)
	}
	if filed == "" {
		t.Error("the filed report has no number at all")
	}
}

// A rejection needs a reason, because the integration shows it to the officer.
func TestE2E_RejectingADraftNeedsAReason(t *testing.T) {
	f := newReviewFixture(t)
	f.upsertDraft(t, `{"externalId":"rev-7","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)
	draftID := f.draftIDOf(t, "rev-7")
	vars := map[string]string{"communityId": f.community.ID.Hex(), "draftId": draftID}
	path := "/api/v1/developer/community/" + f.community.ID.Hex() + "/report-drafts/" + draftID + "/reject?userId=" + f.ownerID

	empty := f.reviewRun(t, http.MethodPost, path, `{}`, vars, f.api.RejectCommunityReportDraftHandler)
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 with no reason, got %d: %s", empty.Code, empty.Body.String())
	}

	ok := f.reviewRun(t, http.MethodPost, path,
		`{"notes":"The narrative does not match the clip."}`, vars,
		f.api.RejectCommunityReportDraftHandler)
	if ok.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", ok.Code, ok.Body.String())
	}

	// And the integration can read the reason.
	read := f.run(t, http.MethodGet, "/reports/drafts/rev-7?source=SixPD", "",
		map[string]string{"externalId": "rev-7"},
		f.api.RequireScope(models.ScopeReportsRead, f.api.DevGetReportDraftHandler))
	var out draftEnvelope
	_ = json.Unmarshal(read.Body.Bytes(), &out)
	if out.Draft.Status != models.ReportDraftStatusRejected {
		t.Errorf("expected rejected, got %q", out.Draft.Status)
	}
	if !strings.Contains(out.Draft.ReviewNotes, "does not match") {
		t.Errorf("the reason must reach the integration, got %q", out.Draft.ReviewNotes)
	}
	if out.Draft.Filed {
		t.Error("a rejected draft is not filed")
	}
}

// A draft id from another community is a 404, not a 403 after a read.
func TestE2E_ReviewingAnotherCommunitysDraftIsNotFound(t *testing.T) {
	f := newReviewFixture(t)
	f.upsertDraft(t, `{"externalId":"rev-8","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)
	draftID := f.draftIDOf(t, "rev-8")

	// Same draft id, but asked for under a community the caller does own and
	// the draft does not belong to. Seeded so the permission check passes and
	// the scoping is what decides.
	other := models.Community{ID: primitive.NewObjectID()}
	other.Details.OwnerID = f.ownerID
	if err := f.communityDB.seed(other); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	rr := f.reviewRun(t, http.MethodGet,
		"/api/v1/developer/community/"+other.ID.Hex()+"/report-drafts/"+draftID+"?userId="+f.ownerID,
		"", map[string]string{"communityId": other.ID.Hex(), "draftId": draftID},
		f.api.GetCommunityReportDraftHandler)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// The queue a reviewer opens is the drafts nobody has acted on, and it names
// the integration that wrote each one — a community owner's first question.
func TestE2E_ReviewQueueDefaultsToUnreviewedAndNamesTheKey(t *testing.T) {
	f := newReviewFixture(t)
	f.upsertDraft(t, `{"externalId":"rev-9","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)
	f.upsertDraft(t, `{"externalId":"rev-10","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)

	// One of them gets withdrawn, so it should drop out of the queue.
	f.run(t, http.MethodDelete, "/reports/drafts/rev-10?source=SixPD", "",
		map[string]string{"externalId": "rev-10"},
		f.api.RequireScope(models.ScopeReportsWrite, f.api.DevWithdrawReportDraftHandler))

	rr := f.reviewRun(t, http.MethodGet,
		"/api/v1/developer/community/"+f.community.ID.Hex()+"/report-drafts?userId="+f.ownerID,
		"", map[string]string{"communityId": f.community.ID.Hex()},
		f.api.ListCommunityReportDraftsHandler)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var out struct {
		Data []struct {
			Draft            devReportDraftView `json:"draft"`
			WrittenByKeyID   string             `json:"writtenByKeyId"`
			WrittenByKeyName string             `json:"writtenByKeyName"`
		} `json:"data"`
		TotalCount int64 `json:"totalCount"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(out.Data) != 1 || out.Data[0].Draft.ExternalID != "rev-9" {
		t.Errorf("the queue should hold only the unreviewed draft, got %+v", out.Data)
	}
	if out.Data[0].WrittenByKeyID != f.keyID.Hex() {
		t.Errorf("the queue must name the key that wrote it, got %q", out.Data[0].WrittenByKeyID)
	}
	if out.Data[0].WrittenByKeyName == "" {
		t.Error("the key's name is what an owner can act on; an id alone is not")
	}
}

// ---- query parameters that become part of a query ----

// A list filter is built from our own constants, never from the caller's
// string. The characters are identical either way; what differs is where the
// value in the query came from, and that is the thing a reader — or a static
// analyser — has to be able to follow.
//
// Checking a value and then using the original is the mistake these helpers
// exist to make impossible, so the test asserts the returned value is the
// package's own constant rather than merely equal to it.
func TestE2E_ListFiltersComeFromOurOwnConstants(t *testing.T) {
	t.Run("report kind", func(t *testing.T) {
		for raw, want := range map[string]string{
			"arrest":     models.ReportDraftKindArrest,
			"ARREST":     models.ReportDraftKindArrest,
			" citation ": models.ReportDraftKindCitation,
			"incident":   models.ReportDraftKindIncident,
		} {
			got, err := devSafeReportKind(raw)
			if err != nil {
				t.Fatalf("%q: unexpected error %v", raw, err)
			}
			if got != want {
				t.Errorf("%q: got %q want the constant %q", raw, got, want)
			}
		}
		for _, bad := range []string{"", "arrests", "$ne", "incident;drop", "report"} {
			if got, err := devSafeReportKind(bad); err == nil {
				t.Errorf("%q must be refused, got %q", bad, got)
			}
		}
	})

	t.Run("report status", func(t *testing.T) {
		got, err := devSafeReportStatus("SUBMITTED")
		if err != nil || got != models.ReportDraftStatusSubmitted {
			t.Errorf("got %q, %v", got, err)
		}
		if _, err := devSafeReportStatus("filed"); err == nil {
			t.Error("a status we do not use must be refused")
		}
	})

	t.Run("warrant status", func(t *testing.T) {
		got, err := devSafeWarrantStatus("Approved")
		if err != nil || got != models.WarrantStatusApproved {
			t.Errorf("got %q, %v", got, err)
		}
		if _, err := devSafeWarrantStatus("accepted"); err == nil {
			t.Error("a status we do not use must be refused")
		}
	})

	t.Run("warrant type", func(t *testing.T) {
		// Empty defaults to arrest, which is what nearly every request is.
		got, err := devSafeWarrantType("")
		if err != nil || got != devWarrantTypeArrest {
			t.Errorf("empty should default to arrest, got %q, %v", got, err)
		}
		if got, _ := devSafeWarrantType("SEARCH"); got != devWarrantTypeSearch {
			t.Errorf("got %q", got)
		}
		if _, err := devSafeWarrantType("civil"); err == nil {
			t.Error("a type we do not issue must be refused")
		}
	})

	t.Run("activity outcome", func(t *testing.T) {
		got, err := devSafeActivityOutcome("DENIED")
		if err != nil || got != models.DeveloperActivityDenied {
			t.Errorf("got %q, %v", got, err)
		}
		if _, err := devSafeActivityOutcome("refused"); err == nil {
			t.Error("an outcome we do not record must be refused")
		}
	})

	t.Run("event types", func(t *testing.T) {
		got, err := devSafeEventTypes("call.created,call.closed")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 || got[0] != models.EventCallCreated || got[1] != models.EventCallClosed {
			t.Errorf("got %+v", got)
		}
		if _, err := devSafeEventTypes("call.close"); err == nil {
			t.Error("a typo must be refused rather than matching nothing")
		}
		if _, err := devSafeEventTypes(""); err == nil {
			t.Error("an empty list must be refused rather than matching everything")
		}
	})

	t.Run("webhook subscription", func(t *testing.T) {
		got, err := validateWebhookEvents([]string{"call.created", "call.created"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Deduplicated, and the value is ours.
		if len(got) != 1 || got[0] != models.EventCallCreated {
			t.Errorf("got %+v", got)
		}
		if _, err := validateWebhookEvents([]string{"call.close"}); err == nil {
			t.Error("a typo must be refused at save time rather than never firing")
		}
	})

}

// The list handlers go through those helpers, not around them. A filter value
// that arrived from a caller and was only bool-checked is the shape this test
// is here to catch.
func TestE2E_ListHandlersRefuseAnUnknownKind(t *testing.T) {
	f := newReportFixture(t)

	rr := f.run(t, http.MethodGet, "/reports/drafts?kind=arrests", "", nil,
		f.api.RequireScope(models.ScopeReportsRead, f.api.DevListReportDraftsHandler))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_kind") {
		t.Errorf("got %s", rr.Body.String())
	}
}

// ============================================================================
// Phase 3b: the activity feed, the change cursor, webhooks, attachments.
//
// Items 4-6 of Hiceguy's follow-up. The common thread in these tests is that a
// developer has to be able to find out what happened without asking us, and
// that nothing we push out is a second source of truth — a missed delivery
// degrades to the polling case rather than to a gap.
// ============================================================================

type fakeActivityDB struct {
	databases.DeveloperActivityDatabase
	mu   sync.Mutex
	rows []models.DeveloperAPIActivity
}

func (f *fakeActivityDB) InsertOne(_ context.Context, row models.DeveloperAPIActivity) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, row)
	return fakeInsertResult{id: row.ID}, nil
}

func (f *fakeActivityDB) Find(_ context.Context, filter interface{}, _ ...*options.FindOptions) ([]models.DeveloperAPIActivity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	out := []models.DeveloperAPIActivity{}
	for _, row := range f.rows {
		if v, ok := devFilterString(m["keyId"]); ok && row.KeyID != v {
			continue
		}
		if v, ok := devFilterString(m["communityId"]); ok && row.CommunityID != v {
			continue
		}
		if v, ok := devFilterString(m["outcome"]); ok && row.Outcome != v {
			continue
		}
		if v, ok := devFilterString(m["requestId"]); ok && row.RequestID != v {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeActivityDB) CountDocuments(ctx context.Context, filter interface{}, _ ...*options.CountOptions) (int64, error) {
	rows, err := f.Find(ctx, filter)
	return int64(len(rows)), err
}

func (f *fakeActivityDB) snapshot() []models.DeveloperAPIActivity {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]models.DeveloperAPIActivity{}, f.rows...)
}

type fakeEventDB struct {
	databases.DeveloperEventDatabase
	mu     sync.Mutex
	events []models.DeveloperAPIEvent
}

func (f *fakeEventDB) InsertOne(_ context.Context, event models.DeveloperAPIEvent) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
	return fakeInsertResult{id: event.ID}, nil
}

func (f *fakeEventDB) Find(_ context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.DeveloperAPIEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)

	out := []models.DeveloperAPIEvent{}
	for _, event := range f.events {
		if v, ok := devFilterString(m["communityId"]); ok && event.CommunityID != v {
			continue
		}
		if cond, ok := m["_id"].(bson.M); ok {
			if gt, has := cond["$gt"].(primitive.ObjectID); has && !(event.ID.Hex() > gt.Hex()) {
				continue
			}
		}
		if cond, ok := m["type"].(bson.M); ok {
			if wanted, has := cond["$in"].([]string); has && !containsString(wanted, event.Type) {
				continue
			}
		}
		if cond, ok := m["actorKeyId"].(bson.M); ok {
			if ne, has := cond["$ne"].(string); has && event.ActorKeyID == ne {
				continue
			}
		}
		out = append(out, event)
	}

	// Honour the sort direction the handler asked for, because the cursor's
	// correctness depends on it: ascending to walk forward, descending to find
	// the newest when priming.
	descending := false
	if len(opts) > 0 && opts[0] != nil && opts[0].Sort != nil {
		if d, ok := opts[0].Sort.(bson.D); ok && len(d) > 0 {
			if v, isInt := d[0].Value.(int); isInt && v < 0 {
				descending = true
			}
		}
	}
	sortEventsByID(out, descending)

	if len(opts) > 0 && opts[0] != nil && opts[0].Limit != nil {
		if n := int(*opts[0].Limit); n < len(out) {
			out = out[:n]
		}
	}
	return out, nil
}

func sortEventsByID(events []models.DeveloperAPIEvent, descending bool) {
	for i := 1; i < len(events); i++ {
		for j := i; j > 0; j-- {
			before := events[j].ID.Hex() < events[j-1].ID.Hex()
			if descending {
				before = events[j].ID.Hex() > events[j-1].ID.Hex()
			}
			if !before {
				break
			}
			events[j], events[j-1] = events[j-1], events[j]
		}
	}
}

func (f *fakeEventDB) CountDocuments(ctx context.Context, filter interface{}, _ ...*options.CountOptions) (int64, error) {
	rows, err := f.Find(ctx, filter)
	return int64(len(rows)), err
}

func (f *fakeEventDB) waitForEvents(t *testing.T, want int) []models.DeveloperAPIEvent {
	t.Helper()
	// Events are emitted off the request path, so the test waits rather than
	// asserting immediately. Short, bounded, and a failure says how many
	// arrived.
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		got := append([]models.DeveloperAPIEvent{}, f.events...)
		f.mu.Unlock()
		if len(got) >= want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected at least %d events, got %d: %+v", want, len(got), got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type fakeWebhookDB struct {
	databases.DeveloperWebhookDatabase
	mu    sync.Mutex
	hooks []models.DeveloperWebhook
}

func (f *fakeWebhookDB) FindOne(_ context.Context, filter interface{}) (*models.DeveloperWebhook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	for _, hook := range f.hooks {
		if oid, ok := devFilterObjectID(m["_id"]); ok && hook.ID != oid {
			continue
		}
		if v, ok := devFilterString(m["keyId"]); ok && hook.KeyID != v {
			continue
		}
		found := hook
		return &found, nil
	}
	return nil, mongo.ErrNoDocuments
}

func (f *fakeWebhookDB) Find(_ context.Context, filter interface{}, _ ...*options.FindOptions) ([]models.DeveloperWebhook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	out := []models.DeveloperWebhook{}
	for _, hook := range f.hooks {
		if v, ok := devFilterString(m["keyId"]); ok && hook.KeyID != v {
			continue
		}
		out = append(out, hook)
	}
	return out, nil
}

func (f *fakeWebhookDB) InsertOne(_ context.Context, hook models.DeveloperWebhook) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hooks = append(f.hooks, hook)
	return fakeInsertResult{id: hook.ID}, nil
}

func (f *fakeWebhookDB) UpdateOne(_ context.Context, filter, update interface{}, _ ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fm, _ := filter.(bson.M)
	um, _ := update.(bson.M)
	set, _ := um["$set"].(bson.M)
	oid, _ := devFilterObjectID(fm["_id"])

	for i := range f.hooks {
		if f.hooks[i].ID != oid {
			continue
		}
		if v, ok := set["url"].(string); ok {
			f.hooks[i].URL = v
		}
		if v, ok := set["events"].([]string); ok {
			f.hooks[i].Events = v
		}
		if v, ok := set["communities"].([]string); ok {
			f.hooks[i].Communities = v
		}
		if v, ok := set["status"].(string); ok {
			f.hooks[i].Status = v
		}
		if v, ok := set["secretHash"].(string); ok {
			f.hooks[i].SecretHash = v
		}
		if v, ok := set["secretPrefix"].(string); ok {
			f.hooks[i].SecretPrefix = v
		}
		if v, ok := set["consecutiveFailures"].(int); ok {
			f.hooks[i].ConsecutiveFailures = v
		}
		if v, ok := set["disabledReason"].(string); ok {
			f.hooks[i].DisabledReason = v
		}
		return &mongo.UpdateResult{ModifiedCount: 1}, nil
	}
	return &mongo.UpdateResult{}, nil
}

func (f *fakeWebhookDB) DeleteOne(_ context.Context, filter interface{}, _ ...*options.DeleteOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	oid, _ := devFilterObjectID(m["_id"])
	kept := f.hooks[:0:0]
	for _, hook := range f.hooks {
		if hook.ID == oid {
			continue
		}
		kept = append(kept, hook)
	}
	f.hooks = kept
	return nil
}

func (f *fakeWebhookDB) CountDocuments(ctx context.Context, filter interface{}, _ ...*options.CountOptions) (int64, error) {
	rows, err := f.Find(ctx, filter)
	return int64(len(rows)), err
}

// eventsFixture is the phase-3 stack with the activity, event and webhook
// stores in place.
type eventsFixture struct {
	*reportFixture
	activityDB *fakeActivityDB
	eventDB    *fakeEventDB
	webhookDB  *fakeWebhookDB
}

func newEventsFixture(t *testing.T) *eventsFixture {
	t.Helper()
	base := newReportFixture(t)

	activityDB := &fakeActivityDB{}
	eventDB := &fakeEventDB{}
	webhookDB := &fakeWebhookDB{}
	base.api.ActivityDB = activityDB
	base.api.EventDB = eventDB
	base.api.WebhookDB = webhookDB

	return &eventsFixture{
		reportFixture: base,
		activityDB:    activityDB,
		eventDB:       eventDB,
		webhookDB:     webhookDB,
	}
}

// ---- item 4: the activity feed ----

// Every response carries a request id, including the ones the middleware
// refuses. A 401 that cannot be traced is the one a developer most needs to
// trace.
func TestE2E_EveryResponseCarriesARequestID(t *testing.T) {
	f := newEventsFixture(t)

	t.Run("on a success", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/whoami", nil)
		req.Header.Set("Authorization", "Bearer "+f.plaintext)
		f.api.RequireDeveloperAPIKey(http.HandlerFunc(f.api.DevWhoamiHandler)).ServeHTTP(rr, req)

		if rr.Header().Get(devRequestIDHeader) == "" {
			t.Error("a successful response must carry a request id")
		}
	})

	t.Run("on a refusal", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/whoami", nil)
		req.Header.Set("Authorization", "Bearer lpc_live_not_a_real_key")
		f.api.RequireDeveloperAPIKey(http.HandlerFunc(f.api.DevWhoamiHandler)).ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
		if rr.Header().Get(devRequestIDHeader) == "" {
			t.Error("a refused response must carry a request id too; that is the one worth tracing")
		}
	})
}

// A caller's own id is echoed beside ours, never in place of it.
func TestE2E_CorrelationIDIsEchoedNotTrusted(t *testing.T) {
	f := newEventsFixture(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	req.Header.Set(devCorrelationIDHeader, "sixpd-tick-8821")
	f.api.RequireDeveloperAPIKey(http.HandlerFunc(f.api.DevWhoamiHandler)).ServeHTTP(rr, req)

	if got := rr.Header().Get(devCorrelationIDHeader); got != "sixpd-tick-8821" {
		t.Errorf("the caller's correlation id must come back, got %q", got)
	}
	// Ours is still there and is not the caller's.
	ours := rr.Header().Get(devRequestIDHeader)
	if ours == "" || ours == "sixpd-tick-8821" {
		t.Errorf("our own id must be separate from the caller's, got %q", ours)
	}

	rows := f.activityDB.snapshot()
	if len(rows) != 1 {
		t.Fatalf("expected one activity row, got %d", len(rows))
	}
	if rows[0].CorrelationID != "sixpd-tick-8821" {
		t.Errorf("the correlation id must be recorded, got %q", rows[0].CorrelationID)
	}
	if rows[0].RequestID != ours {
		t.Errorf("the row must carry the id we returned, got %q want %q", rows[0].RequestID, ours)
	}
}

// A header value we reflect back has to be safe to reflect. A correlation id
// with a newline in it is a response-splitting attempt and is dropped rather
// than echoed.
func TestE2E_CorrelationIDIsSanitised(t *testing.T) {
	// This is the one value we take from a caller and hand straight back in a
	// response header, so the class is the narrow machine-handle one rather
	// than "anything printable". Everything below is dropped entirely, not
	// cleaned up: a malformed correlation id is not worth guessing at.
	for _, bad := range []string{
		"has\r\nInjected: header",
		"starts-with-$operator",
		`<script>alert(1)</script>`,
		`has"a quote`,
		"has a space",
		"has/a/slash",
	} {
		if got := safeCorrelationID(bad); got != "" {
			t.Errorf("%q must be dropped, got %q", bad, got)
		}
	}

	// An oversized one is bounded rather than dropped. The characters are
	// acceptable; there are just too many of them, and truncating leaves the
	// developer something they can still match their own logs against.
	long := safeCorrelationID(strings.Repeat("x", maxCorrelationIDLen+50))
	if len(long) != maxCorrelationIDLen {
		t.Errorf("an oversized id should be bounded to %d, got %d", maxCorrelationIDLen, len(long))
	}

	// And an ordinary one survives unchanged, because the point is to be
	// usable rather than merely safe.
	for _, good := range []string{"sixpd-tick-8821", "run_42.3", "a:b-c_d", "ABC123"} {
		if got := safeCorrelationID(good); got != good {
			t.Errorf("%q should survive, got %q", good, got)
		}
	}
}

// The feed records the shape of the exchange and nothing of its contents.
func TestE2E_ActivityRecordsTheExchangeNotTheContents(t *testing.T) {
	f := newEventsFixture(t)

	f.upsertDraft(t, `{"externalId":"act-1","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`",
		"narrative":{"text":"something a reviewer would read"}}`)

	rows := f.activityDB.snapshot()
	if len(rows) == 0 {
		t.Fatal("the request was not recorded")
	}
	row := rows[len(rows)-1]

	if row.Method != http.MethodPost {
		t.Errorf("the method must be recorded, got %q", row.Method)
	}
	if row.Status != http.StatusCreated {
		t.Errorf("the status must be recorded, got %d", row.Status)
	}
	if row.Outcome != models.DeveloperActivityOK {
		t.Errorf("expected an ok outcome, got %q", row.Outcome)
	}
	if row.CommunityID != f.community.ID.Hex() {
		t.Errorf("the community must be recorded, got %q", row.CommunityID)
	}

	// Nothing from the body. This is the property the feed's whole design
	// rests on, so it is asserted rather than assumed.
	serialized := fmt.Sprintf("%+v", row)
	for _, secret := range []string{
		"something a reviewer would read",
		f.plaintext,
		"Bearer",
	} {
		if strings.Contains(serialized, secret) {
			t.Errorf("the activity row must not carry %q", secret)
		}
	}
}

// The route is the mux template, not the raw path. A feed keyed on raw paths
// would have as many distinct values as there are communities.
func TestE2E_ActivityRouteIsBounded(t *testing.T) {
	f := newEventsFixture(t)
	f.upsertDraft(t, `{"externalId":"act-2","reportingOfficer":"`+fivemRef("admin")+`"}`)

	rows := f.activityDB.snapshot()
	row := rows[len(rows)-1]
	if strings.Contains(row.Route, f.community.ID.Hex()) {
		t.Errorf("the route must not carry a community id, got %q", row.Route)
	}

	// The same property through the real router, which is where the template
	// actually comes from. The test above goes through mux.SetURLVars, so it
	// exercises the no-matched-route fallback — which must also be bounded.
	router := mux.NewRouter()
	var recorded string
	router.Handle("/api/v1/dev/communities/{communityId}/reports/drafts",
		http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
			recorded = devRouteTemplate(req)
		})).Methods("POST")

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/dev/communities/"+f.community.ID.Hex()+"/reports/drafts", nil)
	router.ServeHTTP(httptest.NewRecorder(), req)

	if recorded != "/communities/{communityId}/reports/drafts" {
		t.Errorf("the route must be the template, got %q", recorded)
	}
}

// A missing-scope refusal records which scope was missing, which is the thing
// a bare 403 on a row does not say.
func TestE2E_ActivityNamesTheMissingScope(t *testing.T) {
	f := newEventsFixture(t)
	f.keyDB.byHash = upgradeKeyScopes(f.keyDB.byHash, models.ScopeReportsRead)

	f.upsertDraft(t, `{"externalId":"act-3","reportingOfficer":"`+fivemRef("admin")+`"}`)

	rows := f.activityDB.snapshot()
	if len(rows) == 0 {
		t.Fatal("the refusal was not recorded")
	}
	row := rows[len(rows)-1]
	if row.Outcome != models.DeveloperActivityDenied {
		t.Errorf("expected a denied outcome, got %q", row.Outcome)
	}
	if row.ErrorCode != "missing_scope" {
		t.Errorf("the error code must be recorded, got %q", row.ErrorCode)
	}
	if row.RequiredScope != models.ScopeReportsWrite {
		t.Errorf("the missing scope must be named, got %q", row.RequiredScope)
	}
}

// A key reads its own feed and nobody else's, with no scope required — the
// keys that most need the diagnostic are the ones being refused.
func TestE2E_ActivityFeedIsTheKeysOwn(t *testing.T) {
	f := newEventsFixture(t)
	f.upsertDraft(t, `{"externalId":"act-4","reportingOfficer":"`+fivemRef("admin")+`"}`)

	// Another key's row, in the same store.
	f.activityDB.mu.Lock()
	f.activityDB.rows = append(f.activityDB.rows, models.DeveloperAPIActivity{
		ID: primitive.NewObjectID(), RequestID: "req_someone_else",
		KeyID: primitive.NewObjectID().Hex(), Route: "/whoami",
		Outcome: models.DeveloperActivityOK,
	})
	f.activityDB.mu.Unlock()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/activity", nil)
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	f.api.RequireDeveloperAPIKey(http.HandlerFunc(f.api.DevActivityHandler)).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Data          []devActivityView `json:"data"`
		RetentionDays int               `json:"retentionDays"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, row := range out.Data {
		if row.RequestID == "req_someone_else" {
			t.Fatal("a key must not see another key's activity")
		}
	}
	// The retention window is published in the response, not only in the docs.
	if out.RetentionDays != models.DeveloperAPIActivityRetentionDays {
		t.Errorf("the retention window must be stated, got %d", out.RetentionDays)
	}
}

// A revoked key cannot read the feed either.
func TestE2E_ActivityFeedRefusesARevokedKey(t *testing.T) {
	f := newEventsFixture(t)
	for hash, key := range f.keyDB.byHash {
		key.Status = models.DeveloperKeyRevoked
		f.keyDB.byHash[hash] = key
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/activity", nil)
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	f.api.RequireDeveloperAPIKey(http.HandlerFunc(f.api.DevActivityHandler)).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// ---- item 5a: the change cursor ----

// A first call returns nothing and hands back a position. Replaying a week of
// history to a client that is about to read current state anyway is not a
// service.
func TestE2E_ChangesFirstCallPrimesTheCursor(t *testing.T) {
	f := newEventsFixture(t)

	rr := f.run(t, http.MethodGet, "/changes", "", nil,
		f.api.RequireScope(models.ScopeCommunityRead, f.api.DevChangesHandler))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var out struct {
		Data          []devEventView `json:"data"`
		Cursor        string         `json:"cursor"`
		Primed        bool           `json:"primed"`
		RetentionDays int            `json:"retentionDays"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(out.Data) != 0 {
		t.Errorf("a first call must not replay history, got %d events", len(out.Data))
	}
	if out.Cursor == "" {
		t.Error("a first call must hand back a usable cursor")
	}
	if !out.Primed {
		t.Error("the response must say this was a priming call")
	}
	if out.RetentionDays != models.DeveloperEventRetentionDays {
		t.Errorf("the retention window must be stated, got %d", out.RetentionDays)
	}
	// Also in a header, so a Lua HTTP callback does not have to parse the body.
	if rr.Header().Get(devCursorHeader) == "" {
		t.Error("the cursor must be available as a header")
	}
}

// The cursor walks forward and never redelivers the event it names.
func TestE2E_ChangesCursorAdvancesWithoutRepeating(t *testing.T) {
	f := newEventsFixture(t)

	// Prime.
	first := f.run(t, http.MethodGet, "/changes", "", nil,
		f.api.RequireScope(models.ScopeCommunityRead, f.api.DevChangesHandler))
	var primed struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &primed)

	// Something happens.
	f.upsert(t, `{"externalId":"chg-1","source":"SixPD","title":"Traffic stop"}`)
	f.eventDB.waitForEvents(t, 1)

	second := f.run(t, http.MethodGet, "/changes?since="+primed.Cursor, "", nil,
		f.api.RequireScope(models.ScopeCommunityRead, f.api.DevChangesHandler))
	var page struct {
		Data   []devEventView `json:"data"`
		Cursor string         `json:"cursor"`
	}
	_ = json.Unmarshal(second.Body.Bytes(), &page)

	if len(page.Data) != 1 || page.Data[0].Type != models.EventCallCreated {
		t.Fatalf("expected one call.created, got %+v", page.Data)
	}
	if page.Data[0].ResourceExternalID != "chg-1" {
		t.Errorf("the integration's own id must be on the event, got %q", page.Data[0].ResourceExternalID)
	}
	if page.Cursor == primed.Cursor {
		t.Error("the cursor must advance")
	}

	// Asking again from the new cursor returns nothing. An inclusive bound
	// would redeliver one event on every poll forever.
	third := f.run(t, http.MethodGet, "/changes?since="+page.Cursor, "", nil,
		f.api.RequireScope(models.ScopeCommunityRead, f.api.DevChangesHandler))
	var empty struct {
		Data []devEventView `json:"data"`
	}
	_ = json.Unmarshal(third.Body.Bytes(), &empty)
	if len(empty.Data) != 0 {
		t.Errorf("the cursor must not redeliver what it names, got %+v", empty.Data)
	}
}

// A key can exclude its own writes, which is the difference between a working
// integration and a loop: close a call, receive your own "call.closed", close
// it again.
func TestE2E_ChangesCanExcludeTheKeysOwnWrites(t *testing.T) {
	f := newEventsFixture(t)

	first := f.run(t, http.MethodGet, "/changes", "", nil,
		f.api.RequireScope(models.ScopeCommunityRead, f.api.DevChangesHandler))
	var primed struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &primed)

	f.upsert(t, `{"externalId":"chg-2","source":"SixPD","title":"Mine"}`)
	f.eventDB.waitForEvents(t, 1)

	// Somebody else's change, in the same community.
	f.eventDB.mu.Lock()
	f.eventDB.events = append(f.eventDB.events, models.DeveloperAPIEvent{
		ID: primitive.NewObjectID(), Type: models.EventCallClosed,
		CommunityID: f.community.ID.Hex(), Actor: models.EventActorWebsite,
	})
	f.eventDB.mu.Unlock()

	rr := f.run(t, http.MethodGet, "/changes?since="+primed.Cursor+"&excludeSelf=true", "", nil,
		f.api.RequireScope(models.ScopeCommunityRead, f.api.DevChangesHandler))
	var out struct {
		Data []devEventView `json:"data"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)

	for _, event := range out.Data {
		if event.ActorKeyID == f.keyID.Hex() {
			t.Error("excludeSelf must leave out this key's own writes")
		}
	}
	if len(out.Data) != 1 || out.Data[0].Actor != models.EventActorWebsite {
		t.Errorf("the other actor's change must still arrive, got %+v", out.Data)
	}
}

// A type filter is validated against what we emit, so a typo is refused rather
// than silently matching nothing.
func TestE2E_ChangesRefusesAnUnknownEventType(t *testing.T) {
	f := newEventsFixture(t)

	rr := f.run(t, http.MethodGet, "/changes?since="+primitive.NewObjectID().Hex()+"&types=call.close", "", nil,
		f.api.RequireScope(models.ScopeCommunityRead, f.api.DevChangesHandler))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_event_type") {
		t.Errorf("got %s", rr.Body.String())
	}
}

// A cursor that is not a cursor is refused, rather than quietly matching
// everything or nothing.
func TestE2E_ChangesRefusesAJunkCursor(t *testing.T) {
	f := newEventsFixture(t)

	rr := f.run(t, http.MethodGet, "/changes?since=not-a-cursor", "", nil,
		f.api.RequireScope(models.ScopeCommunityRead, f.api.DevChangesHandler))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_cursor") {
		t.Errorf("got %s", rr.Body.String())
	}
}

func TestE2E_ChangesRefuseARevokedKey(t *testing.T) {
	f := newEventsFixture(t)
	for hash, key := range f.keyDB.byHash {
		key.Status = models.DeveloperKeyRevoked
		f.keyDB.byHash[hash] = key
	}

	rr := f.run(t, http.MethodGet, "/changes", "", nil,
		f.api.RequireScope(models.ScopeCommunityRead, f.api.DevChangesHandler))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("a revoked key must be refused, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestE2E_ChangesRefuseAnotherCommunity(t *testing.T) {
	f := newEventsFixture(t)
	other := primitive.NewObjectID()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/dev/communities/"+other.Hex()+"/changes", nil)
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	req = mux.SetURLVars(req, map[string]string{"communityId": other.Hex()})
	f.api.RequireDeveloperAPIKey(
		f.api.RequireScope(models.ScopeCommunityRead, f.api.DevChangesHandler),
	).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// The things an integration polls for all emit. Each of these is a question
// somebody would otherwise have to sweep a collection to answer.
func TestE2E_TheChangesThatMatterAllEmit(t *testing.T) {
	t.Run("a closed call", func(t *testing.T) {
		f := newEventsFixture(t)
		f.upsert(t, `{"externalId":"emit-1","source":"SixPD","title":"Stop"}`)
		f.run(t, http.MethodPost, "/dispatch/calls/emit-1/close", `{"source":"SixPD"}`,
			map[string]string{"externalId": "emit-1"},
			f.api.RequireScope(models.ScopeCallsWrite, f.api.DevCloseCallByExternalIDHandler))

		events := f.eventDB.waitForEvents(t, 2)
		if !hasEventType(events, models.EventCallClosed) {
			t.Errorf("a closed call must emit, got %+v", eventTypes(events))
		}
	})

	t.Run("a unit status change", func(t *testing.T) {
		f := newEventsFixture(t)
		f.run(t, http.MethodPut, "/units/status/bulk",
			`{"units":[{"player":"`+fivemRef("member")+`","tenCodeId":"`+f.tenCodeID.Hex()+`"}]}`,
			nil, f.api.RequireScope(models.ScopeUnitsWrite, f.api.DevBulkSetUnitStatusHandler))

		events := f.eventDB.waitForEvents(t, 1)
		if !hasEventType(events, models.EventUnitStatusChanged) {
			t.Errorf("a status change must emit, got %+v", eventTypes(events))
		}
	})

	t.Run("a warrant request", func(t *testing.T) {
		f := newEventsFixture(t)
		f.requestWarrant(t, `{"externalId":"emit-2","source":"SixPD",
			"requestingOfficer":"`+fivemRef("admin")+`","accusedFirstName":"John",
			"probableCause":"Observed leaving the scene."}`)

		events := f.eventDB.waitForEvents(t, 1)
		if !hasEventType(events, models.EventWarrantRequested) {
			t.Errorf("a warrant request must emit, got %+v", eventTypes(events))
		}
	})

	t.Run("an assignment, separately from the update", func(t *testing.T) {
		f := newEventsFixture(t)
		f.upsert(t, `{"externalId":"emit-3","source":"SixPD","title":"Stop"}`)
		f.upsert(t, `{"externalId":"emit-3","source":"SixPD","title":"Stop",
			"assignedUnits":["`+fivemRef("member")+`"]}`)

		events := f.eventDB.waitForEvents(t, 3)
		// Two events on one write: "edited" and "these units are on it". A
		// client that only wants the second should not have to diff every
		// update to find it.
		if !hasEventType(events, models.EventCallAssigned) {
			t.Errorf("an assignment must emit its own event, got %+v", eventTypes(events))
		}
		if !hasEventType(events, models.EventCallUpdated) {
			t.Errorf("the update must emit too, got %+v", eventTypes(events))
		}
	})
}

// An unknown event type is refused at the emit site rather than written. A row
// nobody can subscribe to is worse than no row.
func TestE2E_EmitRefusesAnUnknownType(t *testing.T) {
	f := newEventsFixture(t)

	EmitDeveloperEvent(f.eventDB, devEvent{
		Type:        "call.exploded",
		CommunityID: f.community.ID.Hex(),
		Actor:       models.EventActorDeveloperAPI,
	})

	time.Sleep(50 * time.Millisecond)
	f.eventDB.mu.Lock()
	defer f.eventDB.mu.Unlock()
	if len(f.eventDB.events) != 0 {
		t.Errorf("an unknown type must not be written, got %+v", f.eventDB.events)
	}
}

func hasEventType(events []models.DeveloperAPIEvent, want string) bool {
	for _, e := range events {
		if e.Type == want {
			return true
		}
	}
	return false
}

func eventTypes(events []models.DeveloperAPIEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}

// ---- item 5b: webhooks ----

// The SSRF gate. This is the reason webhooks were declined on #277, so each
// shape it has to refuse is named rather than covered by a single happy case.
func TestE2E_WebhookURLGateRefusesWhatItMust(t *testing.T) {
	refused := []struct {
		name string
		url  string
	}{
		{"plain http, which would ship the signature in clear", "http://example.com/hook"},
		{"loopback", "https://127.0.0.1/hook"},
		{"loopback by name", "https://localhost/hook"},
		{"a private range", "https://10.0.0.5/hook"},
		{"another private range", "https://192.168.1.10/hook"},
		{"link-local", "https://169.254.1.1/hook"},
		{"the cloud metadata address, which is the whole trick", "https://169.254.169.254/latest/meta-data/"},
		{"carrier-grade NAT", "https://100.64.0.1/hook"},
		{"the unspecified address", "https://0.0.0.0/hook"},
		{"IPv6 loopback", "https://[::1]/hook"},
		{"credentials in the url", "https://user:pass@example.com/hook"},
		{"no host", "https:///hook"},
		{"not a url at all", "nonsense"},
		{"empty", ""},
	}
	for _, c := range refused {
		if _, err := validateWebhookURL(c.url); err == nil {
			t.Errorf("%s must be refused: %q", c.name, c.url)
		}
	}
}

// And the gate is not so strict that a real endpoint cannot be saved.
func TestE2E_WebhookURLGateAcceptsAPublicHTTPSEndpoint(t *testing.T) {
	got, err := validateWebhookURL("https://example.com/lpc/hook?v=1")
	if err != nil {
		t.Fatalf("a public https endpoint must be accepted: %v", err)
	}
	// Rebuilt from the parsed URL, like every other outside string here.
	if got != "https://example.com/lpc/hook?v=1" {
		t.Errorf("got %q", got)
	}
}

// A signature covers the timestamp as well as the body, which is what stops a
// captured delivery being replayable tomorrow.
func TestE2E_WebhookSignatureCoversTheTimestamp(t *testing.T) {
	body := []byte(`{"event":{"id":"abc"}}`)
	at := time.Unix(1762000000, 0)

	sig := SignWebhookPayload("whsec_test", at, body)
	if !strings.HasPrefix(sig, "t=1762000000,v1=") {
		t.Fatalf("the timestamp must be in the header, got %q", sig)
	}

	// A different timestamp over the same body is a different signature. If it
	// were not, the timestamp would be something an attacker could rewrite.
	later := SignWebhookPayload("whsec_test", at.Add(time.Hour), body)
	if strings.TrimPrefix(sig, "t=1762000000,") == strings.TrimPrefix(later, "t=1762003600,") {
		t.Error("the signature must change with the timestamp")
	}

	// A different secret is a different signature.
	other := SignWebhookPayload("whsec_other", at, body)
	if other == sig {
		t.Error("the signature must depend on the secret")
	}

	// And a tampered body does not verify.
	tampered := SignWebhookPayload("whsec_test", at, []byte(`{"event":{"id":"def"}}`))
	if tampered == sig {
		t.Error("the signature must depend on the body")
	}
}

// The signature format is published, and a receiver's verification code is
// written against the published description rather than against our source. So
// the format itself is pinned here: computed independently, byte for byte.
//
// The delivery worker has its own copy of this function, because api/handlers
// cannot import api/scheduler without a cycle over a six-line helper. That the
// two agree is asserted in api/scheduler, where both can be imported.
func TestE2E_WebhookSignatureFormatIsPinned(t *testing.T) {
	body := []byte(`{"a":1}`)
	at := time.Unix(1762000111, 0)

	mac := hmac.New(sha256.New, []byte("whsec_x"))
	mac.Write([]byte("1762000111"))
	mac.Write([]byte("."))
	mac.Write(body)
	want := "t=1762000111,v1=" + hex.EncodeToString(mac.Sum(nil))

	if got := SignWebhookPayload("whsec_x", at, body); got != want {
		t.Fatalf("the published signature format must not drift\n got: %s\nwant: %s", got, want)
	}
}

// Registering an endpoint returns the secret exactly once and never again.
func TestE2E_WebhookSecretIsShownOnce(t *testing.T) {
	f := newEventsFixture(t)
	t.Setenv(apiGatewayKeyEnv, "")

	created := f.consoleRun(t, http.MethodPost,
		"/api/v1/developer/keys/"+f.keyID.Hex()+"/webhooks?userId="+f.keyOwnerID(),
		`{"url":"https://example.com/hook"}`,
		map[string]string{"keyId": f.keyID.Hex()},
		f.api.CreateDeveloperWebhookHandler)
	if created.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", created.Code, created.Body.String())
	}

	var out struct {
		Webhook       map[string]interface{} `json:"webhook"`
		SigningSecret string                 `json:"signingSecret"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !strings.HasPrefix(out.SigningSecret, webhookSecretPrefix) {
		t.Errorf("the secret should be recognisable on sight, got %q", out.SigningSecret)
	}

	// Listing never returns it again.
	listed := f.consoleRun(t, http.MethodGet,
		"/api/v1/developer/keys/"+f.keyID.Hex()+"/webhooks?userId="+f.keyOwnerID(), "",
		map[string]string{"keyId": f.keyID.Hex()},
		f.api.ListDeveloperWebhooksHandler)
	if strings.Contains(listed.Body.String(), out.SigningSecret) {
		t.Error("the signing secret must never be returned again")
	}
	if !strings.Contains(listed.Body.String(), "secretPrefix") {
		t.Error("the console needs a prefix to tell two secrets apart")
	}
}

// Rotating keeps the endpoint and its subscription, so it is not an outage.
func TestE2E_WebhookRotateKeepsTheEndpoint(t *testing.T) {
	f := newEventsFixture(t)
	t.Setenv(apiGatewayKeyEnv, "")

	created := f.consoleRun(t, http.MethodPost,
		"/api/v1/developer/keys/"+f.keyID.Hex()+"/webhooks?userId="+f.keyOwnerID(),
		`{"url":"https://example.com/hook","events":["call.created"]}`,
		map[string]string{"keyId": f.keyID.Hex()},
		f.api.CreateDeveloperWebhookHandler)
	var madeOut struct {
		Webhook       map[string]interface{} `json:"webhook"`
		SigningSecret string                 `json:"signingSecret"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &madeOut)
	hookID, _ := madeOut.Webhook["id"].(string)

	rotated := f.consoleRun(t, http.MethodPost,
		"/api/v1/developer/keys/"+f.keyID.Hex()+"/webhooks/"+hookID+"/rotate?userId="+f.keyOwnerID(),
		`{}`, map[string]string{"keyId": f.keyID.Hex(), "webhookId": hookID},
		f.api.RotateDeveloperWebhookSecretHandler)
	if rotated.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rotated.Code, rotated.Body.String())
	}
	var rotatedOut struct {
		SigningSecret string `json:"signingSecret"`
	}
	_ = json.Unmarshal(rotated.Body.Bytes(), &rotatedOut)
	if rotatedOut.SigningSecret == "" || rotatedOut.SigningSecret == madeOut.SigningSecret {
		t.Error("rotating must issue a new secret")
	}

	f.webhookDB.mu.Lock()
	defer f.webhookDB.mu.Unlock()
	if len(f.webhookDB.hooks) != 1 {
		t.Fatalf("rotating must not create a second endpoint, got %d", len(f.webhookDB.hooks))
	}
	hook := f.webhookDB.hooks[0]
	if hook.URL != "https://example.com/hook" || len(hook.Events) != 1 {
		t.Errorf("the endpoint and its subscription must survive a rotate, got %+v", hook)
	}
}

// An unknown event type is refused at save time rather than silently never
// firing.
func TestE2E_WebhookRefusesAnUnknownSubscription(t *testing.T) {
	f := newEventsFixture(t)
	t.Setenv(apiGatewayKeyEnv, "")

	rr := f.consoleRun(t, http.MethodPost,
		"/api/v1/developer/keys/"+f.keyID.Hex()+"/webhooks?userId="+f.keyOwnerID(),
		`{"url":"https://example.com/hook","events":["call.close"]}`,
		map[string]string{"keyId": f.keyID.Hex()},
		f.api.CreateDeveloperWebhookHandler)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

// A key cannot subscribe an endpoint to a community it is not granted.
func TestE2E_WebhookRefusesAnUngrantedCommunity(t *testing.T) {
	f := newEventsFixture(t)
	t.Setenv(apiGatewayKeyEnv, "")

	rr := f.consoleRun(t, http.MethodPost,
		"/api/v1/developer/keys/"+f.keyID.Hex()+"/webhooks?userId="+f.keyOwnerID(),
		`{"url":"https://example.com/hook","communities":["`+primitive.NewObjectID().Hex()+`"]}`,
		map[string]string{"keyId": f.keyID.Hex()},
		f.api.CreateDeveloperWebhookHandler)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

// A webhook id belonging to another key is a 404, so its existence is never
// confirmed.
func TestE2E_WebhookFromAnotherKeyIsNotFound(t *testing.T) {
	f := newEventsFixture(t)
	t.Setenv(apiGatewayKeyEnv, "")

	foreign := models.DeveloperWebhook{
		ID: primitive.NewObjectID(), KeyID: primitive.NewObjectID().Hex(),
		URL: "https://elsewhere.example.com/hook", Status: models.WebhookStatusActive,
	}
	f.webhookDB.mu.Lock()
	f.webhookDB.hooks = append(f.webhookDB.hooks, foreign)
	f.webhookDB.mu.Unlock()

	rr := f.consoleRun(t, http.MethodDelete,
		"/api/v1/developer/keys/"+f.keyID.Hex()+"/webhooks/"+foreign.ID.Hex()+"?userId="+f.keyOwnerID(),
		"", map[string]string{"keyId": f.keyID.Hex(), "webhookId": foreign.ID.Hex()},
		f.api.DeleteDeveloperWebhookHandler)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
	f.webhookDB.mu.Lock()
	defer f.webhookDB.mu.Unlock()
	if len(f.webhookDB.hooks) != 1 {
		t.Error("the other key's endpoint must still be there")
	}
}

// An endpoint belonging to another developer's key cannot be reached at all.
func TestE2E_WebhookRoutesRefuseAnotherDevelopersKey(t *testing.T) {
	f := newEventsFixture(t)
	t.Setenv(apiGatewayKeyEnv, "")

	rr := f.consoleRun(t, http.MethodGet,
		"/api/v1/developer/keys/"+f.keyID.Hex()+"/webhooks?userId="+primitive.NewObjectID().Hex(), "",
		map[string]string{"keyId": f.keyID.Hex()},
		f.api.ListDeveloperWebhooksHandler)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for somebody else's key, got %d: %s", rr.Code, rr.Body.String())
	}
}

// An endpoint starts at now, not at the beginning of the log.
func TestE2E_WebhookStartsAtNowNotAtTheBeginning(t *testing.T) {
	f := newEventsFixture(t)
	t.Setenv(apiGatewayKeyEnv, "")

	// A week of history already in the log.
	f.eventDB.mu.Lock()
	for i := 0; i < 3; i++ {
		f.eventDB.events = append(f.eventDB.events, models.DeveloperAPIEvent{
			ID:   primitive.NewObjectIDFromTimestamp(time.Now().Add(-time.Duration(i+1) * time.Hour)),
			Type: models.EventCallCreated, CommunityID: f.community.ID.Hex(),
		})
	}
	f.eventDB.mu.Unlock()

	f.consoleRun(t, http.MethodPost,
		"/api/v1/developer/keys/"+f.keyID.Hex()+"/webhooks?userId="+f.keyOwnerID(),
		`{"url":"https://example.com/hook"}`,
		map[string]string{"keyId": f.keyID.Hex()},
		f.api.CreateDeveloperWebhookHandler)

	f.webhookDB.mu.Lock()
	defer f.webhookDB.mu.Unlock()
	if len(f.webhookDB.hooks) != 1 {
		t.Fatal("the endpoint was not created")
	}
	cursor := f.webhookDB.hooks[0].Cursor
	if cursor == "" {
		t.Fatal("a new endpoint needs a cursor, or it would replay the log")
	}
	for _, event := range f.eventDB.events {
		if event.ID.Hex() > cursor {
			t.Errorf("a new endpoint must not be positioned before existing history: %s > %s",
				event.ID.Hex(), cursor)
		}
	}
}

// ---- item 6: attachments ----

// Metadata and a link. The thing the model refuses is the interesting part.
func TestE2E_AttachmentRefusesToBeCalledEvidence(t *testing.T) {
	f := newEventsFixture(t)
	f.upsertDraft(t, `{"externalId":"att-1","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)

	rr := f.run(t, http.MethodPost, "/reports/drafts/att-1/attachments?source=SixPD",
		`{"kind":"evidence","url":"https://clips.example.com/a.mp4"}`,
		map[string]string{"externalId": "att-1"},
		f.api.RequireScope(models.ScopeReportsWrite, f.api.DevAddDraftAttachmentHandler))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	// And the refusal explains why rather than just listing the enum.
	if !strings.Contains(rr.Body.String(), "not evidence") {
		t.Errorf("the refusal should say why, got %s", rr.Body.String())
	}
	for _, kind := range models.ReportAttachmentKinds() {
		if kind == "evidence" {
			t.Fatal("there must be no evidence attachment kind")
		}
	}
}

func TestE2E_AttachmentIsMetadataAndALink(t *testing.T) {
	f := newEventsFixture(t)
	f.upsertDraft(t, `{"externalId":"att-2","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)

	rr := f.run(t, http.MethodPost, "/reports/drafts/att-2/attachments?source=SixPD",
		`{"kind":"clip","label":"Dashcam","url":"https://clips.example.com/a.mp4",
		  "contentType":"video/mp4","sizeBytes":8421376}`,
		map[string]string{"externalId": "att-2"},
		f.api.RequireScope(models.ScopeReportsWrite, f.api.DevAddDraftAttachmentHandler))

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Attachment models.DeveloperReportAttachment `json:"attachment"`
		Added      bool                             `json:"added"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if !out.Added || out.Attachment.ID == "" {
		t.Errorf("expected an added attachment, got %+v", out)
	}
	if out.Attachment.AddedByKey != f.keyID.Hex() {
		t.Errorf("the key that attached it must be recorded, got %q", out.Attachment.AddedByKey)
	}
	// Audited, because a reviewer may act on it. Waited for rather than read
	// immediately: logAudit writes on a goroutine so it never delays a
	// request, which means asserting straight after the response is a race.
	waitForAuditAction(t, f.api.ALDB, "attachment_added")
}

// An attachment nobody can read afterwards is not an attachment. It was stored
// and then returned by no read at all, so neither the integration that added it
// nor the reviewer on the website could see it.
func TestE2E_AnAttachmentComesBackOnTheDraft(t *testing.T) {
	f := newEventsFixture(t)
	f.upsertDraft(t, `{"externalId":"att-read","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)

	rr := f.run(t, http.MethodPost, "/reports/drafts/att-read/attachments?source=SixPD",
		`{"kind":"screenshot","label":"Scene","url":"https://clips.example.com/scene.png",
		  "contentType":"image/png","sizeBytes":20480}`,
		map[string]string{"externalId": "att-read"},
		f.api.RequireScope(models.ScopeReportsWrite, f.api.DevAddDraftAttachmentHandler))
	if rr.Code != http.StatusCreated {
		t.Fatalf("attaching: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	rr = f.run(t, http.MethodGet, "/reports/drafts/att-read?source=SixPD", "",
		map[string]string{"externalId": "att-read"},
		f.api.RequireScope(models.ScopeReportsRead, f.api.DevGetReportDraftHandler))
	if rr.Code != http.StatusOK {
		t.Fatalf("reading it back: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var out struct {
		Draft devReportDraftView `json:"draft"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(out.Draft.Attachments) != 1 {
		t.Fatalf("the attachment must come back on the draft, got %+v", out.Draft.Attachments)
	}
	got := out.Draft.Attachments[0]
	if got.Kind != "screenshot" || got.Label != "Scene" {
		t.Errorf("the reviewer needs the kind and the label, got %+v", got)
	}
	if got.URL != "https://clips.example.com/scene.png" {
		t.Errorf("and the link, got %q", got.URL)
	}
	if got.AddedByKey == "" {
		t.Error("and who added it, so the attribution can be shown")
	}
}

// waitForAuditAction waits for an audit entry whose action contains want.
//
// logAudit deliberately writes off the request path, so a test that reads the
// entries the instant a handler returns is testing the scheduler rather than
// the handler. This polls with a bounded deadline and says what it did see.
func waitForAuditAction(t *testing.T, store databases.AuditLogDatabase, want string) {
	t.Helper()
	audit, ok := store.(*fakeAuditDB)
	if !ok {
		t.Fatalf("the fixture's audit store is %T, not the fake", store)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		audit.mu.Lock()
		actions := make([]string, 0, len(audit.entries))
		for _, entry := range audit.entries {
			actions = append(actions, entry.Action)
		}
		audit.mu.Unlock()

		for _, action := range actions {
			if strings.Contains(action, want) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected an audit entry containing %q, saw %v", want, actions)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The same URL twice is a retry, not a second attachment for a reviewer to
// look at twice.
func TestE2E_AttachmentIsIdempotentOnTheURL(t *testing.T) {
	f := newEventsFixture(t)
	f.upsertDraft(t, `{"externalId":"att-3","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)
	body := `{"kind":"clip","url":"https://clips.example.com/b.mp4"}`

	first := f.run(t, http.MethodPost, "/reports/drafts/att-3/attachments?source=SixPD", body,
		map[string]string{"externalId": "att-3"},
		f.api.RequireScope(models.ScopeReportsWrite, f.api.DevAddDraftAttachmentHandler))
	second := f.run(t, http.MethodPost, "/reports/drafts/att-3/attachments?source=SixPD", body,
		map[string]string{"externalId": "att-3"},
		f.api.RequireScope(models.ScopeReportsWrite, f.api.DevAddDraftAttachmentHandler))

	if first.Code != http.StatusCreated {
		t.Fatalf("expected 201 first, got %d: %s", first.Code, first.Body.String())
	}
	if second.Code != http.StatusOK {
		t.Fatalf("expected 200 on the repeat, got %d: %s", second.Code, second.Body.String())
	}
	var out struct {
		Added bool `json:"added"`
	}
	_ = json.Unmarshal(second.Body.Bytes(), &out)
	if out.Added {
		t.Error("a repeat must report adding nothing")
	}
}

// Bounded, so a typo cannot render "4 PB" on a reviewer's screen and a draft
// cannot grow past what a person can read.
func TestE2E_AttachmentLimitsAreEnforced(t *testing.T) {
	t.Run("an absurd declared size", func(t *testing.T) {
		if _, err := buildAttachment(nil, devAttachmentRequest{
			Kind: "clip", URL: "https://clips.example.com/a.mp4",
			SizeBytes: maxAttachmentSizeBytes + 1,
		}); err == nil {
			t.Error("an out-of-range size must be refused")
		}
	})

	t.Run("a url with credentials in it", func(t *testing.T) {
		if _, err := buildAttachment(nil, devAttachmentRequest{
			Kind: "clip", URL: "https://user:pass@clips.example.com/a.mp4",
		}); err == nil {
			t.Error("credentials in an attachment url would end up on a reviewer's screen")
		}
	})

	t.Run("an unbounded url", func(t *testing.T) {
		if _, err := buildAttachment(nil, devAttachmentRequest{
			Kind: "clip", URL: "https://clips.example.com/" + strings.Repeat("a", maxAttachmentURLLen),
		}); err == nil {
			t.Error("an oversized url must be refused")
		}
	})

	t.Run("no url at all", func(t *testing.T) {
		if _, err := buildAttachment(nil, devAttachmentRequest{Kind: "clip"}); err == nil {
			t.Error("this surface stores a link; without one there is nothing to store")
		}
	})

	t.Run("the count cap", func(t *testing.T) {
		f := newEventsFixture(t)
		f.upsertDraft(t, `{"externalId":"att-4","source":"SixPD",
			"reportingOfficer":"`+fivemRef("admin")+`"}`)
		for i := 0; i < maxAttachmentsPerRecord; i++ {
			rr := f.run(t, http.MethodPost, "/reports/drafts/att-4/attachments?source=SixPD",
				fmt.Sprintf(`{"kind":"clip","url":"https://clips.example.com/%d.mp4"}`, i),
				map[string]string{"externalId": "att-4"},
				f.api.RequireScope(models.ScopeReportsWrite, f.api.DevAddDraftAttachmentHandler))
			if rr.Code != http.StatusCreated {
				t.Fatalf("attachment %d: expected 201, got %d: %s", i, rr.Code, rr.Body.String())
			}
		}
		over := f.run(t, http.MethodPost, "/reports/drafts/att-4/attachments?source=SixPD",
			`{"kind":"clip","url":"https://clips.example.com/one-too-many.mp4"}`,
			map[string]string{"externalId": "att-4"},
			f.api.RequireScope(models.ScopeReportsWrite, f.api.DevAddDraftAttachmentHandler))
		if over.Code != http.StatusConflict {
			t.Fatalf("expected 409 past the cap, got %d: %s", over.Code, over.Body.String())
		}
	})
}

// Nothing can be attached to a draft a person has already acted on. A clip
// arriving afterwards must not change what they signed off.
func TestE2E_AttachmentRefusedOnAReviewedDraft(t *testing.T) {
	f := newEventsFixture(t)
	_, created := f.upsertDraft(t, `{"externalId":"att-5","source":"SixPD",
		"reportingOfficer":"`+fivemRef("admin")+`"}`)

	f.draftDB.mu.Lock()
	for i := range f.draftDB.drafts {
		if f.draftDB.drafts[i].ID.Hex() == created.Draft.ID {
			f.draftDB.drafts[i].Status = models.ReportDraftStatusSubmitted
		}
	}
	f.draftDB.mu.Unlock()

	rr := f.run(t, http.MethodPost, "/reports/drafts/att-5/attachments?source=SixPD",
		`{"kind":"clip","url":"https://clips.example.com/late.mp4"}`,
		map[string]string{"externalId": "att-5"},
		f.api.RequireScope(models.ScopeReportsWrite, f.api.DevAddDraftAttachmentHandler))

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestE2E_AttachmentsRefuseAnotherCommunity(t *testing.T) {
	f := newEventsFixture(t)
	other := primitive.NewObjectID()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/dev/communities/"+other.Hex()+"/reports/drafts/x/attachments",
		strings.NewReader(`{"kind":"clip","url":"https://clips.example.com/a.mp4"}`))
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	req = mux.SetURLVars(req, map[string]string{"communityId": other.Hex(), "externalId": "x"})
	f.api.RequireDeveloperAPIKey(
		f.api.RequireScope(models.ScopeReportsWrite, f.api.DevAddDraftAttachmentHandler),
	).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestE2E_AttachmentsRefuseARevokedKey(t *testing.T) {
	f := newEventsFixture(t)
	for hash, key := range f.keyDB.byHash {
		key.Status = models.DeveloperKeyRevoked
		f.keyDB.byHash[hash] = key
	}

	rr := f.run(t, http.MethodPost, "/reports/drafts/x/attachments?source=SixPD",
		`{"kind":"clip","url":"https://clips.example.com/a.mp4"}`,
		map[string]string{"externalId": "x"},
		f.api.RequireScope(models.ScopeReportsWrite, f.api.DevAddDraftAttachmentHandler))

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---- helpers ----

// keyOwnerID is the LPC user the fixture's key belongs to, which the console
// routes check ownership against.
//
// Read from byID rather than byHash, because that is the map the console's
// lookup uses — the fixture re-points the key's owner in byHash only, and
// reading the wrong map here would send a userId the lookup does not match.
func (f *eventsFixture) keyOwnerID() string {
	if key, ok := f.keyDB.byID[f.keyID.Hex()]; ok {
		return key.UserID
	}
	return ""
}

// consoleRun drives a first-party console route, the way the website does.
func (f *eventsFixture) consoleRun(t *testing.T, method, path, body string, vars map[string]string, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req = mux.SetURLVars(req, vars)
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr
}
