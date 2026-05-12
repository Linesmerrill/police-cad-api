package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// ClockSession represents a single clock-in/clock-out shift for a civilian at a department.
// One civilian may only have one Status="active" session at a time (enforced by partial unique index).
type ClockSession struct {
	ID               primitive.ObjectID `json:"_id" bson:"_id,omitempty"`
	CommunityID      string             `json:"communityId" bson:"communityId"`
	DepartmentID     string             `json:"departmentId,omitempty" bson:"departmentId,omitempty"`
	DepartmentName   string             `json:"departmentName,omitempty" bson:"departmentName,omitempty"`
	JobID            string             `json:"jobId,omitempty" bson:"jobId,omitempty"`     // set when the session is against a community Job instead of a Department
	JobName          string             `json:"jobName,omitempty" bson:"jobName,omitempty"` // snapshot of job.Name at clock-in
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
	MaxSessionMinutes int               `json:"maxSessionMinutes" bson:"maxSessionMinutes"` // snapshot at clock-in
	AfkGraceSeconds  int                `json:"afkGraceSeconds" bson:"afkGraceSeconds"`     // snapshot at clock-in
	CreatedAt        primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt        primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}
