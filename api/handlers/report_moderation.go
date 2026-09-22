package handlers

import (
	"time"

	"github.com/linesmerrill/police-cad-api/models"
)

// offensePlan is what upholding a report would do, computed before anything is
// written so the console can show it and the acting admin can confirm it.
//
// Reports are unverified accusations, usually from someone in a dispute with
// the target, so nothing here is ever applied automatically. A coordinated
// group filing four reports must not be able to remove an account before a
// person has read a word of it.
type offensePlan struct {
	Scope         string                `json:"scope"`
	Tier          string                `json:"tier"`
	ReportedIssue string                `json:"reportedIssue"`
	IssueKnown    bool                  `json:"issueKnown"`
	OffenseNumber int                   `json:"offenseNumber"`
	Penalty       models.OffensePenalty `json:"-"`
	Action        string                `json:"action"`
	PenaltyLabel  string                `json:"penaltyLabel,omitempty"`
	ExpiresAt     *time.Time            `json:"expiresAt,omitempty"`
	NextLabel     string                `json:"nextPenalty,omitempty"`

	// Ladders produce a penalty and a notice. Escalate and Welfare do not, and
	// the console must not offer an uphold button for them.
	Ladders bool `json:"ladders"`

	// AlreadyInForce is set when the target is still serving an earlier
	// penalty. Issuing another would stack a second offense and jump the
	// ladder a rung for behavior that has already been actioned, so the
	// console blocks it and the admin reverses the existing one instead.
	AlreadyInForce      bool       `json:"alreadyInForce"`
	AlreadyInForceUntil *time.Time `json:"alreadyInForceUntil,omitempty"`
}

// buildOffensePlan works out the rung for a report without touching the
// database.
//
// priorActive is the number of offenses already recorded against this target
// that count toward escalation: active ones, including those whose suspension
// has already elapsed. Reversed offenses are excluded by the caller, so an
// appeal that succeeded genuinely un-escalates.
//
// inForce is the target's current unexpired penalty, if any.
func buildOffensePlan(scope, issue string, priorActive int, inForce *models.ContentOffense, now time.Time) offensePlan {
	tier := models.ReportTierForIssue(issue)

	plan := offensePlan{
		Scope:         scope,
		Tier:          tier,
		ReportedIssue: issue,
		IssueKnown:    models.IsKnownReportIssue(issue),
		Ladders:       models.IsLadderTier(tier),
	}

	// A community-scoped action is always a delisting, whatever the issue's
	// tier, so it ladders even when the issue would otherwise escalate. The
	// person-side escalation is handled separately and is never automated.
	if scope == models.ContentOffenseScopeCommunity {
		plan.Ladders = true
	}

	if !plan.Ladders {
		return plan
	}

	if inForce != nil && inForce.InForce(now) {
		plan.AlreadyInForce = true
		if inForce.ExpiresAt != nil {
			until := inForce.ExpiresAt.Time()
			plan.AlreadyInForceUntil = &until
		}
		return plan
	}

	if priorActive < 0 {
		priorActive = 0
	}
	plan.OffenseNumber = priorActive + 1
	plan.Penalty = models.PenaltyForOffense(scope, tier, plan.OffenseNumber)
	plan.Action = plan.Penalty.Action
	plan.PenaltyLabel = plan.Penalty.Label
	plan.NextLabel = models.NextPenaltyLabel(scope, tier, plan.OffenseNumber)

	if plan.Penalty.IsSuspension() {
		expires := now.Add(plan.Penalty.Duration)
		plan.ExpiresAt = &expires
	}

	return plan
}
