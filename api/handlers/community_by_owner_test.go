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
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
)

const ownerListID = "507f1f77bcf86cd799439077"

func ownedCommunityDoc(name string) bson.M {
	return bson.M{
		"_id": primitive.NewObjectID(),
		"community": bson.M{
			"name":    name,
			"ownerID": ownerListID,
		},
	}
}

func ownerCursor(t *testing.T, docs ...interface{}) databases.MongoCursor {
	t.Helper()
	cursor, err := databases.NewMongoCursorFromDocuments(docs)
	assert.NoError(t, err)
	return cursor
}

// sortSpec pulls the sort out of the FindOptions the handler built.
func sortSpec(opts *options.FindOptions) bson.D {
	if opts == nil || opts.Sort == nil {
		return nil
	}
	d, _ := opts.Sort.(bson.D)
	return d
}

// TestCommunitiesByOwnerV2_ReportsTheRealTotal is the regression guard for
// "I created two more communities but it still says I have 6".
//
// v1 returns a bare array with no total, so the website set its owned-community
// total to the length of the page it had just received. At a page size of 6
// that pinned the count at 6 and disabled the next-page control, so anything
// past the first six -- including a community just created -- was unreachable.
func TestCommunitiesByOwnerV2_ReportsTheRealTotal(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}

	page := make([]interface{}, 0, 6)
	for i := 0; i < 6; i++ {
		page = append(page, ownedCommunityDoc("Community"))
	}

	cdb.On("Find", mock.Anything, bson.M{"community.ownerID": ownerListID}, mock.Anything).
		Return(ownerCursor(t, page...), nil)
	cdb.On("CountDocuments", mock.Anything, bson.M{"community.ownerID": ownerListID}).
		Return(int64(14), nil)
	udb.On("Aggregate", mock.Anything, mock.Anything).Return(ownerCursor(t), nil).Maybe()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/communities/"+ownerListID+"?limit=6&page=1", nil)
	req = mux.SetURLVars(req, map[string]string{"owner_id": ownerListID})
	w := httptest.NewRecorder()
	handlers.Community{DB: cdb, UDB: udb}.CommunitiesByOwnerIDHandlerV2(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Data       []map[string]interface{} `json:"data"`
		TotalCount int64                    `json:"totalCount"`
		Page       int                      `json:"page"`
		Limit      int                      `json:"limit"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	// A full page must not be mistaken for the whole set.
	assert.Len(t, body.Data, 6)
	assert.Equal(t, int64(14), body.TotalCount)
	assert.Greater(t, body.TotalCount, int64(len(body.Data)))
	assert.Equal(t, 1, body.Page)
	assert.Equal(t, 6, body.Limit)
}

// TestCommunitiesByOwnerV2_PagesAreOneBased matches
// /api/v2/user/{userId}/communities and the v1 handler this replaces, so a
// caller can switch endpoints without changing how it pages.
func TestCommunitiesByOwnerV2_PagesAreOneBased(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}

	var gotSkip int64 = -1
	cdb.On("Find", mock.Anything, mock.Anything, mock.MatchedBy(func(opts *options.FindOptions) bool {
		if opts != nil && opts.Skip != nil {
			gotSkip = *opts.Skip
		}
		return true
	})).Return(ownerCursor(t), nil)
	cdb.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(0), nil)
	udb.On("Aggregate", mock.Anything, mock.Anything).Return(ownerCursor(t), nil).Maybe()

	req := httptest.NewRequest(http.MethodGet, "/?limit=6&page=3", nil)
	req = mux.SetURLVars(req, map[string]string{"owner_id": ownerListID})
	w := httptest.NewRecorder()
	handlers.Community{DB: cdb, UDB: udb}.CommunitiesByOwnerIDHandlerV2(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(12), gotSkip, "page 3 at limit 6 should skip 12, not 18")
}

// TestCommunitiesByOwnerV2_EmptyIsAnArrayNotNull keeps `data` iterable on the
// client rather than serialising as null.
func TestCommunitiesByOwnerV2_EmptyIsAnArrayNotNull(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	udb := &mocks.UserDatabase{}

	cdb.On("Find", mock.Anything, mock.Anything, mock.Anything).Return(ownerCursor(t), nil)
	cdb.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(0), nil)
	udb.On("Aggregate", mock.Anything, mock.Anything).Return(ownerCursor(t), nil).Maybe()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = mux.SetURLVars(req, map[string]string{"owner_id": ownerListID})
	w := httptest.NewRecorder()
	handlers.Community{DB: cdb, UDB: udb}.CommunitiesByOwnerIDHandlerV2(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"data":[]`)
	assert.NotContains(t, w.Body.String(), `"data":null`)
}

// TestCommunitiesByOwner_SortIsDeterministic covers both handlers. Without a
// sort Mongo returns natural order, so skip/limit paging can repeat or omit
// documents between requests and a newly created community can land anywhere.
// Newest first puts one someone just made on page 1.
func TestCommunitiesByOwner_SortIsDeterministic(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(c handlers.Community, w http.ResponseWriter, r *http.Request)
	}{
		{"v1", func(c handlers.Community, w http.ResponseWriter, r *http.Request) { c.CommunitiesByOwnerIDHandler(w, r) }},
		{"v2", func(c handlers.Community, w http.ResponseWriter, r *http.Request) { c.CommunitiesByOwnerIDHandlerV2(w, r) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cdb := &mocks.CommunityDatabase{}
			udb := &mocks.UserDatabase{}

			var got bson.D
			cdb.On("Find", mock.Anything, mock.Anything, mock.MatchedBy(func(opts *options.FindOptions) bool {
				got = sortSpec(opts)
				return true
			})).Return(ownerCursor(t), nil)
			cdb.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(0), nil).Maybe()
			udb.On("Aggregate", mock.Anything, mock.Anything).Return(ownerCursor(t), nil).Maybe()

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = mux.SetURLVars(req, map[string]string{"owner_id": ownerListID})
			tc.invoke(handlers.Community{DB: cdb, UDB: udb}, httptest.NewRecorder(), req)

			assert.Equal(t, bson.D{
				{Key: "community.createdAt", Value: -1},
				{Key: "_id", Value: -1},
			}, got)
		})
	}
}
