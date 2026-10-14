package handlers

import (
	"bytes"
	"context"
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
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// An invite code is a way into a community, so handing one out, reading the
// outstanding ones and revoking them are all governed by "manage members": the
// owner and administrators pass, everyone else is refused. Who is asking comes
// from a verified bearer token, or from a query-string id only when the request
// also carries the server-to-server secret, because an id a caller simply typed
// is not an identity and a check keyed off one would decide nothing.

// --- helpers ---------------------------------------------------------------

// stubUserResult is one answer from userDirectory.
type stubUserResult struct {
	user  *models.User
	found bool
}

func (s stubUserResult) Decode(v interface{}) error {
	if !s.found {
		return mongo.ErrNoDocuments
	}
	if p, ok := v.(*models.User); ok {
		*p = *s.user
	}
	return nil
}

// userDirectory answers every user lookup a handler makes from one map, so a
// test only has to name the users it cares about and the rest come back missing.
type userDirectory struct{ byID map[string]models.User }

func newUserDirectory(udb *mocks.UserDatabase) *userDirectory {
	d := &userDirectory{byID: map[string]models.User{}}
	udb.On("FindOne", mock.Anything, mock.Anything).Return(
		func(_ context.Context, filter interface{}, _ ...*options.FindOneOptions) databases.SingleResultHelper {
			doc, ok := filter.(bson.M)
			if !ok {
				return stubUserResult{}
			}
			oid, ok := doc["_id"].(primitive.ObjectID)
			if !ok {
				return stubUserResult{}
			}
			if u, ok := d.byID[oid.Hex()]; ok {
				return stubUserResult{user: &u, found: true}
			}
			return stubUserResult{}
		}).Maybe()
	return d
}

func (d *userDirectory) add(id string, user models.User) { d.byID[id] = user }

type inviteFixture struct {
	community   *models.Community
	communityID string
	owner       string
	idb         *mocks.InviteCodeDatabase
	cdb         *mocks.CommunityDatabase
	udb         *mocks.UserDatabase
	users       *userDirectory
	handler     Community
}

// newInviteFixture builds a community whose roles are supplied by the caller,
// wired into a Community handler with mocked collections.
func newInviteFixture(t *testing.T, roles ...models.Role) *inviteFixture {
	t.Helper()
	cID := primitive.NewObjectID()
	owner := primitive.NewObjectID().Hex()
	community := &models.Community{ID: cID, Details: models.CommunityDetails{
		Name:    "Lakeside RP",
		OwnerID: owner,
		Roles:   roles,
	}}

	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, bson.M{"_id": cID}).Return(community, nil).Maybe()
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	idb := &mocks.InviteCodeDatabase{}
	udb := &mocks.UserDatabase{}
	users := newUserDirectory(udb)
	users.add(owner, models.User{ID: owner, Details: models.UserDetails{Username: "owner"}})

	return &inviteFixture{
		community:   community,
		communityID: cID.Hex(),
		owner:       owner,
		idb:         idb,
		cdb:         cdb,
		udb:         udb,
		users:       users,
		handler:     Community{DB: cdb, IDB: idb, UDB: udb, ALDB: auditMock()},
	}
}

// inviteRequest builds a request carrying the given path variables. actorID,
// when set, is seeded as the token-verified caller the middleware would resolve.
func inviteRequest(t *testing.T, method, path, actorID string, vars map[string]string, body interface{}) *http.Request {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader([]byte(`{}`))
	}
	req := httptest.NewRequest(method, path, reader)
	if actorID != "" {
		req = asActor(req, actorID)
	}
	return mux.SetURLVars(req, vars)
}

// adminRole holds the given member with "administrator" enabled.
func adminRole(member string) models.Role { return roleWith("administrator", member) }

// unrelatedRole holds the given member with a permission that has nothing to do
// with membership.
func unrelatedRole(member string) models.Role { return roleWith("manage forms", member) }

func inviteCodeDoc(communityID, code string, maxUses, remainingUses, uses int, expiresAt *time.Time) models.InviteCode {
	return models.InviteCode{
		ID:            primitive.NewObjectID(),
		Code:          code,
		CommunityID:   communityID,
		MaxUses:       maxUses,
		RemainingUses: remainingUses,
		Uses:          uses,
		ExpiresAt:     expiresAt,
		CreatedBy:     primitive.NewObjectID().Hex(),
		CreatedAt:     time.Now(),
	}
}

// --- create ----------------------------------------------------------------

