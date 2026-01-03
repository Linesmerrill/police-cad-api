package api

import (
	"context"
	"time"
)

// QueryTimeout is the default timeout for database queries
// Reduced from 10s to 7s to release connections faster and prevent pool exhaustion
// If queries consistently timeout, check for missing indexes or slow query patterns
const QueryTimeout = 7 * time.Second

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

