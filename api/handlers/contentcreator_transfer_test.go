package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// Transferring the free community promotion spends a capped lifetime
// allowance and rewrites three documents with no transaction around them, so
// these tests are mostly about what must NOT happen: the count must never pass
// the limit, never go below zero, and a subscription somebody is paying for
// must never be cleared or overwritten.
//
// The fakes below do not pretend to be Mongo. They implement exactly the
// semantics the handler relies on for the specific filters it sends — the
// $expr limit guard, the `transfersUsed > 0` refund guard, the cc_program_
// marker guard, and the issue-grant guard — because those guards ARE the
// safety, and a test that let any write through unconditionally would prove
// nothing.

// transferWorld is an in-memory stand-in for the three collections a transfer
// touches, wired into the generated mocks.
type transferWorld struct {
	creator      models.ContentCreator
	entitlements []*models.ContentCreatorEntitlement
	communities  map[primitive.ObjectID]*models.Community

	ownerID string

	// Recorded for assertions.
	insertedEntitlements []models.ContentCreatorEntitlement
	deletedEntitlements  []primitive.ObjectID
	clearAttempts        []primitive.ObjectID
	clearsApplied        []primitive.ObjectID
}

func ccTestSubscription(id, plan string, active bool) models.Subscription {
	return models.Subscription{ID: id, Plan: plan, Active: active}
}

func ccTestCommunity(name, ownerID string, sub models.Subscription) *models.Community {
	community := &models.Community{ID: primitive.NewObjectID()}
	community.Details.Name = name
	community.Details.OwnerID = ownerID
	community.Details.Subscription = sub
	return community
}

func newTransferWorld() *transferWorld {
	ownerID := primitive.NewObjectID().Hex()
	return &transferWorld{
		creator: models.ContentCreator{
			ID:     primitive.NewObjectID(),
			Status: "active",
		},
		communities: map[primitive.ObjectID]*models.Community{},
		ownerID:     ownerID,
	}
}

func (w *transferWorld) addCommunity(community *models.Community) *models.Community {
	w.communities[community.ID] = community
	return community
}

// grantTo seeds the state the feature starts from: an active community
// entitlement plus the matching program grant on that community.
func (w *transferWorld) grantTo(community *models.Community) *models.ContentCreatorEntitlement {
	ent := &models.ContentCreatorEntitlement{
		ID:               primitive.NewObjectID(),
		ContentCreatorID: w.creator.ID,
		TargetType:       "community",
		TargetID:         community.ID,
		Plan:             ccProgramCommunityPlan,
		Source:           "content_creator_program",
		Active:           true,
	}
	w.entitlements = append(w.entitlements, ent)
	community.Details.Subscription = ccTestSubscription(
		ccProgramSubscriptionPrefix+w.creator.ID.Hex(), ccProgramCommunityPlan, true)
	return ent
}

func (w *transferWorld) activeEntitlement() *models.ContentCreatorEntitlement {
	for _, ent := range w.entitlements {
		if ent.Active && ent.TargetType == "community" {
			return ent
		}
	}
	return nil
}

// isProgramGrantFilter recognises the ccProgramGrantFilter shape: clear the
// grant only when subscription.id carries our marker.
func isProgramGrantFilter(filter interface{}) bool {
	f, ok := filter.(bson.M)
	if !ok {
		return false
	}
	_, hasRegex := f["community.subscription.id"]
	return hasRegex
}

// isIssueGrantFilter recognises the ccProgramIssueGrantFilter shape.
func isIssueGrantFilter(filter interface{}) bool {
	f, ok := filter.(bson.M)
	if !ok {
		return false
	}
	_, hasOr := f["$or"]
	return hasOr
}

func filterObjectID(filter interface{}, key string) primitive.ObjectID {
	f, ok := filter.(bson.M)
	if !ok {
		return primitive.NilObjectID
	}
	if id, ok := f[key].(primitive.ObjectID); ok {
		return id
	}
	return primitive.NilObjectID
}

