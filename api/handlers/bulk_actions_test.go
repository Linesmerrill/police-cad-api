package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// --- helpers ---------------------------------------------------------------

func bulkRequest(t *testing.T, path, communityID string, body interface{}) *http.Request {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	return mux.SetURLVars(req, map[string]string{"communityId": communityID})
}

func asActor(req *http.Request, actorID string) *http.Request {
	return req.WithContext(api.WithAuthenticatedUserID(req.Context(), actorID))
}

func decodeBulk(t *testing.T, rr *httptest.ResponseRecorder) bulkActionResponse {
	t.Helper()
	var resp bulkActionResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	return resp
}

func resultFor(resp bulkActionResponse, id string) (bulkItemResult, bool) {
	for _, r := range resp.Results {
		if r.ID == id {
			return r, true
		}
	}
	return bulkItemResult{}, false
}

func bulkStubUser(udb *mocks.UserDatabase, id string, user *models.User, err error) {
	oid, _ := primitive.ObjectIDFromHex(id)
	sr := &mocks.SingleResultHelper{}
	sr.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		if p, ok := args.Get(0).(*models.User); ok && user != nil {
			*p = *user
		}
	}).Return(err)
	udb.On("FindOne", mock.Anything, bson.M{"_id": oid}).Return(sr)
}

func auditMock() *mocks.AuditLogDatabase {
	al := &mocks.AuditLogDatabase{}
	al.On("InsertOne", mock.Anything, mock.Anything).Return(&mocks.InsertOneResultHelper{}, nil).Maybe()
	return al
}

// roleWith builds a role holding the given members with one enabled permission.
func roleWith(permission string, members ...string) models.Role {
	return models.Role{
		ID:          primitive.NewObjectID(),
		Name:        "Staff",
		Members:     members,
		Permissions: []models.Permission{{ID: primitive.NewObjectID(), Name: permission, Enabled: true}},
	}
}

// --- normalizeBulkIDs ------------------------------------------------------

func TestNormalizeBulkIDs(t *testing.T) {
	ids, err := normalizeBulkIDs([]string{" a ", "b", "a", "", "  "})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, ids, "trims, drops blanks and duplicates, keeps order")

	_, err = normalizeBulkIDs(nil)
	assert.Error(t, err)
	_, err = normalizeBulkIDs([]string{"", " "})
	assert.Error(t, err)

	hundred := make([]string, 0, bulkActionMaxIDs+1)
	for i := 0; i < bulkActionMaxIDs; i++ {
		hundred = append(hundred, fmt.Sprintf("id-%d", i))
	}
	_, err = normalizeBulkIDs(hundred)
	assert.NoError(t, err, "exactly the cap is allowed")

	// Duplicates do not count toward the cap.
	_, err = normalizeBulkIDs(append(hundred, "id-0"))
	assert.NoError(t, err)

	_, err = normalizeBulkIDs(append(hundred, "one-too-many"))
	assert.Error(t, err)
}

// --- bulk member removal ---------------------------------------------------

