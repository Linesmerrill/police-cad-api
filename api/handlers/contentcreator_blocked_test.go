package handlers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/linesmerrill/police-cad-api/models"
	"github.com/linesmerrill/police-cad-api/platforms"
)

func tiktokPlatform(claimed int) models.ContentCreatorPlatform {
	return models.ContentCreatorPlatform{
		Type: "tiktok", Handle: "daypixie", FollowerCount: claimed,
		VerificationCode: "LPC-VERIFY-2WY32A",
	}
}

func checkStatus(res screenResult, key string) string {
	for _, c := range res.Checks {
		if c.Key == key {
			return c.Status
		}
	}
	return "<missing>"
}

// The deadlock this fixes: a blocked platform left the follower check pending,
// pending forced screenResult.Passed to false, and Passed is the only thing that
// triggers the admin review email. So the application sat invisible forever,
// waiting on a human who was never told it existed.
func TestScreen_BlockedPlatformIsReviewableNotStuck(t *testing.T) {
	res := screenApplication(context.Background(), appWith(tiktokPlatform(1200), 0),
		fetcherReturning(platforms.ChannelInfo{}, platforms.ErrPlatformBlocked))

	assert.True(t, res.Passed, "a blocked platform must still reach a reviewer; Passed is what sends the admin email")
	assert.False(t, res.Blocked, "being refused by the platform is our problem, never the applicant's")

	for _, key := range []string{models.CheckChannelResolves, models.CheckOwnership, models.CheckFollowers} {
		assert.Equal(t, models.CheckManual, checkStatus(res, key),
			"%s should fall back to manual, never pending: pending is what blocks the email", key)
	}

	// So the caller knows to raise the warning.
	assert.Equal(t, []string{"tiktok"}, res.PlatformsBlocked)
}

// The manual follower reason has to carry the applicant's own number, because
// when we are blocked it is the only figure anybody has to judge.
func TestScreen_BlockedFollowerReasonNamesTheClaim(t *testing.T) {
	res := screenApplication(context.Background(), appWith(tiktokPlatform(1200), 0),
		fetcherReturning(platforms.ChannelInfo{}, platforms.ErrPlatformBlocked))

	var reason string
	for _, c := range res.Checks {
		if c.Key == models.CheckFollowers {
			reason = c.Reason
		}
	}
	assert.Contains(t, reason, "1.2K", "a reviewer needs the number they are being asked to weigh")
}

// A handle that does not exist is the applicant's to fix and must still fail,
// otherwise "blocked" becomes a way to launder a typo into a manual pass.
func TestScreen_NotFoundStillFailsWhenBlockedDoesNot(t *testing.T) {
	res := screenApplication(context.Background(), appWith(tiktokPlatform(1200), 0),
		fetcherReturning(platforms.ChannelInfo{}, platforms.ErrChannelNotFound))

	assert.True(t, res.Blocked, "a handle that does not resolve is a real failure")
	assert.Equal(t, models.CheckFailed, checkStatus(res, models.CheckChannelResolves))
	assert.Empty(t, res.PlatformsBlocked, "a typo is not a platform outage and must not raise a warning")
}

// When the fetch works, TikTok behaves like any other measured platform: the
// code is found in the bio and the real follower count decides the bar.
func TestScreen_TikTokVerifiesFromTheBio(t *testing.T) {
	res := screenApplication(context.Background(), appWith(tiktokPlatform(0), 0),
		fetcherReturning(platforms.ChannelInfo{
			Handle:        "daypixie",
			Description:   "gaming 🎮\nLPC-VERIFY-2WY32A",
			FollowerCount: 1009,
		}, nil))

	assert.Equal(t, models.CheckPassed, checkStatus(res, models.CheckChannelResolves))
	assert.Equal(t, models.CheckPassed, checkStatus(res, models.CheckOwnership),
		"the code sits in the bio; a newline before it must not hide it")
	assert.True(t, res.OwnershipProven)
	assert.Empty(t, res.PlatformsBlocked)
}
