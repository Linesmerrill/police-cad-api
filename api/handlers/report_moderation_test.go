package handlers

import (
	"testing"
	"time"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

var planNow = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

func offenseExpiring(at time.Time, penalty string) *models.ContentOffense {
	d := primitive.NewDateTimeFromTime(at)
	return &models.ContentOffense{
		Status:    models.ContentOffenseStatusActive,
		Penalty:   penalty,
		ExpiresAt: &d,
	}
}

func TestBuildOffensePlan_FirstOffenseByTier(t *testing.T) {
	tests := []struct {
		name       string
		issue      string
		wantAction string
		wantExpiry time.Duration // 0 means no expiry expected
	}{
		{"spam starts with a warning", "Spam", models.PenaltyActionWarning, 0},
		{"harassment starts at a week", "Abuse & Harassment", models.PenaltyActionSuspension, 7 * 24 * time.Hour},
		{"hate starts at a week", "Hate", models.PenaltyActionSuspension, 7 * 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := buildOffensePlan(models.ContentOffenseScopeUser, tt.issue, 0, nil, planNow)
			if !plan.Ladders {
				t.Fatal("expected this issue to ladder")
			}
			if plan.OffenseNumber != 1 {
				t.Errorf("offense number = %d, want 1", plan.OffenseNumber)
			}
			if plan.Action != tt.wantAction {
				t.Errorf("action = %q, want %q", plan.Action, tt.wantAction)
			}
			if tt.wantExpiry == 0 {
				if plan.ExpiresAt != nil {
					t.Errorf("expiry = %v, want none", plan.ExpiresAt)
				}
				return
			}
			if plan.ExpiresAt == nil {
				t.Fatal("expected an expiry")
			}
			if got := plan.ExpiresAt.Sub(planNow); got != tt.wantExpiry {
				t.Errorf("expiry in %v, want %v", got, tt.wantExpiry)
			}
		})
	}
}

// Nothing about a Child Safety or self-harm report may produce an automatic
// penalty or a notice. This is the test that must never be relaxed.
func TestBuildOffensePlan_EscalateAndWelfareNeverLadder(t *testing.T) {
	for _, issue := range []string{"Child Safety", "Suicide or Self-Harm"} {
		t.Run(issue, func(t *testing.T) {
			plan := buildOffensePlan(models.ContentOffenseScopeUser, issue, 3, nil, planNow)
			if plan.Ladders {
				t.Fatal("this tier must never ladder")
			}
			if plan.OffenseNumber != 0 {
				t.Errorf("offense number = %d, want 0 (no offense is issued)", plan.OffenseNumber)
			}
			if plan.Action != "" {
				t.Errorf("action = %q, want none", plan.Action)
			}
			if plan.ExpiresAt != nil {
				t.Error("no penalty, so no expiry")
			}
		})
	}
}

func TestBuildOffensePlan_EscalationIsCumulative(t *testing.T) {
	tests := []struct {
		name       string
		issue      string
		prior      int
		wantNumber int
		wantAction string
		wantNext   string
	}{
		{"second spam report", "Spam", 1, 2, models.PenaltyActionSuspension, "a 30 days suspension"},
		{"third spam report", "Spam", 2, 3, models.PenaltyActionSuspension, "permanent removal"},
		{"fourth spam report", "Spam", 3, 4, models.PenaltyActionPermanent, ""},
		{"fifth spam report stays permanent", "Spam", 4, 5, models.PenaltyActionPermanent, ""},
		{"second hate report", "Hate", 1, 2, models.PenaltyActionSuspension, "a 1 year suspension"},
		{"fourth hate report", "Hate", 3, 4, models.PenaltyActionPermanent, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := buildOffensePlan(models.ContentOffenseScopeUser, tt.issue, tt.prior, nil, planNow)
			if plan.OffenseNumber != tt.wantNumber {
				t.Errorf("offense number = %d, want %d", plan.OffenseNumber, tt.wantNumber)
			}
			if plan.Action != tt.wantAction {
				t.Errorf("action = %q, want %q", plan.Action, tt.wantAction)
			}
			if plan.NextLabel != tt.wantNext {
				t.Errorf("next penalty = %q, want %q", plan.NextLabel, tt.wantNext)
			}
		})
	}
}

