package api

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// MetricsMiddleware tracks request timing and metrics
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip tracking metrics endpoints themselves to avoid polluting metrics
		path := r.URL.Path
		if path == "/api/v2/metrics" || 
		   path == "/api/v2/metrics/summary" || 
		   path == "/api/v2/metrics/route" || 
		   path == "/api/v2/metrics/slow-queries" ||
		   path == "/metrics-dashboard" ||
		   path == "/health" {
			// Skip metrics collection for these endpoints
			next.ServeHTTP(w, r)
			return
		}

		startTime := time.Now()
		requestID := uuid.New().String()

		// Create trace
		trace := &RequestTrace{
			RequestID: requestID,
			Method:    r.Method,
			Path:      path,
			StartTime: startTime,
			DBQueries: make([]DBQueryTrace, 0),
			Metadata:   make(map[string]string),
		}

		// Add trace to context
		ctx := WithRequestTrace(r.Context(), trace)
		r = r.WithContext(ctx)

		// Wrap response writer to capture status code
		wrappedWriter := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Track middleware time
		middlewareStart := time.Now()
		
		// Execute handler
		next.ServeHTTP(wrappedWriter, r)

		// Calculate times
		middlewareTime := time.Since(middlewareStart)
		totalDuration := time.Since(startTime)
		handlerTime := totalDuration - middlewareTime

		trace.EndTime = time.Now()
		trace.TotalDuration = totalDuration
		trace.MiddlewareTime = middlewareTime
		trace.HandlerTime = handlerTime
		trace.Status = wrappedWriter.statusCode

		// Record error if status >= 400
		if wrappedWriter.statusCode >= 400 {
			trace.Error = http.StatusText(wrappedWriter.statusCode)
		}

		// Record trace asynchronously (non-blocking) - never impacts request flow
		// If metrics collection fails or is slow, it doesn't affect the response
		GetMetrics().RecordTrace(*trace)

		// Log slow requests asynchronously (non-blocking)
		// Use a goroutine to ensure logging never blocks the response
		if totalDuration > 1*time.Second {
			go func() {
				// Recover from any panics in logging to ensure it never crashes
				defer func() {
					if r := recover(); r != nil {
						// Silently ignore logging panics
					}
				}()
				zap.S().Warnw("Slow request detected",
					"requestId", requestID,
					"method", r.Method,
					"path", r.URL.Path,
					"duration", totalDuration,
					"status", wrappedWriter.statusCode,
					"dbQueries", len(trace.DBQueries),
					"dbTime", trace.DBTotalTime,
				)
			}()
		}
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
// It implements http.Hijacker to support WebSocket upgrades
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Hijack implements http.Hijacker to support WebSocket upgrades
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

