package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/models"
)

// Adjusting a balance creates money from nothing, so a forged identity is
// directly profitable. The rest of the economy trusts ?userId= when there is no
// bearer token, and a community's ownerID is visible to anyone through the
// public search endpoint. These tests pin the one thing that must hold: a
// query-string userId is believed only alongside the server-to-server secret.

func adjustRequest(query string, secret string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v2/economy/civilian/x/adjust"+query, nil)
	if secret != "" {
		req.Header.Set(apiGatewayHeader, secret)
	}
	return req
}

func TestResolveAdjustActor(t *testing.T) {
	t.Run("a forged userId with no secret is not believed", func(t *testing.T) {
		t.Setenv(apiGatewayKeyEnv, "s3cret")
		// The attack: someone reads the owner id off the public search endpoint
		// and sends it as if they were the owner.
		assert.Equal(t, "", resolveAdjustActor(adjustRequest("?userId=ownerid", "")))
	})

	t.Run("a wrong secret is not believed", func(t *testing.T) {
		t.Setenv(apiGatewayKeyEnv, "s3cret")
		assert.Equal(t, "", resolveAdjustActor(adjustRequest("?userId=ownerid", "guess")))
	})

	t.Run("the website backend's real secret is believed", func(t *testing.T) {
		t.Setenv(apiGatewayKeyEnv, "s3cret")
		assert.Equal(t, "ownerid", resolveAdjustActor(adjustRequest("?userId=ownerid", "s3cret")))
	})

	t.Run("with no secret configured it fails closed", func(t *testing.T) {
		// Two empty strings compare equal. Without the explicit guard an unset
		// key would hand the forgeable path straight back.
		t.Setenv(apiGatewayKeyEnv, "")
		assert.Equal(t, "", resolveAdjustActor(adjustRequest("?userId=ownerid", "")))
	})

	t.Run("no identity at all gives no actor", func(t *testing.T) {
		t.Setenv(apiGatewayKeyEnv, "s3cret")
		assert.Equal(t, "", resolveAdjustActor(adjustRequest("", "")))
	})

	t.Run("a secret with no userId gives no actor", func(t *testing.T) {
		t.Setenv(apiGatewayKeyEnv, "s3cret")
		assert.Equal(t, "", resolveAdjustActor(adjustRequest("", "s3cret")))
	})
}

func TestAdjustCivilianBalanceHandlerRejectsUnauthenticated(t *testing.T) {
	t.Setenv(apiGatewayKeyEnv, "s3cret")

	// No databases wired: the request must be refused before any of them are
	// touched. A nil DB here would panic if the auth gate were bypassed.
	handler := Economy{}

	body, _ := json.Marshal(map[string]interface{}{"amountCents": 100000000, "reason": "free money"})
	req := httptest.NewRequest(http.MethodPost,
		"/api/v2/economy/civilian/"+primitive.NewObjectID().Hex()+"/adjust?userId=ownerid",
		bytes.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"civilianId": primitive.NewObjectID().Hex()})
	rec := httptest.NewRecorder()

	handler.AdjustCivilianBalanceHandler(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestValidateAdjustment(t *testing.T) {
	assert.NoError(t, validateAdjustment(50000, "Payout for a property sale"))
	assert.NoError(t, validateAdjustment(-50000, "Refund reversal"))

	assert.Error(t, validateAdjustment(0, "nothing"), "zero changes nothing")
	assert.Error(t, validateAdjustment(50000, ""), "a reason is required")
	assert.Error(t, validateAdjustment(50000, "   "), "whitespace is not a reason")
	assert.Error(t, validateAdjustment(adjustBalanceMaxCents+1, "typo"), "over the ceiling")
	assert.Error(t, validateAdjustment(-adjustBalanceMaxCents-1, "typo"), "under the negative ceiling")

	long := make([]byte, adjustReasonMaxChars+1)
	for i := range long {
		long[i] = 'a'
	}
	assert.Error(t, validateAdjustment(50000, string(long)), "reason too long")
}

func TestAdjustmentLedgerItem(t *testing.T) {
	civ := &models.Civilian{ID: primitive.NewObjectID()}
	civ.Details.UserID = "civowner"
	now := primitive.NewDateTimeFromTime(primitive.NewObjectID().Timestamp())

	t.Run("adding money is recorded as a credit", func(t *testing.T) {
		item := adjustmentLedgerItem(civ, "comm1", "admin1", "sale payout", 50000, 150000, now)
		// Inbox convention: positive is owed, negative is a credit.
		assert.Equal(t, int64(-50000), item.Amount)
		assert.Equal(t, "Money added by an admin", item.Title)
	})

	t.Run("removing money is recorded as a debit", func(t *testing.T) {
		item := adjustmentLedgerItem(civ, "comm1", "admin1", "clawback", -50000, 100000, now)
		assert.Equal(t, int64(50000), item.Amount)
		assert.Equal(t, "Money removed by an admin", item.Title)
	})

	t.Run("it is traceable", func(t *testing.T) {
		item := adjustmentLedgerItem(civ, "comm1", "admin1", "sale payout", 50000, 150000, now)
		assert.Equal(t, "admin", item.Source)
		assert.Equal(t, "adjustment", item.Type)
		assert.Equal(t, "paid", item.Status)
		assert.Equal(t, "admin1", item.IssuedBy)
		assert.Equal(t, "sale payout", item.Memo)
		assert.Equal(t, int64(150000), item.BalanceAfter)
		assert.Equal(t, civ.ID.Hex(), item.CivilianID)
		assert.Equal(t, "civowner", item.UserID)
		assert.Equal(t, "comm1", item.CommunityID)
	})
}

// The mobile app authenticates with a real bearer token, which api.Middleware
// validates into the request context before the handler runs.
func TestResolveAdjustActorBearer(t *testing.T) {
	withBearer := func(req *http.Request, userID string) *http.Request {
		return req.WithContext(api.WithAuthenticatedUserID(req.Context(), userID))
	}

	t.Run("a validated bearer identity is used", func(t *testing.T) {
		t.Setenv(apiGatewayKeyEnv, "s3cret")
		assert.Equal(t, "mobileuser", resolveAdjustActor(withBearer(adjustRequest("", ""), "mobileuser")))
	})

	t.Run("the bearer identity wins over a query-string userId", func(t *testing.T) {
		// A signed-in user cannot relabel themselves by also sending ?userId=.
		t.Setenv(apiGatewayKeyEnv, "s3cret")
		assert.Equal(t, "mobileuser", resolveAdjustActor(withBearer(adjustRequest("?userId=ownerid", ""), "mobileuser")))
	})

	t.Run("the bearer path works with no secret configured", func(t *testing.T) {
		t.Setenv(apiGatewayKeyEnv, "")
		assert.Equal(t, "mobileuser", resolveAdjustActor(withBearer(adjustRequest("", ""), "mobileuser")))
	})
}