// setFields pulls the $set document out of an update.
func setFields(update interface{}) bson.M {
	u, ok := update.(bson.M)
	if !ok {
		return bson.M{}
	}
	if s, ok := u["$set"].(bson.M); ok {
		return s
	}
	return bson.M{}
}

func (w *transferWorld) handler() ContentCreator {
	creatorDB := &mocks.ContentCreatorDatabase{}
	entDB := &mocks.ContentCreatorEntitlementDatabase{}
	commDB := &mocks.CommunityDatabase{}

	creatorDB.On("FindOne", mock.Anything, mock.Anything).Return(
		func(_ context.Context, filter interface{}) (*models.ContentCreator, error) {
			if w.creator.Status == "" {
				return nil, mongo.ErrNoDocuments
			}
			copied := w.creator
			return &copied, nil
		})

	// The creator document is only ever written by two guarded updates: the
	// consume ($expr limit guard) and the refund (transfersUsed > 0), plus the
	// admin grant (an update pipeline).
	// Registered at both arities: the consume and the admin grant pass a
	// ReturnDocument option, the refund passes none, and testify matches on the
	// argument count.
	consumeOrRefund := func(_ context.Context, filter interface{}, update interface{}, _ ...*options.FindOneAndUpdateOptions) (*models.ContentCreator, error) {
		f, _ := filter.(bson.M)

		if pipeline, ok := update.(mongo.Pipeline); ok {
			// Admin grant: resolve the absent limit to the default, then add.
			added := ccAdminGrantAmount(pipeline)
			w.creator.TransferLimit = w.creator.EffectiveTransferLimit() + added
			copied := w.creator
			return &copied, nil
		}

		if _, isConsume := f["$expr"]; isConsume {
			if w.creator.TransfersUsed >= w.creator.EffectiveTransferLimit() {
				return nil, mongo.ErrNoDocuments
			}
			w.creator.TransfersUsed++
			copied := w.creator
			return &copied, nil
		}

		// Refund. The guard is what keeps the count from going negative.
		if _, isRefund := f["transfersUsed"]; isRefund {
			if w.creator.TransfersUsed <= 0 {
				return nil, mongo.ErrNoDocuments
			}
			w.creator.TransfersUsed--
			copied := w.creator
			return &copied, nil
		}

		return nil, mongo.ErrNoDocuments
	}
	creatorDB.On("FindOneAndUpdate", mock.Anything, mock.Anything, mock.Anything).Return(consumeOrRefund)
	creatorDB.On("FindOneAndUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(consumeOrRefund)

	entDB.On("FindOne", mock.Anything, mock.Anything).Return(
		func(_ context.Context, _ interface{}) (*models.ContentCreatorEntitlement, error) {
			active := w.activeEntitlement()
			if active == nil {
				return nil, mongo.ErrNoDocuments
			}
			copied := *active
			return &copied, nil
		})

	entDB.On("FindOneAndUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, _ interface{}, _ interface{}, _ ...*options.FindOneAndUpdateOptions) (*models.ContentCreatorEntitlement, error) {
			active := w.activeEntitlement()
			if active == nil {
				return nil, mongo.ErrNoDocuments
			}
			before := *active
			active.Active = false
			active.RevokeReason = ccTransferRevokeReason
			return &before, nil
		})

	entDB.On("InsertOne", mock.Anything, mock.Anything).Return(
		func(_ context.Context, ent models.ContentCreatorEntitlement, _ ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
			w.insertedEntitlements = append(w.insertedEntitlements, ent)
			stored := ent
			w.entitlements = append(w.entitlements, &stored)
			return nil, nil
		})

	entDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, filter interface{}, _ interface{}, _ ...*options.UpdateOptions) error {
			id := filterObjectID(filter, "_id")
			for _, ent := range w.entitlements {
				if ent.ID == id {
					ent.Active = true
					ent.RevokeReason = ""
				}
			}
			return nil
		})

	entDB.On("DeleteOne", mock.Anything, mock.Anything).Return(
		func(_ context.Context, filter interface{}, _ ...*options.DeleteOptions) error {
			id := filterObjectID(filter, "_id")
			w.deletedEntitlements = append(w.deletedEntitlements, id)
			kept := w.entitlements[:0]
			for _, ent := range w.entitlements {
				if ent.ID != id {
					kept = append(kept, ent)
				}
			}
			w.entitlements = kept
			return nil
		})

	lookup := func(_ context.Context, filter interface{}) (*models.Community, error) {
		community, ok := w.communities[filterObjectID(filter, "_id")]
		if !ok {
			return nil, mongo.ErrNoDocuments
		}
		copied := *community
		return &copied, nil
	}
	commDB.On("FindOne", mock.Anything, mock.Anything).Return(lookup)
	commDB.On("FindOneIncludingPending", mock.Anything, mock.Anything).Return(lookup)

	commDB.On("FindOneAndUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, filter interface{}, update interface{}, _ ...*options.FindOneAndUpdateOptions) (*models.Community, error) {
			id := filterObjectID(filter, "_id")
			community, ok := w.communities[id]
			if !ok {
				return nil, mongo.ErrNoDocuments
			}
			before := *community

			switch {
			case isIssueGrantFilter(filter):
				// Issuing a grant: refuse a target holding a live self-funded
				// subscription, exactly as the $or in the real filter does.
				if isSelfFundedSubscription(community.Details.Subscription) {
					return nil, mongo.ErrNoDocuments
				}
			case isProgramGrantFilter(filter):
				// Clearing a grant: only ours carry the marker.
				w.clearAttempts = append(w.clearAttempts, id)
				if !strings.HasPrefix(community.Details.Subscription.ID, ccProgramSubscriptionPrefix) {
					return nil, mongo.ErrNoDocuments
				}
				w.clearsApplied = append(w.clearsApplied, id)
			}

			fields := setFields(update)
			if plan, ok := fields["community.subscription.plan"].(string); ok {
				community.Details.Subscription.Plan = plan
			}
			if active, ok := fields["community.subscription.active"].(bool); ok {
				community.Details.Subscription.Active = active
			}
			if subID, ok := fields["community.subscription.id"].(string); ok {
				community.Details.Subscription.ID = subID
			}
			return &before, nil
		})

	commDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, filter interface{}, update interface{}, _ ...*options.UpdateOptions) error {
			community, ok := w.communities[filterObjectID(filter, "_id")]
			if !ok {
				return nil
			}
			fields := setFields(update)
			if plan, ok := fields["community.subscription.plan"].(string); ok {
				community.Details.Subscription.Plan = plan
			}
			if active, ok := fields["community.subscription.active"].(bool); ok {
				community.Details.Subscription.Active = active
			}
			if subID, ok := fields["community.subscription.id"].(string); ok {
				community.Details.Subscription.ID = subID
			}
			return nil
		})

	return ContentCreator{CCDB: creatorDB, EntDB: entDB, CDB: commDB}
}

