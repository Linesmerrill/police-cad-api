package models

import (
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// The tier a report lands in decides whether a player is warned or locked out
// for a month, so every category the mobile client can send is pinned here.
// Counts are the production distribution surveyed 2026-09-22 (40 reports).
func TestReportTierForIssue(t *testing.T) {
	tests := []struct {
		issue string
		want  string
	}{
		{"Child Safety", ReportTierEscalate},        // 7 in production
		{"Suicide or Self-Harm", ReportTierWelfare}, // 2
		{"Hate", ReportTierSerious},                 // 8
		{"Abuse & Harassment", ReportTierSerious},   // 8
		{"Violent Speech", ReportTierSerious},       // 4
		{"Violent & Hateful Entities", ReportTierSerious},
		{"Illegal & Regulated Behavior", ReportTierSerious},
		// Privacy covers non-consensual intimate images, so it does not belong
		// with spam and impersonation.
		{"Privacy", ReportTierSerious},
		{"Spam", ReportTierMinor},
		{"Impersonation", ReportTierMinor},
		{"Sensitive or Disturbing Media", ReportTierMinor},
	}
	for _, tt := range tests {
		t.Run(tt.issue, func(t *testing.T) {
			if got := ReportTierForIssue(tt.issue); got != tt.want {
				t.Errorf("ReportTierForIssue(%q) = %q, want %q", tt.issue, got, tt.want)
			}
			if !IsKnownReportIssue(tt.issue) {
				t.Errorf("IsKnownReportIssue(%q) = false, want true", tt.issue)
			}
		})
	}
}

func TestReportTierForIssue_NormalizesCasingAndSpacing(t *testing.T) {
	for _, in := range []string{"child safety", "  Child Safety  ", "CHILD SAFETY"} {
		if got := ReportTierForIssue(in); got != ReportTierEscalate {
			t.Errorf("ReportTierForIssue(%q) = %q, want %q", in, got, ReportTierEscalate)
		}
	}
}

// An issue we have never seen must land on the rung that is recoverable when
// we are wrong: a warning, not a suspension.
func TestReportTierForIssue_UnknownFallsBackToMinorAndIsFlagged(t *testing.T) {
	const unknown = "Something A Future Client Invented"
	if got := ReportTierForIssue(unknown); got != ReportTierMinor {
		t.Errorf("tier = %q, want %q", got, ReportTierMinor)
	}
	if IsKnownReportIssue(unknown) {
		t.Error("IsKnownReportIssue = true for an unclassified issue; the console would present the default as a decision")
	}
	if got := PenaltyForOffense(ContentOffenseScopeUser, ReportTierMinor, 1); got.Action != PenaltyActionWarning {
		t.Errorf("first rung = %q, want %q", got.Action, PenaltyActionWarning)
	}
}

func TestIsLadderTier(t *testing.T) {
	tests := []struct {
		tier string
		want bool
	}{
		{ReportTierMinor, true},
		{ReportTierSerious, true},
		// Neither of these may ever produce an automated penalty or notice.
		{ReportTierEscalate, false},
		{ReportTierWelfare, false},
	}
	for _, tt := range tests {
		if got := IsLadderTier(tt.tier); got != tt.want {
			t.Errorf("IsLadderTier(%q) = %v, want %v", tt.tier, got, tt.want)
		}
	}
}

func TestPenaltyForOffense(t *testing.T) {
	tests := []struct {
		name         string
		scope, tier  string
		n            int
		wantAction   string
		wantDuration time.Duration
	}{
		{"minor 1st is a warning", ContentOffenseScopeUser, ReportTierMinor, 1, PenaltyActionWarning, 0},
		{"minor 2nd", ContentOffenseScopeUser, ReportTierMinor, 2, PenaltyActionSuspension, 7 * day},
		{"minor 3rd", ContentOffenseScopeUser, ReportTierMinor, 3, PenaltyActionSuspension, 30 * day},
		{"minor 4th", ContentOffenseScopeUser, ReportTierMinor, 4, PenaltyActionPermanent, 0},

		{"serious skips the warning rung", ContentOffenseScopeUser, ReportTierSerious, 1, PenaltyActionSuspension, 7 * day},
		{"serious 2nd", ContentOffenseScopeUser, ReportTierSerious, 2, PenaltyActionSuspension, 30 * day},
		{"serious 3rd", ContentOffenseScopeUser, ReportTierSerious, 3, PenaltyActionSuspension, year},
		{"serious 4th", ContentOffenseScopeUser, ReportTierSerious, 4, PenaltyActionPermanent, 0},

		// Community offenses delist; they never suspend the members' access.
		{"community 1st", ContentOffenseScopeCommunity, ReportTierSerious, 1, PenaltyActionSuspension, 7 * day},
		{"community 2nd", ContentOffenseScopeCommunity, ReportTierSerious, 2, PenaltyActionSuspension, 30 * day},
		{"community 3rd", ContentOffenseScopeCommunity, ReportTierSerious, 3, PenaltyActionPermanent, 0},
		{"community tier does not change the ladder", ContentOffenseScopeCommunity, ReportTierMinor, 1, PenaltyActionSuspension, 7 * day},

		// Past the end of a ladder clamps to the final rung rather than panicking.
		{"minor 5th clamps to permanent", ContentOffenseScopeUser, ReportTierMinor, 5, PenaltyActionPermanent, 0},
		{"serious 99th clamps to permanent", ContentOffenseScopeUser, ReportTierSerious, 99, PenaltyActionPermanent, 0},
		{"zero clamps to the first rung", ContentOffenseScopeUser, ReportTierSerious, 0, PenaltyActionSuspension, 7 * day},
		{"negative clamps to the first rung", ContentOffenseScopeUser, ReportTierMinor, -3, PenaltyActionWarning, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PenaltyForOffense(tt.scope, tt.tier, tt.n)
			if got.Action != tt.wantAction {
				t.Errorf("action = %q, want %q", got.Action, tt.wantAction)
			}
			if got.Duration != tt.wantDuration {
				t.Errorf("duration = %v, want %v", got.Duration, tt.wantDuration)
			}
			if got.IsSuspension() && got.Label == "" {
				t.Error("a suspension rung has no label, so the notice cannot say how long it lasts")
			}
		})
	}
}

