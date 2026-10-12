package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

// Moving the free community promotion from one owned community to another.
//
// The program grants exactly one community boost per creator. Creators who run
// more than one community asked to be able to move it rather than open a ticket
// each time, so this exists — but it is capped, because every move is a free
// boost appearing and disappearing on the Discover ranking.
//
// The whole handler is written around one rule: a creator must never end up
// with two active community entitlements, and must never lose the one they had
// without gaining the new one. There are no transactions here (the deployment
// is a single-node Mongo), so instead every step that could race is a single
// guarded atomic write, and every step after the first failure compensates for
// the ones already done.
const (
	// ccDefaultTransferLimit is the free lifetime allowance. Shared with the
	// model so the $ifNull default in the consume filter and the default used
	// when reading a creator document can never drift apart.
	ccDefaultTransferLimit = models.DefaultCreatorTransferLimit

	// ccTransferRevokeReason marks the entitlement a transfer retired, so the
	// history distinguishes "moved it" from "removed from the program".
	ccTransferRevokeReason = "transferred"

	// ccTransferLimitReachedCode is the machine-readable code the dashboard
	// keys off to show the "contact support" state rather than a generic error.
	ccTransferLimitReachedCode = "transfer_limit_reached"
	ccTransferLimitReachedMsg  = "You've used all of your free transfers. Contact LPC support to request another."
)

