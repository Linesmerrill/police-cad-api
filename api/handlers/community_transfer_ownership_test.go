package handlers

import (
	"bytes"
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

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

func TestCommunity_TransferCommunityOwnershipHandler_Success(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	handler := Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"
	currentUserID := "507f1f77bcf86cd799439012"
	newOwnerID := "507f1f77bcf86cd799439013"

	// Convert to ObjectIDs
	cID, _ := primitive.ObjectIDFromHex(communityID)
	newOwnerObjID, _ := primitive.ObjectIDFromHex(newOwnerID)

	// Mock community with Head Admin role
	community := &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			OwnerID: currentUserID,
			Roles: []models.Role{
				{
					ID:      primitive.NewObjectID(),
					Name:    "Head Admin",
					Members: []string{currentUserID},
					Permissions: []models.Permission{
						{
							ID:          primitive.NewObjectID(),
							Name:        "administrator",
							Description: "Head Admin",
							Enabled:     true,
						},
					},
				},
			},
		},
	}

	// Mock new owner user
	newOwner := &models.User{
		ID: newOwnerID,
		Details: models.UserDetails{
			Username: "newowner",
		},
	}

	// Setup mocks
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)
	
	// Create mock SingleResultHelper for new owner
	mockNewOwnerResult := &mocks.SingleResultHelper{}
	mockNewOwnerResult.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		userPtr := args.Get(0).(*models.User)
		*userPtr = *newOwner
	}).Return(nil)
	
	mockUserDB.On("FindOne", mock.Anything, bson.M{"_id": newOwnerObjID}).Return(mockNewOwnerResult)
	
	// Mock the update operation
	mockCommunityDB.On("UpdateOne", mock.Anything, bson.M{"_id": cID}, mock.Anything).Return(nil)

	// Create request
	requestBody := map[string]string{
		"currentUserId": currentUserID,
		"newOwnerId":    newOwnerID,
	}
	requestBodyBytes, _ := json.Marshal(requestBody)

	req, _ := http.NewRequest("POST", "/api/v2/community/"+communityID+"/transfer-ownership", bytes.NewBuffer(requestBodyBytes))
	req.Header.Set("Content-Type", "application/json")

	// Set up router with the route
	router := mux.NewRouter()
	router.HandleFunc("/api/v2/community/{communityId}/transfer-ownership", handler.TransferCommunityOwnershipHandler)

	// Create response recorder
	w := httptest.NewRecorder()

	// Execute request
	router.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code)
	
	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "Community ownership transferred successfully", response["message"])
	assert.Equal(t, newOwnerID, response["newOwnerId"])

	// Verify mocks were called
	mockCommunityDB.AssertExpectations(t)
	mockUserDB.AssertExpectations(t)
}

func TestCommunity_TransferCommunityOwnershipHandler_NotOwner(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}

	handler := Community{
		DB: mockCommunityDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"
	currentUserID := "507f1f77bcf86cd799439012"
	newOwnerID := "507f1f77bcf86cd799439013"
	actualOwnerID := "507f1f77bcf86cd799439014"

	// Convert to ObjectID
	cID, _ := primitive.ObjectIDFromHex(communityID)

	// Mock community with different owner
	community := &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			OwnerID: actualOwnerID,
			Roles:   []models.Role{},
		},
	}

	// Setup mocks
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)

	// Create request
	requestBody := map[string]string{
		"currentUserId": currentUserID,
		"newOwnerId":    newOwnerID,
	}
	requestBodyBytes, _ := json.Marshal(requestBody)

	req, _ := http.NewRequest("POST", "/api/v2/community/"+communityID+"/transfer-ownership", bytes.NewBuffer(requestBodyBytes))
	req.Header.Set("Content-Type", "application/json")

	// Set up router with the route
	router := mux.NewRouter()
	router.HandleFunc("/api/v2/community/{communityId}/transfer-ownership", handler.TransferCommunityOwnershipHandler)

	// Create response recorder
	w := httptest.NewRecorder()

	// Execute request
	router.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Verify mocks were called
	mockCommunityDB.AssertExpectations(t)
}

func TestCommunity_TransferCommunityOwnershipHandler_NoPermission(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}

	handler := Community{
		DB: mockCommunityDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"
	currentUserID := "507f1f77bcf86cd799439012"
	newOwnerID := "507f1f77bcf86cd799439013"

	// Convert to ObjectID
	cID, _ := primitive.ObjectIDFromHex(communityID)

	// Mock community where user is owner but doesn't have Head Admin role
	community := &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			OwnerID: currentUserID,
			Roles:   []models.Role{}, // No Head Admin role
		},
	}

	// Setup mocks
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)

	// Create request
	requestBody := map[string]string{
		"currentUserId": currentUserID,
		"newOwnerId":    newOwnerID,
	}
	requestBodyBytes, _ := json.Marshal(requestBody)

	req, _ := http.NewRequest("POST", "/api/v2/community/"+communityID+"/transfer-ownership", bytes.NewBuffer(requestBodyBytes))
	req.Header.Set("Content-Type", "application/json")

	// Set up router with the route
	router := mux.NewRouter()
	router.HandleFunc("/api/v2/community/{communityId}/transfer-ownership", handler.TransferCommunityOwnershipHandler)

	// Create response recorder
	w := httptest.NewRecorder()

	// Execute request
	router.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Verify mocks were called
	mockCommunityDB.AssertExpectations(t)
}

func TestCommunity_TransferCommunityOwnershipHandler_NewOwnerNotFound(t *testing.T) {
	// Setup
	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	handler := Community{
		DB:  mockCommunityDB,
		UDB: mockUserDB,
	}

	// Test data
	communityID := "507f1f77bcf86cd799439011"
	currentUserID := "507f1f77bcf86cd799439012"
	newOwnerID := "507f1f77bcf86cd799439013"

	// Convert to ObjectIDs
	cID, _ := primitive.ObjectIDFromHex(communityID)
	newOwnerObjID, _ := primitive.ObjectIDFromHex(newOwnerID)

	// Mock community with Head Admin role
	community := &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			OwnerID: currentUserID,
			Roles: []models.Role{
				{
					ID:      primitive.NewObjectID(),
					Name:    "Head Admin",
					Members: []string{currentUserID},
					Permissions: []models.Permission{
						{
							ID:          primitive.NewObjectID(),
							Name:        "administrator",
							Description: "Head Admin",
							Enabled:     true,
						},
					},
				},
			},
		},
	}

	// Setup mocks
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)
	
	// Mock new owner not found
	mockNewOwnerResult := &mocks.SingleResultHelper{}
	mockNewOwnerResult.On("Decode", mock.Anything).Return(mongo.ErrNoDocuments)
	
	mockUserDB.On("FindOne", mock.Anything, bson.M{"_id": newOwnerObjID}).Return(mockNewOwnerResult)

	// Create request
	requestBody := map[string]string{
		"currentUserId": currentUserID,
		"newOwnerId":    newOwnerID,
	}
	requestBodyBytes, _ := json.Marshal(requestBody)

	req, _ := http.NewRequest("POST", "/api/v2/community/"+communityID+"/transfer-ownership", bytes.NewBuffer(requestBodyBytes))
	req.Header.Set("Content-Type", "application/json")

	// Set up router with the route
	router := mux.NewRouter()
	router.HandleFunc("/api/v2/community/{communityId}/transfer-ownership", handler.TransferCommunityOwnershipHandler)

	// Create response recorder
	w := httptest.NewRecorder()

	// Execute request
	router.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Verify mocks were called
	mockCommunityDB.AssertExpectations(t)
	mockUserDB.AssertExpectations(t)
}
