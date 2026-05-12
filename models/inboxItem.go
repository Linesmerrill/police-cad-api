package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// InboxItem represents a single fee/fine/notice delivered to a civilian's inbox.
// Generic enough that future systems (shops, judicial verdicts, admin-issued fees) can drop items in.
type InboxItem struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id,omitempty"`
	CommunityID string             `json:"communityId" bson:"communityId"`
	UserID      string             `json:"userId" bson:"userId"`         // owning user (recipient for app-level routing)
	CivilianID  string             `json:"civilianId" bson:"civilianId"` // target civilian whose balance is debited on pay
	Type        string             `json:"type" bson:"type"`             // "fine" | "fee" | "payroll" | "verdict" | "other"
	Source      string             `json:"source" bson:"source"`         // "citation" | "admin" | "judicial" | "shop" | "system"
	Title       string             `json:"title" bson:"title"`
	Body        string             `json:"body,omitempty" bson:"body,omitempty"`
	Amount      int64              `json:"amount" bson:"amount"` // signed cents; positive = owed, negative = credit
	Status      string             `json:"status" bson:"status"` // "pending" | "paid" | "dismissed" | "delinquent" | "contested"
	IssuedBy    string             `json:"issuedBy,omitempty" bson:"issuedBy,omitempty"`
	RefType     string             `json:"refType,omitempty" bson:"refType,omitempty"` // e.g. "criminalHistoryId" | "courtCaseId"
	RefID       string             `json:"refId,omitempty" bson:"refId,omitempty"`
	DueAt       primitive.DateTime `json:"dueAt,omitempty" bson:"dueAt,omitempty"`
	PaidAt      primitive.DateTime `json:"paidAt,omitempty" bson:"paidAt,omitempty"`
	DismissedAt primitive.DateTime `json:"dismissedAt,omitempty" bson:"dismissedAt,omitempty"`
	DismissedBy string             `json:"dismissedBy,omitempty" bson:"dismissedBy,omitempty"`
	// Contest workflow: a civilian can contest a pending fine to push it
	// before a judge. The original due date is preserved and the active
	// due date is pushed out by community.economy.contestExtensionDays.
	ContestedAt   primitive.DateTime `json:"contestedAt,omitempty" bson:"contestedAt,omitempty"`
	ContestReason string             `json:"contestReason,omitempty" bson:"contestReason,omitempty"`
	OriginalDueAt primitive.DateTime `json:"originalDueAt,omitempty" bson:"originalDueAt,omitempty"`
	ResolvedAt    primitive.DateTime `json:"resolvedAt,omitempty" bson:"resolvedAt,omitempty"`
	ResolvedBy    string             `json:"resolvedBy,omitempty" bson:"resolvedBy,omitempty"`
	Resolution    string             `json:"resolution,omitempty" bson:"resolution,omitempty"` // "upheld" | "dismissed"
	CreatedAt     primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt     primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}
