package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

func TestCommunity_FetchCommunityMembersByRoleIDHandlerV2_Success(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	handler := Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"
	roleID := "507f1f77bcf86cd799439012"
	userID1 := "507f1f77bcf86cd799439013"
	userID2 := "507f1f77bcf86cd799439014"

	// Convert to ObjectIDs
	cID, _ := primitive.ObjectIDFromHex(communityID)
	rID, _ := primitive.ObjectIDFromHex(roleID)

	// Mock community with role
	community := &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			Roles: []models.Role{
				{
					ID:      rID,
					Name:    "Test Role",
					Members: []string{userID1, userID2},
				},
			},
		},
	}

	// Setup mocks
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)

	// Create a test cursor with both users for the batch Find call
	userDocs := []interface{}{
		bson.M{
			"_id": userID1,
			"user": bson.M{
				"username":       "user1",
				"profilePicture": "pic1.jpg",
				"callSign":       "CS1",
				"subscription":   bson.M{"active": true, "plan": "premium"},
			},
		},
		bson.M{
			"_id": userID2,
			"user": bson.M{
				"username":       "user2",
				"profilePicture": "pic2.jpg",
				"callSign":       "CS2",
				"subscription":   bson.M{"active": true, "plan": "basic"},
			},
		},
	}
	testCursor, _ := databases.NewMongoCursorFromDocuments(userDocs)
	mockUserDB.On("Find", mock.Anything, mock.Anything).Return(testCursor, nil)

	// Create request
	req, err := http.NewRequest("GET", "/api/v2/community/"+communityID+"/roles/"+roleID+"/members?page=1&limit=10", nil)
	assert.NoError(t, err)

	// Set URL variables
	req = mux.SetURLVars(req, map[string]string{
		"communityId": communityID,
		"roleId":      roleID,
	})

	// Create response recorder
	w := httptest.NewRecorder()

	// Call handler
	handler.FetchCommunityMembersByRoleIDHandlerV2(w, req)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)

	// Check response structure
	assert.Contains(t, response, "members")
	assert.Contains(t, response, "pagination")

	// Check pagination info
	pagination := response["pagination"].(map[string]interface{})
	assert.Equal(t, float64(1), pagination["currentPage"])
	assert.Equal(t, float64(1), pagination["totalPages"])
	assert.Equal(t, float64(2), pagination["totalCount"])
	assert.Equal(t, false, pagination["hasNextPage"])
	assert.Equal(t, false, pagination["hasPrevPage"])

	// Check members
	members := response["members"].([]interface{})
	assert.Len(t, members, 2)

	// Check first member
	member1 := members[0].(map[string]interface{})
	assert.Equal(t, userID1, member1["id"])
	assert.Equal(t, "user1", member1["username"])
	assert.Equal(t, "pic1.jpg", member1["profilePicture"])
	assert.Equal(t, "CS1", member1["callSign"])
	assert.Equal(t, true, member1["isVerified"])

	// Check second member
	member2 := members[1].(map[string]interface{})
	assert.Equal(t, userID2, member2["id"])
	assert.Equal(t, "user2", member2["username"])
	assert.Equal(t, "pic2.jpg", member2["profilePicture"])
	assert.Equal(t, "CS2", member2["callSign"])
	assert.Equal(t, false, member2["isVerified"]) // basic plan, not verified

	// Verify all mocks were called
	mockCommunityDB.AssertExpectations(t)
	mockUserDB.AssertExpectations(t)
}

