package scheduler

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
)

// hardDeleteCommunityWithCascade runs the full community hard-delete pipeline:
// removes the community document, pulls user.communities references, and
// cascades deletion across all child collections.
//
// Mirrors the cascade that DeleteCommunityByIDHandler used to perform before
// the soft-delete refactor. Best-effort: logs errors per collection but keeps
// going, so a single failed collection does not block the rest.
func (s *Scheduler) hardDeleteCommunityWithCascade(ctx context.Context, communityID string, cID primitive.ObjectID) {
	if err := s.CDB.DeleteOne(ctx, bson.M{"_id": cID}); err != nil {
		zap.S().Errorw("hard delete: failed to delete community document",
			"communityId", communityID, "error", err)
		return
	}

	if _, err := s.UDB.UpdateMany(ctx,
		bson.M{"user.communities.communityId": communityID},
		bson.M{"$pull": bson.M{"user.communities": bson.M{"communityId": communityID}}},
	); err != nil {
		zap.S().Errorw("hard delete: failed to remove community refs from users",
			"communityId", communityID, "error", err)
	}

	type collectionCleanup struct {
		name   string
		filter bson.M
	}

	cleanups := []collectionCleanup{
		{"civilians", bson.M{"civilian.activeCommunityID": communityID}},
		{"vehicles", bson.M{"vehicle.activeCommunityID": communityID}},
		{"firearms", bson.M{"firearm.activeCommunityID": communityID}},
		{"licenses", bson.M{"license.activeCommunityID": communityID}},
		{"warrants", bson.M{"warrant.activeCommunityID": communityID}},
		{"ems", bson.M{"ems.activeCommunityID": communityID}},
		{"emsvehicles", bson.M{"vehicle.activeCommunityID": communityID}},
		{"medicalreports", bson.M{"report.activeCommunityID": communityID}},
		{"medications", bson.M{"medication.activeCommunityID": communityID}},
		{"calls", bson.M{"call.communityID": communityID}},
		{"bolos", bson.M{"bolo.communityID": communityID}},
		{"courtcases", bson.M{"courtCase.communityID": communityID}},
		{"courtsessions", bson.M{"courtSession.communityID": communityID}},
		{"most_wanted_entries", bson.M{"mostWanted.communityID": communityID}},
		{"inviteCodes", bson.M{"communityId": communityID}},
		{"tone_logs", bson.M{"communityId": communityID}},
		{"audit_logs", bson.M{"communityId": cID}},
		{"announcements", bson.M{"community": cID}},
	}

	for _, col := range cleanups {
		deleted, err := s.DBHelper.Collection(col.name).DeleteMany(ctx, col.filter)
		if err != nil {
			zap.S().Errorw("hard delete: cascade delete failed",
				"collection", col.name, "communityId", communityID, "error", err)
			continue
		}
		if deleted > 0 {
			zap.S().Infow("hard delete: cascade deleted documents",
				"collection", col.name, "communityId", communityID, "count", deleted)
		}
	}
}
