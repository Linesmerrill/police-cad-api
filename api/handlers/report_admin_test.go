package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

const (
	testGatewayKey = "test-gateway-secret"
	testReportID   = "507f1f77bcf86cd799439021"
	testTargetID   = "507f1f77bcf86cd799439022"
	testReporterID = "507f1f77bcf86cd799439023"
)

func withGatewayKey(t *testing.T) {
	t.Helper()
	t.Setenv(apiGatewayKeyEnv, testGatewayKey)
}

func adminBody(t *testing.T, extra map[string]interface{}) string {
	t.Helper()
	body := map[string]interface{}{
		"currentUser": map[string]interface{}{"roles": []string{"admin"}, "email": "staff@example.com"},
	}
	for k, v := range extra {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func adminRequest(t *testing.T, method, path, body string, vars map[string]string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(apiGatewayHeader, testGatewayKey)
	return mux.SetURLVars(req, vars)
}

func testReport(issue, itemType string) *models.Report {
	oid, _ := primitive.ObjectIDFromHex(testReportID)
	return &models.Report{
		ID:                oid,
		ItemID:            testTargetID,
		ItemType:          itemType,
		ReportType:        models.ReportTypeUserReport,
		ReportedIssue:     issue,
		AdditionalDetails: "he was doing the thing",
		ReportedByID:      testReporterID,
		Active:            true,
		Status:            models.ReportStatusNew,
		CreatedAt:         primitive.NewDateTimeFromTime(time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)),
	}
}

// stubTargetUser wires the reported account lookup.
func stubTargetUser(mockUserDB *mocks.UserDatabase, username, email string) {
	oid, _ := primitive.ObjectIDFromHex(testTargetID)
	result := &mocks.SingleResultHelper{}
	result.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		userPtr := args.Get(0).(*models.User)
		*userPtr = models.User{
			ID:      testTargetID,
			Details: models.UserDetails{Username: username, Email: email},
		}
	}).Return(nil)
	mockUserDB.On("FindOne", mock.Anything, bson.M{"_id": oid}).Return(result)
}

func newReportAdmin(rdb *mocks.ReportDatabase, codb *mocks.ContentOffenseDatabase, udb *mocks.UserDatabase, cdb *mocks.CommunityDatabase) ReportAdmin {
	return ReportAdmin{RDB: rdb, CODB: codb, UDB: udb, CDB: cdb}
}

