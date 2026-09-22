package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// ContentOffense is a single moderation action taken after a staff member
// upheld a user report. One document per offense, so escalation can be counted
// per account or per community.
//
// The lifecycle deliberately mirrors RpPromoOffense: there is no "expired"
// status and no expiry cron, because a stored flag that nothing flips is a
// known footgun in this codebase. An offense is only ever "active" or
// "reversed". A penalty is in force when Status == "active" AND (ExpiresAt ==
// nil OR ExpiresAt > now). Escalation counts every "active" offense (ones whose
// suspension has already elapsed still count toward the next rung) and excludes
// "reversed" ones, which were overturned on appeal.
//
// An offense is created only when a staff member UPHOLDS a report, never when
// one is filed. Reports are unverified accusations, usually from someone in a
// dispute with the target, so a filed report leaves no mark on the accused.
type ContentOffense struct {
	ID    primitive.ObjectID `json:"_id,omitempty" bson:"_id,omitempty"`
	Scope string             `json:"scope" bson:"scope"` // "user" | "community"

	// User-scoped offenses set UserID; community-scoped offenses set
	// CommunityID and leave UserID empty, so the escalation count and the
	// enforcement lookup stay unambiguous.
	UserID        string `json:"userId,omitempty" bson:"userId,omitempty"`
	CommunityID   string `json:"communityId,omitempty" bson:"communityId,omitempty"`
	CommunityName string `json:"communityName,omitempty" bson:"communityName,omitempty"`

	// Username and Email are the notified contact, captured at issue time: the
	// offending user, or the community's owner for a community-scoped offense.
	Username string `json:"username,omitempty" bson:"username,omitempty"`
	Email    string `json:"email,omitempty" bson:"email,omitempty"`

	// ReportIDs are the reports this offense was issued from. Several reports
	// about the same behavior can be upheld together as one offense, so that a
	// pile-on does not escalate someone three rungs in one sitting.
	ReportIDs []string `json:"reportIds" bson:"reportIds"`

	ReportedIssue string `json:"reportedIssue" bson:"reportedIssue"` // "Hate", "Spam", ...
	Tier          string `json:"tier" bson:"tier"`                   // minor | serious
	OffenseNumber int    `json:"offenseNumber" bson:"offenseNumber"` // 1-based, drives the ladder
	Penalty       string `json:"penalty" bson:"penalty"`             // warning | suspension | permanent

	Reason   string             `json:"reason" bson:"reason"` // staff-written, internal only
	IssuedBy string             `json:"issuedBy" bson:"issuedBy"`
	IssuedAt primitive.DateTime `json:"issuedAt" bson:"issuedAt"`

	// ExpiresAt is nil for a warning and for a permanent removal. Those two are
	// told apart by Penalty, never by a nil expiry.
	ExpiresAt *primitive.DateTime `json:"expiresAt,omitempty" bson:"expiresAt,omitempty"`

	Status         string              `json:"status" bson:"status"` // "active" | "reversed"
	ReversedBy     string              `json:"reversedBy,omitempty" bson:"reversedBy,omitempty"`
	ReversedAt     *primitive.DateTime `json:"reversedAt,omitempty" bson:"reversedAt,omitempty"`
	ReversalReason string              `json:"reversalReason,omitempty" bson:"reversalReason,omitempty"`

	// EmailSentAt is stamped only on a successful send. There is no bounce
	// handling anywhere in the product, so a send is best-effort by definition
	// and a null here means "we could not tell them", not "we chose not to".
	EmailSentAt *primitive.DateTime `json:"emailSentAt,omitempty" bson:"emailSentAt,omitempty"`
}

// ContentOffense status values.
const (
	ContentOffenseStatusActive   = "active"
	ContentOffenseStatusReversed = "reversed"
)

// ContentOffense scope values.
const (
	ContentOffenseScopeUser      = "user"
	ContentOffenseScopeCommunity = "community"
)

// Suspension is a time-limited lock on an account, written to user.suspension.
//
// It is deliberately NOT stored in the isDeactivated / deactivatedAt /
// restoreUntil fields. Those belong to account deactivation, which a user can
// do to themselves, and restoreUntil already means "the window in which you can
// restore your own account". Overloading them would make a ban and a
// self-deletion indistinguishable, and would collide with the self-service
// restore window.
type Suspension struct {
	// Until is nil for a permanent suspension.
	Until     *primitive.DateTime `json:"until,omitempty" bson:"until,omitempty"`
	OffenseID string              `json:"offenseId,omitempty" bson:"offenseId,omitempty"`
	Reason    string              `json:"reason,omitempty" bson:"reason,omitempty"`
	IssuedBy  string              `json:"issuedBy,omitempty" bson:"issuedBy,omitempty"`
	IssuedAt  primitive.DateTime  `json:"issuedAt,omitempty" bson:"issuedAt,omitempty"`
}

// ListingSuspension removes a community from every discovery surface for a
// period, without touching community.visibility.
//
// visibility is the owner's own setting. Using it as a penalty would mean that
// on relisting we could not tell whether the owner had set it private
// themselves, so we would hand back a state we invented. Keeping the penalty in
// its own field also lets it expire on its own.
type ListingSuspension struct {
	// Until is nil for an indefinite delisting.
	Until     *primitive.DateTime `json:"until,omitempty" bson:"until,omitempty"`
	OffenseID string              `json:"offenseId,omitempty" bson:"offenseId,omitempty"`
	Reason    string              `json:"reason,omitempty" bson:"reason,omitempty"`
	IssuedBy  string              `json:"issuedBy,omitempty" bson:"issuedBy,omitempty"`
	IssuedAt  primitive.DateTime  `json:"issuedAt,omitempty" bson:"issuedAt,omitempty"`
}

// LegalHold marks an account whose data must be retained, set when a report is
// escalated to the NCMEC CyberTipline.
//
// A completed CyberTipline submission triggers a one-year preservation
// obligation for the associated data. Every deletion path must refuse while
// this is set: "permanently removed" for a held account means suspended and
// retained, never purged.
type LegalHold struct {
	SetAt     primitive.DateTime  `json:"setAt" bson:"setAt"`
	SetBy     string              `json:"setBy" bson:"setBy"`
	ReportID  string              `json:"reportId,omitempty" bson:"reportId,omitempty"`
	ExpiresAt *primitive.DateTime `json:"expiresAt,omitempty" bson:"expiresAt,omitempty"`
	Note      string              `json:"note,omitempty" bson:"note,omitempty"`
}
