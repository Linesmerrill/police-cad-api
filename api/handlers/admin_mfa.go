package handlers

// Admin two-factor authentication (TOTP authenticator app + backup codes).
//
// Login with MFA enabled is two steps:
//
//	POST /api/v1/admin/login      {email, password}   -> 401 {code: "MFA_REQUIRED", challenge}
//	POST /api/v1/admin/login/mfa  {challenge, code}   -> 200 {token, admin}
//
// The challenge is a 5-minute JWT that only proves the password step passed;
// it is not an access token. Access tokens carry an "mfa" claim, and
// RequireOwner (finance) only accepts tokens with mfa=true from an admin whose
// MFA is still enabled.
//
// Enrollment and management (admin access token required):
//
//	GET  /api/v1/admin/mfa                  status
//	POST /api/v1/admin/mfa/setup            new pending secret + QR (refused while enabled)
//	POST /api/v1/admin/mfa/enable           {code} -> backup codes + upgraded token
//	POST /api/v1/admin/mfa/backup-codes     {code} -> fresh backup codes (mfa token)
//	POST /api/v1/admin/mfa/disable          {code} -> token without mfa (mfa token)
//
// Recovery when both the authenticator and the backup codes are lost is a
// manual `$unset: {mfa: ""}` on the admin_users document.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/crypto/bcrypt"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

const (
	mfaIssuer          = "Lines Police CAD Admin"
	mfaPeriod          = 30
	mfaSkewSteps       = 1
	mfaMaxFailures     = 10
	mfaLockout         = 15 * time.Minute
	mfaChallengeTTL    = 5 * time.Minute
	mfaPendingTTL      = 15 * time.Minute
	mfaBackupCodeCount = 10
	mfaChallengeScope  = "admin_mfa_challenge"
	adminTokenTTL      = 24 * time.Hour
	adminRefreshScope  = "admin_refresh"
	adminRefreshTTL    = 30 * 24 * time.Hour
)

var (
	errMFALocked  = errors.New("too many incorrect codes; try again in 15 minutes")
	errMFAInvalid = errors.New("that code is not valid")
)

// ---------------------------------------------------------------------------
// Tokens
// ---------------------------------------------------------------------------

func adminJWTSecret() ([]byte, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return nil, errors.New("server misconfigured")
	}
	return []byte(secret), nil
}

// issueAdminToken signs an admin access token. mfa records whether the
// second factor was verified for this session.
func issueAdminToken(admin *models.AdminUser, mfa bool) (string, error) {
	secret, err := adminJWTSecret()
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   admin.ID.Hex(),
		"email": admin.Email,
		"roles": admin.Roles,
		"scope": "admin",
		"typ":   "access",
		"mfa":   mfa,
		"iat":   now.Unix(),
		"exp":   now.Add(adminTokenTTL).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
}

// issueAdminRefreshToken signs a 30-day token that can only be traded for a
// new access token (AdminTokenRefreshHandler). The website keeps it in its
// server-side session, matching its 30-day session cookie, so an owner
// isn't sent back to the login page every 24 hours.
func issueAdminRefreshToken(admin *models.AdminUser, mfa bool) (string, error) {
	secret, err := adminJWTSecret()
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   admin.ID.Hex(),
		"scope": adminRefreshScope,
		"typ":   "refresh",
		"mfa":   mfa,
		"sv":    admin.SessionVersion,
		"iat":   now.Unix(),
		"exp":   now.Add(adminRefreshTTL).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
}

func issueMFAChallenge(admin *models.AdminUser) (string, error) {
	secret, err := adminJWTSecret()
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   admin.ID.Hex(),
		"scope": mfaChallengeScope,
		"typ":   "mfa_challenge",
		"iat":   now.Unix(),
		"exp":   now.Add(mfaChallengeTTL).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
}

