package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// Report represents a player report
type Report struct {
	ID                primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	ItemID            string             `bson:"itemId" json:"itemId"`               // The ID of the item being reported, userID, communityID, etc.
	ItemType          string             `bson:"itemType" json:"itemType"`           // The database name of the item being reported, user, community, etc.
	ReportType        ReportType         `bson:"reportType" json:"reportType"`       // USER_REPORT, AD_REPORT, etc.
	ReportedIssue     string             `bson:"reportedIssue" json:"reportedIssue"` // hate, scan, etc.
	AdditionalDetails string             `bson:"additionalDetails" json:"additionalDetails"`
	ReportedByID      string             `bson:"reportedById" json:"reportedById"`
	Active            bool               `bson:"active" json:"active"`
	ActionTaken       string             `bson:"actionTaken" json:"actionTaken"` // warning, ban, etc.
	CreatedAt         primitive.DateTime `bson:"createdAt" json:"createdAt"`

	// Status is the moderation workflow state. Reports created before this
	// field existed have none, and are treated as ReportStatusNew on read: a
	// stale API deploy must never be able to hide a report by omitting a field.
	Status string `bson:"status,omitempty" json:"status,omitempty"`

	// Location says where the reported behaviour happened. Reports about
	// Discord, Xbox or an in-game voice chat are not ours to judge: we cannot
	// see the content, cannot verify it, and the platform that could never
	// hears about it. Clients ask first and send people elsewhere, so a report
	// that reaches us should always be LocationInApp.
	//
	// Empty means an older mobile build that predates the question. Those are
	// still accepted and labelled in the queue rather than thrown away.
	Location string `bson:"location,omitempty" json:"location,omitempty"`

	// ImpersonatedName is who the reported account is pretending to be, asked
	// only for Impersonation. Without it staff cannot check the claim.
	ImpersonatedName string `bson:"impersonatedName,omitempty" json:"impersonatedName,omitempty"`

	// Target is the one piece of content the report is about. Without it a
	// report is an accusation against a person with nothing attached, which
	// staff cannot verify and which is most of what has been filed.
	Target *ReportTarget `bson:"target,omitempty" json:"target,omitempty"`

	// Snapshot is what that content said when it was reported, copied by the
	// server from the content itself. Editing or deleting it afterwards can no
	// longer erase what was reported.
	Snapshot *ReportSnapshot `bson:"snapshot,omitempty" json:"snapshot,omitempty"`

	// SeverityRank is stored so the queue can sort by it. Escalate first, then
	// welfare, serious and minor. See ReportSeverityRank.
	SeverityRank *int `bson:"severityRank,omitempty" json:"severityRank,omitempty"`

	// Tier is stored at review time rather than derived on every read, so that
	// a later change to the tier map cannot silently rewrite the basis on which
	// a past decision was made.
	Tier string `bson:"tier,omitempty" json:"tier,omitempty"`

	ReviewedByID   string              `bson:"reviewedById,omitempty" json:"reviewedById,omitempty"`
	ReviewedByName string              `bson:"reviewedByName,omitempty" json:"reviewedByName,omitempty"`
	ReviewedAt     *primitive.DateTime `bson:"reviewedAt,omitempty" json:"reviewedAt,omitempty"`

	// InternalNote is staff-only and never leaves the admin console.
	InternalNote string `bson:"internalNote,omitempty" json:"internalNote,omitempty"`

	// OffenseID links to the content_offenses row created when this report was
	// upheld. Empty for dismissed, escalated and welfare reports.
	OffenseID string `bson:"offenseId,omitempty" json:"offenseId,omitempty"`

	// Escalation, for Child Safety reports handed to the NCMEC CyberTipline.
	EscalatedAt *primitive.DateTime `bson:"escalatedAt,omitempty" json:"escalatedAt,omitempty"`
	EscalatedBy string              `bson:"escalatedBy,omitempty" json:"escalatedBy,omitempty"`

	// ContentRemovedAt is set when staff took the reported content down. The
	// snapshot is kept: it is the record of what was there.
	ContentRemovedAt *primitive.DateTime `bson:"contentRemovedAt,omitempty" json:"contentRemovedAt,omitempty"`

	UpdatedAt *primitive.DateTime `bson:"updatedAt,omitempty" json:"updatedAt,omitempty"`

	// DecisionID is shared by every report closed by the same decision. A
	// dismissal closes the whole case, so reopening one has to find the others
	// it closed; this is how.
	DecisionID string `bson:"decisionId,omitempty" json:"decisionId,omitempty"`

	// History is every decision and reopen on this report, oldest first. The
	// reviewedBy fields only ever hold the latest decision; this keeps the
	// ones before it, including who reopened a report and why.
	History []ReportEvent `bson:"history,omitempty" json:"history,omitempty"`
}

// ReportEvent is one entry in a report's history.
type ReportEvent struct {
	Action         string             `bson:"action" json:"action"` // resolved | dismissed | welfare | escalated | reopened
	PreviousStatus string             `bson:"previousStatus,omitempty" json:"previousStatus,omitempty"`
	By             string             `bson:"by" json:"by"` // admin display name, never an email
	ByID           string             `bson:"byId,omitempty" json:"byId,omitempty"`
	Reason         string             `bson:"reason,omitempty" json:"reason,omitempty"`
	DecisionID     string             `bson:"decisionId,omitempty" json:"decisionId,omitempty"`
	At             primitive.DateTime `bson:"at" json:"at"`
}

