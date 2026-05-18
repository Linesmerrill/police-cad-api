package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// ClockSession represents a single clock-in/clock-out shift for a civilian at a department.
// One civilian may only have one Status="active" session at a time (enforced by partial unique index).
type ClockSession struct {
	ID               primitive.ObjectID `json:"_id" bson:"_id,omitempty"`
	CommunityID      string             `json:"communityId" bson:"communityId"`
	DepartmentID     string             `json:"departmentId" bson:"departmentId"`
	DepartmentName   string             `json:"departmentName" bson:"departmentName"`
	UserID           string             `json:"userId" bson:"userId"`
	CivilianID       string             `json:"civilianId,omitempty" bson:"civilianId,omitempty"` // empty for user-scoped jobs (LEO/EMS/...)
	RankID           string             `json:"rankId,omitempty" bson:"rankId,omitempty"`
	PayRateSnapshot  int64              `json:"payRateSnapshot" bson:"payRateSnapshot"` // cents/hr resolved at clock-in
	PayoutMode       string             `json:"payoutMode" bson:"payoutMode"`           // "on_heartbeat" | "on_clockout"
	Status           string             `json:"status" bson:"status"`                   // "active" | "ended" | "expired" | "abandoned"
	StartedAt        primitive.DateTime `json:"startedAt" bson:"startedAt"`
	LastHeartbeatAt  primitive.DateTime `json:"lastHeartbeatAt" bson:"lastHeartbeatAt"`
	LastPayoutAt     primitive.DateTime `json:"lastPayoutAt,omitempty" bson:"lastPayoutAt,omitempty"` // for on_heartbeat: last time we credited
	EndedAt          primitive.DateTime `json:"endedAt,omitempty" bson:"endedAt,omitempty"`
	PaidSeconds      int64              `json:"paidSeconds" bson:"paidSeconds"` // cumulative paid duration
	Earnings         int64              `json:"earnings" bson:"earnings"`       // cumulative credited cents
	// BalanceAfter is the civilian's wallet balance (cents) immediately after the
	// most recent credit was applied. Written by paySession via an atomic
	// FindOneAndUpdate so the value reflects the true post-credit balance even
	// under concurrent debits. Older sessions credited before this field existed
	// will be 0; consumers should treat 0 as "unknown" rather than zero balance.
	BalanceAfter int64 `json:"balanceAfter,omitempty" bson:"balanceAfter,omitempty"`
	MaxSessionMinutes int               `json:"maxSessionMinutes" bson:"maxSessionMinutes"` // snapshot at clock-in
	AfkGraceSeconds  int                `json:"afkGraceSeconds" bson:"afkGraceSeconds"`     // snapshot at clock-in
	CreatedAt        primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt        primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}
