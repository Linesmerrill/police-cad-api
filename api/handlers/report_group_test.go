package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// caseReport is another report about the same target as testReport.
func caseReport(hexID, issue string, filed time.Time) *models.Report {
	oid, _ := primitive.ObjectIDFromHex(hexID)
	r := testReport(issue, "user")
	r.ID = oid
	r.CreatedAt = primitive.NewDateTimeFromTime(filed)
	return r
}

var t0 = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

// Four people reporting the same account is one strike, not four rungs up the
// ladder in one sitting, and every one of those reports is closed by it.
func TestUpholdCase_OneStrikeClosesEveryLadderReport(t *testing.T) {
	withGatewayKey(t)
	rdb, codb, udb := &mocks.ReportDatabase{}, &mocks.ContentOffenseDatabase{}, &mocks.UserDatabase{}

	spam := testReport("Spam", "user")
	hate := caseReport("507f1f77bcf86cd799439041", "Hate", t0)
	spam2 := caseReport("507f1f77bcf86cd799439042", "Spam", t0.Add(time.Hour))

	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	rdb.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).Return(spam, nil)
	stubOpenCase(t, rdb, hate, spam, spam2)

	var closed []primitive.ObjectID
	rdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Run(func(a mock.Arguments) {
		closed = append(closed, a.Get(1).(bson.M)["_id"].(primitive.ObjectID))
	}).Return(nil)

	stubTargetUser(udb, "offender", "")
	udb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(&mongo.UpdateResult{}, nil)
	codb.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(0), nil)
	codb.On("FindOne", mock.Anything, mock.Anything).Return(nil, mongo.ErrNoDocuments)

	var offense models.ContentOffense
	codb.On("InsertOne", mock.Anything, mock.Anything).Run(func(a mock.Arguments) {
		offense = a.Get(1).(models.ContentOffense)
	}).Return(&mocks.InsertOneResultHelper{}, nil)

	ra := newReportAdmin(rdb, codb, udb, &mocks.CommunityDatabase{})
	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/uphold",
		adminBody(t, map[string]interface{}{"reason": "confirmed", "sendEmail": false}),
		map[string]string{"reportId": testReportID})
	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminUpholdReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	codb.AssertNumberOfCalls(t, "InsertOne", 1)
	assert.Equal(t, 1, offense.OffenseNumber, "a pile-on is one strike")
	assert.Len(t, offense.ReportIDs, 3)
	// The case is issued under its most severe issue, so this is a hate case
	// even though the report clicked on was spam.
	assert.Equal(t, "Hate", offense.ReportedIssue)
	assert.Equal(t, models.PenaltyActionSuspension, offense.Penalty, "hate skips the warning rung")
	assert.ElementsMatch(t, []primitive.ObjectID{spam.ID, hate.ID, spam2.ID}, closed)
}

// A week's ban for harassment must not overtake a child safety allegation
// against the same account that nobody has escalated yet.
func TestUpholdCase_RefusesWhileTheSameAccountHasAnOpenChildSafetyReport(t *testing.T) {
	withGatewayKey(t)
	rdb, codb, udb := &mocks.ReportDatabase{}, &mocks.ContentOffenseDatabase{}, &mocks.UserDatabase{}

	harassment := testReport("Abuse & Harassment", "user")
	childSafety := caseReport("507f1f77bcf86cd799439043", "Child Safety", t0)

	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	rdb.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).Return(harassment, nil)
	stubOpenCase(t, rdb, childSafety, harassment)

	ra := newReportAdmin(rdb, codb, udb, &mocks.CommunityDatabase{})
	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/uphold",
		adminBody(t, map[string]interface{}{"reason": "confirmed"}),
		map[string]string{"reportId": testReportID})
	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminUpholdReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusConflict, rr.Code)
	assert.Contains(t, rr.Body.String(), "child safety")
	codb.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)
	udb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// Dismissing spam closes the spam in the case and nothing else. The child
