package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// HousePromo is an internal promotion shown in place of a paid ad that did not
// fill. Staff-authored and global, like Countdown, and keyed by Slug so more
// than one can exist and each surface can ask for the ones it cares about.
//
// It lives in the database rather than in client constants for the same reason
// the countdown does, only more sharply: a mobile release takes about a day to
// reach users, and a promo is something you sometimes need to stop showing
// today. Flipping Active on this document clears every surface within the
// response cache TTL.
//
// Deliberately no client-side fallback, which is where this parts company with
// Countdown. A countdown rendering a known date while the API is unreachable is
// useful; a promo that keeps running when it cannot be switched off is not.
type HousePromo struct {
	ID   primitive.ObjectID `json:"_id"  bson:"_id"`
	Slug string             `json:"slug" bson:"slug"`

	// Eyebrow is the small label above the title, e.g. "A quick one from us".
	// It is what tells a reader this is ours and not a bought ad.
	Eyebrow string `json:"eyebrow" bson:"eyebrow"`
	Title   string `json:"title"   bson:"title"`
	Body    string `json:"body"    bson:"body"`

	CTALabel string `json:"ctaLabel" bson:"ctaLabel"`
	CTAURL   string `json:"ctaUrl"   bson:"ctaUrl"`

	// Theme names the visual treatment the clients apply, e.g. "osrs".
	Theme string `json:"theme" bson:"theme"`
	// Surfaces limits where the promo shows: any of "web", "mobile". Empty
	// means every surface.
	Surfaces []string `json:"surfaces" bson:"surfaces"`

	// EndsAt retires the promo on its own. Zero means it runs until Active is
	// set false by hand. Enforced server-side as well as on the clients, so a
	// campaign that has finished cannot keep being served because nobody
	// remembered to flip the flag.
	EndsAt primitive.DateTime `json:"endsAt,omitempty" bson:"endsAt,omitempty"`

	// Active gates visibility without deleting the record. This is the kill
	// switch.
	Active bool `json:"active" bson:"active"`

	CreatedAt primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}
