package handlers_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// The create handler must recompute the fine + jail-time totals server-side from
// the submitted charge list, so stored values never depend on the client.
func TestCreateArrestReport_ComputesTotals(t *testing.T) {
	mockDB := &mocks.ArrestReportDatabase{}
	var stored models.ArrestReport
	mockDB.On("InsertOne", mock.Anything, mock.AnythingOfType("models.ArrestReport")).
		Run(func(args mock.Arguments) {
			stored = args.Get(1).(models.ArrestReport)
		}).
		Return(&mocks.InsertOneResultHelper{}, nil)

	h := handlers.ArrestReport{DB: mockDB}
	body := `{"arrestReport":{"chargesList":[
		{"name":"Speeding","amount":100,"jailTime":"30 seconds"},
		{"name":"Reckless Driving","amount":250,"jailTime":"2 minutes"}
	]}}`
	rr := httptest.NewRecorder()
	h.CreateArrestReportHandler(rr, httptest.NewRequest("POST", "/", bytes.NewBufferString(body)))

	assert.Equal(t, http.StatusCreated, rr.Code)
	assert.Equal(t, 350.0, stored.Details.TotalFine)
	assert.Equal(t, int64(150), stored.Details.TotalJailTimeSeconds)
	assert.Equal(t, "2 minutes 30 seconds", stored.Details.TotalJailTimeLabel)
	// Unspecified mode normalises to consecutive.
	assert.Equal(t, "consecutive", stored.Details.SentenceMode)
}

func TestCreateArrestReport_ConcurrentAndLife(t *testing.T) {
	mockDB := &mocks.ArrestReportDatabase{}
	var stored models.ArrestReport
	mockDB.On("InsertOne", mock.Anything, mock.AnythingOfType("models.ArrestReport")).
		Run(func(args mock.Arguments) { stored = args.Get(1).(models.ArrestReport) }).
		Return(&mocks.InsertOneResultHelper{}, nil)

	h := handlers.ArrestReport{DB: mockDB}
	// concurrent -> single longest charge; a Life charge overrides the number.
	body := `{"arrestReport":{"sentenceMode":"concurrent","chargesList":[
		{"name":"A","amount":50,"jailTime":"1 minute"},
		{"name":"B","amount":0,"jailTime":"Life"}
	]}}`
	rr := httptest.NewRecorder()
	h.CreateArrestReportHandler(rr, httptest.NewRequest("POST", "/", bytes.NewBufferString(body)))

	assert.Equal(t, http.StatusCreated, rr.Code)
	assert.Equal(t, 50.0, stored.Details.TotalFine)
	assert.Equal(t, models.LifeSentinelSeconds, stored.Details.TotalJailTimeSeconds)
	assert.Equal(t, models.LifeLabel, stored.Details.TotalJailTimeLabel)
	assert.Equal(t, "concurrent", stored.Details.SentenceMode)
}
