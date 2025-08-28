package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/models"
)

func TestCommunity_FetchBannedUsersHandlerV2_InvalidObjectID(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	handler := handlers.Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	// Test data with invalid ObjectID
	communityID := "invalid-id"

	// Create request
	req, err := http.NewRequest("GET", "/api/v2/community/"+communityID+"/banned-users?page=1&limit=10", nil)
	assert.NoError(t, err)

	// Set URL variables
	req = mux.SetURLVars(req, map[string]string{
		"communityId": communityID,
	})

	// Create response recorder
	w := httptest.NewRecorder()

	// Call handler
	handler.FetchBannedUsersHandlerV2(w, req)

	// Assertions
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "failed to get objectID from Hex")
}

func TestCommunity_FetchBannedUsersHandlerV2_CommunityNotFound(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	handler := handlers.Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"

	// Convert to ObjectID
	cID, _ := primitive.ObjectIDFromHex(communityID)

	// Setup mocks - return error for community not found
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(nil, mongo.ErrNoDocuments)

	// Create request
	req, err := http.NewRequest("GET", "/api/v2/community/"+communityID+"/banned-users?page=1&limit=10", nil)
	assert.NoError(t, err)

	// Set URL variables
	req = mux.SetURLVars(req, map[string]string{
		"communityId": communityID,
	})

	// Create response recorder
	w := httptest.NewRecorder()

	// Call handler
	handler.FetchBannedUsersHandlerV2(w, req)

	// Assertions
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "failed to get community by ID")

	// Verify mock was called
	mockCommunityDB.AssertExpectations(t)
}

func TestCommunity_FetchBannedUsersHandlerV2_NoBannedUsers(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	handler := handlers.Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"

	// Convert to ObjectID
	cID, _ := primitive.ObjectIDFromHex(communityID)

	// Mock community with no banned users
	community := &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			BanList: []string{},
		},
	}

	// Setup mocks
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)

	// Create request
	req, err := http.NewRequest("GET", "/api/v2/community/"+communityID+"/banned-users?page=1&limit=10", nil)
	assert.NoError(t, err)

	// Set URL variables
	req = mux.SetURLVars(req, map[string]string{
		"communityId": communityID,
	})

	// Create response recorder
	w := httptest.NewRecorder()

	// Call handler
	handler.FetchBannedUsersHandlerV2(w, req)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)

	// Check response structure
	assert.Contains(t, response, "bannedUsers")
	assert.Contains(t, response, "pagination")

	// Check pagination info
	pagination := response["pagination"].(map[string]interface{})
	assert.Equal(t, float64(1), pagination["currentPage"])
	assert.Equal(t, float64(0), pagination["totalPages"])
	assert.Equal(t, float64(0), pagination["totalCount"])
	assert.Equal(t, false, pagination["hasNextPage"])
	assert.Equal(t, false, pagination["hasPrevPage"])

	// Check banned users
	bannedUsers := response["bannedUsers"].([]interface{})
	assert.Len(t, bannedUsers, 0)

	// Verify mock was called
	mockCommunityDB.AssertExpectations(t)
}

func TestCommunity_FetchBannedUsersHandlerV2_PageBeyondData(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	handler := handlers.Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"

	// Convert to ObjectID
	cID, _ := primitive.ObjectIDFromHex(communityID)

	// Mock community with 5 banned users
	community := &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			BanList: []string{
				"507f1f77bcf86cd799439013",
				"507f1f77bcf86cd799439014",
				"507f1f77bcf86cd799439015",
				"507f1f77bcf86cd799439016",
				"507f1f77bcf86cd799439017",
			},
		},
	}

	// Setup mocks
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)

	// Create request with page 3, limit 10 (beyond available data)
	req, err := http.NewRequest("GET", "/api/v2/community/"+communityID+"/banned-users?page=3&limit=10", nil)
	assert.NoError(t, err)

	// Set URL variables
	req = mux.SetURLVars(req, map[string]string{
		"communityId": communityID,
	})

	// Create response recorder
	w := httptest.NewRecorder()

	// Call handler
	handler.FetchBannedUsersHandlerV2(w, req)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)

	// Check response structure
	assert.Contains(t, response, "bannedUsers")
	assert.Contains(t, response, "pagination")

	// Check pagination info
	pagination := response["pagination"].(map[string]interface{})
	assert.Equal(t, float64(3), pagination["currentPage"])
	assert.Equal(t, float64(1), pagination["totalPages"]) // 5 banned users / 10 per page = 1 page
	assert.Equal(t, float64(5), pagination["totalCount"])
	assert.Equal(t, false, pagination["hasNextPage"])
	assert.Equal(t, true, pagination["hasPrevPage"])

	// Check banned users (should be empty for page beyond data)
	bannedUsers := response["bannedUsers"].([]interface{})
	assert.Len(t, bannedUsers, 0)

	// Verify mock was called
	mockCommunityDB.AssertExpectations(t)
}
