# API Performance Metrics & Monitoring

This document describes the performance metrics and monitoring system implemented to track API performance, identify bottlenecks, and optimize slow endpoints.

## Overview

The metrics system tracks:
- **Request timing**: Total, middleware, handler, and database query times
- **Request counts**: Per route with error rates
- **Database performance**: Individual query timing and slow query detection
- **TPS (Transactions Per Second)**: Real-time throughput metrics
- **Percentiles**: P50, P95, P99 response times per route

## Endpoints

### Metrics Dashboard (Web UI)
- **URL**: `/metrics-dashboard`
- **Description**: Interactive HTML dashboard for viewing metrics
- **Features**:
  - Real-time metrics summary (TPS, error rate, DB queries)
  - Slowest routes table
  - Most frequent routes table
  - Slow database queries viewer
  - Auto-refresh every 10 seconds

### Metrics API Endpoints

#### Get Full Metrics Dashboard Data
```
GET /api/v2/metrics?since=1h&limit=50
```
- `since`: Time range (e.g., "5m", "15m", "1h", "6h")
- `limit`: Maximum number of traces/routes to return

**Response**: Complete metrics including summary, route metrics, and recent traces

#### Get Metrics Summary
```
GET /api/v2/metrics/summary
```
**Response**: Lightweight summary with TPS, error rate, DB stats

#### Get Route Metrics
```
GET /api/v2/metrics/route?route=GET /api/v1/user/{userId}/communities
```
**Response**: Detailed metrics for a specific route

#### Get Slow Database Queries
```
GET /api/v2/metrics/slow-queries?since=1h&minDuration=100ms&limit=100
```
- `since`: Time range
- `minDuration`: Minimum query duration to include
- `limit`: Maximum number of queries to return

**Response**: List of slow database queries with request context

## Metrics Collected

### Request-Level Metrics
- Request ID (UUID)
- HTTP method and path
- Status code
- Total duration
- Middleware time
- Handler time
- Database query count and total time
- Error messages (if any)

### Route-Level Aggregates
- Request count
- Error count and rate
- Average, min, max response times
- P50, P95, P99 percentiles
- Average database query time
- Last request timestamp

### Database Query Metrics
- Operation type (FindOne, Find, InsertOne, etc.)
- Collection name
- Duration
- Error (if any)
- Timestamp

## Usage Examples

### View Dashboard
Open in browser: `https://your-api.com/metrics-dashboard`

### Check Current TPS
```bash
curl https://your-api.com/api/v2/metrics/summary | jq '.tps'
```

### Find Slowest Routes
```bash
curl https://your-api.com/api/v2/metrics?since=15m | jq '.routes.slowest'
```

### Identify Slow Database Queries
```bash
curl "https://your-api.com/api/v2/metrics/slow-queries?minDuration=500ms&since=1h" | jq '.slowQueries'
```

## Performance Targets

Based on your requirements:
- **Target TPS**: Monitor to ensure it stays reasonable under load
- **Target Response Time**: Most calls should be < 100ms
- **Database Query Time**: Should be < 50ms for most queries
- **Timeout Threshold**: Requests taking > 1 second are logged as warnings

## Configuration

Metrics are initialized in `main.go`:
```go
api.InitMetrics(10000, 1*time.Hour) // 10k traces, 1 hour window
```

You can adjust:
- **Max traces**: Number of request traces to keep in memory
- **Window duration**: How long to keep metrics (older data is cleaned up)

## Performance Guarantees

**CRITICAL: Metrics collection is designed to NEVER impact production performance.**

- ✅ **Non-blocking**: All metrics recording is asynchronous via buffered channels
- ✅ **Best-effort**: If metrics buffer is full, traces are dropped silently (this is intentional)
- ✅ **Fire-and-forget**: Metrics are queued and processed in background goroutines
- ✅ **Fault-tolerant**: If metrics processing fails, it never affects requests
- ✅ **Zero overhead**: Missing a metric is acceptable; slowing down a request is not

**Design Philosophy**: Production request flow is always prioritized over metrics collection. If there's any conflict, metrics are dropped to ensure zero impact on response times.

## Database Query Tracking

All database operations are automatically tracked:
- `FindOne`, `Find`, `InsertOne`, `UpdateOne`, `DeleteOne`
- `UpdateMany`, `CountDocuments`, `Aggregate`
- `FindOneAndUpdate`

Query timing is recorded in the request context and aggregated per route.

## Troubleshooting

### High TPS but Slow Responses
- Check database query times in slow queries endpoint
- Look for routes with high DB time vs handler time
- May indicate database connection pool exhaustion

### High Error Rate
- Check error messages in recent traces
- Look for patterns in failing routes
- May indicate timeout issues or database problems

### Slow Database Queries
- Use `/api/v2/metrics/slow-queries` to identify problematic queries
- Check for missing indexes
- Consider query optimization or caching

### Memory Usage
- Reduce `maxTraces` if memory is a concern
- Reduce `windowDuration` to keep less historical data
- Metrics are stored in-memory, so adjust based on your server capacity

## Integration with Heroku

The metrics system works well with Heroku's metrics:
- Use Heroku metrics for infrastructure (CPU, memory, dyno metrics)
- Use this system for application-level performance (route timing, DB queries)
- Combine both for complete visibility

## Next Steps

1. **Monitor**: Watch the dashboard during high traffic periods
2. **Identify**: Find routes consistently > 100ms
3. **Investigate**: Check slow queries and optimize database operations
4. **Optimize**: Add indexes, optimize queries, add caching where needed
5. **Iterate**: Continue monitoring and optimizing

## Example Optimization Workflow

1. Open `/metrics-dashboard` during peak traffic
2. Identify routes with P95 > 100ms in "Slowest Routes"
3. Click "View Slow Queries" to see database queries for those routes
4. Check if queries are slow due to:
   - Missing indexes (add indexes)
   - Inefficient queries (optimize queries)
   - Too many queries (add caching or batch operations)
5. Deploy optimizations and monitor improvement

