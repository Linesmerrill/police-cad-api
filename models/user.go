package models

import (
	"encoding/json"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// User holds the structure for the user collection in mongo
type User struct {
	ID      string      `json:"_id" bson:"_id"`
	Details UserDetails `json:"user" bson:"user"`
	Version int32       `json:"__v" bson:"__v"`
}

// MarshalJSON strips secret fields from any JSON representation of a user.
//
// The password hash and the password-reset / email-verification tokens are
// stored on the user document but must never appear in an API response —
// returning the reset token in particular allowed account takeover (read a
// user's token, then complete a password reset for them). Because the same
// UserDetails struct is used to decode signup requests, we can't simply drop
// the json tags; this Marshaler only affects OUTPUT (json.Marshal). Input
// decoding (json.Unmarshal) and all database reads/writes (bson) are
// unaffected, so signup still reads the password and the reset flow — which
// queries the token directly in the DB — keeps working.
func (u UserDetails) MarshalJSON() ([]byte, error) {
	// alias drops the methods on UserDetails so json.Marshal uses the default
	// struct encoding (and doesn't recurse into this method).
	type alias UserDetails
	a := alias(u)
	a.Password = ""
	a.ResetPasswordToken = ""
	a.ResetPasswordExpires = nil
	a.EmailVerificationToken = ""
	a.EmailVerificationExpires = nil
	return json.Marshal(a)
}

// UserDetails holds the structure for the inner user structure as defined in the user collection in mongo
type UserDetails struct {
	Address                  string                `json:"address" bson:"address"`
	ActiveCommunity          string                `json:"activeCommunity" bson:"activeCommunity"` // will be deprecated, use lastAccessedCommunity and communities
	CallSign                 string                `json:"callSign" bson:"callSign"`
	DispatchStatus           string                `json:"dispatchStatus" bson:"dispatchStatus"`
	DispatchStatusSetBy      string                `json:"dispatchStatusSetBy" bson:"dispatchStatusSetBy"`
	LastAccessedCommunity    LastAccessedCommunity `json:"lastAccessedCommunity" bson:"lastAccessedCommunity"`
	Email                    string                `json:"email" bson:"email"`
	Name                     string                `json:"name" bson:"name"`
	Notes                    []Note                `json:"notes" bson:"notes"`
	Username                 string                `json:"username" bson:"username"`
	Password                 string                `json:"password" bson:"password"`
	ProfilePicture           string                `json:"profilePicture" bson:"profilePicture"`
	BackgroundImage          string                `json:"backgroundImage" bson:"backgroundImage"`
	Friends                  []Friend              `json:"friends" bson:"friends"`
	Notifications            []Notification        `json:"notifications" bson:"notifications"`
	Communities              []UserCommunity       `json:"communities" bson:"communities"`
	IsOnline                 bool                  `json:"isOnline" bson:"isOnline"`
	Subscription             Subscription          `json:"subscription" bson:"subscription"`
	IsDeactivated            bool                  `json:"isDeactivated" bson:"isDeactivated"`
	DeactivatedAt            interface{}           `json:"deactivatedAt" bson:"deactivatedAt"`
	RestoreUntil             interface{}           `json:"restoreUntil" bson:"restoreUntil"`
	DeactivationReason       string                `json:"deactivationReason,omitempty" bson:"deactivationReason,omitempty"`
	DeactivatedByAdminID     string                `json:"deactivatedByAdminId,omitempty" bson:"deactivatedByAdminId,omitempty"`
	ResetPasswordToken       string                `json:"resetPasswordToken" bson:"resetPasswordToken"`
	ResetPasswordExpires     interface{}           `json:"resetPasswordExpires" bson:"resetPasswordExpires"`
	EmailVerified            *bool                 `json:"emailVerified" bson:"emailVerified"`
	EmailVerificationToken   string                `json:"emailVerificationToken" bson:"emailVerificationToken"`
	EmailVerificationExpires interface{}           `json:"emailVerificationExpires" bson:"emailVerificationExpires"`
	DismissedTutorials       []string              `json:"dismissedTutorials,omitempty" bson:"dismissedTutorials,omitempty"`
	AlertSoundsEnabled       bool                  `json:"alertSoundsEnabled" bson:"alertSoundsEnabled"`
	SeenAnnouncements        []string              `json:"seenAnnouncements,omitempty" bson:"seenAnnouncements,omitempty"`
	CreatedAt                interface{}           `json:"createdAt" bson:"createdAt"`
	UpdatedAt                interface{}           `json:"updatedAt" bson:"updatedAt"`
}

// Note holds the structure for a note
type Note struct {
	ID        primitive.ObjectID `json:"_id" bson:"_id"`
	Title     string             `json:"title" bson:"title"`
	Content   string             `json:"content" bson:"content"`
	CreatedAt interface{}        `json:"createdAt" bson:"createdAt"`
	UpdatedAt interface{}        `json:"updatedAt" bson:"updatedAt"`
}

// Subscription holds the structure for a user's subscription
type Subscription struct {
	ID                 string      `json:"id" bson:"id"`
	Plan               string      `json:"plan" bson:"plan"`
	Active             bool        `json:"active" bson:"active"`
	Source             string      `json:"source" bson:"source"`                     // "stripe" | "app_store" | "" (empty for legacy)
	StripeCustomerID   string      `json:"stripeCustomerId" bson:"stripeCustomerId"` // Stripe customer ID for portal access
	CancelAt           interface{} `json:"cancelAt" bson:"cancelAt"`
	CurrentPeriodStart interface{} `json:"currentPeriodStart" bson:"currentPeriodStart"`
	CurrentPeriodEnd   interface{} `json:"currentPeriodEnd" bson:"currentPeriodEnd"`
	PurchaseDate       string      `json:"purchaseDate" bson:"purchaseDate"`     // Used for Community In-App Purchases
	ExpirationDate     string      `json:"expirationDate" bson:"expirationDate"` // Used for Community In-App Purchases
	DurationMonths     int         `json:"durationMonths" bson:"durationMonths"` // Used for Community In-App Purchases
	IsAnnual           bool        `json:"isAnnual" bson:"isAnnual"`
	CreatedAt          interface{} `json:"createdAt" bson:"createdAt"`
	UpdatedAt          interface{} `json:"updatedAt" bson:"updatedAt"`

	// KickbackBanner is set when a price-drop kickback was applied to this
	// user. Frontend reads it to render a "thank-you, N months added" banner
	// and hides it after ExpiresAt. Nil when no active kickback.
	KickbackBanner *KickbackBanner `json:"kickbackBanner,omitempty" bson:"kickbackBanner,omitempty"`
}

// KickbackBanner holds the user-visible summary of a price-drop kickback.
type KickbackBanner struct {
	Months         int         `json:"months" bson:"months"`
	OriginalAmount float64     `json:"originalAmount" bson:"originalAmount"`
	ExpiresAt      interface{} `json:"expiresAt" bson:"expiresAt"`
	CreatedAt      interface{} `json:"createdAt" bson:"createdAt"`
}

// Friend holds the structure for a friend
type Friend struct {
	FriendID  string      `json:"friend_id" bson:"friend_id"`
	Status    string      `json:"status" bson:"status"` // e.g., "pending", "approved"
	CreatedAt interface{} `json:"created_at" bson:"created_at"`
}

// Notification holds the structure for a notification
type Notification struct {
	ID         string      `json:"_id" bson:"_id"`
	SentFromID string      `json:"sentFromID" bson:"sentFromID"`
	SentToID   string      `json:"sentToID" bson:"sentToID"`
	Type       string      `json:"type" bson:"type"`
	Message    string      `json:"message" bson:"message"`
	Data1      string      `json:"data1" bson:"data1"`
	Data2      string      `json:"data2" bson:"data2"`
	Data3      string      `json:"data3" bson:"data3"`
	Data4      string      `json:"data4" bson:"data4"`
	Seen       bool        `json:"seen" bson:"seen"`
	CreatedAt  interface{} `json:"createdAt" bson:"createdAt"`
}

// UserCommunity holds the structure for a user community, mainly used to store the status of a community request for the user
type UserCommunity struct {
	ID          string `json:"_id" bson:"_id"`
	CommunityID string `json:"communityId" bson:"communityId"`
	Status      string `json:"status" bson:"status"`
}
