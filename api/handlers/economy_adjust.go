package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// adjustBalanceMaxCents bounds a single admin adjustment. It shares the peer
// transfer ceiling for the same reason: the point of a cap is to stop a mistyped
// amount from minting a fortune, not to limit legitimate roleplay.
const adjustBalanceMaxCents = TransferCeilingCents

// adjustReasonMaxChars keeps the reason short enough to render inline in the
// civilian's transaction history.
const adjustReasonMaxChars = 280

// resolveAdjustActor returns who is making a balance adjustment.
//
// Creating money is the one economy action where a forged identity is directly
// profitable, so it does NOT fall back to the query-string userId the way
// resolveActorFromRequest does. That fallback is trusted only when the request
// also carries the server-to-server shared secret. Only the website backend holds
// that secret, and it fills userId in from the logged-in session, so a browser —
// which never sees the secret — cannot claim to be the community owner.
//
// The mobile app sends a real bearer token, which api.Middleware has already
// validated into the request context, so it passes the first check.
func resolveAdjustActor(r *http.Request) string {
	if id := api.GetAuthenticatedUserIDFromContext(r.Context()); id != "" {
		return id
	}
	if hasValidGatewaySecret(r) {
		return strings.TrimSpace(r.URL.Query().Get("userId"))
	}
	return ""
}

// hasValidGatewaySecret reports whether the request carries the configured
// shared secret. It fails closed: with no secret configured there is nothing to
// match, so nothing is trusted. Comparing two empty strings would otherwise
// "match" and hand the forgeable path straight back.
func hasValidGatewaySecret(r *http.Request) bool {
	key := os.Getenv(apiGatewayKeyEnv)
	if key == "" {
		return false
	}
	provided := r.Header.Get(apiGatewayHeader)
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(key)) == 1
}

// validateAdjustment checks the amount and reason of an admin adjustment.
// A signed amount: positive adds money, negative removes it.
func validateAdjustment(amountCents int64, reason string) error {
	if amountCents == 0 {
		return errors.New("enter an amount to add or remove")
	}
	if amountCents > adjustBalanceMaxCents || amountCents < -adjustBalanceMaxCents {
		return fmt.Errorf("the most you can adjust in one go is %s", formatCents(adjustBalanceMaxCents))
	}
	r := strings.TrimSpace(reason)
	if r == "" {
		return errors.New("a reason is required, so the change is traceable later")
	}
	if utf8.RuneCountInString(r) > adjustReasonMaxChars {
		return fmt.Errorf("the reason can be at most %d characters", adjustReasonMaxChars)
	}
	return nil
}

