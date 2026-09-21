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
	"github.com/linesmerrill/police-cad-api/models"
)

const (
	tenCodeCommunityID = "507f1f77bcf86cd799439011"
	tenCodeID          = "507f1f77bcf86cd799439044"
)

// A community that rewrote its ten-codes into plain words has nothing in the
// text for dispatch to read, so it says what each code means instead. These
// cover that category surviving the write path.

func newAddTenCodeRequest(t *testing.T, body map[string]interface{}) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	assert.NoError(t, err)
	req, err := http.NewRequest(
		http.MethodPost,
		"/api/v1/community/"+tenCodeCommunityID+"/tenCodes",
		bytes.NewReader(raw),
	)
	assert.NoError(t, err)
	return mux.SetURLVars(req, map[string]string{"communityId": tenCodeCommunityID})
}

func newUpdateTenCodeRequest(t *testing.T, body map[string]interface{}) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	assert.NoError(t, err)
	req, err := http.NewRequest(
		http.MethodPut,
		"/api/v1/community/"+tenCodeCommunityID+"/tenCodes/"+tenCodeID,
		bytes.NewReader(raw),
	)
	assert.NoError(t, err)
	return mux.SetURLVars(req, map[string]string{
		"communityId": tenCodeCommunityID,
		"codeId":      tenCodeID,
	})
}

func tenCodeHandler(t *testing.T) (handlers.Community, *mocks.CommunityDatabase, *interface{}) {
	t.Helper()
	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}
	aldb := &mocks.AuditLogDatabase{}
	// The audit entry is written on a goroutine, so it has to be allowed for
	// even though nothing here asserts on it.
	aldb.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	udb.On("FindOne", mock.Anything, mock.Anything).Return(&models.User{}, nil).Maybe()
	var captured interface{}
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(2) }).
		Return(nil).Maybe()
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(2) }).
		Return(nil).Maybe()
	return handlers.Community{DB: cdb, UDB: udb, ALDB: aldb}, cdb, &captured
}

func TestAddTenCode_StoresTheCategory(t *testing.T) {
	c, _, captured := tenCodeHandler(t)

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.AddTenCodeHandler).ServeHTTP(rr, newAddTenCodeRequest(t, map[string]interface{}{
		"code":        "Mobile",
		"description": "Free to respond",
		"category":    "Available", // mixed case, as a UI might send it
	}))

	assert.Equal(t, http.StatusOK, rr.Code)
	push, ok := (*captured).(bson.M)["$push"].(bson.M)
	assert.True(t, ok, "expected a $push: %#v", *captured)
	added, ok := push["community.tenCodes"].(models.TenCodes)
	assert.True(t, ok, "expected a ten-code: %#v", push)
	assert.Equal(t, models.TenCodeCategoryAvailable, added.Category)
	assert.Equal(t, "Mobile", added.Code)
}

func TestAddTenCode_WithoutACategoryIsStillFine(t *testing.T) {
	c, _, captured := tenCodeHandler(t)

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.AddTenCodeHandler).ServeHTTP(rr, newAddTenCodeRequest(t, map[string]interface{}{
		"code":        "10-20",
		"description": "Location",
	}))

	assert.Equal(t, http.StatusOK, rr.Code)
	push := (*captured).(bson.M)["$push"].(bson.M)
	assert.Equal(t, "", push["community.tenCodes"].(models.TenCodes).Category)
}

func TestAddTenCode_RejectsAnUnknownCategory(t *testing.T) {
	c, cdb, _ := tenCodeHandler(t)

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.AddTenCodeHandler).ServeHTTP(rr, newAddTenCodeRequest(t, map[string]interface{}{
		"code":        "Mobile",
		"description": "Free to respond",
		"category":    "sort of free",
	}))

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestUpdateTenCode_SetsTheCategory(t *testing.T) {
	c, _, captured := tenCodeHandler(t)

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateTenCodeHandler).ServeHTTP(rr, newUpdateTenCodeRequest(t, map[string]interface{}{
		"code":        "Mobile",
		"description": "Free to respond",
		"category":    "available",
	}))

	assert.Equal(t, http.StatusOK, rr.Code)
	set := (*captured).(bson.M)["$set"].(bson.M)
	assert.Equal(t, models.TenCodeCategoryAvailable, set["community.tenCodes.$[tenCode].category"])
	assert.Equal(t, "Mobile", set["community.tenCodes.$[tenCode].code"])
}

