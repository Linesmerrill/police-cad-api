package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
func buildRankAssignCommunity(ownerID, memberUserID string, lowRankID, highRankID primitive.ObjectID, resetStats bool) *models.Community {
	cID := primitive.NewObjectID()
	deptID := primitive.NewObjectID()
	return &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			OwnerID:      ownerID,
			RankSettings: models.RankSettings{ResetStatsOnPromotion: resetStats},
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

// rankStatsSince gates the since-promotion metric window on the community flag and the
// member's recorded rank-assignment time.
func TestRankStatsSince(t *testing.T) {
	assignedAt := primitive.NewDateTimeFromTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	withReset := func(on bool) *models.Community {
		return &models.Community{Details: models.CommunityDetails{
			RankSettings: models.RankSettings{ResetStatsOnPromotion: on},
		}}
	}

	t.Run("flag off returns nil (all-time)", func(t *testing.T) {
		got := rankStatsSince(withReset(false), models.MemberStatus{RankAssignedAt: assignedAt})
		assert.Nil(t, got)
	})

	t.Run("flag on but no assignment time returns nil (all-time fallback)", func(t *testing.T) {
		got := rankStatsSince(withReset(true), models.MemberStatus{})
		assert.Nil(t, got)
	})

	t.Run("flag on with assignment time returns that time", func(t *testing.T) {
		got := rankStatsSince(withReset(true), models.MemberStatus{RankAssignedAt: assignedAt})
		if assert.NotNil(t, got) {
			assert.True(t, got.Equal(assignedAt.Time()), "expected since == RankAssignedAt")
		}
	})

	t.Run("nil community returns nil", func(t *testing.T) {
		assert.Nil(t, rankStatsSince(nil, models.MemberStatus{RankAssignedAt: assignedAt}))
	})
}

// customRequirementsMetKey returns the $set key ending in customRequirementsMet, if any.
func customRequirementsMetKey(set bson.M) string {
	const suffix = "customRequirementsMet"
	for k := range set {
		if len(k) > len(suffix) && k[len(k)-len(suffix):] == suffix {
			return k
		}
	}
	return ""
}

// With reset-stats mode ON, a rank change must clear met custom requirements so
// progress toward the new rank starts fresh.
func TestAssignMemberRankHandler_ResetMode_ClearsCustomRequirementsOnRankChange(t *testing.T) {
	ownerID := primitive.NewObjectID().Hex()
	memberUserID := primitive.NewObjectID().Hex()
	lowRankID := primitive.NewObjectID()
	highRankID := primitive.NewObjectID()

	community := buildRankAssignCommunity(ownerID, memberUserID, lowRankID, highRankID, true)

	set := captureAssignRankUpdate(t, community, ownerID, highRankID.Hex())

	key := customRequirementsMetKey(set)
	assert.NotEmpty(t, key, "expected customRequirementsMet to be reset on rank change in reset mode")
	assert.Equal(t, []string{}, set[key], "customRequirementsMet should be reset to an empty slice")
}

// With reset-stats mode OFF (default / all-time), a rank change must NOT touch met
// custom requirements — you keep what you earned.
func TestAssignMemberRankHandler_AllTimeMode_PreservesCustomRequirementsOnRankChange(t *testing.T) {
	ownerID := primitive.NewObjectID().Hex()
	memberUserID := primitive.NewObjectID().Hex()
	lowRankID := primitive.NewObjectID()
	highRankID := primitive.NewObjectID()

	community := buildRankAssignCommunity(ownerID, memberUserID, lowRankID, highRankID, false)

	set := captureAssignRankUpdate(t, community, ownerID, highRankID.Hex())

	if key := customRequirementsMetKey(set); key != "" {
		t.Fatalf("did not expect customRequirementsMet reset in all-time mode, got key %q", key)
	}
}

// Re-assigning the same rank should leave met custom requirements untouched even in
// reset-stats mode.
func TestAssignMemberRankHandler_ResetMode_PreservesCustomRequirementsOnSameRank(t *testing.T) {
	ownerID := primitive.NewObjectID().Hex()
	memberUserID := primitive.NewObjectID().Hex()
	lowRankID := primitive.NewObjectID()
	highRankID := primitive.NewObjectID()

	community := buildRankAssignCommunity(ownerID, memberUserID, lowRankID, highRankID, true)

	// Assign the rank the member already holds.
	set := captureAssignRankUpdate(t, community, ownerID, lowRankID.Hex())

	if key := customRequirementsMetKey(set); key != "" {
		t.Fatalf("did not expect customRequirementsMet reset when re-assigning the same rank, got key %q", key)
	}
}
