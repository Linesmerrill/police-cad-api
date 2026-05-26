package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// SubscriptionEvent records a single subscription-related event for audit and
// forensics. Every webhook delivery (RevenueCat, Stripe), every mobile-app
// subscribe call, and every admin/manual cancel writes one row. The raw
// payload is preserved verbatim so we can replay or reinterpret later.
type SubscriptionEvent struct {
	ID primitive.ObjectID `bson:"_id,omitempty" json:"_id"`

	// Provider event id used for idempotency. RevenueCat event.id (UUID) or
	// Stripe event.id (evt_...). Empty for mobile_app / admin / manual rows.
	ProviderEventID string `bson:"providerEventId,omitempty" json:"providerEventId,omitempty"`

	UserID    string `bson:"userId,omitempty" json:"userId,omitempty"`
	UserEmail string `bson:"userEmail,omitempty" json:"userEmail,omitempty"`

	// Provider is the high-level source of the event.
	// One of: "revenuecat", "stripe", "mobile_app", "admin".
	Provider string `bson:"provider" json:"provider"`

	// Store is the underlying payment processor. One of:
	// "PLAY_STORE", "APP_STORE", "STRIPE", "PROMOTIONAL", "MAC_APP_STORE", "AMAZON".
	Store string `bson:"store,omitempty" json:"store,omitempty"`

	// EventType is provider-specific. Examples:
	// RevenueCat: "INITIAL_PURCHASE", "RENEWAL", "CANCELLATION", "EXPIRATION", ...
	// Stripe:     "checkout.session.completed", "customer.subscription.deleted", ...
	// Mobile:     "mobile_subscribe"
	// Admin:      "admin_unsubscribe", "admin_cancel", "admin_update"
	EventType string `bson:"eventType" json:"eventType"`

	Plan      string `bson:"plan,omitempty" json:"plan,omitempty"`
	IsAnnual  bool   `bson:"isAnnual,omitempty" json:"isAnnual,omitempty"`
	ProductID string `bson:"productId,omitempty" json:"productId,omitempty"`

	// TransactionID: RevenueCat storeTransactionId / Stripe subscription id.
	// OriginalTransactionID: RevenueCat original_transaction_id (the renewable
	// subscription instance — stable across renewals).
	TransactionID         string `bson:"transactionId,omitempty" json:"transactionId,omitempty"`
	OriginalTransactionID string `bson:"originalTransactionId,omitempty" json:"originalTransactionId,omitempty"`

	PriceUSD   float64 `bson:"priceUsd,omitempty" json:"priceUsd,omitempty"`
	PriceLocal float64 `bson:"priceLocal,omitempty" json:"priceLocal,omitempty"`
	Currency   string  `bson:"currency,omitempty" json:"currency,omitempty"`

	PurchasedAt *time.Time `bson:"purchasedAt,omitempty" json:"purchasedAt,omitempty"`
	ExpiresAt   *time.Time `bson:"expiresAt,omitempty" json:"expiresAt,omitempty"`

	// Environment: "SANDBOX" or "PRODUCTION" (from RevenueCat / Stripe livemode).
	Environment string `bson:"environment,omitempty" json:"environment,omitempty"`

	// PreviousSubscription is a snapshot of user.subscription before this
	// event mutated it. Stored as a generic map so we don't couple to schema
	// changes.
	PreviousSubscription map[string]interface{} `bson:"previousSubscription,omitempty" json:"previousSubscription,omitempty"`

	// RawPayload is the verbatim webhook / request body so we can replay.
	RawPayload string `bson:"rawPayload" json:"rawPayload"`

	// ProcessingStatus: "ok" | "user_not_found" | "parse_error" |
	// "skipped_duplicate" | "skipped_unhandled".
	ProcessingStatus string `bson:"processingStatus" json:"processingStatus"`
	ProcessingError  string `bson:"processingError,omitempty" json:"processingError,omitempty"`

	SourceIP  string    `bson:"sourceIp,omitempty" json:"sourceIp,omitempty"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`

	// Kickback fields — set when the price-drop kickback script credits
	// this purchase. Idempotency guard: skip events where KickbackApplied is true.
	KickbackApplied       bool       `bson:"kickbackApplied,omitempty" json:"kickbackApplied,omitempty"`
	KickbackMonthsGranted int        `bson:"kickbackMonthsGranted,omitempty" json:"kickbackMonthsGranted,omitempty"`
	KickbackAppliedAt     *time.Time `bson:"kickbackAppliedAt,omitempty" json:"kickbackAppliedAt,omitempty"`
}