// TransferCommunityPromotionHandler moves the creator's active community
// promotion to another community they own, spending one lifetime transfer.
// POST /api/v1/content-creators/me/community-promotion/transfer
func (cc ContentCreator) TransferCommunityPromotionHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	userIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	userObjID, err := primitive.ObjectIDFromHex(userIDStr)
	if err != nil {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, err)
		return
	}

	var req models.TransferCommunityPromotionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if req.CommunityID == "" {
		config.ErrorStatus("communityId is required", http.StatusBadRequest, w, nil)
		return
	}
	communityObjID, err := primitive.ObjectIDFromHex(req.CommunityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// 1. The creator must be in the program. Same statuses and same message as
	// applying the promotion in the first place.
	creator, err := cc.CCDB.FindOne(ctx, bson.M{
		"userId": userObjID,
		"status": bson.M{"$in": []string{"active", "approved", "warned"}},
	})
	if err != nil || creator == nil {
		config.ErrorStatus("you must be an active creator to use this benefit", http.StatusForbidden, w, err)
		return
	}

	// 2. The destination must exist and be theirs. Same rule as enrollment: a
	// creator can only boost a community they own.
	destination, err := cc.CDB.FindOne(ctx, bson.M{"_id": communityObjID})
	if err != nil || destination == nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}
	if destination.Details.OwnerID != userIDStr {
		config.ErrorStatus("you must be the owner of this community to apply the promotion", http.StatusForbidden, w, nil)
		return
	}

	// 3. Nothing to do, and nothing to charge them for.
	current, _ := cc.EntDB.FindOne(ctx, bson.M{
		"contentCreatorId": creator.ID,
		"targetType":       "community",
		"active":           true,
	})
	if current == nil {
		config.ErrorStatus("you do not have a community promotion to transfer", http.StatusConflict, w, nil)
		return
	}
	if current.TargetID == communityObjID {
		config.ErrorStatus("that community already has your Partner Premium", http.StatusConflict, w, nil)
		return
	}

	// 4. Refuse a destination somebody is already paying for a boost on. Our
	// marker would replace the Stripe / app-store id while the billing kept
	// running. A leftover cc_program_ grant is ours and may be replaced.
	if ccCommunityHasSelfFundedBoost(destination.Details.Subscription) {
		config.ErrorStatus("this community already has an active subscription", http.StatusConflict, w, nil)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	// 5. Spend one transfer. This is a single guarded FindOneAndUpdate and the
	// ONLY place transfersUsed moves up: the $expr compares the stored count to
	// the stored limit inside the write, so no number of concurrent requests
	// can push it past the limit, and no read-then-write window exists.
	consumed, err := cc.CCDB.FindOneAndUpdate(ctx,
		bson.M{
			"_id": creator.ID,
			"$expr": bson.M{"$lt": bson.A{
				bson.M{"$ifNull": bson.A{"$transfersUsed", 0}},
				bson.M{"$ifNull": bson.A{"$transferLimit", ccDefaultTransferLimit}},
			}},
		},
		bson.M{
			"$inc": bson.M{"transfersUsed": 1},
			"$set": bson.M{"updatedAt": now},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)
	if errors.Is(err, mongo.ErrNoDocuments) {
		writeTransferLimitReached(w, creator)
		return
	}
	if err != nil || consumed == nil {
		config.ErrorStatus("failed to reserve a transfer", http.StatusInternalServerError, w, err)
		return
	}

	// 6. Claim the old entitlement, also atomically. Two concurrent transfers
	// both get past step 3, but only one of them matches here, so only one ever
	// moves the promotion. The loser compensates its own counter spend.
	claimed, err := cc.EntDB.FindOneAndUpdate(ctx,
		bson.M{
			"contentCreatorId": creator.ID,
			"targetType":       "community",
			"active":           true,
		},
		bson.M{"$set": bson.M{
			"active":       false,
			"revokedAt":    now,
			"revokedBy":    userObjID,
			"revokeReason": ccTransferRevokeReason,
			"updatedAt":    now,
		}},
		options.FindOneAndUpdate().SetReturnDocument(options.Before),
	)
	if errors.Is(err, mongo.ErrNoDocuments) || (err == nil && claimed == nil) {
		cc.refundTransfer(ctx, creator.ID, now)
		config.ErrorStatus("you do not have a community promotion to transfer", http.StatusConflict, w, nil)
		return
	}
	if err != nil {
		cc.refundTransfer(ctx, creator.ID, now)
		config.ErrorStatus("failed to release the current community promotion", http.StatusInternalServerError, w, err)
		return
	}
	// Re-check against the document we actually claimed rather than the one we
	// read in step 3: a concurrent transfer could have moved the promotion onto
	// this very community in between.
	if claimed.TargetID == communityObjID {
		cc.restoreEntitlement(ctx, claimed.ID, now)
		cc.refundTransfer(ctx, creator.ID, now)
		config.ErrorStatus("that community already has your Partner Premium", http.StatusConflict, w, nil)
		return
	}

	// Name for the audit trail and the response. Read including pending so a
	// community queued for deletion still reports its name.
	fromName := ""
	if from, fromErr := cc.CDB.FindOneIncludingPending(ctx, bson.M{"_id": claimed.TargetID}); fromErr == nil && from != nil {
		fromName = from.Details.Name
	}

	// 7. Take the boost off the old community, guarded by the cc_program_
	// marker so a subscription they are paying for is never touched. Writes the
	// same four fields the other revocation paths write, so a community that
	// was transferred away is indistinguishable from one whose creator left the
	// program. ReturnDocument(Before) hands back what was there, which is what
	// makes the rollback below able to put it back.
	oldClearFilter := ccProgramGrantFilter("community.subscription.id")
	oldClearFilter["_id"] = claimed.TargetID
	oldBefore, oldErr := cc.CDB.FindOneAndUpdate(ctx, oldClearFilter,
		bson.M{"$set": bson.M{
			"community.subscription.plan":      "",
			"community.subscription.active":    false,
			"community.subscription.id":        "",
			"community.subscription.updatedAt": now,
		}},
		options.FindOneAndUpdate().SetReturnDocument(options.Before),
	)
	switch {
	case errors.Is(oldErr, mongo.ErrNoDocuments):
		// Nothing of ours on that community: either it was already cleared or
		// the owner bought a real boost for it. Either way there is nothing to
		// revoke and nothing to restore, and the transfer still stands.
		zap.S().Infow("old community had no program boost to clear",
			"creatorId", creator.ID.Hex(), "communityId", claimed.TargetID.Hex())
	case oldErr != nil:
		zap.S().Warnw("failed to clear the old community's program boost",
			"error", oldErr, "creatorId", creator.ID.Hex(), "communityId", claimed.TargetID.Hex())
	}

	// 8. Record the new entitlement. A fresh row, never a mutated targetId, so
	// the history shows what was held when.
	entitlement := models.ContentCreatorEntitlement{
		ID:               primitive.NewObjectID(),
		ContentCreatorID: creator.ID,
		TargetType:       "community",
		TargetID:         communityObjID,
		Plan:             ccProgramCommunityPlan,
		Source:           "content_creator_program",
		Active:           true,
		GrantedAt:        now,
		GrantedBy:        userObjID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if _, err := cc.EntDB.InsertOne(ctx, entitlement); err != nil {
		cc.rollbackTransfer(ctx, creator.ID, claimed, oldBefore, primitive.NilObjectID, now)
		config.ErrorStatus("failed to move the community promotion", http.StatusInternalServerError, w, err)
		return
	}

	// 9. Put the boost on the destination, guarded by the same filter the apply
	// path uses to issue a grant: if somebody bought a boost for this community
	// between step 4 and here, this matches nothing and we undo everything
	// rather than overwrite their live subscription id.
	_, err = cc.CDB.FindOneAndUpdate(ctx,
		ccProgramIssueGrantFilter(communityObjID, "community.subscription.id"),
		bson.M{"$set": bson.M{
			"community.subscription.plan":      ccProgramCommunityPlan,
			"community.subscription.active":    true,
			"community.subscription.id":        ccProgramSubscriptionPrefix + creator.ID.Hex(),
			"community.subscription.createdAt": now,
			"community.subscription.updatedAt": now,
		}},
		options.FindOneAndUpdate().SetReturnDocument(options.Before),
	)
	if err != nil {
		cc.rollbackTransfer(ctx, creator.ID, claimed, oldBefore, entitlement.ID, now)
		if errors.Is(err, mongo.ErrNoDocuments) {
			config.ErrorStatus("this community already has an active subscription", http.StatusConflict, w, nil)
			return
		}
		config.ErrorStatus("failed to apply the community promotion", http.StatusInternalServerError, w, err)
		return
	}

	used := consumed.TransfersUsed
	limit := consumed.EffectiveTransferLimit()
	remaining := consumed.RemainingTransfers()

	cc.auditTransfer(r, creator, claimed.TargetID.Hex(), fromName,
		communityObjID.Hex(), destination.Details.Name, used, limit)

	zap.S().Infow("content creator transferred community promotion",
		"creatorId", creator.ID.Hex(),
		"userId", userObjID.Hex(),
		"fromCommunityId", claimed.TargetID.Hex(),
		"toCommunityId", communityObjID.Hex(),
		"transfersUsed", used,
		"transferLimit", limit,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":            true,
		"fromCommunityId":    claimed.TargetID.Hex(),
		"fromCommunityName":  fromName,
		"toCommunityId":      communityObjID.Hex(),
		"toCommunityName":    destination.Details.Name,
		"transfersUsed":      used,
		"transfersRemaining": remaining,
		"transferLimit":      limit,
	})
}

// ccCommunityHasSelfFundedBoost reports whether a community is on a boost
// somebody is paying for. "base" and "free" are not boost tiers — "base" is
// what the program itself used to write before it granted a real tier — so
// neither counts as a paid boost.
func ccCommunityHasSelfFundedBoost(sub models.Subscription) bool {
	return isSelfFundedSubscription(sub) &&
		sub.Plan != "" && sub.Plan != "base" && sub.Plan != "free"
}

// writeTransferLimitReached returns the one error on this endpoint the frontend
// has to branch on, so it carries a stable code alongside the copy. The shape
// is a top-level {error, message}, which the website's apiErrorReason helper
// already understands.
func writeTransferLimitReached(w http.ResponseWriter, creator *models.ContentCreator) {
	zap.S().Infow("content creator transfer refused, limit reached",
		"creatorId", creator.ID.Hex(),
		"transfersUsed", creator.TransfersUsed,
		"transferLimit", creator.EffectiveTransferLimit(),
	)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":              ccTransferLimitReachedCode,
		"message":            ccTransferLimitReachedMsg,
		"transfersUsed":      creator.TransfersUsed,
		"transfersRemaining": creator.RemainingTransfers(),
		"transferLimit":      creator.EffectiveTransferLimit(),
	})
}

