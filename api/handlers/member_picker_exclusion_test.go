package handlers_test

import (
	"errors"
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

// The Add Members picker asks the API to leave out people the department
// already has, via exclude_dept_id. "Already has" has to mean the same thing
// the department's member list means: approved.
//
// The report this covers: searching a private department's picker for a member
// found nobody, while the same person appeared in another department's picker.
// They had a pending join request, which lives in the same members array, so
// the picker excluded them as already in the department while the member list,
// which shows approved members only, said they were not. Nobody could add them.

// Returned by the mocked count so the handler stops before the find.
var errStopAfterCount = errors.New("stop after count")

const (
	pickerCommunityID  = "507f1f77bcf86cd799439011"
	pickerDepartmentID = "507f1f77bcf86cd799439022"
	pickerApprovedUser = "507f1f77bcf86cd799439001"
	pickerPendingUser  = "507f1f77bcf86cd799439002"
	pickerDeniedUser   = "507f1f77bcf86cd799439003"
)

// excludedIDsFromFilter digs the $nin list out of the filter the handler built.
func excludedIDsFromFilter(t *testing.T, filter interface{}) []string {
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
		idClause, ok := clause["_id"].(bson.M)
		if !ok {
			continue
		}
		nin, ok := idClause["$nin"].([]primitive.ObjectID)
		if !ok {
			continue
		}
		out := make([]string, 0, len(nin))
		for _, oid := range nin {
			out = append(out, oid.Hex())
		}
		return out
	}
	return nil
}

func communityWithMixedDepartmentMembers(t *testing.T) *models.Community {
	t.Helper()
	cID, err := primitive.ObjectIDFromHex(pickerCommunityID)
	assert.NoError(t, err)
	dID, err := primitive.ObjectIDFromHex(pickerDepartmentID)
	assert.NoError(t, err)

	return &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			Name: "Redgum RP",
			Departments: []models.Department{
				{
					ID:               dID,
					Name:             "Fire & Rescue",
					ApprovalRequired: true,
					Members: []models.MemberStatus{
						{UserID: pickerApprovedUser, Status: "approved"},
						{UserID: pickerPendingUser, Status: "pending"},
						{UserID: pickerDeniedUser, Status: "denied"},
					},
				},
			},
		},
	}
}

func newMembersRequest(t *testing.T, excludeDept bool) *http.Request {
	t.Helper()
	url := "/api/v1/community/" + pickerCommunityID + "/members?page=1&limit=10"
	if excludeDept {
		url += "&exclude_dept_id=" + pickerDepartmentID
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	assert.NoError(t, err)
	return mux.SetURLVars(req, map[string]string{"communityId": pickerCommunityID})
}

func pickerHandler(t *testing.T) (handlers.Community, *[]interface{}) {
	t.Helper()
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).
		Return(communityWithMixedDepartmentMembers(t), nil).Maybe()

	// The count runs first and the handler gives up if it fails, so failing it
	// captures the query without needing a cursor for the find that follows.
	udb := &mocks.UserDatabase{}
	var filters []interface{}
	udb.On("CountDocuments", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { filters = append(filters, args.Get(1)) }).
		Return(int64(0), errStopAfterCount).Maybe()

	return handlers.Community{DB: cdb, UDB: udb}, &filters
}

func TestMemberPicker_ExcludesApprovedMembersOnly(t *testing.T) {
	c, filters := pickerHandler(t)

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.CommunityMembersHandler).ServeHTTP(rr, newMembersRequest(t, true))

	assert.NotEmpty(t, *filters, "the handler built no query")
	excluded := excludedIDsFromFilter(t, (*filters)[0])

	assert.Equal(t, []string{pickerApprovedUser}, excluded,
		"a pending or denied entry is not membership, so those people stay in the picker")
}

// The picker has to tell the two apart: someone who has not asked to join, and
// someone whose request is sitting there waiting. Without that, an admin adding
// members cannot see why one name behaves differently from another.
func TestMemberPicker_ReportsWhoIsWaitingToJoin(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).
		Return(communityWithMixedDepartmentMembers(t), nil).Maybe()

	membership := handlers.DepartmentMembershipForTest(cdb, pickerCommunityID, pickerDepartmentID)

	assert.Equal(t, map[string]string{
		pickerPendingUser: "pending",
		pickerDeniedUser:  "denied",
	}, membership.Requests, "an approved member is not a request")

	assert.Len(t, membership.ApprovedIDs, 1)
	assert.Equal(t, pickerApprovedUser, membership.ApprovedIDs[0].Hex())
}

func TestMemberPicker_WithoutTheParameterExcludesNobody(t *testing.T) {
	c, filters := pickerHandler(t)

	rr := httptest.NewRecorder()
	http.HandlerFunc(c.CommunityMembersHandler).ServeHTTP(rr, newMembersRequest(t, false))

	assert.NotEmpty(t, *filters, "the handler built no query")
	assert.Nil(t, excludedIDsFromFilter(t, (*filters)[0]))
}
