package api

import (
	"net/http"
	"strconv"
)

const (
	DefaultListLimit = 10
	MaxListLimit     = 100
)

// ParseLimitPage parses ?limit and ?page query parameters with safe defaults.
// Missing, zero, negative, or non-numeric limits fall back to defaultLimit.
// Limits exceeding maxLimit are clamped to maxLimit. Page is clamped to >= 0.
// Returns the parsed limit, page, and pre-computed skip (page * limit).
//
// This replaces the broken `Limit|10` pattern where SetLimit(0) sent to MongoDB
// returned the entire matching set, tripping the "objects returned > 1000" alert.
func ParseLimitPage(r *http.Request, defaultLimit, maxLimit int) (limit, page, skip int64) {
	limit = int64(defaultLimit)
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		if v > maxLimit {
			v = maxLimit
		}
		limit = int64(v)
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v >= 0 {
		page = int64(v)
	}
	skip = page * limit
	return
}
