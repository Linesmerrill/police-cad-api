package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/linesmerrill/police-cad-api/models"
	"github.com/linesmerrill/police-cad-api/platforms"
)

// fakeFetcher returns canned channel data so screening can be exercised without
// touching YouTube or Twitch.
type fakeFetcher struct {
	info platforms.ChannelInfo
	err  error
}

func (f fakeFetcher) Fetch(context.Context, string) (platforms.ChannelInfo, error) {
	return f.info, f.err
}

func fetcherReturning(info platforms.ChannelInfo, err error) channelFetcher {
	return func(string) (platforms.Fetcher, error) { return fakeFetcher{info, err}, nil }
}

func manualFetcher() channelFetcher {
	return func(string) (platforms.Fetcher, error) { return nil, platforms.ErrManualOnly }
}

func appWith(p models.ContentCreatorPlatform, attempts int) *models.ContentCreatorApplication {
	return &models.ContentCreatorApplication{
		Platforms:     []models.ContentCreatorPlatform{p},
		CheckAttempts: attempts,
	}
}

func checkFor(res screenResult, key string) (models.ApplicationCheck, bool) {
	for _, c := range res.Checks {
		if c.Key == key {
			return c, true
		}
	}
	return models.ApplicationCheck{}, false
}

func TestScreenApplication(t *testing.T) {
	ctx := context.Background()
	yt := models.ContentCreatorPlatform{Type: "youtube", Handle: "cryptic", VerificationCode: "LPC-VERIFY-ABC123"}

	t.Run("everything good passes and is ready for a human", func(t *testing.T) {
		res := screenApplication(ctx, appWith(yt, 0), fetcherReturning(
			platforms.ChannelInfo{Description: "streamer LPC-VERIFY-ABC123", FollowerCount: 30000}, nil))

		assert.True(t, res.Passed)
		assert.False(t, res.Blocked)
		assert.True(t, res.OwnershipVerified[0])
		assert.Equal(t, 30000, res.FollowerCounts[0])
	})

	t.Run("a channel that does not exist blocks and says so plainly", func(t *testing.T) {
		res := screenApplication(ctx, appWith(yt, 0), fetcherReturning(
			platforms.ChannelInfo{}, platforms.ErrChannelNotFound))

		assert.True(t, res.Blocked)
		assert.False(t, res.Passed)
		c, ok := checkFor(res, models.CheckChannelResolves)
		assert.True(t, ok)
		assert.Equal(t, models.CheckFailed, c.Status)
		assert.Contains(t, c.Reason, "could not find")
	})

	t.Run("missing code stays pending and retries rather than rejecting", func(t *testing.T) {
		res := screenApplication(ctx, appWith(yt, 0), fetcherReturning(
			platforms.ChannelInfo{Description: "no code here", FollowerCount: 30000}, nil))

		c, _ := checkFor(res, models.CheckOwnership)
		assert.Equal(t, models.CheckPending, c.Status)
		assert.False(t, res.Blocked, "someone who has not edited their bio yet is not a liar")
		assert.False(t, res.Passed, "but nobody should be emailed about it either")
	})

	t.Run("missing code eventually gives up", func(t *testing.T) {
		res := screenApplication(ctx, appWith(yt, maxOwnershipAttempts), fetcherReturning(
			platforms.ChannelInfo{Description: "still no code", FollowerCount: 30000}, nil))

		c, _ := checkFor(res, models.CheckOwnership)
		assert.Equal(t, models.CheckFailed, c.Status)
		assert.True(t, res.Blocked)
	})

	t.Run("too few followers blocks with the real number", func(t *testing.T) {
		res := screenApplication(ctx, appWith(yt, 0), fetcherReturning(
			platforms.ChannelInfo{Description: "LPC-VERIFY-ABC123", FollowerCount: 120}, nil))

		c, _ := checkFor(res, models.CheckFollowers)
		assert.Equal(t, models.CheckFailed, c.Status)
		assert.Contains(t, c.Reason, "120")
		assert.Contains(t, c.Reason, "500")
		assert.True(t, res.Blocked)
	})

	t.Run("a wrong claim still passes when the real count clears the bar", func(t *testing.T) {
		// The applicant guessed 900; reality is 4000. Both clear 500, so the
		// discrepancy is not a reason to reject.
		p := yt
		p.FollowerCount = 900
		res := screenApplication(ctx, appWith(p, 0), fetcherReturning(
			platforms.ChannelInfo{Description: "LPC-VERIFY-ABC123", FollowerCount: 4000}, nil))

		c, _ := checkFor(res, models.CheckFollowers)
		assert.Equal(t, models.CheckPassed, c.Status)
		assert.True(t, res.Passed)
	})

	t.Run("an inflated claim fails on the real number, not the claim", func(t *testing.T) {
		p := yt
		p.FollowerCount = 50000 // claimed
		res := screenApplication(ctx, appWith(p, 0), fetcherReturning(
			platforms.ChannelInfo{Description: "LPC-VERIFY-ABC123", FollowerCount: 12}, nil))

		c, _ := checkFor(res, models.CheckFollowers)
		assert.Equal(t, models.CheckFailed, c.Status)
		assert.Contains(t, c.Reason, "12", "the real count is what gets reported")
		assert.True(t, res.Blocked)
	})

	t.Run("our own outage never blames the applicant", func(t *testing.T) {
		res := screenApplication(ctx, appWith(yt, 0), fetcherReturning(
			platforms.ChannelInfo{}, platforms.ErrNotConfigured))

		c, _ := checkFor(res, models.CheckChannelResolves)
		assert.Equal(t, models.CheckPending, c.Status)
		assert.False(t, res.Blocked, "a missing API key is our fault, not theirs")
		assert.False(t, res.Passed)
	})

	t.Run("manual-only platforms wait for a human without blocking", func(t *testing.T) {
		tk := models.ContentCreatorPlatform{Type: "tiktok", Handle: "someone", FollowerCount: 5000, VerifiedByAdmin: true}
		res := screenApplication(ctx, appWith(tk, 0), manualFetcher())

		assert.False(t, res.Blocked)
		assert.True(t, res.Passed, "an admin-vouched platform should not hold up review")
	})

	t.Run("the largest channel decides the follower check", func(t *testing.T) {
		app := &models.ContentCreatorApplication{Platforms: []models.ContentCreatorPlatform{
			{Type: "youtube", Handle: "big", VerificationStatus: models.PlatformVerified, FollowerCount: 30000},
			{Type: "twitch", Handle: "small", VerificationStatus: models.PlatformVerified, FollowerCount: 20},
		}}
		res := screenApplication(ctx, app, fetcherReturning(platforms.ChannelInfo{}, nil))

		c, _ := checkFor(res, models.CheckFollowers)
		assert.Equal(t, models.CheckPassed, c.Status, "a small second channel must not sink a big first one")
		assert.True(t, res.Passed)
	})

	t.Run("already-verified platforms are not re-fetched", func(t *testing.T) {
		p := models.ContentCreatorPlatform{
			Type: "youtube", Handle: "cryptic",
			VerificationStatus: models.PlatformVerified, FollowerCount: 30000,
		}
		called := false
		fetcher := func(string) (platforms.Fetcher, error) {
			called = true
			return fakeFetcher{}, nil
		}
		res := screenApplication(ctx, appWith(p, 0), fetcher)

		assert.False(t, called, "re-fetching a verified channel burns quota for nothing")
		assert.True(t, res.Passed)
	})

	t.Run("reasons are written for the applicant, not for a log", func(t *testing.T) {
		res := screenApplication(ctx, appWith(yt, 0), fetcherReturning(
			platforms.ChannelInfo{Description: "LPC-VERIFY-ABC123", FollowerCount: 3}, nil))
		for _, c := range res.Checks {
			if c.Reason == "" {
				continue
			}
			assert.False(t, strings.Contains(strings.ToLower(c.Reason), "err"),
				"check reason %q leaks internals", c.Reason)
		}
	})
}
