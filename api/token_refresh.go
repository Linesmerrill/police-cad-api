package api

import (
	"encoding/json"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/models"
)

// Renewing a session without a password.
//
// POST /api/v1/auth/session/refresh
//
// The mobile app has always re-minted its bearer from the email and password
// it keeps in the keychain, roughly every ten minutes. An account that signed
// in with Google or Apple has no password to re-mint from — a social account
// is created with none — so that path cannot renew it and the app's own
// "no stored credentials" branch signs the person straight back out.
//
// This route is the renewal those accounts use: a still-valid bearer buys a
// fresh one. It proves as much as a password does, because the token store is
// the thing a password produces and revoking it (DELETE /auth/logout) ends
// the session either way.
//
// The account gate runs again on every refresh, which is what keeps the
// ten-minute cycle doing the work SuspensionLoginError describes: a suspension
// applied mid-session takes hold at the next renewal rather than waiting for
// the token's own 30-day expiry.

// refreshResponse matches /auth/token's body exactly, so the app stores the
// result of either through one code path.
type refreshResponse struct {
	Token string `json:"token"`
	ID    string `json:"_id"`
}

// RefreshToken exchanges a valid bearer token for a new one.
//
// The actor comes from the token the middleware verified and from nothing else
// — no body, no query parameter. A refresh that could name its own account
// would be a way to mint a session for somebody else's.
func (m MiddlewareDB) RefreshToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	userID := GetAuthenticatedUserIDFromContext(r.Context())
	if userID == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write(unauthorizedBody("sign in again"))
		return
	}

	oid, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		// The token carried something that is not an account id. It came out of
		// our own store, so this is not a caller's doing, but it is not a
		// session we can renew either.
		zap.S().Warnw("RefreshToken: token holds an unusable account id", "userID", userID)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write(unauthorizedBody("sign in again"))
		return
	}

	ctx, cancel := WithQueryTimeout(r.Context())
	defer cancel()

	user := models.User{}
	if err := m.DB.FindOne(ctx, bson.M{"_id": oid}).Decode(&user); err != nil {
		// A token for an account that is gone cannot be renewed. Answering 401
		// is what gets the app to the login screen.
		zap.S().Infow("RefreshToken: no account for this token", "userID", userID, "error", err)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write(unauthorizedBody("sign in again"))
		return
	}

	if status, err := AccountSignInError(&user, time.Now()); err != nil {
		zap.S().Infow("RefreshToken: account cannot be signed into",
			"userID", userID, "reason", err)
		w.WriteHeader(status)
		_, _ = w.Write(unauthorizedBody(err.Error()))
		return
	}

	token, err := IssueBearerToken(r, user.Details.Email, user.ID)
	if err != nil {
		zap.S().Errorw("RefreshToken: could not issue a token", "userID", userID, "error", err)
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write(unavailableBody(errAuthUnavailable.Error()))
		return
	}

	// The presented token is deliberately left valid. Revoking it here would
	// break any request already in flight with it, and both tokens expire on
	// the store's own schedule; /auth/token has always behaved this way.
	_ = json.NewEncoder(w).Encode(refreshResponse{Token: token, ID: user.ID})
}