// The whole point of routing these through the website's server is that page
// JavaScript cannot reach them. Without the secret the request is refused even
// with a valid admin role attached.
func TestAdminReports_RefusesWithoutTheGatewaySecret(t *testing.T) {
	withGatewayKey(t)

	ra := newReportAdmin(&mocks.ReportDatabase{}, &mocks.ContentOffenseDatabase{}, &mocks.UserDatabase{}, &mocks.CommunityDatabase{})

	req, _ := http.NewRequest("POST", "/api/v1/admin/reports/"+testReportID+"/uphold",
		strings.NewReader(adminBody(t, map[string]interface{}{"reason": "confirmed"})))
	req.Header.Set("Content-Type", "application/json")
	req = mux.SetURLVars(req, map[string]string{"reportId": testReportID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminUpholdReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestAdminReports_RefusesANonAdminRole(t *testing.T) {
	withGatewayKey(t)

	ra := newReportAdmin(&mocks.ReportDatabase{}, &mocks.ContentOffenseDatabase{}, &mocks.UserDatabase{}, &mocks.CommunityDatabase{})

	body, _ := json.Marshal(map[string]interface{}{
		"currentUser": map[string]interface{}{"roles": []string{"staff"}},
		"reason":      "confirmed",
	})
	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/uphold", string(body),
		map[string]string{"reportId": testReportID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminUpholdReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

// A Child Safety report must never take the automated path. It has to be
// escalated by a person, and upholding it would both penalise on a ladder it
// does not belong to and email the account that it is under investigation.
func TestAdminUphold_RefusesAChildSafetyReport(t *testing.T) {
	withGatewayKey(t)

	mockReportDB := &mocks.ReportDatabase{}
	mockOffenseDB := &mocks.ContentOffenseDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	mockReportDB.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).
		Return(testReport("Child Safety", "user"), nil)
	stubTargetUser(mockUserDB, "suspect", "suspect@example.com")
	mockOffenseDB.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(0), nil)
	mockOffenseDB.On("FindOne", mock.Anything, mock.Anything).Return(nil, mongo.ErrNoDocuments)

	ra := newReportAdmin(mockReportDB, mockOffenseDB, mockUserDB, &mocks.CommunityDatabase{})

	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/uphold",
		adminBody(t, map[string]interface{}{"reason": "confirmed by staff"}),
		map[string]string{"reportId": testReportID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminUpholdReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "does not take an automated action")
	// Nothing was recorded and nothing was applied.
	mockOffenseDB.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)
	mockUserDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestAdminUphold_RefusesASelfHarmReport(t *testing.T) {
	withGatewayKey(t)

	mockReportDB := &mocks.ReportDatabase{}
	mockOffenseDB := &mocks.ContentOffenseDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	mockReportDB.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).
		Return(testReport("Suicide or Self-Harm", "user"), nil)
	stubTargetUser(mockUserDB, "kid", "kid@example.com")
	mockOffenseDB.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(0), nil)
	mockOffenseDB.On("FindOne", mock.Anything, mock.Anything).Return(nil, mongo.ErrNoDocuments)

	ra := newReportAdmin(mockReportDB, mockOffenseDB, mockUserDB, &mocks.CommunityDatabase{})

	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/uphold",
		adminBody(t, map[string]interface{}{"reason": "looks genuine"}),
		map[string]string{"reportId": testReportID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminUpholdReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	mockOffenseDB.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)
	mockUserDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// A first spam report is a warning: a record, not a restriction. Nothing may be
// written to user.suspension.
func TestAdminUphold_FirstMinorOffenseWarnsWithoutSuspending(t *testing.T) {
	withGatewayKey(t)

	mockReportDB := &mocks.ReportDatabase{}
	mockOffenseDB := &mocks.ContentOffenseDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	mockReportDB.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).Return(testReport("Spam", "user"), nil)
	mockReportDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	stubTargetUser(mockUserDB, "spammer", "")
	mockOffenseDB.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(0), nil)
	mockOffenseDB.On("FindOne", mock.Anything, mock.Anything).Return(nil, mongo.ErrNoDocuments)

	var recorded models.ContentOffense
	mockOffenseDB.On("InsertOne", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		recorded = args.Get(1).(models.ContentOffense)
	}).Return(&mocks.InsertOneResultHelper{}, nil)

	ra := newReportAdmin(mockReportDB, mockOffenseDB, mockUserDB, &mocks.CommunityDatabase{})

	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/uphold",
		adminBody(t, map[string]interface{}{"reason": "advertising another server"}),
		map[string]string{"reportId": testReportID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminUpholdReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, models.PenaltyActionWarning, recorded.Penalty)
	assert.Equal(t, 1, recorded.OffenseNumber)
	assert.Nil(t, recorded.ExpiresAt, "a warning has no expiry")
	mockUserDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// Harassment skips the warning rung, so a first report suspends for a week.
func TestAdminUphold_FirstSeriousOffenseSuspendsForAWeek(t *testing.T) {
	withGatewayKey(t)

	mockReportDB := &mocks.ReportDatabase{}
	mockOffenseDB := &mocks.ContentOffenseDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	targetOID, _ := primitive.ObjectIDFromHex(testTargetID)
	mockReportDB.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).
		Return(testReport("Abuse & Harassment", "user"), nil)
	mockReportDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	stubTargetUser(mockUserDB, "bully", "")
	mockOffenseDB.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(0), nil)
	mockOffenseDB.On("FindOne", mock.Anything, mock.Anything).Return(nil, mongo.ErrNoDocuments)
	mockOffenseDB.On("InsertOne", mock.Anything, mock.Anything).Return(&mocks.InsertOneResultHelper{}, nil)

	var applied bson.M
	mockUserDB.On("UpdateOne", mock.Anything, bson.M{"_id": targetOID}, mock.Anything).
		Run(func(args mock.Arguments) {
			applied = args.Get(2).(bson.M)
		}).Return(&mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil)

	ra := newReportAdmin(mockReportDB, mockOffenseDB, mockUserDB, &mocks.CommunityDatabase{})

	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/uphold",
		adminBody(t, map[string]interface{}{"reason": "sustained harassment in chat", "sendEmail": false}),
		map[string]string{"reportId": testReportID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminUpholdReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	set, ok := applied["$set"].(bson.M)
	if !assert.True(t, ok, "expected a $set on the user") {
		return
	}
	// The suspension goes in its own namespace, never in the deactivation
	// fields that self-deactivation also writes.
	suspension, ok := set["user.suspension"].(models.Suspension)
	if !assert.True(t, ok, "expected user.suspension to be written, got %#v", set) {
		return
	}
	assert.NotNil(t, suspension.Until)
	assert.Nil(t, set["user.isDeactivated"])
	assert.Nil(t, set["user.restoreUntil"])

	days := suspension.Until.Time().Sub(time.Now()).Hours() / 24
	assert.InDelta(t, 7, days, 0.1)
}

