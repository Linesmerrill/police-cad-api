package handlers

import (
	"context"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/models"
)

// rpPromoRemovedForDeletion is the reason recorded on promotions taken down
// because their community was deleted. It is what staff see in the Server
// Promos panel next to the post.
const rpPromoRemovedForDeletion = "community deleted"

// removeLiveRpPromotions takes every promotion a community still has live in
// the shared rp-servers channel down, and marks each one removed.
//
// A promotion advertises a community someone can join. Once the owner deletes
// the community, the post points at something that is going away, and often at
// a Discord invite they have already replaced, so it is no longer an ad for
// anything real. Left up, it also keeps counting as a live post, which is how a
// deleted community and the one an owner rebuilt in its place got flagged as a
// duplicate pair in the moderation console.
//
// Best effort, by design. Each Discord deletion that fails is logged and its
// entry left as live, so the console still shows it for a person to remove; it
// never fails the caller, because a promotion that could not be taken down is
// no reason to refuse the deletion the owner asked for. A post with no Discord
// message ID was never confirmed live in the channel, so it is simply marked.
//
// Returns how many promotions were taken down.
func (c Community) removeLiveRpPromotions(ctx context.Context, community *models.Community, removedBy, reason string) int {
	if community == nil || community.Details.RpPromotion == nil {
		return 0
	}

	webhookURL := os.Getenv(rpPromoWebhookEnv)
	var removedIDs []string
	var serverNames []string
	for _, post := range community.Details.RpPromotion.History {
		if post.RemovedAt != nil || post.ID == "" {
			continue
		}
		if post.MessageID != "" {
			if err := deleteRpPromotionWebhookMessage(webhookURL, post.MessageID); err != nil {
				zap.S().Warnw("rp promo cleanup: could not take a deleted community's promotion down",
					"communityId", community.ID.Hex(), "postId", post.ID, "error", err)
				continue
			}
		}
		removedIDs = append(removedIDs, post.ID)
		serverNames = append(serverNames, post.Data.ServerName)
	}
	if len(removedIDs) == 0 {
		return 0
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	update := bson.M{"$set": bson.M{
		"community.rpPromotion.history.$[elem].removedAt":    now,
		"community.rpPromotion.history.$[elem].removedBy":    removedBy,
		"community.rpPromotion.history.$[elem].removeReason": reason,
	}}
	opts := options.Update().SetArrayFilters(options.ArrayFilters{
		Filters: []interface{}{bson.M{"elem.id": bson.M{"$in": removedIDs}}},
	})
	if err := c.DB.UpdateOne(ctx, bson.M{"_id": community.ID}, update, opts); err != nil {
		// The posts are already gone from Discord. The console will show them
		// as live until a person removes them there, which is harmless.
		zap.S().Warnw("rp promo cleanup: promotions taken down but not marked removed",
			"communityId", community.ID.Hex(), "posts", removedIDs, "error", err)
	}

	logAudit(c.ALDB, community.ID, "rp_promotion.removed", "community", "", removedBy, "", "",
		map[string]interface{}{"reason": reason, "posts": removedIDs, "serverNames": serverNames})
	return len(removedIDs)
}
