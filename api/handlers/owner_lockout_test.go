package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// These cover the lockout chain reported from the app: transfer a community to
// someone who is not in a department, have the previous owner leave, and the new
// owner can never get into that department again because the only people who can
// approve their join request are themselves and admins who left with the previous
// owner.

func stubUserFindOne(mockUserDB *mocks.UserDatabase, uID primitive.ObjectID, user *models.User) {
	mr := &mocks.SingleResultHelper{}
	mr.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		ptr, ok := args.Get(0).(*models.User)
		if ok {
			*ptr = *user
		}
	}).Return(nil)
	mockUserDB.On("FindOne", mock.Anything, bson.M{"_id": uID}).Return(mr)
}

func TestRemoveCommunityFromUserRefusesTheOwner(t *testing.T) {
	uID := primitive.NewObjectID()
	cID := primitive.NewObjectID()

	user := &models.User{
		ID: uID.Hex(),
		Details: models.UserDetails{
			Communities: []models.UserCommunity{
				{ID: primitive.NewObjectID().Hex(), CommunityID: cID.Hex(), Status: "approved"},
			},
		},
	}
	community := &models.Community{
		ID:      cID,
		Details: models.CommunityDetails{OwnerID: uID.Hex()},
	}

	mockUserDB := &mocks.UserDatabase{}
	mockCommunityDB := &mocks.CommunityDatabase{}
	stubUserFindOne(mockUserDB, uID, user)
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)

	handler := User{DB: mockUserDB, CDB: mockCommunityDB}

	body, _ := json.Marshal(map[string]string{"communityId": cID.Hex()})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/user/"+uID.Hex()+"/remove-community", bytes.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"userId": uID.Hex()})
	rec := httptest.NewRecorder()

	handler.RemoveCommunityFromUserHandler(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "Transfer ownership")

	// Nothing may be mutated: this handler strips roles and department membership,
	// and there is no way back once the owner is out.
	mockUserDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	mockCommunityDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestRemoveCommunityFromUserAllowsANonOwner(t *testing.T) {
	uID := primitive.NewObjectID()
	ownerID := primitive.NewObjectID()
	cID := primitive.NewObjectID()

	user := &models.User{
		ID: uID.Hex(),
		Details: models.UserDetails{
			Communities: []models.UserCommunity{
				{ID: primitive.NewObjectID().Hex(), CommunityID: cID.Hex(), Status: "approved"},
			},
		},
	}
	community := &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			OwnerID:     ownerID.Hex(),
			Roles:       []models.Role{{ID: primitive.NewObjectID(), Name: "Officer", Members: []string{uID.Hex()}}},
			Departments: []models.Department{{ID: primitive.NewObjectID(), Name: "Police"}},
		},
	}

	mockUserDB := &mocks.UserDatabase{}
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockAuditDB := &mocks.AuditLogDatabase{}
	stubUserFindOne(mockUserDB, uID, user)
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)
	mockUserDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	mockCommunityDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockAuditDB.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	handler := User{DB: mockUserDB, CDB: mockCommunityDB, ALDB: mockAuditDB}

	body, _ := json.Marshal(map[string]string{"communityId": cID.Hex()})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/user/"+uID.Hex()+"/remove-community", bytes.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"userId": uID.Hex()})
	rec := httptest.NewRecorder()

	handler.RemoveCommunityFromUserHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	mockUserDB.AssertCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestEnsureCommunityMembership(t *testing.T) {
	uID := primitive.NewObjectID()
	communityID := primitive.NewObjectID().Hex()

	t.Run("already approved writes nothing", func(t *testing.T) {
		mockUserDB := &mocks.UserDatabase{}
		user := &models.User{Details: models.UserDetails{
			Communities: []models.UserCommunity{{CommunityID: communityID, Status: "approved"}},
		}}
		changed := ensureCommunityMembership(context.Background(), mockUserDB, uID, communityID, user)
		assert.False(t, changed)
		mockUserDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("pending entry is promoted in place, not duplicated", func(t *testing.T) {
		mockUserDB := &mocks.UserDatabase{}
		var updates []bson.M
		mockUserDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				updates = append(updates, args.Get(2).(bson.M))
			}).Return(nil, nil)

		user := &models.User{Details: models.UserDetails{
			Communities: []models.UserCommunity{{CommunityID: communityID, Status: "pending"}},
		}}
		changed := ensureCommunityMembership(context.Background(), mockUserDB, uID, communityID, user)

		assert.True(t, changed)
		assert.Len(t, updates, 1, "one write, not a push alongside the existing row")
		assert.Equal(t,
			bson.M{"$set": bson.M{"user.communities.$.status": "approved"}},
			updates[0])
	})

	t.Run("missing entry is added as approved", func(t *testing.T) {
		mockUserDB := &mocks.UserDatabase{}
		var updates []bson.M
		mockUserDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				updates = append(updates, args.Get(2).(bson.M))
			}).Return(nil, nil)

		user := &models.User{Details: models.UserDetails{
			Communities: []models.UserCommunity{{CommunityID: primitive.NewObjectID().Hex(), Status: "approved"}},
		}}
		changed := ensureCommunityMembership(context.Background(), mockUserDB, uID, communityID, user)

		assert.True(t, changed)
		// Init-if-null, then the push.
		assert.Len(t, updates, 2)
		added := updates[1]["$addToSet"].(bson.M)["user.communities"].(models.UserCommunity)
		assert.Equal(t, communityID, added.CommunityID)
		assert.Equal(t, "approved", added.Status)
		assert.NotEmpty(t, added.ID)
	})

	t.Run("nil user is a no-op", func(t *testing.T) {
		mockUserDB := &mocks.UserDatabase{}
		assert.False(t, ensureCommunityMembership(context.Background(), mockUserDB, uID, communityID, nil))
		assert.False(t, ensureCommunityMembership(context.Background(), nil, uID, communityID, &models.User{}))
	})
}

