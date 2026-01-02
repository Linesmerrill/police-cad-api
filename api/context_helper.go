package api

import (
	"context"
	"time"
)

// QueryTimeout is the default timeout for database queries
const QueryTimeout = 10 * time.Second

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

