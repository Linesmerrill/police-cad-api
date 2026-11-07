package handlers

// Tests for the admin console's "Send reset email". The invariant that matters
// is the one the original bug broke: the token inside the emailed link must
// hash to the value stored on the account, and the link must point at the
// route the website actually serves. Both are asserted against the real
// handler output rather than restated as constants.

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

const testResetWebBase = "https://example.test"

// resetTestRig is one handler plus the mocks it talks to, and the link the
// handler tried to email.
type resetTestRig struct {
	handler  Admin
	udb      *mocks.UserDatabase
	adb      *mocks.AdminDatabase
	aadb     *mocks.AdminActivityDatabase
	updates  []bson.M
	sentTo   string
	sentLink string
}

// newResetRig wires a member, a staff account and a stubbed send.
func newResetRig(t *testing.T, memberID primitive.ObjectID, adminDoc *models.AdminUser, sendErr error) *resetTestRig {
	t.Helper()
	t.Setenv(apiGatewayKeyEnv, testGatewayKey)
	t.Setenv("PUBLIC_WEB_BASE_URL", testResetWebBase)

	rig := &resetTestRig{
		udb:  &mocks.UserDatabase{},
		adb:  &mocks.AdminDatabase{},
		aadb: &mocks.AdminActivityDatabase{},
	}

	member := &models.User{
		ID:      memberID.Hex(),
		Details: models.UserDetails{Email: "member@example.test"},
	}
	result := &mocks.SingleResultHelper{}
	result.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		if ptr, ok := args.Get(0).(*models.User); ok {
			*ptr = *member
		}
	}).Return(nil)
	rig.udb.On("FindOne", mock.Anything, mock.Anything).Return(result)

	// Record every $set so the test can assert on what was written, and when.
	rig.udb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			if update, ok := args.Get(2).(bson.M); ok {
				if set, ok := update["$set"].(bson.M); ok {
					rig.updates = append(rig.updates, set)
				}
			}
		}).Return(&mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil)

	if adminDoc != nil {
		rig.adb.On("FindOne", mock.Anything, mock.Anything).Return(adminDoc, nil)
	} else {
		rig.adb.On("FindOne", mock.Anything, mock.Anything).
			Return((*models.AdminUser)(nil), mongo.ErrNoDocuments)
	}
	rig.aadb.On("InsertOne", mock.Anything, mock.Anything).
		Return(&mocks.InsertOneResultHelper{}, nil)

	original := sendUserResetEmailFn
	t.Cleanup(func() { sendUserResetEmailFn = original })
	sendUserResetEmailFn = func(to, link string) error {
		rig.sentTo = to
		rig.sentLink = link
		return sendErr
	}

	rig.handler = Admin{UDB: rig.udb, ADB: rig.adb, AADB: rig.aadb}
	return rig
}

// call runs the handler. withSecret controls whether the request looks like it
// came from the website backend.
func (rig *resetTestRig) call(t *testing.T, userID, body string, withSecret bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/admin/users/"+userID+"/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if withSecret {
		req.Header.Set(apiGatewayHeader, testGatewayKey)
	}
	req = mux.SetURLVars(req, map[string]string{"id": userID})
	rr := httptest.NewRecorder()
	http.HandlerFunc(rig.handler.AdminUserResetPasswordHandler).ServeHTTP(rr, req)
	return rr
}

func staffAdmin() *models.AdminUser {
	return &models.AdminUser{
		ID:        primitive.NewObjectID(),
		Email:     "staff@example.test",
		FirstName: "Dana",
		LastName:  "Reed",
		Roles:     []string{"admin"},
	}
}

func actorBody(admin *models.AdminUser) string {
	return `{"currentUser":{"id":"` + admin.ID.Hex() + `"}}`
}