// adjustmentLedgerItem builds the paid inbox entry that records an admin
// adjustment, so it appears in the civilian's transaction history alongside
// fines, pay and transfers.
//
// Amount follows the existing inbox convention: positive is money owed (a
// debit), negative is a credit. Adding money is a credit, so its Amount is the
// negation of the adjustment.
func adjustmentLedgerItem(civ *models.Civilian, communityID, actorID, reason string, amountCents, balanceAfter int64, now primitive.DateTime) models.InboxItem {
	title := "Money added by an admin"
	if amountCents < 0 {
		title = "Money removed by an admin"
	}
	return models.InboxItem{
		ID:           primitive.NewObjectID(),
		CommunityID:  communityID,
		UserID:       civ.Details.UserID,
		CivilianID:   civ.ID.Hex(),
		Type:         "adjustment",
		Source:       "admin",
		Title:        title,
		Memo:         reason,
		Amount:       -amountCents,
		Status:       "paid",
		IssuedBy:     actorID,
		PaidAt:       now,
		BalanceAfter: balanceAfter,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

type adjustBalanceRequest struct {
	AmountCents int64  `json:"amountCents"`
	Reason      string `json:"reason"`
}

// AdjustCivilianBalanceHandler lets a community owner or administrator add money
// to, or remove money from, a civilian's wallet.
//
//	POST /api/v2/economy/civilian/{civilianId}/adjust
//	body: { amountCents (signed cents), reason }
//
// Every adjustment writes a paid ledger entry and an audit log record, so an
// owner can always see who changed a balance, by how much, and why.
func (e Economy) AdjustCivilianBalanceHandler(w http.ResponseWriter, r *http.Request) {
	actorID := resolveAdjustActor(r)
	if actorID == "" {
		config.ErrorStatus("sign in again to adjust balances", http.StatusUnauthorized, w, nil)
		return
	}

	civIDHex := mux.Vars(r)["civilianId"]
	civID, err := primitive.ObjectIDFromHex(civIDHex)
	if err != nil {
		config.ErrorStatus("invalid civilian id", http.StatusBadRequest, w, err)
		return
	}

	var req adjustBalanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if verr := validateAdjustment(req.AmountCents, req.Reason); verr != nil {
		config.ErrorStatus(verr.Error(), http.StatusBadRequest, w, nil)
		return
	}
	reason := strings.TrimSpace(req.Reason)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	civ, err := e.CivDB.FindOne(ctx, bson.M{"_id": civID})
	if err != nil || civ == nil {
		config.ErrorStatus("civilian not found", http.StatusNotFound, w, err)
		return
	}

	communityIDHex := strings.TrimSpace(civ.Details.ActiveCommunityID)
	commID, err := primitive.ObjectIDFromHex(communityIDHex)
	if err != nil {
		config.ErrorStatus("this civilian is not in a community", http.StatusBadRequest, w, err)
		return
	}
	community, err := e.CommDB.FindOne(ctx, bson.M{"_id": commID})
	if err != nil || community == nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	// Owner or administrator only. Passing no extra permission names means only
	// the owner and the administrator permission qualify — "manage members" can
	// see civilians but must not be able to create money.
	if !userHasCommunityPermission(community, actorID) {
		config.ErrorStatus("only the community owner or an administrator can adjust balances", http.StatusForbidden, w, nil)
		return
	}
	if !community.Details.Economy.Enabled {
		config.ErrorStatus("economy is disabled for this community", http.StatusForbidden, w, nil)
		return
	}

	// Grant the starting balance first if this civilian never received one, so
	// the adjustment lands on top of it rather than the later lazy backfill
	// overwriting the adjusted amount.
	e.grantStartingBalanceIfUnset(ctx, civID, communityIDHex)

	now := primitive.NewDateTimeFromTime(time.Now())
	filter := bson.M{"_id": civID}
	// Removing money cannot take a civilian below zero unless the community has
	// chosen to allow negative balances. The condition lives in the filter so the
	// check and the write are one atomic operation.
	if req.AmountCents < 0 && !community.Details.Economy.AllowNegativeBalance {
		filter["civilian.balance"] = bson.M{"$gte": -req.AmountCents}
	}

	res := e.CivDB.FindOneAndUpdate(ctx, filter,
		bson.M{
			"$inc": bson.M{"civilian.balance": req.AmountCents},
			"$set": bson.M{"civilian.balanceInitialized": true, "civilian.updatedAt": now},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)
	if res.Err() != nil {
		if errors.Is(res.Err(), mongo.ErrNoDocuments) && req.AmountCents < 0 {
			config.ErrorStatus("that would take this civilian below zero, and this community does not allow negative balances", http.StatusConflict, w, nil)
			return
		}
		config.ErrorStatus("failed to adjust balance", http.StatusInternalServerError, w, res.Err())
		return
	}

	var updated models.Civilian
	if err := res.Decode(&updated); err != nil {
		config.ErrorStatus("balance adjusted but could not be read back", http.StatusInternalServerError, w, err)
		return
	}
	balanceAfter := updated.Details.Balance

	// The balance has already moved. If the ledger write fails, log loudly rather
	// than fail the request: an error here would invite a retry that applies the
	// adjustment twice.
	item := adjustmentLedgerItem(&updated, communityIDHex, actorID, reason, req.AmountCents, balanceAfter, now)
	if e.IDB != nil {
		if _, ierr := e.IDB.InsertOne(ctx, item); ierr != nil {
			zap.S().Errorw("balance adjusted but ledger entry failed",
				"civilianId", civIDHex, "actorId", actorID, "amountCents", req.AmountCents, "error", ierr)
		}
	}

	if e.ALDB != nil {
		logAudit(e.ALDB, commID, "economy.balance_adjusted", "economy",
			actorID, resolveActorName(e.UDB, actorID),
			civIDHex, civilianDisplayName(&updated),
			map[string]interface{}{
				"amountCents":  req.AmountCents,
				"balanceAfter": balanceAfter,
				"reason":       reason,
			})
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"civilianId":  civIDHex,
		"amountCents": req.AmountCents,
		"balance":     balanceAfter,
	})
}
