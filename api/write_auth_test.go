package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shaj13/go-guardian/auth"
	"github.com/shaj13/go-guardian/auth/strategies/bearer"
	"github.com/shaj13/go-guardian/store"
)

// newTestBearerAuthenticator installs a package authenticator with a single
// valid bearer token and returns that token.
func newTestBearerAuthenticator(t *testing.T) string {
	t.Helper()
	authenticator = auth.New()
	c := store.NewFIFO(context.Background(), time.Hour)
	ts := bearer.New(bearer.NoOpAuthenticate, c)
	authenticator.EnableStrategy(bearer.CachedStrategyKey, ts)

	token := "valid-test-token"
	user := auth.NewDefaultUser("test@example.com", "user-123", nil, nil)
	if err := auth.Append(ts, token, user, nil); err != nil {
		t.Fatalf("append token: %v", err)
	}
	t.Cleanup(func() { authenticator = nil })
	return token
}

func TestRequireWriteAuth(t *testing.T) {
	const secret = "gateway-secret"

	tests := []struct {
		name       string
		enforce    string
		setupAuth  bool // install a valid-token authenticator
		method     string
		path       string
		headers    map[string]string
		wantStatus int
		wantPass   bool
	}{
		{
			name:       "fail-open when not enforced",
			enforce:    "",
			method:     http.MethodPatch,
			path:       "/api/v1/community/abc",
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:       "GET is never blocked",
			enforce:    "true",
			method:     http.MethodGet,
			path:       "/api/v1/community/abc",
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:       "public write path allowed (signup)",
			enforce:    "true",
			method:     http.MethodPost,
			path:       "/api/v1/user/create-user",
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:       "login allowed without token",
			enforce:    "true",
			method:     http.MethodPost,
			path:       "/api/v1/auth/token",
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:       "valid X-API-Key allowed (server-to-server)",
			enforce:    "true",
			method:     http.MethodPatch,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"X-API-Key": secret},
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:       "wrong X-API-Key without token rejected",
			enforce:    "true",
			method:     http.MethodPatch,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"X-API-Key": "nope"},
			wantStatus: http.StatusUnauthorized,
			wantPass:   false,
		},
		{
			name:       "valid bearer token allowed",
			enforce:    "true",
			setupAuth:  true,
			method:     http.MethodPatch,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"Authorization": "Bearer valid-test-token"},
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:       "invalid bearer token rejected",
			enforce:    "true",
			setupAuth:  true,
			method:     http.MethodPatch,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"Authorization": "Bearer bogus"},
			wantStatus: http.StatusUnauthorized,
			wantPass:   false,
		},
		{
			name:       "anonymous write rejected",
			enforce:    "true",
			method:     http.MethodDelete,
			path:       "/api/v1/community/abc",
			wantStatus: http.StatusUnauthorized,
			wantPass:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(enforceWriteAuthEnv, tt.enforce)
			t.Setenv(gatewayKeyEnv, secret)

			authenticator = nil
			if tt.setupAuth {
				newTestBearerAuthenticator(t)
			}

			reached := false
			handler := RequireWriteAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(tt.method, tt.path, nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if reached != tt.wantPass {
				t.Errorf("downstream reached = %v, want %v", reached, tt.wantPass)
			}
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}
