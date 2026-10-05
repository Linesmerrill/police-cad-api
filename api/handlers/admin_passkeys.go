package handlers

// Passkeys (WebAuthn) as the admin's second factor, alongside the
// authenticator code and backup codes.
//
// Registration (admin access token; if two-factor is already on, the session
// must have passed it):
//
//	POST   /api/v1/admin/mfa/passkeys/register/begin   -> PublicKeyCredentialCreationOptions
//	POST   /api/v1/admin/mfa/passkeys/register/finish  {name, credential}
//	DELETE /api/v1/admin/mfa/passkeys/{id}             (MFA-verified session)
//
// Sign-in, after the password step returned an MFA challenge:
//
//	POST /api/v1/admin/login/passkey/begin   {challenge}             -> PublicKeyCredentialRequestOptions
//	POST /api/v1/admin/login/passkey/finish  {challenge, credential} -> {token, refreshToken, admin}
//
// The relying party is the website: WEBAUTHN_RP_ID (default
// linespolice-cad.com, so www and the bare domain share passkeys) and the
// allowed WEBAUTHN_ORIGINS (comma-separated, default the two https origins).
// Each ceremony's challenge is kept on the admin document for five minutes.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/linesmerrill/police-cad-api/models"
)

const (
	passkeyCeremonyTTL = 5 * time.Minute
	maxAdminPasskeys   = 10
	passkeyNameMax     = 40
)

func adminWebAuthn() (*webauthn.WebAuthn, error) {
	rpID := strings.TrimSpace(os.Getenv("WEBAUTHN_RP_ID"))
	if rpID == "" {
		rpID = "linespolice-cad.com"
	}
	var origins []string
	for _, o := range strings.Split(os.Getenv("WEBAUTHN_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	if len(origins) == 0 {
		origins = []string{"https://www.linespolice-cad.com", "https://linespolice-cad.com"}
	}
	return webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: "Lines Police CAD Admin",
		RPOrigins:     origins,
	})
}

// passkeyUser adapts an admin to webauthn.User.
type passkeyUser struct{ admin *models.AdminUser }

func (u passkeyUser) WebAuthnID() []byte          { return []byte(u.admin.ID.Hex()) }
func (u passkeyUser) WebAuthnName() string        { return u.admin.Email }
func (u passkeyUser) WebAuthnDisplayName() string { return u.admin.Email }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential {
	if u.admin.MFA == nil {
		return nil
	}
	creds := make([]webauthn.Credential, 0, len(u.admin.MFA.Passkeys))
	for _, p := range u.admin.MFA.Passkeys {
		creds = append(creds, p.Credential)
	}
	return creds
}

func passkeyID(c webauthn.Credential) string {
	return base64.RawURLEncoding.EncodeToString(c.ID)
}

func (h Admin) savePasskeySession(ctx context.Context, admin *models.AdminUser, purpose string, data *webauthn.SessionData) error {
	_, err := h.ADB.UpdateOne(ctx, bson.M{"_id": admin.ID}, bson.M{"$set": bson.M{
		"mfa.passkeySession": models.AdminPasskeySession{
			Purpose:   purpose,
			Data:      *data,
			ExpiresAt: time.Now().Add(passkeyCeremonyTTL),
		},
	}})
	return err
}