func TestCommunity_FetchCommunityMembersByRoleIDHandlerV2_Pagination(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	handler := Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"
	roleID := "507f1f77bcf86cd799439012"

	// Convert to ObjectIDs
	cID, _ := primitive.ObjectIDFromHex(communityID)
	rID, _ := primitive.ObjectIDFromHex(roleID)

	// Mock community with many members
	var memberIDs []string
	for i := 0; i < 25; i++ {
		memberIDs = append(memberIDs, primitive.NewObjectID().Hex())
	}

	community := &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			Roles: []models.Role{
				{
					ID:      rID,
					Name:    "Test Role",
					Members: memberIDs,
				},
			},
		},
	}

	// Setup mocks
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)

	// Create test cursor with user documents for paginated page (page 2, limit 10 = members 10-19)
	startIndex := 10
	endIndex := 20
	var userDocs []interface{}
	for i := startIndex; i < endIndex && i < len(memberIDs); i++ {
		userDocs = append(userDocs, bson.M{
			"_id": memberIDs[i],
			"user": bson.M{
				"username":       "user" + memberIDs[i][:8],
				"profilePicture": "pic" + memberIDs[i][:8] + ".jpg",
				"callSign":       "CS" + memberIDs[i][:8],
				"subscription":   bson.M{"active": true, "plan": "basic"},
			},
		})
	}
	testCursor, _ := databases.NewMongoCursorFromDocuments(userDocs)
	mockUserDB.On("Find", mock.Anything, mock.Anything).Return(testCursor, nil)

	// Create request with page 2, limit 10
	req, err := http.NewRequest("GET", "/api/v2/community/"+communityID+"/roles/"+roleID+"/members?page=2&limit=10", nil)
	assert.NoError(t, err)

	// Set URL variables
	req = mux.SetURLVars(req, map[string]string{
		"communityId": communityID,
		"roleId":      roleID,
	})

	// Create response recorder
	w := httptest.NewRecorder()

	// Call handler
	handler.FetchCommunityMembersByRoleIDHandlerV2(w, req)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)

	// Check pagination info
	pagination := response["pagination"].(map[string]interface{})
	assert.Equal(t, float64(2), pagination["currentPage"])
	assert.Equal(t, float64(3), pagination["totalPages"]) // 25 members / 10 per page = 3 pages
	assert.Equal(t, float64(25), pagination["totalCount"])
	assert.Equal(t, true, pagination["hasNextPage"])
	assert.Equal(t, true, pagination["hasPrevPage"])

	// Verify mock was called
	mockCommunityDB.AssertExpectations(t)
}

func TestCommunity_FetchCommunityMembersByRoleIDHandlerV2_InvalidObjectID(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	handler := Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	// Test data with invalid ObjectID
	communityID := "invalid-id"
	roleID := "507f1f77bcf86cd799439012"

	// Create request
	req, err := http.NewRequest("GET", "/api/v2/community/"+communityID+"/roles/"+roleID+"/members?page=1&limit=10", nil)
	assert.NoError(t, err)

	// Set URL variables
	req = mux.SetURLVars(req, map[string]string{
		"communityId": communityID,
		"roleId":      roleID,
	})

	// Create response recorder
	w := httptest.NewRecorder()

	// Call handler
	handler.FetchCommunityMembersByRoleIDHandlerV2(w, req)

	// Assertions
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Verify no mocks were called
	mockCommunityDB.AssertNotCalled(t, "FindOne")
	mockUserDB.AssertNotCalled(t, "FindOne")
}

func TestCommunity_FetchCommunityMembersByRoleIDHandlerV2_CommunityNotFound(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	handler := Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"
	roleID := "507f1f77bcf86cd799439012"

	// Convert to ObjectIDs
	cID, _ := primitive.ObjectIDFromHex(communityID)

	// Setup mocks - community not found
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(nil, mongo.ErrNoDocuments)

	// Create request
	req, err := http.NewRequest("GET", "/api/v2/community/"+communityID+"/roles/"+roleID+"/members?page=1&limit=10", nil)
	assert.NoError(t, err)

	// Set URL variables
	req = mux.SetURLVars(req, map[string]string{
		"communityId": communityID,
		"roleId":      roleID,
	})

	// Create response recorder
	w := httptest.NewRecorder()

	// Call handler
	handler.FetchCommunityMembersByRoleIDHandlerV2(w, req)

	// Assertions
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Verify mock was called
	mockCommunityDB.AssertExpectations(t)
}

func TestCommunity_FetchCommunityMembersByRoleIDHandlerV2_RoleNotFound(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	handler := Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"
	roleID := "507f1f77bcf86cd799439012"

	// Convert to ObjectIDs
	cID, _ := primitive.ObjectIDFromHex(communityID)

	// Mock community without the specified role
	community := &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			Roles: []models.Role{
				{
					ID:      primitive.NewObjectID(), // Different role ID
					Name:    "Different Role",
					Members: []string{},
				},
			},
		},
	}

	// Setup mocks
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)

	// Create request
	req, err := http.NewRequest("GET", "/api/v2/community/"+communityID+"/roles/"+roleID+"/members?page=1&limit=10", nil)
	assert.NoError(t, err)

	// Set URL variables
	req = mux.SetURLVars(req, map[string]string{
		"communityId": communityID,
		"roleId":      roleID,
	})

	// Create response recorder
	w := httptest.NewRecorder()

	// Call handler
	handler.FetchCommunityMembersByRoleIDHandlerV2(w, req)

	// Assertions
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Verify mock was called
	mockCommunityDB.AssertExpectations(t)
}