// The regression the member actually hit: the emailed link has to be a /reset/
// link whose token hashes to the stored value.
func TestAdminUserResetPassword_EmailedLinkMatchesTheStoredToken(t *testing.T) {
	memberID := primitive.NewObjectID()
	admin := staffAdmin()
	rig := newResetRig(t, memberID, admin, nil)

	rr := rig.call(t, memberID.Hex(), actorBody(admin), true)
	assert.Equal(t, http.StatusOK, rr.Code)

	prefix := testResetWebBase + "/reset/"
	assert.True(t, strings.HasPrefix(rig.sentLink, prefix),
		"link %q must point at the website's /reset/:token route", rig.sentLink)
	assert.Equal(t, "member@example.test", rig.sentTo)

	rawToken := strings.TrimPrefix(rig.sentLink, prefix)
	assert.Len(t, rawToken, 64, "raw token should be 32 bytes of hex")
	sum := sha256.Sum256([]byte(rawToken))

	if !assert.NotEmpty(t, rig.updates) {
		return
	}
	assert.Equal(t, hex.EncodeToString(sum[:]), rig.updates[0]["user.resetPasswordToken"],
		"the stored token must be sha256 of the token in the link")
}

// The console reads updatedAt as account activity, so a reset email must not
// move it — that is what made a sent email look like a changed password.
func TestAdminUserResetPassword_DoesNotTouchUpdatedAt(t *testing.T) {
	memberID := primitive.NewObjectID()
	admin := staffAdmin()
	rig := newResetRig(t, memberID, admin, nil)

	rig.call(t, memberID.Hex(), actorBody(admin), true)

	for i, set := range rig.updates {
		assert.NotContains(t, set, "updatedAt", "update %d bumped updatedAt", i)
		assert.NotContains(t, set, "user.updatedAt", "update %d bumped user.updatedAt", i)
		assert.NotContains(t, set, "user.passwordChangedAt",
			"update %d claimed a password change that did not happen", i)
	}
}

// One hour, the same window the website's own /forgot-password uses.
func TestAdminUserResetPassword_ExpiresInOneHour(t *testing.T) {
	memberID := primitive.NewObjectID()
	admin := staffAdmin()
	rig := newResetRig(t, memberID, admin, nil)

	before := time.Now()
	rig.call(t, memberID.Hex(), actorBody(admin), true)

	expires, ok := rig.updates[0]["user.resetPasswordExpires"].(time.Time)
	if !assert.True(t, ok, "expiry should be stored as a time") {
		return
	}
	assert.WithinDuration(t, before.Add(time.Hour), expires, 30*time.Second)
}

// Who sent it is resolved from admin_users, not from the request body.
func TestAdminUserResetPassword_RecordsTheStaffSender(t *testing.T) {
	memberID := primitive.NewObjectID()
	admin := staffAdmin()
	rig := newResetRig(t, memberID, admin, nil)

	rig.call(t, memberID.Hex(), actorBody(admin), true)

	if !assert.Len(t, rig.updates, 2, "the send should be recorded in a second write") {
		return
	}
	assert.Equal(t, "Dana Reed", rig.updates[1]["user.lastResetEmailBy"])
	assert.Contains(t, rig.updates[1], "user.lastResetEmailAt")
	rig.aadb.AssertCalled(t, "InsertOne", mock.Anything, mock.Anything)
}

// A failed send must not be reported as success, and must not leave a record
// claiming an email went out.
func TestAdminUserResetPassword_FailedSendIsNotRecordedAsSent(t *testing.T) {
	memberID := primitive.NewObjectID()
	admin := staffAdmin()
	rig := newResetRig(t, memberID, admin, assert.AnError)

	rr := rig.call(t, memberID.Hex(), actorBody(admin), true)

	assert.Equal(t, http.StatusBadGateway, rr.Code)
	assert.Len(t, rig.updates, 1, "only the token write should have happened")
	for _, set := range rig.updates {
		assert.NotContains(t, set, "user.lastResetEmailAt")
		assert.NotContains(t, set, "user.lastResetEmailBy")
	}
	rig.aadb.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)
}

