package handlers_test

import (
	"bytes"
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

const softDeleteCommunityID = "507f1f77bcf86cd799439011"

func newSoftDeleteHandler(cdb *mocks.CommunityDatabase, udb *mocks.UserDatabase, aldb *mocks.AuditLogDatabase) handlers.Community {
	return handlers.Community{
		DB:   cdb,
		UDB:  udb,
		ALDB: aldb,
	}
}

func newCommunityRequest(method, path string, body []byte, vars map[string]string) *http.Request {
	var req *http.Request
	if body == nil {
		req, _ = http.NewRequest(method, path, nil)
	} else {
		req, _ = http.NewRequest(method, path, bytes.NewReader(body))
	}
	return mux.SetURLVars(req, vars)
}

// TestDeleteCommunity_SoftDeletesAndDoesNotCascade asserts that the rewritten
// handler sets the soft-delete fields and does NOT issue any DeleteOne against
// the community or cascade collections. Hard deletion now belongs to the cron.
func TestDeleteCommunity_SoftDeletesAndDoesNotCascade(t *testing.T) {
	cID, _ := primitive.ObjectIDFromHex(softDeleteCommunityID)

	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}
	aldb := &mocks.AuditLogDatabase{}

	cdb.On("FindOneIncludingPending", mock.Anything, bson.M{"_id": cID}).
		Return(&models.Community{
			ID:      cID,
			Details: models.CommunityDetails{Name: "Test Community", OwnerID: "owner-1"},
		}, nil)

	cdb.On("UpdateOne", mock.Anything, bson.M{"_id": cID}, mock.MatchedBy(func(update bson.M) bool {
		set, ok := update["$set"].(bson.M)
		if !ok {
			return false
		}
		_, hasPending := set["community.pendingDeletionAt"]
		_, hasScheduled := set["community.scheduledDeletionAt"]
		return hasPending && hasScheduled
	})).Return(nil)

	aldb.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	c := newSoftDeleteHandler(cdb, udb, aldb)

	req := newCommunityRequest(http.MethodDelete, "/api/v1/community/"+softDeleteCommunityID, nil,
		map[string]string{"community_id": softDeleteCommunityID})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.DeleteCommunityByIDHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var body map[string]interface{}
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, float64(handlers.CommunitySoftDeleteGraceDays), body["graceDays"])
	scheduled, ok := body["scheduledDeletionAt"].(string)
	assert.True(t, ok, "scheduledDeletionAt should be present in response")
	parsed, err := time.Parse(time.RFC3339, scheduled)
	assert.NoError(t, err)
	assert.WithinDuration(t,
		time.Now().UTC().Add(handlers.CommunitySoftDeleteGraceDays*24*time.Hour),
		parsed,
		2*time.Minute,
		"scheduledDeletionAt should be ~30 days out")

	cdb.AssertNotCalled(t, "DeleteOne", mock.Anything, mock.Anything)
	cdb.AssertExpectations(t)
}

// TestDeleteCommunity_RejectsDoubleDelete asserts the second delete on the
// same community returns 400 rather than silently re-scheduling it.
func TestDeleteCommunity_RejectsDoubleDelete(t *testing.T) {
	cID, _ := primitive.ObjectIDFromHex(softDeleteCommunityID)
	now := primitive.NewDateTimeFromTime(time.Now())

	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}
	aldb := &mocks.AuditLogDatabase{}

	cdb.On("FindOneIncludingPending", mock.Anything, bson.M{"_id": cID}).
		Return(&models.Community{
			ID: cID,
			Details: models.CommunityDetails{
				Name:              "Already Pending",
				PendingDeletionAt: &now,
			},
		}, nil)

	c := newSoftDeleteHandler(cdb, udb, aldb)

	req := newCommunityRequest(http.MethodDelete, "/api/v1/community/"+softDeleteCommunityID, nil,
		map[string]string{"community_id": softDeleteCommunityID})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.DeleteCommunityByIDHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// TestRestoreCommunity_RequiresAdminRole asserts a regular user payload is
// rejected with 403 — restores are staff-only.
func TestRestoreCommunity_RequiresAdminRole(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}
	aldb := &mocks.AuditLogDatabase{}

	c := newSoftDeleteHandler(cdb, udb, aldb)

	payload := map[string]interface{}{
		"currentUser": map[string]interface{}{
			"role": "user",
		},
	}
	body, _ := json.Marshal(payload)
	req := newCommunityRequest(http.MethodPost,
		"/api/v1/admin/communities/"+softDeleteCommunityID+"/restore-pending-deletion",
		body, map[string]string{"community_id": softDeleteCommunityID})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.RestoreCommunityPendingDeletionHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// TestRestoreCommunity_AdminClearsFields asserts an admin call $unsets the
