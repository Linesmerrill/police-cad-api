package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

const (
	bulkCommunityID  = "64b000000000000000000001"
	bulkDispatchDept = "64b000000000000000000002"
	bulkPoliceDept   = "64b000000000000000000003"
	bulkTenCodeID    = "64b000000000000000000004"
	bulkOwnerID      = "64b0000000000000000000a0"
	bulkDispatcherID = "64b0000000000000000000a1"
	bulkCivilianID   = "64b0000000000000000000a2"
	bulkUnitA        = "64b0000000000000000000b1"
	bulkUnitB        = "64b0000000000000000000b2"
	bulkUnitOther    = "64b0000000000000000000b3"
	bulkOutsider     = "64b0000000000000000000c1"
)

func oid(t *testing.T, hex string) primitive.ObjectID {
	t.Helper()
	id, err := primitive.ObjectIDFromHex(hex)
	assert.NoError(t, err)
	return id
}

// bulkCommunity has an approval-required Dispatch department whose only
// approved member is the dispatcher, a Police department, one ten-code, and
// three units with existing member entries.
func bulkCommunity(t *testing.T) *models.Community {
	t.Helper()
	return &models.Community{
		ID: oid(t, bulkCommunityID),
		Details: models.CommunityDetails{
			OwnerID: bulkOwnerID,
			Departments: []models.Department{
				{
					ID:               oid(t, bulkDispatchDept),
					Name:             "Dispatch",
					ApprovalRequired: true,
					Template:         models.Template{Name: "Dispatch"},
					Members: []models.MemberStatus{
						{UserID: bulkDispatcherID, Status: "approved"},
						{UserID: bulkCivilianID, Status: "pending"},
					},
				},
				{
					ID:       oid(t, bulkPoliceDept),
					Name:     "LSPD",
					Template: models.Template{Name: "Police"},
					Members:  []models.MemberStatus{{UserID: bulkCivilianID, Status: "approved"}},
				},
			},
			TenCodes: []models.TenCodes{{ID: oid(t, bulkTenCodeID), Code: "10-7", Description: "Out of Service"}},
			Members: map[string]models.MemberDetail{
				bulkUnitA:     {TenCodeID: "old", ActiveDepartmentID: bulkPoliceDept, ActiveDepartmentName: "LSPD", IsOnline: true},
				bulkUnitB:     {TenCodeID: "old", ActiveDepartmentID: bulkPoliceDept, ActiveDepartmentName: "LSPD"},
				bulkUnitOther: {TenCodeID: "old", ActiveDepartmentID: bulkPoliceDept},
			},
		},
	}
}

func bulkRequest(t *testing.T, actorID string, body interface{}) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	assert.NoError(t, err)
	req, err := http.NewRequest(http.MethodPut,
		"/api/v1/community/"+bulkCommunityID+"/members/tenCode/bulk", bytes.NewReader(raw))
	assert.NoError(t, err)
	req = mux.SetURLVars(req, map[string]string{"communityId": bulkCommunityID})
	if actorID != "" {
		req = req.WithContext(api.WithAuthenticatedUserID(req.Context(), actorID))
	}
	return req
}

// membersCursor is what the users query returns: the ids that are approved
// members of the community.
func membersCursor(t *testing.T, ids ...string) databases.MongoCursor {
	t.Helper()
	docs := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		docs = append(docs, bson.M{"_id": oid(t, id)})
	}
	cursor, err := databases.NewMongoCursorFromDocuments(docs)
	assert.NoError(t, err)
	return cursor
}

