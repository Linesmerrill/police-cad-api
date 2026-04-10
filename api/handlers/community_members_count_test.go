package handlers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// approvedMembersFilter is the filter shape used by both CommunityHandler
// (self-healing read) and CommunityMembersHandler. The tests assert against
// this exact shape so any future drift between the two endpoints fails fast.
func approvedMembersFilter(communityID string) bson.M {
	return bson.M{
		"$and": []bson.M{
			{"user.communities": bson.M{"$exists": true}},
			{"user.communities": bson.M{"$ne": nil}},
			{"user.communities": bson.M{
				"$elemMatch": bson.M{
					"communityId": communityID,
					"status":      "approved",
				},
			}},
		},
	}
}

// -----------------------------------------------------------------------------
// CommunityHandler — self-healing read
// -----------------------------------------------------------------------------

func TestCommunity_CommunityHandler_UsesLiveCount_OverwritesStored(t *testing.T) {
	communityID := "507f1f77bcf86cd799439011"
	communityObjectID, _ := primitive.ObjectIDFromHex(communityID)

	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	storedCommunity := &models.Community{
		ID: communityObjectID,
		Details: models.CommunityDetails{
			Name:         "Test Community",
			MembersCount: 85, // stale, drifted
		},
	}
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": communityObjectID}).
		Return(storedCommunity, nil)
	mockUserDB.On("CountDocuments", mock.Anything, approvedMembersFilter(communityID)).
		Return(int64(35), nil)

	c := handlers.Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	req, _ := http.NewRequest("GET", "/api/v1/community/"+communityID, nil)
	req = mux.SetURLVars(req, map[string]string{"community_id": communityID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.CommunityHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var response models.Community
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
	assert.Equal(t, 35, response.Details.MembersCount,
		"response should reflect the live count, not the stored value")

	mockCommunityDB.AssertExpectations(t)
	mockUserDB.AssertExpectations(t)
}

func TestCommunity_CommunityHandler_CountFails_FallsBackToStored(t *testing.T) {
	communityID := "507f1f77bcf86cd799439011"
	communityObjectID, _ := primitive.ObjectIDFromHex(communityID)

	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	storedCommunity := &models.Community{
		ID: communityObjectID,
		Details: models.CommunityDetails{
			Name:         "Test Community",
			MembersCount: 42,
		},
	}
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": communityObjectID}).
		Return(storedCommunity, nil)
	mockUserDB.On("CountDocuments", mock.Anything, approvedMembersFilter(communityID)).
		Return(int64(0), errors.New("transient mongo error"))

	c := handlers.Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	req, _ := http.NewRequest("GET", "/api/v1/community/"+communityID, nil)
	req = mux.SetURLVars(req, map[string]string{"community_id": communityID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.CommunityHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code,
		"count failure should NOT fail the request — it must fall back to stored")

	var response models.Community
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
	assert.Equal(t, 42, response.Details.MembersCount,
		"on count failure, response should preserve the stored value")

	mockCommunityDB.AssertExpectations(t)
	mockUserDB.AssertExpectations(t)
}

func TestCommunity_CommunityHandler_CommunityNotFound_ReturnsNotFound(t *testing.T) {
	communityID := "507f1f77bcf86cd799439011"
	communityObjectID, _ := primitive.ObjectIDFromHex(communityID)

	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": communityObjectID}).
		Return((*models.Community)(nil), mongo.ErrNoDocuments)

	c := handlers.Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	req, _ := http.NewRequest("GET", "/api/v1/community/"+communityID, nil)
	req = mux.SetURLVars(req, map[string]string{"community_id": communityID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.CommunityHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
	mockCommunityDB.AssertExpectations(t)
	mockUserDB.AssertNotCalled(t, "CountDocuments", mock.Anything, mock.Anything)
}

func TestCommunity_CommunityHandler_InvalidObjectID_ReturnsBadRequest(t *testing.T) {
	communityID := "not-a-valid-object-id"

	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	c := handlers.Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	req, _ := http.NewRequest("GET", "/api/v1/community/"+communityID, nil)
	req = mux.SetURLVars(req, map[string]string{"community_id": communityID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.CommunityHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	mockCommunityDB.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
	mockUserDB.AssertNotCalled(t, "CountDocuments", mock.Anything, mock.Anything)
}

// -----------------------------------------------------------------------------
// BanUserFromCommunityHandler — conditional decrement on prior approved status
// -----------------------------------------------------------------------------

// banUserRequest builds the wire request for BanUserFromCommunityHandler.
func banUserRequest(t *testing.T, userID, communityID string) *http.Request {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"communityId": communityID})
	req, err := http.NewRequest("POST", "/api/v1/user/"+userID+"/ban-community", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return mux.SetURLVars(req, map[string]string{"userId": userID})
}

// stubUserFindOne wires up u.DB.FindOne to decode into the supplied user model.
func stubUserFindOne(mockUserDB *mocks.UserDatabase, userObjectID primitive.ObjectID, userDoc *models.User) *mocks.SingleResultHelper {
	mockResult := &mocks.SingleResultHelper{}
	mockResult.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		userPtr := args.Get(0).(*models.User)
		*userPtr = *userDoc
	}).Return(nil)
	mockUserDB.On("FindOne", mock.Anything, bson.M{"_id": userObjectID}).Return(mockResult)
	return mockResult
}

func TestUser_BanUserFromCommunityHandler_PriorStatusApproved_DecrementsCount(t *testing.T) {
	userID := "507f1f77bcf86cd799439011"
	communityID := "507f1f77bcf86cd799439012"
	userObjectID, _ := primitive.ObjectIDFromHex(userID)
	communityObjectID, _ := primitive.ObjectIDFromHex(communityID)

	mockUserDB := &mocks.UserDatabase{}
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockAuditLogDB := &mocks.AuditLogDatabase{}

	stubUserFindOne(mockUserDB, userObjectID, &models.User{
		ID: userID,
		Details: models.UserDetails{
			Communities: []models.UserCommunity{
				{ID: "uc1", CommunityID: communityID, Status: "approved"},
			},
		},
	})

	mockUserDB.On("UpdateOne", mock.Anything,
		bson.M{"_id": userObjectID, "user.communities.communityId": communityID},
		bson.M{"$set": bson.M{"user.communities.$.status": "banned"}},
	).Return(&mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil)

	mockCommunityDB.On("UpdateOne", mock.Anything,
		bson.M{"_id": communityObjectID},
		bson.M{"$addToSet": bson.M{"community.banList": userID}},
	).Return(nil)

	mockCommunityDB.On("UpdateOne", mock.Anything,
		bson.M{"_id": communityObjectID},
		bson.M{"$inc": bson.M{"community.membersCount": -1}},
	).Return(nil)

	mockAuditLogDB.On("InsertOne", mock.Anything, mock.Anything).Return(&mocks.InsertOneResultHelper{}, nil)

	u := handlers.User{
		DB:   mockUserDB,
		CDB:  mockCommunityDB,
		ALDB: mockAuditLogDB,
	}

	rr := httptest.NewRecorder()
	http.HandlerFunc(u.BanUserFromCommunityHandler).ServeHTTP(rr, banUserRequest(t, userID, communityID))
	time.Sleep(50 * time.Millisecond) // let logAudit goroutine complete

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "User banned from community successfully")
	mockUserDB.AssertExpectations(t)
	mockCommunityDB.AssertExpectations(t)
}

func TestUser_BanUserFromCommunityHandler_PriorStatusPending_DoesNotDecrement(t *testing.T) {
	userID := "507f1f77bcf86cd799439011"
	communityID := "507f1f77bcf86cd799439012"
	userObjectID, _ := primitive.ObjectIDFromHex(userID)
	communityObjectID, _ := primitive.ObjectIDFromHex(communityID)

	mockUserDB := &mocks.UserDatabase{}
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockAuditLogDB := &mocks.AuditLogDatabase{}

	stubUserFindOne(mockUserDB, userObjectID, &models.User{
		ID: userID,
		Details: models.UserDetails{
			Communities: []models.UserCommunity{
				{ID: "uc1", CommunityID: communityID, Status: "pending"},
			},
		},
	})

	mockUserDB.On("UpdateOne", mock.Anything,
		bson.M{"_id": userObjectID, "user.communities.communityId": communityID},
		bson.M{"$set": bson.M{"user.communities.$.status": "banned"}},
	).Return(&mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil)

	mockCommunityDB.On("UpdateOne", mock.Anything,
		bson.M{"_id": communityObjectID},
		bson.M{"$addToSet": bson.M{"community.banList": userID}},
	).Return(nil)

	mockAuditLogDB.On("InsertOne", mock.Anything, mock.Anything).Return(&mocks.InsertOneResultHelper{}, nil)

	u := handlers.User{
		DB:   mockUserDB,
		CDB:  mockCommunityDB,
		ALDB: mockAuditLogDB,
	}

	rr := httptest.NewRecorder()
	http.HandlerFunc(u.BanUserFromCommunityHandler).ServeHTTP(rr, banUserRequest(t, userID, communityID))
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, http.StatusOK, rr.Code)
	mockCommunityDB.AssertNotCalled(t, "UpdateOne", mock.Anything,
		bson.M{"_id": communityObjectID},
		bson.M{"$inc": bson.M{"community.membersCount": -1}},
	)
	mockUserDB.AssertExpectations(t)
}

func TestUser_BanUserFromCommunityHandler_UserNotMember_ReturnsBadRequest(t *testing.T) {
	userID := "507f1f77bcf86cd799439011"
	communityID := "507f1f77bcf86cd799439012"
	otherCommunityID := "507f1f77bcf86cd7994390aa"
	userObjectID, _ := primitive.ObjectIDFromHex(userID)

	mockUserDB := &mocks.UserDatabase{}
	mockCommunityDB := &mocks.CommunityDatabase{}

	stubUserFindOne(mockUserDB, userObjectID, &models.User{
		ID: userID,
		Details: models.UserDetails{
			Communities: []models.UserCommunity{
				{ID: "uc1", CommunityID: otherCommunityID, Status: "approved"},
			},
		},
	})

	u := handlers.User{
		DB:  mockUserDB,
		CDB: mockCommunityDB,
	}

	rr := httptest.NewRecorder()
	http.HandlerFunc(u.BanUserFromCommunityHandler).ServeHTTP(rr, banUserRequest(t, userID, communityID))

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "user is not a member of this community")
	mockUserDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	mockCommunityDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestUser_BanUserFromCommunityHandler_AlreadyBanned_IsIdempotent(t *testing.T) {
	userID := "507f1f77bcf86cd799439011"
	communityID := "507f1f77bcf86cd799439012"
	userObjectID, _ := primitive.ObjectIDFromHex(userID)

	mockUserDB := &mocks.UserDatabase{}
	mockCommunityDB := &mocks.CommunityDatabase{}

	stubUserFindOne(mockUserDB, userObjectID, &models.User{
		ID: userID,
		Details: models.UserDetails{
			Communities: []models.UserCommunity{
				{ID: "uc1", CommunityID: communityID, Status: "banned"},
			},
		},
	})

	u := handlers.User{
		DB:  mockUserDB,
		CDB: mockCommunityDB,
	}

	rr := httptest.NewRecorder()
	http.HandlerFunc(u.BanUserFromCommunityHandler).ServeHTTP(rr, banUserRequest(t, userID, communityID))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "User already banned from community")
	mockUserDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	mockCommunityDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestUser_BanUserFromCommunityHandler_DecrementFailureIsNonFatal(t *testing.T) {
	userID := "507f1f77bcf86cd799439011"
	communityID := "507f1f77bcf86cd799439012"
	userObjectID, _ := primitive.ObjectIDFromHex(userID)
	communityObjectID, _ := primitive.ObjectIDFromHex(communityID)

	mockUserDB := &mocks.UserDatabase{}
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockAuditLogDB := &mocks.AuditLogDatabase{}

	stubUserFindOne(mockUserDB, userObjectID, &models.User{
		ID: userID,
		Details: models.UserDetails{
			Communities: []models.UserCommunity{
				{ID: "uc1", CommunityID: communityID, Status: "approved"},
			},
		},
	})

	mockUserDB.On("UpdateOne", mock.Anything,
		bson.M{"_id": userObjectID, "user.communities.communityId": communityID},
		bson.M{"$set": bson.M{"user.communities.$.status": "banned"}},
	).Return(&mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil)

	mockCommunityDB.On("UpdateOne", mock.Anything,
		bson.M{"_id": communityObjectID},
		bson.M{"$addToSet": bson.M{"community.banList": userID}},
	).Return(nil)

	mockCommunityDB.On("UpdateOne", mock.Anything,
		bson.M{"_id": communityObjectID},
		bson.M{"$inc": bson.M{"community.membersCount": -1}},
	).Return(errors.New("transient mongo error"))

	mockAuditLogDB.On("InsertOne", mock.Anything, mock.Anything).Return(&mocks.InsertOneResultHelper{}, nil)

	u := handlers.User{
		DB:   mockUserDB,
		CDB:  mockCommunityDB,
		ALDB: mockAuditLogDB,
	}

	rr := httptest.NewRecorder()
	http.HandlerFunc(u.BanUserFromCommunityHandler).ServeHTTP(rr, banUserRequest(t, userID, communityID))
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, http.StatusOK, rr.Code,
		"decrement failure must be non-fatal — the ban itself succeeded")
	assert.Contains(t, rr.Body.String(), "User banned from community successfully")
	mockUserDB.AssertExpectations(t)
	mockCommunityDB.AssertExpectations(t)
}