// refundTransfer gives back the transfer spent by a run that then failed. The
// `$gt: 0` guard is what makes this safe to call on any failure path: the
// decrement cannot take the count below zero even if it is somehow called twice
// or after the counter was reset.
func (cc ContentCreator) refundTransfer(ctx context.Context, creatorID primitive.ObjectID, now primitive.DateTime) {
	_, err := cc.CCDB.FindOneAndUpdate(ctx,
		bson.M{"_id": creatorID, "transfersUsed": bson.M{"$gt": 0}},
		bson.M{
			"$inc": bson.M{"transfersUsed": -1},
			"$set": bson.M{"updatedAt": now},
		},
	)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		zap.S().Errorw("failed to refund a consumed creator transfer",
			"error", err, "creatorId", creatorID.Hex())
	}
}

// restoreEntitlement puts back the entitlement a failed transfer revoked. The
// revocation fields are unset rather than left behind so the row reads as the
// untouched active entitlement it is again.
func (cc ContentCreator) restoreEntitlement(ctx context.Context, entitlementID primitive.ObjectID, now primitive.DateTime) {
	err := cc.EntDB.UpdateOne(ctx, bson.M{"_id": entitlementID}, bson.M{
		"$set":   bson.M{"active": true, "updatedAt": now},
		"$unset": bson.M{"revokedAt": "", "revokedBy": "", "revokeReason": ""},
	})
	if err != nil {
		zap.S().Errorw("failed to restore the entitlement after a failed transfer",
			"error", err, "entitlementId", entitlementID.Hex())
	}
}

