package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

const (
	createTargetID   = "507f1f77bcf86cd799439071"
	createReporterID = "507f1f77bcf86cd799439072"
	createTokenUser  = "507f1f77bcf86cd799439073"
)

func createBody(t *testing.T, overrides map[string]interface{}) string {
	t.Helper()
	body := map[string]interface{}{
		"itemId":            createTargetID,
		"itemType":          "community",
		"reportType":        "COMMUNITY_REPORT",
		"reportedIssue":     "Hate",
		"additionalDetails": "slurs in the server description",
		"reportedById":      createReporterID,
	}
	for k, v := range overrides {
		if v == nil {
			delete(body, k)
			continue
		}
		body[k] = v
	}
	raw, _ := json.Marshal(body)
	return string(raw)
}

func postReport(t *testing.T, rdb *mocks.ReportDatabase, body, tokenUser string) *httptest.ResponseRecorder {
	t.Helper()
	req, _ := http.NewRequest("POST", "/api/v1/report", strings.NewReader(body))
	if tokenUser != "" {
		req = req.WithContext(api.WithAuthenticatedUserID(req.Context(), tokenUser))
	}
	rr := httptest.NewRecorder()
	http.HandlerFunc(Report{RDB: rdb}.CreateReportHandler).ServeHTTP(rr, req)
	return rr
}

func TestCreateReport_StoresACommunityReportInTheQueue(t *testing.T) {
	rdb := &mocks.ReportDatabase{}
	rdb.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(0), nil)
	var stored models.Report
	rdb.On("InsertOne", mock.Anything, mock.Anything).
		Run(func(a mock.Arguments) { stored = a.Get(1).(models.Report) }).
		Return(&mocks.InsertOneResultHelper{}, nil)

	rr := postReport(t, rdb, createBody(t, nil), "")

	assert.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	assert.Equal(t, models.ReportTypeCommunityReport, stored.ReportType)
	assert.Equal(t, models.ReportStatusNew, stored.Status)
	assert.Equal(t, models.ReportTierSerious, stored.Tier)
	if assert.NotNil(t, stored.SeverityRank) {
		assert.Equal(t, models.SeverityRankSerious, *stored.SeverityRank)
	}
	assert.Equal(t, createReporterID, stored.ReportedByID, "no token, so the body's reporter stands")
}

// reportedById in the body was trusted, so anyone could file a report in
// someone else's name. A token wins over whatever the body says.
func TestCreateReport_TheTokenUserIsTheReporter(t *testing.T) {
	rdb := &mocks.ReportDatabase{}
	rdb.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(0), nil)
	var stored models.Report
	rdb.On("InsertOne", mock.Anything, mock.Anything).
		Run(func(a mock.Arguments) { stored = a.Get(1).(models.Report) }).
		Return(&mocks.InsertOneResultHelper{}, nil)

	rr := postReport(t, rdb, createBody(t, map[string]interface{}{"itemType": "user", "reportType": "USER_REPORT"}), createTokenUser)

	assert.Equal(t, http.StatusCreated, rr.Code)
	assert.Equal(t, createTokenUser, stored.ReportedByID)
}

func TestCreateReport_RejectsAnIncompleteReport(t *testing.T) {
	tests := []struct {
		name     string
		override map[string]interface{}
	}{
		{"no target", map[string]interface{}{"itemId": nil}},
		{"target is not an id", map[string]interface{}{"itemId": "abc"}},
		{"unknown item type", map[string]interface{}{"itemType": "post"}},
		{"unknown report type", map[string]interface{}{"reportType": "WHATEVER"}},
		{"no issue", map[string]interface{}{"reportedIssue": "  "}},
		{"no reporter", map[string]interface{}{"reportedById": nil}},
		{"note too long", map[string]interface{}{"additionalDetails": strings.Repeat("a", MaxReportDetailsLength+1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rdb := &mocks.ReportDatabase{}
			rr := postReport(t, rdb, createBody(t, tt.override), "")
			assert.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
			rdb.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)
		})
	}
}

// The mobile app's existing report types must keep working after validation
// was added. An older build in someone's pocket sends exactly these.
func TestCreateReport_AcceptsWhatTheMobileAppSends(t *testing.T) {
	for _, c := range []struct{ itemType, reportType string }{
		{"user", "USER_REPORT"},
		{"community", "AD_REPORT"},
	} {
		rdb := &mocks.ReportDatabase{}
		rdb.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(0), nil)
		rdb.On("InsertOne", mock.Anything, mock.Anything).Return(&mocks.InsertOneResultHelper{}, nil)
		rr := postReport(t, rdb, createBody(t, map[string]interface{}{
			"itemType": c.itemType, "reportType": c.reportType,
			"additionalDetails": strings.Repeat("a", MaxReportDetailsLength),
		}), createTokenUser)
		assert.Equal(t, http.StatusCreated, rr.Code, "%s/%s: %s", c.itemType, c.reportType, rr.Body.String())
	}
}

// One open report per person per target. A second one says it was received,
// so neither client shows an error, but nothing new is stored.
func TestCreateReport_ASecondOpenReportIsNotStoredAgain(t *testing.T) {
	rdb := &mocks.ReportDatabase{}
	rdb.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(1), nil)

	rr := postReport(t, rdb, createBody(t, nil), "")

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"message"`, "the mobile app treats any message as success")
	assert.Contains(t, rr.Body.String(), `"duplicate": true`)
	rdb.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)
}
