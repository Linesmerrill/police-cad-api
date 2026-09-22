package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// A join request is the membership row: asking to join writes "pending" on
// user.communities[], and approving flips it to "approved". The members
// endpoints have always filtered to approved, so the people waiting existed
// nowhere an owner would look — only inside a notification they had to catch.

const statusCommunityID = "507f1f77bcf86cd799439011"

// statusFromFilter pulls the member status out of the query a handler built.
func statusFromFilter(t *testing.T, filter interface{}) string {
	t.Helper()
	doc, ok := filter.(bson.M)
	if !ok {
		t.Fatalf("filter was not a document: %#v", filter)
	}
	clauses, ok := doc["$and"].([]bson.M)
	if !ok {
		t.Fatalf("filter carried no $and: %#v", doc)
	}
	for _, clause := range clauses {
		communities, ok := clause["user.communities"].(bson.M)
		if !ok {
			continue
		}
		elem, ok := communities["$elemMatch"].(bson.M)
		if !ok {
			continue
		}
		if status, ok := elem["status"].(string); ok {
			return status
		}
	}
	return ""
}

func membersRequestWithStatus(t *testing.T, status string) *http.Request {
	t.Helper()
	url := "/api/v1/community/" + statusCommunityID + "/members?page=1&limit=10"
	if status != "" {
		url += "&status=" + status
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	assert.NoError(t, err)
	return mux.SetURLVars(req, map[string]string{"communityId": statusCommunityID})
}

func membersHandlerCapturingFilter(t *testing.T) (handlers.Community, *[]interface{}) {
	t.Helper()
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(&models.Community{}, nil).Maybe()

	udb := &mocks.UserDatabase{}
	var filters []interface{}
	udb.On("CountDocuments", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { filters = append(filters, args.Get(1)) }).
		Return(int64(0), errStopAfterCount).Maybe()

	return handlers.Community{DB: cdb, UDB: udb}, &filters
}

func TestCommunityMembers_DefaultsToApproved(t *testing.T) {
	c, filters := membersHandlerCapturingFilter(t)

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.CommunityMembersHandler).ServeHTTP(rr, membersRequestWithStatus(t, ""))

	assert.NotEmpty(t, *filters)
	assert.Equal(t, models.CommunityMemberStatusApproved, statusFromFilter(t, (*filters)[0]))
}

func TestCommunityMembers_ListsPeopleWaitingToJoin(t *testing.T) {
	c, filters := membersHandlerCapturingFilter(t)

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.CommunityMembersHandler).ServeHTTP(rr, membersRequestWithStatus(t, "pending"))

	assert.NotEmpty(t, *filters)
	assert.Equal(t, models.CommunityMemberStatusPending, statusFromFilter(t, (*filters)[0]))
}

func TestCommunityMembers_AcceptsDeclinedAndBanned(t *testing.T) {
	for _, status := range []string{"declined", "banned"} {
		c, filters := membersHandlerCapturingFilter(t)
		rr := httptest.NewRecorder()
		http.HandlerFunc(c.CommunityMembersHandler).ServeHTTP(rr, membersRequestWithStatus(t, status))

		assert.NotEmpty(t, *filters, status)
		assert.Equal(t, status, statusFromFilter(t, (*filters)[0]), status)
	}
}

// A typo in a client must not make a community look deserted.
func TestCommunityMembers_UnknownStatusFallsBackToApproved(t *testing.T) {
	c, filters := membersHandlerCapturingFilter(t)

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.CommunityMembersHandler).ServeHTTP(rr, membersRequestWithStatus(t, "waiting"))

	assert.NotEmpty(t, *filters)
	assert.Equal(t, models.CommunityMemberStatusApproved, statusFromFilter(t, (*filters)[0]))
}

func TestCommunityMembers_StatusIsCaseInsensitive(t *testing.T) {
	c, filters := membersHandlerCapturingFilter(t)

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.CommunityMembersHandler).ServeHTTP(rr, membersRequestWithStatus(t, "PENDING"))

	assert.NotEmpty(t, *filters)
	assert.Equal(t, models.CommunityMemberStatusPending, statusFromFilter(t, (*filters)[0]))
}

