# MongoDB Atlas M10 Cluster Considerations

## Important Limits & Specifications

### Connection Limits (Updated January 2026)
- **Max Connections**: **1,500 concurrent connections per node** (UPDATED - was ~350 per cluster)
- **Connection Rate Limit**: **20 new connections per second per node** (CRITICAL)
  - M10 has 3 nodes (1 PRIMARY + 2 SECONDARY)
  - Total: ~60 new connections/second across all nodes
  - **With 2 dynos × 150 MaxPoolSize = 300 connections** - well within 1,500 per node limit

### Current Configuration
- **MaxPoolSize**: 600 per dyno (2 dynos × 600 = 1,200 total connections)
- **MinPoolSize**: 20 (keeps pool warm)
- **MaxConnecting**: 10 (limits concurrent connection attempts)
- **MaxConnIdleTime**: 30 seconds (closes idle connections)
- **Query Timeout**: 10 seconds (via `api.WithQueryTimeout`)

**Note**: MaxPoolSize is a **ceiling** - connections are only created when needed, up to this limit. With 10s query timeouts, connections release quickly, so a high limit is safe.

### Why These Settings Work
1. **600 max pool × 2 dynos = 1,200 connections** - 80% of M10's 1,500 per node limit (leaves 300 buffer)
2. **MaxPoolSize is a ceiling** - connections only created when needed, up to this limit
3. **10s query timeouts** ensure connections release quickly (prevents indefinite hangs)
4. **20 min pool** keeps connections warm for faster queries
5. **30s idle timeout** closes unused connections (releases resources)
6. **Read preference set to Primary()** - connects primarily to PRIMARY node (within 1,500 limit)
7. **10 MaxConnecting** prevents overwhelming MongoDB during connection spikes

### Performance Impact
**✅ Verified Results (January 2026):**
- **Error reduction**: > 45% decrease in errors after increasing MaxPoolSize from 100 to 600 per dyno
- **Connection pool exhaustion**: Eliminated - pool now has sufficient capacity for traffic spikes
- **Actual usage**: Pool scales from ~40 (low traffic) to 1,200 (high traffic) automatically
- **MongoDB limit**: 1,200 total connections = 80% of 1,500 limit (safe buffer of 300)

**Key Insight**: The previous conservative setting (100 per dyno = 200 total) was causing connection pool exhaustion during traffic spikes. Increasing to 600 per dyno (1,200 total) provides sufficient headroom while staying well within M10 limits.

### Connection Pool Behavior
- **MaxPoolSize (600)**: Maximum connections per dyno (ceiling, not guaranteed)
- **MinPoolSize (20)**: Minimum connections kept warm
- **Actual usage**: Will be between 20-600 per dyno depending on traffic
- **With 2 dynos**: Can scale from 40 (low traffic) to 1,200 (high traffic) connections
- **MongoDB limit**: 1,500 per node, so 1,200 total = 80% (safe buffer of 300)

## Performance Considerations

### What We Fixed
- ✅ All handlers now use `api.WithQueryTimeout()` (10s max)
- ✅ No more `context.Background()` or `context.TODO()` hanging indefinitely
- ✅ Connections release quickly instead of holding for 60-120s
- ✅ Connection pool exhaustion fixed

### Monitoring
Watch for these in your metrics:
- **Connection pool timeouts**: Should be zero now
- **Average DB query time**: Should be <1s for most queries
- **Connection wait time**: Should be minimal

## When to Upgrade

Consider upgrading from M10 if you see:
- Consistent connection pool exhaustion (even with 200 max)
- Connection rate limit errors (exceeding 20/sec per node)
- Need for more storage (>10GB)
- Need for better performance (M20+ has more RAM/CPU)

## Best Practices

1. **Always use timeouts** - Never use `context.Background()` for DB operations
2. **Monitor connection usage** - Keep an eye on pool metrics
3. **Index optimization** - Ensure queries use indexes (see `MONGODB_INDEXES.md`)
4. **Connection pooling** - Let the driver manage connections, don't create new clients per request
5. **Read preferences** - Using `PrimaryPreferred` for resilience during migrations

## WebSocket Routes

- `/ws/notifications` is excluded from metrics (long-lived connections are expected)
- WebSocket connections don't count against MongoDB connection pool (they're HTTP connections upgraded to WebSocket)
- They maintain persistent connections for real-time notifications

