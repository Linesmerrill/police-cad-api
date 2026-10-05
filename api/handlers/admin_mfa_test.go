package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/crypto/bcrypt"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

const mfaTestSecret = "JBSWY3DPEHPK3PXP"

func mfaCodeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, at, totpOpts())
	assert.NoError(t, err)
	return code
}

func mfaAdmin() *models.AdminUser {
	return &models.AdminUser{
		ID:     primitive.NewObjectID(),
		Email:  "owner@lpc.test",
		Roles:  []string{"owner"},
		Active: true,
		MFA:    &models.AdminMFA{Enabled: true, Secret: mfaTestSecret},
	}
}

// mfaHandler returns an Admin whose DB always finds admin and whose
// UpdateOne reports matched (the conditional update succeeded).
func mfaHandler(t *testing.T, admin *models.AdminUser, matched int64) (Admin, *mocks.AdminDatabase) {
	t.Setenv("JWT_SECRET", financeTestSecret)
	adb := &mocks.AdminDatabase{}
	adb.On("FindOne", mock.Anything, mock.Anything).Return(admin, nil)
	adb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(&mongo.UpdateResult{MatchedCount: matched}, nil)
	aadb := &mocks.AdminActivityDatabase{}
	aadb.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil)
	return Admin{ADB: adb, AADB: aadb}, adb
}

func postJSON(h http.HandlerFunc, body interface{}, token string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(b))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func tokenHasMFA(t *testing.T, raw string) bool {
	t.Helper()
	claims, err := parseAdminJWT(raw)
	assert.NoError(t, err)
	return claimsMFA(claims)
}

func TestMatchTOTPStep_AcceptsCurrentAndAdjacentOnly(t *testing.T) {
	now := time.Now()
	_, ok := matchTOTPStep(mfaTestSecret, mfaCodeAt(t, mfaTestSecret, now), now)
	assert.True(t, ok)
	_, ok = matchTOTPStep(mfaTestSecret, mfaCodeAt(t, mfaTestSecret, now.Add(-30*time.Second)), now)
	assert.True(t, ok)
	_, ok = matchTOTPStep(mfaTestSecret, mfaCodeAt(t, mfaTestSecret, now.Add(-5*time.Minute)), now)
	assert.False(t, ok)
}

