package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// ChangelogPost is a platform-wide "What's New" entry surfaced in a launch-time
// modal on the website and mobile app. It is intentionally distinct from the
// per-community Announcement (bulletin board) model: changelog posts are authored
// by the LPC team, apply to everyone, and are gated per-user via
// User.SeenAnnouncements so each person sees a given post at most once — even
// after a reinstall, since the seen state lives on the user document, not on the
// device.
type ChangelogPost struct {
	ID    primitive.ObjectID `json:"_id"   bson:"_id"`
	Title string             `json:"title" bson:"title"`
	// Body is rendered as trusted HTML in the modal (authored by staff only).
	Body string `json:"body" bson:"body"`
	// Surfaces limits where the post shows: any of "web", "mobile". Empty means
	// all surfaces.
	Surfaces []string `json:"surfaces" bson:"surfaces"`
	// Active gates visibility without deleting; inactive posts never surface.
	Active bool `json:"active" bson:"active"`
	// PublishedAt is the cutoff used to suppress the back-catalog for new users:
	// a post only shows to users whose account predates PublishedAt.
	PublishedAt primitive.DateTime `json:"publishedAt" bson:"publishedAt"`
	CreatedBy   string             `json:"createdBy,omitempty" bson:"createdBy,omitempty"`
	CreatedAt   primitive.DateTime `json:"createdAt" bson:"createdAt"`
}

// CreateChangelogPostRequest is the POST /api/v1/admin/changelog body.
type CreateChangelogPostRequest struct {
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Surfaces []string `json:"surfaces"`
	// Active defaults to true when omitted (see handler).
	Active *bool `json:"active"`
	// CurrentUser carries the acting admin's roles for the server-side admin
	// gate, matching the other admin-console write endpoints.
	CurrentUser map[string]interface{} `json:"currentUser"`
}