// The counts the Members screen labels its filters with.
func TestMemberStatusCounts_ReportsEachState(t *testing.T) {
	cID, err := primitive.ObjectIDFromHex(statusCommunityID)
	assert.NoError(t, err)

	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(&models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			// One id twice: a duplicate is one banned person, not two.
			BanList: []string{"user-9", "user-8", "user-9", ""},
		},
	}, nil)

	udb := &mocks.UserDatabase{}
	for status, count := range map[string]int64{"approved": 124, "pending": 3, "declined": 7} {
		wanted := status
		udb.On("CountDocuments", mock.Anything, mock.MatchedBy(func(filter interface{}) bool {
			doc, ok := filter.(bson.M)
			if !ok {
				return false
			}
			communities, ok := doc["user.communities"].(bson.M)
			if !ok {
				return false
			}
			elem, ok := communities["$elemMatch"].(bson.M)
			return ok && elem["status"] == wanted
		})).Return(count, nil)
	}

	c := handlers.Community{DB: cdb, UDB: udb}
	req, err := http.NewRequest(http.MethodGet, "/api/v2/community/"+statusCommunityID+"/member-status-counts", nil)
	assert.NoError(t, err)
	req = mux.SetURLVars(req, map[string]string{"communityId": statusCommunityID})

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.MemberStatusCountsHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var body map[string]float64
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, float64(124), body["approved"])
	assert.Equal(t, float64(3), body["pending"])
	assert.Equal(t, float64(7), body["declined"])
	assert.Equal(t, float64(2), body["banned"])
}

func TestMemberStatusCounts_RejectsAnInvalidCommunityID(t *testing.T) {
	c := handlers.Community{DB: &mocks.CommunityDatabase{}, UDB: &mocks.UserDatabase{}}
	req, err := http.NewRequest(http.MethodGet, "/api/v2/community/nope/member-status-counts", nil)
	assert.NoError(t, err)
	req = mux.SetURLVars(req, map[string]string{"communityId": "nope"})

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.MemberStatusCountsHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// Banning and unbanning had no permission check at all: anyone who could reach
// the endpoint could ban anyone from any community.

func banRequest(t *testing.T, actorID string) *http.Request {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"communityId": statusCommunityID})
	assert.NoError(t, err)
	url := "/api/v1/user/" + pickerPendingUser + "/ban-community"
	if actorID != "" {
		url += "?userId=" + actorID
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	assert.NoError(t, err)
	return mux.SetURLVars(req, map[string]string{"userId": pickerPendingUser})
}

func communityOwnedBy(t *testing.T, ownerID string) *models.Community {
	t.Helper()
	cID, err := primitive.ObjectIDFromHex(statusCommunityID)
	assert.NoError(t, err)
	return &models.Community{ID: cID, Details: models.CommunityDetails{OwnerID: ownerID}}
}

func TestBanUserFromCommunity_RefusesSomeoneWhoCannotManageBans(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).
		Return(communityOwnedBy(t, "507f1f77bcf86cd799439099"), nil)

	udb := &mocks.UserDatabase{}
	u := handlers.User{DB: udb, CDB: cdb}

	rr := httptest.NewRecorder()
	http.HandlerFunc(u.BanUserFromCommunityHandler).ServeHTTP(rr, banRequest(t, pickerApprovedUser))

	assert.Equal(t, http.StatusForbidden, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestBanUserFromCommunity_RefusesAnUnidentifiedCaller(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).
		Return(communityOwnedBy(t, pickerApprovedUser), nil)

	u := handlers.User{DB: &mocks.UserDatabase{}, CDB: cdb}

	rr := httptest.NewRecorder()
	http.HandlerFunc(u.BanUserFromCommunityHandler).ServeHTTP(rr, banRequest(t, ""))

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