// parseAdminJWT validates signature and expiry and returns the claims.
func parseAdminJWT(raw string) (jwt.MapClaims, error) {
	secret, err := adminJWTSecret()
	if err != nil {
		return nil, err
	}
	token, err := jwt.Parse(raw, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return secret, nil
	})
	if err != nil || !token.Valid {
		return nil, errors.New("invalid or expired token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}

func claimsAdminID(claims jwt.MapClaims) (primitive.ObjectID, error) {
	sub, _ := claims["sub"].(string)
	return primitive.ObjectIDFromHex(sub)
}

func claimsMFA(claims jwt.MapClaims) bool {
	v, _ := claims["mfa"].(bool)
	return v
}

func mfaEnabled(admin *models.AdminUser) bool {
	return admin != nil && admin.MFA != nil && admin.MFA.Enabled && admin.MFA.Secret != ""
}

// ---------------------------------------------------------------------------
// Code verification
// ---------------------------------------------------------------------------

func totpOpts() totp.ValidateOpts {
	return totp.ValidateOpts{Period: mfaPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}
}

// matchTOTPStep returns the time step the code is valid for, within
// ±mfaSkewSteps of now, or false.
func matchTOTPStep(secret, code string, now time.Time) (int64, bool) {
	current := now.Unix() / mfaPeriod
	for offset := int64(-mfaSkewSteps); offset <= mfaSkewSteps; offset++ {
		step := current + offset
		want, err := totp.GenerateCodeCustom(secret, time.Unix(step*mfaPeriod, 0), totpOpts())
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

func normalizeMFACode(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, " ", "")
	return strings.ReplaceAll(code, "-", "")
}

func isTOTPCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// verifyAdminSecondFactor checks a TOTP code or a backup code for an admin
// with MFA enabled. Accepted TOTP steps can't be reused, backup codes are
// consumed, and repeated failures lock the second factor for mfaLockout.
// Every state change is a conditional update, so concurrent attempts can't
// both succeed with the same code.
func verifyAdminSecondFactor(ctx context.Context, adb databases.AdminDatabase, admin *models.AdminUser, rawCode string) (usedBackup bool, err error) {
	if !mfaEnabled(admin) {
		return false, errMFAInvalid
	}
	now := time.Now()
	if admin.MFA.LockedUntil != nil && now.Before(*admin.MFA.LockedUntil) {
		return false, errMFALocked
	}

	code := normalizeMFACode(rawCode)
	ok := false
	if isTOTPCode(code) {
		if step, match := matchTOTPStep(admin.MFA.Secret, code, now); match {
			res, uerr := adb.UpdateOne(ctx,
				bson.M{"_id": admin.ID, "mfa.enabled": true, "$or": bson.A{
					bson.M{"mfa.lastUsedStep": bson.M{"$lt": step}},
					bson.M{"mfa.lastUsedStep": bson.M{"$exists": false}},
				}},
				bson.M{"$set": bson.M{"mfa.lastUsedStep": step}})
			if uerr != nil {
				return false, uerr
			}
			ok = res != nil && res.MatchedCount == 1
		}
	} else if code != "" {
		for _, hash := range admin.MFA.BackupCodes {
			if bcrypt.CompareHashAndPassword([]byte(hash), []byte(code)) != nil {
				continue
			}
			res, uerr := adb.UpdateOne(ctx,
				bson.M{"_id": admin.ID, "mfa.backupCodes": hash},
				bson.M{"$pull": bson.M{"mfa.backupCodes": hash}})
			if uerr != nil {
				return false, uerr
			}
			ok = res != nil && res.MatchedCount == 1
			usedBackup = ok
			break
		}
	}

	if !ok {
		failures := admin.MFA.FailedAttempts + 1
		set := bson.M{"mfa.failedAttempts": failures}
		if failures >= mfaMaxFailures {
			set["mfa.failedAttempts"] = 0
			set["mfa.lockedUntil"] = now.Add(mfaLockout)
		}
		_, _ = adb.UpdateOne(ctx, bson.M{"_id": admin.ID}, bson.M{"$set": set})
		if failures >= mfaMaxFailures {
			return false, errMFALocked
		}
		return false, errMFAInvalid
	}
	if admin.MFA.FailedAttempts > 0 || admin.MFA.LockedUntil != nil {
		_, _ = adb.UpdateOne(ctx, bson.M{"_id": admin.ID},
			bson.M{"$set": bson.M{"mfa.failedAttempts": 0}, "$unset": bson.M{"mfa.lockedUntil": ""}})
	}
	return usedBackup, nil
}

// newBackupCodes returns plaintext codes (shown once) and their bcrypt hashes.
// Codes are 10 lowercase base32 characters, displayed as xxxxx-xxxxx.
func newBackupCodes() (plain []string, hashes []string, err error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	for i := 0; i < mfaBackupCodeCount; i++ {
		buf := make([]byte, 10)
		if _, err = rand.Read(buf); err != nil {
			return nil, nil, err
		}
		for j := range buf {
			buf[j] = alphabet[int(buf[j])%len(alphabet)]
		}
		code := string(buf)
		hash, herr := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if herr != nil {
			return nil, nil, herr
		}
		plain = append(plain, code[:5]+"-"+code[5:])
		hashes = append(hashes, string(hash))
	}
	return plain, hashes, nil
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func writeAdminJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeAdminError(w http.ResponseWriter, status int, msg, code string) {
	writeAdminJSON(w, status, models.ErrorResponse{Success: false, Error: msg, Code: code})
}

func mfaErrorStatus(err error) (int, string) {
	switch err {
	case errMFALocked:
		return http.StatusTooManyRequests, "MFA_LOCKED"
	case errMFAInvalid:
		return http.StatusUnauthorized, "MFA_INVALID"
	default:
		return http.StatusInternalServerError, "MFA_ERROR"
	}
}

func adminLoginBody(admin *models.AdminUser, token, refresh string) adminLoginResponse {
	var resp adminLoginResponse
	resp.Token = token
	resp.RefreshToken = refresh
	resp.Admin.ID = admin.ID.Hex()
	resp.Admin.Email = admin.Email
	resp.Admin.Roles = admin.Roles
	return resp
}

type adminMFALoginRequest struct {
	Challenge string `json:"challenge"`
	Code      string `json:"code"`
}

// AdminLoginMFAHandler completes a login that AdminLoginHandler answered with
// MFA_REQUIRED.
func (h Admin) AdminLoginMFAHandler(w http.ResponseWriter, r *http.Request) {
	var req adminMFALoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Challenge == "" || req.Code == "" {
		writeAdminError(w, http.StatusBadRequest, "challenge and code required", "INVALID_REQUEST")
		return
	}
	claims, err := parseAdminJWT(req.Challenge)
	if err != nil {
		writeAdminError(w, http.StatusUnauthorized, "Your sign-in expired. Enter your password again.", "MFA_CHALLENGE_EXPIRED")
		return
	}
	if scope, _ := claims["scope"].(string); scope != mfaChallengeScope {
		writeAdminError(w, http.StatusUnauthorized, "invalid challenge", "MFA_CHALLENGE_EXPIRED")
		return
	}
	adminID, err := claimsAdminID(claims)
	if err != nil {
		writeAdminError(w, http.StatusUnauthorized, "invalid challenge", "MFA_CHALLENGE_EXPIRED")
		return
	}
	admin, err := h.ADB.FindOne(r.Context(), bson.M{"_id": adminID, "active": true})
	if err != nil || !mfaEnabled(admin) {
		writeAdminError(w, http.StatusUnauthorized, "Invalid credentials", "INVALID_CREDENTIALS")
		return
	}
	if _, err := verifyAdminSecondFactor(r.Context(), h.ADB, admin, req.Code); err != nil {
		status, code := mfaErrorStatus(err)
		writeAdminError(w, status, err.Error(), code)
		return
	}
	token, err := issueAdminToken(admin, true)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "token generation failed", "SERVER_ERROR")
		return
	}
	refresh, err := issueAdminRefreshToken(admin, true)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "token generation failed", "SERVER_ERROR")
		return
	}
	h.trackAdminLogin(admin.ID, r)
	now := time.Now()
	_, _ = h.ADB.UpdateOne(r.Context(), bson.M{"_id": admin.ID}, bson.M{"$set": bson.M{"lastAccessedAt": now}})
	writeAdminJSON(w, http.StatusOK, adminLoginBody(admin, token, refresh))
}

