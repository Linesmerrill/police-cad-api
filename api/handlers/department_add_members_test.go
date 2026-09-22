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

const (
	addMembersCommunityID  = "507f1f77bcf86cd799439011"
	addMembersDepartmentID = "507f1f77bcf86cd799439022"
	addMembersExistingUser = "507f1f77bcf86cd799439001"
	addMembersNewUser      = "507f1f77bcf86cd799439002"
	addMembersSecondUser   = "507f1f77bcf86cd799439003"
)

// communityWithDepartmentMembers builds a community whose single department already
// holds the given members.
func communityWithDepartmentMembers(t *testing.T, members ...models.MemberStatus) *models.Community {
	t.Helper()
	cID, err := primitive.ObjectIDFromHex(addMembersCommunityID)
	assert.NoError(t, err)
	dID, err := primitive.ObjectIDFromHex(addMembersDepartmentID)
	assert.NoError(t, err)

	return &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			Name: "Rockford RP",
			Departments: []models.Department{
				{
					ID:      dID,
					Name:    "State Police",
					Members: members,
				},
			},
		},
	}
}

func newAddMembersRequest(t *testing.T, memberIDs ...string) *http.Request {
	t.Helper()
	raw, err := json.Marshal(map[string][]string{"members": memberIDs})
	assert.NoError(t, err)
	req, err := http.NewRequest(
		http.MethodPost,
		"/api/v1/community/"+addMembersCommunityID+"/departments/"+addMembersDepartmentID+"/members",
		bytes.NewReader(raw),
	)
	assert.NoError(t, err)
	return mux.SetURLVars(req, map[string]string{
		"communityId":  addMembersCommunityID,
		"departmentId": addMembersDepartmentID,
	})
}

// pushedUserIDs pulls the userIDs out of the $push the handler issues.
func pushedUserIDs(t *testing.T, update interface{}) []string {
	t.Helper()
	push, ok := update.(bson.M)["$push"].(bson.M)
	if !ok {
		t.Fatalf("update did not carry a $push document: %#v", update)
	}
	var ids []string
	for _, value := range push {
		each, ok := value.(bson.M)["$each"].([]interface{})
		if !ok {
			t.Fatalf("$push did not carry an $each list: %#v", value)
		}
		for _, member := range each {
			ids = append(ids, member.(bson.M)["userID"].(string))
		}
	}
	return ids
}

func decodeAddMembersBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	return body
}

func TestUpdateDepartmentMembers_AddsANewMember(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).
		Return(communityWithDepartmentMembers(t), nil)

	var captured interface{}
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(2) }).
		Return(nil)

	c := handlers.Community{DB: cdb}
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentMembersHandler).ServeHTTP(rr, newAddMembersRequest(t, addMembersNewUser))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, []string{addMembersNewUser}, pushedUserIDs(t, captured))
	assert.Equal(t, []interface{}{addMembersNewUser}, decodeAddMembersBody(t, rr)["added"])
}

// Adding someone the department already has used to answer 409, which surfaced as
// "Failed to add user" every time a picker offered a member it had not paged past.
func TestUpdateDepartmentMembers_ExistingMemberIsSkippedNotRejected(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).
		Return(communityWithDepartmentMembers(t, models.MemberStatus{
			UserID: addMembersExistingUser,
			Status: "approved",
		}), nil)

	c := handlers.Community{DB: cdb}
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentMembersHandler).ServeHTTP(rr, newAddMembersRequest(t, addMembersExistingUser))

	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeAddMembersBody(t, rr)
	assert.Equal(t, []interface{}{addMembersExistingUser}, body["skipped"])
	assert.Empty(t, body["added"])
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// A bulk add used to stop at the first duplicate, leaving the members before it
// written and the ones after it dropped, under an error toast.
func TestUpdateDepartmentMembers_BulkAddSkipsOnlyTheDuplicate(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).
		Return(communityWithDepartmentMembers(t, models.MemberStatus{
			UserID: addMembersExistingUser,
			Status: "approved",
		}), nil)

	var captured interface{}
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(2) }).
		Return(nil)

	c := handlers.Community{DB: cdb}
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentMembersHandler).ServeHTTP(rr, newAddMembersRequest(
		t, addMembersNewUser, addMembersExistingUser, addMembersSecondUser,
	))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, []string{addMembersNewUser, addMembersSecondUser}, pushedUserIDs(t, captured))
	cdb.AssertNumberOfCalls(t, "UpdateOne", 1)

	body := decodeAddMembersBody(t, rr)
	assert.Equal(t, []interface{}{addMembersNewUser, addMembersSecondUser}, body["added"])
	assert.Equal(t, []interface{}{addMembersExistingUser}, body["skipped"])
}

