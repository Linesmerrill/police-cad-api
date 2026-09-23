package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

func closedReport(status, decisionID string) *models.Report {
	r := testReport("Spam", "user")
	r.Status = status
	r.DecisionID = decisionID
	r.ReviewedByName = "Merrill L"
	return r
}

func reopenRequest(t *testing.T, reason string) *http.Request {
	return adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/reopen",
		adminBody(t, map[string]interface{}{"reason": reason}),
		map[string]string{"reportId": testReportID})
}

// An accidental dismissal closed the whole case, so reopening brings back
// every report that decision closed, each with a record of who and why.
func TestReopen_BringsBackEveryReportTheDismissalClosed(t *testing.T) {
	withGatewayKey(t)
	rdb := &mocks.ReportDatabase{}

	clicked := closedReport(models.ReportStatusDismissed, "decision-1")
	sibling := caseReport("507f1f77bcf86cd799439061", "Spam", t0)
	sibling.Status, sibling.DecisionID = models.ReportStatusDismissed, "decision-1"

	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	rdb.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).Return(clicked, nil)
	var siblingFilter bson.M
	rdb.On("Find", mock.Anything, mock.Anything, mock.Anything).
		Run(func(a mock.Arguments) { siblingFilter = a.Get(1).(bson.M) }).
		Return(reportCursor(t, clicked, sibling), nil)

	var updates []bson.M
	var filters []bson.M
	rdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Run(func(a mock.Arguments) {
		filters = append(filters, a.Get(1).(bson.M))
		updates = append(updates, a.Get(2).(bson.M))
	}).Return(nil)

	ra := newReportAdmin(rdb, &mocks.ContentOffenseDatabase{}, &mocks.UserDatabase{}, &mocks.CommunityDatabase{})
	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminReopenReportHandler).ServeHTTP(rr, reopenRequest(t, "dismissed the wrong case"))

	assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Equal(t, bson.M{"decisionId": "decision-1", "status": models.ReportStatusDismissed}, siblingFilter)
	if !assert.Len(t, updates, 2) {
		return
	}
	for i, u := range updates {
		set := u["$set"].(bson.M)
		assert.Equal(t, models.ReportStatusNew, set["status"])
		assert.Equal(t, true, set["active"])

		unset := u["$unset"].(bson.M)
		for _, f := range []string{"reviewedByName", "reviewedById", "reviewedAt", "decisionId"} {
			assert.Contains(t, unset, f)
		}

		event := u["$push"].(bson.M)["history"].(models.ReportEvent)
		assert.Equal(t, models.ReportEventReopened, event.Action)
		assert.Equal(t, models.ReportStatusDismissed, event.PreviousStatus)
		assert.Equal(t, "dismissed the wrong case", event.Reason)
		assert.Equal(t, "Staff", event.By, "attributed by display name, never email")
		assert.NotContains(t, event.By, "@")

		// Guarded on the status it was in, so a report decided again in the
		// meantime is not yanked back.
		assert.Equal(t, models.ReportStatusDismissed, filters[i]["status"])
	}
}

func TestReopen_RequiresAReason(t *testing.T) {
	withGatewayKey(t)
	ra := newReportAdmin(&mocks.ReportDatabase{}, &mocks.ContentOffenseDatabase{}, &mocks.UserDatabase{}, &mocks.CommunityDatabase{})
	for _, reason := range []string{"", "  ", "ok"} {
		rr := httptest.NewRecorder()
		http.HandlerFunc(ra.AdminReopenReportHandler).ServeHTTP(rr, reopenRequest(t, reason))
		assert.Equal(t, http.StatusBadRequest, rr.Code, "reason %q", reason)
	}
}