// adminFromAccessToken authenticates an admin access token (any active
// admin) and returns the admin document and the token's claims.
func (h Admin) adminFromAccessToken(r *http.Request) (*models.AdminUser, jwt.MapClaims, int, string) {
	parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return nil, nil, http.StatusUnauthorized, "missing or malformed Authorization header"
	}
	claims, err := parseAdminJWT(parts[1])
	if err != nil {
		return nil, nil, http.StatusUnauthorized, err.Error()
	}
	if scope, _ := claims["scope"].(string); scope != "admin" {
		return nil, nil, http.StatusUnauthorized, "token is not an admin token"
	}
	adminID, err := claimsAdminID(claims)
	if err != nil {
		return nil, nil, http.StatusUnauthorized, "invalid token subject"
	}
	admin, err := h.ADB.FindOne(r.Context(), bson.M{"_id": adminID})
	if err != nil || !admin.Active {
		return nil, nil, http.StatusUnauthorized, "unknown or inactive admin"
	}
	return admin, claims, 0, ""
}

type adminMFAStatus struct {
	Enabled              bool       `json:"enabled"`
	EnabledAt            *time.Time `json:"enabledAt,omitempty"`
	BackupCodesRemaining int        `json:"backupCodesRemaining"`
	SessionVerified      bool       `json:"sessionVerified"`
}

