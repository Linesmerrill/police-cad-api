package handlers_test

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
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

const previewCommunityID = "6adab9b0c602a448a910d97e"

func previewRequest(id, roles string) *http.Request {
	path := "/api/v1/admin/community-preview/" + id
	if roles != "" {
		path += "?roles=" + roles
	}
	req, _ := http.NewRequest(http.MethodGet, path, nil)
	return mux.SetURLVars(req, map[string]string{"id": id})
}

func servePreview(t *testing.T, cdb *mocks.CommunityDatabase, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	ra := handlers.ReportAdmin{CDB: cdb}
	rr := httptest.NewRecorder()
	http.HandlerFunc(ra.AdminCommunityPreviewHandler).ServeHTTP(rr, req)
	return rr
}

// Only staff may look, the same rule as the report it is opened from.
func TestCommunityPreview_RequiresStaff(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	rr := servePreview(t, cdb, previewRequest(previewCommunityID, "user"))
	assert.Equal(t, http.StatusForbidden, rr.Code)
	cdb.AssertNotCalled(t, "FindOneIncludingPending", mock.Anything, mock.Anything)
}

// The preview carries what the community shows the world, and nothing that
// would let a reviewer act on it or see its members.
func TestCommunityPreview_ReturnsPresentationOnly(t *testing.T) {
	cID, _ := primitive.ObjectIDFromHex(previewCommunityID)
	pending := primitive.NewDateTimeFromTime(time.Date(2026, 10, 23, 1, 33, 0, 0, time.UTC))
	created := primitive.NewDateTimeFromTime(time.Date(2026, 10, 23, 1, 34, 40, 0, time.UTC))

	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOneIncludingPending", mock.Anything, bson.M{"_id": cID}).Return(&models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			Name:              "Chicago xbox roleplay",
			OwnerID:           "6ab2f588ff0ab400157a820a",
			Code:              "SECRETCODE",
			ImageLink:         "https://res.cloudinary.com/x/logo.png",
			Description:       "Serious RP on Xbox.",
			PromotionalText:   "Join now",
			Tags:              []string{"Xbox", "GTA RP"},
			MembersCount:      2,
			DiscordInviteURL:  "https://discord.gg/g8dkBk6QF",
			BanList:           []string{"someone"},
			InviteCodeIds:     []string{"abc"},
			PendingDeletionAt: &pending,
			CreatedAt:         created,
			Members:           map[string]models.MemberDetail{"u1": {}},
			Departments: []models.Department{
				{Name: "CPD", Description: "Chicago Police", Image: "https://res.cloudinary.com/x/cpd.png"},
			},
			Events: []models.Event{
				{Title: "Older", CreatedAt: primitive.NewDateTimeFromTime(time.Now().Add(-48 * time.Hour))},
				{Title: "Newer", CreatedAt: primitive.NewDateTimeFromTime(time.Now())},
			},
		},
	}, nil)

	rr := servePreview(t, cdb, previewRequest(previewCommunityID, "admin"))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var body struct {
		Community map[string]interface{} `json:"community"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	c := body.Community

	assert.Equal(t, "Chicago xbox roleplay", c["name"])
	assert.Equal(t, "https://res.cloudinary.com/x/logo.png", c["imageLink"])
	assert.Equal(t, "Serious RP on Xbox.", c["description"])
	assert.Equal(t, []interface{}{"Xbox", "GTA RP"}, c["tags"])
	assert.Equal(t, float64(2), c["membersCount"])
	assert.NotNil(t, c["pendingDeletionAt"], "a community being deleted can still be reviewed")
	assert.Equal(t, "6ab2f588ff0ab400157a820a", c["owner"].(map[string]interface{})["id"])

	depts := c["departments"].([]interface{})
	require.Len(t, depts, 1)
	assert.Equal(t, "https://res.cloudinary.com/x/cpd.png", depts[0].(map[string]interface{})["image"])

	events := c["events"].([]interface{})
	require.Len(t, events, 2)
	assert.Equal(t, "Newer", events[0].(map[string]interface{})["title"], "newest event first, as the home page shows them")

	raw := rr.Body.String()
	for _, leaked := range []string{"SECRETCODE", "banList", "inviteCodeIds", "members\"", "roles"} {
		assert.False(t, strings.Contains(raw, leaked), "preview must not carry %q", leaked)
	}
}

func TestCommunityPreview_NotFound(t *testing.T) {
	cID, _ := primitive.ObjectIDFromHex(previewCommunityID)
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOneIncludingPending", mock.Anything, bson.M{"_id": cID}).Return(nil, mongo.ErrNoDocuments)

	rr := servePreview(t, cdb, previewRequest(previewCommunityID, "admin"))
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestCommunityPreview_RejectsABadID(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	rr := servePreview(t, cdb, previewRequest("not-an-id", "admin"))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
