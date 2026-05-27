package handlers

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/databases"
)

// liveMemberCounts returns the live approved-member count per community ID,
// computed from the users collection. The stored `community.membersCount` is
// a denormalized counter and drifts over time (e.g. user account deletions,
// historical ban paths that did not decrement). Mirrors the single-community
// self-heal in CommunityHandler. Returns an empty map on aggregation error
// so callers can fall back to whatever stored value they already have.
//
// Backed by the {user.communities.communityId, user.communities.status}
// compound index (scripts/create_indexes.js).
func liveMemberCounts(ctx context.Context, udb databases.UserDatabase, ids []string) map[string]int {
	counts := make(map[string]int, len(ids))
	if len(ids) == 0 {
		return counts
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"user.communities": bson.M{"$elemMatch": bson.M{
				"communityId": bson.M{"$in": ids},
				"status":      "approved",
			}},
		}}},
		{{Key: "$unwind", Value: "$user.communities"}},
		{{Key: "$match", Value: bson.M{
			"user.communities.communityId": bson.M{"$in": ids},
			"user.communities.status":      "approved",
		}}},
		{{Key: "$group", Value: bson.M{
			"_id":   "$user.communities.communityId",
			"count": bson.M{"$sum": 1},
		}}},
	}

	cursor, err := udb.Aggregate(ctx, pipeline)
	if err != nil {
		zap.S().Warnw("failed to compute live member counts; callers will fall back to stored values",
			"community_count", len(ids), "error", err)
		return counts
	}
	defer cursor.Close(ctx)

	var results []struct {
		ID    string `bson:"_id"`
		Count int    `bson:"count"`
	}
	if err := cursor.All(ctx, &results); err != nil {
		zap.S().Warnw("failed to decode live member counts; callers will fall back to stored values",
			"community_count", len(ids), "error", err)
		return counts
	}

	for _, r := range results {
		counts[r.ID] = r.Count
	}
	return counts
}