// soft-delete fields on a pending community.
func TestRestoreCommunity_AdminClearsFields(t *testing.T) {
	cID, _ := primitive.ObjectIDFromHex(softDeleteCommunityID)
	now := primitive.NewDateTimeFromTime(time.Now())

	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}
	aldb := &mocks.AuditLogDatabase{}

	cdb.On("FindOneIncludingPending", mock.Anything, bson.M{"_id": cID}).
		Return(&models.Community{
			ID: cID,
			Details: models.CommunityDetails{
				Name:              "Pending",
				PendingDeletionAt: &now,
			},
		}, nil)

	cdb.On("UpdateOne", mock.Anything, bson.M{"_id": cID}, mock.MatchedBy(func(update bson.M) bool {
		unset, ok := update["$unset"].(bson.M)
		if !ok {
			return false
		}
		_, hasPending := unset["community.pendingDeletionAt"]
		_, hasScheduled := unset["community.scheduledDeletionAt"]
		return hasPending && hasScheduled
	})).Return(nil)

	aldb.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	c := newSoftDeleteHandler(cdb, udb, aldb)

	payload := map[string]interface{}{
		"currentUser": map[string]interface{}{
			"email": "staff@linespolice-cad.com",
			"roles": []interface{}{"admin"},
		},
	}
	body, _ := json.Marshal(payload)
	req := newCommunityRequest(http.MethodPost,
		"/api/v1/admin/communities/"+softDeleteCommunityID+"/restore-pending-deletion",
		body, map[string]string{"community_id": softDeleteCommunityID})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.RestoreCommunityPendingDeletionHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	cdb.AssertExpectations(t)
}

// TestRestoreCommunity_RejectsNonPending asserts restoring a community that
// is not pending deletion returns 400.
func TestRestoreCommunity_RejectsNonPending(t *testing.T) {
	cID, _ := primitive.ObjectIDFromHex(softDeleteCommunityID)

	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}
	aldb := &mocks.AuditLogDatabase{}

	cdb.On("FindOneIncludingPending", mock.Anything, bson.M{"_id": cID}).
		Return(&models.Community{
			ID:      cID,
			Details: models.CommunityDetails{Name: "Active"},
		}, nil)

	c := newSoftDeleteHandler(cdb, udb, aldb)

	payload := map[string]interface{}{
		"currentUser": map[string]interface{}{"roles": []interface{}{"owner"}},
	}
	body, _ := json.Marshal(payload)
	req := newCommunityRequest(http.MethodPost,
		"/api/v1/admin/communities/"+softDeleteCommunityID+"/restore-pending-deletion",
		body, map[string]string{"community_id": softDeleteCommunityID})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.RestoreCommunityPendingDeletionHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// TestCommunityHandler_Returns410ForPendingDeletion asserts a direct hit to
// the detail endpoint returns 410 Gone with the route-block payload.
func TestCommunityHandler_Returns410ForPendingDeletion(t *testing.T) {
	cID, _ := primitive.ObjectIDFromHex(softDeleteCommunityID)
	now := primitive.NewDateTimeFromTime(time.Now())
	scheduled := primitive.NewDateTimeFromTime(time.Now().Add(30 * 24 * time.Hour))

	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}

	cdb.On("FindOneIncludingPending", mock.Anything, bson.M{"_id": cID}).
		Return(&models.Community{
			ID: cID,
			Details: models.CommunityDetails{
				Name:                "Pending Community",
				PendingDeletionAt:   &now,
				ScheduledDeletionAt: &scheduled,
			},
		}, nil)

	c := handlers.Community{DB: cdb, UDB: udb}

	req := newCommunityRequest(http.MethodGet, "/api/v1/community/"+softDeleteCommunityID, nil,
		map[string]string{"community_id": softDeleteCommunityID})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.CommunityHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusGone, rr.Code)

	var body map[string]interface{}
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, "pending_deletion", body["error"])
	assert.Equal(t, "Pending Community", body["communityName"])
	assert.NotEmpty(t, body["scheduledDeletionAt"])
	// CountDocuments must not run — the handler short-circuits on the pending check.
	udb.AssertNotCalled(t, "CountDocuments", mock.Anything, mock.Anything)
}

// TestCommunityHandler_Returns404ForMissing asserts the active path still
// surfaces a real ErrNoDocuments as 404.
func TestCommunityHandler_Returns404ForMissing(t *testing.T) {
	cID, _ := primitive.ObjectIDFromHex(softDeleteCommunityID)

	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}

	cdb.On("FindOneIncludingPending", mock.Anything, bson.M{"_id": cID}).
		Return(nil, mongo.ErrNoDocuments)

	c := handlers.Community{DB: cdb, UDB: udb}

	req := newCommunityRequest(http.MethodGet, "/api/v1/community/"+softDeleteCommunityID, nil,
		map[string]string{"community_id": softDeleteCommunityID})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.CommunityHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// Reference imports so the linter doesn't complain when one of them is
// transiently unused while editing.
var _ = strings.TrimSpace
var _ = errors.New