type bulkResponse struct {
	Results []struct {
		ID    string `json:"id"`
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	} `json:"results"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

func decodeBulk(t *testing.T, rr *httptest.ResponseRecorder) bulkResponse {
	t.Helper()
	var out bulkResponse
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out), rr.Body.String())
	return out
}

func serveBulk(c handlers.Community, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	http.HandlerFunc(c.BulkSetMemberTenCodeHandler).ServeHTTP(rr, req)
	return rr
}

func TestBulkSetMemberTenCode_DispatcherUpdatesOnlyListedMembers(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(bulkCommunity(t), nil)
	var updates []interface{}
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { updates = append(updates, args.Get(2)) }).
		Return(nil)
	udb := &mocks.UserDatabase{}
	udb.On("Find", mock.Anything, mock.Anything, mock.Anything).Return(membersCursor(t, bulkUnitA, bulkUnitB), nil)

	rr := serveBulk(handlers.Community{DB: cdb, UDB: udb}, bulkRequest(t, bulkDispatcherID, map[string]interface{}{
		"userIds":      []string{bulkUnitA, bulkUnitB, bulkUnitA},
		"departmentId": bulkDispatchDept,
		"tenCodeId":    bulkTenCodeID,
	}))

	assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	out := decodeBulk(t, rr)
	assert.Equal(t, 2, out.Succeeded, "duplicates are collapsed")
	assert.Equal(t, 0, out.Failed)

	// One targeted write, never the whole members map.
	if assert.Len(t, updates, 1) {
		set := updates[0].(bson.M)["$set"].(bson.M)
		assert.NotContains(t, set, "community.members")
		assert.Equal(t, bson.M{
			"community.members." + bulkUnitA + ".departmentID":         bulkDispatchDept,
			"community.members." + bulkUnitA + ".tenCodeID":            bulkTenCodeID,
			"community.members." + bulkUnitA + ".activeDepartmentId":   bulkPoliceDept,
			"community.members." + bulkUnitA + ".activeDepartmentName": "LSPD",
			"community.members." + bulkUnitB + ".departmentID":         bulkDispatchDept,
			"community.members." + bulkUnitB + ".tenCodeID":            bulkTenCodeID,
			"community.members." + bulkUnitB + ".activeDepartmentId":   bulkPoliceDept,
			"community.members." + bulkUnitB + ".activeDepartmentName": "LSPD",
		}, set, "only the listed units' status fields change; unit 'other' and isOnline are untouched")
	}
}

func TestBulkSetMemberTenCode_NonDispatcherIsForbidden(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(bulkCommunity(t), nil)
	udb := &mocks.UserDatabase{}

	// A police officer with only a pending Dispatch request is not a dispatcher.
	rr := serveBulk(handlers.Community{DB: cdb, UDB: udb}, bulkRequest(t, bulkCivilianID, map[string]interface{}{
		"userIds":   []string{bulkUnitA},
		"tenCodeId": bulkTenCodeID,
	}))

	assert.Equal(t, http.StatusForbidden, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestBulkSetMemberTenCode_RequiresAuthenticatedActor(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	// A forged ?userId= without the gateway secret is not an identity.
	req := bulkRequest(t, "", map[string]interface{}{"userIds": []string{bulkUnitA}, "tenCodeId": bulkTenCodeID})
	req.URL.RawQuery = "userId=" + bulkOwnerID

	rr := serveBulk(handlers.Community{DB: cdb}, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestBulkSetMemberTenCode_AdministratorAndOwnerPass(t *testing.T) {
	for _, tc := range []struct {
		name  string
		actor string
		roles []models.Role
	}{
		{name: "owner", actor: bulkOwnerID},
		{name: "administrator", actor: bulkCivilianID, roles: []models.Role{{
			Name:        "Staff",
			Members:     []string{bulkCivilianID},
			Permissions: []models.Permission{{Name: "administrator", Enabled: true}},
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			community := bulkCommunity(t)
			community.Details.Roles = tc.roles
			cdb := &mocks.CommunityDatabase{}
			cdb.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)
			cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)
			udb := &mocks.UserDatabase{}
			udb.On("Find", mock.Anything, mock.Anything, mock.Anything).Return(membersCursor(t, bulkUnitA), nil)

			rr := serveBulk(handlers.Community{DB: cdb, UDB: udb}, bulkRequest(t, tc.actor, map[string]interface{}{
				"userIds": []string{bulkUnitA}, "tenCodeId": bulkTenCodeID,
			}))

			assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			assert.Equal(t, 1, decodeBulk(t, rr).Succeeded)
		})
	}
}

// An open Dispatch department admits every community member, the same rule
// that lets them open its dashboard.
func TestBulkSetMemberTenCode_OpenDispatchDepartmentAdmitsCommunityMembers(t *testing.T) {
	community := bulkCommunity(t)
	community.Details.Departments[0].ApprovalRequired = false
	community.Details.Departments[0].Members = nil
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	udb := &mocks.UserDatabase{}
	udb.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(1), nil).Once()
	udb.On("Find", mock.Anything, mock.Anything, mock.Anything).Return(membersCursor(t, bulkUnitA), nil)

	rr := serveBulk(handlers.Community{DB: cdb, UDB: udb}, bulkRequest(t, bulkCivilianID, map[string]interface{}{
		"userIds": []string{bulkUnitA}, "tenCodeId": bulkTenCodeID,
	}))
	assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	// Someone outside the community still cannot.
	udb2 := &mocks.UserDatabase{}
	udb2.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(0), nil)
	rr = serveBulk(handlers.Community{DB: cdb, UDB: udb2}, bulkRequest(t, bulkOutsider, map[string]interface{}{
		"userIds": []string{bulkUnitA}, "tenCodeId": bulkTenCodeID,
	}))
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestBulkSetMemberTenCode_CapsAt100(t *testing.T) {
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = fmt.Sprintf("64b0000000000000000%05d", i)
	}
	cdb := &mocks.CommunityDatabase{}

	rr := serveBulk(handlers.Community{DB: cdb}, bulkRequest(t, bulkDispatcherID, map[string]interface{}{
		"userIds": ids, "tenCodeId": bulkTenCodeID,
	}))

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
}

func TestBulkSetMemberTenCode_ReportsNonMembersAndBadIDsAsFailed(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(bulkCommunity(t), nil)
	var updates []interface{}
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { updates = append(updates, args.Get(2)) }).
		Return(nil)
	udb := &mocks.UserDatabase{}
	udb.On("Find", mock.Anything, mock.Anything, mock.Anything).Return(membersCursor(t, bulkUnitA), nil)

	rr := serveBulk(handlers.Community{DB: cdb, UDB: udb}, bulkRequest(t, bulkDispatcherID, map[string]interface{}{
		"userIds": []string{bulkUnitA, bulkOutsider, "not-an-id"}, "tenCodeId": bulkTenCodeID,
	}))

	assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	out := decodeBulk(t, rr)
	assert.Equal(t, 1, out.Succeeded)
	assert.Equal(t, 2, out.Failed)
	if assert.Len(t, out.Results, 3) {
		assert.True(t, out.Results[0].OK)
		assert.Equal(t, bulkOutsider, out.Results[1].ID)
		assert.False(t, out.Results[1].OK)
		assert.Equal(t, "not a member of this community", out.Results[1].Error)
		assert.Equal(t, "invalid user id", out.Results[2].Error)
	}
	if assert.Len(t, updates, 1) {
		set := updates[0].(bson.M)["$set"].(bson.M)
		assert.Len(t, set, 4, "only the one member's four status fields are written")
		assert.NotContains(t, set, "community.members."+bulkOutsider+".tenCodeID")
	}
}

func TestBulkSetMemberTenCode_UnknownTenCodeIsRejected(t *testing.T) {
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(bulkCommunity(t), nil)

	rr := serveBulk(handlers.Community{DB: cdb}, bulkRequest(t, bulkDispatcherID, map[string]interface{}{
		"userIds": []string{bulkUnitA}, "tenCodeId": bulkUnitB,
	}))

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// A legacy community whose members map is null cannot take a dotted $set, so
// the handler seeds an empty map first.
func TestBulkSetMemberTenCode_SeedsNullMembersMap(t *testing.T) {
	community := bulkCommunity(t)
	community.Details.Members = nil
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)
	var updates []interface{}
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { updates = append(updates, args.Get(2)) }).
		Return(nil)
	udb := &mocks.UserDatabase{}
	udb.On("Find", mock.Anything, mock.Anything, mock.Anything).Return(membersCursor(t, bulkUnitA), nil)

	rr := serveBulk(handlers.Community{DB: cdb, UDB: udb}, bulkRequest(t, bulkDispatcherID, map[string]interface{}{
		"userIds": []string{bulkUnitA}, "tenCodeId": bulkTenCodeID,
	}))

	assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	if assert.Len(t, updates, 2) {
		assert.Equal(t, bson.M{"$set": bson.M{"community.members": bson.M{}}}, updates[0])
		set := updates[1].(bson.M)["$set"].(bson.M)
		assert.Equal(t, bulkTenCodeID, set["community.members."+bulkUnitA+".tenCodeID"])
	}
}
