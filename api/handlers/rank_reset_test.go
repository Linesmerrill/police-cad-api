package handlers

import (
	"bytes"
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

// buildRankAssignCommunity returns a community with a single department holding two
// ranks and one member currently sitting at the lower rank with a custom requirement
// already marked as met.
func buildRankAssignCommunity(ownerID, memberUserID string, lowRankID, highRankID primitive.ObjectID) *models.Community {
	cID := primitive.NewObjectID()
	deptID := primitive.NewObjectID()
	return &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			OwnerID: ownerID,
			Departments: []models.Department{
			{
				ID:   deptID,
				Name: "Police",
				Ranks: []models.Rank{
					{ID: lowRankID, Name: "Officer", DisplayOrder: 2},
					{ID: highRankID, Name: "Sergeant", DisplayOrder: 1},
				},
				Members: []models.MemberStatus{
					{
						UserID:                memberUserID,
						Status:                "approved",
						RankID:                lowRankID.Hex(),
						CustomRequirementsMet: []string{"req-already-met"},
					},
				},
			},
			},
		},
	}
}

// captureAssignRankUpdate exercises AssignMemberRankHandler and returns the $set
// document persisted to Mongo so callers can assert on customRequirementsMet.
func captureAssignRankUpdate(t *testing.T, community *models.Community, ownerID, newRankID string) bson.M {
	t.Helper()

	mockCommunityDB := &mocks.CommunityDatabase{}
	mockUserDB := &mocks.UserDatabase{}
	mockAuditDB := &mocks.AuditLogDatabase{}

	handler := Community{
		DB:   mockCommunityDB,
		UDB:  mockUserDB,
		ALDB: mockAuditDB,
	}

	deptID := community.Details.Departments[0].ID.Hex()
	memberUserID := community.Details.Departments[0].Members[0].UserID

	mockCommunityDB.On("FindOne", mock.Anything, bson.M{"_id": community.ID}).Return(community, nil)

	var captured bson.M
	mockCommunityDB.On("UpdateOne", mock.Anything, bson.M{"_id": community.ID}, mock.Anything).
		Run(func(args mock.Arguments) {
			update := args.Get(2).(bson.M)
			if set, ok := update["$set"].(bson.M); ok {
				captured = set
			}
		}).Return(nil)

	// resolveActorName looks up the actor username; return an empty result.
	actorResult := &mocks.SingleResultHelper{}
	actorResult.On("Decode", mock.Anything).Return(nil)
	mockUserDB.On("FindOne", mock.Anything, mock.Anything).Return(actorResult)

	// logAudit fires asynchronously; allow the insert to succeed if it runs.
	mockAuditDB.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	body, _ := json.Marshal(map[string]string{"rankId": newRankID})
	url := fmt.Sprintf("/api/v1/community/%s/departments/%s/members/%s/rank?userId=%s",
		community.ID.Hex(), deptID, memberUserID, ownerID)
	req := httptest.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{
		"communityId":  community.ID.Hex(),
		"departmentId": deptID,
		"userId":       memberUserID,
	})
	rec := httptest.NewRecorder()

	handler.AssignMemberRankHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	return captured
}

// When a member's rank actually changes, met custom requirements must be reset so
// progress toward the new rank starts fresh.
func TestAssignMemberRankHandler_ResetsCustomRequirementsOnRankChange(t *testing.T) {
	ownerID := primitive.NewObjectID().Hex()
	memberUserID := primitive.NewObjectID().Hex()
	lowRankID := primitive.NewObjectID()
	highRankID := primitive.NewObjectID()

	community := buildRankAssignCommunity(ownerID, memberUserID, lowRankID, highRankID)

	set := captureAssignRankUpdate(t, community, ownerID, highRankID.Hex())

	var customKey string
	for k := range set {
		if len(k) > len("customRequirementsMet") && k[len(k)-len("customRequirementsMet"):] == "customRequirementsMet" {
			customKey = k
		}
	}
	assert.NotEmpty(t, customKey, "expected customRequirementsMet to be reset on rank change")
	assert.Equal(t, []string{}, set[customKey], "customRequirementsMet should be reset to an empty slice")
}

// Re-assigning the same rank should leave met custom requirements untouched.
func TestAssignMemberRankHandler_PreservesCustomRequirementsOnSameRank(t *testing.T) {
	ownerID := primitive.NewObjectID().Hex()
	memberUserID := primitive.NewObjectID().Hex()
	lowRankID := primitive.NewObjectID()
	highRankID := primitive.NewObjectID()

	community := buildRankAssignCommunity(ownerID, memberUserID, lowRankID, highRankID)

	// Assign the rank the member already holds.
	set := captureAssignRankUpdate(t, community, ownerID, lowRankID.Hex())

	for k := range set {
		if len(k) > len("customRequirementsMet") && k[len(k)-len("customRequirementsMet"):] == "customRequirementsMet" {
			t.Fatalf("did not expect customRequirementsMet reset when re-assigning the same rank, got key %q", k)
		}
	}
}
