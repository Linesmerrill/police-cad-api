package handlers

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Owners locked out of their own community.
//
// Membership is read from the user's `user.communities` array. Communities
// created before 2025 recorded their owner only in the community's own
// `members` map, which nothing reads any more, so their owners have no entry
// in that array. Every surface then treats them as strangers to their own
// community: the app offers "Request to Join", member counts read zero, and
// department access fails. At the time of writing this covered about 148,000
// communities, essentially all of those created before 2025.
//
// Rather than migrate them all at once, the owner is made an approved member
// the first time their community is loaded, by anyone. It does not depend on
// who is asking, so a forged or missing user id cannot aim it anywhere but at
// the community's real owner.

// healOwnerMembership makes a community's owner an approved member of it when
// they are not one already. It reports whether it changed anything.
func healOwnerMembership(ctx context.Context, udb databases.UserDatabase, cdb databases.CommunityDatabase, community *models.Community) bool {
	if udb == nil || community == nil {
		return false
	}
	ownerID, err := primitive.ObjectIDFromHex(community.Details.OwnerID)
	if err != nil {
		// No owner, or a malformed one: nothing to heal. The orphan audit
		// (scripts/audit_orphan_communities.js) covers those.
		return false
	}

	var owner models.User
	if err := udb.FindOne(ctx, bson.M{"_id": ownerID}).Decode(&owner); err != nil {
		// The owner's account is gone. That is an orphaned community, not a
		// locked-out owner.
		return false
	}
	return ensureCommunityMembership(ctx, udb, cdb, ownerID, community.ID.Hex(), &owner)
}
