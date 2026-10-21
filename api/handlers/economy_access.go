package handlers

import (
	"context"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"
)

// civilianEconomyViewPermission is the community permission that lets someone
// other than the civilian's own player see that civilian's wallet and inbox.
// It matches the permission that opens the community's civilians panel, which
// shows the balance; the owner and administrators always pass.
const civilianEconomyViewPermission = "manage members"

// canViewCivilianEconomy reports whether actorID may read civ's wallet and
// inbox: the player who owns the civilian, or someone who manages members of
// the community the civilian belongs to. community may be nil when the
// civilian is not in a community, in which case only the owner passes.
func canViewCivilianEconomy(civ *models.Civilian, community *models.Community, actorID string) bool {
	if civ == nil || actorID == "" {
		return false
	}
	if civ.Details.UserID == actorID {
		return true
	}
	if community == nil || community.ID.Hex() != strings.TrimSpace(civ.Details.ActiveCommunityID) {
		return false
	}
	return userHasCommunityPermission(community, actorID, civilianEconomyViewPermission)
}

// ownsCivilianInCommunity reports whether civ is userID's civilian in
// communityID. Used where a user points their own economy at a civilian, so
// only the owner qualifies.
func ownsCivilianInCommunity(civ *models.Civilian, userID, communityID string) bool {
	if civ == nil || userID == "" || communityID == "" {
		return false
	}
	return civ.Details.UserID == userID &&
		strings.TrimSpace(civ.Details.ActiveCommunityID) == strings.TrimSpace(communityID)
}

// civilianCommunity loads the community a civilian belongs to, or nil when it
// has none or the lookup fails.
func (e Economy) civilianCommunity(ctx context.Context, civ *models.Civilian) *models.Community {
	if civ == nil || e.CommDB == nil {
		return nil
	}
	commID, err := primitive.ObjectIDFromHex(strings.TrimSpace(civ.Details.ActiveCommunityID))
	if err != nil {
		return nil
	}
	community, err := e.CommDB.FindOne(ctx, bson.M{"_id": commID})
	if err != nil {
		return nil
	}
	return community
}
