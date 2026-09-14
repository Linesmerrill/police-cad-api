package models

import (
	"strings"
	"time"
)

// DefaultRespondDays is how long a civilian has to contest a citation or arrest
// before it is filed for them. Three days matches what communities asked for
// when they described their own rules.
const DefaultRespondDays = 3

// MaxRespondDays bounds the setting so a typo cannot park cases for a century.
const MaxRespondDays = 365

// ResolveRespondDays returns the response window for a community, falling back
// to the default when unset and clamping absurd values.
func ResolveRespondDays(c *Community) int {
	if c == nil {
		return DefaultRespondDays
	}
	d := c.Details.CourtProcessing.RespondDays
	if d <= 0 {
		return DefaultRespondDays
	}
	if d > MaxRespondDays {
		return MaxRespondDays
	}
	return d
}

// AutoFileEnabled reports whether a community has opted into filing unanswered
// citations and arrests for a judge to see.
func AutoFileEnabled(c *Community) bool {
	return c != nil && c.Details.CourtProcessing.AutoFileUnanswered
}

// FailureToRespondCandidate is the shape the sweep needs from either source —
// a criminal-history entry or an arrest report — so the eligibility rule is
// written once instead of twice.
type FailureToRespondCandidate struct {
	ItemID      string
	ItemType    string // "citation" | "warning" | "arrest"
	Status      string // "" | "contested" | "dismissed"
	CourtCaseID string
	CreatedAt   time.Time
	Redacted    bool
}

// EligibleForFailureToRespond decides whether an unanswered item should be
// filed as a court case.
//
// Every condition here is a way of NOT touching something a human already dealt
// with:
//
//   - a status of anything but empty means it was contested or already ruled on
//   - a CourtCaseID means it is already in front of a judge
//   - a redacted entry has been deliberately hidden and must stay hidden
//   - a zero CreatedAt means we cannot age it, and guessing would file records
//     of unknown age on the first run
//
// The window is exclusive: an item is eligible only once strictly more than
// respondDays have passed, so a civilian gets the whole final day.
func EligibleForFailureToRespond(c FailureToRespondCandidate, now time.Time, respondDays int) bool {
	if c.Redacted {
		return false
	}
	if strings.TrimSpace(c.Status) != "" {
		return false
	}
	if strings.TrimSpace(c.CourtCaseID) != "" {
		return false
	}
	if c.CreatedAt.IsZero() {
		return false
	}
	if respondDays <= 0 {
		respondDays = DefaultRespondDays
	}
	// Only things a court can actually act on. A warning carries no penalty to
	// contest, so filing one wastes a judge's time.
	switch strings.ToLower(strings.TrimSpace(c.ItemType)) {
	case "citation", "ticket", "arrest":
	default:
		return false
	}
	deadline := c.CreatedAt.AddDate(0, 0, respondDays)
	return now.After(deadline)
}