func TestNextPenaltyLabel(t *testing.T) {
	tests := []struct {
		name        string
		scope, tier string
		n           int
		want        string
	}{
		{"after a minor warning", ContentOffenseScopeUser, ReportTierMinor, 1, "a 7 days suspension"},
		{"after a minor 7 day", ContentOffenseScopeUser, ReportTierMinor, 2, "a 30 days suspension"},
		{"before the final minor rung", ContentOffenseScopeUser, ReportTierMinor, 3, "permanent removal"},
		{"at the final minor rung", ContentOffenseScopeUser, ReportTierMinor, 4, ""},
		{"past the final rung", ContentOffenseScopeUser, ReportTierMinor, 50, ""},
		{"before the final serious rung", ContentOffenseScopeUser, ReportTierSerious, 3, "permanent removal"},
		{"before the final community rung", ContentOffenseScopeCommunity, ReportTierSerious, 2, "permanent removal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextPenaltyLabel(tt.scope, tt.tier, tt.n); got != tt.want {
				t.Errorf("NextPenaltyLabel = %q, want %q", got, tt.want)
			}
		})
	}
}

// A notice must never echo an unrecognized issue string back at the recipient,
// which would leak whatever text a future client happens to send.
func TestIssuePhrase(t *testing.T) {
	if got := IssuePhrase("Child Safety"); got != "conduct that puts a minor at risk" {
		t.Errorf("IssuePhrase = %q", got)
	}
	if got := IssuePhrase("  hATE "); got != "hateful conduct" {
		t.Errorf("IssuePhrase did not normalize: %q", got)
	}
	const injected = "<script>alert(1)</script>"
	got := IssuePhrase(injected)
	if strings.Contains(got, injected) {
		t.Errorf("IssuePhrase echoed the raw issue back: %q", got)
	}
	if got == "" {
		t.Error("IssuePhrase returned empty, which would leave a hole in the notice")
	}
	// Every classified issue needs a phrase, or a notice for it reads as the
	// generic fallback.
	for issue := range reportTiersByIssue {
		if _, ok := issuePhrases[issue]; !ok {
			t.Errorf("issue %q has a tier but no notice phrase", issue)
		}
	}
}