// stubCreate lets a create request reach its writes: no code of that name
// exists yet, and the insert succeeds.
func stubCreate(f *inviteFixture) {
	f.idb.On("FindOne", mock.Anything, mock.Anything).Return(nil, mongo.ErrNoDocuments).Maybe()
	f.idb.On("InsertOne", mock.Anything, mock.Anything).Return(&mocks.InsertOneResultHelper{}, nil).Maybe()
}

func createRequest(t *testing.T, f *inviteFixture, actorID string, body interface{}) *http.Request {
	return inviteRequest(t, http.MethodPost,
		"/api/v1/community/"+f.communityID+"/add-invite-code", actorID,
		map[string]string{"communityId": f.communityID}, body)
}

func newCodeBody() map[string]interface{} {
	return map[string]interface{}{"code": "LAKESIDE1", "maxUses": 5}
}

func TestAddInviteCode_RefusesAnUnidentifiedCaller(t *testing.T) {
	f := newInviteFixture(t)
	stubCreate(f)

	rr := httptest.NewRecorder()
	f.handler.AddInviteCodeHandler(rr, createRequest(t, f, "", newCodeBody()))

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	f.idb.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)
	f.cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestAddInviteCode_RefusesACallerWhoCannotManageMembers(t *testing.T) {
	outsider := primitive.NewObjectID().Hex()
	f := newInviteFixture(t, unrelatedRole(outsider))
	stubCreate(f)

	rr := httptest.NewRecorder()
	f.handler.AddInviteCodeHandler(rr, createRequest(t, f, outsider, newCodeBody()))

	assert.Equal(t, http.StatusForbidden, rr.Code)
	f.idb.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)
	f.cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// The website's browser calls carry a plain ?userId=, which the caller chooses.
// Without the server-to-server secret alongside it, there is nobody identified.
func TestAddInviteCode_QueryUserIDNeedsGatewaySecret(t *testing.T) {
	t.Setenv(apiGatewayKeyEnv, "s3cret")
	f := newInviteFixture(t)
	stubCreate(f)

	req := inviteRequest(t, http.MethodPost,
		"/api/v1/community/"+f.communityID+"/add-invite-code?userId="+f.owner, "",
		map[string]string{"communityId": f.communityID}, newCodeBody())
	rr := httptest.NewRecorder()
	f.handler.AddInviteCodeHandler(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	f.idb.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)
}

func TestAddInviteCode_QueryUserIDWithGatewaySecretAuthorizes(t *testing.T) {
	t.Setenv(apiGatewayKeyEnv, "s3cret")
	f := newInviteFixture(t)
	stubCreate(f)

	req := inviteRequest(t, http.MethodPost,
		"/api/v1/community/"+f.communityID+"/add-invite-code?userId="+f.owner, "",
		map[string]string{"communityId": f.communityID}, newCodeBody())
	req.Header.Set(apiGatewayHeader, "s3cret")
	rr := httptest.NewRecorder()
	f.handler.AddInviteCodeHandler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	f.idb.AssertCalled(t, "InsertOne", mock.Anything, mock.Anything)
}

func TestAddInviteCode_AllowsTheOwner(t *testing.T) {
	f := newInviteFixture(t)
	stubCreate(f)

	rr := httptest.NewRecorder()
	f.handler.AddInviteCodeHandler(rr, createRequest(t, f, f.owner, newCodeBody()))

	assert.Equal(t, http.StatusOK, rr.Code)
	f.idb.AssertCalled(t, "InsertOne", mock.Anything, mock.Anything)
}

func TestAddInviteCode_AllowsAnAdministrator(t *testing.T) {
	admin := primitive.NewObjectID().Hex()
	f := newInviteFixture(t, adminRole(admin))
	f.users.add(admin, models.User{ID: admin, Details: models.UserDetails{Username: "admin"}})
	stubCreate(f)

	rr := httptest.NewRecorder()
	f.handler.AddInviteCodeHandler(rr, createRequest(t, f, admin, newCodeBody()))

	assert.Equal(t, http.StatusOK, rr.Code)
	f.idb.AssertCalled(t, "InsertOne", mock.Anything, mock.Anything)
}