// Upholding a second report while the first penalty is still running would
// stack an offense and jump the ladder for behavior already actioned.
func TestBuildOffensePlan_RefusesWhileAPenaltyIsStillRunning(t *testing.T) {
	until := planNow.Add(3 * 24 * time.Hour)
	plan := buildOffensePlan(models.ContentOffenseScopeUser, "Hate", 1,
		offenseExpiring(until, models.PenaltyActionSuspension), planNow)

	if !plan.AlreadyInForce {
		t.Fatal("expected the plan to report an in-force penalty")
	}
	if plan.OffenseNumber != 0 {
		t.Errorf("offense number = %d, want 0 — nothing should be issued", plan.OffenseNumber)
	}
	if plan.AlreadyInForceUntil == nil || !plan.AlreadyInForceUntil.Equal(until) {
		t.Errorf("in-force until = %v, want %v", plan.AlreadyInForceUntil, until)
	}
}

func TestBuildOffensePlan_ProceedsOnceAnEarlierPenaltyHasElapsed(t *testing.T) {
	elapsed := offenseExpiring(planNow.Add(-time.Hour), models.PenaltyActionSuspension)
	plan := buildOffensePlan(models.ContentOffenseScopeUser, "Hate", 1, elapsed, planNow)

	if plan.AlreadyInForce {
		t.Fatal("an elapsed penalty must not block a new one")
	}
	// It has elapsed, but it still counts: this is the second offense.
	if plan.OffenseNumber != 2 {
		t.Errorf("offense number = %d, want 2", plan.OffenseNumber)
	}
	if plan.Action != models.PenaltyActionSuspension || plan.PenaltyLabel != "30 days" {
		t.Errorf("penalty = %q %q, want a 30 days suspension", plan.Action, plan.PenaltyLabel)
	}
}

// A warning is a record, not a restriction, so it must not block the next
// action the way a running suspension does.
func TestBuildOffensePlan_AWarningDoesNotBlockTheNextRung(t *testing.T) {
	warning := &models.ContentOffense{Status: models.ContentOffenseStatusActive, Penalty: models.PenaltyActionWarning}
	plan := buildOffensePlan(models.ContentOffenseScopeUser, "Spam", 1, warning, planNow)

	if plan.AlreadyInForce {
		t.Fatal("a warning is not a penalty in force")
	}
	if plan.OffenseNumber != 2 || plan.Action != models.PenaltyActionSuspension {
		t.Errorf("plan = #%d %q, want #2 suspension", plan.OffenseNumber, plan.Action)
	}
}

// A community action is a delisting whatever the issue, so it ladders even for
// an issue that would escalate on the person side.
func TestBuildOffensePlan_CommunityScopeAlwaysLadders(t *testing.T) {
	plan := buildOffensePlan(models.ContentOffenseScopeCommunity, "Child Safety", 0, nil, planNow)
	if !plan.Ladders {
		t.Fatal("community scope must ladder")
	}
	if plan.Action != models.PenaltyActionSuspension || plan.PenaltyLabel != "7 days" {
		t.Errorf("plan = %q %q, want a 7 days delisting", plan.Action, plan.PenaltyLabel)
	}
	if plan.ExpiresAt == nil || plan.ExpiresAt.Sub(planNow) != 7*24*time.Hour {
		t.Errorf("expiry = %v, want 7 days out", plan.ExpiresAt)
	}
}

func TestBuildOffensePlan_UnknownIssueIsFlaggedAndStartsGently(t *testing.T) {
	plan := buildOffensePlan(models.ContentOffenseScopeUser, "Something New", 0, nil, planNow)
	if plan.IssueKnown {
		t.Error("an unclassified issue must be flagged so the console does not present the default as a decision")
	}
	if plan.Action != models.PenaltyActionWarning {
		t.Errorf("action = %q, want a warning for an unclassified issue", plan.Action)
	}
}

func TestBuildOffensePlan_NegativePriorCountIsTreatedAsZero(t *testing.T) {
	plan := buildOffensePlan(models.ContentOffenseScopeUser, "Spam", -5, nil, planNow)
	if plan.OffenseNumber != 1 {
		t.Errorf("offense number = %d, want 1", plan.OffenseNumber)
	}
}