func passkeySession(admin *models.AdminUser, purpose string) (webauthn.SessionData, bool) {
	if admin.MFA == nil || admin.MFA.PasskeySession == nil {
		return webauthn.SessionData{}, false
	}
	s := admin.MFA.PasskeySession
	if s.Purpose != purpose || time.Now().After(s.ExpiresAt) {
		return webauthn.SessionData{}, false
	}
	return s.Data, true
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// adminForPasskeySetup authenticates the access token. Once two-factor is on,
// adding a passkey needs a session that passed it, so a stolen password alone
// can't add the attacker's passkey.
func (h Admin) adminForPasskeySetup(w http.ResponseWriter, r *http.Request) (*models.AdminUser, bool) {
	admin, claims, status, msg := h.adminFromAccessToken(r)
	if admin == nil {
		writeAdminError(w, status, msg, "UNAUTHORIZED")
		return nil, false
	}
	if mfaEnabled(admin) && !claimsMFA(claims) {
		writeAdminError(w, http.StatusForbidden, "Sign in again with two-factor first.", "MFA_REQUIRED")
		return nil, false
	}
	return admin, true
}

// AdminPasskeyRegisterBeginHandler starts adding a passkey.
func (h Admin) AdminPasskeyRegisterBeginHandler(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.adminForPasskeySetup(w, r)
	if !ok {
		return
	}
	if admin.MFA != nil && len(admin.MFA.Passkeys) >= maxAdminPasskeys {
		writeAdminError(w, http.StatusConflict, "You already have the maximum of 10 passkeys.", "PASSKEY_LIMIT")
		return
	}
	wa, err := adminWebAuthn()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "passkeys are not configured", "SERVER_ERROR")
		return
	}
	user := passkeyUser{admin}
	exclusions := make([]protocol.CredentialDescriptor, 0)
	for _, c := range user.WebAuthnCredentials() {
		exclusions = append(exclusions, c.Descriptor())
	}
	creation, session, err := wa.BeginRegistration(user,
		webauthn.WithExclusions(exclusions),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
	)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "could not start passkey setup", "SERVER_ERROR")
		return
	}
	if err := h.savePasskeySession(r.Context(), admin, "register", session); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "could not start passkey setup", "SERVER_ERROR")
		return
	}
	writeAdminJSON(w, http.StatusOK, creation.Response)
}

type passkeyFinishRequest struct {
	Name       string          `json:"name"`
	Challenge  string          `json:"challenge"`
	Credential json.RawMessage `json:"credential"`
}

// AdminPasskeyRegisterFinishHandler verifies the new passkey and saves it.
// If two-factor was off, this turns it on (with backup codes) and returns an
// access token that counts as MFA-verified.
func (h Admin) AdminPasskeyRegisterFinishHandler(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.adminForPasskeySetup(w, r)
	if !ok {
		return
	}
	var req passkeyFinishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Credential) == 0 {
		writeAdminError(w, http.StatusBadRequest, "credential required", "INVALID_REQUEST")
		return
	}
	session, ok := passkeySession(admin, "register")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Passkey setup expired. Try again.", "PASSKEY_EXPIRED")
		return
	}
	wa, err := adminWebAuthn()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "passkeys are not configured", "SERVER_ERROR")
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(req.Credential)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "That passkey response could not be read.", "PASSKEY_INVALID")
		return
	}
	cred, err := wa.CreateCredential(passkeyUser{admin}, session, parsed)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "That passkey could not be verified.", "PASSKEY_INVALID")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Passkey"
	}
	if len([]rune(name)) > passkeyNameMax {
		name = string([]rune(name)[:passkeyNameMax])
	}
	now := time.Now()
	passkey := models.AdminPasskey{Name: name, CreatedAt: now, Credential: *cred}

	wasEnabled := mfaEnabled(admin)
	set := bson.M{}
	var plain []string
	if !wasEnabled {
		set["mfa.enabled"] = true
		set["mfa.enabledAt"] = now
		if admin.MFA == nil || len(admin.MFA.BackupCodes) == 0 {
			var hashes []string
			plain, hashes, err = newBackupCodes()
			if err != nil {
				writeAdminError(w, http.StatusInternalServerError, "could not create backup codes", "SERVER_ERROR")
				return
			}
			set["mfa.backupCodes"] = hashes
		}
	}
	update := bson.M{
		"$push":  bson.M{"mfa.passkeys": passkey},
		"$unset": bson.M{"mfa.passkeySession": "", "mfa.pendingSecret": "", "mfa.pendingAt": ""},
	}
	if len(set) > 0 {
		update["$set"] = set
	}
	if _, err := h.ADB.UpdateOne(r.Context(), bson.M{"_id": admin.ID}, update); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "could not save the passkey", "SERVER_ERROR")
		return
	}

	resp := map[string]interface{}{"passkey": map[string]interface{}{"id": passkeyID(*cred), "name": name}}
	if !wasEnabled {
		// The owner just proved the new passkey: this session now counts as
		// two-factor verified.
		if admin.MFA == nil {
			admin.MFA = &models.AdminMFA{}
		}
		admin.MFA.Enabled = true
		admin.MFA.Passkeys = append(admin.MFA.Passkeys, passkey)
		token, terr := issueAdminToken(admin, true)
		refresh, rerr := issueAdminRefreshToken(admin, true)
		if terr != nil || rerr != nil {
			writeAdminError(w, http.StatusInternalServerError, "token generation failed", "SERVER_ERROR")
			return
		}
		resp["token"] = token
		resp["refreshToken"] = refresh
		if len(plain) > 0 {
			resp["backupCodes"] = plain
		}
	}
	writeAdminJSON(w, http.StatusOK, resp)
}

