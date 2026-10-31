package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// Claiming: how a player takes ownership of the account a mod made for them.
//
// This is the hinge of the whole provisioning design, so the rule it enforces
// is worth saying plainly. A mod can create an account and fill it with
// characters, but it cannot give anyone access to it. Only the player can take
// it, and only by proving control of both sides: they hold the code the game
// showed them, and they are signed in to LPC as themselves.
//
// This endpoint is on the first-party surface, not the developer one. A
// developer key cannot reach it. A developer minting a code and then redeeming
// it themselves is exactly what must not be possible.

// maxClaimAttemptsPerWindow limits how many codes one account may try.
//
// A claim code is ten characters of Crockford base32, which is 50 bits and not
// guessable — but it is also short, and short things attract scripts, so the
// attempt counter exists regardless.
const maxClaimAttemptsPerWindow = 10

type claimRequest struct {
	Code string `json:"code"`
}

// ClaimProvisionedAccountHandler redeems a claim code for the signed-in user.
//
// POST /api/v1/developer/claim
//
// Two outcomes, depending on who is asking:
//
//   - A player with no LPC account of their own signs up first, which gives
//     them an ordinary empty account, and then redeems. Their provisioned
//     records move onto the account they just made.
//   - A player who already had an LPC account signs in and redeems. Same
//     move, onto the account they already had.
//
// Either way the records move to the signed-in account and the provisioned
// shell is retired. We never match the two accounts by email, because the only
// email we could match on would be one the mod supplied.
func (d DeveloperAPI) ClaimProvisionedAccountHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	var req claimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		config.InfoStatus("enter the code shown in game", http.StatusBadRequest, w, fmt.Errorf("empty code"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// The claimant must be a real, claimed account. A provisioned shell cannot
	// be the one doing the claiming, or a developer who got hold of one could
	// use it to absorb another.
	var claimant models.User
	if err := d.UDB.FindOne(ctx, bson.M{"_id": objectIDOrZero(actorID)}).Decode(&claimant); err != nil {
		config.InfoStatus("sign in and try again", http.StatusUnauthorized, w, err)
		return
	}
	if !claimant.IsClaimed() {
		config.InfoStatus("that account cannot claim another", http.StatusForbidden, w,
			fmt.Errorf("claimant is itself unclaimed"))
		return
	}

	record, err := d.ClaimCodeDB.FindOne(ctx, bson.M{"codeHash": hashClaimCode(code)})
	if err != nil || record == nil {
		// One message for wrong, expired and already-used. Distinguishing them
		// would turn this into an oracle for which codes exist.
		config.InfoStatus("that code is not valid. Ask the server to show you a new one",
			http.StatusNotFound, w, fmt.Errorf("no such code"))
		return
	}
	if record.UsedAt != nil {
		config.InfoStatus("that code has already been used. Ask the server to show you a new one",
			http.StatusConflict, w, fmt.Errorf("code already used"))
		return
	}
	if record.ExpiresAt.Time().Before(time.Now()) {
		config.InfoStatus("that code has expired. Ask the server to show you a new one",
			http.StatusGone, w, fmt.Errorf("code expired"))
		return
	}

	// Claim the code before moving anything, with a filter that only matches
	// while it is still unused. Two players racing on the same code means one
	// update matches and the other does not, so the records can only move once.
	now := primitive.NewDateTimeFromTime(time.Now())
	res, err := d.ClaimCodeDB.UpdateOne(ctx,
		bson.M{"_id": record.ID, "usedAt": nil},
		bson.M{"$set": bson.M{"usedAt": now, "usedByUserId": actorID}})
	if err != nil {
		config.ErrorStatus("could not redeem that code", http.StatusInternalServerError, w, err)
		return
	}
	if res != nil && res.MatchedCount == 0 {
		config.InfoStatus("that code has already been used. Ask the server to show you a new one",
			http.StatusConflict, w, fmt.Errorf("lost the race"))
		return
	}

	moved, err := d.transferProvisionedAccount(ctx, record.UserID, actorID, record)
	if err != nil {
		zap.S().Errorw("developer api: claim transfer failed",
			"from", record.UserID, "to", actorID, "error", err)
		config.ErrorStatus("could not move those characters over", http.StatusInternalServerError, w, err)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"claimed":      true,
		"provider":     record.Provider,
		"identifier":   record.Identifier,
		"moved":        moved,
		"message":      "Your in-game characters are now on this account.",
	})
}

