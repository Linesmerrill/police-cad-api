package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// guardFixture is a community whose owner holds admin only through the Head
// Admin role, plus an unrelated Officer role the owner is also in.
type guardFixture struct {
	cID       primitive.ObjectID
	ownerID   string
	otherID   string
	headAdmin models.Role
	officer   models.Role
	community *models.Community
	cdb       *mocks.CommunityDatabase
	handler   Community
}

func newGuardFixture(roles func(owner, other string) []models.Role) *guardFixture {
	f := &guardFixture{
		cID:     primitive.NewObjectID(),
		ownerID: primitive.NewObjectID().Hex(),
		otherID: primitive.NewObjectID().Hex(),
	}
	f.headAdmin = models.BuildHeadAdminRole(f.ownerID)
	f.officer = models.Role{
		ID:          primitive.NewObjectID(),
		Name:        "Officer",
		Members:     []string{f.ownerID, f.otherID},
		Permissions: []models.Permission{{ID: primitive.NewObjectID(), Name: "manage members", Enabled: true}},
	}
	communityRoles := []models.Role{f.headAdmin, f.officer}
	if roles != nil {
		communityRoles = roles(f.ownerID, f.otherID)
	}
	f.community = &models.Community{ID: f.cID, Details: models.CommunityDetails{OwnerID: f.ownerID, Roles: communityRoles}}

	f.cdb = &mocks.CommunityDatabase{}
	f.cdb.On("FindOne", mock.Anything, bson.M{"_id": f.cID}).Return(f.community, nil)
	f.cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	udb := &mocks.UserDatabase{}
	noUser := &mocks.SingleResultHelper{}
	noUser.On("Decode", mock.Anything).Return(mongo.ErrNoDocuments)
	udb.On("FindOne", mock.Anything, mock.Anything).Return(noUser).Maybe()

	aldb := &mocks.AuditLogDatabase{}
	aldb.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	f.handler = Community{DB: f.cdb, UDB: udb, ALDB: aldb}
	return f
}

func (f *guardFixture) serve(method string, vars map[string]string, body interface{}, h http.HandlerFunc) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	vars["communityId"] = f.cID.Hex()
	req := httptest.NewRequest(method, "/", &buf)
	req = mux.SetURLVars(req, vars)
	req = req.WithContext(api.WithAuthenticatedUserID(req.Context(), f.ownerID))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func assertOwnerMustKeepAdmin(t *testing.T, f *guardFixture, rec *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, http.StatusConflict, rec.Code)
	var body map[string]interface{}
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, ownerMustKeepAdminMessage, body["message"])
	assert.Equal(t, ownerMustKeepAdminMessage, body["response"].(map[string]interface{})["message"])
	f.cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestDeleteRoleMember_RefusesRemovingTheOwnersOnlyAdmin(t *testing.T) {
	f := newGuardFixture(nil)
	rec := f.serve(http.MethodDelete, map[string]string{"roleId": f.headAdmin.ID.Hex(), "memberId": f.ownerID}, nil, f.handler.DeleteRoleMemberHandler)
	assertOwnerMustKeepAdmin(t, f, rec)
}

