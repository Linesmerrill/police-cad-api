package handlers

import (
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

func TestCommunity_FetchCommunityMembersHandlerV2_Success(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	handler := Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"
	userID1 := "507f1f77bcf86cd799439012"
	userID2 := "507f1f77bcf86cd799439013"

	// Convert to ObjectIDs
	cID, _ := primitive.ObjectIDFromHex(communityID)
	user1ObjID, _ := primitive.ObjectIDFromHex(userID1)
	user2ObjID, _ := primitive.ObjectIDFromHex(userID2)

	// Mock community with members
	community := &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			Members: map[string]models.MemberDetail{
				userID1: {DepartmentID: "dept1", TenCodeID: "code1", IsOnline: true},
				userID2: {DepartmentID: "dept2", TenCodeID: "code2", IsOnline: false},
			},
		},
	}

	// Mock users
	user1 := &models.User{
		ID: userID1,
		Details: models.UserDetails{
			Username:       "user1",
			ProfilePicture: "pic1.jpg",
			CallSign:       "CS1",
			Subscription: models.Subscription{
				Active: true,
				Plan:   "premium",
			},
		},
	}

	user2 := &models.User{
		ID: userID2,
		Details: models.UserDetails{
			Username:       "user2",
			ProfilePicture: "pic2.jpg",
			CallSign:       "CS2",
			Subscription: models.Subscription{
				Active: true,
				Plan:   "basic",
			},
		},
	}

	// Setup mocks
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)
	
	// Create mock SingleResultHelper for user1
	mockUser1Result := &mocks.SingleResultHelper{}
	mockUser1Result.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		userPtr := args.Get(0).(*models.User)
		*userPtr = *user1
	}).Return(nil)
	
	mockUserDB.On("FindOne", mock.Anything, bson.M{"_id": user1ObjID}).Return(mockUser1Result)
	
	// Create mock SingleResultHelper for user2
	mockUser2Result := &mocks.SingleResultHelper{}
	mockUser2Result.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		userPtr := args.Get(0).(*models.User)
		*userPtr = *user2
	}).Return(nil)
	
	mockUserDB.On("FindOne", mock.Anything, bson.M{"_id": user2ObjID}).Return(mockUser2Result)

	// Create request
	req, _ := http.NewRequest("GET", "/api/v2/community/"+communityID+"/members?page=1&limit=10", nil)

	// Set up router with the route
	router := mux.NewRouter()
	router.HandleFunc("/api/v2/community/{communityId}/members", handler.FetchCommunityMembersHandlerV2)

	// Create response recorder
	w := httptest.NewRecorder()

	// Execute request
	router.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code)
	
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	
	// Check members
	members, ok := response["members"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, members, 2)
	
	// Check pagination
	pagination, ok := response["pagination"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, float64(1), pagination["currentPage"])
	assert.Equal(t, float64(1), pagination["totalPages"])
	assert.Equal(t, float64(2), pagination["totalCount"])
	assert.Equal(t, false, pagination["hasNextPage"])
	assert.Equal(t, false, pagination["hasPrevPage"])

	// Verify mocks were called
	mockCommunityDB.AssertExpectations(t)
	mockUserDB.AssertExpectations(t)
}

