package handlers

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

const (
	testRPID   = "linespolice-cad.com"
	testOrigin = "https://www.linespolice-cad.com"
)

// softAuthenticator is a minimal WebAuthn authenticator: one P-256 key,
// "none" attestation, user present and verified.
type softAuthenticator struct {
	key    *ecdsa.PrivateKey
	credID []byte
	count  uint32
}

func newSoftAuthenticator(t *testing.T) *softAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &softAuthenticator{key: key, credID: id}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func clientData(typ, challenge string) []byte {
	cd, _ := json.Marshal(map[string]interface{}{"type": typ, "challenge": challenge, "origin": testOrigin, "crossOrigin": false})
	return cd
}

func (a *softAuthenticator) authData(withCredential bool) []byte {
	rp := sha256.Sum256([]byte(testRPID))
	flags := byte(0x01 | 0x04) // user present, user verified
	if withCredential {
		flags |= 0x40 // attested credential data
	}
	var buf bytes.Buffer
	buf.Write(rp[:])
	buf.WriteByte(flags)
	_ = binary.Write(&buf, binary.BigEndian, a.count)
	if withCredential {
		buf.Write(make([]byte, 16)) // AAGUID
		_ = binary.Write(&buf, binary.BigEndian, uint16(len(a.credID)))
		buf.Write(a.credID)
		pad := func(b []byte) []byte { out := make([]byte, 32); copy(out[32-len(b):], b); return out }
		cose, _ := cbor.Marshal(map[int]interface{}{
			1: 2, 3: -7, -1: 1,
			-2: pad(a.key.PublicKey.X.Bytes()),
			-3: pad(a.key.PublicKey.Y.Bytes()),
		})
		buf.Write(cose)
	}
	return buf.Bytes()
}

// register answers PublicKeyCredentialCreationOptions with a credential.
func (a *softAuthenticator) register(t *testing.T, options map[string]interface{}) json.RawMessage {
	t.Helper()
	challenge := options["challenge"].(string)
	attObj, err := cbor.Marshal(map[string]interface{}{"fmt": "none", "attStmt": map[string]interface{}{}, "authData": a.authData(true)})
	require.NoError(t, err)
	cred, _ := json.Marshal(map[string]interface{}{
		"id": b64(a.credID), "rawId": b64(a.credID), "type": "public-key",
		"response": map[string]interface{}{
			"clientDataJSON":    b64(clientData("webauthn.create", challenge)),
			"attestationObject": b64(attObj),
		},
	})
	return cred
}

// sign answers PublicKeyCredentialRequestOptions with an assertion.
func (a *softAuthenticator) sign(t *testing.T, options map[string]interface{}) json.RawMessage {
	t.Helper()
	challenge := options["challenge"].(string)
	a.count++
	ad := a.authData(false)
	cd := clientData("webauthn.get", challenge)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	require.NoError(t, err)
	cred, _ := json.Marshal(map[string]interface{}{
		"id": b64(a.credID), "rawId": b64(a.credID), "type": "public-key",
		"response": map[string]interface{}{
			"clientDataJSON": b64(cd), "authenticatorData": b64(ad), "signature": b64(sig),
		},
	})
	return cred
}

// memAdminDB is an AdminDatabase holding one admin, applying the update
// operators the passkey handlers use.
type memAdminDB struct {
	*mocks.AdminDatabase
	admin *models.AdminUser
}

func newMemAdminDB(admin *models.AdminUser) *memAdminDB {
	return &memAdminDB{AdminDatabase: &mocks.AdminDatabase{}, admin: admin}
}

func (m *memAdminDB) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.AdminUser, error) {
	return m.clone(), nil
}

func (m *memAdminDB) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	m.apply(update.(bson.M))
	return &mongo.UpdateResult{MatchedCount: 1}, nil
}

func (m *memAdminDB) clone() *models.AdminUser {
	raw, _ := bson.Marshal(m.admin)
	var out models.AdminUser
	_ = bson.Unmarshal(raw, &out)
	return &out
}