func TestDeleteRoleMember_AllowsOtherRemovals(t *testing.T) {
	t.Run("the owner from a role that is not their admin", func(t *testing.T) {
		f := newGuardFixture(nil)
		rec := f.serve(http.MethodDelete, map[string]string{"roleId": f.officer.ID.Hex(), "memberId": f.ownerID}, nil, f.handler.DeleteRoleMemberHandler)
		assert.Equal(t, http.StatusOK, rec.Code)
		f.cdb.AssertCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	})
	t.Run("someone else from the admin role", func(t *testing.T) {
		f := newGuardFixture(func(owner, other string) []models.Role {
			r := models.BuildHeadAdminRole(owner)
			r.Members = append(r.Members, other)
			return []models.Role{r}
		})
		roleID := f.community.Details.Roles[0].ID.Hex()
		rec := f.serve(http.MethodDelete, map[string]string{"roleId": roleID, "memberId": f.otherID}, nil, f.handler.DeleteRoleMemberHandler)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
	t.Run("the owner from Head Admin while another role keeps them admin", func(t *testing.T) {
		f := newGuardFixture(func(owner, other string) []models.Role {
			admins := models.Role{ID: primitive.NewObjectID(), Name: "Admins", Members: []string{owner},
				Permissions: []models.Permission{{ID: primitive.NewObjectID(), Name: "administrator", Enabled: true}}}
			return []models.Role{models.BuildHeadAdminRole(owner), admins}
		})
		roleID := f.community.Details.Roles[0].ID.Hex()
		rec := f.serve(http.MethodDelete, map[string]string{"roleId": roleID, "memberId": f.ownerID}, nil, f.handler.DeleteRoleMemberHandler)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestUpdateRolePermissions_RefusesDisablingTheOwnersAdmin(t *testing.T) {
	f := newGuardFixture(nil)
	perms := append([]models.Permission(nil), f.headAdmin.Permissions...)
	for i := range perms {
		perms[i].Enabled = false
	}
	rec := f.serve(http.MethodPut, map[string]string{"roleId": f.headAdmin.ID.Hex()}, map[string]interface{}{"permissions": perms}, f.handler.UpdateRolePermissionsHandler)
	assertOwnerMustKeepAdmin(t, f, rec)
}

func TestUpdateRolePermissions_AllowsChangesThatKeepAdmin(t *testing.T) {
	f := newGuardFixture(nil)
	perms := append([]models.Permission(nil), f.headAdmin.Permissions...)
	perms[1].Enabled = true // turn on "manage community settings"; administrator stays on
	rec := f.serve(http.MethodPut, map[string]string{"roleId": f.headAdmin.ID.Hex()}, map[string]interface{}{"permissions": perms}, f.handler.UpdateRolePermissionsHandler)
	assert.Equal(t, http.StatusOK, rec.Code)

	f2 := newGuardFixture(nil)
	rec = f2.serve(http.MethodPut, map[string]string{"roleId": f2.officer.ID.Hex()}, map[string]interface{}{"permissions": []models.Permission{}}, f2.handler.UpdateRolePermissionsHandler)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestDeleteRoleByID_RefusesDeletingTheOwnersAdminRole(t *testing.T) {
	f := newGuardFixture(nil)
	rec := f.serve(http.MethodDelete, map[string]string{"roleId": f.headAdmin.ID.Hex()}, nil, f.handler.DeleteRoleByIDHandler)
	assertOwnerMustKeepAdmin(t, f, rec)
}

func TestDeleteRoleByID_AllowsOtherRoles(t *testing.T) {
	f := newGuardFixture(nil)
	rec := f.serve(http.MethodDelete, map[string]string{"roleId": f.officer.ID.Hex()}, nil, f.handler.DeleteRoleByIDHandler)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// A legacy owner who never had admin is not made worse by an unrelated edit,
// so the guard stays out of the way there (the heal restores their admin).
func TestOwnerAdminGuard_DoesNotBlockLegacyCommunities(t *testing.T) {
	f := newGuardFixture(func(owner, other string) []models.Role {
		return []models.Role{{ID: primitive.NewObjectID(), Name: "Officer", Members: []string{other}}}
	})
	roleID := f.community.Details.Roles[0].ID.Hex()
	rec := f.serve(http.MethodDelete, map[string]string{"roleId": roleID}, nil, f.handler.DeleteRoleByIDHandler)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestStripsOwnerAdmin(t *testing.T) {
	owner := primitive.NewObjectID().Hex()
	ha := models.BuildHeadAdminRole(owner)
	c := &models.Community{Details: models.CommunityDetails{OwnerID: owner, Roles: []models.Role{ha}}}

	assert.True(t, stripsOwnerAdmin(c, nil))
	assert.False(t, stripsOwnerAdmin(c, []models.Role{ha}))
	assert.False(t, stripsOwnerAdmin(nil, nil))
	assert.False(t, stripsOwnerAdmin(&models.Community{}, nil), "no owner, nothing to protect")

	// rolesWith must not alias the stored community's slices.
	after := rolesWith(c.Details.Roles, ha.ID, func(r *models.Role) { r.Members[0] = "someone-else" })
	assert.Equal(t, owner, c.Details.Roles[0].Members[0])
	assert.True(t, stripsOwnerAdmin(c, after))
}

// The incoming owner gets Head Admin, an approved membership, the member count
// bump that membership is worth, and is taken off the ban list.
func TestTransferOwnership_NewOwnerIsAnApprovedHeadAdmin(t *testing.T) {
	cID := primitive.NewObjectID()
	oldOwner := primitive.NewObjectID().Hex()
	newOwnerOID := primitive.NewObjectID()
	newOwner := newOwnerOID.Hex()

	community := &models.Community{ID: cID, Details: models.CommunityDetails{
		OwnerID: oldOwner,
		Roles:   []models.Role{models.BuildHeadAdminRole(oldOwner)},
		BanList: []string{newOwner},
	}}
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)
	var communityUpdates []bson.M
	cdb.On("UpdateOne", mock.Anything, bson.M{"_id": cID}, mock.Anything).
		Run(func(a mock.Arguments) { communityUpdates = append(communityUpdates, a.Get(2).(bson.M)) }).
		Return(nil)

	udb := &mocks.UserDatabase{}
	found := &mocks.SingleResultHelper{}
	found.On("Decode", mock.Anything).Run(func(a mock.Arguments) {
		*(a.Get(0).(*models.User)) = models.User{ID: newOwner, Details: models.UserDetails{
			Communities: []models.UserCommunity{{CommunityID: cID.Hex(), Status: "banned"}},
		}}
	}).Return(nil)
	udb.On("FindOne", mock.Anything, bson.M{"_id": newOwnerOID}).Return(found)
	var userUpdates []bson.M
	udb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(a mock.Arguments) { userUpdates = append(userUpdates, a.Get(2).(bson.M)) }).
		Return(&mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil)

	handler := Community{DB: cdb, UDB: udb}
	body, _ := json.Marshal(map[string]string{"currentUserId": oldOwner, "newOwnerId": newOwner})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"communityId": cID.Hex()})
	rec := httptest.NewRecorder()
	handler.TransferCommunityOwnershipHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	if assert.Len(t, communityUpdates, 2, "the transfer, then the membersCount bump") {
		set := communityUpdates[0]["$set"].(bson.M)
		assert.Equal(t, newOwner, set["community.ownerID"])
		assert.True(t, models.OwnerHasAdmin(set["community.roles"].([]models.Role), newOwner))
		assert.Equal(t, bson.M{"community.banList": newOwner}, communityUpdates[0]["$pull"])
		assert.Equal(t, bson.M{"$inc": bson.M{"community.membersCount": 1}}, communityUpdates[1])
	}
	if assert.Len(t, userUpdates, 1) {
		assert.Equal(t, bson.M{"$set": bson.M{"user.communities.$.status": "approved"}}, userUpdates[0])
	}
}

func TestEnsureCommunityMembership_CountsApprovedMembers(t *testing.T) {
	uID := primitive.NewObjectID()
	cID := primitive.NewObjectID()

	run := func(user *models.User, res *mongo.UpdateResult, err error) (bool, *mocks.CommunityDatabase) {
		udb := &mocks.UserDatabase{}
		udb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(res, err)
		cdb := &mocks.CommunityDatabase{}
		cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		return ensureCommunityMembership(context.Background(), udb, cdb, uID, cID.Hex(), user), cdb
	}
	inc := bson.M{"$inc": bson.M{"community.membersCount": 1}}

	t.Run("a new approved entry bumps the count once", func(t *testing.T) {
		changed, cdb := run(&models.User{}, &mongo.UpdateResult{ModifiedCount: 1}, nil)
		assert.True(t, changed)
		cdb.AssertCalled(t, "UpdateOne", mock.Anything, bson.M{"_id": cID}, inc)
		cdb.AssertNumberOfCalls(t, "UpdateOne", 1)
	})
	t.Run("a promoted entry bumps the count", func(t *testing.T) {
		user := &models.User{Details: models.UserDetails{Communities: []models.UserCommunity{{CommunityID: cID.Hex(), Status: "pending"}}}}
		changed, cdb := run(user, &mongo.UpdateResult{ModifiedCount: 1}, nil)
		assert.True(t, changed)
		cdb.AssertCalled(t, "UpdateOne", mock.Anything, bson.M{"_id": cID}, inc)
	})
	t.Run("already approved does not", func(t *testing.T) {
		user := &models.User{Details: models.UserDetails{Communities: []models.UserCommunity{{CommunityID: cID.Hex(), Status: "approved"}}}}
		changed, cdb := run(user, nil, nil)
		assert.False(t, changed)
		cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	})
	t.Run("losing a race to another heal does not", func(t *testing.T) {
		changed, cdb := run(&models.User{}, &mongo.UpdateResult{MatchedCount: 0, ModifiedCount: 0}, nil)
		assert.False(t, changed)
		cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	})
	t.Run("a failed write does not", func(t *testing.T) {
		changed, cdb := run(&models.User{}, nil, errors.New("boom"))
		assert.False(t, changed)
		cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	})
}

// The owner heal on the community load path and the V1 heal on sign-in both
// count the member they add.
func TestMembershipHeals_BumpMembersCount(t *testing.T) {
	inc := bson.M{"$inc": bson.M{"community.membersCount": 1}}

	ownerID := primitive.NewObjectID()
	community := communityOwnedBy(ownerID.Hex())
	udb := ownerLookup(models.User{ID: ownerID.Hex()}, true)
	udb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	cdb := &mocks.CommunityDatabase{}
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	assert.True(t, healOwnerMembership(context.Background(), udb, cdb, community))
	cdb.AssertCalled(t, "UpdateOne", mock.Anything, bson.M{"_id": community.ID}, inc)

	cid := primitive.NewObjectID()
	vudb, _ := recordingUserDB()
	vcdb := communityLookup(&models.Community{ID: cid}, nil)
	assert.True(t, healV1Membership(context.Background(), vudb, vcdb, nil, v1Member(cid.Hex())))
	vcdb.AssertCalled(t, "UpdateOne", mock.Anything, bson.M{"_id": cid}, inc)
}
