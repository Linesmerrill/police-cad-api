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

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

const guardCommunityID = "6a14be849e33604ef2c8d373"

func patchCommunity(t *testing.T, cdb *mocks.CommunityDatabase, body string) *httptest.ResponseRecorder {
	t.Helper()
	req, _ := http.NewRequest("PATCH", "/api/v1/community/"+guardCommunityID, strings.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"community_id": guardCommunityID})
	// A successful update writes an audit entry in a goroutine.
	audit := &mocks.AuditLogDatabase{}
	audit.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil)
	rr := httptest.NewRecorder()
	http.HandlerFunc(Community{DB: cdb, ALDB: audit}.UpdateCommunityFieldHandler).ServeHTTP(rr, req)
	time.Sleep(20 * time.Millisecond) // let the audit goroutine finish
	return rr
}

func delistedCommunity(until time.Time) *models.Community {
	oid, _ := primitive.ObjectIDFromHex(guardCommunityID)
	u := primitive.NewDateTimeFromTime(until)
	return &models.Community{ID: oid, Details: models.CommunityDetails{
		Visibility: "private",
		ListingSuspension: &models.ListingSuspension{
			Until: &u, Category: "Spam", CategoryPhrase: "spam or repeated unwanted messages",
			Reason: "staff note", IssuedBy: "Merrill L",
		},
	}}
}

// The catch-all PATCH writes any key it is given. Before this, a community
// owner could lift their own delisting, and anyone could take a community.
func TestCommunityPatch_RefusesServerOwnedFields(t *testing.T) {
	for _, body := range []string{
		`{"listingSuspension": null}`,
		`{"listingSuspension.until": "2020-01-01T00:00:00Z"}`,
		`{"ownerID": "someone-else"}`,
		`{"subscription": {"plan": "elite", "active": true}}`,
		`{"membersCount": 9999}`,
		`{"pendingDeletionAt": null}`,
		`{"name": "fine", "banList": []}`,
	} {
		t.Run(body, func(t *testing.T) {
			cdb := &mocks.CommunityDatabase{}
			rr := patchCommunity(t, cdb, body)
			assert.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())
			cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

// A delisted community cannot be flipped public: the toggle would save and
// change nothing, which reads as though the penalty had been lifted.
func TestCommunityPatch_RefusesPublicWhileDelisted(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(delistedCommunity(time.Now().Add(72*time.Hour)), nil)

	rr := patchCommunity(t, cdb, `{"visibility": "public"}`)

	assert.Equal(t, http.StatusConflict, rr.Code)
	assert.Contains(t, rr.Body.String(), "removed from public listings until")
	assert.Contains(t, rr.Body.String(), "spam or repeated unwanted messages")
	assert.NotContains(t, rr.Body.String(), "staff note", "the staff reason never reaches the owner")
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestCommunityPatch_AllowsEverythingElse(t *testing.T) {
	tests := []struct {
		name      string
		community *models.Community
		body      string
	}{
		{"private while delisted", delistedCommunity(time.Now().Add(time.Hour)), `{"visibility": "private"}`},
		{"public once the delisting has lifted", delistedCommunity(time.Now().Add(-time.Hour)), `{"visibility": "public"}`},
		{"public when never delisted", &models.Community{}, `{"visibility": "public"}`},
		{"other settings while delisted", delistedCommunity(time.Now().Add(time.Hour)), `{"description": "new"}`},
		// The website's profile form sends visibility on every save. A
		// community already public when it was delisted must still be able to
		// save its name and description.
		{"re-sending public when already public", func() *models.Community {
			c := delistedCommunity(time.Now().Add(time.Hour))
			c.Details.Visibility = "public"
			return c
		}(), `{"visibility": "public", "name": "Red Red RP", "description": "edited"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cdb := &mocks.CommunityDatabase{}
			cdb.On("FindOne", mock.Anything, mock.Anything).Return(tt.community, nil)
			cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)
			rr := patchCommunity(t, cdb, tt.body)
			assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			cdb.AssertCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

// Community and user documents are served by public endpoints. The staff
// reason, the admin who acted, and any legal hold must never be in them.
func TestModerationFields_StaffOnlyPartsNeverSerialise(t *testing.T) {
	until := primitive.NewDateTimeFromTime(time.Now().Add(time.Hour))
	community := models.CommunityDetails{ListingSuspension: &models.ListingSuspension{
		Until: &until, Category: "Spam", CategoryPhrase: "spam or repeated unwanted messages",
		OffenseID: "off-1", Reason: "staff note", IssuedBy: "Merrill L",
	}}
	raw, _ := json.Marshal(community)
	out := string(raw)
	assert.Contains(t, out, "spam or repeated unwanted messages", "the owner is told why")
	for _, secret := range []string{"staff note", "Merrill L", "off-1"} {
		assert.NotContains(t, out, secret)
	}

	user := models.UserDetails{
		Suspension: &models.Suspension{Until: &until, Reason: "Escalated child-safety report 123", IssuedBy: "Merrill L", OffenseID: "off-2"},
		LegalHold:  &models.LegalHold{SetBy: "Merrill L", ReportID: "123", Note: "Escalated to the NCMEC CyberTipline."},
	}
	raw, _ = json.Marshal(user)
	out = string(raw)
	for _, secret := range []string{"child-safety", "CyberTipline", "legalHold", "Merrill L", "off-2"} {
		assert.NotContains(t, out, secret)
	}
	// The data itself is still stored.
	b, _ := bson.Marshal(user)
	var back bson.M
	_ = bson.Unmarshal(b, &back)
	assert.Contains(t, back, "legalHold")
	assert.Contains(t, back["suspension"], "reason")
}
