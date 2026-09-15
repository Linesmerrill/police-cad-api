package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// An owner could type -20, 0 and around 4 billion into a settings field. These
// are the reasonable ranges the API enforces, mirrored in the website form
// (public/js/settings-validation.js) and the mobile form
// (utils/settingsValidation.js). If one changes, change all three.

func econPatch(field string, v float64) map[string]interface{} {
	return map[string]interface{}{field: v}
}

func TestEconomyPatchDayRanges(t *testing.T) {
	for _, field := range []string{"defaultDueDays", "contestExtensionDays"} {
		t.Run(field, func(t *testing.T) {
			for v, ok := range map[float64]bool{0: false, 1: true, 14: true, 365: true, 366: false, -20: false, 4e9: false} {
				_, err := validateCommunityEconomyPatch(econPatch(field, v))
				if ok {
					assert.NoError(t, err, "%v", v)
				} else {
					assert.Error(t, err, "%v", v)
				}
			}
		})
	}
}

func TestEconomyPatchStartingBalanceRange(t *testing.T) {
	for v, ok := range map[float64]bool{0: true, 50000: true, 1_000_000_000: true, 1_000_000_001: false, -1: false, 4e11: false} {
		_, err := validateCommunityEconomyPatch(econPatch("defaultStartingBalance", v))
		if ok {
			assert.NoError(t, err, "%v", v)
		} else {
			assert.Error(t, err, "%v", v)
		}
	}
}

func TestEconomyPatchMaxTransferRange(t *testing.T) {
	cases := map[float64]bool{
		0:                                 true,  // use the default
		50:                                false, // a cap under $1 makes no sense
		100:                               true,
		float64(TransferCeilingCents):     true,
		float64(TransferCeilingCents) + 1: false,
		-100:                              false,
	}
	for v, ok := range cases {
		_, err := validateCommunityEconomyPatch(econPatch("maxTransferCents", v))
		if ok {
			assert.NoError(t, err, "%v", v)
		} else {
			assert.Error(t, err, "%v", v)
		}
	}
}

func TestCourtProcessingRespondDaysRange(t *testing.T) {
	// 0 used to be accepted, which is how a typed 0 could "save" without error.
	for v, ok := range map[float64]bool{0: false, 1: true, 3: true, 365: true, 366: false, -20: false, 4e9: false} {
		_, err := validateCommunityCourtProcessingPatch(map[string]interface{}{"respondDays": v})
		if ok {
			assert.NoError(t, err, "%v", v)
		} else {
			assert.Error(t, err, "%v", v)
		}
	}
}
