package api

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// TimeoutMiddleware adds request timeout to prevent long-running requests
func TimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Create context with timeout
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			// Replace request context with timeout context
			r = r.WithContext(ctx)

			// Create a channel to signal completion
			done := make(chan bool)
			go func() {
				next.ServeHTTP(w, r)
				done <- true
			}()

			// Wait for either completion or timeout
			select {
			case <-done:
				// Request completed successfully
			case <-ctx.Done():
				// Request timed out
				if ctx.Err() == context.DeadlineExceeded {
					zap.S().Warnw("Request timeout",
						"path", r.URL.Path,
						"method", r.Method,
						"timeout", timeout)
					w.WriteHeader(http.StatusRequestTimeout)
					w.Write([]byte(`{"error": "Request timeout", "message": "The request took too long to process"}`))
				}
			}
		})
	}
}

