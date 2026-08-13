package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/linesmerrill/police-cad-api/models"
)

// The submission gate has one job that is easy to break by accident: the form
// stopped asking for a follower count on the platforms we measure, so the old
// "max claimed >= 500" rule would now 400 every YouTube and Twitch applicant.
func TestFollowerBarSatisfiable(t *testing.T) {
	p := func(kind string, followers int) models.ContentCreatorPlatform {
		return models.ContentCreatorPlatform{Type: kind, Handle: "someone", FollowerCount: followers}
	}

	t.Run("a measured platform submits without a claimed count", func(t *testing.T) {
		assert.True(t, followerBarSatisfiable([]models.ContentCreatorPlatform{p("youtube", 0)}))
		assert.True(t, followerBarSatisfiable([]models.ContentCreatorPlatform{p("twitch", 0)}))
	})

	t.Run("a measured platform is not pre-judged on a low claim either", func(t *testing.T) {
		// Screening decides this one on the real number a moment later. Rejecting
		// it here on a guess would turn a typo into a closed door.
		assert.True(t, followerBarSatisfiable([]models.ContentCreatorPlatform{p("youtube", 12)}))
	})

	t.Run("platforms we cannot read must clear the bar themselves", func(t *testing.T) {
		assert.False(t, followerBarSatisfiable([]models.ContentCreatorPlatform{p("tiktok", 499)}))
		assert.False(t, followerBarSatisfiable([]models.ContentCreatorPlatform{p("other", 0)}))
		assert.True(t, followerBarSatisfiable([]models.ContentCreatorPlatform{p("tiktok", models.MinFollowers)}))
	})

	t.Run("one measured platform carries the whole application", func(t *testing.T) {
		assert.True(t, followerBarSatisfiable([]models.ContentCreatorPlatform{
			p("tiktok", 3), p("youtube", 0),
		}))
	})

	t.Run("no platforms at all cannot clear it", func(t *testing.T) {
		assert.False(t, followerBarSatisfiable(nil))
	})
}

func TestSplitCSV(t *testing.T) {
	assert.Equal(t, []string{"submitted", "under_review"}, splitCSV("submitted,under_review"))
	assert.Equal(t, []string{"submitted", "under_review"}, splitCSV(" submitted , under_review "))
	assert.Equal(t, []string{"submitted"}, splitCSV("submitted"))
	// A trailing comma must not put an empty status into the filter — that
	// would match nothing and quietly empty the admin queue.
	assert.Equal(t, []string{"submitted"}, splitCSV("submitted,"))
	assert.Empty(t, splitCSV(",  ,"))
}