// safety and self-harm reports about the same account stay open for a person.
func TestDismissCase_OnlyClosesReportsOnTheSameTrack(t *testing.T) {
	withGatewayKey(t)
	rdb := &mocks.ReportDatabase{}

	spam := testReport("Spam", "user")
	spam2 := caseReport("507f1f77bcf86cd799439044", "Spam", t0)
	childSafety := caseReport("507f1f77bcf86cd799439045", "Child Safety", t0)
	selfHarm := caseReport("507f1f77bcf86cd799439046", "Suicide or Self-Harm", t0)

	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	rdb.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).Return(spam, nil)
	stubOpenCase(t, rdb, childSafety, selfHarm, spam, spam2)

	var closed []primitive.ObjectID
	rdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Run(func(a mock.Arguments) {
		closed = append(closed, a.Get(1).(bson.M)["_id"].(primitive.ObjectID))
	}).Return(nil)

	ra := newReportAdmin(rdb, &mocks.ContentOffenseDatabase{}, &mocks.UserDatabase{}, &mocks.CommunityDatabase{})
	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/dismiss",
		adminBody(t, nil), map[string]string{"reportId": testReportID})
	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminDismissReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.ElementsMatch(t, []primitive.ObjectID{spam.ID, spam2.ID}, closed)
	assert.NotContains(t, closed, childSafety.ID)
	assert.NotContains(t, closed, selfHarm.ID)
}

func TestMostSevereIssue(t *testing.T) {
	reports := []models.Report{
		*caseReport("507f1f77bcf86cd799439047", "Spam", t0),
		*caseReport("507f1f77bcf86cd799439048", "Violent Speech", t0),
		*caseReport("507f1f77bcf86cd799439049", "Hate", t0),
	}
	// Both serious; the first one met wins, so the choice is stable.
	assert.Equal(t, "Violent Speech", mostSevereIssue(reports, "Spam"))
	assert.Equal(t, "Spam", mostSevereIssue(nil, "Spam"))
}

// The escalation package carries every child safety report about the account,
// each verbatim, not just the one that was clicked.
func TestCyberTiplinePackage_IncludesRelatedReportsVerbatim(t *testing.T) {
	first := testReport("Child Safety", "user")
	first.AdditionalDetails = "first account of it"
	pkg := buildCyberTiplinePackage(*first, offenseTarget{UserID: testTargetID, ContactUsername: "suspect"}, "", "reporter1", "Staff", t0)

	second := caseReport("507f1f77bcf86cd79943904a", "Child Safety", t0)
	second.AdditionalDetails = "line one\nline two"
	pkg.addRelatedReports([]models.Report{*second}, func(string) string { return "reporter2" })

	assert.Len(t, pkg.Related, 1)
	assert.Equal(t, "line one\nline two", pkg.Related[0].ReportText)
	assert.Contains(t, pkg.PlainText, "FURTHER REPORTS ABOUT THE SAME ACCOUNT (1)")
	assert.Contains(t, pkg.PlainText, "     line two")
	// Related reports sit above the footer, not after it.
	assert.Less(t, strings.Index(pkg.PlainText, "FURTHER REPORTS"), strings.Index(pkg.PlainText, "PLATFORM ACTIONS TAKEN"))
}

// Decisions are shown to every admin who opens a report. An email address
// there puts a staff member's personal address in front of all of them.
func TestAdminDisplayName_NeverAnEmail(t *testing.T) {
	assert.Equal(t, "Merrill Lines", adminDisplayName(map[string]interface{}{"name": "Merrill Lines", "email": "x@y.com"}))
	assert.Equal(t, "Staff", adminDisplayName(map[string]interface{}{"email": "x@y.com"}))
	// A "name" that is really an email is not a display name.
	assert.Equal(t, "Staff", adminDisplayName(map[string]interface{}{"name": "x@y.com"}))
	assert.Equal(t, "Staff", adminDisplayName(nil))
	assert.Equal(t, "abc123", adminID(map[string]interface{}{"id": " abc123 "}))
}