// ccAdminGrantAmount digs the added amount out of the update pipeline the admin
// grant sends, so the fake can apply it the way Mongo would.
func ccAdminGrantAmount(pipeline mongo.Pipeline) int {
	for _, stage := range pipeline {
		for _, elem := range stage {
			if elem.Key != "$set" {
				continue
			}
			set, ok := elem.Value.(bson.D)
			if !ok {
				continue
			}
			for _, field := range set {
				if field.Key != "transferLimit" {
					continue
				}
				add, ok := field.Value.(bson.D)
				if !ok || len(add) == 0 {
					continue
				}
				args, ok := add[0].Value.(bson.A)
				if !ok || len(args) != 2 {
					continue
				}
				if n, ok := args[1].(int); ok {
					return n
				}
			}
		}
	}
	return 0
}

func (w *transferWorld) transfer(handler ContentCreator, userID, communityID string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"communityId": communityID})
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/content-creators/me/community-promotion/transfer", bytes.NewReader(body))
	req.Header.Set("X-User-ID", userID)
	rec := httptest.NewRecorder()
	handler.TransferCommunityPromotionHandler(rec, req)
	return rec
}

// Three free transfers, then the door closes. The count is the thing that must
// hold: a fourth attempt gets a machine-readable code and changes nothing.
func TestTransferCommunityPromotion_LimitIsThree(t *testing.T) {
	w := newTransferWorld()
	first := w.addCommunity(ccTestCommunity("First", w.ownerID, models.Subscription{}))
	second := w.addCommunity(ccTestCommunity("Second", w.ownerID, models.Subscription{}))
	w.grantTo(first)
	handler := w.handler()

	destinations := []*models.Community{second, first, second}
	for i, dest := range destinations {
		rec := w.transfer(handler, w.ownerID, dest.ID.Hex())
		assert.Equal(t, http.StatusOK, rec.Code, "transfer %d should succeed", i+1)

		body := decodeBody(t, rec)
		assert.Equal(t, true, body["success"])
		assert.Equal(t, dest.ID.Hex(), body["toCommunityId"])
		assert.Equal(t, float64(i+1), body["transfersUsed"])
		assert.Equal(t, float64(3-(i+1)), body["transfersRemaining"])
		assert.Equal(t, float64(3), body["transferLimit"])

		// The promotion lands on the destination and leaves the other one free.
		assert.True(t, dest.Details.Subscription.Active)
		assert.Equal(t, ccProgramCommunityPlan, dest.Details.Subscription.Plan)
	}

	assert.Equal(t, 3, w.creator.TransfersUsed)

	rec := w.transfer(handler, w.ownerID, first.ID.Hex())
	assert.Equal(t, http.StatusConflict, rec.Code)
	body := decodeBody(t, rec)
	assert.Equal(t, ccTransferLimitReachedCode, body["error"])
	assert.Equal(t, ccTransferLimitReachedMsg, body["message"])
	assert.Equal(t, 3, w.creator.TransfersUsed, "a refused transfer must not move the count")
	// The promotion is untouched: still on the community it was on.
	active := w.activeEntitlement()
	assert.NotNil(t, active)
	assert.Equal(t, second.ID, active.TargetID)
	assert.True(t, second.Details.Subscription.Active)
}