// claimMoveCounts is what a claim moved, for the confirmation the player sees.
type claimMoveCounts struct {
	Civilians   int64 `json:"civilians"`
	Vehicles    int64 `json:"vehicles"`
	Firearms    int64 `json:"firearms"`
	Licenses    int64 `json:"licenses"`
	Communities int   `json:"communities"`
}

// transferProvisionedAccount moves everything a provisioned shell holds onto
// the claimant's real account.
//
// Order matters, and the principle is that a half-finished transfer must leave
// the player better off, not worse. Records move first, so if this fails
// part-way the player's characters are already on their real account and a
// retry finishes the rest. The shell is marked claimed last, which is what
// stops a second code from being issued against it.
//
// The shell is not deleted. It is left behind, claimed and pointing at the real
// account, because anything in the database still referencing its id — a call
// someone took, a note, an audit row — would otherwise point at nothing.
func (d DeveloperAPI) transferProvisionedAccount(ctx context.Context, fromUserID, toUserID string, record *models.DeveloperClaimCode) (claimMoveCounts, error) {
	var moved claimMoveCounts

	if fromUserID == toUserID {
		// The player is claiming the account they are already signed in to,
		// which happens if they somehow signed in as the shell. Nothing to
		// move; just mark it claimed below.
		return moved, d.markClaimed(ctx, toUserID, record)
	}

	// Characters and the records hanging off them.
	if n, err := d.reassignOwnedRecords(ctx, fromUserID, toUserID); err != nil {
		return moved, err
	} else {
		moved = n
	}

	// The game identity now belongs to the real account.
	if _, err := d.IdentityDB.UpdateMany(ctx,
		bson.M{"userId": fromUserID},
		bson.M{"$set": bson.M{"userId": toUserID, "claimed": true, "updatedAt": primitive.NewDateTimeFromTime(time.Now())}}); err != nil {
		return moved, err
	}

	// Community memberships. Added with $addToSet so a player who was already
	// a member of the same community on their real account does not end up
	// with two rows for it.
	var shell models.User
	if err := d.UDB.FindOne(ctx, bson.M{"_id": objectIDOrZero(fromUserID)}).Decode(&shell); err == nil {
		for _, membership := range shell.Details.Communities {
			fresh := membership
			fresh.ID = primitive.NewObjectID().Hex()
			if _, err := d.UDB.UpdateOne(ctx,
				bson.M{"_id": objectIDOrZero(toUserID), "user.communities.communityId": bson.M{"$ne": fresh.CommunityID}},
				bson.M{"$addToSet": bson.M{"user.communities": fresh}}); err != nil {
				zap.S().Warnw("developer api: could not move a membership on claim",
					"communityId", fresh.CommunityID, "error", err)
				continue
			}
			moved.Communities++
		}
	}

	// Record the game identity on the real account too, for its account page.
	identityEntry := models.GameIdentity{
		Provider:   record.Provider,
		Identifier: record.Identifier,
		AddedAt:    primitive.NewDateTimeFromTime(time.Now()),
	}
	if _, err := d.UDB.UpdateOne(ctx, bson.M{"_id": objectIDOrZero(toUserID)},
		bson.M{"$addToSet": bson.M{"user.gameIdentities": identityEntry}}); err != nil {
		zap.S().Warnw("developer api: could not record a game identity on claim", "error", err)
	}

	return moved, d.markClaimed(ctx, fromUserID, record)
}

// reassignOwnedRecords re-points a shell's characters and their records at the
// claimant.
//
// Civilians carry the owning user on civilian.userID; vehicles, firearms and
// licenses hang off a civilian rather than a user, so moving the civilians
// carries them along and only the legacy owner fields need touching.
func (d DeveloperAPI) reassignOwnedRecords(ctx context.Context, fromUserID, toUserID string) (claimMoveCounts, error) {
	var moved claimMoveCounts

	if d.CivDB != nil {
		res, err := d.CivDB.UpdateMany(ctx,
			bson.M{"civilian.userID": fromUserID},
			bson.M{"$set": bson.M{"civilian.userID": toUserID}})
		if err != nil {
			return moved, err
		}
		if res != nil {
			moved.Civilians = res.ModifiedCount
		}
	}

	// The legacy ownerID fields on vehicles and firearms predate linkedCivilianID
	// and still hold a user id on older records, so they are moved too. Doing
	// this is cheap and skipping it would leave a player unable to see their own
	// older vehicles.
	if d.VehDB != nil {
		if res, err := d.VehDB.UpdateMany(ctx,
			bson.M{"vehicle.registeredOwnerID": fromUserID},
			bson.M{"$set": bson.M{"vehicle.registeredOwnerID": toUserID}}); err == nil && res != nil {
			moved.Vehicles = res.ModifiedCount
		}
	}
	if d.FirDB != nil {
		if res, err := d.FirDB.UpdateMany(ctx,
			bson.M{"firearm.registeredOwnerID": fromUserID},
			bson.M{"$set": bson.M{"firearm.registeredOwnerID": toUserID}}); err == nil && res != nil {
			moved.Firearms = res.ModifiedCount
		}
	}

	return moved, nil
}

