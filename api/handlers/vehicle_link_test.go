package handlers_test

import (
	"bytes"
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

// updateVehicle drives UpdateVehicleHandler against a stored vehicle and
// returns the VehicleDetails that would have been written.
func updateVehicle(t *testing.T, stored models.VehicleDetails, body string) models.VehicleDetails {
	t.Helper()
	vID := primitive.NewObjectID()

	var written models.VehicleDetails
	mockDB := &mocks.VehicleDatabase{}
	mockDB.On("FindOne", mock.Anything, bson.M{"_id": vID}).
		Return(&models.Vehicle{ID: vID, Details: stored}, nil)
	mockDB.On("UpdateOne", mock.Anything, bson.M{"_id": vID}, mock.Anything).
		Run(func(args mock.Arguments) {
			set := args.Get(2).(bson.M)["$set"].(bson.M)
			written = set["vehicle"].(models.VehicleDetails)
		}).Return(nil)

	req := httptest.NewRequest("PUT", "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = mux.SetURLVars(req, map[string]string{"vehicle_id": vID.Hex()})

	rr := httptest.NewRecorder()
	handlers.Vehicle{DB: mockDB}.UpdateVehicleHandler(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	return written
}

// The 1.3M case: ownership held only on the deprecated id. Before this, unlink
// cleared linkedCivilianID (already empty) and the vehicle kept matching the
// list handler's $or, so it came straight back.
func TestUnlinkClearsDeprecatedOwnerFields(t *testing.T) {
	got := updateVehicle(t,
		models.VehicleDetails{RegisteredOwnerID: "civA", RegisteredOwner: "Jack Dean"},
		`{"linkedCivilianID":""}`)

	assert.Equal(t, "", got.LinkedCivilianID)
	assert.Equal(t, "", got.RegisteredOwnerID, "vehicle would still match the list query")
	assert.Equal(t, "", got.RegisteredOwner, "a stale owner name would still show on CAD lookups")
}

func TestLinkMirrorsOntoDeprecatedOwnerID(t *testing.T) {
	got := updateVehicle(t,
		models.VehicleDetails{},
		`{"linkedCivilianID":"civB"}`)

	assert.Equal(t, "civB", got.LinkedCivilianID)
	assert.Equal(t, "civB", got.RegisteredOwnerID)
}

// Re-linking an asset that was owned by someone else must not leave it
// matching the previous owner as well.
func TestRelinkDoesNotLeaveTheAssetOnTwoCivilians(t *testing.T) {
	got := updateVehicle(t,
		models.VehicleDetails{RegisteredOwnerID: "civA", RegisteredOwner: "Jack Dean"},
		`{"linkedCivilianID":"civB"}`)

	assert.Equal(t, "civB", got.LinkedCivilianID)
	assert.Equal(t, "civB", got.RegisteredOwnerID)
	assert.Equal(t, "", got.RegisteredOwner, "previous owner's name must not survive the move")
}

func TestLinkKeepsAnOwnerNameTheCallerSupplied(t *testing.T) {
	got := updateVehicle(t,
		models.VehicleDetails{RegisteredOwnerID: "civA", RegisteredOwner: "Jack Dean"},
		`{"linkedCivilianID":"civB","registeredOwner":"Marty Mcfly"}`)

	assert.Equal(t, "civB", got.RegisteredOwnerID)
	assert.Equal(t, "Marty Mcfly", got.RegisteredOwner)
}

// The guard that matters most: an edit that never mentions ownership must not
// silently unlink a vehicle held on the deprecated id.
func TestEditingAnUnrelatedFieldLeavesOwnershipAlone(t *testing.T) {
	got := updateVehicle(t,
		models.VehicleDetails{RegisteredOwnerID: "civA", RegisteredOwner: "Jack Dean", Color: "Blue"},
		`{"color":"Red"}`)

	assert.Equal(t, "Red", got.Color)
	assert.Equal(t, "civA", got.RegisteredOwnerID, "recolouring a vehicle must not unlink it")
	assert.Equal(t, "Jack Dean", got.RegisteredOwner)
}
