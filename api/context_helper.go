package api

import (
	"context"
	"time"
)

// QueryTimeout is the default timeout for database queries
const QueryTimeout = 10 * time.Second

// WithQueryTimeout creates a context with query timeout
func WithQueryTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, QueryTimeout)
}