// Without the gateway secret this would be an unauthenticated way to plant a
// live reset token on any account.
func TestAdminUserResetPassword_RequiresTheFirstPartySecret(t *testing.T) {
	memberID := primitive.NewObjectID()
	admin := staffAdmin()
	rig := newResetRig(t, memberID, admin, nil)

	rr := rig.call(t, memberID.Hex(), actorBody(admin), false)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Empty(t, rig.updates)
	assert.Empty(t, rig.sentLink)
}

// An unidentifiable caller is refused rather than recorded as nobody.
func TestAdminUserResetPassword_UnknownStaffIsRefused(t *testing.T) {
	memberID := primitive.NewObjectID()
	rig := newResetRig(t, memberID, nil, nil)

	rr := rig.call(t, memberID.Hex(),
		`{"currentUser":{"id":"`+primitive.NewObjectID().Hex()+`"}}`, true)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Empty(t, rig.updates)
	assert.Empty(t, rig.sentLink)
}

// An id is the only way in: an email in the body is neither accepted nor put
// in a query.
func TestAdminUserResetPassword_EmailInTheBodyIsNotAnIdentity(t *testing.T) {
	memberID := primitive.NewObjectID()
	admin := staffAdmin()
	rig := newResetRig(t, memberID, admin, nil)

	rr := rig.call(t, memberID.Hex(),
		`{"currentUser":{"email":"`+admin.Email+`"}}`, true)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Empty(t, rig.updates)
	assert.Empty(t, rig.sentLink)
	rig.adb.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
}

// A path segment that is not a member id is refused before any query is built,
// so no part of the request string reaches Mongo.
func TestAdminUserResetPassword_RejectsANonObjectIDPath(t *testing.T) {
	memberID := primitive.NewObjectID()
	admin := staffAdmin()

	for _, bad := range []string{"not-an-id", "", "507f1f77bcf86cd79943902", `{"$ne":null}`} {
		rig := newResetRig(t, memberID, admin, nil)
		rr := rig.call(t, bad, actorBody(admin), true)
		assert.Equal(t, http.StatusBadRequest, rr.Code, "id %q should be refused", bad)
		assert.Empty(t, rig.updates)
		assert.Empty(t, rig.sentLink)
	}
}

func TestUserResetLink(t *testing.T) {
	assert.Equal(t, "https://www.linespolice-cad.com/reset/abc123",
		userResetLink("https://www.linespolice-cad.com/", "abc123"))
	assert.Equal(t, "https://example.test/reset/abc123",
		userResetLink("https://example.test", "abc123"))
}

func TestNewResetTokenHashesTheRawToken(t *testing.T) {
	raw, hashed, err := newResetToken()
	assert.NoError(t, err)
	assert.Len(t, raw, 64)
	sum := sha256.Sum256([]byte(raw))
	assert.Equal(t, hex.EncodeToString(sum[:]), hashed)
	assert.NotEqual(t, raw, hashed)
}

func TestAdminDisplayLabel(t *testing.T) {
	assert.Equal(t, "Dana Reed", adminDisplayLabel(&models.AdminUser{
		FirstName: "Dana", LastName: "Reed", Email: "staff@example.test",
	}))
	// No name on the account: the address it signs in with, which the admins
	// panel already shows.
	assert.Equal(t, "staff@example.test", adminDisplayLabel(&models.AdminUser{
		Email: "staff@example.test",
	}))
	assert.Equal(t, "Dana", adminDisplayLabel(&models.AdminUser{
		FirstName: "Dana", Email: "staff@example.test",
	}))
	assert.Equal(t, "", adminDisplayLabel(nil))
}

// The admin response must not carry the stored token: UserDetails.MarshalJSON
// redacts it everywhere else, and this struct bypasses that.
func TestAdminUserDetailsHasNoResetToken(t *testing.T) {
	typ := reflect.TypeOf(models.AdminUserDetails{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		assert.NotEqual(t, "resetPasswordToken", name,
			"the admin user response must not expose the stored reset token")
	}
}