// The stored creator is whoever the request proved it was, so the code's
// history says who really minted it.
func TestAddInviteCode_RecordsTheCallerAsCreator(t *testing.T) {
	admin := primitive.NewObjectID().Hex()
	someoneElse := primitive.NewObjectID().Hex()
	f := newInviteFixture(t, adminRole(admin))
	f.users.add(admin, models.User{ID: admin, Details: models.UserDetails{Username: "admin"}})

	var inserted models.InviteCode
	f.idb.On("FindOne", mock.Anything, mock.Anything).Return(nil, mongo.ErrNoDocuments).Maybe()
	f.idb.On("InsertOne", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { inserted = args.Get(1).(models.InviteCode) }).
		Return(&mocks.InsertOneResultHelper{}, nil)

	body := newCodeBody()
	body["createdBy"] = someoneElse
	body["userId"] = someoneElse

	rr := httptest.NewRecorder()
	f.handler.AddInviteCodeHandler(rr, createRequest(t, f, admin, body))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, admin, inserted.CreatedBy)
	assert.Equal(t, "LAKESIDE1", inserted.Code)
	assert.Equal(t, f.communityID, inserted.CommunityID)
}

// Released mobile builds and the website still send the old fields. They must
// be ignored, not rejected.
func TestAddInviteCode_AcceptsTheOlderClientBody(t *testing.T) {
	f := newInviteFixture(t)
	stubCreate(f)

	body := map[string]interface{}{
		"code":          "LAKESIDE2",
		"maxUses":       0,
		"createdBy":     primitive.NewObjectID().Hex(),
		"remainingUses": 99,
		"createdAt":     time.Now().Format(time.RFC3339),
	}
	rr := httptest.NewRecorder()
	f.handler.AddInviteCodeHandler(rr, createRequest(t, f, f.owner, body))

	assert.Equal(t, http.StatusOK, rr.Code)
}

// --- list ------------------------------------------------------------------

func listRequest(t *testing.T, f *inviteFixture, actorID string) *http.Request {
	return inviteRequest(t, http.MethodGet,
		"/api/v2/community/"+f.communityID+"/invite-codes?page=1&limit=10", actorID,
		map[string]string{"communityId": f.communityID}, nil)
}

func stubList(f *inviteFixture, codes ...models.InviteCode) {
	f.idb.On("CountDocuments", mock.Anything, mock.Anything).Return(int64(len(codes)), nil).Maybe()
	f.idb.On("Find", mock.Anything, mock.Anything, mock.Anything).Return(codes, nil).Maybe()
}

func TestGetCommunityInviteCodes_RefusesAnUnidentifiedCaller(t *testing.T) {
	f := newInviteFixture(t)
	stubList(f, inviteCodeDoc(f.communityID, "LAKESIDE1", 5, 5, 0, nil))

	rr := httptest.NewRecorder()
	f.handler.GetCommunityInviteCodesHandlerV2(rr, listRequest(t, f, ""))

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	f.idb.AssertNotCalled(t, "Find", mock.Anything, mock.Anything, mock.Anything)
	assert.NotContains(t, rr.Body.String(), "LAKESIDE1")
}

func TestGetCommunityInviteCodes_RefusesACallerWhoCannotManageMembers(t *testing.T) {
	outsider := primitive.NewObjectID().Hex()
	f := newInviteFixture(t, unrelatedRole(outsider))
	stubList(f, inviteCodeDoc(f.communityID, "LAKESIDE1", 5, 5, 0, nil))

	rr := httptest.NewRecorder()
	f.handler.GetCommunityInviteCodesHandlerV2(rr, listRequest(t, f, outsider))

	assert.Equal(t, http.StatusForbidden, rr.Code)
	f.idb.AssertNotCalled(t, "Find", mock.Anything, mock.Anything, mock.Anything)
	assert.NotContains(t, rr.Body.String(), "LAKESIDE1")
}

func TestGetCommunityInviteCodes_QueryUserIDNeedsGatewaySecret(t *testing.T) {
	t.Setenv(apiGatewayKeyEnv, "s3cret")
	f := newInviteFixture(t)
	stubList(f, inviteCodeDoc(f.communityID, "LAKESIDE1", 5, 5, 0, nil))

	req := inviteRequest(t, http.MethodGet,
		"/api/v2/community/"+f.communityID+"/invite-codes?userId="+f.owner, "",
		map[string]string{"communityId": f.communityID}, nil)
	rr := httptest.NewRecorder()
	f.handler.GetCommunityInviteCodesHandlerV2(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.NotContains(t, rr.Body.String(), "LAKESIDE1")
}

func TestGetCommunityInviteCodes_AllowsTheOwner(t *testing.T) {
	f := newInviteFixture(t)
	stubList(f, inviteCodeDoc(f.communityID, "LAKESIDE1", 5, 5, 0, nil))

	rr := httptest.NewRecorder()
	f.handler.GetCommunityInviteCodesHandlerV2(rr, listRequest(t, f, f.owner))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "LAKESIDE1")
}