func TestCommunity_FetchCommunityMembersHandlerV2_Pagination(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	handler := Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"
	userID1 := "507f1f77bcf86cd799439012"
	userID2 := "507f1f77bcf86cd799439013"

	// Convert to ObjectIDs
	cID, _ := primitive.ObjectIDFromHex(communityID)
	user1ObjID, _ := primitive.ObjectIDFromHex(userID1)

	// Mock community with members
	community := &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			Members: map[string]models.MemberDetail{
				userID1: {DepartmentID: "dept1", TenCodeID: "code1", IsOnline: true},
				userID2: {DepartmentID: "dept2", TenCodeID: "code2", IsOnline: false},
			},
		},
	}

	// Mock user for first page
	user1 := &models.User{
		ID: userID1,
		Details: models.UserDetails{
			Username:       "user1",
			ProfilePicture: "pic1.jpg",
			CallSign:       "CS1",
			Subscription: models.Subscription{
				Active: true,
				Plan:   "premium",
			},
		},
	}

	// Setup mocks
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)
	
	// Create mock SingleResultHelper for user1
	mockUser1Result := &mocks.SingleResultHelper{}
	mockUser1Result.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		userPtr := args.Get(0).(*models.User)
		*userPtr = *user1
	}).Return(nil)
	
	mockUserDB.On("FindOne", mock.Anything, bson.M{"_id": user1ObjID}).Return(mockUser1Result)

	// Create request with limit 1
	req, _ := http.NewRequest("GET", "/api/v2/community/"+communityID+"/members?page=1&limit=1", nil)

	// Set up router with the route
	router := mux.NewRouter()
	router.HandleFunc("/api/v2/community/{communityId}/members", handler.FetchCommunityMembersHandlerV2)

	// Create response recorder
	w := httptest.NewRecorder()

	// Execute request
	router.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code)
	
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	
	// Check members (should only have 1 due to limit)
	members, ok := response["members"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, members, 1)
	
	// Check pagination
	pagination, ok := response["pagination"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, float64(1), pagination["currentPage"])
	assert.Equal(t, float64(2), pagination["totalPages"])
	assert.Equal(t, float64(2), pagination["totalCount"])
	assert.Equal(t, true, pagination["hasNextPage"])
	assert.Equal(t, false, pagination["hasPrevPage"])

	// Verify mocks were called
	mockCommunityDB.AssertExpectations(t)
	mockUserDB.AssertExpectations(t)
}

func TestCommunity_FetchCommunityMembersHandlerV2_InvalidObjectID(t *testing.T) {
	// Setup
	handler := Community{}

	// Create request with invalid community ID
	req, _ := http.NewRequest("GET", "/api/v2/community/invalid-id/members", nil)

	// Set up router with the route
	router := mux.NewRouter()
	router.HandleFunc("/api/v2/community/{communityId}/members", handler.FetchCommunityMembersHandlerV2)

	// Create response recorder
	w := httptest.NewRecorder()

	// Execute request
	router.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCommunity_FetchCommunityMembersHandlerV2_CommunityNotFound(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}

	handler := Community{
		DB: mockCommunityDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"

	// Convert to ObjectID
	cID, _ := primitive.ObjectIDFromHex(communityID)

	// Setup mocks - community not found
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(nil, fmt.Errorf("community not found"))

	// Create request
	req, _ := http.NewRequest("GET", "/api/v2/community/"+communityID+"/members", nil)

	// Set up router with the route
	router := mux.NewRouter()
	router.HandleFunc("/api/v2/community/{communityId}/members", handler.FetchCommunityMembersHandlerV2)

	// Create response recorder
	w := httptest.NewRecorder()

	// Execute request
	router.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Verify mocks were called
	mockCommunityDB.AssertExpectations(t)
}

func TestCommunity_FetchCommunityMembersHandlerV2_EmptyMembers(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}

	handler := Community{
		DB: mockCommunityDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"

	// Convert to ObjectID
	cID, _ := primitive.ObjectIDFromHex(communityID)

	// Mock community with no members
	community := &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			Members: map[string]models.MemberDetail{},
		},
	}

	// Setup mocks
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)

	// Create request
	req, _ := http.NewRequest("GET", "/api/v2/community/"+communityID+"/members?page=1&limit=10", nil)

	// Set up router with the route
	router := mux.NewRouter()
	router.HandleFunc("/api/v2/community/{communityId}/members", handler.FetchCommunityMembersHandlerV2)

	// Create response recorder
	w := httptest.NewRecorder()

	// Execute request
	router.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code)
	
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	
	// Check members (should be empty)
	members, ok := response["members"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, members, 0)
	
	// Check pagination
	pagination, ok := response["pagination"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, float64(1), pagination["currentPage"])
	assert.Equal(t, float64(0), pagination["totalPages"])
	assert.Equal(t, float64(0), pagination["totalCount"])
	assert.Equal(t, false, pagination["hasNextPage"])
	assert.Equal(t, false, pagination["hasPrevPage"])

	// Verify mocks were called
	mockCommunityDB.AssertExpectations(t)
}