func ptrDT(t time.Time) *primitive.DateTime {
	d := primitive.NewDateTimeFromTime(t)
	return &d
}

func TestSuspensionInForce(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		s    *Suspension
		want bool
	}{
		{"absent suspension", nil, false},
		{"nil until is permanent", &Suspension{}, true},
		{"expiry in the future", &Suspension{Until: ptrDT(now.Add(time.Hour))}, true},
		{"expiry in the past has lifted itself", &Suspension{Until: ptrDT(now.Add(-time.Hour))}, false},
		{"expiry exactly now has lifted", &Suspension{Until: ptrDT(now)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.InForce(now); got != tt.want {
				t.Errorf("InForce = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListingSuspensionInForce(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	var absent *ListingSuspension
	if absent.InForce(now) {
		t.Error("an absent listing suspension must not delist a community")
	}
	if !(&ListingSuspension{Until: ptrDT(now.Add(time.Hour))}).InForce(now) {
		t.Error("a future expiry should still be delisting")
	}
	if (&ListingSuspension{Until: ptrDT(now.Add(-time.Second))}).InForce(now) {
		t.Error("an elapsed delisting must lift itself without a cron")
	}
}

func TestContentOffenseInForceAndEscalation(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name             string
		o                ContentOffense
		wantInForce      bool
		wantCountsToward bool
	}{
		{
			name:             "active suspension not yet elapsed",
			o:                ContentOffense{Status: ContentOffenseStatusActive, Penalty: PenaltyActionSuspension, ExpiresAt: ptrDT(now.Add(time.Hour))},
			wantInForce:      true,
			wantCountsToward: true,
		},
		{
			// The ladder is cumulative: serving a suspension does not wipe the
			// slate, otherwise the second offense would repeat the first rung.
			name:             "elapsed suspension still counts toward the next rung",
			o:                ContentOffense{Status: ContentOffenseStatusActive, Penalty: PenaltyActionSuspension, ExpiresAt: ptrDT(now.Add(-time.Hour))},
			wantInForce:      false,
			wantCountsToward: true,
		},
		{
			name:             "permanent has no expiry",
			o:                ContentOffense{Status: ContentOffenseStatusActive, Penalty: PenaltyActionPermanent},
			wantInForce:      true,
			wantCountsToward: true,
		},
		{
			// A warning is a record, not a restriction.
			name:             "warning never locks the account but does count",
			o:                ContentOffense{Status: ContentOffenseStatusActive, Penalty: PenaltyActionWarning},
			wantInForce:      false,
			wantCountsToward: true,
		},
		{
			// An appeal that succeeded must un-escalate, or the next offense
			// jumps a rung for something we agreed did not happen.
			name:             "reversed is neither enforced nor counted",
			o:                ContentOffense{Status: ContentOffenseStatusReversed, Penalty: PenaltyActionPermanent},
			wantInForce:      false,
			wantCountsToward: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.o.InForce(now); got != tt.wantInForce {
				t.Errorf("InForce = %v, want %v", got, tt.wantInForce)
			}
			if got := tt.o.CountsTowardEscalation(); got != tt.wantCountsToward {
				t.Errorf("CountsTowardEscalation = %v, want %v", got, tt.wantCountsToward)
			}
		})
	}
}