// A limit already reached is refused by the guard inside the write, not by a
// read beforehand, so no increment can slip through.
func TestTransferCommunityPromotion_ConsumeGuardBlocksWhenExhausted(t *testing.T) {
	w := newTransferWorld()
	w.creator.TransfersUsed = 3
	w.creator.TransferLimit = 3
	from := w.addCommunity(ccTestCommunity("From", w.ownerID, models.Subscription{}))
	to := w.addCommunity(ccTestCommunity("To", w.ownerID, models.Subscription{}))
	w.grantTo(from)
	handler := w.handler()

	rec := w.transfer(handler, w.ownerID, to.ID.Hex())

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, ccTransferLimitReachedCode, decodeBody(t, rec)["error"])
	assert.Equal(t, 3, w.creator.TransfersUsed, "no increment when the guard does not match")
	assert.Empty(t, w.insertedEntitlements, "nothing may be written after a refusal")
	assert.Empty(t, w.clearsApplied)
	assert.Equal(t, from.ID, w.activeEntitlement().TargetID)
}

// A raised limit is honoured by the same guard, which is the whole point of
// admins being able to grant more.
func TestTransferCommunityPromotion_RaisedLimitAllowsAnotherTransfer(t *testing.T) {
	w := newTransferWorld()
	w.creator.TransfersUsed = 3
	w.creator.TransferLimit = 4
	from := w.addCommunity(ccTestCommunity("From", w.ownerID, models.Subscription{}))
	to := w.addCommunity(ccTestCommunity("To", w.ownerID, models.Subscription{}))
	w.grantTo(from)
	handler := w.handler()

	rec := w.transfer(handler, w.ownerID, to.ID.Hex())

	assert.Equal(t, http.StatusOK, rec.Code)
	body := decodeBody(t, rec)
	assert.Equal(t, float64(4), body["transfersUsed"])
	assert.Equal(t, float64(0), body["transfersRemaining"])
	assert.Equal(t, float64(4), body["transferLimit"])
}