// Upholding a second report while the first penalty is still running would
// stack an offense and jump the ladder for behaviour already actioned.
func TestAdminUphold_RefusesWhileAPenaltyIsRunning(t *testing.T) {
	withGatewayKey(t)

	mockReportDB := &mocks.ReportDatabase{}
	mockOffenseDB := &mocks.ContentOffenseDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	mockReportDB.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).Return(testReport("Hate", "user"), nil)
	stubTargetUser(mockUserDB, "bully", "")
	mockOffenseDB.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(1), nil)

	running := primitive.NewDateTimeFromTime(time.Now().Add(72 * time.Hour))
	mockOffenseDB.On("FindOne", mock.Anything, mock.Anything).Return(&models.ContentOffense{
		Status:    models.ContentOffenseStatusActive,
		Penalty:   models.PenaltyActionSuspension,
		ExpiresAt: &running,
	}, nil)

	ra := newReportAdmin(mockReportDB, mockOffenseDB, mockUserDB, &mocks.CommunityDatabase{})

	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/uphold",
		adminBody(t, map[string]interface{}{"reason": "second report"}),
		map[string]string{"reportId": testReportID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminUpholdReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusConflict, rr.Code)
	mockOffenseDB.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)
}

func TestAdminUphold_RequiresAReason(t *testing.T) {
	withGatewayKey(t)

	ra := newReportAdmin(&mocks.ReportDatabase{}, &mocks.ContentOffenseDatabase{}, &mocks.UserDatabase{}, &mocks.CommunityDatabase{})

	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/uphold",
		adminBody(t, map[string]interface{}{"reason": " "}),
		map[string]string{"reportId": testReportID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminUpholdReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// Dismissing a self-harm report closes it as welfare: a person read it, and no
// penalty was ever appropriate.
func TestAdminDismiss_SelfHarmClosesAsWelfare(t *testing.T) {
	withGatewayKey(t)

	mockReportDB := &mocks.ReportDatabase{}
	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	mockReportDB.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).
		Return(testReport("Suicide or Self-Harm", "user"), nil)

	var update bson.M
	mockReportDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			update = args.Get(2).(bson.M)
		}).Return(nil)

	ra := newReportAdmin(mockReportDB, &mocks.ContentOffenseDatabase{}, &mocks.UserDatabase{}, &mocks.CommunityDatabase{})

	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/dismiss",
		adminBody(t, map[string]interface{}{"note": "reached out"}),
		map[string]string{"reportId": testReportID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminDismissReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	set := update["$set"].(bson.M)
	assert.Equal(t, models.ReportStatusWelfare, set["status"])
	assert.Nil(t, set["offenseId"], "no offense is recorded against a welfare report")
}

