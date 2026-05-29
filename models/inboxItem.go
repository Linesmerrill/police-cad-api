package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// InboxItem represents a single fee/fine/notice delivered to a civilian's inbox.
// Generic enough that future systems (shops, judicial verdicts, admin-issued fees) can drop items in.
type InboxItem struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id,omitempty"`
	CommunityID string             `json:"communityId" bson:"communityId"`
	UserID      string             `json:"userId" bson:"userId"`         // owning user (recipient for app-level routing)
	CivilianID  string             `json:"civilianId" bson:"civilianId"` // target civilian whose balance is debited on pay
	Type        string             `json:"type" bson:"type"`             // "fine" | "fee" | "payroll" | "verdict" | "transfer-sent" | "transfer-received" | "other"
	Source      string             `json:"source" bson:"source"`         // "citation" | "admin" | "judicial" | "shop" | "system" | "peer"
	Title       string             `json:"title" bson:"title"`
	Body        string             `json:"body,omitempty" bson:"body,omitempty"`
	Amount      int64              `json:"amount" bson:"amount"` // signed cents; positive = owed, negative = credit
	Status      string             `json:"status" bson:"status"` // "pending" | "paid" | "dismissed" | "delinquent" | "contested"
	IssuedBy    string             `json:"issuedBy,omitempty" bson:"issuedBy,omitempty"`
	// Peer-transfer fields. Set on transfer-sent / transfer-received items so
	// the UI can render the counterparty's name + the sender's optional memo.
	// Unset on system-issued items (fines, fees, payroll, verdicts).
	CounterpartyCivilianID string `json:"counterpartyCivilianId,omitempty" bson:"counterpartyCivilianId,omitempty"`
	CounterpartyUserID     string `json:"counterpartyUserId,omitempty" bson:"counterpartyUserId,omitempty"`
	Memo                   string `json:"memo,omitempty" bson:"memo,omitempty"` // ≤140 chars, set on peer transfers
	RefType     string             `json:"refType,omitempty" bson:"refType,omitempty"` // e.g. "criminalHistoryId" | "courtCaseId"
	RefID       string             `json:"refId,omitempty" bson:"refId,omitempty"`
	DueAt       primitive.DateTime `json:"dueAt,omitempty" bson:"dueAt,omitempty"`
	PaidAt      primitive.DateTime `json:"paidAt,omitempty" bson:"paidAt,omitempty"`
	// BalanceAfter is the civilian's wallet balance (cents) immediately after
	// this item was paid. Written by PayInboxItemHandler via an atomic
	// FindOneAndUpdate. Unset for unpaid items and for items paid before this
	// field existed; consumers should treat 0 as "unknown" not zero balance.
	BalanceAfter int64 `json:"balanceAfter,omitempty" bson:"balanceAfter,omitempty"`
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
	Resolution    string             `json:"resolution,omitempty" bson:"resolution,omitempty"` // "upheld" | "dismissed" | "partial"
	// Charges, when present, enumerates the individual citations that make up
	// this inbox item along with their per-charge status. Omitted for non-
	// citation items (admin fees, payroll, etc.) so legacy callers stay clean.
	Charges   []InboxCharge      `json:"charges,omitempty" bson:"charges,omitempty"`
	CreatedAt primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// InboxCharge represents a single charge inside a citation-style inbox item.
// Amount is stored in cents and matches the contribution to the parent item's
// Amount (sum of non-dismissed charge amounts).
type InboxCharge struct {
	Label  string `json:"label" bson:"label"`
	Amount int64  `json:"amount" bson:"amount"`           // cents
	Status string `json:"status" bson:"status"`           // "pending" | "upheld" | "dismissed"
}