func TestAdminLogin_MFAEnabledReturnsChallengeNotToken(t *testing.T) {
	admin := mfaAdmin()
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	admin.Password = string(hash)
	h, _ := mfaHandler(t, admin, 1)

	rec := postJSON(h.AdminLoginHandler, map[string]string{"email": admin.Email, "password": "pw"}, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	body := decodeBody(t, rec)
	assert.Equal(t, "MFA_REQUIRED", body["code"])
	assert.NotEmpty(t, body["challenge"])
	assert.Nil(t, body["token"])

	// The challenge is not an access token.
	f, fh := requireOwnerFixture(t, admin, nil)
	assert.Equal(t, http.StatusUnauthorized, runRequireOwner(t, f, fh, body["challenge"].(string)).Code)
}

func TestAdminLogin_NoMFAIssuesTokenWithoutMFAClaim(t *testing.T) {
	admin := mfaAdmin()
	admin.MFA = nil
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	admin.Password = string(hash)
	h, _ := mfaHandler(t, admin, 1)

	rec := postJSON(h.AdminLoginHandler, map[string]string{"email": admin.Email, "password": "pw"}, "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, tokenHasMFA(t, decodeBody(t, rec)["token"].(string)))
}

func TestAdminLoginMFA_ValidCodeIssuesMFAToken(t *testing.T) {
	admin := mfaAdmin()
	h, _ := mfaHandler(t, admin, 1)
	challenge, _ := issueMFAChallenge(admin)

	rec := postJSON(h.AdminLoginMFAHandler, map[string]string{"challenge": challenge, "code": mfaCodeAt(t, mfaTestSecret, time.Now())}, "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, tokenHasMFA(t, decodeBody(t, rec)["token"].(string)))
}

func TestAdminLoginMFA_ReplayedCodeRejected(t *testing.T) {
	admin := mfaAdmin()
	h, _ := mfaHandler(t, admin, 0) // conditional lastUsedStep update matched nothing
	challenge, _ := issueMFAChallenge(admin)

	rec := postJSON(h.AdminLoginMFAHandler, map[string]string{"challenge": challenge, "code": mfaCodeAt(t, mfaTestSecret, time.Now())}, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "MFA_INVALID", decodeBody(t, rec)["code"])
}

func TestAdminLoginMFA_WrongCodeCountsFailure(t *testing.T) {
	admin := mfaAdmin()
	h, adb := mfaHandler(t, admin, 1)
	challenge, _ := issueMFAChallenge(admin)

	rec := postJSON(h.AdminLoginMFAHandler, map[string]string{"challenge": challenge, "code": "000000"}, "")
	if mfaCodeAt(t, mfaTestSecret, time.Now()) == "000000" {
		t.Skip("current code happens to be 000000")
	}
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	adb.AssertCalled(t, "UpdateOne", mock.Anything, mock.Anything,
		bson.M{"$set": bson.M{"mfa.failedAttempts": 1}})
}

func TestAdminLoginMFA_TenthFailureLocks(t *testing.T) {
	admin := mfaAdmin()
	admin.MFA.FailedAttempts = mfaMaxFailures - 1
	h, adb := mfaHandler(t, admin, 1)
	challenge, _ := issueMFAChallenge(admin)

	rec := postJSON(h.AdminLoginMFAHandler, map[string]string{"challenge": challenge, "code": "not-a-code"}, "")
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	adb.AssertCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.MatchedBy(func(u bson.M) bool {
		set, _ := u["$set"].(bson.M)
		_, locked := set["mfa.lockedUntil"]
		return locked
	}))
}

func TestAdminLoginMFA_LockedRejectsEvenValidCode(t *testing.T) {
	admin := mfaAdmin()
	until := time.Now().Add(10 * time.Minute)
	admin.MFA.LockedUntil = &until
	h, _ := mfaHandler(t, admin, 1)
	challenge, _ := issueMFAChallenge(admin)

	rec := postJSON(h.AdminLoginMFAHandler, map[string]string{"challenge": challenge, "code": mfaCodeAt(t, mfaTestSecret, time.Now())}, "")
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
}

func TestAdminLoginMFA_BackupCodeWorksAndIsConsumed(t *testing.T) {
	admin := mfaAdmin()
	hash, _ := bcrypt.GenerateFromPassword([]byte("abcdefghjk"), bcrypt.MinCost)
	admin.MFA.BackupCodes = []string{string(hash)}
	h, adb := mfaHandler(t, admin, 1)
	challenge, _ := issueMFAChallenge(admin)

	rec := postJSON(h.AdminLoginMFAHandler, map[string]string{"challenge": challenge, "code": "ABCDE-FGHJK"}, "")
	assert.Equal(t, http.StatusOK, rec.Code)
	adb.AssertCalled(t, "UpdateOne", mock.Anything, mock.Anything,
		bson.M{"$pull": bson.M{"mfa.backupCodes": string(hash)}})
}

func TestAdminLoginMFA_AccessTokenIsNotAChallenge(t *testing.T) {
	admin := mfaAdmin()
	h, _ := mfaHandler(t, admin, 1)
	access, _ := issueAdminToken(admin, false)

	rec := postJSON(h.AdminLoginMFAHandler, map[string]string{"challenge": access, "code": mfaCodeAt(t, mfaTestSecret, time.Now())}, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAdminLoginMFA_ExpiredChallengeRejected(t *testing.T) {
	admin := mfaAdmin()
	h, _ := mfaHandler(t, admin, 1)
	expired, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": admin.ID.Hex(), "scope": mfaChallengeScope, "exp": time.Now().Add(-time.Minute).Unix(),
	}).SignedString([]byte(financeTestSecret))

	rec := postJSON(h.AdminLoginMFAHandler, map[string]string{"challenge": expired, "code": mfaCodeAt(t, mfaTestSecret, time.Now())}, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "MFA_CHALLENGE_EXPIRED", decodeBody(t, rec)["code"])
}

func TestAdminMFASetup_RefusedWhileEnabled(t *testing.T) {
	admin := mfaAdmin()
	h, _ := mfaHandler(t, admin, 1)
	token, _ := issueAdminToken(admin, false)

	rec := postJSON(h.AdminMFASetupHandler, nil, token)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestAdminMFASetup_ReturnsQRAndStoresPending(t *testing.T) {
	admin := mfaAdmin()
	admin.MFA = nil
	h, adb := mfaHandler(t, admin, 1)
	token, _ := issueAdminToken(admin, false)

	rec := postJSON(h.AdminMFASetupHandler, nil, token)
	assert.Equal(t, http.StatusOK, rec.Code)
	body := decodeBody(t, rec)
	assert.True(t, strings.HasPrefix(body["qrCode"].(string), "data:image/png;base64,"))
	assert.Contains(t, body["otpauthUrl"], "otpauth://totp/")
	adb.AssertCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.MatchedBy(func(u bson.M) bool {
		set, _ := u["$set"].(bson.M)
		return set["mfa.pendingSecret"] == body["secret"]
	}))
}

func TestAdminMFAEnable_ValidCodeReturnsBackupCodesAndMFAToken(t *testing.T) {
	admin := mfaAdmin()
	pendingAt := time.Now()
	admin.MFA = &models.AdminMFA{PendingSecret: mfaTestSecret, PendingAt: &pendingAt}
	h, _ := mfaHandler(t, admin, 1)
	token, _ := issueAdminToken(admin, false)

	rec := postJSON(h.AdminMFAEnableHandler, map[string]string{"code": mfaCodeAt(t, mfaTestSecret, time.Now())}, token)
	assert.Equal(t, http.StatusOK, rec.Code)
	body := decodeBody(t, rec)
	assert.Len(t, body["backupCodes"], mfaBackupCodeCount)
	assert.True(t, tokenHasMFA(t, body["token"].(string)))
}

func TestAdminMFAEnable_ExpiredPendingRejected(t *testing.T) {
	admin := mfaAdmin()
	pendingAt := time.Now().Add(-time.Hour)
	admin.MFA = &models.AdminMFA{PendingSecret: mfaTestSecret, PendingAt: &pendingAt}
	h, _ := mfaHandler(t, admin, 1)
	token, _ := issueAdminToken(admin, false)

	rec := postJSON(h.AdminMFAEnableHandler, map[string]string{"code": mfaCodeAt(t, mfaTestSecret, time.Now())}, token)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminMFADisable_NeedsMFASession(t *testing.T) {
	admin := mfaAdmin()
	h, adb := mfaHandler(t, admin, 1)
	token, _ := issueAdminToken(admin, false)

	rec := postJSON(h.AdminMFADisableHandler, map[string]string{"code": mfaCodeAt(t, mfaTestSecret, time.Now())}, token)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	adb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, bson.M{"$unset": bson.M{"mfa": ""}})
}

func TestAdminMFADisable_WithMFASessionAndCode(t *testing.T) {
	admin := mfaAdmin()
	h, adb := mfaHandler(t, admin, 1)
	token, _ := issueAdminToken(admin, true)

	rec := postJSON(h.AdminMFADisableHandler, map[string]string{"code": mfaCodeAt(t, mfaTestSecret, time.Now())}, token)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, tokenHasMFA(t, decodeBody(t, rec)["token"].(string)))
	adb.AssertCalled(t, "UpdateOne", mock.Anything, mock.Anything, bson.M{"$unset": bson.M{"mfa": ""}})
}

func TestAdminMFAStatus_NeverLeaksSecret(t *testing.T) {
	admin := mfaAdmin()
	h, _ := mfaHandler(t, admin, 1)
	token, _ := issueAdminToken(admin, true)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.AdminMFAStatusHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), mfaTestSecret)
	body := decodeBody(t, rec)
	assert.Equal(t, true, body["enabled"])
	assert.Equal(t, true, body["sessionVerified"])
}