func TestDepartmentsScreenDataOwnerIsAlwaysAMember(t *testing.T) {
	ownerID := primitive.NewObjectID()
	cID := primitive.NewObjectID()

	// The shape the old transfer handler left behind: ownerID points at a user
	// with no approved membership row of their own.
	owner := &models.User{ID: ownerID.Hex()}
	community := &models.Community{
		ID:      cID,
		Details: models.CommunityDetails{OwnerID: ownerID.Hex()},
	}

	mockUserDB := &mocks.UserDatabase{}
	mockCommunityDB := &mocks.CommunityDatabase{}
	stubUserFindOne(mockUserDB, ownerID, owner)
	mockCommunityDB.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)

	handler := Community{DB: mockCommunityDB, UDB: mockUserDB}

	url := fmt.Sprintf("/api/v2/departments-screen-data?communityId=%s&userId=%s", cID.Hex(), ownerID.Hex())
	rec := httptest.NewRecorder()
	handler.GetDepartmentsScreenDataHandler(rec, httptest.NewRequest(http.MethodGet, url, nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	var body map[string]bool
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.True(t, body["isMember"], "the owner is a member of their own community")
	assert.True(t, body["canManageDepartments"], "the owner can manage departments with no role")
}

func TestDepartmentsScreenDataNonMemberStillRefused(t *testing.T) {
	userID := primitive.NewObjectID()
	cID := primitive.NewObjectID()

	stranger := &models.User{ID: userID.Hex()}
	community := &models.Community{
		ID:      cID,
		Details: models.CommunityDetails{OwnerID: primitive.NewObjectID().Hex()},
	}

	mockUserDB := &mocks.UserDatabase{}
	mockCommunityDB := &mocks.CommunityDatabase{}
	stubUserFindOne(mockUserDB, userID, stranger)
	mockCommunityDB.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)

	handler := Community{DB: mockCommunityDB, UDB: mockUserDB}

	url := fmt.Sprintf("/api/v2/departments-screen-data?communityId=%s&userId=%s", cID.Hex(), userID.Hex())
	rec := httptest.NewRecorder()
	handler.GetDepartmentsScreenDataHandler(rec, httptest.NewRequest(http.MethodGet, url, nil))

	var body map[string]bool
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.False(t, body["isMember"])
	assert.False(t, body["canManageDepartments"])
}
