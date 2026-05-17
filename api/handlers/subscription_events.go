package handlers

import (
	"context"
	"time"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

// subscriptionEventInput is what callers fill in to record a single event.
// Either UserIDHint (preferred — the Mongo _id as a hex string) or
// TransactionIDHint (Stripe subscription id, used to look up the user when
// the event arrived from Stripe and we don't have the user id directly)
// must be set if we want the recorder to attach a user.
type subscriptionEventInput struct {
	Provider              string
	ProviderEventID       string
	EventType             string
	Store                 string
	UserIDHint            string
	TransactionIDHint     string
	Plan                  string
	IsAnnual              bool
	ProductID             string
	TransactionID         string
	OriginalTransactionID string
	PriceUSD              float64
	PriceLocal            float64
	Currency              string
	PurchasedAt           *time.Time
	ExpiresAt             *time.Time
	Environment           string
	RawPayload            []byte
	SourceIP              string
	// ProcessingStatus / ProcessingError are filled in by the recorder.
}

// recordedEvent is returned to the caller so they can choose to skip
// state mutations on duplicates.
type recordedEvent struct {
	Duplicate bool   // true if (provider, providerEventId) already present
	UserID    string // resolved user _id (hex), or "" if not found
}

// subscriptionEventRecorder bundles the two DBs the recorder needs.
type subscriptionEventRecorder struct {
	UserDB  databases.UserDatabase
	EventDB databases.SubscriptionEventDatabase
}

// isDuplicate returns true if a row for (provider, providerEventId) is
// already present. Webhook providers retry on non-2xx and on socket
// failures, so dedupe is essential. Returns false on lookup errors —
// the unique index is the source of truth and will catch any race.
func (r subscriptionEventRecorder) isDuplicate(ctx context.Context, provider, providerEventID string) bool {
	if r.EventDB == nil || providerEventID == "" {
		return false
	}
	var existing models.SubscriptionEvent
	err := r.EventDB.FindOne(ctx, bson.M{
		"provider":        provider,
		"providerEventId": providerEventID,
	}).Decode(&existing)
	return err == nil
}

// record looks up the user (if possible), snapshots their current
// subscription, and inserts a subscription_events row. Insertion failures
// (other than the dedupe race) are logged but never returned — recording
// must never block a real webhook from being processed.
func (r subscriptionEventRecorder) record(ctx context.Context, in subscriptionEventInput) recordedEvent {
	result := recordedEvent{}
	if r.EventDB == nil {
		// Safety: don't blow up if a handler accidentally runs without SEDB
		// wired (e.g. an old test fixture).
		return result
	}

	var (
		user      models.User
		userFound bool
	)
	if r.UserDB != nil {
		// Resolve the user. Prefer the explicit hex _id (used by mobile,
		// admin, and RevenueCat — RC's app_user_id is the Mongo _id
		// stringified). Fall back to Stripe's subscription id lookup.
		if in.UserIDHint != "" {
			if oid, err := primitive.ObjectIDFromHex(in.UserIDHint); err == nil {
				if err := r.UserDB.FindOne(ctx, bson.M{"_id": oid}).Decode(&user); err == nil {
					userFound = true
				}
			}
		}
		if !userFound && in.TransactionIDHint != "" {
			if err := r.UserDB.FindOne(ctx, bson.M{"user.subscription.id": in.TransactionIDHint}).Decode(&user); err == nil {
				userFound = true
			}
		}
	}

	status := "ok"
	if !userFound {
		status = "user_not_found"
	}

	previous := map[string]interface{}{}
	if userFound {
		result.UserID = user.ID
		// Snapshot the prior subscription as a generic map so we are not
		// coupled to schema changes. Encoding through bson preserves
		// fidelity (dates, etc.).
		raw, _ := bson.Marshal(user.Details.Subscription)
		_ = bson.Unmarshal(raw, &previous)
	}

	evt := models.SubscriptionEvent{
		ProviderEventID:       in.ProviderEventID,
		UserID:                result.UserID,
		UserEmail:             user.Details.Email,
		Provider:              in.Provider,
		Store:                 in.Store,
		EventType:             in.EventType,
		Plan:                  in.Plan,
		IsAnnual:              in.IsAnnual,
		ProductID:             in.ProductID,
		TransactionID:         in.TransactionID,
		OriginalTransactionID: in.OriginalTransactionID,
		PriceUSD:              in.PriceUSD,
		PriceLocal:            in.PriceLocal,
		Currency:              in.Currency,
		PurchasedAt:           in.PurchasedAt,
		ExpiresAt:             in.ExpiresAt,
		Environment:           in.Environment,
		PreviousSubscription:  previous,
		RawPayload:            string(in.RawPayload),
		ProcessingStatus:      status,
		SourceIP:              in.SourceIP,
		CreatedAt:             time.Now(),
	}

	if _, err := r.EventDB.InsertOne(ctx, evt); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			// The unique (provider, providerEventId) index caught a race:
			// two concurrent webhook deliveries of the same event. Tell the
			// caller to skip its state mutation.
			result.Duplicate = true
			zap.S().Infow("subscription_event duplicate insert — concurrent delivery, treating as already processed",
				"provider", in.Provider,
				"providerEventId", in.ProviderEventID)
			return result
		}
		zap.S().Errorw("failed to insert subscription_event — continuing without audit row",
			"provider", in.Provider,
			"eventType", in.EventType,
			"providerEventId", in.ProviderEventID,
			"userId", result.UserID,
			"error", err)
	}

	return result
}