// AdminPasskeyDeleteHandler removes a passkey. The last remaining factor
// can't be removed this way: turning two-factor off is its own action.
func (h Admin) AdminPasskeyDeleteHandler(w http.ResponseWriter, r *http.Request) {
	admin, claims, status, msg := h.adminFromAccessToken(r)
	if admin == nil {
		writeAdminError(w, status, msg, "UNAUTHORIZED")
		return
	}
	if !mfaEnabled(admin) || !claimsMFA(claims) {
		writeAdminError(w, http.StatusForbidden, "Sign in again with two-factor first.", "MFA_REQUIRED")
		return
	}
	id := mux.Vars(r)["id"]
	var target *models.AdminPasskey
	for i := range admin.MFA.Passkeys {
		if passkeyID(admin.MFA.Passkeys[i].Credential) == id {
			target = &admin.MFA.Passkeys[i]
			break
		}
	}
	if target == nil {
		writeAdminError(w, http.StatusNotFound, "passkey not found", "NOT_FOUND")
		return
	}
	if admin.MFA.Secret == "" && len(admin.MFA.Passkeys) == 1 {
		writeAdminError(w, http.StatusConflict, "This is your only two-factor method. Add another first, or turn two-factor off.", "LAST_FACTOR")
		return
	}
	if _, err := h.ADB.UpdateOne(r.Context(), bson.M{"_id": admin.ID},
		bson.M{"$pull": bson.M{"mfa.passkeys": bson.M{"credential.id": target.Credential.ID}}}); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "could not remove the passkey", "SERVER_ERROR")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// ---------------------------------------------------------------------------
// Sign-in
// ---------------------------------------------------------------------------

// adminFromMFAChallenge resolves the admin behind a password-step challenge.
func (h Admin) adminFromMFAChallenge(ctx context.Context, challenge string) (*models.AdminUser, error) {
	claims, err := parseAdminJWT(challenge)
	if err != nil {
		return nil, err
	}
	if scope, _ := claims["scope"].(string); scope != mfaChallengeScope {
		return nil, errors.New("not a challenge")
	}
	adminID, err := claimsAdminID(claims)
	if err != nil {
		return nil, err
	}
	admin, err := h.ADB.FindOne(ctx, bson.M{"_id": adminID, "active": true})
	if err != nil || !mfaEnabled(admin) {
		return nil, errors.New("invalid credentials")
	}
	return admin, nil
}

type passkeyLoginRequest struct {
	Challenge  string          `json:"challenge"`
	Credential json.RawMessage `json:"credential"`
}

// AdminLoginPasskeyBeginHandler returns the WebAuthn request options for the
// admin's passkeys.
func (h Admin) AdminLoginPasskeyBeginHandler(w http.ResponseWriter, r *http.Request) {
	var req passkeyLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Challenge == "" {
		writeAdminError(w, http.StatusBadRequest, "challenge required", "INVALID_REQUEST")
		return
	}
	admin, err := h.adminFromMFAChallenge(r.Context(), req.Challenge)
	if err != nil {
		writeAdminError(w, http.StatusUnauthorized, "Your sign-in expired. Enter your password again.", "MFA_CHALLENGE_EXPIRED")
		return
	}
	if len(admin.MFA.Passkeys) == 0 {
		writeAdminError(w, http.StatusConflict, "No passkeys on this account.", "NO_PASSKEYS")
		return
	}
	if mfaLocked(admin, time.Now()) {
		writeAdminError(w, http.StatusTooManyRequests, errMFALocked.Error(), "MFA_LOCKED")
		return
	}
	wa, err := adminWebAuthn()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "passkeys are not configured", "SERVER_ERROR")
		return
	}
	assertion, session, err := wa.BeginLogin(passkeyUser{admin}, webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "could not start passkey sign-in", "SERVER_ERROR")
		return
	}
	if err := h.savePasskeySession(r.Context(), admin, "login", session); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "could not start passkey sign-in", "SERVER_ERROR")
		return
	}
	writeAdminJSON(w, http.StatusOK, assertion.Response)
}

