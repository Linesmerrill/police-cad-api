package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// Regression cover for the "civilian has 16 arrests but only 10 are visible"
// report: the arrestee endpoint is paginated, so a client must be able to walk
// past page 0, and every page has to come back in a stable order or a
// multi-page walk duplicates and drops rows.

func newArresteeRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	return mux.SetURLVars(req, map[string]string{"arrestee_id": "civ1"})
}

func TestGetArrestReportsByArresteeIDHandler_PagesPastTheFirst(t *testing.T) {
	mockDB := &mocks.ArrestReportDatabase{}
	h := handlers.ArrestReport{DB: mockDB}

	var gotLimit, gotSkip int64
	mockDB.On("Find", mock.Anything, mock.Anything, mock.MatchedBy(func(opts *options.FindOptions) bool {
		if opts.Limit != nil {
			gotLimit = *opts.Limit
		}
		if opts.Skip != nil {
			gotSkip = *opts.Skip
		}
		return true
	})).Return([]models.ArrestReport{}, nil)
	mockDB.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(16), nil)

	h.GetArrestReportsByArresteeIDHandler(
		httptest.NewRecorder(),
		newArresteeRequest(t, "/api/v1/arrest-report/arrestee/civ1?limit=10&page=1"),
	)

	assert.Equal(t, int64(10), gotLimit)
	assert.Equal(t, int64(10), gotSkip, "page is 0-based; page=1 must skip the first page")
}

func TestGetArrestReportsByArresteeIDHandler_DefaultsToTenPerPage(t *testing.T) {
	mockDB := &mocks.ArrestReportDatabase{}
	h := handlers.ArrestReport{DB: mockDB}

	var gotLimit, gotSkip int64
	mockDB.On("Find", mock.Anything, mock.Anything, mock.MatchedBy(func(opts *options.FindOptions) bool {
		if opts.Limit != nil {
			gotLimit = *opts.Limit
		}
		if opts.Skip != nil {
			gotSkip = *opts.Skip
		}
		return true
	})).Return([]models.ArrestReport{}, nil)
	mockDB.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(16), nil)

	h.GetArrestReportsByArresteeIDHandler(
		httptest.NewRecorder(),
		newArresteeRequest(t, "/api/v1/arrest-report/arrestee/civ1"),
	)

	// The default is deliberately small; callers that render a full record list
	// are expected to page, not to rely on the default covering everything.
	assert.Equal(t, int64(10), gotLimit)
	assert.Equal(t, int64(0), gotSkip)
}

// Without an explicit sort, skip/limit runs over Mongo's natural order and a
// client walking multiple pages can see the same report twice or miss one.
func TestGetArrestReportsByArresteeIDHandler_SortsForStablePaging(t *testing.T) {
	mockDB := &mocks.ArrestReportDatabase{}
	h := handlers.ArrestReport{DB: mockDB}

	var gotSort interface{}
	mockDB.On("Find", mock.Anything, mock.Anything, mock.MatchedBy(func(opts *options.FindOptions) bool {
		gotSort = opts.Sort
		return true
	})).Return([]models.ArrestReport{}, nil)
	mockDB.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(16), nil)

	h.GetArrestReportsByArresteeIDHandler(
		httptest.NewRecorder(),
		newArresteeRequest(t, "/api/v1/arrest-report/arrestee/civ1?limit=10&page=0"),
	)

	assert.Equal(t, bson.M{"_id": -1}, gotSort, "paged results must be deterministically ordered")
}

// The exact shape behind the bug report: totalCount says 16 while data carries
// only the current page, so a client that ignores pagination shows 16 and
// renders 10.
func TestGetArrestReportsByArresteeIDHandler_TotalCountSpansAllPages(t *testing.T) {
	mockDB := &mocks.ArrestReportDatabase{}
	h := handlers.ArrestReport{DB: mockDB}

	lastPage := make([]models.ArrestReport, 6)
	mockDB.On("Find", mock.Anything, mock.Anything, mock.Anything).Return(lastPage, nil)
	mockDB.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(16), nil)

	w := httptest.NewRecorder()
	h.GetArrestReportsByArresteeIDHandler(
		w,
		newArresteeRequest(t, "/api/v1/arrest-report/arrestee/civ1?limit=10&page=1"),
	)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Page       int                   `json:"page"`
		TotalCount int64                 `json:"totalCount"`
		Data       []models.ArrestReport `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 1, body.Page)
	assert.Equal(t, int64(16), body.TotalCount)
	assert.Len(t, body.Data, 6, "page 1 of 16 with limit 10 holds the remaining 6")
}