// apply round-trips the admin through BSON so stored passkeys and sessions
// are decoded exactly as from Mongo.
func (m *memAdminDB) apply(update bson.M) {
	raw, _ := bson.Marshal(m.admin)
	var doc bson.M
	_ = bson.Unmarshal(raw, &doc)
	mfa, _ := doc["mfa"].(bson.M)
	if mfa == nil {
		mfa = bson.M{}
	}
	toBSON := func(v interface{}) interface{} {
		b, err := bson.Marshal(bson.M{"v": v})
		if err != nil {
			return v
		}
		var d bson.M
		_ = bson.Unmarshal(b, &d)
		return d["v"]
	}
	if set, ok := update["$set"].(bson.M); ok {
		for k, v := range set {
			if strings.HasPrefix(k, "mfa.passkeys.") {
				// mfa.passkeys.<i>.<field>
				parts := strings.SplitN(strings.TrimPrefix(k, "mfa.passkeys."), ".", 2)
				i, _ := strconv.Atoi(parts[0])
				list, _ := mfa["passkeys"].(bson.A)
				if i < len(list) {
					if el, ok := list[i].(bson.M); ok {
						el[parts[1]] = toBSON(v)
					}
				}
			} else if len(k) > 4 && k[:4] == "mfa." {
				mfa[k[4:]] = toBSON(v)
			} else {
				doc[k] = toBSON(v)
			}
		}
	}
	if unset, ok := update["$unset"].(bson.M); ok {
		for k := range unset {
			if len(k) > 4 && k[:4] == "mfa." {
				delete(mfa, k[4:])
			}
		}
	}
	if push, ok := update["$push"].(bson.M); ok {
		if v, ok := push["mfa.passkeys"]; ok {
			list, _ := mfa["passkeys"].(bson.A)
			mfa["passkeys"] = append(list, toBSON(v))
		}
	}
	doc["mfa"] = mfa
	raw, _ = bson.Marshal(doc)
	var out models.AdminUser
	_ = bson.Unmarshal(raw, &out)
	*m.admin = out
}

func passkeyAdminFixture(t *testing.T, admin *models.AdminUser) (Admin, *memAdminDB) {
	t.Helper()
	t.Setenv("JWT_SECRET", financeTestSecret)
	t.Setenv("WEBAUTHN_RP_ID", testRPID)
	t.Setenv("WEBAUTHN_ORIGINS", testOrigin)
	db := newMemAdminDB(admin)
	aadb := &mocks.AdminActivityDatabase{}
	aadb.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil)
	return Admin{ADB: db, AADB: aadb}, db
}

