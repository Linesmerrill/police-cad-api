package models

import (
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Report tiers. A tier is derived from the reported issue and decides how a
// report is handled once a staff member upholds it.
//
// Only the two ladder tiers ever produce an automated penalty or an offense
// email. Escalate and Welfare deliberately do not:
//
//   - Escalate (Child Safety) goes to a person. Automating a penalty on a
//     child-safety allegation would also tip off the account while an
//     escalation is being prepared.
//   - Welfare (Suicide or Self-Harm) is not an offense at all. Someone
//     reporting a player for self-harm is flagging a person in distress, not
//     an offender, and must never be sent an enforcement notice.
const (
	ReportTierMinor    = "minor"
	ReportTierSerious  = "serious"
	ReportTierEscalate = "escalate"
	ReportTierWelfare  = "welfare"
)

// reportTiersByIssue maps the reported issue, as the mobile app records it, to
// its tier. Keys are normalized (see normalizeReportIssue) so that casing or
// spacing drift in a future client does not silently fall through to the
// default.
var reportTiersByIssue = map[string]string{
	"child safety": ReportTierEscalate,

	"suicide or self-harm": ReportTierWelfare,

	"hate":                         ReportTierSerious,
	"abuse & harassment":           ReportTierSerious,
	"violent speech":               ReportTierSerious,
	"violent & hateful entities":   ReportTierSerious,
	"illegal & regulated behavior": ReportTierSerious,
	// Privacy covers non-consensual intimate images, which is not a minor
	// offense in the way the rest of the low tier is.
	"privacy": ReportTierSerious,

	"spam":                          ReportTierMinor,
	"impersonation":                 ReportTierMinor,
	"sensitive or disturbing media": ReportTierMinor,
}

func normalizeReportIssue(issue string) string {
	return strings.ToLower(strings.TrimSpace(issue))
}

// ReportTierForIssue returns the tier for a reported issue.
//
// An issue we do not recognize falls back to the minor tier, whose first rung
// is a warning with no restriction. That is the recoverable direction to be
// wrong in: a warning issued in error costs an apology, a suspension issued in
// error locks a player out of the game. The console flags unrecognized issues
// so a staff member can raise the tier by hand.
func ReportTierForIssue(issue string) string {
	if tier, ok := reportTiersByIssue[normalizeReportIssue(issue)]; ok {
		return tier
	}
	return ReportTierMinor
}

// IsKnownReportIssue reports whether the issue is one we have classified. The
// console uses this to mark a report as unclassified rather than quietly
// presenting the minor-tier default as if it were a decision.
func IsKnownReportIssue(issue string) bool {
	_, ok := reportTiersByIssue[normalizeReportIssue(issue)]
	return ok
}

// IsLadderTier reports whether a tier produces an automated penalty and offense
// email. Escalate and Welfare do not.
func IsLadderTier(tier string) bool {
	return tier == ReportTierMinor || tier == ReportTierSerious
}

// Penalty actions.
const (
	PenaltyActionWarning    = "warning"
	PenaltyActionSuspension = "suspension"
	PenaltyActionPermanent  = "permanent"
)

// OffensePenalty is one rung of a ladder: what happens, for how long, and how
// to say it in an email.
type OffensePenalty struct {
	Action string
	// Duration is zero for a warning and for a permanent removal. A permanent
	// removal is distinguished by Action, never by a zero duration.
	Duration time.Duration
	// Label is the human phrase used in the notice ("7 days"). Empty for a
	// warning.
	Label string
}

// IsSuspension reports whether this rung locks the account for a fixed period.
func (p OffensePenalty) IsSuspension() bool { return p.Action == PenaltyActionSuspension }

const (
	day  = 24 * time.Hour
	year = 365 * day
)

var (
	// minorLadder: a first spam or impersonation report earns a warning, which
	// costs nothing and creates the record that makes a second one defensible.
	minorLadder = []OffensePenalty{
		{Action: PenaltyActionWarning},
		{Action: PenaltyActionSuspension, Duration: 7 * day, Label: "7 days"},
		{Action: PenaltyActionSuspension, Duration: 30 * day, Label: "30 days"},
		{Action: PenaltyActionPermanent},
	}

	// seriousLadder skips the warning rung. Harassment and violent speech are
	// not behaviors a first-time notice is a proportionate answer to.
	seriousLadder = []OffensePenalty{
		{Action: PenaltyActionSuspension, Duration: 7 * day, Label: "7 days"},
		{Action: PenaltyActionSuspension, Duration: 30 * day, Label: "30 days"},
		{Action: PenaltyActionSuspension, Duration: year, Label: "1 year"},
		{Action: PenaltyActionPermanent},
	}

	// communityLadder applies to community-scoped offenses. Every rung is a
	// delisting: the community drops out of discovery while existing members
	// carry on unaffected. Shutting a server down punishes its members for what
	// its staff did, so that decision stays with a person.
	communityLadder = []OffensePenalty{
		{Action: PenaltyActionSuspension, Duration: 7 * day, Label: "7 days"},
		{Action: PenaltyActionSuspension, Duration: 30 * day, Label: "30 days"},
		{Action: PenaltyActionPermanent},
	}
)

func ladderFor(scope, tier string) []OffensePenalty {
	if scope == ContentOffenseScopeCommunity {
		return communityLadder
	}
	if tier == ReportTierSerious {
		return seriousLadder
	}
	return minorLadder
}

// PenaltyForOffense returns the rung for a given offense number, which is
// 1-based. Anything past the end of the ladder is the final rung, so a fifth
// offense is permanent rather than an index panic.
func PenaltyForOffense(scope, tier string, offenseNumber int) OffensePenalty {
	ladder := ladderFor(scope, tier)
	if offenseNumber < 1 {
		offenseNumber = 1
	}
	if offenseNumber > len(ladder) {
		offenseNumber = len(ladder)
	}
	return ladder[offenseNumber-1]
}

// NextPenaltyLabel describes the rung after this one, for the "a further breach
// will result in X" line of the notice. It returns an empty string when the
// offense being issued is already the final rung.
func NextPenaltyLabel(scope, tier string, offenseNumber int) string {
	ladder := ladderFor(scope, tier)
	if offenseNumber >= len(ladder) {
		return ""
	}
	next := PenaltyForOffense(scope, tier, offenseNumber+1)
	switch next.Action {
	case PenaltyActionPermanent:
		return "permanent removal"
	case PenaltyActionSuspension:
		return "a " + next.Label + " suspension"
	default:
		return ""
	}
}

// issuePhrases map a reported issue to the wording used in an offense notice.
//
// The phrasing is deliberately categorical. A notice never quotes the report
// and never hints at who filed it: in a player base this size, repeating the
// detail identifies the reporter, who is often a child who reported a bully.
var issuePhrases = map[string]string{
	"hate":                          "hateful conduct",
	"abuse & harassment":            "abusive or harassing behavior",
	"violent speech":                "violent threats or language",
	"violent & hateful entities":    "affiliation with violent or hateful groups",
	"illegal & regulated behavior":  "illegal or regulated activity",
	"privacy":                       "sharing private information without consent",
	"spam":                          "spam or repeated unwanted messages",
	"impersonation":                 "impersonating another person or organization",
	"sensitive or disturbing media": "sharing sensitive or disturbing media",
	"child safety":                  "conduct that puts a minor at risk",
	"suicide or self-harm":          "content relating to self-harm",
}

// IssuePhrase returns the notice wording for a reported issue, falling back to
// a neutral phrase for an issue we have not classified. The fallback is
// deliberately vague rather than echoing the raw issue string back at the
// recipient, which would leak whatever text a future client happens to send.
func IssuePhrase(issue string) string {
	if phrase, ok := issuePhrases[normalizeReportIssue(issue)]; ok {
		return phrase
	}
	return "activity that breached our community standards"
}

// inForce is the shared lazy expiry test used by suspensions, listing
// suspensions and offenses. A nil expiry means permanent, never "expired".
func inForce(until *primitive.DateTime, now time.Time) bool {
	if until == nil {
		return true
	}
	return until.Time().After(now)
}

// InForce reports whether the suspension currently locks the account. A nil
// receiver is not in force, so callers can test an absent suspension directly.
func (s *Suspension) InForce(now time.Time) bool {
	if s == nil {
		return false
	}
	return inForce(s.Until, now)
}

// InForce reports whether the community is currently delisted.
func (l *ListingSuspension) InForce(now time.Time) bool {
	if l == nil {
		return false
	}
	return inForce(l.Until, now)
}

// InForce reports whether this offense's penalty is currently being enforced.
// A reversed offense is never in force, and a warning carries no penalty to
// enforce.
func (o ContentOffense) InForce(now time.Time) bool {
	if o.Status != ContentOffenseStatusActive {
		return false
	}
	if o.Penalty == PenaltyActionWarning {
		return false
	}
	return inForce(o.ExpiresAt, now)
}

// CountsTowardEscalation reports whether this offense adds to the next rung.
// An offense whose suspension has already elapsed still counts; one that was
// reversed on appeal does not.
func (o ContentOffense) CountsTowardEscalation() bool {
	return o.Status == ContentOffenseStatusActive
}