func TestTransferCommunityPromotion_Eligibility(t *testing.T) {
	t.Run("not a creator is refused", func(t *testing.T) {
		w := newTransferWorld()
		w.creator.Status = "" // the fake reads this as "no creator document"
		to := w.addCommunity(ccTestCommunity("To", w.ownerID, models.Subscription{}))
		handler := w.handler()

		rec := w.transfer(handler, w.ownerID, to.ID.Hex())

		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Equal(t, 0, w.creator.TransfersUsed)
	})

	t.Run("destination they do not own is refused", func(t *testing.T) {
		w := newTransferWorld()
		from := w.addCommunity(ccTestCommunity("From", w.ownerID, models.Subscription{}))
		someoneElse := w.addCommunity(ccTestCommunity("Theirs", primitive.NewObjectID().Hex(), models.Subscription{}))
		w.grantTo(from)
		handler := w.handler()

		rec := w.transfer(handler, w.ownerID, someoneElse.ID.Hex())

		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Equal(t, 0, w.creator.TransfersUsed)
		assert.Equal(t, from.ID, w.activeEntitlement().TargetID)
	})

	t.Run("missing destination is a 404", func(t *testing.T) {
		w := newTransferWorld()
		from := w.addCommunity(ccTestCommunity("From", w.ownerID, models.Subscription{}))
		w.grantTo(from)
		handler := w.handler()

		rec := w.transfer(handler, w.ownerID, primitive.NewObjectID().Hex())

		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Equal(t, 0, w.creator.TransfersUsed)
	})

	t.Run("a malformed community id is a 400", func(t *testing.T) {
		w := newTransferWorld()
		handler := w.handler()

		rec := w.transfer(handler, w.ownerID, "not-an-object-id")

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

// Transferring to the community that already has it is a no-op, and must not
// cost a transfer.
func TestTransferCommunityPromotion_SameCommunityIsRefused(t *testing.T) {
	w := newTransferWorld()
	current := w.addCommunity(ccTestCommunity("Current", w.ownerID, models.Subscription{}))
	w.grantTo(current)
	handler := w.handler()

	rec := w.transfer(handler, w.ownerID, current.ID.Hex())

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "already has your Partner Premium")
	assert.Equal(t, 0, w.creator.TransfersUsed)
	assert.Empty(t, w.clearsApplied)
	assert.True(t, current.Details.Subscription.Active)
}

// The destination is already paying for a boost: refuse, and write nothing at
// all. Overwriting subscription.id there would detach live billing.
func TestTransferCommunityPromotion_SelfFundedDestinationWritesNothing(t *testing.T) {
	w := newTransferWorld()
	from := w.addCommunity(ccTestCommunity("From", w.ownerID, models.Subscription{}))
	paid := w.addCommunity(ccTestCommunity("Paid", w.ownerID,
		ccTestSubscription("sub_1PabcXYZ", "elite", true)))
	fromEnt := w.grantTo(from)
	handler := w.handler()

	rec := w.transfer(handler, w.ownerID, paid.ID.Hex())

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, 0, w.creator.TransfersUsed, "the counter must be untouched")
	assert.Empty(t, w.insertedEntitlements)
	assert.Empty(t, w.clearAttempts, "the old community must not even be written to")

	// The old community keeps its program grant and its entitlement.
	assert.True(t, fromEnt.Active)
	assert.Equal(t, ccProgramSubscriptionPrefix+w.creator.ID.Hex(), from.Details.Subscription.ID)
	assert.True(t, from.Details.Subscription.Active)

	// And the paid boost is exactly as it was.
	assert.Equal(t, "sub_1PabcXYZ", paid.Details.Subscription.ID)
	assert.Equal(t, "elite", paid.Details.Subscription.Plan)
	assert.True(t, paid.Details.Subscription.Active)
}

// Nothing to transfer. The pre-check catches the ordinary case without
// spending anything.
func TestTransferCommunityPromotion_NothingToTransfer(t *testing.T) {
	w := newTransferWorld()
	to := w.addCommunity(ccTestCommunity("To", w.ownerID, models.Subscription{}))
	handler := w.handler()

	rec := w.transfer(handler, w.ownerID, to.ID.Hex())

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "do not have a community promotion to transfer")
	assert.Equal(t, 0, w.creator.TransfersUsed)
}