// Only reports closed with no action can be reopened, and each refusal says
// what to do instead.
func TestReopen_RefusesWhatItCannotSafelyUndo(t *testing.T) {
	withGatewayKey(t)
	tests := []struct {
		status string
		want   string
	}{
		{models.ReportStatusResolved, "reverse the strike"},
		{models.ReportStatusEscalated, "escalated reports cannot be reopened"},
		{models.ReportStatusNew, "already open"},
		{"", "already open"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			rdb := &mocks.ReportDatabase{}
			reportOID, _ := primitive.ObjectIDFromHex(testReportID)
			rdb.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).Return(closedReport(tt.status, "d"), nil)

			ra := newReportAdmin(rdb, &mocks.ContentOffenseDatabase{}, &mocks.UserDatabase{}, &mocks.CommunityDatabase{})
			rr := httptest.NewRecorder()
			http.HandlerFunc(ra.AdminReopenReportHandler).ServeHTTP(rr, reopenRequest(t, "a real reason"))

			assert.Equal(t, http.StatusConflict, rr.Code)
			assert.Contains(t, rr.Body.String(), tt.want)
			rdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

// Reports dismissed before decisions were tracked reopen on their own, and a
// self-harm report closed as read can be reopened too.
func TestReopen_UntrackedWelfareReportReopensAlone(t *testing.T) {
	withGatewayKey(t)
	rdb := &mocks.ReportDatabase{}
	legacy := closedReport(models.ReportStatusWelfare, "")
	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	rdb.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).Return(legacy, nil)
	rdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	ra := newReportAdmin(rdb, &mocks.ContentOffenseDatabase{}, &mocks.UserDatabase{}, &mocks.CommunityDatabase{})
	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminReopenReportHandler).ServeHTTP(rr, reopenRequest(t, "needs a second look"))

	assert.Equal(t, http.StatusOK, rr.Code)
	rdb.AssertNotCalled(t, "Find", mock.Anything, mock.Anything, mock.Anything)
	rdb.AssertNumberOfCalls(t, "UpdateOne", 1)
}

// Every decision now leaves a history entry and a decision id, which is what
// makes a reopen able to find the rest of the case.
func TestDismiss_RecordsHistoryAndASharedDecision(t *testing.T) {
	withGatewayKey(t)
	rdb := &mocks.ReportDatabase{}
	spam := testReport("Spam", "user")
	spam2 := caseReport("507f1f77bcf86cd799439062", "Spam", t0)
	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	rdb.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).Return(spam, nil)
	stubOpenCase(t, rdb, spam, spam2)

	var updates []bson.M
	rdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(a mock.Arguments) { updates = append(updates, a.Get(2).(bson.M)) }).Return(nil)

	ra := newReportAdmin(rdb, &mocks.ContentOffenseDatabase{}, &mocks.UserDatabase{}, &mocks.CommunityDatabase{})
	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/dismiss",
		adminBody(t, map[string]interface{}{"note": "joke report"}), map[string]string{"reportId": testReportID})
	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminDismissReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	if !assert.Len(t, updates, 2) {
		return
	}
	first := updates[0]["$set"].(bson.M)["decisionId"]
	assert.NotEmpty(t, first)
	for _, u := range updates {
		assert.Equal(t, first, u["$set"].(bson.M)["decisionId"], "one decision, one id")
		event := u["$push"].(bson.M)["history"].(models.ReportEvent)
		assert.Equal(t, models.ReportStatusDismissed, event.Action)
		assert.Equal(t, "joke report", event.Reason)
		assert.Equal(t, first, event.DecisionID)
	}
}

// The uphold reason used to land only on the strike, leaving the reports and
// their history blank for the next admin. It is now on both.
func TestUphold_RecordsTheReasonOnTheReportsAndHistory(t *testing.T) {
	withGatewayKey(t)
	rdb, codb, udb := &mocks.ReportDatabase{}, &mocks.ContentOffenseDatabase{}, &mocks.UserDatabase{}

	spam := testReport("Spam", "user")
	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	rdb.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).Return(spam, nil)
	stubOpenCase(t, rdb, spam)
	var update bson.M
	rdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(a mock.Arguments) { update = a.Get(2).(bson.M) }).Return(nil)
	stubTargetUser(udb, "spammer", "")
	codb.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(0), nil)
	codb.On("FindOne", mock.Anything, mock.Anything).Return(nil, assert.AnError)
	codb.On("InsertOne", mock.Anything, mock.Anything).Return(&mocks.InsertOneResultHelper{}, nil)

	ra := newReportAdmin(rdb, codb, udb, &mocks.CommunityDatabase{})
	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/uphold",
		adminBody(t, map[string]interface{}{"reason": "confirmed with the real owner", "sendEmail": false}),
		map[string]string{"reportId": testReportID})
	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminUpholdReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Equal(t, "confirmed with the real owner", update["$set"].(bson.M)["internalNote"])
	assert.Equal(t, "confirmed with the real owner", update["$push"].(bson.M)["history"].(models.ReportEvent).Reason)
}