// AdminMFAStatusHandler reports whether MFA is on and whether this session
// passed it.
func (h Admin) AdminMFAStatusHandler(w http.ResponseWriter, r *http.Request) {
	admin, claims, status, msg := h.adminFromAccessToken(r)
	if admin == nil {
		writeAdminError(w, status, msg, "UNAUTHORIZED")
		return
	}
	resp := adminMFAStatus{SessionVerified: claimsMFA(claims) && mfaEnabled(admin)}
	if mfaEnabled(admin) {
		resp.Enabled = true
		resp.EnabledAt = admin.MFA.EnabledAt
		resp.BackupCodesRemaining = len(admin.MFA.BackupCodes)
	}
	writeAdminJSON(w, http.StatusOK, resp)
}

// AdminMFASetupHandler starts enrollment: a new secret is stored as pending
// and returned with a QR code. Refused while MFA is already on, so a stolen
// password alone can't re-enroll someone else's authenticator.
func (h Admin) AdminMFASetupHandler(w http.ResponseWriter, r *http.Request) {
	admin, _, status, msg := h.adminFromAccessToken(r)
	if admin == nil {
		writeAdminError(w, status, msg, "UNAUTHORIZED")
		return
	}
	if mfaEnabled(admin) {
		writeAdminError(w, http.StatusConflict, "Two-factor authentication is already on.", "MFA_ALREADY_ENABLED")
		return
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      mfaIssuer,
		AccountName: admin.Email,
		Period:      mfaPeriod,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "could not create a secret", "SERVER_ERROR")
		return
	}
	img, err := key.Image(240, 240)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "could not create a QR code", "SERVER_ERROR")
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "could not create a QR code", "SERVER_ERROR")
		return
	}
	now := time.Now()
	if _, err := h.ADB.UpdateOne(r.Context(), bson.M{"_id": admin.ID}, bson.M{"$set": bson.M{
		"mfa.enabled":       false,
		"mfa.pendingSecret": key.Secret(),
		"mfa.pendingAt":     now,
	}}); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "could not save the secret", "SERVER_ERROR")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{
		"secret":     key.Secret(),
		"otpauthUrl": key.URL(),
		"qrCode":     "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()),
	})
}

