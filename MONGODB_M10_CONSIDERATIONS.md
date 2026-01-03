# MongoDB Atlas M10 Cluster Considerations

## Important Limits & Specifications

### Connection Limits
- **Max Connections**: ~350 connections per cluster
- **Connection Rate Limit**: **20 new connections per second per node** (CRITICAL)
  - M10 has 3 nodes (1 PRIMARY + 2 SECONDARY)
  - Total: ~60 new connections/second across all nodes
  - **This is why we increased MaxPoolSize to 200** - it's safe and gives headroom

### Current Configuration
- **MaxPoolSize**: 200 (increased from 100)
- **MinPoolSize**: 20 (increased from 10)
- **MaxConnecting**: 10 (increased from 5)
- **MaxConnIdleTime**: 30 seconds
- **Query Timeout**: 10 seconds (via `api.WithQueryTimeout`)

### Why These Settings Work
1. **200 max pool** is well under the 350 connection limit
2. **10s query timeouts** ensure connections release quickly
3. **20 min pool** keeps connections warm without wasting resources
4. **30s idle timeout** closes unused connections

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