// Report history actions.
const (
	ReportEventReopened = "reopened"
)

// IsReopenable reports whether a closed report can be put back in the queue.
//
// Only reports closed with no action qualify. An upheld report carries a
// strike, and reopening it would leave that strike standing on a report marked
// new; the strike is undone with Reverse instead. An escalated report has been
// handed to the CyberTipline under a legal hold, and taking it back out of the
// queue's escalated state is not something a misclick should be able to do.
func (r Report) IsReopenable() bool {
	switch r.EffectiveStatus() {
	case ReportStatusDismissed, ReportStatusWelfare, ReportStatusOffPlatform:
		return true
	default:
		return false
	}
}

// Where the reported behaviour happened.
const (
	// LocationInApp is the only location we accept a report for.
	LocationInApp = "in_app"
	// LocationUnknown is an older client that never asked.
	LocationUnknown = ""
)

// EffectiveLocation returns the report's location, with the empty value of an
// older client called out rather than silently reading as in-app.
func (r Report) EffectiveLocation() string {
	if r.Location == "" {
		return LocationUnknown
	}
	return r.Location
}

// ReportTarget points at one piece of content.
type ReportTarget struct {
	// Kind is what sort of content it is, from the registry of reportable
	// content. See ReportableKinds.
	Kind string `bson:"kind" json:"kind"`
	// ID is the content's own id. For content that lives inside another
	// document, such as a comment, ParentID is the document holding it.
	ID       string `bson:"id" json:"id"`
	ParentID string `bson:"parentId,omitempty" json:"parentId,omitempty"`
	// Fields are the specific parts being reported, such as the name or the
	// image. Empty means the content as a whole.
	Fields []string `bson:"fields,omitempty" json:"fields,omitempty"`
}

// ReportSnapshot is the server's own copy of the reported content, taken when
// the report was filed.
type ReportSnapshot struct {
	// Text is the human-written wording, one entry per reported field, in the
	// order the fields were given.
	Text []SnapshotField `bson:"text,omitempty" json:"text,omitempty"`
	// ImageURLs are any images that were part of it.
	ImageURLs []string `bson:"imageUrls,omitempty" json:"imageUrls,omitempty"`
	// AuthorID and AuthorName are whoever wrote it, resolved server-side. The
	// strike goes to them, not to whoever happens to own the container.
	AuthorID   string `bson:"authorId,omitempty" json:"authorId,omitempty"`
	AuthorName string `bson:"authorName,omitempty" json:"authorName,omitempty"`
	// Where it lives, for the admin link.
	CommunityID string `bson:"communityId,omitempty" json:"communityId,omitempty"`
	// Label is how to describe it in the console, e.g. "Community profile".
	Label     string             `bson:"label,omitempty" json:"label,omitempty"`
	CapturedAt primitive.DateTime `bson:"capturedAt" json:"capturedAt"`
}

// SnapshotField is one reported field and what it said.
type SnapshotField struct {
	Field string `bson:"field" json:"field"`
	Label string `bson:"label,omitempty" json:"label,omitempty"`
	Value string `bson:"value" json:"value"`
}

// Report workflow states.
const (
	// ReportStatusNew is the implicit state of any report with no status set.
	ReportStatusNew = "new"
	// ReportStatusUnderReview means a staff member has picked it up.
	ReportStatusUnderReview = "under_review"
	// ReportStatusResolved means it was upheld and an offense was issued.
	ReportStatusResolved = "resolved"
	// ReportStatusDismissed means no action was warranted. Nothing is recorded
	// against the accused.
	ReportStatusDismissed = "dismissed"
	// ReportStatusOffPlatform closes a report about something that did not
	// happen in this product. Kept separate from dismissed so the backlog of
	// them is countable and does not read as "we decided this was nothing".
	ReportStatusOffPlatform = "off_platform"
	// ReportStatusEscalated means it was handed to the CyberTipline and the
	// account is under a legal hold.
	ReportStatusEscalated = "escalated"
	// ReportStatusWelfare is the terminal state for a self-harm report: read by
	// a person, no penalty, no notice.
	ReportStatusWelfare = "welfare"
)

// EffectiveStatus returns the report's workflow state, treating an unset
// status as new. Every read path must go through this rather than reading
// Status directly.
func (r Report) EffectiveStatus() string {
	if r.Status == "" {
		return ReportStatusNew
	}
	return r.Status
}

// EffectiveTier returns the stored tier, falling back to deriving it from the
// reported issue for reports triaged before the tier was recorded.
func (r Report) EffectiveTier() string {
	if r.Tier != "" {
		return r.Tier
	}
	return ReportTierForIssue(r.ReportedIssue)
}

// IsOpen reports whether the report still needs a decision.
func (r Report) IsOpen() bool {
	switch r.EffectiveStatus() {
	case ReportStatusNew, ReportStatusUnderReview:
		return true
	default:
		return false
	}
}