// The same id twice in one request must not create two entries.
func TestUpdateDepartmentMembers_RepeatedIDInOneRequestIsAddedOnce(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).
		Return(communityWithDepartmentMembers(t), nil)

	var captured interface{}
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(2) }).
		Return(nil)

	c := handlers.Community{DB: cdb}
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentMembersHandler).ServeHTTP(rr, newAddMembersRequest(
		t, addMembersNewUser, addMembersNewUser,
	))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, []string{addMembersNewUser}, pushedUserIDs(t, captured))
}

func TestUpdateDepartmentMembers_UnknownDepartmentIsNotFound(t *testing.T) {
	otherDept, err := primitive.ObjectIDFromHex("507f1f77bcf86cd799439099")
	assert.NoError(t, err)

	community := communityWithDepartmentMembers(t)
	community.Details.Departments[0].ID = otherDept

	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)

	c := handlers.Community{DB: cdb}
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentMembersHandler).ServeHTTP(rr, newAddMembersRequest(t, addMembersNewUser))

	assert.Equal(t, http.StatusNotFound, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// The follow-up report: a private department where searching for a member in
// the Add Members picker found nobody, while the same person showed up fine in
// another department. They had a pending join request, which is an entry in the
// same members array — so the picker excluded them as "already in the
// department" while the department's own list, which shows approved members
// only, said they were not in it. Nobody could add them at all.

func TestUpdateDepartmentMembers_PendingMemberIsApprovedNotDuplicated(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).
		Return(communityWithDepartmentMembers(t, models.MemberStatus{
			UserID: addMembersExistingUser,
			Status: "pending",
		}), nil)

	var captured interface{}
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(2) }).
		Return(nil)

	c := handlers.Community{DB: cdb}
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentMembersHandler).ServeHTTP(rr, newAddMembersRequest(t, addMembersExistingUser))

	assert.Equal(t, http.StatusOK, rr.Code)

	// Approved in place: a $set on the existing entry, never a second row.
	set, ok := (captured).(bson.M)["$set"].(bson.M)
	assert.True(t, ok, "expected a $set, got %#v", captured)
	assert.Equal(t, "approved", set["community.departments.0.members.$[m0].status"])

	body := decodeAddMembersBody(t, rr)
	assert.Equal(t, []interface{}{addMembersExistingUser}, body["added"])
	assert.Equal(t, []interface{}{addMembersExistingUser}, body["approved"])
	assert.Empty(t, body["skipped"])
}

func TestUpdateDepartmentMembers_DeniedMemberCanBeAddedBack(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).
		Return(communityWithDepartmentMembers(t, models.MemberStatus{
			UserID: addMembersExistingUser,
			Status: "denied",
		}), nil)
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	c := handlers.Community{DB: cdb}
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentMembersHandler).ServeHTTP(rr, newAddMembersRequest(t, addMembersExistingUser))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, []interface{}{addMembersExistingUser}, decodeAddMembersBody(t, rr)["approved"])
}

// A pending request and a brand new member in the same click: one write each,
// and the new member is still pushed.
func TestUpdateDepartmentMembers_MixOfPendingAndNew(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).
		Return(communityWithDepartmentMembers(t, models.MemberStatus{
			UserID: addMembersExistingUser,
			Status: "pending",
		}), nil)

	var updates []interface{}
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { updates = append(updates, args.Get(2)) }).
		Return(nil)
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { updates = append(updates, args.Get(2)) }).
		Return(nil)

	c := handlers.Community{DB: cdb}
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.UpdateDepartmentMembersHandler).ServeHTTP(rr, newAddMembersRequest(
		t, addMembersExistingUser, addMembersNewUser,
	))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Len(t, updates, 2, "one write to approve, one to push")

	body := decodeAddMembersBody(t, rr)
	assert.Equal(t, []interface{}{addMembersExistingUser, addMembersNewUser}, body["added"])
	assert.Equal(t, []interface{}{addMembersExistingUser}, body["approved"])
}
