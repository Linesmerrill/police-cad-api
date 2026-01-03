package api

import (
	"context"
	"time"
)

// QueryTimeout is the default timeout for database queries
// Reduced to 5s: Queries should complete in <100ms with proper indexes
// If queries consistently timeout, check for missing indexes or slow query patterns
// With optimized connection pool (400 per dyno), connections release faster
const QueryTimeout = 5 * time.Second

// WithQueryTimeout creates a context with query timeout
// IMPORTANT: This preserves the request trace from the parent context
// so DB queries can be tracked even when using timeout contexts
func WithQueryTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	// context.WithTimeout preserves all values from parent context
	// So if parent has the trace, it will be preserved
	return context.WithTimeout(parent, QueryTimeout)
}

