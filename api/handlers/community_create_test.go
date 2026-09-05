package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

const createOwnerID = "507f1f77bcf86cd799439099"

// stubCreateOwnerLookup wires UDB.FindOne({_id: uID}) -> a user on the given plan.
func stubCreateOwnerLookup(udb *mocks.UserDatabase, uID primitive.ObjectID, plan string, active bool) {
	mr := &mocks.SingleResultHelper{}
	mr.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		ptr := args.Get(0).(*models.User)
		*ptr = models.User{
			ID: uID.Hex(),
			Details: models.UserDetails{
				Subscription: models.Subscription{Plan: plan, Active: active},
			},
		}
	}).Return(nil)
	udb.On("FindOne", mock.Anything, bson.M{"_id": uID}).Return(mr)
}

func createCommunityBody(ownerID string) []byte {
	body, _ := json.Marshal(map[string]interface{}{
		"community": map[string]interface{}{
			"ownerID":     ownerID,
			"name":        "Test Community",
			"description": "A community for testing",
			"visibility":  "public",
		},
	})
	return body
}

func doCreateCommunity(cdb *mocks.CommunityDatabase, udb *mocks.UserDatabase, body []byte) *httptest.ResponseRecorder {
	c := handlers.Community{DB: cdb, UDB: udb}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/community", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c.CreateCommunityHandler(w, req)
	return w
}

// TestCreateCommunity_MissingOwnerIDDoesNotInsert is the regression guard for
// the reported "every time I create a community it says that it fails" bug.
//
// A logged-out web visitor or a mobile client whose stored userId had been
// cleared sent ownerID:"" . The old handler skipped the cap check (it was
// wrapped in `if ownerHex != ""`), inserted the community, and only then tried
// ObjectIDFromHex and returned 400. The user saw a failure and every retry left
// another ownerless community in the collection.
func TestCreateCommunity_MissingOwnerIDDoesNotInsert(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}

	w := doCreateCommunity(cdb, udb, createCommunityBody(""))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	cdb.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)

	var body map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "missing_owner", body["error"])
	assert.NotEmpty(t, body["message"])
}

func TestCreateCommunity_InvalidOwnerIDDoesNotInsert(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}

	w := doCreateCommunity(cdb, udb, createCommunityBody("not-a-hex-id"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	cdb.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)

	var body map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "invalid_owner", body["error"])
}

// TestCreateCommunity_CapReachedIsActionable asserts the 403 carries a message a
// client can render verbatim, plus the numbers behind it. Both clients used to
// throw this body away and show a bare "Failed to create community".
func TestCreateCommunity_CapReachedIsActionable(t *testing.T) {
	uID, _ := primitive.ObjectIDFromHex(createOwnerID)
	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}

	stubCreateOwnerLookup(udb, uID, models.TierFree, false)
	cdb.On("CountDocuments", mock.Anything, bson.M{"community.ownerID": createOwnerID}).
		Return(int64(1), nil)

	w := doCreateCommunity(cdb, udb, createCommunityBody(createOwnerID))

	assert.Equal(t, http.StatusForbidden, w.Code)
	cdb.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)

	var body map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "community_limit_reached", body["error"])
	assert.Contains(t, body["message"], "free")
	assert.Equal(t, float64(1), body["cap"])
	assert.Equal(t, float64(1), body["active"])
	assert.Equal(t, "free", body["plan"])

	// The nested envelope mirrors the flat keys so anything still parsing
	// models.ErrorMessageResponse keeps working.
	resp, ok := body["response"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "community_limit_reached", resp["error"])
}

// TestCreateCommunity_LapsedSubscriptionFallsBackToFree covers the client/server
// disagreement the website had: it read subscription.plan and ignored
// subscription.active, so a lapsed premium user was shown a limit of 10 while
// the server capped them at 1.
func TestCreateCommunity_LapsedSubscriptionFallsBackToFree(t *testing.T) {
	uID, _ := primitive.ObjectIDFromHex(createOwnerID)
	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}

	stubCreateOwnerLookup(udb, uID, models.TierPremium, false)
	cdb.On("CountDocuments", mock.Anything, bson.M{"community.ownerID": createOwnerID}).
		Return(int64(1), nil)

	w := doCreateCommunity(cdb, udb, createCommunityBody(createOwnerID))

	assert.Equal(t, http.StatusForbidden, w.Code)

	var body map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "free", body["plan"])
	assert.Equal(t, float64(1), body["cap"])
}

// TestCreateCommunity_UnderCapCreates is the happy path: an active premium_plus
// owner with existing communities still gets through, and the community is
// linked onto the user document.
func TestCreateCommunity_UnderCapCreates(t *testing.T) {
	uID, _ := primitive.ObjectIDFromHex(createOwnerID)
	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}

	stubCreateOwnerLookup(udb, uID, models.TierPremiumPlus, true)
	cdb.On("CountDocuments", mock.Anything, bson.M{"community.ownerID": createOwnerID}).
		Return(int64(42), nil)
	cdb.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil)
	udb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	w := doCreateCommunity(cdb, udb, createCommunityBody(createOwnerID))

	assert.Equal(t, http.StatusCreated, w.Code)
	cdb.AssertCalled(t, "InsertOne", mock.Anything, mock.Anything)
}