type adminMFACodeRequest struct {
	Code string `json:"code"`
}

func decodeMFACode(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req adminMFACodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		writeAdminError(w, http.StatusBadRequest, "Enter the code from your authenticator app.", "INVALID_REQUEST")
		return "", false
	}
	return req.Code, true
}

// AdminMFAEnableHandler confirms enrollment with a code from the pending
// secret, turns MFA on, and returns backup codes plus an access token that
// counts as MFA-verified.
func (h Admin) AdminMFAEnableHandler(w http.ResponseWriter, r *http.Request) {
	admin, _, status, msg := h.adminFromAccessToken(r)
	if admin == nil {
		writeAdminError(w, status, msg, "UNAUTHORIZED")
		return
	}
	code, ok := decodeMFACode(w, r)
	if !ok {
		return
	}
	if mfaEnabled(admin) {
		writeAdminError(w, http.StatusConflict, "Two-factor authentication is already on.", "MFA_ALREADY_ENABLED")
		return
	}
	if admin.MFA == nil || admin.MFA.PendingSecret == "" || admin.MFA.PendingAt == nil ||
		time.Since(*admin.MFA.PendingAt) > mfaPendingTTL {
		writeAdminError(w, http.StatusBadRequest, "Setup expired. Start again to get a new QR code.", "MFA_SETUP_EXPIRED")
		return
	}
	step, match := matchTOTPStep(admin.MFA.PendingSecret, normalizeMFACode(code), time.Now())
	if !match {
		writeAdminError(w, http.StatusUnauthorized, "That code doesn't match. Check the time on your phone and try the newest code.", "MFA_INVALID")
		return
	}
	plain, hashes, err := newBackupCodes()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "could not create backup codes", "SERVER_ERROR")
		return
	}
	now := time.Now()
	res, err := h.ADB.UpdateOne(r.Context(),
		bson.M{"_id": admin.ID, "mfa.pendingSecret": admin.MFA.PendingSecret},
		bson.M{"$set": bson.M{"mfa": models.AdminMFA{
			Enabled:      true,
			Secret:       admin.MFA.PendingSecret,
			EnabledAt:    &now,
			BackupCodes:  hashes,
			LastUsedStep: step,
		}}})
	if err != nil || res == nil || res.MatchedCount != 1 {
		writeAdminError(w, http.StatusConflict, "Setup changed in another window. Start again.", "MFA_SETUP_EXPIRED")
		return
	}
	admin.MFA = &models.AdminMFA{Enabled: true, Secret: "set"}
	token, err := issueAdminToken(admin, true)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "token generation failed", "SERVER_ERROR")
		return
	}
	refresh, err := issueAdminRefreshToken(admin, true)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "token generation failed", "SERVER_ERROR")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]interface{}{"backupCodes": plain, "token": token, "refreshToken": refresh})
}

// requireVerifiedAdmin authenticates the token and requires an MFA-verified
// session plus a fresh code, for changes to the second factor itself.
func (h Admin) requireVerifiedAdmin(w http.ResponseWriter, r *http.Request) (*models.AdminUser, bool) {
	admin, claims, status, msg := h.adminFromAccessToken(r)
	if admin == nil {
		writeAdminError(w, status, msg, "UNAUTHORIZED")
		return nil, false
	}
	if !mfaEnabled(admin) {
		writeAdminError(w, http.StatusConflict, "Two-factor authentication is off.", "MFA_NOT_ENABLED")
		return nil, false
	}
	if !claimsMFA(claims) {
		writeAdminError(w, http.StatusForbidden, "Sign in again with your two-factor code first.", "MFA_REQUIRED")
		return nil, false
	}
	code, ok := decodeMFACode(w, r)
	if !ok {
		return nil, false
	}
	if _, err := verifyAdminSecondFactor(r.Context(), h.ADB, admin, code); err != nil {
		status, errCode := mfaErrorStatus(err)
		writeAdminError(w, status, err.Error(), errCode)
		return nil, false
	}
	return admin, true
}