func TestBulkRemoveMembers_RejectsOverCap(t *testing.T) {
	cID := primitive.NewObjectID()
	ids := make([]string, 0, bulkActionMaxIDs+1)
	for i := 0; i <= bulkActionMaxIDs; i++ {
		ids = append(ids, primitive.NewObjectID().Hex())
	}
	udb := &mocks.UserDatabase{}
	cdb := &mocks.CommunityDatabase{}
	u := User{DB: udb, CDB: cdb, ALDB: auditMock()}

	req := asActor(bulkRequest(t, "/x", cID.Hex(), map[string]interface{}{"userIds": ids}), primitive.NewObjectID().Hex())
	rr := httptest.NewRecorder()
	u.BulkRemoveCommunityMembersHandler(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
	udb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestBulkRemoveMembers_PermissionDenied(t *testing.T) {
	cID := primitive.NewObjectID()
	actor := primitive.NewObjectID().Hex()
	target := primitive.NewObjectID().Hex()

	community := &models.Community{ID: cID, Details: models.CommunityDetails{
		OwnerID: primitive.NewObjectID().Hex(),
		// The actor holds a role, but not one that can manage members.
		Roles: []models.Role{roleWith("manage forms", actor)},
	}}
	udb := &mocks.UserDatabase{}
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)
	u := User{DB: udb, CDB: cdb, ALDB: auditMock()}

	req := asActor(bulkRequest(t, "/x", cID.Hex(), map[string]interface{}{"userIds": []string{target}}), actor)
	rr := httptest.NewRecorder()
	u.BulkRemoveCommunityMembersHandler(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	udb.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
	udb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// The website has no bearer token, so its browser calls carry ?userId=. A bulk
// kick must not believe that without the server-to-server secret, otherwise
// anyone could name the owner (public via search) and empty a community.
func TestBulkRemoveMembers_QueryUserIDNeedsGatewaySecret(t *testing.T) {
	t.Setenv(apiGatewayKeyEnv, "s3cret")
	cID := primitive.NewObjectID()
	owner := primitive.NewObjectID().Hex()
	target := primitive.NewObjectID().Hex()

	community := &models.Community{ID: cID, Details: models.CommunityDetails{OwnerID: owner}}
	udb := &mocks.UserDatabase{}
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)
	u := User{DB: udb, CDB: cdb, ALDB: auditMock()}

	req := bulkRequest(t, "/x?userId="+owner, cID.Hex(), map[string]interface{}{"userIds": []string{target}})
	rr := httptest.NewRecorder()
	u.BulkRemoveCommunityMembersHandler(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	udb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestBulkRemoveMembers_PerIDResultsAndProtections(t *testing.T) {
	t.Setenv(apiGatewayKeyEnv, "s3cret")
	cID := primitive.NewObjectID()
	communityID := cID.Hex()
	owner := primitive.NewObjectID().Hex()
	actor := primitive.NewObjectID().Hex() // has "manage members"
	member := primitive.NewObjectID().Hex()
	outsider := primitive.NewObjectID().Hex()
	missing := primitive.NewObjectID().Hex()
	memberOID, _ := primitive.ObjectIDFromHex(member)

	staff := roleWith("manage members", actor, member)
	dept := models.Department{ID: primitive.NewObjectID(), Name: "LEO"}
	community := &models.Community{ID: cID, Details: models.CommunityDetails{
		OwnerID:     owner,
		Roles:       []models.Role{staff},
		Departments: []models.Department{dept},
	}}

	udb := &mocks.UserDatabase{}
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)

	bulkStubUser(udb, actor, &models.User{ID: actor, Details: models.UserDetails{Username: "admin"}}, nil)
	bulkStubUser(udb, member, &models.User{ID: member, Details: models.UserDetails{
		Username:    "kicked",
		Communities: []models.UserCommunity{{CommunityID: communityID, Status: "approved"}},
	}}, nil)
	bulkStubUser(udb, outsider, &models.User{ID: outsider, Details: models.UserDetails{
		Communities: []models.UserCommunity{{CommunityID: primitive.NewObjectID().Hex(), Status: "approved"}},
	}}, nil)
	bulkStubUser(udb, missing, nil, mongo.ErrNoDocuments)

	// The member's removal runs the exact same writes as the single endpoint.
	udb.On("UpdateOne", mock.Anything, bson.M{"_id": memberOID},
		bson.M{"$pull": bson.M{"user.communities": bson.M{"communityId": communityID}}}).
		Return(&mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil).Once()
	cdb.On("UpdateOne", mock.Anything, bson.M{"_id": cID}, bson.M{"$inc": bson.M{"community.membersCount": -1}}).Return(nil).Once()
	cdb.On("UpdateOne", mock.Anything,
		bson.M{"_id": cID, "community.roles._id": staff.ID, "community.roles.members": member},
		bson.M{"$pull": bson.M{"community.roles.$.members": member}}).Return(nil).Once()
	cdb.On("UpdateOne", mock.Anything,
		bson.M{"_id": cID, "community.departments._id": dept.ID},
		bson.M{"$pull": bson.M{"community.departments.$.members": bson.M{"userID": member}}}).Return(nil).Once()

	u := User{DB: udb, CDB: cdb, ALDB: auditMock()}

	ids := []string{owner, actor, member, member, outsider, missing, "not-an-id"}
	// Authenticated through the website path: ?userId= plus the shared secret.
	req := bulkRequest(t, "/x?userId="+actor, communityID, map[string]interface{}{"userIds": ids})
	req.Header.Set(apiGatewayHeader, "s3cret")
	rr := httptest.NewRecorder()
	u.BulkRemoveCommunityMembersHandler(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	resp := decodeBulk(t, rr)
	assert.Equal(t, 1, resp.Succeeded)
	assert.Equal(t, 5, resp.Failed)
	assert.Len(t, resp.Results, 6, "duplicate ids are acted on once")

	r, _ := resultFor(resp, member)
	assert.True(t, r.OK)
	for id, want := range map[string]string{
		owner:       "the community owner can't be removed",
		actor:       "you can't remove yourself",
		outsider:    "not a member of this community",
		missing:     "user not found",
		"not-an-id": "invalid user id",
	} {
		r, found := resultFor(resp, id)
		require.True(t, found, id)
		assert.False(t, r.OK, id)
		assert.Equal(t, want, r.Error, id)
	}

	udb.AssertExpectations(t)
	cdb.AssertExpectations(t)
	// Only the one real member was written to.
	udb.AssertNumberOfCalls(t, "UpdateOne", 1)
	cdb.AssertNumberOfCalls(t, "UpdateOne", 3)
}

func TestBulkRemoveMembers_AdministratorPasses(t *testing.T) {
	cID := primitive.NewObjectID()
	actor := primitive.NewObjectID().Hex()
	target := primitive.NewObjectID().Hex()
	community := &models.Community{ID: cID, Details: models.CommunityDetails{
		OwnerID: primitive.NewObjectID().Hex(),
		Roles:   []models.Role{roleWith("administrator", actor)},
	}}
	udb := &mocks.UserDatabase{}
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)
	bulkStubUser(udb, actor, &models.User{ID: actor}, nil)
	bulkStubUser(udb, target, &models.User{ID: target}, nil) // not a member: no writes
	u := User{DB: udb, CDB: cdb, ALDB: auditMock()}

	req := asActor(bulkRequest(t, "/x", cID.Hex(), map[string]interface{}{"userIds": []string{target}}), actor)
	rr := httptest.NewRecorder()
	u.BulkRemoveCommunityMembersHandler(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	resp := decodeBulk(t, rr)
	assert.Equal(t, 0, resp.Succeeded)
	assert.Equal(t, 1, resp.Failed)
}

// --- bulk civilian delete --------------------------------------------------

type recordingActiveCivDB struct {
	mu      sync.Mutex
	deleted []interface{}
}

func (f *recordingActiveCivDB) FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.UserActiveCivilian, error) {
	return nil, mongo.ErrNoDocuments
}
func (f *recordingActiveCivDB) UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error {
	return nil
}
func (f *recordingActiveCivDB) DeleteMany(_ context.Context, filter interface{}, _ ...*options.DeleteOptions) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, filter)
	return 1, nil
}
func (f *recordingActiveCivDB) EnsureIndexes(context.Context) error { return nil }

type recordingSessionDB struct {
	mu    sync.Mutex
	finds []interface{}
}

func (f *recordingSessionDB) FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.ClockSession, error) {
	return nil, mongo.ErrNoDocuments
}
func (f *recordingSessionDB) Find(_ context.Context, filter interface{}, _ ...*options.FindOptions) ([]models.ClockSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finds = append(f.finds, filter)
	return nil, nil
}
func (f *recordingSessionDB) InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	return nil, nil
}
func (f *recordingSessionDB) UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error {
	return nil
}
func (f *recordingSessionDB) FindOneAndUpdate(context.Context, interface{}, interface{}, ...*options.FindOneAndUpdateOptions) *mongo.SingleResult {
	return nil
}
func (f *recordingSessionDB) CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error) {
	return 0, nil
}

