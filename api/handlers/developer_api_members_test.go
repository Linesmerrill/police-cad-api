package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
	"testing"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Membership from inside the game, and the edge cases Merrill listed.
//
// The property that matters most: a key is not a person. Every write names an
// acting player, and the permission check runs against that player, so a key
// can never grant more than the player it acts for.

// ---- fixture ----

type memberFixture struct {
	*provisionFixture
	communityDB *fakeMemberCommunityDB
	community   models.Community

	ownerID     string
	adminID     string
	plainID     string
	outsiderID  string
	deptID      primitive.ObjectID
	rankID      primitive.ObjectID
}

type fakeMemberCommunityDB struct {
	databases.CommunityDatabase
	mu          sync.Mutex
	communities map[string]models.Community
	updates     []bson.M
	// findOneAndUpdateMatches controls whether the department-member update
	// reports a match, which is how the handler decides to append instead.
	findOneAndUpdateMatches bool
}

func (f *fakeMemberCommunityDB) FindOneIncludingPending(_ context.Context, filter interface{}) (*models.Community, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	if oid, ok := m["_id"].(primitive.ObjectID); ok {
		if c, found := f.communities[oid.Hex()]; found {
			return &c, nil
		}
	}
	return nil, mongo.ErrNoDocuments
}

func (f *fakeMemberCommunityDB) FindOne(ctx context.Context, filter interface{}) (*models.Community, error) {
	return f.FindOneIncludingPending(ctx, filter)
}

func (f *fakeMemberCommunityDB) UpdateOne(_ context.Context, _, update interface{}, _ ...*options.UpdateOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := update.(bson.M); ok {
		f.updates = append(f.updates, m)
	}
	return nil
}

func (f *fakeMemberCommunityDB) FindOneAndUpdate(_ context.Context, _, update interface{}, _ ...*options.FindOneAndUpdateOptions) (*models.Community, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := update.(bson.M); ok {
		f.updates = append(f.updates, m)
	}
	if !f.findOneAndUpdateMatches {
		return nil, mongo.ErrNoDocuments
	}
	return &models.Community{}, nil
}

func (f *fakeMemberCommunityDB) CountDocuments(_ context.Context, _ interface{}, _ ...*options.CountOptions) (int64, error) {
	return 0, nil
}

// fakeAuditDB swallows audit writes so the real logAudit path runs in tests.
type fakeAuditDB struct {
	databases.AuditLogDatabase
	mu      sync.Mutex
	entries []models.AuditLog
}

func (f *fakeAuditDB) InsertOne(_ context.Context, log models.AuditLog, _ ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, log)
	return fakeInsertResult{id: primitive.NewObjectID()}, nil
}

// fakeMemberUserDB adds roster listing and membership editing to the user fake.
type fakeMemberUserDB struct {
	*fakeUserDB
}