func TestGetCommunityInviteCodes_AllowsAnAdministrator(t *testing.T) {
	admin := primitive.NewObjectID().Hex()
	f := newInviteFixture(t, adminRole(admin))
	stubList(f, inviteCodeDoc(f.communityID, "LAKESIDE1", 5, 5, 0, nil))

	rr := httptest.NewRecorder()
	f.handler.GetCommunityInviteCodesHandlerV2(rr, listRequest(t, f, admin))

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "LAKESIDE1")
}

// The list says who made each code. A name answers that; an address is the
// person's own and belongs elsewhere.
func TestGetCommunityInviteCodes_NamesTheCreatorWithoutTheirEmail(t *testing.T) {
	f := newInviteFixture(t)
	creator := primitive.NewObjectID().Hex()
	code := inviteCodeDoc(f.communityID, "LAKESIDE1", 5, 5, 0, nil)
	code.CreatedBy = creator
	stubList(f, code)
	f.users.add(creator, models.User{ID: creator, Details: models.UserDetails{
		Username: "dispatcher",
		Email:    "dispatcher@example.com",
	}})

	rr := httptest.NewRecorder()
	f.handler.GetCommunityInviteCodesHandlerV2(rr, listRequest(t, f, f.owner))

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, "dispatcher")
	assert.NotContains(t, body, "dispatcher@example.com")
	assert.NotContains(t, strings.ToLower(body), `"email"`)
}

// --- delete ----------------------------------------------------------------

func deleteRequest(t *testing.T, f *inviteFixture, actorID, inviteCodeID string) *http.Request {
	return inviteRequest(t, http.MethodDelete,
		"/api/v1/invite-code/"+inviteCodeID, actorID,
		map[string]string{"inviteCodeId": inviteCodeID}, nil)
}

// stubDelete makes one code findable by id and lets the delete through.
func stubDelete(f *inviteFixture) string {
	code := inviteCodeDoc(f.communityID, "LAKESIDE1", 5, 5, 0, nil)
	f.idb.On("FindOne", mock.Anything, bson.M{"_id": code.ID}).Return(&code, nil).Maybe()
	f.idb.On("DeleteOne", mock.Anything, mock.Anything).Return(nil).Maybe()
	return code.ID.Hex()
}

