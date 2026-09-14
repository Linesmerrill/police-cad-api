package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/linesmerrill/police-cad-api/models"
)

// A community whose members trade property cannot move $600,000 in one go under
// a flat $100,000 cap, which is what the request that prompted this described.
// The cap stays — it bounds a fat finger or a stolen account — but the community
// sets where it sits.

func communityWithTransferMax(max int64) *models.Community {
	return &models.Community{
		Details: models.CommunityDetails{
			Economy: models.EconomySettings{Enabled: true, MaxTransferCents: max},
		},
	}
}

func TestResolveTransferMax(t *testing.T) {
	t.Run("unset falls back to the default", func(t *testing.T) {
		assert.Equal(t, int64(DefaultTransferMaxCents), ResolveTransferMax(communityWithTransferMax(0)))
	})

	t.Run("a configured limit is used", func(t *testing.T) {
		assert.Equal(t, int64(1_000_000_00), ResolveTransferMax(communityWithTransferMax(1_000_000_00)))
	})

	t.Run("the reported case now fits", func(t *testing.T) {
		// A $600,000 property sale, with the community's limit set to $1M.
		max := ResolveTransferMax(communityWithTransferMax(1_000_000_00))
		assert.GreaterOrEqual(t, max, int64(600_000_00))
	})

	t.Run("a stored value above the ceiling is clamped", func(t *testing.T) {
		// The rail moves, it does not come off.
		assert.Equal(t, int64(TransferCeilingCents),
			ResolveTransferMax(communityWithTransferMax(TransferCeilingCents*10)))
	})

	t.Run("a negative stored value falls back rather than blocking everything", func(t *testing.T) {
		assert.Equal(t, int64(DefaultTransferMaxCents), ResolveTransferMax(communityWithTransferMax(-5)))
	})

	t.Run("a nil community falls back to the default", func(t *testing.T) {
		assert.Equal(t, int64(DefaultTransferMaxCents), ResolveTransferMax(nil))
	})

	t.Run("the default is unchanged", func(t *testing.T) {
		// Existing communities must see exactly the behaviour they had before.
		assert.Equal(t, int64(100_000_00), int64(DefaultTransferMaxCents))
	})
}

func TestFormatCents(t *testing.T) {
	assert.Equal(t, "$100,000.00", formatCents(100_000_00))
	assert.Equal(t, "$1,000,000.00", formatCents(1_000_000_00))
	assert.Equal(t, "$600,000.00", formatCents(600_000_00))
	assert.Equal(t, "$0.50", formatCents(50))
	assert.Equal(t, "$12.34", formatCents(1234))
	assert.Equal(t, "$999.99", formatCents(99999))
}
