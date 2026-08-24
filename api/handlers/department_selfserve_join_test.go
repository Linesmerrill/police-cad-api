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
	selfServeUserID       = "507f1f77bcf86cd799439001"
	selfServeCommunityID  = "507f1f77bcf86cd799439011"
	selfServeDepartmentID = "507f1f77bcf86cd799439022"
)

// communityWithDepartment builds a community holding a single department with the
// given approval requirement.
func communityWithDepartment(t *testing.T, approvalRequired bool) *models.Community {
	t.Helper()
	cID, err := primitive.ObjectIDFromHex(selfServeCommunityID)
	assert.NoError(t, err)
	dID, err := primitive.ObjectIDFromHex(selfServeDepartmentID)
	assert.NoError(t, err)

	return &models.Community{
		ID: cID,
		Details: models.CommunityDetails{
			Name: "Rockford RP",
			Departments: []models.Department{
				{
					ID:               dID,
					Name:             "State Police",
					ApprovalRequired: approvalRequired,
					Members:          []models.MemberStatus{},
				},
			},
		},
	}
}

func newSelfServeJoinRequest(t *testing.T) *http.Request {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"communityId":  selfServeCommunityID,
		"departmentId": selfServeDepartmentID,
	})
	assert.NoError(t, err)
	req, err := http.NewRequest(
		http.MethodPost,
		"/api/v1/user/"+selfServeUserID+"/pending-department-request",
		bytes.NewReader(raw),
	)
	assert.NoError(t, err)
	return mux.SetURLVars(req, map[string]string{"userId": selfServeUserID})
}

// capturedMemberStatus pulls the status written for our user out of the
// $set update the handler issues.
func capturedMemberStatus(t *testing.T, update interface{}) string {
	t.Helper()
	set, ok := update.(bson.M)["$set"].(bson.M)
	if !ok {
		t.Fatalf("update did not carry a $set document: %#v", update)
	}
	departments, ok := set["community.departments"].([]models.Department)
	if !ok {
		t.Fatalf("$set did not carry departments: %#v", set)
	}
	for _, dept := range departments {
		for _, member := range dept.Members {
			if member.UserID == selfServeUserID {
				return member.Status
			}
		}
	}
	t.Fatal("user was not written into any department's members")
	return ""
}

// A department that does not require approval has nothing to approve. Writing
// "pending" for it parked self-serve joins in a queue nobody works, while every
// other read path already treated the user as a member — so the member could use
// the department while their own entry said otherwise, and an admin saw a request
// they could not action.
func TestAddUserToPendingDepartment_NoApprovalRequiredJoinsImmediately(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).
		Return(communityWithDepartment(t, false), nil)

	var captured interface{}
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(2) }).
		Return(nil)

	u := handlers.User{CDB: cdb}
	rr := httptest.NewRecorder()
	http.HandlerFunc(u.AddUserToPendingDepartmentHandler).ServeHTTP(rr, newSelfServeJoinRequest(t))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "approved", capturedMemberStatus(t, captured))
	assert.Contains(t, rr.Body.String(), `"status": "approved"`)
}

func TestAddUserToPendingDepartment_ApprovalRequiredStaysPending(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).
		Return(communityWithDepartment(t, true), nil)

	var captured interface{}
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = args.Get(2) }).
		Return(nil)

	u := handlers.User{CDB: cdb}
	rr := httptest.NewRecorder()
	http.HandlerFunc(u.AddUserToPendingDepartmentHandler).ServeHTTP(rr, newSelfServeJoinRequest(t))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "pending", capturedMemberStatus(t, captured))
	assert.Contains(t, rr.Body.String(), `"status": "pending"`)
}

// Joining twice must still be refused regardless of the approval setting, so the
// fix cannot be used to stack duplicate member entries.
func TestAddUserToPendingDepartment_AlreadyApprovedIsRejected(t *testing.T) {
	community := communityWithDepartment(t, false)
	community.Details.Departments[0].Members = []models.MemberStatus{
		{UserID: selfServeUserID, Status: "approved"},
	}

	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)

	u := handlers.User{CDB: cdb}
	rr := httptest.NewRecorder()
	http.HandlerFunc(u.AddUserToPendingDepartmentHandler).ServeHTTP(rr, newSelfServeJoinRequest(t))

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}
