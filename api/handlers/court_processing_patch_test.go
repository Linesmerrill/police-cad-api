package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
)

// respondDays is read by a cron job and autoFileUnanswered decides whether that
// job touches a community at all, so a value stored in a shape the BSON decoder
// cannot read would break both. Same reasoning as the economy validator.

func TestValidateCommunityCourtProcessingPatch(t *testing.T) {
	t.Run("accepts a well-formed patch", func(t *testing.T) {
		out, err := validateCommunityCourtProcessingPatch(map[string]interface{}{
			"autoFileUnanswered": true,
			"respondDays":        float64(7), // JSON numbers decode as float64
		})
		assert.NoError(t, err)
		assert.Equal(t, bson.M{"autoFileUnanswered": true, "respondDays": int64(7)}, out)
	})

	t.Run("accepts a partial patch", func(t *testing.T) {
		out, err := validateCommunityCourtProcessingPatch(map[string]interface{}{
			"autoFileUnanswered": false,
		})
		assert.NoError(t, err)
		assert.Equal(t, bson.M{"autoFileUnanswered": false}, out)
	})

	t.Run("rejects a non-boolean toggle", func(t *testing.T) {
		_, err := validateCommunityCourtProcessingPatch(map[string]interface{}{
			"autoFileUnanswered": "yes",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expected boolean")
	})

	t.Run("rejects a window beyond the maximum", func(t *testing.T) {
		_, err := validateCommunityCourtProcessingPatch(map[string]interface{}{
			"respondDays": float64(100000),
		})
		assert.Error(t, err)
	})

	t.Run("rejects a negative window", func(t *testing.T) {
		_, err := validateCommunityCourtProcessingPatch(map[string]interface{}{
			"respondDays": float64(-1),
		})
		assert.Error(t, err)
	})

	t.Run("rejects unknown fields rather than storing them", func(t *testing.T) {
		_, err := validateCommunityCourtProcessingPatch(map[string]interface{}{
			"autoSuspendLicense": true, // the half that is not built yet
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not updatable")
	})

	t.Run("rejects a non-object", func(t *testing.T) {
		_, err := validateCommunityCourtProcessingPatch("nope")
		assert.Error(t, err)
	})
}