func (f *fakeMemberUserDB) CountDocuments(_ context.Context, filter interface{}, _ ...*options.CountOptions) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	oid, _ := m["_id"].(primitive.ObjectID)
	elem, _ := m["user.communities"].(bson.M)
	match, _ := elem["$elemMatch"].(bson.M)
	wantCommunity, _ := match["communityId"].(string)
	wantStatus, _ := match["status"].(string)

	u, ok := f.users[oid.Hex()]
	if !ok {
		return 0, nil
	}
	for _, c := range u.Details.Communities {
		if c.CommunityID == wantCommunity && c.Status == wantStatus {
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeMemberUserDB) Find(_ context.Context, _ interface{}, _ ...*options.FindOptions) (databases.MongoCursor, error) {
	return databases.MongoCursor{}, mongo.ErrNoDocuments
}

// newMemberFixture builds a community with an owner, an admin with
// "manage members", a plain member, and an outsider — each with a fivem
// identity so they can be named the way a mod would name them.
func newMemberFixture(t *testing.T) *memberFixture {
	t.Helper()
	base := newProvisionFixture(t)
	base.keyDB.byHash = upgradeKeyScopes(base.keyDB.byHash, models.ScopePlayersProvision,
		models.ScopeMembersRead, models.ScopeMembersWrite)

	communityID := primitive.NewObjectID()
	deptID := primitive.NewObjectID()
	rankID := primitive.NewObjectID()

	ownerID := primitive.NewObjectID().Hex()
	adminID := primitive.NewObjectID().Hex()
	plainID := primitive.NewObjectID().Hex()
	outsiderID := primitive.NewObjectID().Hex()

	community := models.Community{ID: communityID}
	community.Details.Name = "Test RP"
	community.Details.OwnerID = ownerID
	community.Details.MembersCount = 3
	community.Details.Departments = []models.Department{{
		ID:    deptID,
		Name:  "BCSO",
		Ranks: []models.Rank{{ID: rankID, Name: "Deputy"}},
	}}
	community.Details.Roles = []models.Role{{
		ID:      primitive.NewObjectID(),
		Name:    "Staff",
		Members: []string{adminID},
		Permissions: []models.Permission{
			{ID: primitive.NewObjectID(), Name: "manage members", Enabled: true},
		},
	}}

	cdb := &fakeMemberCommunityDB{communities: map[string]models.Community{communityID.Hex(): community}}
	userDB := &fakeMemberUserDB{fakeUserDB: base.userDB}

	base.api.CDB = cdb
	base.api.UDB = userDB
	base.api.ALDB = &fakeAuditDB{}

	f := &memberFixture{
		provisionFixture: base,
		communityDB:      cdb,
		community:        community,
		ownerID:          ownerID,
		adminID:          adminID,
		plainID:          plainID,
		outsiderID:       outsiderID,
		deptID:           deptID,
		rankID:           rankID,
	}

	// Everyone gets an account, a membership and a game identity.
	f.seedPlayer(ownerID, "owner", communityID.Hex(), models.CommunityMemberStatusApproved)
	f.seedPlayer(adminID, "admin", communityID.Hex(), models.CommunityMemberStatusApproved)
	f.seedPlayer(plainID, "member", communityID.Hex(), models.CommunityMemberStatusApproved)
	f.seedPlayer(outsiderID, "outsider", "", "")

	f.grantDB.grants = []models.DeveloperCommunityGrant{{
		KeyID: f.keyID.Hex(), CommunityID: communityID.Hex(), Status: models.DeveloperKeyActive,
	}}
	return f
}

// upgradeKeyScopes re-points the fixture's key at a wider scope set.
func upgradeKeyScopes(byHash map[string]models.DeveloperAPIKey, scopes ...string) map[string]models.DeveloperAPIKey {
	for h, k := range byHash {
		k.Scopes = scopes
		byHash[h] = k
	}
	return byHash
}

// seedPlayer creates an account, a fivem identity for it, and optionally a
// membership.
func (f *memberFixture) seedPlayer(userID, name, communityID, status string) {
	claimed := true
	details := models.UserDetails{Username: name, Claimed: &claimed}
	if communityID != "" {
		details.Communities = []models.UserCommunity{{
			ID: primitive.NewObjectID().Hex(), CommunityID: communityID, Status: status,
		}}
	}
	f.userDB.mu.Lock()
	f.userDB.users[userID] = models.User{ID: userID, Details: details}
	f.userDB.mu.Unlock()

	f.identityDB.mu.Lock()
	f.identityDB.rows = append(f.identityDB.rows, models.PlayerIdentity{
		ID: primitive.NewObjectID(), Provider: models.PlayerProviderFiveM,
		Identifier: name, UserID: userID, Claimed: true,
		CreatedByDeveloperID: f.developerID.Hex(), CreatedByKeyID: f.keyID.Hex(),
	})
	f.identityDB.mu.Unlock()
}

// addMember runs POST members.
func (f *memberFixture) addMember(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.run(t, http.MethodPost, "/members", body, nil,
		f.api.RequireScope(models.ScopeMembersWrite, f.api.DevAddMemberHandler))
}

func (f *memberFixture) run(t *testing.T, method, suffix, body string, extraVars map[string]string, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/dev/communities/" + f.community.ID.Hex() + suffix
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	vars := map[string]string{"communityId": f.community.ID.Hex()}
	for k, v := range extraVars {
		vars[k] = v
	}
	req = mux.SetURLVars(req, vars)
	rr := httptest.NewRecorder()
	f.api.RequireDeveloperAPIKey(h).ServeHTTP(rr, req)
	return rr
}

// ---- who may act ----

// The whole point of the acting-player design: a key with members:write cannot
// change the roster on its own authority.
func TestDevAddMember_KeyCannotActWithoutAPermittedPlayer(t *testing.T) {
	t.Run("a plain member cannot add anyone", func(t *testing.T) {
		f := newMemberFixture(t)
		rr := f.addMember(t, `{"actingPlayer":"fivem:member","player":"fivem:newfriend"}`)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "acting_player_lacks_permission") {
			t.Errorf("the reason should be specific: %s", rr.Body.String())
		}
	})

	t.Run("an outsider cannot add anyone", func(t *testing.T) {
		f := newMemberFixture(t)
		rr := f.addMember(t, `{"actingPlayer":"fivem:outsider","player":"fivem:newfriend"}`)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "acting_player_not_a_member") {
			t.Errorf("the reason should be specific: %s", rr.Body.String())
		}
	})

	t.Run("no acting player at all is refused", func(t *testing.T) {
		f := newMemberFixture(t)
		rr := f.addMember(t, `{"player":"fivem:newfriend"}`)
		if rr.Code != http.StatusBadRequest && rr.Code != http.StatusForbidden {
			t.Fatalf("a write with no acting player must be refused, got %d", rr.Code)
		}
	})

	t.Run("an admin with manage members can", func(t *testing.T) {
		f := newMemberFixture(t)
		rr := f.addMember(t, `{"actingPlayer":"fivem:admin","player":"fivem:newfriend","username":"Friend"}`)
		if rr.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("the owner always can", func(t *testing.T) {
		f := newMemberFixture(t)
		rr := f.addMember(t, `{"actingPlayer":"fivem:owner","player":"fivem:newfriend"}`)
		if rr.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
		}
	})
}

