package handlers

import (
	"errors"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// inviteCodeManagePermission is the permission that governs a community's
// invite codes.
//
// An invite code is a way into the community, so handing one out, listing the
// outstanding ones and revoking them are all forms of deciding who is a member:
// only someone who can manage members may do it. The owner and anyone holding
// "administrator" pass automatically inside userHasCommunityPermission.
const inviteCodeManagePermission = "manage members"

// authorizeInviteCodeAction checks the caller may manage this community's invite
// codes, and returns who they are.
//
// Identity is resolved with the hardened rule the destructive community actions
// already use (resolveAdjustActor): a bearer token the middleware has verified,
// or the query-string userId only when the request also carries the
// server-to-server secret that the website backend holds and a browser never
// sees. A plain caller-supplied id is not an identity, so believing one would
// leave the permission check deciding nothing.
//
// On failure it writes the response (401 when the caller cannot be identified,
// 403 when they lack the permission) and returns ok=false.
func authorizeInviteCodeAction(w http.ResponseWriter, r *http.Request, community *models.Community) (string, bool) {
	return authorizeBulkCommunityAction(w, r, community, inviteCodeManagePermission)
}

// loadCommunityForInviteCode loads the community an invite-code request is about,
// so it can be authorized against. On failure it writes the response and returns
// nil.
func (c Community) loadCommunityForInviteCode(w http.ResponseWriter, r *http.Request, communityID string) (*models.Community, primitive.ObjectID) {
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return nil, cID
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			config.InfoStatus("community not found", http.StatusNotFound, w, err)
		} else {
			config.ErrorStatus("failed to fetch community", http.StatusInternalServerError, w, err)
		}
		return nil, cID
	}
	return community, cID
}

// inviteCodeExpired reports whether an invite code's expiry has passed.
func inviteCodeExpired(invite *models.InviteCode, now time.Time) bool {
	if invite == nil || invite.ExpiresAt == nil {
		return false
	}
	return invite.ExpiresAt.Before(now)
}

// inviteCodeHasUsesLeft reports whether an invite code can still be redeemed.
//
// It mirrors, in Go, exactly what the join query accepts, so a preview and a
// join agree about the same code. Two generations of code live in the
// collection: current ones track remainingUses (with -1 meaning unlimited),
// while older ones left remainingUses unset and counted upwards in uses
// against maxUses (where maxUses 0 meant unlimited).
func inviteCodeHasUsesLeft(invite *models.InviteCode) bool {
	if invite == nil {
		return false
	}
	if invite.RemainingUses == -1 || invite.RemainingUses > 0 {
		return true
	}
	if invite.MaxUses == 0 {
		return true
	}
	return invite.Uses < invite.MaxUses
}

// inviteCodeRemainingUses is how many redemptions an invite code has left, with
// -1 meaning unlimited.
//
// It reads both generations of counter the same way inviteCodeHasUsesLeft does,
// so a code the join query would accept never reports zero left.
func inviteCodeRemainingUses(invite *models.InviteCode) int {
	if invite == nil {
		return 0
	}
	if invite.RemainingUses == -1 || invite.MaxUses == 0 {
		return -1
	}
	if invite.RemainingUses > 0 {
		return invite.RemainingUses
	}
	// An older code counted upwards in uses instead of downwards.
	if left := invite.MaxUses - invite.Uses; left > 0 {
		return left
	}
	return 0
}
