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

const updateCommunityID = "507f1f77bcf86cd799439033"

func newUpdateCommunityHandler(cdb *mocks.CommunityDatabase, aldb *mocks.AuditLogDatabase) handlers.Community {
	return handlers.Community{
		DB:   cdb,
		ALDB: aldb,
	}
}

func newUpdateCommunityRequest(t *testing.T, body interface{}) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	assert.NoError(t, err)
	req, err := http.NewRequest(
		http.MethodPatch,
		"/api/v1/community/"+updateCommunityID,
		bytes.NewReader(raw),
	)
	assert.NoError(t, err)
	return mux.SetURLVars(req, map[string]string{"community_id": updateCommunityID})
}

// Regression: same class of bug as UpdateDepartmentDetailsHandler. A
// community.economy field with a giant numeric like 3e22 used to land on the
// document and break every subsequent BSON decode of the community.
func TestUpdateCommunityField_RejectsOverflowingEconomyNumeric(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	aldb := &mocks.AuditLogDatabase{}
	c := newUpdateCommunityHandler(cdb, aldb)

	req := newUpdateCommunityRequest(t, map[string]interface{}{
		"economy": map[string]interface{}{"defaultStartingBalance": 3e22},
	})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateCommunityFieldHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestUpdateCommunityField_RejectsNonIntegerEconomyNumeric(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	aldb := &mocks.AuditLogDatabase{}
	c := newUpdateCommunityHandler(cdb, aldb)

	req := newUpdateCommunityRequest(t, map[string]interface{}{
		"economy": map[string]interface{}{"defaultDueDays": 12.5},
	})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateCommunityFieldHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestUpdateCommunityField_RejectsInvalidFineMode(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	aldb := &mocks.AuditLogDatabase{}
	c := newUpdateCommunityHandler(cdb, aldb)

	req := newUpdateCommunityRequest(t, map[string]interface{}{
		"economy": map[string]interface{}{"fineMode": "yacht_seizure"},
	})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateCommunityFieldHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestUpdateCommunityField_RejectsUnknownEconomyKey(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	aldb := &mocks.AuditLogDatabase{}
	c := newUpdateCommunityHandler(cdb, aldb)

	req := newUpdateCommunityRequest(t, map[string]interface{}{
		"economy": map[string]interface{}{"superSecretBackdoor": true},
	})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateCommunityFieldHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// Happy path: a realistic mobile save (every economy field present). Each
// numeric must persist as int64 — never float64 — so BSON cannot store a
// Double that the Go decoder won't read back.
func TestUpdateCommunityField_AcceptsValidEconomyPatch(t *testing.T) {
	cID, _ := primitive.ObjectIDFromHex(updateCommunityID)

	cdb := &mocks.CommunityDatabase{}
	aldb := &mocks.AuditLogDatabase{}
	aldb.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	cdb.On(
		"UpdateOne",
		mock.Anything,
		bson.M{"_id": cID},
		mock.MatchedBy(func(update bson.M) bool {
			set, ok := update["$set"].(bson.M)
			if !ok {
				return false
			}
			econ, ok := set["community.economy"].(bson.M)
			if !ok {
				return false
			}
			intChecks := map[string]int64{
				"defaultStartingBalance": 50000,
				"defaultDueDays":         14,
				"contestExtensionDays":   30,
			}
			for k, want := range intChecks {
				got, ok := econ[k].(int64)
				if !ok || got != want {
					return false
				}
			}
			return econ["enabled"] == true &&
				econ["allowNegativeBalance"] == false &&
				econ["fineMode"] == "inbox"
		}),
	).Return(nil)

	c := newUpdateCommunityHandler(cdb, aldb)
	req := newUpdateCommunityRequest(t, map[string]interface{}{
		"economy": map[string]interface{}{
			"enabled":                true,
			"defaultStartingBalance": 50000,
			"fineMode":               "inbox",
			"defaultDueDays":         14,
			"contestExtensionDays":   30,
			"allowNegativeBalance":   false,
		},
	})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateCommunityFieldHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	cdb.AssertExpectations(t)
}

// Backwards-compatibility: a non-economy PATCH (the common case for this
// catch-all endpoint) must still flow through unchanged. We don't want the
// economy hardening to lock down the other 40+ fields this endpoint serves.
func TestUpdateCommunityField_PassesThroughNonEconomyFields(t *testing.T) {
	cID, _ := primitive.ObjectIDFromHex(updateCommunityID)

	cdb := &mocks.CommunityDatabase{}
	aldb := &mocks.AuditLogDatabase{}
	aldb.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	cdb.On(
		"UpdateOne",
		mock.Anything,
		bson.M{"_id": cID},
		mock.MatchedBy(func(update bson.M) bool {
			set, ok := update["$set"].(bson.M)
			return ok && set["community.name"] == "Renamed Town"
		}),
	).Return(nil)

	c := newUpdateCommunityHandler(cdb, aldb)
	req := newUpdateCommunityRequest(t, map[string]interface{}{
		"name": "Renamed Town",
	})
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateCommunityFieldHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	cdb.AssertExpectations(t)
}
