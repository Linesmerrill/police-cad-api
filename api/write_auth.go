package api

import (
	"crypto/subtle"
	"net/http"
	"os"

	"go.uber.org/zap"
)

// enforceWriteAuthEnv toggles write enforcement. Fail-open until set to "true"
// so the change can be deployed and then switched on once the token store and
// the website's X-API-Key are confirmed working.
const enforceWriteAuthEnv = "ENFORCE_WRITE_AUTH"

// These mirror the gateway constants in the handlers package (which the api
// package can't import without a cycle). The website backend sends the secret
// in this header for server-to-server calls.
const (
	gatewayKeyEnv = "API_GATEWAY_KEY"
	gatewayHeader = "X-API-Key"
)

// publicWritePaths are the only mutating endpoints reachable without a logged-in
// user: the signup / login / email-verification flows. Everything else must
// present either a valid bearer token (browser + mobile) or the first-party
// gateway secret (website backend, server-to-server).
var publicWritePaths = map[string]bool{
	"/api/v1/auth/token":                      true, // login (HTTP basic auth)
	"/api/v1/auth/logout":                     true, // revoke (carries its own token)
	"/api/v1/user/create-user":                true, // signup
	"/api/v1/user/check-user":                 true, // email-exists check during signup
	"/api/v1/verify/send-verification-code":   true,
	"/api/v1/verify/verify-code":              true,
	"/api/v1/verify/resend-verification-code": true,
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// RequireWriteAuth rejects mutating requests (POST/PUT/PATCH/DELETE) that aren't
// authenticated, closing the hole where anyone could mutate data by hitting the
// API directly with a victim's userId. A write is allowed when it:
//
//   - targets a public endpoint (signup / login / email verification);
//   - presents the first-party gateway secret in X-API-Key (the website backend
//     makes server-to-server calls this way); or
//   - carries a valid bearer token (browser-direct and mobile calls).
//
// Enforcement is fail-open until ENFORCE_WRITE_AUTH=true. Reads are never
// affected. NOTE: this depends on the persistent token store (token_store.go) —
// without it, a valid bearer would stop validating after every restart.
func RequireWriteAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv(enforceWriteAuthEnv) != "true" {
			next.ServeHTTP(w, r)
			return
		}
		if !isMutatingMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if publicWritePaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		// First-party server-to-server calls (website backend) carry the shared
		// gateway secret.
		if key := os.Getenv(gatewayKeyEnv); key != "" {
			if provided := r.Header.Get(gatewayHeader); provided != "" &&
				subtle.ConstantTimeCompare([]byte(provided), []byte(key)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Otherwise require a valid bearer token.
		if authenticator != nil {
			if user, err := authenticator.Authenticate(r); err == nil && user != nil {
				if uid := user.ID(); uid != "" {
					r = r.WithContext(withAuthenticatedUserID(r.Context(), uid))
				}
				next.ServeHTTP(w, r)
				return
			}
		}

		zap.S().Warnw("write auth: rejected unauthenticated write",
			"path", r.URL.Path,
			"method", r.Method,
			"ua", r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized","message":"A valid login is required to perform this action."}`))
	})
}
