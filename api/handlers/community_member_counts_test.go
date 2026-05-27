package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
)

// liveMemberCounts is the batched analogue of CommunityHandler's per-community
// self-heal. These tests pin its behaviour because the list handlers all rely
// on it to correct drifted stored values.

func TestLiveMemberCounts_ReturnsCountsKeyedByCommunityID(t *testing.T) {
	mockUDB := &mocks.UserDatabase{}

	aggResults := []interface{}{
		bson.M{"_id": "community-a", "count": 62},
		bson.M{"_id": "community-c", "count": 7},
	}
	cursor, err := databases.NewMongoCursorFromDocuments(aggResults)
	assert.NoError(t, err)

	mockUDB.On("Aggregate", mock.Anything, mock.Anything).Return(cursor, nil)

	got := liveMemberCounts(context.Background(), mockUDB, []string{"community-a", "community-b", "community-c"})

	assert.Equal(t, 62, got["community-a"], "live count overwrites stored value")
	assert.Equal(t, 7, got["community-c"])
	_, hasB := got["community-b"]
	assert.False(t, hasB,
		"communities with zero approved members aren't in the aggregation result; "+
			"caller must keep the stored value (often 0/1) when key is missing")
}

func TestLiveMemberCounts_AggregateError_ReturnsEmptyMap_NotError(t *testing.T) {
	mockUDB := &mocks.UserDatabase{}
	mockUDB.On("Aggregate", mock.Anything, mock.Anything).
		Return(databases.MongoCursor{}, errors.New("transient mongo error"))

	got := liveMemberCounts(context.Background(), mockUDB, []string{"community-a"})

	assert.Empty(t, got,
		"on aggregation failure the helper returns an empty map so callers "+
			"fall back to the stored values they already have")
}

func TestLiveMemberCounts_EmptyIDs_NoQuery(t *testing.T) {
	mockUDB := &mocks.UserDatabase{}
	// No Aggregate expectation — empty ID list must short-circuit.

	got := liveMemberCounts(context.Background(), mockUDB, nil)

	assert.Empty(t, got)
	mockUDB.AssertNotCalled(t, "Aggregate", mock.Anything, mock.Anything)
}
