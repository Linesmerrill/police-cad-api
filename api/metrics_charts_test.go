package api

import (
	"testing"
	"time"
)

func newCollector() *MetricsCollector {
	return &MetricsCollector{
		traces:         make([]RequestTrace, 0),
		maxTraces:      1000,
		routeMetrics:   make(map[string]*RouteMetrics),
		windowStart:    time.Now(),
		windowDuration: time.Hour,
	}
}

func addTrace(mc *MetricsCollector, t time.Time, method, path string, status int, durMs int64, dbCol string, dbMs int64) {
	tr := RequestTrace{
		Method:        method,
		Path:          path,
		Status:        status,
		StartTime:     t,
		TotalDuration: time.Duration(durMs) * time.Millisecond,
	}
	if dbCol != "" {
		tr.DBQueries = []DBQueryTrace{{
			Operation:  "find",
			Collection: dbCol,
			Duration:   time.Duration(dbMs) * time.Millisecond,
			Timestamp:  t,
		}}
		tr.DBTotalTime = time.Duration(dbMs) * time.Millisecond
	}
	mc.traces = append(mc.traces, tr)

	key := method + " " + path
	rm, ok := mc.routeMetrics[key]
	if !ok {
		rm = &RouteMetrics{Method: method, Path: path}
		mc.routeMetrics[key] = rm
	}
	rm.Count++
	if status >= 400 {
		rm.ErrorCount++
	}
	rm.AvgTime = time.Duration(durMs) * time.Millisecond
}

func TestGetChartsData_EmptyCollector(t *testing.T) {
	mc := newCollector()
	out := mc.GetChartsData(time.Now().Add(-time.Hour), 30, 10)

	if len(out.TimeSeries) != 30 {
		t.Fatalf("expected 30 buckets, got %d", len(out.TimeSeries))
	}
	for i, b := range out.TimeSeries {
		if b.Requests != 0 || b.Errors != 0 {
			t.Errorf("bucket %d should be empty, got requests=%d errors=%d", i, b.Requests, b.Errors)
		}
	}
	if out.TotalInWindow != 0 {
		t.Errorf("totalInWindow should be 0, got %d", out.TotalInWindow)
	}
	if len(out.LatencyHist) != len(histogramBins) {
		t.Errorf("histogram should have %d bins, got %d", len(histogramBins), len(out.LatencyHist))
	}
	for k := range out.StatusDist {
		if k != "2xx" && k != "3xx" && k != "4xx" && k != "5xx" {
			t.Errorf("unexpected status class key: %s", k)
		}
	}
}

func TestGetChartsData_BasicTraces(t *testing.T) {
	mc := newCollector()
	now := time.Now()
	since := now.Add(-time.Hour)

	// Spread traces across the window
	addTrace(mc, since.Add(5*time.Minute), "GET", "/api/v1/users", 200, 30, "users", 5)
	addTrace(mc, since.Add(10*time.Minute), "GET", "/api/v1/users", 200, 75, "users", 10)
	addTrace(mc, since.Add(15*time.Minute), "POST", "/api/v1/auth", 500, 1500, "users", 1200)
	addTrace(mc, since.Add(30*time.Minute), "DELETE", "/api/v1/widget", 404, 220, "widgets", 30)
	addTrace(mc, since.Add(45*time.Minute), "GET", "/api/v1/users", 200, 60, "users", 8)

	out := mc.GetChartsData(since, 30, 10)

	if out.TotalInWindow != 5 {
		t.Errorf("totalInWindow expected 5, got %d", out.TotalInWindow)
	}
	if out.ErrorsInWindow != 2 {
		t.Errorf("errorsInWindow expected 2, got %d", out.ErrorsInWindow)
	}

	if out.StatusDist["2xx"] != 3 {
		t.Errorf("2xx expected 3, got %d", out.StatusDist["2xx"])
	}
	if out.StatusDist["4xx"] != 1 {
		t.Errorf("4xx expected 1, got %d", out.StatusDist["4xx"])
	}
	if out.StatusDist["5xx"] != 1 {
		t.Errorf("5xx expected 1, got %d", out.StatusDist["5xx"])
	}

	if out.MethodDist["GET"] != 3 {
		t.Errorf("GET method count expected 3, got %d", out.MethodDist["GET"])
	}

	// 1500ms trace lands in 1–2s bucket
	found := false
	for _, b := range out.LatencyHist {
		if b.Label == "1–2s" && b.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 1–2s bin to have count=1, got %+v", out.LatencyHist)
	}

	// users collection has 4 queries
	users := false
	for _, c := range out.DBCollections {
		if c.Collection == "users" && c.Count == 4 {
			users = true
		}
	}
	if !users {
		t.Errorf("expected users collection with 4 queries, got %+v", out.DBCollections)
	}

	// Top errors must include POST /api/v1/auth and DELETE /api/v1/widget
	if len(out.TopErrors) != 2 {
		t.Errorf("expected 2 top error routes, got %d", len(out.TopErrors))
	}

	// Top volume: GET /api/v1/users should be #1 with count=3
	if len(out.TopVolume) == 0 || out.TopVolume[0].Path != "/api/v1/users" || out.TopVolume[0].Count != 3 {
		t.Errorf("expected top volume to lead with GET /api/v1/users count=3, got %+v", out.TopVolume)
	}
}

func TestGetChartsData_BucketBoundaries(t *testing.T) {
	// Verify a trace right at the window edge doesn't crash and lands somewhere sane.
	mc := newCollector()
	since := time.Now().Add(-time.Hour)
	addTrace(mc, since.Add(time.Millisecond), "GET", "/x", 200, 10, "", 0)
	addTrace(mc, time.Now().Add(-time.Second), "GET", "/x", 200, 10, "", 0)

	out := mc.GetChartsData(since, 30, 10)
	if out.TotalInWindow != 2 {
		t.Errorf("expected 2 in window, got %d", out.TotalInWindow)
	}
}

func TestGetChartsData_DropsOldTraces(t *testing.T) {
	mc := newCollector()
	now := time.Now()
	old := now.Add(-3 * time.Hour) // outside the requested window
	since := now.Add(-time.Hour)

	addTrace(mc, old, "GET", "/old", 200, 10, "", 0)
	addTrace(mc, since.Add(10*time.Minute), "GET", "/new", 200, 20, "", 0)

	out := mc.GetChartsData(since, 30, 10)
	if out.TotalInWindow != 1 {
		t.Errorf("expected 1 in window (old trace excluded), got %d", out.TotalInWindow)
	}
}

func TestGetChartsData_TopNRespected(t *testing.T) {
	mc := newCollector()
	since := time.Now().Add(-time.Hour)
	for i := 0; i < 25; i++ {
		path := "/r" + string(rune('a'+(i%26)))
		addTrace(mc, since.Add(time.Duration(i)*time.Minute), "GET", path, 200, int64(i+1), "", 0)
	}
	out := mc.GetChartsData(since, 30, 5)
	if len(out.TopVolume) > 5 {
		t.Errorf("topN=5 violated, got %d", len(out.TopVolume))
	}
}
