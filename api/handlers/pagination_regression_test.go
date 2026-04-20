package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// These tests guard against regressions of the "SetLimit(0) → unbounded query"
// bug and the V2 totalPages divide-by-zero that tripped the Mongo
// "objects returned > 1000" alert. Each case issues a request with no
// ?limit= param and asserts the handler passes a bounded limit to the DB.

func TestCiviliansByUserIDHandler_DefaultLimit(t *testing.T) {
	mockDB := &mocks.CivilianDatabase{}
	h := handlers.Civilian{DB: mockDB}

	var gotLimit, gotSkip int64
	mockDB.On("Find", mock.Anything, mock.Anything, mock.MatchedBy(func(opts *options.FindOptions) bool {
		if opts.Limit != nil {
			gotLimit = *opts.Limit
		}
		if opts.Skip != nil {
			gotSkip = *opts.Skip
		}
		return true
	})).Return([]models.Civilian{}, nil)

	req := httptest.NewRequest("GET", "/api/v1/civilians/user/u1", nil)
	req = mux.SetURLVars(req, map[string]string{"user_id": "u1"})
	h.CiviliansByUserIDHandler(httptest.NewRecorder(), req)

	assert.Equal(t, int64(10), gotLimit, "no ?limit= should default to DefaultListLimit (10)")
	assert.Equal(t, int64(0), gotSkip)
}

func TestCiviliansByUserIDHandler_CapsExcessiveLimit(t *testing.T) {
	mockDB := &mocks.CivilianDatabase{}
	h := handlers.Civilian{DB: mockDB}

	var gotLimit int64
	mockDB.On("Find", mock.Anything, mock.Anything, mock.MatchedBy(func(opts *options.FindOptions) bool {
		if opts.Limit != nil {
			gotLimit = *opts.Limit
		}
		return true
	})).Return([]models.Civilian{}, nil)

	req := httptest.NewRequest("GET", "/api/v1/civilians/user/u1?limit=9999", nil)
	req = mux.SetURLVars(req, map[string]string{"user_id": "u1"})
	h.CiviliansByUserIDHandler(httptest.NewRecorder(), req)

	assert.Equal(t, int64(100), gotLimit, "over-cap ?limit= should clamp to MaxListLimit (100)")
}

// Regression: V2 handler used to compute totalPages = math.Ceil(totalCount / Limit)
// with Limit=0 when ?limit= was missing, producing +Inf and a bogus response.
func TestCiviliansByUserIDHandlerV2_NoPanicWithMissingLimit(t *testing.T) {
	mockDB := &mocks.CivilianDatabase{}
	h := handlers.Civilian{DB: mockDB}

	mockDB.On("Find", mock.Anything, mock.Anything, mock.Anything).Return([]models.Civilian{}, nil)
	mockDB.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(0), nil)

	req := httptest.NewRequest("GET", "/api/v2/civilians/user/u1", nil)
	req = mux.SetURLVars(req, map[string]string{"user_id": "u1"})
	w := httptest.NewRecorder()

	assert.NotPanics(t, func() {
		h.CiviliansByUserIDHandlerV2(w, req)
	})
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestVehiclesByUserIDHandler_DefaultLimit(t *testing.T) {
	mockDB := &mocks.VehicleDatabase{}
	h := handlers.Vehicle{DB: mockDB}

	var gotLimit int64
	mockDB.On("Find", mock.Anything, mock.Anything, mock.MatchedBy(func(opts *options.FindOptions) bool {
		if opts.Limit != nil {
			gotLimit = *opts.Limit
		}
		return true
	})).Return([]models.Vehicle{}, nil)

	req := httptest.NewRequest("GET", "/api/v1/vehicles/user/u1", nil)
	req = mux.SetURLVars(req, map[string]string{"user_id": "u1"})
	h.VehiclesByUserIDHandler(httptest.NewRecorder(), req)

	assert.Equal(t, int64(10), gotLimit)
}

func TestFirearmsByUserIDHandler_DefaultLimit(t *testing.T) {
	mockDB := &mocks.FirearmDatabase{}
	h := handlers.Firearm{DB: mockDB}

	var gotLimit int64
	mockDB.On("Find", mock.Anything, mock.Anything, mock.MatchedBy(func(opts *options.FindOptions) bool {
		if opts.Limit != nil {
			gotLimit = *opts.Limit
		}
		return true
	})).Return([]models.Firearm{}, nil)

	req := httptest.NewRequest("GET", "/api/v1/firearms/user/u1", nil)
	req = mux.SetURLVars(req, map[string]string{"user_id": "u1"})
	h.FirearmsByUserIDHandler(httptest.NewRecorder(), req)

	assert.Equal(t, int64(10), gotLimit)
}
