package api

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"
)

// RequestTrace tracks timing for a single request
type RequestTrace struct {
	RequestID      string            `json:"requestId"`
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	Status         int               `json:"status"`
	StartTime      time.Time         `json:"startTime"`
	EndTime        time.Time         `json:"endTime"`
	TotalDuration  time.Duration     `json:"totalDuration"`
	MiddlewareTime time.Duration     `json:"middlewareTime"`
	HandlerTime    time.Duration     `json:"handlerTime"`
	DBQueries      []DBQueryTrace    `json:"dbQueries"`
	DBTotalTime    time.Duration     `json:"dbTotalTime"`
	Error          string            `json:"error,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// DBQueryTrace tracks a single database query
type DBQueryTrace struct {
	Operation string        `json:"operation"`
	Collection string       `json:"collection"`
	Duration   time.Duration `json:"duration"`
	Error      string        `json:"error,omitempty"`
	Timestamp  time.Time     `json:"timestamp"`
}

// RouteMetrics aggregates metrics for a specific route
type RouteMetrics struct {
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	Count       int64     `json:"count"`
	ErrorCount  int64     `json:"errorCount"`
	TotalTime   time.Duration `json:"totalTime"`
	AvgTime     time.Duration `json:"avgTime"`
	MinTime     time.Duration `json:"minTime"`
	MaxTime     time.Duration `json:"maxTime"`
	P50Time     time.Duration `json:"p50Time"`
	P95Time     time.Duration `json:"p95Time"`
	P99Time     time.Duration `json:"p99Time"`
	DBTotalTime time.Duration `json:"dbTotalTime"`
	DBAvgTime   time.Duration `json:"dbAvgTime"`
	LastRequest time.Time     `json:"lastRequest"`
}

// MetricsCollector collects and aggregates request metrics
type MetricsCollector struct {
	mu              sync.RWMutex
	traces          []RequestTrace
	maxTraces       int
	routeMetrics    map[string]*RouteMetrics
	windowStart     time.Time
	windowDuration  time.Duration
	totalRequests   int64
	totalErrors     int64
	totalDBQueries  int64
	totalDBTime     time.Duration
	traceChan       chan RequestTrace // Buffered channel for async trace processing
	stopChan        chan struct{}    // Channel to signal shutdown
}

var globalMetrics *MetricsCollector

// InitMetrics initializes the global metrics collector
// IMPORTANT: Metrics collection is designed to NEVER block production requests.
// - Traces are queued asynchronously via buffered channel
// - If channel is full, traces are dropped silently (this is intentional)
// - All processing happens in background goroutines
// - Missing metrics is acceptable - performance is the priority
func InitMetrics(maxTraces int, windowDuration time.Duration) {
	// Use a buffered channel - if full, traces are dropped (non-blocking)
	// Buffer size: 1000 traces to handle bursts without blocking
	// If buffer fills up, new traces are dropped (best-effort metrics)
	traceChan := make(chan RequestTrace, 1000)
	stopChan := make(chan struct{})
	
	globalMetrics = &MetricsCollector{
		traces:         make([]RequestTrace, 0, maxTraces),
		maxTraces:      maxTraces,
		routeMetrics:   make(map[string]*RouteMetrics),
		windowStart:    time.Now(),
		windowDuration: windowDuration,
		traceChan:      traceChan,
		stopChan:       stopChan,
	}
	
	// Start async trace processor goroutine
	go globalMetrics.processTraces()
	
	// Start cleanup goroutine
	go globalMetrics.cleanup()
}

// GetMetrics returns the global metrics collector
func GetMetrics() *MetricsCollector {
	if globalMetrics == nil {
		InitMetrics(10000, 1*time.Hour) // Default: 10k traces, 1 hour window
	}
	return globalMetrics
}

// RecordTrace records a request trace asynchronously (non-blocking)
// CRITICAL: This function NEVER blocks. If the channel is full, the trace is dropped.
// This is intentional - production request flow is more important than metrics.
// Missing a metric is acceptable; slowing down a request is not.
func (mc *MetricsCollector) RecordTrace(trace RequestTrace) {
	// Non-blocking send - if channel is full, drop the trace immediately
	// This ensures metrics collection NEVER impacts production performance
	select {
	case mc.traceChan <- trace:
		// Successfully queued for async processing in background goroutine
	default:
		// Channel full - drop trace silently (this is fine, metrics are best-effort)
		// No error logging here - we don't want metrics to generate noise
	}
}

// processTraces processes traces from the channel asynchronously
func (mc *MetricsCollector) processTraces() {
	for {
		select {
		case trace := <-mc.traceChan:
			// Process trace asynchronously - errors are ignored
			mc.processTrace(trace)
		case <-mc.stopChan:
			return
		}
	}
}

// processTrace processes a single trace (called from background goroutine)
// This runs asynchronously and never blocks production requests
func (mc *MetricsCollector) processTrace(trace RequestTrace) {
	// Recover from any panics to ensure metrics never crash the app
	// If metrics processing fails, we silently continue - metrics are best-effort
	defer func() {
		if r := recover(); r != nil {
			// Silently ignore panics in metrics processing
			// Metrics failures should never impact production
		}
	}()

	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Add trace
	if len(mc.traces) >= mc.maxTraces {
		// Remove oldest trace
		mc.traces = mc.traces[1:]
	}
	mc.traces = append(mc.traces, trace)

	// Normalize route path to group similar routes (optional - can be toggled)
	normalizedPath := normalizeRoutePath(trace.Path)
	routeKey := trace.Method + " " + normalizedPath
	
	metrics, exists := mc.routeMetrics[routeKey]
	if !exists {
		metrics = &RouteMetrics{
			Method: trace.Method,
			Path:   trace.Path,
			MinTime: trace.TotalDuration,
		}
		mc.routeMetrics[routeKey] = metrics
	}

	metrics.Count++
	metrics.TotalTime += trace.TotalDuration
	metrics.AvgTime = metrics.TotalTime / time.Duration(metrics.Count)
	metrics.LastRequest = trace.StartTime

	if trace.TotalDuration < metrics.MinTime {
		metrics.MinTime = trace.TotalDuration
	}
	if trace.TotalDuration > metrics.MaxTime {
		metrics.MaxTime = trace.TotalDuration
	}

	if trace.Status >= 400 {
		metrics.ErrorCount++
		mc.totalErrors++
	}

	metrics.DBTotalTime += trace.DBTotalTime
	if metrics.Count > 0 {
		metrics.DBAvgTime = metrics.DBTotalTime / time.Duration(metrics.Count)
	}

	mc.totalRequests++
	mc.totalDBQueries += int64(len(trace.DBQueries))
	mc.totalDBTime += trace.DBTotalTime

	// Calculate percentiles periodically (every 100 requests for this route)
	if metrics.Count%100 == 0 {
		mc.calculatePercentiles(routeKey)
	}
}


// GetTraces returns recent traces
func (mc *MetricsCollector) GetTraces(limit int, since time.Time) []RequestTrace {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	var filtered []RequestTrace
	for i := len(mc.traces) - 1; i >= 0 && len(filtered) < limit; i-- {
		if mc.traces[i].StartTime.After(since) {
			filtered = append([]RequestTrace{mc.traces[i]}, filtered...)
		}
	}
	return filtered
}

// GetRouteMetrics returns aggregated metrics for all routes
func (mc *MetricsCollector) GetRouteMetrics() map[string]*RouteMetrics {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	result := make(map[string]*RouteMetrics)
	for k, v := range mc.routeMetrics {
		// Create a copy to avoid race conditions
		metrics := *v
		result[k] = &metrics
	}
	return result
}

// GetSummary returns overall summary metrics
func (mc *MetricsCollector) GetSummary() map[string]interface{} {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	windowEnd := mc.windowStart.Add(mc.windowDuration)
	elapsed := time.Since(mc.windowStart)
	if elapsed > mc.windowDuration {
		elapsed = mc.windowDuration
	}

	var tps float64
	if elapsed.Seconds() > 0 {
		tps = float64(mc.totalRequests) / elapsed.Seconds()
	}

	// Calculate error rate safely (avoid division by zero)
	var errorRate float64
	if mc.totalRequests > 0 {
		errorRate = float64(mc.totalErrors) / float64(mc.totalRequests)
	}

	// Calculate average DB time safely (avoid division by zero)
	var avgDBTime time.Duration
	if mc.totalDBQueries > 0 {
		avgDBTime = mc.totalDBTime / time.Duration(mc.totalDBQueries)
	}

	return map[string]interface{}{
		"totalRequests":  mc.totalRequests,
		"totalErrors":    mc.totalErrors,
		"errorRate":      errorRate,
		"tps":            tps,
		"totalDBQueries": mc.totalDBQueries,
		"totalDBTime":    mc.totalDBTime.String(),
		"avgDBTime":      avgDBTime.String(),
		"windowStart":    mc.windowStart,
		"windowEnd":      windowEnd,
		"routeCount":     len(mc.routeMetrics),
		"traceCount":     len(mc.traces),
	}
}

// normalizeRoutePath normalizes a route path by replacing dynamic segments with placeholders
// Examples:
//   - /api/v1/user/507f1f77bcf86cd799439011/communities -> /api/v1/user/{id}/communities
//   - /api/v1/community/507f1f77bcf86cd799439011/members -> /api/v1/community/{id}/members
func normalizeRoutePath(path string) string {
	// Common patterns: ObjectIDs (24 hex chars), UUIDs, numeric IDs, etc.
	// Replace common ID patterns with {id}
	
	// ObjectID pattern: 24 hex characters
	objectIDPattern := regexp.MustCompile(`/[0-9a-fA-F]{24}/`)
	path = objectIDPattern.ReplaceAllString(path, "/{id}/")
	
	// UUID pattern
	uuidPattern := regexp.MustCompile(`/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}/`)
	path = uuidPattern.ReplaceAllString(path, "/{id}/")
	
	// Long numeric IDs (likely IDs)
	longNumericPattern := regexp.MustCompile(`/\d{10,}/`)
	path = longNumericPattern.ReplaceAllString(path, "/{id}/")
	
	// Clean up any double slashes
	path = strings.ReplaceAll(path, "//", "/")
	
	// Remove trailing slash if present (except root)
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	
	return path
}

// GetSlowestRoutes returns the slowest routes by average time with pagination
func (mc *MetricsCollector) GetSlowestRoutes(limit int, offset int) []*RouteMetrics {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	routes := make([]*RouteMetrics, 0, len(mc.routeMetrics))
	for _, metrics := range mc.routeMetrics {
		routes = append(routes, metrics)
	}

	// Sort by average time (descending)
	for i := 0; i < len(routes)-1; i++ {
		for j := i + 1; j < len(routes); j++ {
			if routes[i].AvgTime < routes[j].AvgTime {
				routes[i], routes[j] = routes[j], routes[i]
			}
		}
	}

	// Apply pagination
	if offset >= len(routes) {
		return []*RouteMetrics{}
	}
	
	end := offset + limit
	if end > len(routes) {
		end = len(routes)
	}
	
	return routes[offset:end]
}

// GetSlowestRoutesCount returns total count of routes
func (mc *MetricsCollector) GetSlowestRoutesCount() int {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return len(mc.routeMetrics)
}

// GetMostFrequentRoutes returns the most frequently called routes with pagination
func (mc *MetricsCollector) GetMostFrequentRoutes(limit int, offset int) []*RouteMetrics {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	routes := make([]*RouteMetrics, 0, len(mc.routeMetrics))
	for _, metrics := range mc.routeMetrics {
		routes = append(routes, metrics)
	}

	// Sort by count (descending)
	for i := 0; i < len(routes)-1; i++ {
		for j := i + 1; j < len(routes); j++ {
			if routes[i].Count < routes[j].Count {
				routes[i], routes[j] = routes[j], routes[i]
			}
		}
	}

	// Apply pagination
	if offset >= len(routes) {
		return []*RouteMetrics{}
	}
	
	end := offset + limit
	if end > len(routes) {
		end = len(routes)
	}
	
	return routes[offset:end]
}

// calculatePercentiles calculates P50, P95, P99 for a route
func (mc *MetricsCollector) calculatePercentiles(routeKey string) {
	metrics := mc.routeMetrics[routeKey]
	if metrics == nil {
		return
	}

	// Get all traces for this route
	var durations []time.Duration
	for _, trace := range mc.traces {
		if trace.Method+" "+trace.Path == routeKey {
			durations = append(durations, trace.TotalDuration)
		}
	}

	if len(durations) == 0 {
		return
	}

	// Sort durations
	for i := 0; i < len(durations)-1; i++ {
		for j := i + 1; j < len(durations); j++ {
			if durations[i] > durations[j] {
				durations[i], durations[j] = durations[j], durations[i]
			}
		}
	}

	// Calculate percentiles
	p50Idx := int(float64(len(durations)) * 0.50)
	p95Idx := int(float64(len(durations)) * 0.95)
	p99Idx := int(float64(len(durations)) * 0.99)

	if p50Idx < len(durations) {
		metrics.P50Time = durations[p50Idx]
	}
	if p95Idx < len(durations) {
		metrics.P95Time = durations[p95Idx]
	}
	if p99Idx < len(durations) {
		metrics.P99Time = durations[p99Idx]
	}
}

// cleanup removes old traces and resets window periodically
func (mc *MetricsCollector) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		mc.mu.Lock()
		now := time.Now()
		
		// Remove traces older than window
		cutoff := now.Add(-mc.windowDuration)
		var validTraces []RequestTrace
		for _, trace := range mc.traces {
			if trace.StartTime.After(cutoff) {
				validTraces = append(validTraces, trace)
			}
		}
		mc.traces = validTraces

		// Reset window if expired
		if now.Sub(mc.windowStart) > mc.windowDuration {
			mc.windowStart = now
			// Keep route metrics but could reset if needed
		}

		mc.mu.Unlock()
	}
}

// requestTraceContextKey is a type for context keys
type requestTraceContextKey struct{}

// requestTraceContext holds a trace being built during request processing
type requestTraceContext struct {
	trace *RequestTrace
	mu    sync.Mutex
}

// getRequestTraceFromContext gets the request trace from context
func getRequestTraceFromContext(ctx context.Context) *requestTraceContext {
	if val := ctx.Value(requestTraceContextKey{}); val != nil {
		return val.(*requestTraceContext)
	}
	return nil
}

// WithRequestTrace adds request trace to context
func WithRequestTrace(ctx context.Context, trace *RequestTrace) context.Context {
	return context.WithValue(ctx, requestTraceContextKey{}, &requestTraceContext{trace: trace})
}

// RecordDBQueryFromContext records a DB query from context (called from databases package)
// This is non-blocking and thread-safe - if context doesn't have trace, it's silently ignored
// The operation is very fast (just appending to a slice) so lock contention is minimal
func RecordDBQueryFromContext(ctx context.Context, operation, collection string, duration time.Duration, err error) {
	// Fast check - if no trace in context, silently return (metrics are best-effort)
	reqTrace := getRequestTraceFromContext(ctx)
	if reqTrace == nil || reqTrace.trace == nil {
		return
	}

	// Fast append operation - lock is held very briefly
	// This runs during the request, but the operation is so fast (< 1 microsecond)
	// that it won't impact performance. If there's contention, it's still better
	// than dropping metrics entirely.
	reqTrace.mu.Lock()
	trace := DBQueryTrace{
		Operation: operation,
		Collection: collection,
		Duration:   duration,
		Timestamp:  time.Now(),
	}
	if err != nil {
		trace.Error = err.Error()
	}
	reqTrace.trace.DBQueries = append(reqTrace.trace.DBQueries, trace)
	reqTrace.trace.DBTotalTime += duration
	reqTrace.mu.Unlock()
}

