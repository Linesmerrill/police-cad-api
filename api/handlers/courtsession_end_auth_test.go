package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// authorizeEndSession decides who may force-end a court session: the owning judge
// always, otherwise the community owner or an "administrator" of the session's
// community. These tests exercise that gate directly (it only depends on CommDB,
// which has a generated mock).

const (
	endAuthCommunityID = "507f1f77bcf86cd799439011"
	endAuthJudgeID     = "judge-user-1"
	endAuthOwnerID     = "owner-user-1"
	endAuthAdminID     = "admin-user-1"
	endAuthRandomID    = "random-user-1"
)

func endAuthSession() *models.CourtSession {
	return &models.CourtSession{
		ID: primitive.NewObjectID(),
		Details: models.CourtSessionDetails{
			JudgeID:     endAuthJudgeID,
			CommunityID: endAuthCommunityID,
			Status:      "in_progress",
		},
	}
}

func endAuthCommunity() *models.Community {
	return &models.Community{
		Details: models.CommunityDetails{
			OwnerID: endAuthOwnerID,
			Roles: []models.Role{{
				Name:        "Admins",
				Members:     []string{endAuthAdminID},
				Permissions: []models.Permission{{Name: "administrator", Enabled: true}},
			}},
		},
	}
}

func endAuthRequest(userID string) *http.Request {
	url := "/api/v2/court-sessions/x/end"
	if userID != "" {
		url += "?userId=" + userID
	}
	return httptest.NewRequest(http.MethodPut, url, nil)
}

func TestAuthorizeEndSession(t *testing.T) {
	tests := []struct {
		name        string
		userID      string
		expectFind  bool // whether the community is looked up
		wantOK      bool
		wantStatus  int
	}{
		{name: "owning judge always allowed", userID: endAuthJudgeID, expectFind: false, wantOK: true, wantStatus: http.StatusOK},
		{name: "community owner allowed", userID: endAuthOwnerID, expectFind: true, wantOK: true, wantStatus: http.StatusOK},
		{name: "administrator role allowed", userID: endAuthAdminID, expectFind: true, wantOK: true, wantStatus: http.StatusOK},
		{name: "random member forbidden", userID: endAuthRandomID, expectFind: true, wantOK: false, wantStatus: http.StatusForbidden},
		{name: "no actor unauthorized", userID: "", expectFind: false, wantOK: false, wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cdb := &mocks.CommunityDatabase{}
			if tt.expectFind {
				cdb.On("FindOne", mock.Anything, mock.Anything).Return(endAuthCommunity(), nil)
			}
			cs := CourtSession{CommDB: cdb}

			w := httptest.NewRecorder()
			r := endAuthRequest(tt.userID)

			ok := cs.authorizeEndSession(context.Background(), w, r, endAuthSession())

			assert.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				assert.Equal(t, tt.wantStatus, w.Code)
			}
			cdb.AssertExpectations(t)
		})
	}
}
