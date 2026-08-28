package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// Countdown modes. A countdown either targets a wall-clock date in whatever
// timezone the viewer happens to be in, or one fixed instant shared globally.
const (
	// CountdownModeLocalMidnight targets midnight on LaunchDate in the
	// viewer's own timezone. Correct for staggered releases that unlock at
	// local midnight region by region, which is how console storefronts list
	// GTA 6: every market's listing lands on its own local midnight rather
	// than one synchronized worldwide moment.
	CountdownModeLocalMidnight = "localMidnight"
	// CountdownModeInstant targets LaunchesAt, one instant for everyone,
	// rendered in each viewer's local time. Correct for a synchronized
	// worldwide unlock.
	CountdownModeInstant = "instant"
)

// Countdown is a platform-wide launch countdown surfaced on the website, the
// mobile app, and the Discord bot. It is staff-authored and applies to
// everyone, like ChangelogPost, but is keyed by Slug so more than one can exist
// and each surface can ask for the ones it cares about.
//
// The date lives here rather than in client constants because a mobile release
// can take a day to reach users: if the publisher moves the date, a hardcoded
// countdown keeps counting toward the wrong one until the next build ships.
// Editing this document corrects every surface within the response cache TTL.
type Countdown struct {
	ID   primitive.ObjectID `json:"_id"  bson:"_id"`
	Slug string             `json:"slug" bson:"slug"`

	Title    string `json:"title"    bson:"title"`
	Subtitle string `json:"subtitle" bson:"subtitle"`

	// LaunchDate is the local calendar date, "YYYY-MM-DD". Used by
	// CountdownModeLocalMidnight. Stored as a string on purpose: it is a
	// wall-clock date with no timezone of its own, and storing it as a
	// timestamp would silently bind it to one.
	LaunchDate string `json:"launchDate" bson:"launchDate"`
	// LaunchesAt is the absolute instant used by CountdownModeInstant. It is
	// also what the Discord bot renders, since Discord timestamps localize
	// themselves per viewer.
	LaunchesAt primitive.DateTime `json:"launchesAt" bson:"launchesAt"`
	// Mode selects which of the two above drives the target.
	Mode string `json:"mode" bson:"mode"`

	// Theme names the visual treatment the clients apply, e.g. "gta6".
	Theme string `json:"theme" bson:"theme"`
	// Surfaces limits where the countdown shows: any of "web", "mobile",
	// "bot". Empty means all surfaces.
	Surfaces []string `json:"surfaces" bson:"surfaces"`

	// PostLaunchHours is how long the clients keep showing a launched state
	// before hiding the countdown entirely. Without it a passed countdown
	// becomes a negative timer, or dead UI nobody remembers to take down.
	PostLaunchHours int `json:"postLaunchHours" bson:"postLaunchHours"`

	CTALabel string `json:"ctaLabel,omitempty" bson:"ctaLabel,omitempty"`
	CTAURL   string `json:"ctaUrl,omitempty"   bson:"ctaUrl,omitempty"`

	// Active gates visibility without deleting the record.
	Active bool `json:"active" bson:"active"`

	CreatedAt primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}