// AdminLoginPasskeyFinishHandler verifies the passkey and issues tokens, the
// same as a correct authenticator code.
func (h Admin) AdminLoginPasskeyFinishHandler(w http.ResponseWriter, r *http.Request) {
	var req passkeyLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Challenge == "" || len(req.Credential) == 0 {
		writeAdminError(w, http.StatusBadRequest, "challenge and credential required", "INVALID_REQUEST")
		return
	}
	admin, err := h.adminFromMFAChallenge(r.Context(), req.Challenge)
	if err != nil {
		writeAdminError(w, http.StatusUnauthorized, "Your sign-in expired. Enter your password again.", "MFA_CHALLENGE_EXPIRED")
		return
	}
	now := time.Now()
	if mfaLocked(admin, now) {
		writeAdminError(w, http.StatusTooManyRequests, errMFALocked.Error(), "MFA_LOCKED")
		return
	}
	session, ok := passkeySession(admin, "login")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Passkey sign-in expired. Try again.", "PASSKEY_EXPIRED")
		return
	}
	wa, err := adminWebAuthn()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "passkeys are not configured", "SERVER_ERROR")
		return
	}

	parsed, perr := protocol.ParseCredentialRequestResponseBytes(req.Credential)
	var cred *webauthn.Credential
	if perr == nil {
		cred, err = wa.ValidateLogin(passkeyUser{admin}, session, parsed)
	}
	if perr != nil || err != nil {
		ferr := recordMFAFailure(r.Context(), h.ADB, admin, now)
		status, code := mfaErrorStatus(ferr)
		writeAdminError(w, status, "That passkey didn't work. Try again or use a code.", code)
		return
	}

	// Save the updated sign counter, mark it used, and close the ceremony.
	for i := range admin.MFA.Passkeys {
		if passkeyID(admin.MFA.Passkeys[i].Credential) == passkeyID(*cred) {
			_, _ = h.ADB.UpdateOne(r.Context(), bson.M{"_id": admin.ID}, bson.M{
				"$set": bson.M{
					"mfa.passkeys." + strconv.Itoa(i) + ".credential": *cred,
					"mfa.passkeys." + strconv.Itoa(i) + ".lastUsedAt": now,
				},
				"$unset": bson.M{"mfa.passkeySession": ""},
			})
			break
		}
	}
	clearMFAFailures(r.Context(), h.ADB, admin)

	token, terr := issueAdminToken(admin, true)
	refresh, rerr := issueAdminRefreshToken(admin, true)
	if terr != nil || rerr != nil {
		writeAdminError(w, http.StatusInternalServerError, "token generation failed", "SERVER_ERROR")
		return
	}
	h.trackAdminLogin(admin.ID, r)
	_, _ = h.ADB.UpdateOne(r.Context(), bson.M{"_id": admin.ID}, bson.M{"$set": bson.M{"lastAccessedAt": now}})
	writeAdminJSON(w, http.StatusOK, adminLoginBody(admin, token, refresh))
}

// passkeyViews lists an admin's passkeys for the status endpoint.
func passkeyViews(admin *models.AdminUser) []map[string]interface{} {
	out := []map[string]interface{}{}
	if admin == nil || admin.MFA == nil {
		return out
	}
	for _, p := range admin.MFA.Passkeys {
		v := map[string]interface{}{"id": passkeyID(p.Credential), "name": p.Name, "createdAt": p.CreatedAt}
		if p.LastUsedAt != nil {
			v["lastUsedAt"] = p.LastUsedAt
		}
		out = append(out, v)
	}
	return out
}

// mfaMethods are the second factors an admin can use at sign-in.
func mfaMethods(admin *models.AdminUser) []string {
	methods := []string{}
	if admin == nil || admin.MFA == nil {
		return methods
	}
	if len(admin.MFA.Passkeys) > 0 {
		methods = append(methods, "passkey")
	}
	if admin.MFA.Secret != "" {
		methods = append(methods, "totp")
	}
	if len(admin.MFA.BackupCodes) > 0 {
		methods = append(methods, "backup")
	}
	return methods
}
