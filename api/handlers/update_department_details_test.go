package handlers_test

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

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
)

// Shared IDs for these tests.
const (
	updateDeptCommunityID  = "507f1f77bcf86cd799439011"
	updateDeptDepartmentID = "507f1f77bcf86cd799439022"
)

func newUpdateDeptHandler(cdb *mocks.CommunityDatabase, aldb *mocks.AuditLogDatabase) handlers.Community {
	return handlers.Community{
		DB:   cdb,
		ALDB: aldb,
	}
}

func newUpdateDeptRequest(t *testing.T, body interface{}) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	assert.NoError(t, err)
	req, err := http.NewRequest(
		http.MethodPatch,
		"/api/v1/community/"+updateDeptCommunityID+"/departments/"+updateDeptDepartmentID,
		bytes.NewReader(raw),
	)
	assert.NoError(t, err)
	return mux.SetURLVars(req, map[string]string{
		"communityId":  updateDeptCommunityID,
		"departmentId": updateDeptDepartmentID,
	})
}

// TestUpdateDepartmentDetails_RejectsOutOfRangeAfkPrompt is the regression
// test for the original bug: a client sent afkPromptIntervalSeconds=3e22, the
// handler accepted it blindly, MongoDB stored a BSON double, and every
// community read afterwards 500'd with "overflows int64". The hardened
// handler now refuses values outside the documented range.
func TestUpdateDepartmentDetails_RejectsOutOfRangeAfkPrompt(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	aldb := &mocks.AuditLogDatabase{}
	c := newUpdateDeptHandler(cdb, aldb)

	req := newUpdateDeptRequest(t, map[string]interface{}{
		"afkPromptIntervalSeconds": 3e22,
	})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentDetailsHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestUpdateDepartmentDetails_RejectsNonIntegerNumeric(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	aldb := &mocks.AuditLogDatabase{}
	c := newUpdateDeptHandler(cdb, aldb)

	req := newUpdateDeptRequest(t, map[string]interface{}{
		"maxSessionMinutes": 12.5,
	})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentDetailsHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestUpdateDepartmentDetails_RejectsNegativeBasePay(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	aldb := &mocks.AuditLogDatabase{}
	c := newUpdateDeptHandler(cdb, aldb)

	req := newUpdateDeptRequest(t, map[string]interface{}{
		"basePayPerHour": -100,
	})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentDetailsHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// TestUpdateDepartmentDetails_RejectsUnknownField closes the mass-assignment
// vector: the old handler would happily write `_id` or `members` if a client
// named those fields in the body.
func TestUpdateDepartmentDetails_RejectsUnknownField(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	aldb := &mocks.AuditLogDatabase{}
	c := newUpdateDeptHandler(cdb, aldb)

	req := newUpdateDeptRequest(t, map[string]interface{}{
		"_id":     "deadbeefdeadbeefdeadbeef",
		"members": []map[string]string{{"userID": "attacker"}},
	})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentDetailsHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestUpdateDepartmentDetails_RejectsInvalidPayoutMode(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	aldb := &mocks.AuditLogDatabase{}
	c := newUpdateDeptHandler(cdb, aldb)

	req := newUpdateDeptRequest(t, map[string]interface{}{
		"payoutMode": "instant_yacht",
	})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentDetailsHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// TestUpdateDepartmentDetails_AcceptsValidEconomyPatch is the happy path: a
// realistic mixed payload the real economy-settings UI sends. It must persist
// every field with the correct typed value (int64 for the numeric ones, so
// the Go BSON decoder can round-trip without overflow).
func TestUpdateDepartmentDetails_AcceptsValidEconomyPatch(t *testing.T) {
	cID, _ := primitive.ObjectIDFromHex(updateDeptCommunityID)
	dID, _ := primitive.ObjectIDFromHex(updateDeptDepartmentID)

	cdb := &mocks.CommunityDatabase{}
	aldb := &mocks.AuditLogDatabase{}

	// audit log InsertOne fires on a goroutine — allow but do not require
	aldb.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	cdb.On(
		"UpdateOne",
		mock.Anything,
		bson.M{"_id": cID, "community.departments._id": dID},
		mock.MatchedBy(func(update bson.M) bool {
			set, ok := update["$set"].(bson.M)
			if !ok {
				return false
			}
			// Every numeric field must be persisted as int64 (not float64), or
			// the BSON encoder would write a NumberDouble and we'd be right
			// back where we started.
			checks := map[string]int64{
				"community.departments.$.basePayPerHour":           5000,
				"community.departments.$.maxSessionMinutes":        180,
				"community.departments.$.afkPromptIntervalSeconds": 300,
				"community.departments.$.afkGraceSeconds":          45,
			}
			for k, want := range checks {
				got, ok := set[k].(int64)
				if !ok || got != want {
					return false
				}
			}
			if set["community.departments.$.economyEnabled"] != true {
				return false
			}
			if set["community.departments.$.payoutMode"] != "on_clockout" {
				return false
			}
			return true
		}),
	).Return(nil)

	c := newUpdateDeptHandler(cdb, aldb)
	req := newUpdateDeptRequest(t, map[string]interface{}{
		"economyEnabled":           true,
		"basePayPerHour":           5000,
		"maxSessionMinutes":        180,
		"afkPromptIntervalSeconds": 300,
		"afkGraceSeconds":          45,
		"payoutMode":               "on_clockout",
	})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentDetailsHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	cdb.AssertExpectations(t)
}

// TestUpdateDepartmentDetails_EmptyBodyIsNoop guarantees the handler still
// accepts an empty patch (some legacy clients PATCH with no changes when
// debouncing) without hitting the database.
func TestUpdateDepartmentDetails_EmptyBodyIsNoop(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	aldb := &mocks.AuditLogDatabase{}
	c := newUpdateDeptHandler(cdb, aldb)

	req := newUpdateDeptRequest(t, map[string]interface{}{})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentDetailsHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}
