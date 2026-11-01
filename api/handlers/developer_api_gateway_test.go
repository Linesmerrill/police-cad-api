package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The gateway's other exemption for the Developer API. Same reasoning as the
// write gate: a third-party integration is exactly the caller the gateway's
// user-agent heuristic exists to refuse, so the surface has to be let past it
// and authenticated by its own middleware instead.

func TestApiKeyGateway_DeveloperPrefixIsExempt(t *testing.T) {
	t.Setenv(apiGatewayKeyEnv, "a-first-party-secret")

	handler := ApiKeyGateway(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// curl's user agent is the clearest case: without the exemption the
	// gateway refuses it outright, and a FiveM mod may present something very
	// similar.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/whoami", nil)
	req.Header.Set("User-Agent", "curl/8.4.0")
	req.Header.Set("Authorization", "Bearer lpc_live_deadbeef")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("the developer surface must be reachable by a scripted client, got %d", rr.Code)
	}
}

// The exemption must not become a hole for everything else. The same curl
// request to an ordinary endpoint is still refused.
func TestApiKeyGateway_ExemptionDoesNotLeakToOtherPaths(t *testing.T) {
	t.Setenv(apiGatewayKeyEnv, "a-first-party-secret")

	handler := ApiKeyGateway(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{
		"/api/v1/community/6adab9b0c602a448a910d97e",
		"/api/v1/developer/keys",
		"/api/v1/devious/thing",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("User-Agent", "curl/8.4.0")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code == http.StatusOK {
				t.Fatalf("%s should still be refused for a scripted client, got %d", path, rr.Code)
			}
		})
	}
}

// A wrong gateway secret is a hard reject even on the developer surface: the
// gateway rejects a bad secret before any exemption is consulted, and that
// ordering is deliberate. Presenting a wrong first-party secret is a different
// thing from presenting none.
func TestApiKeyGateway_WrongSecretStillRejectedOnDeveloperPath(t *testing.T) {
	t.Setenv(apiGatewayKeyEnv, "a-first-party-secret")

	handler := ApiKeyGateway(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dev/whoami", nil)
	req.Header.Set(apiGatewayHeader, "not-the-secret")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("a wrong gateway secret must be refused, got %d", rr.Code)
	}
}

func TestIsDeveloperAPIPath(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/v1/dev/whoami":                true,
		"/api/v1/dev/communities/abc/calls": true,
		"/api/v1/dev/":                      true,
		"/api/v1/dev":                       false,
		"/api/v1/developer/keys":            false,
		"/api/v1/devious":                   false,
		"/api/v2/dev/whoami":                false,
		"/":                                 false,
	} {
		if got := IsDeveloperAPIPath(path); got != want {
			t.Errorf("IsDeveloperAPIPath(%q) = %v, want %v", path, got, want)
		}
	}
}
