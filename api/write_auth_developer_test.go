package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The Developer API prefix is the one path RequireWriteAuth steps aside for.
// That makes it the riskiest line in the feature, so it is pinned here: the
// exemption must be narrow, must not leak to neighbouring paths, and must not
// turn a developer key into something that passes the first-party gate.

func TestRequireWriteAuth_DeveloperPrefixIsExempt(t *testing.T) {
	t.Setenv(enforceWriteAuthEnv, "true")
	t.Setenv(gatewayKeyEnv, "a-first-party-secret")

	reached := false
	handler := RequireWriteAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	// A developer write carries no gateway secret, no browser Origin and no
	// bearer session, so without the exemption this would be a 401 before the
	// developer middleware ever ran.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dev/communities/abc/calls", nil)
	req.Header.Set("Authorization", "Bearer lpc_live_deadbeef")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !reached || rr.Code != http.StatusOK {
		t.Fatalf("developer writes must reach their own middleware, got %d reached=%v", rr.Code, reached)
	}
}

// The exemption must cover the developer surface and nothing adjacent to it.
// A path that merely starts with the same letters, or sits one level up, is
// still fully gated.
func TestRequireWriteAuth_DeveloperExemptionIsNarrow(t *testing.T) {
	t.Setenv(enforceWriteAuthEnv, "true")
	t.Setenv(gatewayKeyEnv, "a-first-party-secret")

	handler := RequireWriteAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{
		"/api/v1/developer/keys",    // the management surface: a session route
		"/api/v1/dev",               // no trailing slash: not the subrouter
		"/api/v1/devious/community", // shares a prefix, is not the surface
		"/api/v2/dev/communities",   // v2 has no developer surface
		"/api/v1/community/abc",     // an ordinary route
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			req.Header.Set("Authorization", "Bearer lpc_live_deadbeef")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("%s must stay gated, got %d", path, rr.Code)
			}
		})
	}
}

// A developer key presented as the first-party gateway secret must not work.
// The gateway secret is a shared first-party credential; a third-party key
// being able to stand in for it would hand a developer the website's own
// authority.
func TestRequireWriteAuth_DeveloperKeyIsNotTheGatewaySecret(t *testing.T) {
	t.Setenv(enforceWriteAuthEnv, "true")
	t.Setenv(gatewayKeyEnv, "a-first-party-secret")

	handler := RequireWriteAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/community/abc/members", nil)
	req.Header.Set(gatewayHeader, "lpc_live_deadbeefdeadbeef")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden && rr.Code != http.StatusUnauthorized {
		t.Fatalf("a developer key must not satisfy the gateway secret, got %d", rr.Code)
	}
}

// developerAPIPathPrefix is duplicated from handlers.devAPIPathPrefix because
// this package cannot import handlers without a cycle. If the two ever drift,
// the gateway and the write gate would exempt different paths from the one the
// routes are registered under — so the value is pinned here.
func TestDeveloperAPIPathPrefix_IsPinned(t *testing.T) {
	if developerAPIPathPrefix != "/api/v1/dev/" {
		t.Fatalf("the developer prefix changed to %q; update handlers.devAPIPathPrefix "+
			"and the route registration in api/handlers/api.go to match",
			developerAPIPathPrefix)
	}
}