// Escalation holds the data, suspends the account and never emails anyone.
func TestAdminEscalate_HoldsSuspendsAndBuildsThePackage(t *testing.T) {
	withGatewayKey(t)

	mockReportDB := &mocks.ReportDatabase{}
	mockUserDB := &mocks.UserDatabase{}

	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	targetOID, _ := primitive.ObjectIDFromHex(testTargetID)
	report := testReport("Child Safety", "user")
	report.AdditionalDetails = "Caught him asking underage kids for photos in a party"
	mockReportDB.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).Return(report, nil)
	mockReportDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	stubTargetUser(mockUserDB, "suspect", "suspect@example.com")

	reporterOID, _ := primitive.ObjectIDFromHex(testReporterID)
	reporterResult := &mocks.SingleResultHelper{}
	reporterResult.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		userPtr := args.Get(0).(*models.User)
		*userPtr = models.User{ID: testReporterID, Details: models.UserDetails{Username: "reporter"}}
	}).Return(nil)
	mockUserDB.On("FindOne", mock.Anything, bson.M{"_id": reporterOID}).Return(reporterResult)

	writes := []bson.M{}
	mockUserDB.On("UpdateOne", mock.Anything, bson.M{"_id": targetOID}, mock.Anything).
		Run(func(args mock.Arguments) {
			writes = append(writes, args.Get(2).(bson.M))
		}).Return(&mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil)

	ra := newReportAdmin(mockReportDB, &mocks.ContentOffenseDatabase{}, mockUserDB, &mocks.CommunityDatabase{})

	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/escalate",
		adminBody(t, nil), map[string]string{"reportId": testReportID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminEscalateReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var body struct {
		Package   cyberTiplinePackage `json:"package"`
		LegalHold bool                `json:"legalHold"`
		SubmitAt  string              `json:"submitAt"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not decodable: %v", err)
	}

	assert.True(t, body.LegalHold)
	assert.Contains(t, body.SubmitAt, "cybertip.org")
	// The report text is evidence and is reproduced verbatim, never trimmed or
	// paraphrased.
	assert.Equal(t, report.AdditionalDetails, body.Package.ReportText)
	assert.Contains(t, body.Package.PlainText, report.AdditionalDetails)
	assert.Equal(t, "suspect", body.Package.SuspectUsername)
	assert.Equal(t, "reporter", body.Package.ReporterUsername)

	// Both the hold and the suspension were written.
	var sawHold, sawSuspension bool
	for _, w := range writes {
		set, ok := w["$set"].(bson.M)
		if !ok {
			continue
		}
		if hold, ok := set["user.legalHold"].(models.LegalHold); ok {
			sawHold = true
			assert.NotNil(t, hold.ExpiresAt, "a hold must carry its one-year expiry")
		}
		if suspension, ok := set["user.suspension"].(models.Suspension); ok {
			sawSuspension = true
			assert.Nil(t, suspension.Until, "an escalation does not lift itself")
		}
	}
	assert.True(t, sawHold, "the account was not placed under a legal hold")
	assert.True(t, sawSuspension, "the account was not suspended")
}

// A report already decided cannot be decided again.
func TestAdminUphold_RefusesAClosedReport(t *testing.T) {
	withGatewayKey(t)

	mockReportDB := &mocks.ReportDatabase{}
	reportOID, _ := primitive.ObjectIDFromHex(testReportID)
	closed := testReport("Spam", "user")
	closed.Status = models.ReportStatusResolved
	mockReportDB.On("FindOne", mock.Anything, bson.M{"_id": reportOID}).Return(closed, nil)

	ra := newReportAdmin(mockReportDB, &mocks.ContentOffenseDatabase{}, &mocks.UserDatabase{}, &mocks.CommunityDatabase{})

	req := adminRequest(t, "POST", "/api/v1/admin/reports/"+testReportID+"/uphold",
		adminBody(t, map[string]interface{}{"reason": "again"}),
		map[string]string{"reportId": testReportID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminUpholdReportHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusConflict, rr.Code)
}

// The queue's "new" filter has to include the forty reports written before the
// status field existed, or the backlog is invisible.
func TestReportQueueFilter_NewIncludesReportsWithNoStatus(t *testing.T) {
	req, _ := http.NewRequest("GET", "/api/v1/admin/reports?status=new", nil)
	filter := reportQueueFilter(req)

	or, ok := filter["$or"].([]bson.M)
	if !ok {
		t.Fatalf("expected an $or, got %#v", filter)
	}
	var sawNull bool
	for _, branch := range or {
		if cond, ok := branch["status"].(bson.M); ok {
			if v, has := cond["$eq"]; has && v == nil {
				sawNull = true
			}
		}
	}
	assert.True(t, sawNull, "a report with no status is new and must appear in the new filter")
}

func TestReportPaging_ClampsTheLimit(t *testing.T) {
	req, _ := http.NewRequest("GET", "/api/v1/admin/reports?page=-3&limit=9999", nil)
	page, limit := reportPaging(req)
	assert.Equal(t, 0, page)
	assert.Equal(t, maxReportsLimit, limit)

	req, _ = http.NewRequest("GET", "/api/v1/admin/reports", nil)
	_, limit = reportPaging(req)
	assert.Equal(t, defaultReportsLimit, limit)
}