func refreshWith(t *testing.T, h Admin, refresh string) *httptest.ResponseRecorder {
	t.Helper()
	return postJSON(h.AdminTokenRefreshHandler, map[string]string{"refreshToken": refresh}, "")
}

func TestAdminLogin_ReturnsRefreshTokenThatRenewsAccess(t *testing.T) {
	admin := mfaAdmin()
	admin.MFA = nil
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	admin.Password = string(hash)
	h, _ := mfaHandler(t, admin, 1)

	login := decodeBody(t, postJSON(h.AdminLoginHandler, map[string]string{"email": admin.Email, "password": "pw"}, ""))
	refresh, _ := login["refreshToken"].(string)
	require.NotEmpty(t, refresh)

	rec := refreshWith(t, h, refresh)
	assert.Equal(t, http.StatusOK, rec.Code)
	body := decodeBody(t, rec)
	assert.False(t, tokenHasMFA(t, body["token"].(string)))
	assert.NotEmpty(t, body["refreshToken"])
}

func TestAdminTokenRefresh_KeepsTheMFAClaim(t *testing.T) {
	admin := mfaAdmin()
	h, _ := mfaHandler(t, admin, 1)
	challenge, _ := issueMFAChallenge(admin)
	login := decodeBody(t, postJSON(h.AdminLoginMFAHandler, map[string]string{"challenge": challenge, "code": mfaCodeAt(t, mfaTestSecret, time.Now())}, ""))

	rec := refreshWith(t, h, login["refreshToken"].(string))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, tokenHasMFA(t, decodeBody(t, rec)["token"].(string)))
}

func TestAdminTokenRefresh_Refusals(t *testing.T) {
	admin := mfaAdmin()
	h, _ := mfaHandler(t, admin, 1)
	refreshMFA, _ := issueAdminRefreshToken(admin, true)
	access, _ := issueAdminToken(admin, true)

	// An access token is not a refresh token.
	assert.Equal(t, http.StatusUnauthorized, refreshWith(t, h, access).Code)

	// Two-factor turned off since: a two-factor session can't renew.
	admin.MFA = nil
	assert.Equal(t, http.StatusUnauthorized, refreshWith(t, h, refreshMFA).Code)

	// Password changed since: refused.
	admin.MFA = &models.AdminMFA{Enabled: true, Secret: mfaTestSecret}
	admin.SessionVersion = 1
	assert.Equal(t, http.StatusUnauthorized, refreshWith(t, h, refreshMFA).Code)
}

func TestAdminTokenRefresh_InactiveAdmin(t *testing.T) {
	t.Setenv("JWT_SECRET", financeTestSecret)
	admin := mfaAdmin()
	refresh, _ := issueAdminRefreshToken(admin, false)
	adb := &mocks.AdminDatabase{}
	adb.On("FindOne", mock.Anything, mock.Anything).Return(nil, errors.New("not found"))
	h := Admin{ADB: adb}
	assert.Equal(t, http.StatusUnauthorized, refreshWith(t, h, refresh).Code)
}