// The race the atomic claim exists for: the entitlement is gone between the
// pre-check and the claim (a concurrent transfer won it). The transfer already
// spent is given back, so the loser is not charged.
func TestTransferCommunityPromotion_LostClaimRefundsTheTransfer(t *testing.T) {
	w := newTransferWorld()
	from := w.addCommunity(ccTestCommunity("From", w.ownerID, models.Subscription{}))
	to := w.addCommunity(ccTestCommunity("To", w.ownerID, models.Subscription{}))
	ent := w.grantTo(from)
	handler := w.handler()

	// The pre-check sees the entitlement; the claim finds it already revoked.
	entDB := handler.EntDB.(*mocks.ContentCreatorEntitlementDatabase)
	entDB.ExpectedCalls = nil
	entDB.On("FindOne", mock.Anything, mock.Anything).Return(
		func(_ context.Context, _ interface{}) (*models.ContentCreatorEntitlement, error) {
			copied := *ent
			return &copied, nil
		})
	entDB.On("FindOneAndUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, _ interface{}, _ interface{}, _ ...*options.FindOneAndUpdateOptions) (*models.ContentCreatorEntitlement, error) {
			return nil, mongo.ErrNoDocuments
		})

	rec := w.transfer(handler, w.ownerID, to.ID.Hex())

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "do not have a community promotion to transfer")
	assert.Equal(t, 0, w.creator.TransfersUsed, "the consumed transfer must be refunded")
	assert.Empty(t, w.clearsApplied)
}

// The refund can never take the count below zero: it is guarded on the write,
// so a creator whose count is already 0 is left at 0.
func TestRefundTransfer_NeverGoesBelowZero(t *testing.T) {
	w := newTransferWorld()
	handler := w.handler()

	handler.refundTransfer(context.Background(), w.creator.ID, primitive.NewDateTimeFromTime(time.Now()))

	assert.Equal(t, 0, w.creator.TransfersUsed)
}

// The old community's clear is guarded on the cc_program_ marker. A community
// carrying a Stripe id is never cleared, even though it is the one being
// transferred away from.
func TestTransferCommunityPromotion_OldCommunityWithPaidSubIsNotCleared(t *testing.T) {
	w := newTransferWorld()
	// The entitlement says the promotion is on this community, but its
	// subscription is one the owner bought. That mismatch is exactly what the
	// marker guard is for.
	from := w.addCommunity(ccTestCommunity("From", w.ownerID,
		ccTestSubscription("sub_1PoldXYZ", "premium", true)))
	to := w.addCommunity(ccTestCommunity("To", w.ownerID, models.Subscription{}))
	ent := &models.ContentCreatorEntitlement{
		ID:               primitive.NewObjectID(),
		ContentCreatorID: w.creator.ID,
		TargetType:       "community",
		TargetID:         from.ID,
		Active:           true,
	}
	w.entitlements = append(w.entitlements, ent)
	handler := w.handler()

	rec := w.transfer(handler, w.ownerID, to.ID.Hex())

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []primitive.ObjectID{from.ID}, w.clearAttempts, "the clear is attempted")
	assert.Empty(t, w.clearsApplied, "but the guard refuses to touch a paid subscription")
	assert.Equal(t, "sub_1PoldXYZ", from.Details.Subscription.ID)
	assert.Equal(t, "premium", from.Details.Subscription.Plan)
	assert.True(t, from.Details.Subscription.Active)

	// The destination still gets the boost.
	assert.Equal(t, ccProgramSubscriptionPrefix+w.creator.ID.Hex(), to.Details.Subscription.ID)
	assert.True(t, to.Details.Subscription.Active)
}

