package models

import (
	"strings"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Sign-in providers an LPC account can be reached through, beyond a password.
//
// Discord is deliberately absent. The website links a Discord account for
// identity display and for the in-game claim shortcut, but it is not a way in,
// and treating it as one here would quietly make it one.
const (
	AuthProviderGoogle = "google"
	AuthProviderApple  = "apple"
)

// AuthIdentity is one external sign-in attached to an LPC account.
//
// The key is Subject — the provider's own immutable id for the person, the
// `sub` claim of a verified ID token. Email is recorded for display and for the
// one-time linking prompt, and is never the thing we match on: people change
// their Google address, Apple hands out per-app relay addresses, and two
// providers can report the same address for different people. A subject never
// changes and never collides.
type AuthIdentity struct {
	ID       primitive.ObjectID `json:"id" bson:"_id"`
	Provider string             `json:"provider" bson:"provider"`
	Subject  string             `json:"subject" bson:"subject"`
	UserID   string             `json:"userId" bson:"userId"`

	// Email as the provider last reported it, and whether they vouched for it.
	// PrivateRelay marks an Apple "Hide My Email" address: real and deliverable,
	// but specific to us, so it must never be shown as the person's own address
	// or used to match them to another account.
	Email         string `json:"email,omitempty" bson:"email,omitempty"`
	EmailVerified bool   `json:"emailVerified" bson:"emailVerified"`
	PrivateRelay  bool   `json:"privateRelay,omitempty" bson:"privateRelay,omitempty"`

	// DisplayName is whatever the provider offered, which is usually nothing.
	// Apple sends a name only on the very first authorization; every later
	// sign-in carries none. Once recorded it is never overwritten with a blank.
	DisplayName string `json:"displayName,omitempty" bson:"displayName,omitempty"`

	CreatedAt    primitive.DateTime  `json:"createdAt" bson:"createdAt"`
	UpdatedAt    primitive.DateTime  `json:"updatedAt" bson:"updatedAt"`
	LastSignInAt *primitive.DateTime `json:"lastSignInAt,omitempty" bson:"lastSignInAt,omitempty"`
}

// IsValidAuthProvider reports whether a provider is one we accept a sign-in from.
func IsValidAuthProvider(provider string) bool {
	switch provider {
	case AuthProviderGoogle, AuthProviderApple:
		return true
	}
	return false
}

// AuthProviderLabel is the provider's name as a person should read it.
func AuthProviderLabel(provider string) string {
	switch provider {
	case AuthProviderGoogle:
		return "Google"
	case AuthProviderApple:
		return "Apple"
	}
	return provider
}

// MaskEmail reduces an address to a recognisable hint.
//
// Used when telling someone an account already exists for an address they just
// proved they own at a provider: enough to recognise their own account, not
// enough to turn the endpoint into a way of reading addresses back out.
func MaskEmail(email string) string {
	trimmed := strings.TrimSpace(email)
	at := strings.LastIndex(trimmed, "@")
	if at <= 0 {
		return ""
	}
	local, domain := trimmed[:at], trimmed[at+1:]
	switch {
	case len(local) == 1:
		local = "*"
	case len(local) <= 4:
		// Keeping the last character too would give away most of a short local
		// part: "dev" as "d*v" is two of its three letters. The domain is
		// already shown, so the first letter is enough to recognise your own
		// account by.
		local = local[:1] + strings.Repeat("*", len(local)-1)
	default:
		local = local[:1] + strings.Repeat("*", len(local)-2) + local[len(local)-1:]
	}
	return local + "@" + domain
}
