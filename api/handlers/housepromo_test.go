package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// House promos are maintained by hand in Mongo so one can be switched off
// without a mobile release. These tests pin what that depends on: nothing
// expired or inactive is ever served, a half-written document does not reach a
// client, and having no promos running is a normal 200.

func housePromoCursor(t *testing.T, docs ...interface{}) databases.MongoCursor {
	t.Helper()
	cursor, err := databases.NewMongoCursorFromDocuments(docs)
	assert.NoError(t, err)
	return cursor
}

func liveHousePromo() bson.M {
	return bson.M{
		"slug":     "favion-osrs",
		"eyebrow":  "A quick one from us",
		"title":    "2,000,000,000 GP or bust",
		"body":     "My brother lost 2 billion GP in Old School RuneScape.",
		"ctaLabel": "Support Favion",
		"ctaUrl":   "https://www.gofundme.com/f/help-my-brother-recover-2b-lost-runescape-gp",
		"surfaces": []string{"web", "mobile"},
		"active":   true,
	}
}

func runHousePromos(t *testing.T, url string, cursor databases.MongoCursor) (*httptest.ResponseRecorder, interface{}) {
	t.Helper()
	mockDB := &mocks.HousePromoDatabase{}
	var gotFilter interface{}
	mockDB.On("Find", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { gotFilter = args.Get(1) }).
		Return(cursor, nil)

	req := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	HousePromo{DB: mockDB}.ListHousePromosHandler(w, req)
	return w, gotFilter
}

func TestListHousePromos_FiltersActiveSurfaceAndExpiry(t *testing.T) {
	w, gotFilter := runHousePromos(t, "/api/v1/house-promos?surface=mobile",
		housePromoCursor(t, liveHousePromo()))

	assert.Equal(t, http.StatusOK, w.Code)

	filter, ok := gotFilter.(bson.M)
	assert.True(t, ok, "filter should be a bson.M")
	assert.Equal(t, true, filter["active"], "an inactive promo must never surface")

	// Expiry is enforced server-side because a released mobile build cannot be
	// relied on to stop showing a finished campaign.
	and, ok := filter["$and"].([]bson.M)
	assert.True(t, ok, "expiry and surface clauses live under $and")
	assert.Len(t, and, 2, "one clause for expiry, one for the recognized surface")

	var got []models.HousePromo
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Len(t, got, 1)
	assert.Equal(t, "favion-osrs", got[0].Slug)
	assert.Equal(t, "Support Favion", got[0].CTALabel)
}

func TestListHousePromos_UnknownSurfaceStillFiltersExpiry(t *testing.T) {
	// A typo in a query string is not worth a 400, but it must not widen the
	// query to include promos that have finished.
	_, gotFilter := runHousePromos(t, "/api/v1/house-promos?surface=telegram",
		housePromoCursor(t, liveHousePromo()))

	filter := gotFilter.(bson.M)
	and := filter["$and"].([]bson.M)
	assert.Len(t, and, 1, "no surface clause, but the expiry clause stays")
	assert.Equal(t, true, filter["active"])
}

func TestListHousePromos_DropsHalfWrittenDocuments(t *testing.T) {
	// These are hand-edited records. One saved without a link would otherwise
	// render as a card with a dead button.
	noURL := liveHousePromo()
	delete(noURL, "ctaUrl")
	noTitle := liveHousePromo()
	noTitle["slug"] = "untitled"
	delete(noTitle, "title")

	w, _ := runHousePromos(t, "/api/v1/house-promos?surface=web",
		housePromoCursor(t, noURL, noTitle, liveHousePromo()))

	var got []models.HousePromo
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Len(t, got, 1, "only the complete document survives")
	assert.Equal(t, "favion-osrs", got[0].Slug)
}

func TestListHousePromos_DefaultsCTALabel(t *testing.T) {
	noLabel := liveHousePromo()
	delete(noLabel, "ctaLabel")

	w, _ := runHousePromos(t, "/api/v1/house-promos", housePromoCursor(t, noLabel))

	var got []models.HousePromo
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Len(t, got, 1)
	assert.Equal(t, "Learn more", got[0].CTALabel, "a missing label must not render an empty button")
}

func TestListHousePromos_EmptyIsAnArrayNotNull(t *testing.T) {
	// The steady state is no promo running. A client parsing null where it
	// expects a list is a crash, and a 404 here would log a stacktrace per call.
	w, _ := runHousePromos(t, "/api/v1/house-promos?surface=web", housePromoCursor(t))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, "[]", w.Body.String())
	assert.Equal(t, "public, max-age=300", w.Header().Get("Cache-Control"),
		"the cache ceiling is the delay between switching a promo off and it being gone")
}

func TestListHousePromos_SurfacesDefaultToEmptySlice(t *testing.T) {
	noSurfaces := liveHousePromo()
	delete(noSurfaces, "surfaces")

	w, _ := runHousePromos(t, "/api/v1/house-promos", housePromoCursor(t, noSurfaces))

	var raw []map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	assert.Len(t, raw, 1)
	assert.Equal(t, []interface{}{}, raw[0]["surfaces"], "clients iterate this; null would throw")
}

func TestListHousePromos_ExpiredRecordIsNotServed(t *testing.T) {
	// Belt and braces: the query excludes these, but if one is ever returned
	// anyway the response should still carry a usable endsAt for the client to
	// re-check rather than silently rendering forever.
	expired := liveHousePromo()
	expired["endsAt"] = primitive.NewDateTimeFromTime(time.Now().Add(-24 * time.Hour))

	w, _ := runHousePromos(t, "/api/v1/house-promos", housePromoCursor(t, expired))

	var got []models.HousePromo
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Len(t, got, 1)
	assert.True(t, got[0].EndsAt.Time().Before(time.Now()), "endsAt round-trips for the client check")
}

func TestListHousePromos_DatabaseErrorIsNotA200(t *testing.T) {
	mockDB := &mocks.HousePromoDatabase{}
	mockDB.On("Find", mock.Anything, mock.Anything, mock.Anything).
		Return(databases.MongoCursor{}, errors.New("mongo is unwell"))

	req := httptest.NewRequest("GET", "/api/v1/house-promos", nil)
	w := httptest.NewRecorder()
	HousePromo{DB: mockDB}.ListHousePromosHandler(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
