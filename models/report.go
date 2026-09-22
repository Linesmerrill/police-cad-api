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

	UpdatedAt *primitive.DateTime `bson:"updatedAt,omitempty" json:"updatedAt,omitempty"`
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