func TestBulkDeleteCivilians_PermissionDenied(t *testing.T) {
	cID := primitive.NewObjectID()
	actor := primitive.NewObjectID().Hex()
	community := &models.Community{ID: cID, Details: models.CommunityDetails{OwnerID: primitive.NewObjectID().Hex()}}

	civdb := &mocks.CivilianDatabase{}
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)
	c := Civilian{DB: civdb, CommDB: cdb, UDB: &mocks.UserDatabase{}, ALDB: auditMock()}

	req := asActor(bulkRequest(t, "/x", cID.Hex(), map[string]interface{}{"civilianIds": []string{primitive.NewObjectID().Hex()}}), actor)
	rr := httptest.NewRecorder()
	c.BulkDeleteCommunityCiviliansHandler(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	civdb.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
	civdb.AssertNotCalled(t, "DeleteOne", mock.Anything, mock.Anything)
}

func TestBulkDeleteCivilians_RejectsOverCap(t *testing.T) {
	ids := make([]string, 0, bulkActionMaxIDs+1)
	for i := 0; i <= bulkActionMaxIDs; i++ {
		ids = append(ids, primitive.NewObjectID().Hex())
	}
	cdb := &mocks.CommunityDatabase{}
	c := Civilian{DB: &mocks.CivilianDatabase{}, CommDB: cdb}

	req := asActor(bulkRequest(t, "/x", primitive.NewObjectID().Hex(), map[string]interface{}{"civilianIds": ids}), primitive.NewObjectID().Hex())
	rr := httptest.NewRecorder()
	c.BulkDeleteCommunityCiviliansHandler(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
}

func TestBulkDeleteCivilians_CrossCommunityRejectedAndCascadeRuns(t *testing.T) {
	cID := primitive.NewObjectID()
	communityID := cID.Hex()
	owner := primitive.NewObjectID().Hex()

	ours := primitive.NewObjectID()
	theirs := primitive.NewObjectID()
	gone := primitive.NewObjectID()

	community := &models.Community{ID: cID, Details: models.CommunityDetails{OwnerID: owner}}
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)

	civdb := &mocks.CivilianDatabase{}
	civdb.On("FindOne", mock.Anything, bson.M{"_id": ours}).Return(&models.Civilian{ID: ours, Details: models.CivilianDetails{
		FirstName: "John", LastName: "Doe", ActiveCommunityID: communityID,
	}}, nil)
	civdb.On("FindOne", mock.Anything, bson.M{"_id": theirs}).Return(&models.Civilian{ID: theirs, Details: models.CivilianDetails{
		ActiveCommunityID: primitive.NewObjectID().Hex(),
	}}, nil)
	civdb.On("FindOne", mock.Anything, bson.M{"_id": gone}).Return(nil, mongo.ErrNoDocuments)
	civdb.On("DeleteOne", mock.Anything, bson.M{"_id": ours}).Return(nil).Once()

	udb := &mocks.UserDatabase{}
	bulkStubUser(udb, owner, &models.User{ID: owner, Details: models.UserDetails{Username: "owner"}}, nil)

	acdb := &recordingActiveCivDB{}
	sdb := &recordingSessionDB{}
	c := Civilian{DB: civdb, CommDB: cdb, UDB: udb, ACDB: acdb, SDB: sdb, ALDB: auditMock()}

	req := asActor(bulkRequest(t, "/x", communityID, map[string]interface{}{
		"civilianIds": []string{ours.Hex(), theirs.Hex(), gone.Hex(), "bad"},
	}), owner)
	rr := httptest.NewRecorder()
	c.BulkDeleteCommunityCiviliansHandler(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	resp := decodeBulk(t, rr)
	assert.Equal(t, 1, resp.Succeeded)
	assert.Equal(t, 3, resp.Failed)

	r, _ := resultFor(resp, ours.Hex())
	assert.True(t, r.OK)
	r, _ = resultFor(resp, theirs.Hex())
	assert.Equal(t, "civilian is not in this community", r.Error)
	r, _ = resultFor(resp, gone.Hex())
	assert.Equal(t, "civilian not found", r.Error)
	r, _ = resultFor(resp, "bad")
	assert.Equal(t, "invalid civilian id", r.Error)

	// Only our civilian was deleted, and the same cascade as the single delete ran for it.
	civdb.AssertExpectations(t)
	civdb.AssertNumberOfCalls(t, "DeleteOne", 1)
	assert.Equal(t, []interface{}{bson.M{"civilianId": ours.Hex(), "status": "active"}}, sdb.finds)
	assert.Equal(t, []interface{}{bson.M{"civilianId": ours.Hex()}}, acdb.deleted)
}
