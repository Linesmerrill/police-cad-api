package handlers

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Bringing V1 members back into their community.
//
// In V1 a player belonged to one community at a time, recorded only as
// user.activeCommunity, and joining was entering the community's code. Today
// membership is read from user.communities, which V1 never wrote, so about
// 348,000 players point at a community that treats them as a stranger. An
// owner who comes back (see owner_membership_heal.go) finds their community
// empty and their players asked to request to join all over again.
//
// So when a player signs in, a V1 community with no entry for them becomes an
// approved membership, because in V1 knowing the code was the approval. This
// is the only thing that ever grants one, and it runs at most once per player
// per community. It stays out of the way of anything decided since:
//   - any existing entry wins, whatever its status. A pending request, a
//     decline or a ban in the current system is a newer decision than V1;
//   - someone on the community's ban list stays out;
//   - a community that has been deleted, or is pending deletion, is skipped.

// healV1Membership adds a V1 member's approved membership of their
// activeCommunity when they have none. It reports whether it changed anything.
func healV1Membership(ctx context.Context, udb databases.UserDatabase, cdb databases.CommunityDatabase, user *models.User) bool {
	if udb == nil || cdb == nil || user == nil {
		return false
	}
	communityID := user.Details.ActiveCommunity
	communityOID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		return false
	}
	for _, uc := range user.Details.Communities {
		if uc.CommunityID == communityID {
			return false
		}
	}
	userOID, err := primitive.ObjectIDFromHex(user.ID)
	if err != nil {
		return false
	}

	community, err := cdb.FindOne(ctx, bson.M{"_id": communityOID})
	if err != nil || community == nil {
		return false
	}
	for _, banned := range community.Details.BanList {
		if banned == user.ID {
			return false
		}
	}
	return ensureCommunityMembership(ctx, udb, userOID, communityID, user)
}

// NewV1MemberHealer returns the hook the login path runs after a successful
// sign-in. It lives here rather than in the api package, which cannot reach
// the community collection. Failures are logged and never block a login.
func NewV1MemberHealer(udb databases.UserDatabase, cdb databases.CommunityDatabase) func(context.Context, *models.User) {
	return func(ctx context.Context, user *models.User) {
		if healV1Membership(ctx, udb, cdb, user) {
			zap.S().Infow("healed V1 membership",
				"user_id", user.ID, "community_id", user.Details.ActiveCommunity)
		}
	}
}
