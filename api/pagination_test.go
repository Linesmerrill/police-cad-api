package api

import (
	"net/http/httptest"
	"testing"
)

func TestParseLimitPage(t *testing.T) {
	const defaultLimit = 10
	const maxLimit = 100

	tests := []struct {
		name      string
		query     string
		wantLimit int64
		wantPage  int64
		wantSkip  int64
	}{
		{"no params", "", 10, 0, 0},
		{"empty limit", "limit=", 10, 0, 0},
		{"zero limit", "limit=0", 10, 0, 0},
		{"negative limit", "limit=-5", 10, 0, 0},
		{"non-numeric limit", "limit=abc", 10, 0, 0},
		{"valid limit", "limit=25", 25, 0, 0},
		{"limit at cap", "limit=100", 100, 0, 0},
		{"over cap", "limit=500", 100, 0, 0},
		{"huge over cap", "limit=9999", 100, 0, 0},
		{"page only", "page=3", 10, 3, 30},
		{"negative page", "page=-2", 10, 0, 0},
		{"non-numeric page", "page=abc", 10, 0, 0},
		{"limit and page", "limit=20&page=2", 20, 2, 40},
		{"limit and page at cap", "limit=100&page=5", 100, 5, 500},
		{"over-cap with page", "limit=1000&page=1", 100, 1, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/?"+tt.query, nil)
			limit, page, skip := ParseLimitPage(req, defaultLimit, maxLimit)
			if limit != tt.wantLimit {
				t.Errorf("limit = %d, want %d", limit, tt.wantLimit)
			}
			if page != tt.wantPage {
				t.Errorf("page = %d, want %d", page, tt.wantPage)
			}
			if skip != tt.wantSkip {
				t.Errorf("skip = %d, want %d", skip, tt.wantSkip)
			}
		})
	}
}
