package handlers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/linesmerrill/police-cad-api/models"
	templates "github.com/linesmerrill/police-cad-api/templates/html"
)

func coded(platformType, code string) models.ContentCreatorPlatform {
	return models.ContentCreatorPlatform{Type: platformType, VerificationCode: code}
}

// The bug this exists for: an approved creator's channel description still
// opened with LPC-VERIFY-XXXXXX weeks later, because nothing ever told them the
// code had done its job.
func TestCodeRemovalTarget(t *testing.T) {
	t.Run("no code was ever issued, so there is nothing to undo", func(t *testing.T) {
		assert.Equal(t, "", codeRemovalTarget(nil))
		assert.Equal(t, "", codeRemovalTarget([]models.ContentCreatorPlatform{
			{Type: "youtube"},
			{Type: "twitch", VerificationCode: "   "},
		}))
	})

	t.Run("one named platform", func(t *testing.T) {
		assert.Equal(t, "your YouTube description",
			codeRemovalTarget([]models.ContentCreatorPlatform{coded("youtube", "LPC-VERIFY-ABC123")}))
	})

	t.Run("platform casing is the platform's own", func(t *testing.T) {
		// "Youtube" in an email to a YouTuber reads like nobody has seen the site.
		assert.Contains(t, codeRemovalTarget([]models.ContentCreatorPlatform{coded("YOUTUBE", "c")}), "YouTube")
		assert.Contains(t, codeRemovalTarget([]models.ContentCreatorPlatform{coded("tiktok", "c")}), "TikTok")
	})

	t.Run("two named platforms read as a sentence", func(t *testing.T) {
		assert.Equal(t, "your YouTube and Twitch descriptions", codeRemovalTarget([]models.ContentCreatorPlatform{
			coded("youtube", "c1"), coded("twitch", "c2"),
		}))
	})

	t.Run("three named platforms", func(t *testing.T) {
		assert.Equal(t, "your YouTube, Twitch and TikTok descriptions", codeRemovalTarget([]models.ContentCreatorPlatform{
			coded("youtube", "c1"), coded("twitch", "c2"), coded("tiktok", "c3"),
		}))
	})

	t.Run("the same platform twice is named once", func(t *testing.T) {
		assert.Equal(t, "your YouTube description", codeRemovalTarget([]models.ContentCreatorPlatform{
			coded("youtube", "c1"), coded("youtube", "c2"),
		}))
	})

	t.Run("an unnamed platform falls back to generic wording", func(t *testing.T) {
		// platformDisplayName maps "other" to "your channel", which would
		// otherwise read as "your YouTube and your channel descriptions".
		assert.Equal(t, "your channel description",
			codeRemovalTarget([]models.ContentCreatorPlatform{coded("other", "c")}))
	})

	t.Run("named and unnamed together goes generic", func(t *testing.T) {
		// Naming half of them reads as "your YouTube and your other channels
		// descriptions", which no person would say out loud.
		assert.Equal(t, "your channel descriptions", codeRemovalTarget([]models.ContentCreatorPlatform{
			coded("youtube", "c1"), coded("other", "c2"),
		}))
	})

	t.Run("two unnamed platforms pluralise", func(t *testing.T) {
		assert.Equal(t, "your channel descriptions", codeRemovalTarget([]models.ContentCreatorPlatform{
			coded("other", "c1"), coded("", "c2"),
		}))
	})

	t.Run("a platform without a code is not named", func(t *testing.T) {
		assert.Equal(t, "your YouTube description", codeRemovalTarget([]models.ContentCreatorPlatform{
			coded("youtube", "c1"), {Type: "twitch"},
		}))
	})
}

func TestJoinWithAnd(t *testing.T) {
	assert.Equal(t, "", joinWithAnd(nil))
	assert.Equal(t, "a", joinWithAnd([]string{"a"}))
	assert.Equal(t, "a and b", joinWithAnd([]string{"a", "b"}))
	assert.Equal(t, "a, b and c", joinWithAnd([]string{"a", "b", "c"}))
}

func TestDecisionEmailsCarryTheReminder(t *testing.T) {
	const target = "your YouTube description"

	t.Run("approved email includes it", func(t *testing.T) {
		body := templates.RenderApplicationApprovedEmail("Besh", target)
		assert.Contains(t, body, "you can take it out now")
		assert.Contains(t, body, target)
	})

	t.Run("rejected email includes it", func(t *testing.T) {
		body := templates.RenderApplicationRejectedEmail("Besh", "Not enough LPC content.", "", target)
		assert.Contains(t, body, "you can take it out now")
		assert.Contains(t, body, target)
		// The rejection reason must survive the extra format argument.
		assert.Contains(t, body, "Not enough LPC content.")
	})

	t.Run("rejected email keeps feedback alongside the reminder", func(t *testing.T) {
		body := templates.RenderApplicationRejectedEmail("Besh", "Reason here", "Some feedback", target)
		assert.Contains(t, body, "Some feedback")
		assert.Contains(t, body, "Reason here")
		assert.Contains(t, body, target)
	})

	t.Run("an empty target leaves the note off entirely", func(t *testing.T) {
		for _, body := range []string{
			templates.RenderApplicationApprovedEmail("Besh", ""),
			templates.RenderApplicationRejectedEmail("Besh", "r", "f", ""),
		} {
			assert.NotContains(t, body, "you can take it out now")
			assert.NotContains(t, body, "One small thing")
		}
	})

	t.Run("the follower-minimum decision includes it too", func(t *testing.T) {
		steps := []string{"Grow the channel.", "Apply again."}
		body := templates.RenderRequirementNotMetEmail("Besh", "At least 500 followers.", "120 followers.", target, steps)
		assert.Contains(t, body, "you can take it out now")
		assert.Contains(t, body, target)
		assert.Contains(t, body, "At least 500 followers.")
		assert.Contains(t, body, "Grow the channel.")

		bare := templates.RenderRequirementNotMetEmail("Besh", "r", "m", "", steps)
		assert.NotContains(t, bare, "One small thing")
		assert.Contains(t, bare, "Apply again.")
	})

	t.Run("no format verbs are left unconsumed", func(t *testing.T) {
		// A miscounted Sprintf argument shows up as %!s(MISSING) or %!(EXTRA…)
		// in the delivered email rather than as a build error.
		for _, body := range []string{
			templates.RenderApplicationApprovedEmail("Besh", target),
			templates.RenderApplicationApprovedEmail("Besh", ""),
			templates.RenderApplicationRejectedEmail("Besh", "r", "f", target),
			templates.RenderApplicationRejectedEmail("Besh", "r", "", ""),
			templates.RenderRequirementNotMetEmail("Besh", "r", "m", target, []string{"s"}),
			templates.RenderRequirementNotMetEmail("Besh", "r", "m", "", nil),
		} {
			assert.False(t, strings.Contains(body, "%!"), "stray format verb in rendered email")
		}
	})
}
