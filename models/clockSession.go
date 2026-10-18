package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ClockSourceDiscord marks a session started from the Discord bot's /clock-in.
// The bot has no page that stays open to send heartbeats, so these sessions are
// exempt from the AFK timeout and run until /clock-out or the max session
// window. Sessions with no source (website, mobile) keep the AFK rules.
const ClockSourceDiscord = "discord"

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
	Source           string             `json:"source,omitempty" bson:"source,omitempty"`   // "" (website/mobile) | ClockSourceDiscord
	CreatedAt        primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt        primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// TracksAfk reports whether the session is subject to the AFK heartbeat
// timeout. Only clients that send heartbeats can be held to it.
func (s *ClockSession) TracksAfk() bool {
	return s.Source != ClockSourceDiscord
}

// PayableUntil returns the latest instant the session can be paid through,
// given that it is being settled at now: never past the max session window
// and, for sessions that track AFK, never past the last heartbeat plus grace.
func (s *ClockSession) PayableUntil(now time.Time) time.Time {
	end := now
	maxEnd := s.StartedAt.Time().Add(time.Duration(s.MaxSessionMinutes) * time.Minute)
	if end.After(maxEnd) {
		end = maxEnd
	}
	if s.TracksAfk() && s.LastHeartbeatAt != 0 {
		hbCap := s.LastHeartbeatAt.Time().Add(time.Duration(s.AfkGraceSeconds) * time.Second)
		if end.After(hbCap) {
			end = hbCap
		}
	}
	return end
}

// SweepStatus returns the terminal status the stale-session sweep should close
// an active session with at now, or "" to leave it running. Past the max
// session window it is "expired"; past the AFK grace (AFK-tracked sessions
// only) it is "abandoned".
func (s *ClockSession) SweepStatus(now time.Time) string {
	maxEnd := s.StartedAt.Time().Add(time.Duration(s.MaxSessionMinutes) * time.Minute)
	if !now.Before(maxEnd) {
		return "expired"
	}
	if s.TracksAfk() {
		afkCutoff := s.LastHeartbeatAt.Time().Add(time.Duration(s.AfkGraceSeconds) * time.Second)
		if !now.Before(afkCutoff) {
			return "abandoned"
		}
	}
	return ""
}
