package api

import (
	"sort"
	"time"
)

// ChartTimeBucket is one bucket of the request-rate / latency-over-time series.
type ChartTimeBucket struct {
	Time     time.Time `json:"time"`
	Requests int       `json:"requests"`
	Errors   int       `json:"errors"`
	AvgMs    int64     `json:"avgMs"`
	P50Ms    int64     `json:"p50Ms"`
	P95Ms    int64     `json:"p95Ms"`
	P99Ms    int64     `json:"p99Ms"`
}

// ChartHistogramBucket is one bin of the latency histogram.
type ChartHistogramBucket struct {
	Label string `json:"label"`
	MinMs int64  `json:"minMs"`
	MaxMs int64  `json:"maxMs"` // 0 = unbounded
	Count int    `json:"count"`
}

// ChartCollectionStat is one DB-collection summary row.
type ChartCollectionStat struct {
	Collection string `json:"collection"`
	Count      int    `json:"count"`
	TotalMs    int64  `json:"totalMs"`
}

// ChartRouteStat is a route summary used by the top-errors / top-volume charts.
type ChartRouteStat struct {
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	Count      int64   `json:"count"`
	ErrorCount int64   `json:"errorCount"`
	ErrorRate  float64 `json:"errorRate"`
	AvgMs      int64   `json:"avgMs"`
}

// ChartsData bundles every chart's data for a single dashboard fetch.
type ChartsData struct {
	TimeSeries     []ChartTimeBucket      `json:"timeSeries"`
	StatusDist     map[string]int         `json:"statusDistribution"`
	MethodDist     map[string]int64       `json:"methodDistribution"`
	LatencyHist    []ChartHistogramBucket `json:"latencyHistogram"`
	DBCollections  []ChartCollectionStat  `json:"dbCollections"`
	TopErrors      []ChartRouteStat       `json:"topErrors"`
	TopVolume      []ChartRouteStat       `json:"topVolume"`
	Since          time.Time              `json:"since"`
	Until          time.Time              `json:"until"`
	BucketSeconds  int                    `json:"bucketSeconds"`
	TotalInWindow  int                    `json:"totalInWindow"`
	ErrorsInWindow int                    `json:"errorsInWindow"`
}

// histogramBins defines the latency histogram boundaries (max=0 means unbounded).
var histogramBins = []ChartHistogramBucket{
	{Label: "<50ms", MinMs: 0, MaxMs: 50},
	{Label: "50–100ms", MinMs: 50, MaxMs: 100},
	{Label: "100–250ms", MinMs: 100, MaxMs: 250},
	{Label: "250–500ms", MinMs: 250, MaxMs: 500},
	{Label: "500ms–1s", MinMs: 500, MaxMs: 1000},
	{Label: "1–2s", MinMs: 1000, MaxMs: 2000},
	{Label: "2s+", MinMs: 2000, MaxMs: 0},
}