func TestUpdateTenCode_ClearsTheCategory(t *testing.T) {
	c, _, captured := tenCodeHandler(t)

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateTenCodeHandler).ServeHTTP(rr, newUpdateTenCodeRequest(t, map[string]interface{}{
		"category": "",
	}))

	assert.Equal(t, http.StatusOK, rr.Code)
	set := (*captured).(bson.M)["$set"].(bson.M)
	assert.Equal(t, "", set["community.tenCodes.$[tenCode].category"])
}

// The old loop wrote whatever key the body carried into the ten-code document.
func TestUpdateTenCode_IgnoresFieldsATenCodeDoesNotHave(t *testing.T) {
	c, _, captured := tenCodeHandler(t)

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateTenCodeHandler).ServeHTTP(rr, newUpdateTenCodeRequest(t, map[string]interface{}{
		"code":     "Mobile",
		"isActive": true,
		"_id":      "not-an-id",
	}))

	assert.Equal(t, http.StatusOK, rr.Code)
	set := (*captured).(bson.M)["$set"].(bson.M)
	assert.Equal(t, 1, len(set), "only the code should have been written: %#v", set)
	assert.Equal(t, "Mobile", set["community.tenCodes.$[tenCode].code"])
}

func TestUpdateTenCode_RejectsAnUnknownCategory(t *testing.T) {
	c, cdb, _ := tenCodeHandler(t)

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateTenCodeHandler).ServeHTTP(rr, newUpdateTenCodeRequest(t, map[string]interface{}{
		"category": "kind of busy",
	}))

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// New communities start with the standard codes already categorised, so
// dispatch works for them without anyone opening settings.
func TestDefaultTenCodes_CarryCategories(t *testing.T) {
	byCode := map[string]models.TenCodes{}
	for _, tc := range models.DefaultTenCodes() {
		byCode[tc.Code] = tc
	}

	assert.Equal(t, models.TenCodeCategoryAvailable, byCode["10-8"].Category)
	assert.Equal(t, models.TenCodeCategoryBusy, byCode["10-6"].Category)
	assert.Equal(t, models.TenCodeCategoryBusy, byCode["10-7"].Category)
	assert.Equal(t, models.TenCodeCategoryEmergency, byCode["Signal 100"].Category)
	assert.Equal(t, models.TenCodeCategoryOffDuty, byCode["10-42"].Category)
	assert.Equal(t, "", byCode["10-20"].Category, "a location code means nothing to dispatch")

	for _, tc := range models.DefaultTenCodes() {
		assert.True(t, models.IsValidTenCodeCategory(tc.Category), "%s has an unknown category %q", tc.Code, tc.Category)
	}
}

func TestIsValidTenCodeCategory(t *testing.T) {
	for _, valid := range []string{"", "available", "busy", "emergency", "off-duty"} {
		assert.True(t, models.IsValidTenCodeCategory(valid), valid)
	}
	for _, invalid := range []string{"Available", "off duty", "offduty", "free"} {
		assert.False(t, models.IsValidTenCodeCategory(invalid), invalid)
	}
}

// Guard against the ten-code id being regenerated on every call, which would
// break every member whose status points at one.
func TestDefaultTenCodes_HaveDistinctIDs(t *testing.T) {
	seen := map[primitive.ObjectID]bool{}
	for _, tc := range models.DefaultTenCodes() {
		assert.False(t, seen[tc.ID], "duplicate id for %s", tc.Code)
		seen[tc.ID] = true
	}
}