// ---- the edge cases ----

// A mod re-adding someone on every join is normal, so this is idempotent rather
// than an error.
func TestDevAddMember_AlreadyAMember(t *testing.T) {
	f := newMemberFixture(t)

	rr := f.addMember(t, `{"actingPlayer":"fivem:admin","player":"fivem:member"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for an existing member, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Added   bool   `json:"added"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Added {
		t.Error("re-adding an existing member must not report adding them")
	}
}

// A friend who has never played gets an account provisioned here, and the
// response has to say they cannot log in yet so the mod can tell them.
func TestDevAddMember_UnknownFriendIsProvisionedAndNeedsClaiming(t *testing.T) {
	f := newMemberFixture(t)

	rr := f.addMember(t, `{"actingPlayer":"fivem:admin","player":"fivem:brandnew","username":"Mate"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		PlayerCreated bool `json:"playerCreated"`
		NeedsClaim    bool `json:"needsClaim"`
		Member        struct {
			Claimed bool `json:"claimed"`
		} `json:"member"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if !out.PlayerCreated {
		t.Error("an unknown friend should have been provisioned")
	}
	if !out.NeedsClaim {
		t.Error("the response must say the new account still needs claiming")
	}
	if out.Member.Claimed {
		t.Error("a freshly provisioned friend is not claimed")
	}
}

// A friend who already has an LPC account is linked to it, not duplicated — and
// never matched by email.
func TestDevAddMember_FriendWithAnExistingAccountIsLinkedNotDuplicated(t *testing.T) {
	f := newMemberFixture(t)

	// "outsider" already has an LPC account and a fivem identity, and is not in
	// this community.
	rr := f.addMember(t, `{"actingPlayer":"fivem:admin","player":"fivem:outsider"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		PlayerCreated bool `json:"playerCreated"`
		Member        struct {
			PlayerID string `json:"playerId"`
			Claimed  bool   `json:"claimed"`
		} `json:"member"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.PlayerCreated {
		t.Error("a friend who already has an account must not get a second one")
	}
	if out.Member.PlayerID != f.outsiderID {
		t.Errorf("expected their existing account %s, got %s", f.outsiderID, out.Member.PlayerID)
	}
	if !out.Member.Claimed {
		t.Error("their existing account is already claimed and must stay so")
	}
}

// Lifting a ban is a moderation decision, and it belongs to the people who made
// it rather than to whoever is holding a key.
func TestDevAddMember_BannedPlayerCannotBeReInvited(t *testing.T) {
	f := newMemberFixture(t)

	c := f.communityDB.communities[f.community.ID.Hex()]
	c.Details.BanList = []string{f.outsiderID}
	f.communityDB.communities[f.community.ID.Hex()] = c

	rr := f.addMember(t, `{"actingPlayer":"fivem:admin","player":"fivem:outsider"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a banned player, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "player_banned") {
		t.Errorf("the reason should be specific: %s", rr.Body.String())
	}
}

// A banned acting player cannot act, even if they still hold a role.
func TestDevAddMember_BannedActingPlayerCannotAct(t *testing.T) {
	f := newMemberFixture(t)

	c := f.communityDB.communities[f.community.ID.Hex()]
	c.Details.BanList = []string{f.adminID}
	f.communityDB.communities[f.community.ID.Hex()] = c

	rr := f.addMember(t, `{"actingPlayer":"fivem:admin","player":"fivem:brandnew"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "acting_player_banned") {
		t.Errorf("the reason should be specific: %s", rr.Body.String())
	}
}

// A community in its 30-day pending-deletion window takes no membership writes.
func TestDevMembers_PendingDeletionCommunityIsReadOnly(t *testing.T) {
	f := newMemberFixture(t)

	pending := primitive.NewDateTimeFromTime(time.Now())
	c := f.communityDB.communities[f.community.ID.Hex()]
	c.Details.PendingDeletionAt = &pending
	f.communityDB.communities[f.community.ID.Hex()] = c

	rr := f.addMember(t, `{"actingPlayer":"fivem:admin","player":"fivem:brandnew"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a community being deleted, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "community_pending_deletion") {
		t.Errorf("the reason should be specific: %s", rr.Body.String())
	}
}

// A key revoked mid-session stops on the very next call, including mid-way
// through a mod's sequence of membership writes.
func TestDevMembers_KeyRevokedMidSessionStopsImmediately(t *testing.T) {
	f := newMemberFixture(t)

	if rr := f.addMember(t, `{"actingPlayer":"fivem:admin","player":"fivem:brandnew"}`); rr.Code != http.StatusCreated {
		t.Fatalf("expected the first write to work, got %d", rr.Code)
	}

	for hash, key := range f.keyDB.byHash {
		key.Status = models.DeveloperKeyRevoked
		f.keyDB.byHash[hash] = key
	}

	if rr := f.addMember(t, `{"actingPlayer":"fivem:admin","player":"fivem:another"}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after revocation, got %d", rr.Code)
	}
}

// A community owner withdrawing their grant has the same effect, and it must
// not depend on the key being revoked.
func TestDevMembers_GrantRevokedStopsImmediately(t *testing.T) {
	f := newMemberFixture(t)

	f.grantDB.grants[0].Status = models.DeveloperKeyRevoked

	rr := f.addMember(t, `{"actingPlayer":"fivem:admin","player":"fivem:brandnew"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 after the grant was revoked, got %d", rr.Code)
	}
}

// ---- roles, ranks and departments ----

// Roles carry permissions, so a key assigning one is a privilege-escalation
// path. Departments and ranks are fine.
func TestDevUpdateMember_RolesAreNotAssignable(t *testing.T) {
	f := newMemberFixture(t)

	rr := f.run(t, http.MethodPatch, "/members/fivem:member",
		`{"actingPlayer":"fivem:admin","roleId":"`+primitive.NewObjectID().Hex()+`"}`,
		map[string]string{"playerRef": "fivem:member"},
		f.api.RequireScope(models.ScopeMembersWrite, f.api.DevUpdateMemberHandler))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a role assignment, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "roles_not_assignable") {
		t.Errorf("the reason should be specific: %s", rr.Body.String())
	}
}

func TestDevUpdateMember_DepartmentAndRank(t *testing.T) {
	t.Run("a real department and rank are accepted", func(t *testing.T) {
		f := newMemberFixture(t)
		rr := f.run(t, http.MethodPatch, "/members/fivem:member",
			`{"actingPlayer":"fivem:admin","departmentId":"`+f.deptID.Hex()+`","rankId":"`+f.rankID.Hex()+`"}`,
			map[string]string{"playerRef": "fivem:member"},
			f.api.RequireScope(models.ScopeMembersWrite, f.api.DevUpdateMemberHandler))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("a department that does not exist is refused", func(t *testing.T) {
		f := newMemberFixture(t)
		rr := f.run(t, http.MethodPatch, "/members/fivem:member",
			`{"actingPlayer":"fivem:admin","departmentId":"`+primitive.NewObjectID().Hex()+`"}`,
			map[string]string{"playerRef": "fivem:member"},
			f.api.RequireScope(models.ScopeMembersWrite, f.api.DevUpdateMemberHandler))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rr.Code)
		}
	})

	t.Run("a rank that does not exist is refused rather than written dangling", func(t *testing.T) {
		f := newMemberFixture(t)
		rr := f.run(t, http.MethodPatch, "/members/fivem:member",
			`{"actingPlayer":"fivem:admin","departmentId":"`+f.deptID.Hex()+`","rankId":"`+primitive.NewObjectID().Hex()+`"}`,
			map[string]string{"playerRef": "fivem:member"},
			f.api.RequireScope(models.ScopeMembersWrite, f.api.DevUpdateMemberHandler))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for an unknown rank, got %d", rr.Code)
		}
	})

	t.Run("a player who is not a member yet cannot be given a department", func(t *testing.T) {
		f := newMemberFixture(t)
		rr := f.run(t, http.MethodPatch, "/members/fivem:outsider",
			`{"actingPlayer":"fivem:admin","departmentId":"`+f.deptID.Hex()+`"}`,
			map[string]string{"playerRef": "fivem:outsider"},
			f.api.RequireScope(models.ScopeMembersWrite, f.api.DevUpdateMemberHandler))
		if rr.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
		}
	})
}

// ---- removal and leaving ----

// Ownership transfer is a deliberate act that belongs on the website, so the
// owner can neither be removed nor leave.
func TestDevRemoveMember_OwnerCannotBeRemovedOrLeave(t *testing.T) {
	f := newMemberFixture(t)

	t.Run("an admin cannot remove the owner", func(t *testing.T) {
		rr := f.run(t, http.MethodDelete, "/members/fivem:owner",
			`{"actingPlayer":"fivem:admin"}`, map[string]string{"playerRef": "fivem:owner"},
			f.api.RequireScope(models.ScopeMembersWrite, f.api.DevRemoveMemberHandler))
		if rr.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "owner_cannot_be_removed") {
			t.Errorf("the reason should be specific: %s", rr.Body.String())
		}
	})

	t.Run("the owner cannot leave either", func(t *testing.T) {
		rr := f.run(t, http.MethodDelete, "/members/fivem:owner",
			`{"actingPlayer":"fivem:owner"}`, map[string]string{"playerRef": "fivem:owner"},
			f.api.RequireScope(models.ScopeMembersWrite, f.api.DevRemoveMemberHandler))
		if rr.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d", rr.Code)
		}
	})
}

// Leaving needs no permission: a plain member may remove themselves, but not
// anybody else.
func TestDevRemoveMember_LeavingNeedsNoPermission(t *testing.T) {
	t.Run("a plain member can leave", func(t *testing.T) {
		f := newMemberFixture(t)
		rr := f.run(t, http.MethodDelete, "/members/fivem:member",
			`{"actingPlayer":"fivem:member"}`, map[string]string{"playerRef": "fivem:member"},
			f.api.RequireScope(models.ScopeMembersWrite, f.api.DevRemoveMemberHandler))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var out struct {
			Removed bool `json:"removed"`
			Left    bool `json:"left"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		if !out.Removed || !out.Left {
			t.Errorf("expected a recorded self-removal, got %+v", out)
		}
	})

	t.Run("a plain member cannot remove somebody else", func(t *testing.T) {
		f := newMemberFixture(t)
		rr := f.run(t, http.MethodDelete, "/members/fivem:admin",
			`{"actingPlayer":"fivem:member"}`, map[string]string{"playerRef": "fivem:admin"},
			f.api.RequireScope(models.ScopeMembersWrite, f.api.DevRemoveMemberHandler))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rr.Code)
		}
	})
}

// ---- approvals ----

func TestDevApproveMember(t *testing.T) {
	t.Run("a pending request is approved", func(t *testing.T) {
		f := newMemberFixture(t)
		f.seedPlayer(primitive.NewObjectID().Hex(), "waiting", f.community.ID.Hex(), models.CommunityMemberStatusPending)

		rr := f.run(t, http.MethodPost, "/members/fivem:waiting/approve",
			`{"actingPlayer":"fivem:admin"}`, map[string]string{"playerRef": "fivem:waiting"},
			f.api.RequireScope(models.ScopeMembersWrite, f.api.DevApproveMemberHandler))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("somebody who never asked cannot be approved", func(t *testing.T) {
		f := newMemberFixture(t)
		rr := f.run(t, http.MethodPost, "/members/fivem:outsider/approve",
			`{"actingPlayer":"fivem:admin"}`, map[string]string{"playerRef": "fivem:outsider"},
			f.api.RequireScope(models.ScopeMembersWrite, f.api.DevApproveMemberHandler))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("an already approved member is a no-op, not an error", func(t *testing.T) {
		f := newMemberFixture(t)
		rr := f.run(t, http.MethodPost, "/members/fivem:member/approve",
			`{"actingPlayer":"fivem:admin"}`, map[string]string{"playerRef": "fivem:member"},
			f.api.RequireScope(models.ScopeMembersWrite, f.api.DevApproveMemberHandler))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "already approved") {
			t.Errorf("unexpected body: %s", rr.Body.String())
		}
	})
}

// ---- player references ----

// A forged LPC id buys the caller nothing, because membership is the real
// boundary. Asserted so a future refactor cannot make the id itself trusted.
func TestDevMembers_ForgedLpcIdStillFailsTheMembershipCheck(t *testing.T) {
	f := newMemberFixture(t)

	rr := f.addMember(t, `{"actingPlayer":"lpc:`+primitive.NewObjectID().Hex()+`","player":"fivem:brandnew"}`)
	if rr.Code != http.StatusForbidden && rr.Code != http.StatusBadRequest {
		t.Fatalf("a made-up LPC id must not be able to act, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDevMembers_RejectsMalformedPlayerReferences(t *testing.T) {
	f := newMemberFixture(t)

	for name, body := range map[string]string{
		"no colon":         `{"actingPlayer":"admin","player":"fivem:x"}`,
		"unknown provider": `{"actingPlayer":"xbox:admin","player":"fivem:x"}`,
		"empty identifier": `{"actingPlayer":"fivem:","player":"fivem:x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rr := f.addMember(t, body)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}
