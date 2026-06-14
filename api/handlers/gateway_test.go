package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// okHandler is a trivial downstream handler that records that it was reached.
func okHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestApiKeyGateway(t *testing.T) {
	const secret = "super-secret-key"

	tests := []struct {
		name       string
		keyEnv     string // value of API_GATEWAY_KEY for this case
		method     string
		path       string
		headers    map[string]string
		wantStatus int
		wantPass   bool // whether the downstream handler should be reached
	}{
		{
			name:       "fail-open when key unset",
			keyEnv:     "",
			method:     http.MethodGet,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"User-Agent": "curl/8.0.1"},
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:       "valid key allowed",
			keyEnv:     secret,
			method:     http.MethodGet,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"X-API-Key": secret, "User-Agent": "axios/1.6.0"},
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:       "wrong key rejected even from our origin",
			keyEnv:     secret,
			method:     http.MethodGet,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"X-API-Key": "nope", "Origin": "https://www.linespolice-cad.com"},
			wantStatus: http.StatusForbidden,
			wantPass:   false,
		},
		{
			name:       "allowed origin without key (browser)",
			keyEnv:     secret,
			method:     http.MethodGet,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"Origin": "https://www.linespolice-cad.com", "User-Agent": "Mozilla/5.0"},
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:       "allowed via referer",
			keyEnv:     secret,
			method:     http.MethodGet,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"Referer": "https://www.linespolice-cad.com/community/abc", "User-Agent": "Mozilla/5.0"},
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:       "browser on foreign origin blocked",
			keyEnv:     secret,
			method:     http.MethodGet,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"Origin": "https://evil.example.com", "User-Agent": "Mozilla/5.0"},
			wantStatus: http.StatusForbidden,
			wantPass:   false,
		},
		{
			name:       "curl without key blocked",
			keyEnv:     secret,
			method:     http.MethodGet,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"User-Agent": "curl/8.0.1"},
			wantStatus: http.StatusForbidden,
			wantPass:   false,
		},
		{
			name:       "python requests blocked",
			keyEnv:     secret,
			method:     http.MethodGet,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"User-Agent": "python-requests/2.31.0"},
			wantStatus: http.StatusForbidden,
			wantPass:   false,
		},
		{
			name:       "empty user-agent blocked",
			keyEnv:     secret,
			method:     http.MethodGet,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{},
			wantStatus: http.StatusForbidden,
			wantPass:   false,
		},
		{
			name:       "iOS mobile app allowed (exempt)",
			keyEnv:     secret,
			method:     http.MethodGet,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"User-Agent": "Lines Police CAD/1.27.1 CFNetwork/1490.0.4 Darwin/23.4.0"},
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:       "Android mobile app allowed (exempt)",
			keyEnv:     secret,
			method:     http.MethodGet,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"User-Agent": "okhttp/4.9.2"},
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:       "CORS preflight always allowed",
			keyEnv:     secret,
			method:     http.MethodOptions,
			path:       "/api/v1/community/abc",
			headers:    map[string]string{"User-Agent": "curl/8.0.1"},
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
		{
			name:       "health check always allowed",
			keyEnv:     secret,
			method:     http.MethodGet,
			path:       "/health",
			headers:    map[string]string{"User-Agent": "curl/8.0.1"},
			wantStatus: http.StatusOK,
			wantPass:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(apiGatewayKeyEnv, tt.keyEnv)

			reached := false
			handler := ApiKeyGateway(okHandler(&reached))

			req := httptest.NewRequest(tt.method, tt.path, nil)
			// httptest sets a default UA; clear it so empty-UA cases are honest.
			req.Header.Del("User-Agent")
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
