package handlers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/linesmerrill/police-cad-api/models"
)

// The approval gate is the single point that stops a stolen channel becoming a
// real grant, so it has to be exact about what counts as verified.
func TestUnverifiedPlatformsGatesApproval(t *testing.T) {
	verifiedByAPI := models.ContentCreatorPlatform{
		Type: "youtube", Handle: "cryptic", VerificationStatus: models.PlatformVerified,
	}
	verifiedByAdmin := models.ContentCreatorPlatform{
		Type: "tiktok", Handle: "someone", VerificationStatus: models.PlatformVerified, VerificationMethod: "admin",
	}
	legacyAdminTick := models.ContentCreatorPlatform{
		Type: "twitch", Handle: "old", VerifiedByAdmin: true,
	}
	pending := models.ContentCreatorPlatform{
		Type: "youtube", Handle: "notmine", VerificationStatus: models.PlatformPending,
	}
	failed := models.ContentCreatorPlatform{
		Type: "youtube", Handle: "bogus", VerificationStatus: models.PlatformFailed,
	}
	untouched := models.ContentCreatorPlatform{Type: "youtube", Handle: "fresh"}

	t.Run("all verified passes", func(t *testing.T) {
		app := &models.ContentCreatorApplication{Platforms: []models.ContentCreatorPlatform{verifiedByAPI, verifiedByAdmin}}
		assert.Empty(t, unverifiedPlatforms(app))
	})

	t.Run("a legacy admin tick still counts", func(t *testing.T) {
		// VerifiedByAdmin predates this system. Records carrying it must not be
		// retroactively blocked from approval.
		app := &models.ContentCreatorApplication{Platforms: []models.ContentCreatorPlatform{legacyAdminTick}}
		assert.Empty(t, unverifiedPlatforms(app))
	})

	t.Run("pending blocks", func(t *testing.T) {
		app := &models.ContentCreatorApplication{Platforms: []models.ContentCreatorPlatform{verifiedByAPI, pending}}
		got := unverifiedPlatforms(app)
		assert.Len(t, got, 1)
		assert.Contains(t, got[0], "notmine", "the blocking platform must be named so the admin knows which")
	})

	t.Run("failed blocks", func(t *testing.T) {
		app := &models.ContentCreatorApplication{Platforms: []models.ContentCreatorPlatform{failed}}
		assert.Len(t, unverifiedPlatforms(app), 1)
	})

	t.Run("never-attempted blocks", func(t *testing.T) {
		// The dangerous default: an application submitted before verification
		// existed, or one where the applicant simply never tried.
		app := &models.ContentCreatorApplication{Platforms: []models.ContentCreatorPlatform{untouched}}
		assert.Len(t, unverifiedPlatforms(app), 1)
	})

	t.Run("every unverified platform is reported, not just the first", func(t *testing.T) {
		app := &models.ContentCreatorApplication{Platforms: []models.ContentCreatorPlatform{pending, failed, untouched}}
		assert.Len(t, unverifiedPlatforms(app), 3)
	})

	t.Run("an application with no platforms does not block", func(t *testing.T) {
		app := &models.ContentCreatorApplication{}
		assert.Empty(t, unverifiedPlatforms(app))
	})

	t.Run("falls back to url when a handle is missing", func(t *testing.T) {
		app := &models.ContentCreatorApplication{Platforms: []models.ContentCreatorPlatform{
			{Type: "other", URL: "https://example.com/me"},
		}}
		got := unverifiedPlatforms(app)
		assert.Len(t, got, 1)
		assert.Contains(t, got[0], "example.com", "an admin needs something identifying, even without a handle")
	})
}

func TestIsVerifiedAcceptsBothRoutes(t *testing.T) {
	assert.True(t, models.ContentCreatorPlatform{VerificationStatus: models.PlatformVerified}.IsVerified())
	assert.True(t, models.ContentCreatorPlatform{VerifiedByAdmin: true}.IsVerified())
	assert.False(t, models.ContentCreatorPlatform{VerificationStatus: models.PlatformPending}.IsVerified())
	assert.False(t, models.ContentCreatorPlatform{VerificationStatus: models.PlatformFailed}.IsVerified())
	assert.False(t, models.ContentCreatorPlatform{}.IsVerified())
}

// A guessable code would let someone verify a channel they do not own by
// putting a predictable string in any bio.
func TestNewChannelVerificationCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, err := newChannelVerificationCode()
		assert.NoError(t, err)
		assert.True(t, strings.HasPrefix(code, "LPC-VERIFY-"), "codes must be recognizable in a bio")
		assert.Len(t, code, len("LPC-VERIFY-")+channelCodeLen)
		assert.False(t, seen[code], "codes must not repeat")
		seen[code] = true

		// Confusable characters would cause failed checks when someone copies
		// the code by eye out of their channel description.
		body := strings.TrimPrefix(code, "LPC-VERIFY-")
		assert.NotContains(t, body, "O")
		assert.NotContains(t, body, "0")
		assert.NotContains(t, body, "I")
		assert.NotContains(t, body, "1")
		assert.NotContains(t, body, "L")
	}
}

func TestChannelInstructionNamesTheRightField(t *testing.T) {
	// Pointing someone at the wrong field means a failed check they cannot
	// diagnose, so each platform gets its own wording.
	assert.Contains(t, strings.ToLower(channelInstruction("youtube")), "description")
	assert.Contains(t, strings.ToLower(channelInstruction("twitch")), "about")
	// TikTok cannot be automated; the copy must say a human checks it rather
	// than implying the Check button will do something.
	assert.Contains(t, strings.ToLower(channelInstruction("tiktok")), "team")
	assert.NotEmpty(t, channelInstruction("other"))
}
