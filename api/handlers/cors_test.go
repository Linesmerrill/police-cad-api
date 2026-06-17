package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCorsMiddleware_EchoesAllowedOriginNeverWildcardWithCredentials(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := CorsMiddleware(next)

	tests := []struct {
		name            string
		origin          string
		wantAllowOrigin string
	}{
		{"apex", "https://linespolice-cad.com", "https://linespolice-cad.com"},
		{"www", "https://www.linespolice-cad.com", "https://www.linespolice-cad.com"},
		{"cloudflare subdomain", "https://app.linespolice-cad.com", "https://app.linespolice-cad.com"},
		{"dev heroku", "https://police-cad-dev.herokuapp.com", "https://police-cad-dev.herokuapp.com"},
		{"localhost", "http://localhost:8080", "http://localhost:8080"},
		{"unknown origin falls back to canonical (never *)", "https://evil.example.com", "https://www.linespolice-cad.com"},
		{"no origin falls back to canonical (never *)", "", "https://www.linespolice-cad.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v2/communities/tag/all", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			got := rec.Header().Get("Access-Control-Allow-Origin")
			if got != tt.wantAllowOrigin {
				t.Errorf("Allow-Origin = %q, want %q", got, tt.wantAllowOrigin)
			}
			if got == "*" && rec.Header().Get("Access-Control-Allow-Credentials") == "true" {
				t.Errorf("emitted illegal Allow-Origin:* with Allow-Credentials:true")
			}
		})
	}
}
