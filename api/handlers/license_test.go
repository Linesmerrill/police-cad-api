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

// bothCivilianKeys is the filter the list handler must use. Legacy documents
// key the civilian as license.ownerID; matching only license.civilianID hid
// ~316k licenses from every API consumer.
func bothCivilianKeys(civID string) bson.M {
	return bson.M{"$or": []bson.M{
		{"license.civilianID": civID},
		{"license.ownerID": civID},
	}}
}

func licenseRequest(t *testing.T, method, body string, urlVars map[string]string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	return mux.SetURLVars(req, urlVars)
}

func TestLicensesByCivilianID_MatchesLegacyOwnerIDKey(t *testing.T) {
	civID := primitive.NewObjectID().Hex()

	// A legacy record: no civilianID, no type, no notes.
	legacy := models.License{
		ID: primitive.NewObjectID(),
		Details: models.LicenseDetails{
			LicenseType:     "driver license",
			AdditionalNotes: "renewed at the DMV",
			OwnerID:         civID,
			OwnerName:       "jackson dean",
			Status:          "valid",
			ExpirationDate:  "2033-11-11",
		},
	}

	mockDB := &mocks.LicenseDatabase{}
	mockDB.On("Find", mock.Anything, bothCivilianKeys(civID), mock.Anything).
		Return([]models.License{legacy}, nil)
	mockDB.On("CountDocuments", mock.Anything, bothCivilianKeys(civID)).
		Return(int64(1), nil)

	rr := httptest.NewRecorder()
	handlers.License{DB: mockDB}.LicensesByCivilianIDHandler(
		rr, licenseRequest(t, "GET", "", map[string]string{"civilian_id": civID}),
	)

	assert.Equal(t, http.StatusOK, rr.Code)

	var got struct {
		TotalCount int64 `json:"totalCount"`
		Data       []struct {
			License map[string]interface{} `json:"license"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	assert.Equal(t, int64(1), got.TotalCount)
	if assert.Len(t, got.Data, 1) {
		lic := got.Data[0].License
		// The legacy record arrives in the canonical shape, so a client that
		// only knows about type/notes/civilianID can still read it.
		assert.Equal(t, "driver license", lic["type"])
		assert.Equal(t, "renewed at the DMV", lic["notes"])
		assert.Equal(t, civID, lic["civilianID"])
		assert.Equal(t, models.LicenseStatusValid, lic["status"])
		assert.Equal(t, "jackson dean", lic["ownerName"])
	}

	mockDB.AssertExpectations(t)
}

func TestUpdateLicense_NormalizesAndMirrorsLegacyKeys(t *testing.T) {
	lID := primitive.NewObjectID()

	stored := &models.License{
		ID: lID,
		Details: models.LicenseDetails{
			LicenseType:     "driver license",
			AdditionalNotes: "old note",
			OwnerID:         primitive.NewObjectID().Hex(),
			Status:          "valid",
			ExpirationDate:  "2033-11-11",
		},
	}

	var gotUpdate bson.M
	mockDB := &mocks.LicenseDatabase{}
	mockDB.On("FindOne", mock.Anything, bson.M{"_id": lID}).Return(stored, nil)
	mockDB.On("UpdateOne", mock.Anything, bson.M{"_id": lID}, mock.Anything).
		Run(func(args mock.Arguments) {
			gotUpdate = args.Get(2).(bson.M)
		}).Return(nil)

	body := `{"type":"Weapon License","status":"revoked","expirationDate":"01/02/2030","notes":"new note"}`
	rr := httptest.NewRecorder()
	handlers.License{DB: mockDB}.UpdateLicenseByIDHandler(
		rr, licenseRequest(t, "PUT", body, map[string]string{"license_id": lID.Hex()}),
	)

	assert.Equal(t, http.StatusOK, rr.Code)

	set, ok := gotUpdate["$set"].(bson.M)
	if !assert.True(t, ok, "expected a $set document") {
		return
	}

	// Canonical keys carry the normalized values.
	assert.Equal(t, "Weapon License", set["license.type"])
	assert.Equal(t, "new note", set["license.notes"])
	assert.Equal(t, models.LicenseStatusRevoked, set["license.status"])
	assert.Equal(t, "2030-01-02", set["license.expirationDate"])

	// The stored record is legacy, and the website's police and dispatch
	// dashboards read it through the Mongoose model — so the legacy keys have
	// to move too, or those screens keep showing the old value.
	assert.Equal(t, "Weapon License", set["license.licenseType"])
	assert.Equal(t, "new note", set["license.additionalNotes"])

	assert.NotNil(t, set["license.updatedAt"])
	mockDB.AssertExpectations(t)
}

func TestUpdateLicense_ModernRecordDoesNotGainLegacyKeys(t *testing.T) {
	lID := primitive.NewObjectID()

	stored := &models.License{
		ID: lID,
		Details: models.LicenseDetails{
			Type:       "Pilot License",
			CivilianID: primitive.NewObjectID().Hex(),
			Status:     "Valid",
		},
	}

	var gotUpdate bson.M
	mockDB := &mocks.LicenseDatabase{}
	mockDB.On("FindOne", mock.Anything, bson.M{"_id": lID}).Return(stored, nil)
	mockDB.On("UpdateOne", mock.Anything, bson.M{"_id": lID}, mock.Anything).
		Run(func(args mock.Arguments) {
			gotUpdate = args.Get(2).(bson.M)
		}).Return(nil)

	rr := httptest.NewRecorder()
	handlers.License{DB: mockDB}.UpdateLicenseByIDHandler(
		rr, licenseRequest(t, "PUT", `{"type":"Fishing License"}`, map[string]string{"license_id": lID.Hex()}),
	)

	assert.Equal(t, http.StatusOK, rr.Code)
	set := gotUpdate["$set"].(bson.M)

	assert.Equal(t, "Fishing License", set["license.type"])
	for _, key := range []string{"license.licenseType", "license.additionalNotes", "license.ownerID"} {
		assert.NotContains(t, set, key, "a modern record should not sprout legacy keys")
	}
}

// A caller sending the legacy spelling still lands on the canonical key.
func TestUpdateLicense_AcceptsLegacyRequestKeys(t *testing.T) {
	lID := primitive.NewObjectID()

	var gotUpdate bson.M
	mockDB := &mocks.LicenseDatabase{}
	mockDB.On("FindOne", mock.Anything, bson.M{"_id": lID}).
		Return(&models.License{ID: lID, Details: models.LicenseDetails{Type: "Pilot License"}}, nil)
	mockDB.On("UpdateOne", mock.Anything, bson.M{"_id": lID}, mock.Anything).
		Run(func(args mock.Arguments) {
			gotUpdate = args.Get(2).(bson.M)
		}).Return(nil)

	rr := httptest.NewRecorder()
	handlers.License{DB: mockDB}.UpdateLicenseByIDHandler(
		rr, licenseRequest(t, "PUT", `{"licenseType":"Hunting License"}`, map[string]string{"license_id": lID.Hex()}),
	)

	assert.Equal(t, http.StatusOK, rr.Code)
	set := gotUpdate["$set"].(bson.M)
	assert.Equal(t, "Hunting License", set["license.type"])
}
