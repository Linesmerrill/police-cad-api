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

// transferOwnershipRoles runs a transfer against a community with the given
// roles and returns the response recorder plus the roles array written to the
// DB (nil when the update did not touch community.roles).
func transferOwnershipRoles(t *testing.T, roles []models.Role, currentUserID, newOwnerID string) (*httptest.ResponseRecorder, []models.Role) {
	t.Helper()

	communityID := "507f1f77bcf86cd799439011"
	cID, _ := primitive.ObjectIDFromHex(communityID)
	newOwnerObjID, _ := primitive.ObjectIDFromHex(newOwnerID)

	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}
	handler := Community{DB: mockCommunityDB, UDB: mockUserDB}

	community := &models.Community{
		ID:      cID,
		Details: models.CommunityDetails{OwnerID: currentUserID, Roles: roles},
	}
	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil)

	mockNewOwnerResult := &mocks.SingleResultHelper{}
	mockNewOwnerResult.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		userPtr := args.Get(0).(*models.User)
		*userPtr = models.User{ID: newOwnerID, Details: models.UserDetails{Username: "newowner"}}
	}).Return(nil)
	mockUserDB.On("FindOne", mock.Anything, bson.M{"_id": newOwnerObjID}).Return(mockNewOwnerResult)

	var writtenRoles []models.Role
	mockCommunityDB.On("UpdateOne", mock.Anything, bson.M{"_id": cID}, mock.MatchedBy(func(update bson.M) bool {
		if set, ok := update["$set"].(bson.M); ok {
			if r, ok := set["community.roles"].([]models.Role); ok {
				writtenRoles = r
			}
		}
		return true
	})).Return(nil)

	requestBodyBytes, _ := json.Marshal(map[string]string{
		"currentUserId": currentUserID,
		"newOwnerId":    newOwnerID,
	})
	req, _ := http.NewRequest("POST", "/api/v2/community/"+communityID+"/transfer-ownership", bytes.NewBuffer(requestBodyBytes))
	req.Header.Set("Content-Type", "application/json")

	router := mux.NewRouter()
	router.HandleFunc("/api/v2/community/{communityId}/transfer-ownership", handler.TransferCommunityOwnershipHandler)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w, writtenRoles
}

func headAdminRoleNamed(name string, members ...string) models.Role {
	return models.Role{
		ID:      primitive.NewObjectID(),
		Name:    name,
		Members: members,
		Permissions: []models.Permission{
			{ID: primitive.NewObjectID(), Name: "administrator", Description: "Head Admin", Enabled: true},
		},
	}
}

// Regression: owners were required to ALSO sit in a role literally named
// "Head Admin" with administrator enabled. Owners who had renamed that role, or
// whose community predates the roles system, got a 403 transferring their own
// community. Being the owner is authorization enough.
func TestCommunity_TransferCommunityOwnershipHandler_OwnerWithoutAdminRoleCanTransfer(t *testing.T) {
	currentUserID := "507f1f77bcf86cd799439012"
	newOwnerID := "507f1f77bcf86cd799439013"

	w, writtenRoles := transferOwnershipRoles(t, []models.Role{}, currentUserID, newOwnerID)

	assert.Equal(t, http.StatusOK, w.Code, "the owner must be able to transfer their own community")

	// A community with no admin role is the legacy shape the backfill repairs;
	// stamp the canonical role so the new owner is not locked out.
	assert.Len(t, writtenRoles, 1)
	assert.True(t, models.IsHeadAdminRole(writtenRoles[0]))
	assert.Equal(t, []string{newOwnerID}, writtenRoles[0].Members)
}

func TestCommunity_TransferCommunityOwnershipHandler_RenamedAdminRoleIsFound(t *testing.T) {
	currentUserID := "507f1f77bcf86cd799439012"
	newOwnerID := "507f1f77bcf86cd799439013"
	otherMember := "507f1f77bcf86cd799439015"

	// Owners can rename roles, so the admin role must be matched on its
	// permission structure rather than the string "Head Admin".
	roles := []models.Role{headAdminRoleNamed("Server Owner", currentUserID, otherMember)}
	w, writtenRoles := transferOwnershipRoles(t, roles, currentUserID, newOwnerID)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, writtenRoles, 1, "the renamed role must be reused, not duplicated")
	assert.Equal(t, "Server Owner", writtenRoles[0].Name, "renaming must be preserved")
	assert.NotContains(t, writtenRoles[0].Members, currentUserID, "outgoing owner loses admin")
	assert.Contains(t, writtenRoles[0].Members, newOwnerID, "incoming owner gains admin")
	assert.Contains(t, writtenRoles[0].Members, otherMember, "unrelated members are untouched")
}

func TestCommunity_TransferCommunityOwnershipHandler_NewOwnerAlreadyAdminIsNotDuplicated(t *testing.T) {
	currentUserID := "507f1f77bcf86cd799439012"
	newOwnerID := "507f1f77bcf86cd799439013"

	roles := []models.Role{headAdminRoleNamed("Head Admin", currentUserID, newOwnerID)}
	w, writtenRoles := transferOwnershipRoles(t, roles, currentUserID, newOwnerID)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{newOwnerID}, writtenRoles[0].Members)
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