// A successful transfer revokes the old entitlement rather than rewriting its
// target, and inserts a new one, so the history says what was held when.
func TestTransferCommunityPromotion_KeepsEntitlementHistory(t *testing.T) {
	w := newTransferWorld()
	from := w.addCommunity(ccTestCommunity("From", w.ownerID, models.Subscription{}))
	to := w.addCommunity(ccTestCommunity("To", w.ownerID, models.Subscription{}))
	oldEnt := w.grantTo(from)
	handler := w.handler()

	rec := w.transfer(handler, w.ownerID, to.ID.Hex())
	assert.Equal(t, http.StatusOK, rec.Code)

	assert.False(t, oldEnt.Active)
	assert.Equal(t, ccTransferRevokeReason, oldEnt.RevokeReason)
	assert.Equal(t, from.ID, oldEnt.TargetID, "the old row must keep pointing at the old community")

	assert.Len(t, w.insertedEntitlements, 1)
	inserted := w.insertedEntitlements[0]
	assert.Equal(t, to.ID, inserted.TargetID)
	assert.Equal(t, ccProgramCommunityPlan, inserted.Plan)
	assert.True(t, inserted.Active)
	assert.Equal(t, "content_creator_program", inserted.Source)

	// The old community is back to free, written the same way the other
	// revocation paths write it.
	assert.Equal(t, "", from.Details.Subscription.Plan)
	assert.Equal(t, "", from.Details.Subscription.ID)
	assert.False(t, from.Details.Subscription.Active)

	body := decodeBody(t, rec)
	assert.Equal(t, "From", body["fromCommunityName"])
	assert.Equal(t, "To", body["toCommunityName"])
}

// If the destination's guarded grant matches nothing (somebody bought a boost
// for it mid-flight), everything already done is undone.
func TestTransferCommunityPromotion_RollsBackWhenDestinationIsClaimedMidFlight(t *testing.T) {
	w := newTransferWorld()
	from := w.addCommunity(ccTestCommunity("From", w.ownerID, models.Subscription{}))
	to := w.addCommunity(ccTestCommunity("To", w.ownerID, models.Subscription{}))
	oldEnt := w.grantTo(from)
	handler := w.handler()

	// Simulate the purchase landing between the eligibility read and the write.
	commDB := handler.CDB.(*mocks.CommunityDatabase)
	originalCalls := commDB.ExpectedCalls
	commDB.ExpectedCalls = nil
	for _, call := range originalCalls {
		if call.Method == "FindOneAndUpdate" {
			continue
		}
		commDB.ExpectedCalls = append(commDB.ExpectedCalls, call)
	}
	commDB.On("FindOneAndUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
		func(_ context.Context, filter interface{}, update interface{}, _ ...*options.FindOneAndUpdateOptions) (*models.Community, error) {
			if isIssueGrantFilter(filter) {
				return nil, mongo.ErrNoDocuments
			}
			community := w.communities[filterObjectID(filter, "_id")]
			before := *community
			fields := setFields(update)
			community.Details.Subscription.Plan, _ = fields["community.subscription.plan"].(string)
			community.Details.Subscription.ID, _ = fields["community.subscription.id"].(string)
			community.Details.Subscription.Active, _ = fields["community.subscription.active"].(bool)
			w.clearsApplied = append(w.clearsApplied, community.ID)
			return &before, nil
		})

	rec := w.transfer(handler, w.ownerID, to.ID.Hex())

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, 0, w.creator.TransfersUsed, "the transfer is given back")
	assert.Len(t, w.deletedEntitlements, 1, "the new entitlement is removed")
	assert.True(t, oldEnt.Active, "the old entitlement is restored")
	assert.Equal(t, from.ID, w.activeEntitlement().TargetID)

	// The old community has its program boost back.
	assert.Equal(t, ccProgramSubscriptionPrefix+w.creator.ID.Hex(), from.Details.Subscription.ID)
	assert.Equal(t, ccProgramCommunityPlan, from.Details.Subscription.Plan)
	assert.True(t, from.Details.Subscription.Active)
}

// --- Admin: granting extra transfers ---

