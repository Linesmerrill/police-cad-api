package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// The reported bug: a community with "Default starting balance" set to $10,000
// created civilians that read $0.00. The grant was lazy — only a wallet open, a
// fine payment or a transfer ran it — so create-then-look-at-the-list showed
// nothing.

func createCivilian(t *testing.T, community *models.Community, payload map[string]interface{}) models.Civilian {
	t.Helper()

	mockCivDB := &mocks.CivilianDatabase{}
	mockCommDB := &mocks.CommunityDatabase{}

	var inserted models.Civilian
	mockCivDB.On("InsertOne", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			inserted = args.Get(1).(models.Civilian)
		}).Return(nil, nil)

	if community != nil {
		mockCommDB.On("FindOne", mock.Anything, bson.M{"_id": community.ID}).Return(community, nil)
	}
	mockCommDB.On("FindOne", mock.Anything, mock.Anything).Return(nil, assertAnyError{}).Maybe()

	handler := Civilian{DB: mockCivDB, CommDB: mockCommDB}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/civilian", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.CreateCivilianHandler(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	return inserted
}

// assertAnyError stands in for "community not found" on the catch-all mock.
type assertAnyError struct{}

func (assertAnyError) Error() string { return "not found" }

func economyCommunity(enabled bool, start int64) *models.Community {
	return &models.Community{
		ID: primitive.NewObjectID(),
		Details: models.CommunityDetails{
			Economy: models.EconomySettings{Enabled: enabled, DefaultStartingBalance: start},
		},
	}
}

func TestCreateCivilianGetsStartingBalance(t *testing.T) {
	community := economyCommunity(true, 1000000) // $10,000 in cents

	civ := createCivilian(t, community, map[string]interface{}{
		"name":              "Test Dude",
		"activeCommunityID": community.ID.Hex(),
	})

	assert.Equal(t, int64(1000000), civ.Details.Balance)
	assert.True(t, civ.Details.BalanceInitialized)
}

func TestCreateCivilianIgnoresClientSuppliedBalance(t *testing.T) {
	community := economyCommunity(true, 1000000)

	civ := createCivilian(t, community, map[string]interface{}{
		"name":               "Rich Dude",
		"activeCommunityID":  community.ID.Hex(),
		"balance":            999999999,
		"balanceInitialized": true,
	})

	assert.Equal(t, int64(1000000), civ.Details.Balance,
		"balance is server-owned; the posted value must not survive")
	assert.True(t, civ.Details.BalanceInitialized)
}

func TestCreateCivilianWithEconomyDisabledStaysUninitialized(t *testing.T) {
	community := economyCommunity(false, 1000000)

	civ := createCivilian(t, community, map[string]interface{}{
		"name":              "No Economy",
		"activeCommunityID": community.ID.Hex(),
	})

	assert.Equal(t, int64(0), civ.Details.Balance)
	assert.False(t, civ.Details.BalanceInitialized,
		"left uninitialized so the lazy backfill still applies if economy is switched on later")
}

func TestCreateCivilianWithZeroConfiguredStartIsInitialized(t *testing.T) {
	community := economyCommunity(true, 0)

	civ := createCivilian(t, community, map[string]interface{}{
		"name":              "Broke Dude",
		"activeCommunityID": community.ID.Hex(),
	})

	assert.Equal(t, int64(0), civ.Details.Balance)
	assert.True(t, civ.Details.BalanceInitialized,
		"a configured zero is a decision; without the flag the backfill would keep zeroing earnings")
}

func TestCreateCivilianWithNoCommunityStaysUninitialized(t *testing.T) {
	civ := createCivilian(t, nil, map[string]interface{}{"name": "Loner"})

	assert.Equal(t, int64(0), civ.Details.Balance)
	assert.False(t, civ.Details.BalanceInitialized)
}

func TestCreateCivilianWithUnreadableCommunityStaysUninitialized(t *testing.T) {
	// A malformed community id must not fail the create — the civilian is still
	// made, just without an opening balance.
	civ := createCivilian(t, nil, map[string]interface{}{
		"name":              "Bad Community",
		"activeCommunityID": "not-an-object-id",
	})

	assert.Equal(t, int64(0), civ.Details.Balance)
	assert.False(t, civ.Details.BalanceInitialized)
}

// The subtler half: the payout paths $inc the credit and $set
// balanceInitialized: true in one write. For a civilian who never received a
// starting balance that stamps the flag without ever granting it, and
// ensureBalanceInitialized then skips them forever — one shift permanently
// forfeited the starting balance.

func TestGrantStartingBalanceIfUnset(t *testing.T) {
	t.Run("credits the start, gated on not being initialized", func(t *testing.T) {
		community := economyCommunity(true, 1000000)
		civID := primitive.NewObjectID()

		mockCivDB := &mocks.CivilianDatabase{}
		mockCommDB := &mocks.CommunityDatabase{}
		mockCommDB.On("FindOne", mock.Anything, bson.M{"_id": community.ID}).Return(community, nil)

		var filter, update bson.M
		mockCivDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				filter = args.Get(1).(bson.M)
				update = args.Get(2).(bson.M)
			}).Return(nil)

		e := Economy{CivDB: mockCivDB, CommDB: mockCommDB}
		e.grantStartingBalanceIfUnset(context.Background(), civID, community.ID.Hex())

		// The condition must live in the filter so the write is a no-op for an
		// already-initialized civilian, and safe when two payouts race.
		assert.Equal(t, civID, filter["_id"])
		assert.Equal(t, bson.M{"$ne": true}, filter["civilian.balanceInitialized"])

		// $inc, not $set: a $set would clobber a credit applied in the same breath.
		assert.Equal(t, bson.M{"civilian.balance": int64(1000000)}, update["$inc"])
		assert.Equal(t, bson.M{"civilian.balanceInitialized": true}, update["$set"])
	})

	t.Run("writes nothing when economy is off", func(t *testing.T) {
		community := economyCommunity(false, 1000000)
		mockCivDB := &mocks.CivilianDatabase{}
		mockCommDB := &mocks.CommunityDatabase{}
		mockCommDB.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)

		e := Economy{CivDB: mockCivDB, CommDB: mockCommDB}
		e.grantStartingBalanceIfUnset(context.Background(), primitive.NewObjectID(), community.ID.Hex())

		mockCivDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("writes nothing when the configured start is zero", func(t *testing.T) {
		community := economyCommunity(true, 0)
		mockCivDB := &mocks.CivilianDatabase{}
		mockCommDB := &mocks.CommunityDatabase{}
		mockCommDB.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)

		e := Economy{CivDB: mockCivDB, CommDB: mockCommDB}
		e.grantStartingBalanceIfUnset(context.Background(), primitive.NewObjectID(), community.ID.Hex())

		mockCivDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("writes nothing without a usable community", func(t *testing.T) {
		mockCivDB := &mocks.CivilianDatabase{}
		mockCommDB := &mocks.CommunityDatabase{}

		e := Economy{CivDB: mockCivDB, CommDB: mockCommDB}
		e.grantStartingBalanceIfUnset(context.Background(), primitive.NewObjectID(), "")
		e.grantStartingBalanceIfUnset(context.Background(), primitive.NewObjectID(), "not-an-object-id")

		mockCivDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
		mockCommDB.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
	})
}
