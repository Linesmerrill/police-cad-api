package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// RpPromoOffense is a single moderation action taken against a user for abusing
// the RP server promotion feature (for example, spinning up multiple
// communities to evade the per-community posting cooldown). One document is
// stored per offense, keyed by the offending user so escalation can span every
// community they own.
//
// There is deliberately no "expired" status and no expiry cron. Relying on a
// stored flag that nothing flips is a known footgun in this codebase, so an
// offense is only ever "active" or "reversed". A ban is considered in force
// when Status == "active" AND (ExpiresAt == nil OR ExpiresAt > now); escalation
// counts every "active" offense (expired-by-time ones still count) and excludes
// "reversed" ones (those were overturned on appeal).
type RpPromoOffense struct {
	ID    primitive.ObjectID `json:"_id,omitempty" bson:"_id,omitempty"`
	Scope string             `json:"scope" bson:"scope"` // "user" | "community"
	// User-scoped bans set UserID (the banned poster); community-scoped bans set
	// CommunityID instead and leave UserID empty, so escalation counts and the
	// enforcement lookup stay unambiguous. Username/Email are the notified
	// contact either way (the banned user, or the community's owner).
	UserID        string `json:"userId,omitempty" bson:"userId,omitempty"`               // banned user (user scope)
	CommunityID   string `json:"communityId,omitempty" bson:"communityId,omitempty"`     // banned community (community scope)
	CommunityName string `json:"communityName,omitempty" bson:"communityName,omitempty"` // captured at issue time
	Username      string `json:"username,omitempty" bson:"username,omitempty"`           // notified contact username
	Email         string `json:"email,omitempty" bson:"email,omitempty"`                 // notified contact email
	OffenseNumber  int                 `json:"offenseNumber" bson:"offenseNumber"`                   // 1, 2, 3, 4+ — drives the penalty ladder
	Reason         string              `json:"reason" bson:"reason"`                                 // admin-supplied reason
	Evidence       []RpPromoEvidence   `json:"evidence,omitempty" bson:"evidence,omitempty"`         // the promos that justify the ban
	IssuedBy       string              `json:"issuedBy" bson:"issuedBy"`                             // admin email
	IssuedAt       primitive.DateTime  `json:"issuedAt" bson:"issuedAt"`
	ExpiresAt      *primitive.DateTime `json:"expiresAt,omitempty" bson:"expiresAt,omitempty"`       // nil = permanent
	Status         string              `json:"status" bson:"status"`                                 // "active" | "reversed"
	ReversedBy     string              `json:"reversedBy,omitempty" bson:"reversedBy,omitempty"`     // admin email
	ReversedAt     *primitive.DateTime `json:"reversedAt,omitempty" bson:"reversedAt,omitempty"`
	ReversalReason string              `json:"reversalReason,omitempty" bson:"reversalReason,omitempty"`
	EmailSentAt    *primitive.DateTime `json:"emailSentAt,omitempty" bson:"emailSentAt,omitempty"`
}

// RpPromoEvidence is a snapshot of a single promotion that contributed to an
// offense, retained on the offense document so the evidence survives even if
// the underlying promo is later removed or ages out of the community's history.
type RpPromoEvidence struct {
	CommunityID   string             `json:"communityId" bson:"communityId"`
	CommunityName string             `json:"communityName" bson:"communityName"`
	ServerName    string             `json:"serverName" bson:"serverName"`
	InviteURL     string             `json:"inviteUrl" bson:"inviteUrl"`
	MessageID     string             `json:"messageId,omitempty" bson:"messageId,omitempty"`
	PostedAt      primitive.DateTime `json:"postedAt" bson:"postedAt"`
}

// RpPromoOffense status values.
const (
	RpPromoOffenseStatusActive   = "active"
	RpPromoOffenseStatusReversed = "reversed"
)

// RpPromoOffense scope values.
const (
	RpPromoOffenseScopeUser      = "user"
	RpPromoOffenseScopeCommunity = "community"
)