// rollbackTransfer undoes a transfer that could not be completed, in the
// reverse order it was applied: drop the new entitlement, restore the old one,
// put the old community's boost back, and give the transfer back.
//
// newEntitlementID may be NilObjectID when the insert itself is what failed.
// oldBefore may be nil when there was no program boost to clear.
func (cc ContentCreator) rollbackTransfer(
	ctx context.Context,
	creatorID primitive.ObjectID,
	claimed *models.ContentCreatorEntitlement,
	oldBefore *models.Community,
	newEntitlementID primitive.ObjectID,
	now primitive.DateTime,
) {
	if newEntitlementID != primitive.NilObjectID {
		if err := cc.EntDB.DeleteOne(ctx, bson.M{"_id": newEntitlementID}); err != nil {
			zap.S().Errorw("failed to remove the new entitlement during transfer rollback",
				"error", err, "entitlementId", newEntitlementID.Hex())
		}
	}

	if claimed != nil {
		cc.restoreEntitlement(ctx, claimed.ID, now)

		if oldBefore != nil {
			previous := oldBefore.Details.Subscription
			// Guarded by the marker again: if something else has written a real
			// subscription onto that community since, leave it alone.
			restoreFilter := ccProgramIssueGrantFilter(claimed.TargetID, "community.subscription.id")
			err := cc.CDB.UpdateOne(ctx, restoreFilter, bson.M{"$set": bson.M{
				"community.subscription.plan":      previous.Plan,
				"community.subscription.active":    previous.Active,
				"community.subscription.id":        previous.ID,
				"community.subscription.updatedAt": now,
			}})
			if err != nil {
				zap.S().Errorw("failed to restore the old community boost during transfer rollback",
					"error", err, "communityId", claimed.TargetID.Hex())
			}
		}
	}

	cc.refundTransfer(ctx, creatorID, now)
}

// auditTransfer writes the one audit record per transfer, through the same
// admin_audit trail the rest of the content creator handlers use.
func (cc ContentCreator) auditTransfer(
	r *http.Request,
	creator *models.ContentCreator,
	fromCommunityID, fromCommunityName, toCommunityID, toCommunityName string,
	used, limit int,
) {
	if cc.AuditDB == nil {
		return
	}
	actorID, _ := getUserIDFromRequest(r)
	audit := models.AdminAudit{
		AdminID:       actorID,
		Action:        "content_creator_transfer_community_promotion",
		CommunityID:   toCommunityID,
		CommunityName: toCommunityName,
		Before: map[string]interface{}{
			"communityId":   fromCommunityID,
			"communityName": fromCommunityName,
			"transfersUsed": used - 1,
		},
		After: map[string]interface{}{
			"communityId":   toCommunityID,
			"communityName": toCommunityName,
			"transfersUsed": used,
			"transferLimit": limit,
		},
		Details:   "creator " + creator.ID.Hex() + " moved their community promotion",
		Timestamp: time.Now(),
		IP:        getClientIP(r),
	}
	if _, err := cc.AuditDB.InsertOne(r.Context(), audit); err != nil {
		zap.S().Errorw("failed to write community promotion transfer audit log", "error", err)
	}
}