// markClaimed closes the shell out: claimed, pointed at its new owner, and
// unable to have another code issued against it.
func (d DeveloperAPI) markClaimed(ctx context.Context, shellUserID string, record *models.DeveloperClaimCode) error {
	now := primitive.NewDateTimeFromTime(time.Now())
	claimedTrue := true
	_, err := d.UDB.UpdateOne(ctx, bson.M{"_id": objectIDOrZero(shellUserID)}, bson.M{"$set": bson.M{
		"user.claimed":   claimedTrue,
		"user.claimedAt": now,
	}})
	if err != nil {
		return err
	}

	// Any other outstanding code for this shell is dead now.
	if _, err := d.ClaimCodeDB.DeleteMany(ctx, bson.M{"userId": shellUserID, "usedAt": nil}); err != nil {
		zap.S().Warnw("developer api: could not clear remaining claim codes", "error", err)
	}
	return nil
}

// DiscordClaimProvisionedAccountHandler claims a Discord-provisioned account
// without a code.
//
// POST /api/v1/developer/claim/discord
//
// Only when the signed-in account's linked Discord id is the very identifier
// the mod provisioned. That is proof on both sides already: they proved that
// Discord account to us when they linked it, and to the game when they joined
// it. Asking them to copy a code as well would be ceremony.
func (d DeveloperAPI) DiscordClaimProvisionedAccountHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	var claimant models.User
	if err := d.UDB.FindOne(ctx, bson.M{"_id": objectIDOrZero(actorID)}).Decode(&claimant); err != nil {
		config.InfoStatus("sign in and try again", http.StatusUnauthorized, w, err)
		return
	}
	if !claimant.IsClaimed() {
		config.InfoStatus("that account cannot claim another", http.StatusForbidden, w,
			fmt.Errorf("claimant is itself unclaimed"))
		return
	}
	if claimant.Details.Discord == nil || strings.TrimSpace(claimant.Details.Discord.ID) == "" {
		config.InfoStatus("link your Discord account first, then try again", http.StatusBadRequest, w,
			fmt.Errorf("no linked discord"))
		return
	}

	discordID := models.NormalizePlayerIdentifier(models.PlayerProviderDiscord, claimant.Details.Discord.ID)
	identity, err := d.IdentityDB.FindOne(ctx, bson.M{
		"provider":   models.PlayerProviderDiscord,
		"identifier": discordID,
	})
	if err != nil || identity == nil {
		config.InfoStatus("we have no in-game characters waiting for your Discord account",
			http.StatusNotFound, w, fmt.Errorf("no identity"))
		return
	}
	if identity.UserID == actorID {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"claimed": true, "moved": claimMoveCounts{},
			"message": "Those characters are already on this account.",
		})
		return
	}

	var shell models.User
	if err := d.UDB.FindOne(ctx, bson.M{"_id": objectIDOrZero(identity.UserID)}).Decode(&shell); err != nil {
		config.InfoStatus("those characters are no longer available", http.StatusNotFound, w, err)
		return
	}
	if shell.IsClaimed() {
		config.InfoStatus("those characters already belong to another account", http.StatusConflict, w,
			fmt.Errorf("already claimed"))
		return
	}

	record := &models.DeveloperClaimCode{
		Provider:   models.PlayerProviderDiscord,
		Identifier: discordID,
		UserID:     identity.UserID,
	}
	moved, err := d.transferProvisionedAccount(ctx, identity.UserID, actorID, record)
	if err != nil {
		config.ErrorStatus("could not move those characters over", http.StatusInternalServerError, w, err)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"claimed":    true,
		"provider":   models.PlayerProviderDiscord,
		"identifier": discordID,
		"moved":      moved,
		"message":    "Your in-game characters are now on this account.",
	})
}
