package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
)

// formatRouteMetrics converts duration fields to milliseconds for JSON serialization
func formatRouteMetrics(routes []*api.RouteMetrics) []map[string]interface{} {
	result := make([]map[string]interface{}, len(routes))
	for i, route := range routes {
		result[i] = map[string]interface{}{
			"method":      route.Method,
			"path":        route.Path,
			"count":       route.Count,
			"errorCount":  route.ErrorCount,
			"avgTime":     route.AvgTime.Milliseconds(),
			"minTime":     route.MinTime.Milliseconds(),
			"maxTime":     route.MaxTime.Milliseconds(),
			"p50Time":     route.P50Time.Milliseconds(),
			"p95Time":     route.P95Time.Milliseconds(),
			"p99Time":     route.P99Time.Milliseconds(),
			"dbAvgTime":   route.DBAvgTime.Milliseconds(),
			"lastRequest": route.LastRequest,
		}
	}
	return result
}

// formatTraces converts trace durations to milliseconds
func formatTraces(traces []api.RequestTrace) []map[string]interface{} {
	result := make([]map[string]interface{}, len(traces))
	for i, trace := range traces {
		dbQueries := make([]map[string]interface{}, len(trace.DBQueries))
		for j, q := range trace.DBQueries {
			dbQueries[j] = map[string]interface{}{
				"operation":  q.Operation,
				"collection": q.Collection,
				"duration":   q.Duration.Milliseconds(),
				"error":      q.Error,
				"timestamp":  q.Timestamp,
			}
		}
		result[i] = map[string]interface{}{
			"requestId":      trace.RequestID,
			"method":         trace.Method,
			"path":           trace.Path,
			"status":         trace.Status,
			"startTime":      trace.StartTime,
			"endTime":        trace.EndTime,
			"totalDuration":  trace.TotalDuration.Milliseconds(),
			"middlewareTime": trace.MiddlewareTime.Milliseconds(),
			"handlerTime":    trace.HandlerTime.Milliseconds(),
			"dbQueries":      dbQueries,
			"dbTotalTime":    trace.DBTotalTime.Milliseconds(),
			"error":          trace.Error,
			"metadata":       trace.Metadata,
		}
	}
	return result
}

// MetricsHandler handles metrics dashboard requests
type MetricsHandler struct{}

// GetMetricsDashboard returns the metrics dashboard data
func (m MetricsHandler) GetMetricsDashboard(w http.ResponseWriter, r *http.Request) {
	metrics := api.GetMetrics()

	// Parse query parameters
	limit := 20 // Default: 20 routes per page
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	offset := 0
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if parsed, err := strconv.Atoi(offsetStr); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	since := time.Now().Add(-1 * time.Hour) // Default: last hour
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if parsed, err := time.ParseDuration(sinceStr); err == nil {
			since = time.Now().Add(-parsed)
		}
	}

	// Get data
	summary := metrics.GetSummary()
	routeMetrics := metrics.GetRouteMetrics()
	totalRoutes := metrics.GetSlowestRoutesCount()
	slowestRoutes := metrics.GetSlowestRoutes(limit, offset)
	mostFrequentRoutes := metrics.GetMostFrequentRoutes(limit, offset)
	traces := metrics.GetTraces(limit, since)

	// Convert route metrics to include duration in milliseconds for JSON
	slowestRoutesFormatted := formatRouteMetrics(slowestRoutes)
	mostFrequentRoutesFormatted := formatRouteMetrics(mostFrequentRoutes)

	// Build response
	response := map[string]interface{}{
		"summary": map[string]interface{}{
			"totalRequests":  summary["totalRequests"],
			"totalErrors":    summary["totalErrors"],
			"errorRate":      summary["errorRate"],
			"tps":            summary["tps"],
			"totalDBQueries": summary["totalDBQueries"],
			"totalDBTime":    summary["totalDBTime"],
			"avgDBTime":      summary["avgDBTime"],
			"windowStart":    summary["windowStart"],
			"windowEnd":      summary["windowEnd"],
			"routeCount":     summary["routeCount"],
			"traceCount":     summary["traceCount"],
		},
		"routes": map[string]interface{}{
			"all":            routeMetrics,
			"slowest":        slowestRoutesFormatted,
			"mostFrequent":   mostFrequentRoutesFormatted,
			"totalCount":     totalRoutes,
		},
		"recentTraces": formatTraces(traces),
		"pagination": map[string]interface{}{
			"limit":  limit,
			"offset": offset,
			"total":  totalRoutes,
			"hasMore": offset + limit < totalRoutes,
		},
		"filters": map[string]interface{}{
			"since": since,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// GetMetricsSummary returns just the summary metrics (lighter endpoint)
func (m MetricsHandler) GetMetricsSummary(w http.ResponseWriter, r *http.Request) {
	metrics := api.GetMetrics()
	summary := metrics.GetSummary()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(summary)
}

// GetRouteMetrics returns metrics for a specific route
func (m MetricsHandler) GetRouteMetrics(w http.ResponseWriter, r *http.Request) {
	metrics := api.GetMetrics()
	routeMetrics := metrics.GetRouteMetrics()

	// Get route from query param
	route := r.URL.Query().Get("route")
	if route == "" {
		config.ErrorStatus("route parameter required", http.StatusBadRequest, w, nil)
		return
	}

	routeData, exists := routeMetrics[route]
	if !exists {
		config.ErrorStatus("route not found", http.StatusNotFound, w, nil)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(routeData)
}

// GetSlowQueries returns slow database queries
func (m MetricsHandler) GetSlowQueries(w http.ResponseWriter, r *http.Request) {
	metrics := api.GetMetrics()
	
	limit := 100
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	since := time.Now().Add(-1 * time.Hour)
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if parsed, err := time.ParseDuration(sinceStr); err == nil {
			since = time.Now().Add(-parsed)
		}
	}

	minDuration := 100 * time.Millisecond
	if durationStr := r.URL.Query().Get("minDuration"); durationStr != "" {
		if parsed, err := time.ParseDuration(durationStr); err == nil {
			minDuration = parsed
		}
	}

	traces := metrics.GetTraces(limit*10, since) // Get more traces to filter
	
	// Extract slow DB queries
	var slowQueries []map[string]interface{}
	for _, trace := range traces {
		for _, query := range trace.DBQueries {
			if query.Duration >= minDuration {
				slowQueries = append(slowQueries, map[string]interface{}{
					"requestId":  trace.RequestID,
					"method":     trace.Method,
					"path":       trace.Path,
					"operation":  query.Operation,
					"collection": query.Collection,
					"duration":   query.Duration.String(),
					"error":      query.Error,
					"timestamp":  query.Timestamp,
				})
			}
		}
		if len(slowQueries) >= limit {
			break
		}
	}

	response := map[string]interface{}{
		"slowQueries": slowQueries,
		"count":       len(slowQueries),
		"filters": map[string]interface{}{
			"minDuration": minDuration.String(),
			"since":       since,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

