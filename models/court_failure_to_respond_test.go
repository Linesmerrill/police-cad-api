package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A citation or arrest reaches a judge today only if the civilian contests it,
// so anyone who ignores a ticket never surfaces judicially and the case is
// lost. These rules decide what gets filed on their behalf — and, more
// importantly, what must never be touched.

func candidate(mods func(*FailureToRespondCandidate)) FailureToRespondCandidate {
	c := FailureToRespondCandidate{
		ItemID:    "abc",
		ItemType:  "citation",
		Status:    "",
		CreatedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	}
	if mods != nil {
		mods(&c)
	}
	return c
}

func TestEligibleForFailureToRespond(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC) // 9 days later

	t.Run("an ignored citation past the window is filed", func(t *testing.T) {
		assert.True(t, EligibleForFailureToRespond(candidate(nil), now, 3))
	})

	t.Run("an arrest is filed too", func(t *testing.T) {
		c := candidate(func(c *FailureToRespondCandidate) { c.ItemType = "arrest" })
		assert.True(t, EligibleForFailureToRespond(c, now, 3))
	})

	t.Run("a warning is not, since there is nothing to contest", func(t *testing.T) {
		c := candidate(func(c *FailureToRespondCandidate) { c.ItemType = "warning" })
		assert.False(t, EligibleForFailureToRespond(c, now, 3))
	})

	t.Run("already contested is left alone", func(t *testing.T) {
		c := candidate(func(c *FailureToRespondCandidate) { c.Status = "contested" })
		assert.False(t, EligibleForFailureToRespond(c, now, 3))
	})

	t.Run("already dismissed is left alone", func(t *testing.T) {
		c := candidate(func(c *FailureToRespondCandidate) { c.Status = "dismissed" })
		assert.False(t, EligibleForFailureToRespond(c, now, 3))
	})

	t.Run("already in front of a judge is left alone", func(t *testing.T) {
		c := candidate(func(c *FailureToRespondCandidate) { c.CourtCaseID = "case123" })
		assert.False(t, EligibleForFailureToRespond(c, now, 3))
	})

	t.Run("a redacted record stays hidden", func(t *testing.T) {
		// Redaction is a deliberate act; re-surfacing it in a court case would
		// undo somebody's decision.
		c := candidate(func(c *FailureToRespondCandidate) { c.Redacted = true })
		assert.False(t, EligibleForFailureToRespond(c, now, 3))
	})

	t.Run("an undateable record is never filed", func(t *testing.T) {
		// Without a createdAt we cannot age it, and guessing would file every
		// record of unknown age on the very first run.
		c := candidate(func(c *FailureToRespondCandidate) { c.CreatedAt = time.Time{} })
		assert.False(t, EligibleForFailureToRespond(c, now, 3))
	})

	t.Run("the civilian gets the whole final day", func(t *testing.T) {
		issued := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
		c := candidate(func(c *FailureToRespondCandidate) { c.CreatedAt = issued })

		// Exactly on the deadline: not yet.
		assert.False(t, EligibleForFailureToRespond(c, issued.AddDate(0, 0, 3), 3))
		// A second later: filed.
		assert.True(t, EligibleForFailureToRespond(c, issued.AddDate(0, 0, 3).Add(time.Second), 3))
		// Well inside the window: untouched.
		assert.False(t, EligibleForFailureToRespond(c, issued.Add(48*time.Hour), 3))
	})

	t.Run("a nonsense window falls back to the default", func(t *testing.T) {
		c := candidate(nil)
		assert.True(t, EligibleForFailureToRespond(c, now, 0))
		assert.True(t, EligibleForFailureToRespond(c, now, -5))
	})

	t.Run("case and padding in the type do not matter", func(t *testing.T) {
		for _, typ := range []string{"Citation", " CITATION ", "Arrest", "ticket"} {
			c := candidate(func(c *FailureToRespondCandidate) { c.ItemType = typ })
			assert.True(t, EligibleForFailureToRespond(c, now, 3), typ)
		}
	})

	t.Run("an unknown type is never filed", func(t *testing.T) {
		for _, typ := range []string{"", "note", "bolo", "medical"} {
			c := candidate(func(c *FailureToRespondCandidate) { c.ItemType = typ })
			assert.False(t, EligibleForFailureToRespond(c, now, 3), typ)
		}
	})
}

func TestResolveRespondDays(t *testing.T) {
	withDays := func(d int, on bool) *Community {
		return &Community{Details: CommunityDetails{
			CourtProcessing: CourtProcessingSettings{RespondDays: d, AutoFileUnanswered: on},
		}}
	}

	assert.Equal(t, DefaultRespondDays, ResolveRespondDays(withDays(0, true)))
	assert.Equal(t, DefaultRespondDays, ResolveRespondDays(withDays(-1, true)))
	assert.Equal(t, 7, ResolveRespondDays(withDays(7, true)))
	assert.Equal(t, MaxRespondDays, ResolveRespondDays(withDays(99999, true)))
	assert.Equal(t, DefaultRespondDays, ResolveRespondDays(nil))
}

func TestAutoFileEnabled(t *testing.T) {
	on := &Community{Details: CommunityDetails{
		CourtProcessing: CourtProcessingSettings{AutoFileUnanswered: true},
	}}
	off := &Community{}

	assert.True(t, AutoFileEnabled(on))
	assert.False(t, AutoFileEnabled(off), "opt-in: a community that never asked gets nothing")
	assert.False(t, AutoFileEnabled(nil))
}