func callJSON(t *testing.T, h http.HandlerFunc, body interface{}, token string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	rec := postJSON(h, body, token)
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func registerPasskey(t *testing.T, h Admin, auth *softAuthenticator, token string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	rec, opts := callJSON(t, h.AdminPasskeyRegisterBeginHandler, nil, token)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return callJSON(t, h.AdminPasskeyRegisterFinishHandler, map[string]interface{}{"name": "iPhone", "credential": auth.register(t, opts)}, token)
}

func TestPasskey_RegisterTurnsOnTwoFactorThenSignsIn(t *testing.T) {
	admin := &models.AdminUser{ID: primitive.NewObjectID(), Email: "owner@lpc.test", Roles: []string{"owner"}, Active: true}
	h, db := passkeyAdminFixture(t, admin)
	auth := newSoftAuthenticator(t)
	token, _ := issueAdminToken(admin, false)

	rec, out := registerPasskey(t, h, auth, token)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Len(t, out["backupCodes"], mfaBackupCodeCount)
	assert.True(t, tokenHasMFA(t, out["token"].(string)))
	assert.True(t, mfaEnabled(db.admin))
	require.Len(t, db.admin.MFA.Passkeys, 1)
	assert.Equal(t, "iPhone", db.admin.MFA.Passkeys[0].Name)
	assert.Nil(t, db.admin.MFA.PasskeySession)

	// Sign in: the password step's challenge, then the passkey.
	challenge, _ := issueMFAChallenge(db.admin)
	rec, opts := callJSON(t, h.AdminLoginPasskeyBeginHandler, map[string]string{"challenge": challenge}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec, login := callJSON(t, h.AdminLoginPasskeyFinishHandler, map[string]interface{}{"challenge": challenge, "credential": auth.sign(t, opts)}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, tokenHasMFA(t, login["token"].(string)))
	assert.NotEmpty(t, login["refreshToken"])
	assert.NotNil(t, db.admin.MFA.Passkeys[0].LastUsedAt)
	assert.Equal(t, uint32(1), db.admin.MFA.Passkeys[0].Credential.Authenticator.SignCount)
}

func TestPasskey_WrongKeyIsRefusedAndCounted(t *testing.T) {
	admin := &models.AdminUser{ID: primitive.NewObjectID(), Email: "owner@lpc.test", Roles: []string{"owner"}, Active: true}
	h, db := passkeyAdminFixture(t, admin)
	auth := newSoftAuthenticator(t)
	token, _ := issueAdminToken(admin, false)
	rec, _ := registerPasskey(t, h, auth, token)
	require.Equal(t, http.StatusOK, rec.Code)

	// An attacker's key under the same credential id.
	impostor := newSoftAuthenticator(t)
	impostor.credID = auth.credID
	challenge, _ := issueMFAChallenge(db.admin)
	_, opts := callJSON(t, h.AdminLoginPasskeyBeginHandler, map[string]string{"challenge": challenge}, "")
	rec, _ = callJSON(t, h.AdminLoginPasskeyFinishHandler, map[string]interface{}{"challenge": challenge, "credential": impostor.sign(t, opts)}, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, 1, db.admin.MFA.FailedAttempts)
}

func TestPasskey_AddingNeedsATwoFactorSessionOnceOn(t *testing.T) {
	admin := mfaAdmin() // two-factor on with an authenticator app
	h, _ := passkeyAdminFixture(t, admin)
	passwordOnly, _ := issueAdminToken(admin, false)
	rec, _ := callJSON(t, h.AdminPasskeyRegisterBeginHandler, nil, passwordOnly)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	verified, _ := issueAdminToken(admin, true)
	rec, out := registerPasskey(t, h, newSoftAuthenticator(t), verified)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	// Two-factor was already on: no new backup codes or token.
	assert.Nil(t, out["backupCodes"])
	assert.Nil(t, out["token"])
}

func TestPasskey_CannotRemoveTheOnlyFactor(t *testing.T) {
	admin := &models.AdminUser{ID: primitive.NewObjectID(), Email: "owner@lpc.test", Roles: []string{"owner"}, Active: true}
	h, db := passkeyAdminFixture(t, admin)
	token, _ := issueAdminToken(admin, false)
	rec, _ := registerPasskey(t, h, newSoftAuthenticator(t), token)
	require.Equal(t, http.StatusOK, rec.Code)

	verified, _ := issueAdminToken(db.admin, true)
	id := passkeyID(db.admin.MFA.Passkeys[0].Credential)
	req := httptest.NewRequest(http.MethodDelete, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+verified)
	req = mux.SetURLVars(req, map[string]string{"id": id})
	del := httptest.NewRecorder()
	h.AdminPasskeyDeleteHandler(del, req)
	assert.Equal(t, http.StatusConflict, del.Code)
	assert.Len(t, db.admin.MFA.Passkeys, 1)
}

func TestPasskey_ExpiredCeremonyRefused(t *testing.T) {
	admin := &models.AdminUser{ID: primitive.NewObjectID(), Email: "owner@lpc.test", Roles: []string{"owner"}, Active: true}
	h, db := passkeyAdminFixture(t, admin)
	auth := newSoftAuthenticator(t)
	token, _ := issueAdminToken(admin, false)
	_, opts := callJSON(t, h.AdminPasskeyRegisterBeginHandler, nil, token)
	db.admin.MFA.PasskeySession.ExpiresAt = time.Now().Add(-time.Second)
	rec, _ := callJSON(t, h.AdminPasskeyRegisterFinishHandler, map[string]interface{}{"credential": auth.register(t, opts)}, token)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.False(t, mfaEnabled(db.admin))
}

func TestLoginReportsMFAMethods(t *testing.T) {
	admin := mfaAdmin()
	admin.MFA.Passkeys = []models.AdminPasskey{{Name: "Mac"}}
	assert.Equal(t, []string{"passkey", "totp"}, mfaMethods(admin))
}
