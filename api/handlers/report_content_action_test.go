package handlers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// stubUpdate records what a takedown writes.
func stubUpdate(db *mocks.DatabaseHelper, name string, capture *[]interface{}) {
	coll := &mocks.MongoCollectionHelper{}
	coll.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(a mock.Arguments) { *capture = append(*capture, a.Get(1), a.Get(2)) }).
		Return(&mongo.UpdateResult{ModifiedCount: 1}, nil)
	db.On("Collection", name).Return(coll)
}

// Only the reported fields go. A slur in a description does not justify wiping
// a community's name and logo as well.
func TestRemoveReportedContent_ClearsOnlyTheReportedFields(t *testing.T) {
	db := &mocks.DatabaseHelper{}
	var calls []interface{}
	stubUpdate(db, "communities", &calls)

	fields, err := removeReportedContent(context.Background(), db, models.ReportTarget{
		Kind: models.TargetCommunity, ID: targetCommunityID, Fields: []string{"description"},
	})
	assert.NoError(t, err)
	assert.Equal(t, []string{"description"}, fields)

	set := calls[1].(bson.M)["$set"].(bson.M)
	assert.Equal(t, bson.M{"community.description": ""}, set)
	assert.NotContains(t, set, "community.name")
	assert.NotContains(t, set, "community.ownerID")
}

// A comment is cleared inside its parent, matched by its own id.
func TestRemoveReportedContent_ClearsOneCommentInPlace(t *testing.T) {
	db := &mocks.DatabaseHelper{}
	var calls []interface{}
	stubUpdate(db, "announcements", &calls)

	_, err := removeReportedContent(context.Background(), db, models.ReportTarget{
		Kind: models.TargetAnnouncementComment, ID: targetCommentID, ParentID: targetAnnID,
		Fields: []string{"content"},
	})
	assert.NoError(t, err)
	assert.Equal(t, bson.M{"comments.$[el].content": ""}, calls[1].(bson.M)["$set"])
}

// The registry is the whole allowlist: a takedown cannot be pointed at an
// owner id, a subscription or anything else the server owns.
func TestRemoveReportedContent_RefusesFieldsOutsideTheRegistry(t *testing.T) {
	db := &mocks.DatabaseHelper{}
	for _, field := range []string{"ownerID", "subscription", "banList", "listingSuspension"} {
		_, err := removeReportedContent(context.Background(), db, models.ReportTarget{
			Kind: models.TargetCommunity, ID: targetCommunityID, Fields: []string{field},
		})
		assert.Error(t, err, field)
	}
	db.AssertNotCalled(t, "Collection", mock.Anything)
}

// Clearing every field of a profile at once is not a single click.
func TestRemoveReportedContent_NeedsFieldsChosen(t *testing.T) {
	db := &mocks.DatabaseHelper{}
	_, err := removeReportedContent(context.Background(), db, models.ReportTarget{
		Kind: models.TargetCommunity, ID: targetCommunityID,
	})
	assert.ErrorContains(t, err, "choose which parts")
	db.AssertNotCalled(t, "Collection", mock.Anything)
}

// Promotions and creator profiles have their own panels, which also handle the
// Discord message and the programme status.
func TestRemoveReportedContent_PointsAtTheRightPanel(t *testing.T) {
	db := &mocks.DatabaseHelper{}
	_, err := removeReportedContent(context.Background(), db, models.ReportTarget{
		Kind: models.TargetRpPromotion, ID: "abc", ParentID: targetCommunityID, Fields: []string{"description"},
	})
	assert.ErrorContains(t, err, "panel")
}
