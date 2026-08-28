package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// Countdown records are maintained by hand in Mongo so a slipped launch date
// can be corrected without a deploy. These tests pin the two things that
// protects: the read path tolerates a partial document, and having no
// countdowns is a normal 200 rather than an error.

func countdownCursor(t *testing.T, docs ...interface{}) databases.MongoCursor {
	t.Helper()
	cursor, err := databases.NewMongoCursorFromDocuments(docs)
	assert.NoError(t, err)
	return cursor
}

func TestListCountdowns_FiltersByActiveAndSurface(t *testing.T) {
	mockDB := &mocks.CountdownDatabase{}
	var gotFilter interface{}
	mockDB.On("Find", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { gotFilter = args.Get(1) }).
		Return(countdownCursor(t, bson.M{
			"slug":       "gta6",
			"title":      "Grand Theft Auto VI",
			"launchDate": "2026-11-19",
			"mode":       models.CountdownModeLocalMidnight,
			"surfaces":   []string{"web", "mobile", "bot"},
			"active":     true,
		}), nil)

	req := httptest.NewRequest("GET", "/api/v1/countdowns?surface=mobile", nil)
	w := httptest.NewRecorder()
	Countdown{DB: mockDB}.ListCountdownsHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	filter, ok := gotFilter.(bson.M)
	assert.True(t, ok, "filter should be a bson.M")
	assert.Equal(t, true, filter["active"], "inactive countdowns must never surface")
	assert.Contains(t, filter, "$or",
		"a recognized surface narrows the query; records listing only other surfaces are excluded")

	var got []models.Countdown
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Len(t, got, 1)
	assert.Equal(t, "gta6", got[0].Slug)
}

func TestListCountdowns_UnknownSurface_DoesNotNarrowQuery(t *testing.T) {
	mockDB := &mocks.CountdownDatabase{}
	var gotFilter interface{}
	mockDB.On("Find", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { gotFilter = args.Get(1) }).
		Return(countdownCursor(t), nil)

	req := httptest.NewRequest("GET", "/api/v1/countdowns?surface=typo", nil)
	w := httptest.NewRecorder()
	Countdown{DB: mockDB}.ListCountdownsHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	filter, _ := gotFilter.(bson.M)
	assert.NotContains(t, filter, "$or",
		"a typo'd surface should widen to all active countdowns, not blank the card")
}

func TestListCountdowns_PartialDocument_GetsDefaults(t *testing.T) {
	mockDB := &mocks.CountdownDatabase{}
	// A hand-edited document that omits mode and postLaunchHours entirely.
	mockDB.On("Find", mock.Anything, mock.Anything, mock.Anything).
		Return(countdownCursor(t, bson.M{
			"slug":       "gta6",
			"launchDate": "2026-11-19",
			"active":     true,
		}), nil)

	req := httptest.NewRequest("GET", "/api/v1/countdowns", nil)
	w := httptest.NewRecorder()
	Countdown{DB: mockDB}.ListCountdownsHandler(w, req)

	var got []models.Countdown
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Len(t, got, 1)
	assert.Equal(t, models.CountdownModeLocalMidnight, got[0].Mode,
		"mode defaults to localMidnight so an omitted field doesn't produce an invalid target")
	assert.Equal(t, defaultPostLaunchHours, got[0].PostLaunchHours,
		"without a retirement window a passed countdown becomes a negative timer")
	assert.NotNil(t, got[0].Surfaces, "surfaces must serialize as [] rather than null")
}

func TestListCountdowns_Empty_Returns200AndEmptyArray(t *testing.T) {
	mockDB := &mocks.CountdownDatabase{}
	mockDB.On("Find", mock.Anything, mock.Anything, mock.Anything).
		Return(countdownCursor(t), nil)

	req := httptest.NewRequest("GET", "/api/v1/countdowns?surface=web", nil)
	w := httptest.NewRecorder()
	Countdown{DB: mockDB}.ListCountdownsHandler(w, req)

	// Having no countdowns is the steady state between launches. A 404 here
	// would log a stacktrace on every page load.
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `[]`, w.Body.String(),
		"clients iterate the response; null would break them")
	assert.Equal(t, "public, max-age=300", w.Header().Get("Cache-Control"))
}

func TestListCountdowns_FindError_Returns500(t *testing.T) {
	mockDB := &mocks.CountdownDatabase{}
	mockDB.On("Find", mock.Anything, mock.Anything, mock.Anything).
		Return(databases.MongoCursor{}, errors.New("transient mongo error"))

	req := httptest.NewRequest("GET", "/api/v1/countdowns", nil)
	w := httptest.NewRecorder()
	Countdown{DB: mockDB}.ListCountdownsHandler(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