// AdminMFABackupCodesHandler replaces all backup codes.
func (h Admin) AdminMFABackupCodesHandler(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.requireVerifiedAdmin(w, r)
	if !ok {
		return
	}
	plain, hashes, err := newBackupCodes()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "could not create backup codes", "SERVER_ERROR")
		return
	}
	if _, err := h.ADB.UpdateOne(r.Context(), bson.M{"_id": admin.ID, "mfa.enabled": true},
		bson.M{"$set": bson.M{"mfa.backupCodes": hashes}}); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "could not save backup codes", "SERVER_ERROR")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]interface{}{"backupCodes": plain})
}

// AdminMFADisableHandler turns MFA off and returns a token without the mfa
// claim, since the session no longer has a second factor.
func (h Admin) AdminMFADisableHandler(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.requireVerifiedAdmin(w, r)
	if !ok {
		return
	}
	if _, err := h.ADB.UpdateOne(r.Context(), bson.M{"_id": admin.ID}, bson.M{"$unset": bson.M{"mfa": ""}}); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "could not turn off two-factor authentication", "SERVER_ERROR")
		return
	}
	admin.MFA = nil
	token, err := issueAdminToken(admin, false)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "token generation failed", "SERVER_ERROR")
		return
	}
	refresh, err := issueAdminRefreshToken(admin, false)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "token generation failed", "SERVER_ERROR")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]interface{}{"token": token, "refreshToken": refresh})
}

type adminRefreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

// AdminTokenRefreshHandler implements POST /api/v1/admin/token/refresh
// {refreshToken}. It trades a refresh token for a new access token (and a new
// refresh token), re-checking the admin first: the account must still be
// active, the password unchanged since the refresh token was issued
// (SessionVersion), and, for
// a session that passed two-factor, two-factor must still be on. Otherwise
// it's a 401 and the owner signs in again.
func (h Admin) AdminTokenRefreshHandler(w http.ResponseWriter, r *http.Request) {
	var req adminRefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		writeAdminError(w, http.StatusBadRequest, "refreshToken required", "INVALID_REQUEST")
		return
	}
	claims, err := parseAdminJWT(req.RefreshToken)
	if err != nil {
		writeAdminError(w, http.StatusUnauthorized, "Your session has ended. Sign in again.", "REFRESH_INVALID")
		return
	}
	if scope, _ := claims["scope"].(string); scope != adminRefreshScope {
		writeAdminError(w, http.StatusUnauthorized, "Your session has ended. Sign in again.", "REFRESH_INVALID")
		return
	}
	adminID, err := claimsAdminID(claims)
	if err != nil {
		writeAdminError(w, http.StatusUnauthorized, "Your session has ended. Sign in again.", "REFRESH_INVALID")
		return
	}
	admin, err := h.ADB.FindOne(r.Context(), bson.M{"_id": adminID, "active": true})
	if err != nil || admin == nil {
		writeAdminError(w, http.StatusUnauthorized, "Your session has ended. Sign in again.", "REFRESH_INVALID")
		return
	}
	if sv, _ := claims["sv"].(float64); int(sv) != admin.SessionVersion {
		writeAdminError(w, http.StatusUnauthorized, "Your password changed. Sign in again.", "REFRESH_INVALID")
		return
	}
	mfa := claimsMFA(claims)
	if mfa && !mfaEnabled(admin) {
		writeAdminError(w, http.StatusUnauthorized, "Two-factor changed. Sign in again.", "REFRESH_INVALID")
		return
	}

	token, err := issueAdminToken(admin, mfa)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "token generation failed", "SERVER_ERROR")
		return
	}
	refresh, err := issueAdminRefreshToken(admin, mfa)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "token generation failed", "SERVER_ERROR")
		return
	}
	writeAdminJSON(w, http.StatusOK, adminLoginBody(admin, token, refresh))
}
