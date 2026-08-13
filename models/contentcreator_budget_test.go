package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Every manual Check spends a unit of a SHARED daily platform quota. Without a
// budget keyed to the application, one person holding down the button exhausts
// verification for everyone — and an IP-based limiter does not stop that, since
// IPs are trivially rotated.

func dt(t time.Time) *primitive.DateTime {
	d := primitive.NewDateTimeFromTime(t)
	return &d
}

func TestVerifyCheckBudget(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	t.Run("a first check is always allowed", func(t *testing.T) {
		ok, _, _ := ContentCreatorPlatform{}.VerifyCheckBudget(now)
		assert.True(t, ok)
	})

	t.Run("a second check inside the cooldown is refused", func(t *testing.T) {
		p := ContentCreatorPlatform{VerificationLastCheckedAt: dt(now.Add(-5 * time.Second))}
		ok, retryAfter, reason := p.VerifyCheckBudget(now)
		assert.False(t, ok)
		assert.True(t, retryAfter > 0 && retryAfter <= VerifyCheckCooldown)
		assert.Contains(t, reason, "wait", "the applicant needs to know it is temporary")
	})

	t.Run("a check after the cooldown is allowed", func(t *testing.T) {
		p := ContentCreatorPlatform{VerificationLastCheckedAt: dt(now.Add(-VerifyCheckCooldown - time.Second))}
		ok, _, _ := p.VerifyCheckBudget(now)
		assert.True(t, ok)
	})

	t.Run("the daily cap refuses further checks", func(t *testing.T) {
		p := ContentCreatorPlatform{
			VerificationLastCheckedAt:   dt(now.Add(-time.Hour)),
			VerificationCheckCount:      VerifyCheckDailyCap,
			VerificationCheckWindowFrom: dt(now.Add(-time.Hour)),
		}
		ok, retryAfter, reason := p.VerifyCheckBudget(now)
		assert.False(t, ok)
		assert.True(t, retryAfter > 0)
		// Being told to give up would be wrong; the scheduler keeps working.
		assert.Contains(t, reason, "automatically",
			"a capped applicant must know we keep checking for them")
	})

	t.Run("one below the cap is still allowed", func(t *testing.T) {
		p := ContentCreatorPlatform{
			VerificationLastCheckedAt:   dt(now.Add(-time.Hour)),
			VerificationCheckCount:      VerifyCheckDailyCap - 1,
			VerificationCheckWindowFrom: dt(now.Add(-time.Hour)),
		}
		ok, _, _ := p.VerifyCheckBudget(now)
		assert.True(t, ok)
	})

	t.Run("an expired window frees the cap again", func(t *testing.T) {
		// Someone who hit the cap yesterday must not be stuck forever.
		p := ContentCreatorPlatform{
			VerificationLastCheckedAt:   dt(now.Add(-VerifyCheckCapWindow - time.Hour)),
			VerificationCheckCount:      VerifyCheckDailyCap + 10,
			VerificationCheckWindowFrom: dt(now.Add(-VerifyCheckCapWindow - time.Hour)),
		}
		ok, _, _ := p.VerifyCheckBudget(now)
		assert.True(t, ok)
	})

	t.Run("the cooldown is checked before the cap", func(t *testing.T) {
		// Both are breached; the message should be the recoverable one.
		p := ContentCreatorPlatform{
			VerificationLastCheckedAt:   dt(now.Add(-time.Second)),
			VerificationCheckCount:      VerifyCheckDailyCap,
			VerificationCheckWindowFrom: dt(now.Add(-time.Hour)),
		}
		_, _, reason := p.VerifyCheckBudget(now)
		assert.Contains(t, reason, "wait")
	})
}

func TestNextVerifyCheckCounters(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	t.Run("first check opens the window at one", func(t *testing.T) {
		count, from := ContentCreatorPlatform{}.NextVerifyCheckCounters(now)
		assert.Equal(t, 1, count)
		assert.Equal(t, now, from)
	})

	t.Run("checks inside the window accumulate and keep the window", func(t *testing.T) {
		start := now.Add(-2 * time.Hour)
		p := ContentCreatorPlatform{VerificationCheckCount: 4, VerificationCheckWindowFrom: dt(start)}
		count, from := p.NextVerifyCheckCounters(now)
		assert.Equal(t, 5, count)
		assert.Equal(t, start.Unix(), from.Unix(), "the window must not slide, or the cap never resets")
	})

	t.Run("an expired window restarts the count", func(t *testing.T) {
		p := ContentCreatorPlatform{
			VerificationCheckCount:      99,
			VerificationCheckWindowFrom: dt(now.Add(-VerifyCheckCapWindow - time.Minute)),
		}
		count, from := p.NextVerifyCheckCounters(now)
		assert.Equal(t, 1, count)
		assert.Equal(t, now, from)
	})
}

// The point of the cap is a bound on quota spend. State it as a number so a
// future change to the constants has to face the arithmetic.
func TestDailyCapBoundsQuotaSpend(t *testing.T) {
	// YouTube's default free quota is 10,000 units/day; channels.list costs 1.
	const youTubeDailyQuota = 10000
	assert.LessOrEqual(t, VerifyCheckDailyCap*2, 100,
		"a single application (2 platforms) must not be able to spend 1% of the daily quota")

	applicationsToExhaust := youTubeDailyQuota / VerifyCheckDailyCap
	assert.GreaterOrEqual(t, applicationsToExhaust, 400,
		"it should take hundreds of distinct applications to exhaust the quota")
}