func TestDeleteInviteCode_RefusesAnUnidentifiedCaller(t *testing.T) {
	f := newInviteFixture(t)
	id := stubDelete(f)

	rr := httptest.NewRecorder()
	f.handler.DeleteInviteCodeHandler(rr, deleteRequest(t, f, "", id))

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	f.idb.AssertNotCalled(t, "DeleteOne", mock.Anything, mock.Anything)
	f.cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestDeleteInviteCode_RefusesACallerWhoCannotManageMembers(t *testing.T) {
	outsider := primitive.NewObjectID().Hex()
	f := newInviteFixture(t, unrelatedRole(outsider))
	id := stubDelete(f)

	rr := httptest.NewRecorder()
	f.handler.DeleteInviteCodeHandler(rr, deleteRequest(t, f, outsider, id))

	assert.Equal(t, http.StatusForbidden, rr.Code)
	f.idb.AssertNotCalled(t, "DeleteOne", mock.Anything, mock.Anything)
	f.cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestDeleteInviteCode_QueryUserIDNeedsGatewaySecret(t *testing.T) {
	t.Setenv(apiGatewayKeyEnv, "s3cret")
	f := newInviteFixture(t)
	id := stubDelete(f)

	req := inviteRequest(t, http.MethodDelete,
		"/api/v1/invite-code/"+id+"?userId="+f.owner, "",
		map[string]string{"inviteCodeId": id}, nil)
	rr := httptest.NewRecorder()
	f.handler.DeleteInviteCodeHandler(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	f.idb.AssertNotCalled(t, "DeleteOne", mock.Anything, mock.Anything)
}

func TestDeleteInviteCode_AllowsTheOwner(t *testing.T) {
	f := newInviteFixture(t)
	id := stubDelete(f)

	rr := httptest.NewRecorder()
	f.handler.DeleteInviteCodeHandler(rr, deleteRequest(t, f, f.owner, id))

	assert.Equal(t, http.StatusOK, rr.Code)
	f.idb.AssertCalled(t, "DeleteOne", mock.Anything, mock.Anything)
}

func TestDeleteInviteCode_AllowsAnAdministrator(t *testing.T) {
	admin := primitive.NewObjectID().Hex()
	f := newInviteFixture(t, adminRole(admin))
	f.users.add(admin, models.User{ID: admin, Details: models.UserDetails{Username: "admin"}})
	id := stubDelete(f)

	rr := httptest.NewRecorder()
	f.handler.DeleteInviteCodeHandler(rr, deleteRequest(t, f, admin, id))

	assert.Equal(t, http.StatusOK, rr.Code)
	f.idb.AssertCalled(t, "DeleteOne", mock.Anything, mock.Anything)
}

// A code that is not there cannot be authorized against any community.
func TestDeleteInviteCode_UnknownCodeIsNotFound(t *testing.T) {
	f := newInviteFixture(t)
	missing := primitive.NewObjectID().Hex()
	f.idb.On("FindOne", mock.Anything, mock.Anything).Return(nil, mongo.ErrNoDocuments).Maybe()
	f.idb.On("DeleteOne", mock.Anything, mock.Anything).Return(nil).Maybe()

	rr := httptest.NewRecorder()
	f.handler.DeleteInviteCodeHandler(rr, deleteRequest(t, f, f.owner, missing))

	assert.Equal(t, http.StatusNotFound, rr.Code)
	f.idb.AssertNotCalled(t, "DeleteOne", mock.Anything, mock.Anything)
}

// --- delete expired --------------------------------------------------------

func deleteExpiredRequest(t *testing.T, f *inviteFixture, actorID string) *http.Request {
	return inviteRequest(t, http.MethodDelete,
		"/api/v1/community/"+f.communityID+"/invite-codes/expired", actorID,
		map[string]string{"communityId": f.communityID}, nil)
}

func stubDeleteExpired(f *inviteFixture) {
	past := time.Now().Add(-24 * time.Hour)
	expired := inviteCodeDoc(f.communityID, "OLDCODE1", 5, 5, 0, &past)
	f.idb.On("Find", mock.Anything, mock.Anything, mock.Anything).
		Return([]models.InviteCode{expired}, nil).Maybe()
	f.idb.On("DeleteMany", mock.Anything, mock.Anything).Return(int64(1), nil).Maybe()
}

func TestDeleteExpiredInviteCodes_RefusesAnUnidentifiedCaller(t *testing.T) {
	f := newInviteFixture(t)
	stubDeleteExpired(f)

	rr := httptest.NewRecorder()
	f.handler.DeleteExpiredInviteCodesHandler(rr, deleteExpiredRequest(t, f, ""))

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	f.idb.AssertNotCalled(t, "DeleteMany", mock.Anything, mock.Anything)
	f.cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestDeleteExpiredInviteCodes_RefusesACallerWhoCannotManageMembers(t *testing.T) {
	outsider := primitive.NewObjectID().Hex()
	f := newInviteFixture(t, unrelatedRole(outsider))
	stubDeleteExpired(f)

	rr := httptest.NewRecorder()
	f.handler.DeleteExpiredInviteCodesHandler(rr, deleteExpiredRequest(t, f, outsider))

	assert.Equal(t, http.StatusForbidden, rr.Code)
	f.idb.AssertNotCalled(t, "DeleteMany", mock.Anything, mock.Anything)
	f.cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestDeleteExpiredInviteCodes_QueryUserIDNeedsGatewaySecret(t *testing.T) {
	t.Setenv(apiGatewayKeyEnv, "s3cret")
	f := newInviteFixture(t)
	stubDeleteExpired(f)

	req := inviteRequest(t, http.MethodDelete,
		"/api/v1/community/"+f.communityID+"/invite-codes/expired?userId="+f.owner, "",
		map[string]string{"communityId": f.communityID}, nil)
	rr := httptest.NewRecorder()
	f.handler.DeleteExpiredInviteCodesHandler(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	f.idb.AssertNotCalled(t, "DeleteMany", mock.Anything, mock.Anything)
}

func TestDeleteExpiredInviteCodes_AllowsTheOwner(t *testing.T) {
	f := newInviteFixture(t)
	stubDeleteExpired(f)

	rr := httptest.NewRecorder()
	f.handler.DeleteExpiredInviteCodesHandler(rr, deleteExpiredRequest(t, f, f.owner))

	assert.Equal(t, http.StatusOK, rr.Code)
	f.idb.AssertCalled(t, "DeleteMany", mock.Anything, mock.Anything)
}

func TestDeleteExpiredInviteCodes_AllowsAnAdministrator(t *testing.T) {
	admin := primitive.NewObjectID().Hex()
	f := newInviteFixture(t, adminRole(admin))
	stubDeleteExpired(f)

	rr := httptest.NewRecorder()
	f.handler.DeleteExpiredInviteCodesHandler(rr, deleteExpiredRequest(t, f, admin))

	assert.Equal(t, http.StatusOK, rr.Code)
	f.idb.AssertCalled(t, "DeleteMany", mock.Anything, mock.Anything)
}

// --- preview ---------------------------------------------------------------

// A preview has to agree with what joining will do, so a code that is used up
// is reported as used up rather than as a working invitation.
func TestGetInviteCode_RefusesACodeWithNoUsesLeft(t *testing.T) {
	for name, code := range map[string]models.InviteCode{
		"counted down to zero": {Code: "SPENT1", MaxUses: 5, RemainingUses: 0, Uses: 5},
		"older counter full":   {Code: "SPENT2", MaxUses: 3, RemainingUses: 0, Uses: 3},
	} {
		t.Run(name, func(t *testing.T) {
			cID := primitive.NewObjectID()
			code.CommunityID = cID.Hex()
			idb := &mocks.InviteCodeDatabase{}
			idb.On("FindOne", mock.Anything, mock.Anything).Return(&code, nil)
			cdb := &mocks.CommunityDatabase{}
			cdb.On("FindOneIncludingPending", mock.Anything, mock.Anything).
				Return(&models.Community{ID: cID, Details: models.CommunityDetails{Name: "Lakeside RP"}}, nil).Maybe()
			c := Community{DB: cdb, IDB: idb, UDB: &mocks.UserDatabase{}, ALDB: auditMock()}

			req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/v1/community/invite/"+code.Code, nil),
				map[string]string{"invite_code": code.Code})
			rr := httptest.NewRecorder()
			c.GetInviteCodeHandler(rr, req)

			assert.Equal(t, http.StatusBadRequest, rr.Code)
			assert.NotContains(t, rr.Body.String(), "Lakeside RP")
		})
	}
}

func TestGetInviteCode_PreviewsACodeWithUsesLeft(t *testing.T) {
	for name, code := range map[string]models.InviteCode{
		"uses remaining":    {Code: "GOOD1", MaxUses: 5, RemainingUses: 2, Uses: 3},
		"unlimited":         {Code: "GOOD2", MaxUses: 0, RemainingUses: -1},
		"older counter low": {Code: "GOOD3", MaxUses: 3, RemainingUses: 0, Uses: 1},
	} {
		t.Run(name, func(t *testing.T) {
			cID := primitive.NewObjectID()
			code.CommunityID = cID.Hex()
			idb := &mocks.InviteCodeDatabase{}
			idb.On("FindOne", mock.Anything, mock.Anything).Return(&code, nil)
			cdb := &mocks.CommunityDatabase{}
			cdb.On("FindOneIncludingPending", mock.Anything, mock.Anything).
				Return(&models.Community{ID: cID, Details: models.CommunityDetails{Name: "Lakeside RP"}}, nil)
			c := Community{DB: cdb, IDB: idb, UDB: &mocks.UserDatabase{}, ALDB: auditMock()}

			req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/v1/community/invite/"+code.Code, nil),
				map[string]string{"invite_code": code.Code})
			rr := httptest.NewRecorder()
			c.GetInviteCodeHandler(rr, req)

			assert.Equal(t, http.StatusOK, rr.Code)
			assert.Contains(t, rr.Body.String(), "Lakeside RP")
		})
	}
}

func TestInviteCodeHasUsesLeft(t *testing.T) {
	assert.False(t, inviteCodeHasUsesLeft(nil))
	assert.True(t, inviteCodeHasUsesLeft(&models.InviteCode{RemainingUses: -1}))
	assert.True(t, inviteCodeHasUsesLeft(&models.InviteCode{MaxUses: 5, RemainingUses: 1}))
	assert.False(t, inviteCodeHasUsesLeft(&models.InviteCode{MaxUses: 5, RemainingUses: 0, Uses: 5}))
	assert.True(t, inviteCodeHasUsesLeft(&models.InviteCode{MaxUses: 0}), "older unlimited code")
	assert.True(t, inviteCodeHasUsesLeft(&models.InviteCode{MaxUses: 4, Uses: 2}), "older counted code")
	assert.False(t, inviteCodeHasUsesLeft(&models.InviteCode{MaxUses: 4, Uses: 4}), "older counted code, full")
}

// --- the code a prospective joiner can read --------------------------------

// Anyone holding the link can ask about it, so the answer carries only what is
// needed to decide whether to use it.
func TestInviteCodeByCode_TellsAJoinerOnlyWhatTheyNeed(t *testing.T) {
	cID := primitive.NewObjectID()
	creator := primitive.NewObjectID().Hex()
	code := inviteCodeDoc(cID.Hex(), "LAKESIDE1", 5, 2, 3, nil)
	code.CreatedBy = creator

	idb := &mocks.InviteCodeDatabase{}
	idb.On("FindOne", mock.Anything, bson.M{"code": "LAKESIDE1"}).Return(&code, nil)
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, bson.M{"_id": cID}).
		Return(&models.Community{ID: cID, Details: models.CommunityDetails{Name: "Lakeside RP"}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invite?code=LAKESIDE1", nil)
	rr := httptest.NewRecorder()
	InviteCode{DB: idb, CDB: cdb}.InviteCodeByCodeHandler(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, "Lakeside RP", body["communityName"])
	assert.Equal(t, true, body["usable"])
	for _, internal := range []string{"createdBy", "maxUses", "remainingUses", "uses"} {
		assert.NotContains(t, body, internal)
	}
	assert.NotContains(t, rr.Body.String(), creator)
}

func TestInviteCodeByCode_SaysWhenACodeIsSpent(t *testing.T) {
	cID := primitive.NewObjectID()
	code := inviteCodeDoc(cID.Hex(), "SPENT1", 5, 0, 5, nil)

	idb := &mocks.InviteCodeDatabase{}
	idb.On("FindOne", mock.Anything, mock.Anything).Return(&code, nil)
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).
		Return(&models.Community{ID: cID, Details: models.CommunityDetails{Name: "Lakeside RP"}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invite?code=SPENT1", nil)
	rr := httptest.NewRecorder()
	InviteCode{DB: idb, CDB: cdb}.InviteCodeByCodeHandler(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, false, body["usable"])
}

// --- join ------------------------------------------------------------------

type joinFixture struct {
	communityID string
	community   *models.Community
	invite      models.InviteCode
	idb         *mocks.InviteCodeDatabase
	cdb         *mocks.CommunityDatabase
	udb         *mocks.UserDatabase
	users       *userDirectory
	userUpdates *[]interface{}
	handler     Community
}

// newJoinFixture wires up a joinable community holding a usable code, with the
// joining user's stored memberships and the community's ban list supplied by
// the caller.
func newJoinFixture(t *testing.T, joiner string, memberships []models.UserCommunity, banList []string) *joinFixture {
	t.Helper()
	cID := primitive.NewObjectID()
	community := &models.Community{ID: cID, Details: models.CommunityDetails{
		Name:    "Lakeside RP",
		OwnerID: primitive.NewObjectID().Hex(),
		BanList: banList,
	}}
	invite := inviteCodeDoc(cID.Hex(), "LAKESIDE1", 5, 5, 0, nil)

	idb := &mocks.InviteCodeDatabase{}
	idb.On("FindOne", mock.Anything, mock.Anything).Return(&invite, nil).Maybe()
	idb.On("FindOneAndUpdate", mock.Anything, mock.Anything, mock.Anything).
		Return(mongo.NewSingleResultFromDocument(invite, nil, nil)).Maybe()

	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOneIncludingPending", mock.Anything, mock.Anything).Return(community, nil).Maybe()
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(community, nil).Maybe()
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	udb := &mocks.UserDatabase{}
	users := newUserDirectory(udb)
	users.add(joiner, models.User{
		ID:      joiner,
		Details: models.UserDetails{Username: "newcomer", Communities: memberships},
	})
	var updates []interface{}
	udb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { updates = append(updates, args.Get(1)) }).
		Return(&mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil).Maybe()

	return &joinFixture{
		communityID: cID.Hex(),
		community:   community,
		invite:      invite,
		idb:         idb,
		cdb:         cdb,
		udb:         udb,
		users:       users,
		userUpdates: &updates,
		handler:     Community{DB: cdb, IDB: idb, UDB: udb, ALDB: auditMock()},
	}
}

func joinRequest(t *testing.T, actorID string, body map[string]interface{}) *http.Request {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/community/join", bytes.NewReader(b))
	if actorID != "" {
		req = asActor(req, actorID)
	}
	return req
}

func TestJoinCommunity_RefusesAnUnidentifiedCaller(t *testing.T) {
	joiner := primitive.NewObjectID().Hex()
	f := newJoinFixture(t, joiner, nil, nil)

	rr := httptest.NewRecorder()
	f.handler.JoinCommunityHandler(rr, joinRequest(t, "", map[string]interface{}{
		"inviteCode": "LAKESIDE1",
		"userId":     joiner,
	}))

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	// Nobody was identified, so no membership was written and no use of the
	// code was spent.
	f.udb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	f.idb.AssertNotCalled(t, "FindOneAndUpdate", mock.Anything, mock.Anything, mock.Anything)
}

// A code is redeemed for the account the request proved it was, so a body
// naming someone else changes nothing.
func TestJoinCommunity_JoinsTheAuthenticatedAccount(t *testing.T) {
	joiner := primitive.NewObjectID().Hex()
	someoneElse := primitive.NewObjectID().Hex()
	f := newJoinFixture(t, joiner, nil, nil)

	rr := httptest.NewRecorder()
	f.handler.JoinCommunityHandler(rr, joinRequest(t, joiner, map[string]interface{}{
		"inviteCode": "LAKESIDE1",
		"userId":     someoneElse,
	}))

	require.Equal(t, http.StatusOK, rr.Code)
	joinerOID, err := primitive.ObjectIDFromHex(joiner)
	require.NoError(t, err)
	otherOID, err := primitive.ObjectIDFromHex(someoneElse)
	require.NoError(t, err)
	require.NotEmpty(t, *f.userUpdates)
	for _, filter := range *f.userUpdates {
		doc, ok := filter.(bson.M)
		require.True(t, ok)
		assert.Equal(t, joinerOID, doc["_id"])
		assert.NotEqual(t, otherOID, doc["_id"])
	}
}

func TestJoinCommunity_RefusesABannedNewcomer(t *testing.T) {
	joiner := primitive.NewObjectID().Hex()
	f := newJoinFixture(t, joiner, nil, []string{joiner})

	rr := httptest.NewRecorder()
	f.handler.JoinCommunityHandler(rr, joinRequest(t, joiner, map[string]interface{}{
		"inviteCode": "LAKESIDE1",
	}))

	assert.Equal(t, http.StatusForbidden, rr.Code)
	f.udb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	f.cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// A ban holds for someone who already has a membership row too: a code is not
// a way back in.
func TestJoinCommunity_RefusesABannedExistingMember(t *testing.T) {
	joiner := primitive.NewObjectID().Hex()
	f := newJoinFixture(t, joiner, nil, []string{joiner})
	// Give the joiner a membership row for this community so the existing-member
	// path is the one under test.
	f.users.add(joiner, models.User{
		ID: joiner,
		Details: models.UserDetails{Username: "newcomer", Communities: []models.UserCommunity{{
			ID:          primitive.NewObjectID().Hex(),
			CommunityID: f.communityID,
			Status:      "declined",
		}}},
	})

	rr := httptest.NewRecorder()
	f.handler.JoinCommunityHandler(rr, joinRequest(t, joiner, map[string]interface{}{
		"inviteCode": "LAKESIDE1",
	}))

	assert.Equal(t, http.StatusForbidden, rr.Code)
	f.udb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	f.cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// The server-to-server path is the website backend speaking for its logged-in
// session: the id it supplies counts only alongside the shared secret.
func TestJoinCommunity_SuppliedUserIDNeedsGatewaySecret(t *testing.T) {
	t.Setenv(apiGatewayKeyEnv, "s3cret")
	joiner := primitive.NewObjectID().Hex()
	f := newJoinFixture(t, joiner, nil, nil)

	rr := httptest.NewRecorder()
	f.handler.JoinCommunityHandler(rr, joinRequest(t, "", map[string]interface{}{
		"inviteCode": "LAKESIDE1",
		"userId":     joiner,
	}))
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	f.udb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)

	signed := joinRequest(t, "", map[string]interface{}{
		"inviteCode": "LAKESIDE1",
		"userId":     joiner,
	})
	signed.Header.Set(apiGatewayHeader, "s3cret")
	rr = httptest.NewRecorder()
	f.handler.JoinCommunityHandler(rr, signed)
	assert.Equal(t, http.StatusOK, rr.Code)
}
