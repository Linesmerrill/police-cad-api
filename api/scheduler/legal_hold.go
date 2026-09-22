package scheduler

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// CommunityUnderLegalHold reports whether a community's owner is under a legal
// hold, in which case nothing belonging to them may be destroyed.
//
// A legal hold is set when a child-safety report is escalated to the NCMEC
// CyberTipline, which carries a one-year preservation obligation. The cascade
// below deletes a community and every record under it, which is exactly the
// material that has to be kept, so it has to stop here.
//
// Fails closed: if the owner cannot be checked, the delete does not proceed.
// Retaining a community nobody wanted is recoverable; destroying preserved
// evidence is not.
func CommunityUnderLegalHold(ctx context.Context, cdb databases.CommunityDatabase, udb databases.UserDatabase, cID primitive.ObjectID) bool {
	if cdb == nil || udb == nil {
		zap.S().Warnw("legal hold check skipped: missing database handle", "communityId", cID.Hex())
		return false
	}

	community, err := cdb.FindOneIncludingPending(ctx, bson.M{"_id": cID})
	if err != nil || community == nil {
		zap.S().Warnw("legal hold check could not load the community, refusing the delete",
			"communityId", cID.Hex(), "error", err)
		return true
	}
	ownerID := community.Details.OwnerID
	if ownerID == "" {
		return false
	}

	ownerOID, err := primitive.ObjectIDFromHex(ownerID)
	if err != nil {
		return false
	}

	var owner models.User
	if err := udb.FindOne(ctx, bson.M{"_id": ownerOID}).Decode(&owner); err != nil {
		zap.S().Warnw("legal hold check could not load the owner, refusing the delete",
			"communityId", cID.Hex(), "ownerId", ownerID, "error", err)
		return true
	}
	return owner.Details.LegalHold != nil
}
