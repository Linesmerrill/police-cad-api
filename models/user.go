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
	// Suspension is a moderation lock from an upheld report. Deliberately kept
	// out of the IsDeactivated/RestoreUntil fields above: those belong to
	// account deactivation, which a user can also do to themselves, and
	// RestoreUntil already means the self-service restore window.
	Suspension *Suspension `json:"suspension,omitempty" bson:"suspension,omitempty"`
	// LegalHold blocks every deletion path while set. Written when a report is
	// escalated to the CyberTipline, which carries a preservation obligation.
	// Never serialised: user documents are served by public endpoints, and the
	// mere presence of a hold says a child-safety escalation happened.
	LegalHold *LegalHold `json:"-" bson:"legalHold,omitempty"`
	ResetPasswordToken       string                `json:"resetPasswordToken" bson:"resetPasswordToken"`
	ResetPasswordExpires     interface{}           `json:"resetPasswordExpires" bson:"resetPasswordExpires"`
	EmailVerified            *bool                 `json:"emailVerified" bson:"emailVerified"`
	EmailVerificationToken   string                `json:"emailVerificationToken" bson:"emailVerificationToken"`
	EmailVerificationExpires interface{}           `json:"emailVerificationExpires" bson:"emailVerificationExpires"`
	DismissedTutorials       []string              `json:"dismissedTutorials,omitempty" bson:"dismissedTutorials,omitempty"`
	AlertSoundsEnabled       bool                  `json:"alertSoundsEnabled" bson:"alertSoundsEnabled"`
	SeenAnnouncements        []string              `json:"seenAnnouncements,omitempty" bson:"seenAnnouncements,omitempty"`
	// QuickActionUsage counts how many times the user has tapped each department
	// dashboard quick action (keyed by a stable action slug). Powers the "Most
	// used" row; stored on the user doc so it follows the user across devices.
	QuickActionUsage         map[string]int        `json:"quickActionUsage,omitempty" bson:"quickActionUsage,omitempty"`
	// Discord has been written by the website's Mongoose model for years but
	// was never declared here, so Go reads of a user dropped it. Declared now
	// because the Developer API's claim flow matches a provisioned Discord
	// identity against it. Read-only from Go: the website owns these writes.
	Discord          *DiscordIdentity `json:"discord,omitempty" bson:"discord,omitempty"`
	DiscordConnected bool             `json:"discordConnected" bson:"discordConnected"`

	// The Developer API provisioning fields. See docs/DEVELOPER_API.md.
	//
	// DeveloperProvisioned marks an account a third-party integration created
	// on a player's behalf. Claimed says whether the player has since proved
	// they own it: nil means an ordinary account (everything that existed
	// before this feature), false means provisioned and not yet claimed, and
	// such an account must never be able to log in.
	DeveloperProvisioned     bool                `json:"developerProvisioned,omitempty" bson:"developerProvisioned,omitempty"`
	Claimed                  *bool               `json:"claimed,omitempty" bson:"claimed,omitempty"`
	ProvisionedByKeyID       string              `json:"provisionedByKeyId,omitempty" bson:"provisionedByKeyId,omitempty"`
	ProvisionedByDeveloperID string              `json:"provisionedByDeveloperId,omitempty" bson:"provisionedByDeveloperId,omitempty"`
	ProvisionedAt            *primitive.DateTime `json:"provisionedAt,omitempty" bson:"provisionedAt,omitempty"`
	ClaimedAt                *primitive.DateTime `json:"claimedAt,omitempty" bson:"claimedAt,omitempty"`
	// GameIdentities is a convenience copy for the account page. The
	// player_identities collection is authoritative, because it carries the
	// unique index that makes a player upsert idempotent.
	GameIdentities []GameIdentity `json:"gameIdentities,omitempty" bson:"gameIdentities,omitempty"`

	CreatedAt                interface{}           `json:"createdAt" bson:"createdAt"`
	UpdatedAt                interface{}           `json:"updatedAt" bson:"updatedAt"`
}

// DiscordIdentity is the linked Discord account the website records.
type DiscordIdentity struct {
	ID            string `json:"id" bson:"id"`
	Username      string `json:"username" bson:"username"`
	Discriminator string `json:"discriminator,omitempty" bson:"discriminator,omitempty"`
}

// GameIdentity is one game identifier attached to an account.
type GameIdentity struct {
	Provider   string             `json:"provider" bson:"provider"`
	Identifier string             `json:"identifier" bson:"identifier"`
	AddedAt    primitive.DateTime `json:"addedAt" bson:"addedAt"`
}

// IsClaimed reports whether a player owns this account.
//
// Nil reads as claimed, which is the only safe default: every account that
// existed before the Developer API has no Claimed field, and treating those as
// unclaimed would lock out the entire user base. Only an account explicitly
// marked false — one a developer key created and nobody has taken ownership of
// — is unclaimed.
func (u UserDetails) IsClaimed() bool {
	return u.Claimed == nil || *u.Claimed
}

// IsClaimed reports whether a player owns this account.
func (u User) IsClaimed() bool {
	return u.Details.IsClaimed()
}

// CanAuthenticate reports whether this account may be signed into at all.
//
// An unclaimed provisioned account cannot. It holds no email and no password
// hash, so no login path could succeed anyway, but this is checked explicitly
// as well: the account exists, a developer knows its id, and one future code
// path that sets an email would otherwise quietly turn it into a way in.
func (u User) CanAuthenticate() bool {
	return u.IsClaimed()
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