func ccAdminGrantRequest(t *testing.T, handler ContentCreator, adminID, creatorID string, additional int) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"additional": additional, "reason": "support request"})
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/admin/content-creators/"+creatorID+"/transfers", bytes.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"id": creatorID})
	if adminID != "" {
		req.Header.Set("X-User-ID", adminID)
	}
	rec := httptest.NewRecorder()
	handler.AdminGrantCreatorTransfersHandler(rec, req)
	return rec
}

func (w *transferWorld) withAdmin(handler ContentCreator, admin *models.AdminUser) ContentCreator {
	adminDB := &mocks.AdminDatabase{}
	adminDB.On("FindOne", mock.Anything, mock.Anything).Return(
		func(_ context.Context, _ interface{}, _ ...*options.FindOneOptions) (*models.AdminUser, error) {
			if admin == nil {
				return nil, mongo.ErrNoDocuments
			}
			return admin, nil
		})
	handler.AdminDB = adminDB
	return handler
}

// Granting raises the LIMIT, never lowers the used count, so the record of how
// many moves a creator has made survives.
func TestAdminGrantCreatorTransfers_RaisesTheLimit(t *testing.T) {
	w := newTransferWorld()
	w.creator.TransfersUsed = 3
	handler := w.withAdmin(w.handler(), &models.AdminUser{
		ID: primitive.NewObjectID(), Email: "admin@example.com", Role: "owner", Active: true,
	})

	rec := ccAdminGrantRequest(t, handler, primitive.NewObjectID().Hex(), w.creator.ID.Hex(), 2)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := decodeBody(t, rec)
	assert.Equal(t, true, body["success"])
	assert.Equal(t, float64(3), body["transfersUsed"])
	assert.Equal(t, float64(5), body["transferLimit"])
	assert.Equal(t, float64(2), body["transfersRemaining"])
	// A document with no stored limit must resolve to the default FIRST, so a
	// grant can never lower the allowance below it.
	assert.Equal(t, 5, w.creator.TransferLimit)
}

func TestAdminGrantCreatorTransfers_Authorization(t *testing.T) {
	t.Run("no actor is unauthorized", func(t *testing.T) {
		w := newTransferWorld()
		handler := w.withAdmin(w.handler(), &models.AdminUser{Active: true})

		rec := ccAdminGrantRequest(t, handler, "", w.creator.ID.Hex(), 2)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Equal(t, 0, w.creator.TransferLimit)
	})

	t.Run("an id that is not an admin is forbidden", func(t *testing.T) {
		w := newTransferWorld()
		handler := w.withAdmin(w.handler(), nil)

		rec := ccAdminGrantRequest(t, handler, primitive.NewObjectID().Hex(), w.creator.ID.Hex(), 2)

		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Equal(t, 0, w.creator.TransferLimit)
	})

	t.Run("the amount is bounded", func(t *testing.T) {
		w := newTransferWorld()
		handler := w.withAdmin(w.handler(), &models.AdminUser{Active: true})

		for _, additional := range []int{0, -1, 11} {
			rec := ccAdminGrantRequest(t, handler, primitive.NewObjectID().Hex(), w.creator.ID.Hex(), additional)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "additional=%d", additional)
		}
		assert.Equal(t, 0, w.creator.TransferLimit)
	})
}

// The counters a creator document carries are absent on every document written
// before this feature, so the defaults have to hold without a migration.
func TestCreatorTransferCounterDefaults(t *testing.T) {
	var fresh models.ContentCreator
	assert.Equal(t, 3, fresh.EffectiveTransferLimit())
	assert.Equal(t, 3, fresh.RemainingTransfers())

	used := models.ContentCreator{TransfersUsed: 2}
	assert.Equal(t, 1, used.RemainingTransfers())

	exhausted := models.ContentCreator{TransfersUsed: 3}
	assert.Equal(t, 0, exhausted.RemainingTransfers())

	// A limit lowered below what was already spent reads as 0, never negative.
	lowered := models.ContentCreator{TransfersUsed: 5, TransferLimit: 2}
	assert.Equal(t, 0, lowered.RemainingTransfers())

	granted := models.ContentCreator{TransfersUsed: 3, TransferLimit: 5}
	assert.Equal(t, 2, granted.RemainingTransfers())
}
