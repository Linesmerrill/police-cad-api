package handlers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

const (
	searchReporterID  = "507f1f77bcf86cd799439031"
	searchTargetID    = "507f1f77bcf86cd799439032"
	searchCommunityID = "507f1f77bcf86cd799439033"
)

func reportCursor(t *testing.T, docs ...interface{}) databases.MongoCursor {
	t.Helper()
	c, err := databases.NewMongoCursorFromDocuments(docs)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// referencedIDsFixture is what the $group over the reports collection returns:
// one reporter, one reported user, one reported community.
func referencedIDsFixture(t *testing.T) databases.MongoCursor {
	return reportCursor(t, bson.M{
		"_id":              nil,
		"reporters":        bson.A{searchReporterID},
		"userTargets":      bson.A{searchTargetID, nil},
		"communityTargets": bson.A{searchCommunityID, nil},
	})
}

func orBranches(t *testing.T, clause bson.M) []bson.M {
	t.Helper()
	or, ok := clause["$or"].([]bson.M)
	if !ok {
		t.Fatalf("expected an $or clause, got %#v", clause)
	}
	return or
}

func TestReportSearch_IgnoresASearchTooShortToMeanAnything(t *testing.T) {
	ra := ReportAdmin{RDB: &mocks.ReportDatabase{}}
	for _, q := range []string{"", " ", "a", " b "} {
		clause, err := ra.reportSearchClause(context.Background(), q)
		assert.NoError(t, err)
		assert.Nil(t, clause, "q=%q should not filter", q)
	}
}

// The case that prompted this: someone contacts us about a report they filed,
// and we find it by their email.
func TestReportSearch_FindsTheReporterByEmail(t *testing.T) {
	rdb := &mocks.ReportDatabase{}
	udb := &mocks.UserDatabase{}
	cdb := &mocks.CommunityDatabase{}

	rdb.On("Aggregate", mock.Anything, mock.Anything).Return(referencedIDsFixture(t), nil)

	reporterOID, _ := primitive.ObjectIDFromHex(searchReporterID)
	var userFilter bson.M
	udb.On("Find", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { userFilter = args.Get(1).(bson.M) }).
		Return(reportCursor(t, bson.M{"_id": reporterOID}), nil)
	cdb.On("FindOneIncludingPending", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	ra := ReportAdmin{RDB: rdb, UDB: udb, CDB: cdb}
	clause, err := ra.reportSearchClause(context.Background(), "Kid@Example.com")
	if !assert.NoError(t, err) {
		return
	}

	// Scoped to people named in reports, never the whole users collection.
	idIn, ok := userFilter["_id"].(bson.M)
	if assert.True(t, ok, "user lookup must be restricted by _id") {
		assert.Len(t, idIn["$in"], 2, "the reporter and the reported user, nobody else")
	}

	var sawReporter bool
	for _, b := range orBranches(t, clause) {
		if in, ok := b["reportedById"].(bson.M); ok {
			assert.Equal(t, []string{searchReporterID}, in["$in"])
			sawReporter = true
		}
	}
	assert.True(t, sawReporter, "the reporter's reports should match")
}

// A search that finds nobody must show nothing. Returning no clause would show
// the whole queue and read as "your search matched everything".
func TestReportSearch_NoMatchShowsNothingRatherThanEverything(t *testing.T) {
	rdb := &mocks.ReportDatabase{}
	udb := &mocks.UserDatabase{}
	cdb := &mocks.CommunityDatabase{}
	rdb.On("Aggregate", mock.Anything, mock.Anything).Return(referencedIDsFixture(t), nil)
	udb.On("Find", mock.Anything, mock.Anything, mock.Anything).Return(reportCursor(t), nil)
	cdb.On("FindOneIncludingPending", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	ra := ReportAdmin{RDB: rdb, UDB: udb, CDB: cdb}
	clause, err := ra.reportSearchClause(context.Background(), "nobody-by-this-name")
	assert.NoError(t, err)
	if assert.NotNil(t, clause, "a failed search must still filter") {
		assert.Equal(t, bson.M{"_id": bson.M{"$in": []primitive.ObjectID{}}}, clause)
	}
}

func TestReportSearch_FindsACommunityByName(t *testing.T) {
	rdb := &mocks.ReportDatabase{}
	udb := &mocks.UserDatabase{}
	cdb := &mocks.CommunityDatabase{}
	rdb.On("Aggregate", mock.Anything, mock.Anything).Return(referencedIDsFixture(t), nil)
	udb.On("Find", mock.Anything, mock.Anything, mock.Anything).Return(reportCursor(t), nil)

	communityOID, _ := primitive.ObjectIDFromHex(searchCommunityID)
	cdb.On("FindOneIncludingPending", mock.Anything, mock.MatchedBy(func(f bson.M) bool {
		return f["_id"] == communityOID
	})).Return(&models.Community{ID: communityOID}, nil)

	ra := ReportAdmin{RDB: rdb, UDB: udb, CDB: cdb}
	clause, err := ra.reportSearchClause(context.Background(), "vice city")
	assert.NoError(t, err)

	var saw bool
	for _, b := range orBranches(t, clause) {
		if b["itemType"] == "community" {
			assert.Equal(t, bson.M{"$in": []string{searchCommunityID}}, b["itemId"])
			saw = true
		}
	}
	assert.True(t, saw, "reports about the community should match")
}

// Pasting an id from a support ticket should find the report, or anything it
// names, including a community that has since been deleted and so cannot be
// found by name.
func TestReportSearch_MatchesAPastedID(t *testing.T) {
	rdb := &mocks.ReportDatabase{}
	udb := &mocks.UserDatabase{}
	cdb := &mocks.CommunityDatabase{}
	rdb.On("Aggregate", mock.Anything, mock.Anything).Return(referencedIDsFixture(t), nil)
	udb.On("Find", mock.Anything, mock.Anything, mock.Anything).Return(reportCursor(t), nil)
	cdb.On("FindOneIncludingPending", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	ra := ReportAdmin{RDB: rdb, UDB: udb, CDB: cdb}
	pasted := "681D27B24532284752F5F926"
	clause, err := ra.reportSearchClause(context.Background(), pasted)
	assert.NoError(t, err)

	branches := orBranches(t, clause)
	oid, _ := primitive.ObjectIDFromHex("681d27b24532284752f5f926")
	assert.Contains(t, branches, bson.M{"_id": oid})
	assert.Contains(t, branches, bson.M{"itemId": "681d27b24532284752f5f926"})
	assert.Contains(t, branches, bson.M{"reportedById": "681d27b24532284752f5f926"})
}

// A search string is user input going into a regex. Metacharacters must be
// matched literally, not interpreted.
func TestReportSearch_EscapesRegexMetacharacters(t *testing.T) {
	rdb := &mocks.ReportDatabase{}
	udb := &mocks.UserDatabase{}
	cdb := &mocks.CommunityDatabase{}
	rdb.On("Aggregate", mock.Anything, mock.Anything).Return(referencedIDsFixture(t), nil)

	var userFilter bson.M
	udb.On("Find", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { userFilter = args.Get(1).(bson.M) }).
		Return(reportCursor(t), nil)
	cdb.On("FindOneIncludingPending", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	ra := ReportAdmin{RDB: rdb, UDB: udb, CDB: cdb}
	_, err := ra.reportSearchClause(context.Background(), "a.*(b")
	assert.NoError(t, err)

	or := userFilter["$or"].([]bson.M)
	regex := or[0]["user.username"].(bson.M)["$regex"]
	assert.Equal(t, `a\.\*\(b`, regex)
}