// AdminGrantCreatorTransfersHandler raises a creator's lifetime transfer limit.
// POST /api/v1/admin/content-creators/{id}/transfers
//
// The limit is raised rather than the used count lowered, so the history of how
// many moves a creator has actually made stays intact.
func (cc ContentCreator) AdminGrantCreatorTransfersHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	adminIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	adminObjID, err := primitive.ObjectIDFromHex(adminIDStr)
	if err != nil {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, err)
		return
	}
	// The admin console sends the acting admin's id in X-User-ID, as it does for
	// every other admin content-creator route. Requiring that it resolve to a
	// live admin_users record is the gate: without it this endpoint would hand
	// out allowance to anyone who can shape a request.
	admin, err := cc.AdminDB.FindOne(ctx, bson.M{"_id": adminObjID, "active": true})
	if err != nil || admin == nil {
		config.ErrorStatus("admin access required", http.StatusForbidden, w, err)
		return
	}

	creatorObjID, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		config.ErrorStatus("invalid creator ID", http.StatusBadRequest, w, err)
		return
	}

	var req models.GrantCreatorTransfersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if req.Additional < 1 || req.Additional > 10 {
		config.ErrorStatus("additional must be between 1 and 10", http.StatusBadRequest, w, nil)
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if len(reason) > 500 {
		config.ErrorStatus("reason must be at most 500 characters", http.StatusBadRequest, w, nil)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	// An update pipeline rather than $inc: a creator document written before
	// this feature has no transferLimit field at all, and $inc would create it
	// at `additional` — silently LOWERING the allowance below the default. The
	// $ifNull resolves the default first, then adds.
	updated, err := cc.CCDB.FindOneAndUpdate(ctx,
		bson.M{"_id": creatorObjID},
		mongo.Pipeline{{{Key: "$set", Value: bson.D{
			{Key: "transferLimit", Value: bson.D{{Key: "$add", Value: bson.A{
				bson.D{{Key: "$ifNull", Value: bson.A{"$transferLimit", ccDefaultTransferLimit}}},
				req.Additional,
			}}}},
			{Key: "updatedAt", Value: now},
		}}}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)
	if errors.Is(err, mongo.ErrNoDocuments) {
		config.ErrorStatus("creator not found", http.StatusNotFound, w, err)
		return
	}
	if err != nil || updated == nil {
		config.ErrorStatus("failed to grant transfers", http.StatusInternalServerError, w, err)
		return
	}

	if cc.AuditDB != nil {
		audit := models.AdminAudit{
			AdminID:    adminIDStr,
			AdminEmail: admin.Email,
			Action:     "content_creator_grant_transfers",
			Before:     map[string]interface{}{"transferLimit": updated.EffectiveTransferLimit() - req.Additional},
			After: map[string]interface{}{
				"transferLimit": updated.EffectiveTransferLimit(),
				"transfersUsed": updated.TransfersUsed,
			},
			Details:   "granted " + strconv.Itoa(req.Additional) + " extra community promotion transfers to creator " + creatorObjID.Hex() + ": " + reason,
			Timestamp: time.Now(),
			IP:        getClientIP(r),
		}
		if _, auditErr := cc.AuditDB.InsertOne(r.Context(), audit); auditErr != nil {
			zap.S().Errorw("failed to write transfer grant audit log", "error", auditErr)
		}
	}

	zap.S().Infow("admin granted extra community promotion transfers",
		"creatorId", creatorObjID.Hex(),
		"adminId", adminIDStr,
		"additional", req.Additional,
		"transferLimit", updated.EffectiveTransferLimit(),
		"reason", reason,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":            true,
		"transfersUsed":      updated.TransfersUsed,
		"transferLimit":      updated.EffectiveTransferLimit(),
		"transfersRemaining": updated.RemainingTransfers(),
	})
}