// GetChartsData aggregates everything the charts panel needs in one pass.
// since: start of the window. bucketCount: how many time buckets to split it into.
// topN: how many rows for the top-routes charts.
func (mc *MetricsCollector) GetChartsData(since time.Time, bucketCount, topN int) ChartsData {
	if bucketCount <= 0 {
		bucketCount = 30
	}
	if topN <= 0 {
		topN = 10
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	now := time.Now()
	if since.After(now) || since.IsZero() {
		since = now.Add(-1 * time.Hour)
	}
	windowMs := now.Sub(since).Milliseconds()
	if windowMs < int64(bucketCount) {
		windowMs = int64(bucketCount)
	}
	bucketMs := windowMs / int64(bucketCount)
	if bucketMs < 1 {
		bucketMs = 1
	}

	// Pre-build empty buckets so the line chart has stable x-axis even with zero traffic.
	buckets := make([]ChartTimeBucket, bucketCount)
	bucketDurations := make([][]int64, bucketCount)
	for i := 0; i < bucketCount; i++ {
		buckets[i].Time = since.Add(time.Duration(int64(i)*bucketMs) * time.Millisecond)
	}

	statusDist := map[string]int{"2xx": 0, "3xx": 0, "4xx": 0, "5xx": 0}
	hist := make([]ChartHistogramBucket, len(histogramBins))
	copy(hist, histogramBins)

	collectionAgg := map[string]*ChartCollectionStat{}

	totalInWindow := 0
	errorsInWindow := 0

	for _, t := range mc.traces {
		if t.StartTime.Before(since) {
			continue
		}
		totalInWindow++
		ms := t.TotalDuration.Milliseconds()

		// Time bucket
		offsetMs := t.StartTime.Sub(since).Milliseconds()
		bIdx := int(offsetMs / bucketMs)
		if bIdx < 0 {
			bIdx = 0
		}
		if bIdx >= bucketCount {
			bIdx = bucketCount - 1
		}
		buckets[bIdx].Requests++
		bucketDurations[bIdx] = append(bucketDurations[bIdx], ms)
		if t.Status >= 400 {
			buckets[bIdx].Errors++
			errorsInWindow++
		}

		// Status class
		switch {
		case t.Status >= 200 && t.Status < 300:
			statusDist["2xx"]++
		case t.Status >= 300 && t.Status < 400:
			statusDist["3xx"]++
		case t.Status >= 400 && t.Status < 500:
			statusDist["4xx"]++
		case t.Status >= 500:
			statusDist["5xx"]++
		}

		// Latency histogram
		for i := range hist {
			b := hist[i]
			if ms >= b.MinMs && (b.MaxMs == 0 || ms < b.MaxMs) {
				hist[i].Count++
				break
			}
		}

		// DB collections
		for _, q := range t.DBQueries {
			key := q.Collection
			if key == "" {
				key = "(unknown)"
			}
			c, ok := collectionAgg[key]
			if !ok {
				c = &ChartCollectionStat{Collection: key}
				collectionAgg[key] = c
			}
			c.Count++
			c.TotalMs += q.Duration.Milliseconds()
		}
	}

	// Compute per-bucket percentiles
	for i := range buckets {
		ds := bucketDurations[i]
		if len(ds) == 0 {
			continue
		}
		sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
		var sum int64
		for _, v := range ds {
			sum += v
		}
		buckets[i].AvgMs = sum / int64(len(ds))
		buckets[i].P50Ms = ds[pctIndex(len(ds), 0.50)]
		buckets[i].P95Ms = ds[pctIndex(len(ds), 0.95)]
		buckets[i].P99Ms = ds[pctIndex(len(ds), 0.99)]
	}

	// Method distribution from per-route counters (full window of routeMetrics, not just traces)
	methodDist := map[string]int64{}
	for _, rm := range mc.routeMetrics {
		methodDist[rm.Method] += rm.Count
	}

	// Top routes
	allRoutes := make([]ChartRouteStat, 0, len(mc.routeMetrics))
	for _, rm := range mc.routeMetrics {
		var rate float64
		if rm.Count > 0 {
			rate = float64(rm.ErrorCount) / float64(rm.Count)
		}
		allRoutes = append(allRoutes, ChartRouteStat{
			Method:     rm.Method,
			Path:       rm.Path,
			Count:      rm.Count,
			ErrorCount: rm.ErrorCount,
			ErrorRate:  rate,
			AvgMs:      rm.AvgTime.Milliseconds(),
		})
	}

	topErrors := make([]ChartRouteStat, len(allRoutes))
	copy(topErrors, allRoutes)
	sort.Slice(topErrors, func(i, j int) bool {
		if topErrors[i].ErrorCount != topErrors[j].ErrorCount {
			return topErrors[i].ErrorCount > topErrors[j].ErrorCount
		}
		return topErrors[i].ErrorRate > topErrors[j].ErrorRate
	})
	topErrors = trimRoutes(topErrors, topN, true)

	topVolume := make([]ChartRouteStat, len(allRoutes))
	copy(topVolume, allRoutes)
	sort.Slice(topVolume, func(i, j int) bool { return topVolume[i].Count > topVolume[j].Count })
	if len(topVolume) > topN {
		topVolume = topVolume[:topN]
	}

	// DB collections: top by query count
	collections := make([]ChartCollectionStat, 0, len(collectionAgg))
	for _, c := range collectionAgg {
		collections = append(collections, *c)
	}
	sort.Slice(collections, func(i, j int) bool { return collections[i].Count > collections[j].Count })
	if len(collections) > topN {
		collections = collections[:topN]
	}

	return ChartsData{
		TimeSeries:     buckets,
		StatusDist:     statusDist,
		MethodDist:     methodDist,
		LatencyHist:    hist,
		DBCollections:  collections,
		TopErrors:      topErrors,
		TopVolume:      topVolume,
		Since:          since,
		Until:          now,
		BucketSeconds:  int(bucketMs / 1000),
		TotalInWindow:  totalInWindow,
		ErrorsInWindow: errorsInWindow,
	}
}

// trimRoutes truncates to topN. If dropZero, also drops entries with no errors.
func trimRoutes(in []ChartRouteStat, topN int, dropZero bool) []ChartRouteStat {
	if dropZero {
		out := in[:0]
		for _, r := range in {
			if r.ErrorCount > 0 {
				out = append(out, r)
			}
		}
		in = out
	}
	if len(in) > topN {
		in = in[:topN]
	}
	return in
}

// pctIndex returns the index for an n-element sorted slice for percentile p (0..1).
func pctIndex(n int, p float64) int {
	if n <= 0 {
		return 0
	}
	idx := int(float64(n) * p)
	if idx >= n {
		idx = n - 1
	}
	if idx < 0 {
		idx = 0
	}
	return idx
}
