# MongoDB Connection Pool Exhaustion Diagnosis

## Symptoms You're Experiencing
- ✅ Everything works fine initially
- ❌ Suddenly everything starts failing
- ❌ Not a bot attack (Cloudflare is protecting)
- ❌ MongoDB issues despite doubling RAM/storage

**This is classic connection pool exhaustion!**

## Root Cause Analysis

### What's Happening:
1. **Connection Pool Fills Up**: All 300 connections (150 × 2 dynos) get used
2. **Slow Queries Hold Connections**: Queries taking 5-10s hold connections
3. **New Requests Queue**: New requests wait for available connections
4. **Cascade Failure**: When pool is exhausted, all new requests fail
5. **Recovery**: After timeouts, connections release, pool recovers

### Why It's Sudden:
- **Traffic Spike**: More concurrent users = more connections needed
- **Slow Query**: One slow query holds connection for 10s
- **Cascade**: 30 slow queries = 300 connections used = pool exhausted

## Immediate Diagnostic Steps

### 1. Check Current Connection Usage
```bash
# In MongoDB Atlas → Metrics → Connections
# Look for:
# - Current: Should be < 250 (you have 300 max)
# - Peak: If hitting 300, that's the problem!
# - Wait Time: If > 100ms, connections are queuing
```

### 2. Check Active Operations
```javascript
// In MongoDB shell
db.currentOp({
  "active": true,
  "secs_running": { "$gt": 5 }
})

// Look for:
// - Long-running queries (> 5s)
// - Queries without indexes (COLLSCAN)
// - Queries holding connections
```

### 3. Check Connection Pool Metrics
```javascript
// In MongoDB shell
db.serverStatus().connections

// Look for:
// - current: Current active connections
// - available: Available connections
// - active: Active connections
```

### 4. Check Your Dyno Count
```bash
heroku ps --app your-app-name
# Count "web" dynos
# If 2 dynos: 150 × 2 = 300 connections (at limit!)
# If 3 dynos: 150 × 3 = 450 connections (EXCEEDS M10 limit!)
```

## Immediate Fixes

### Fix 1: Reduce MaxPoolSize (If Multiple Dynos)
```go
// In databases/database.go, line 112
// Current: SetMaxPoolSize(150)
// For 2 dynos: 150 × 2 = 300 (at limit!)

// Change to:
SetMaxPoolSize(100).  // 2 × 100 = 200 (safer, leaves buffer)
// OR
SetMaxPoolSize(75).   // 2 × 75 = 150 (very safe, room for spikes)
```

### Fix 2: Reduce Query Timeout (Release Connections Faster)
```go
// In api/context_helper.go, line 9
// Current: const QueryTimeout = 10 * time.Second

// Change to:
const QueryTimeout = 5 * time.Second  // Release connections faster
```

### Fix 3: Check for Connection Leaks
Look for handlers that don't close cursors:
```go
// BAD - Connection leak!
cursor, err := db.Find(ctx, filter)
// Missing: defer cursor.Close(ctx)

// GOOD - Properly closed
cursor, err := db.Find(ctx, filter)
defer cursor.Close(ctx)  // Always close!
```

### Fix 4: Add Connection Pool Monitoring
Add this to your metrics dashboard to track:
- Active connections
- Connection wait time
- Connection pool exhaustion events

## Why This Happens Despite More RAM/Storage

**RAM/Storage ≠ Connection Capacity**

- **M10 Connection Limit**: 1,500 per node (but you're using PRIMARY only)
- **Your Pool**: 300 connections (150 × 2 dynos)
- **Problem**: Not RAM/storage, but **connection pool exhaustion**

**More RAM helps with:**
- Query performance (less disk I/O)
- Caching (faster queries)

**More RAM does NOT help with:**
- Connection pool limits
- Concurrent connection capacity
- Connection wait times

## Prevention Strategies

### 1. Monitor Connection Pool Usage
Add alerts for:
- Connection count > 250 (80% of 300)
- Connection wait time > 100ms
- Connection errors

### 2. Optimize Slow Queries
- Add indexes (see `scripts/create_indexes.js`)
- Reduce query timeouts (5s instead of 10s)
- Use aggregation pipelines efficiently

### 3. Implement Circuit Breaker
If connection pool is exhausted:
- Return 503 (Service Unavailable) immediately
- Don't queue requests (they'll timeout anyway)
- Log the event for monitoring

### 4. Consider Connection Pooling Strategy
- **Current**: Each dyno has its own pool
- **Better**: Shared connection pool (requires Redis or similar)
- **Best**: Upgrade to M20+ (higher connection limits)

## Long-term Solutions

### Option 1: Upgrade to M20+
- **M20 Connection Limit**: 3,500 per node
- **Your Pool**: 300 connections (plenty of room)
- **Cost**: ~2x M10

### Option 2: Reduce MaxPoolSize
- **Current**: 150 per dyno
- **Recommended**: 75-100 per dyno
- **Trade-off**: May need to handle connection wait times

### Option 3: Scale Down Dynos
- **Current**: 2 dynos
- **Option**: 1 dyno (if traffic allows)
- **Pool**: 150 connections (safer)

### Option 4: Add Redis Cache
- Reduce database load
- Fewer queries = fewer connections needed
- Cache frequently accessed data

## Emergency Response

If everything is failing right now:

1. **Reduce MaxPoolSize immediately**:
   ```go
   SetMaxPoolSize(75)  // Reduces pressure
   ```

2. **Scale down dynos** (if possible):
   ```bash
   heroku ps:scale web=1
   ```

3. **Check for stuck queries**:
   ```javascript
   db.currentOp({"active": true, "secs_running": {"$gt": 10}})
   ```

4. **Kill long-running queries** (if safe):
   ```javascript
   db.killOp(<opid>)
   ```

## Monitoring Checklist

- [ ] Check MongoDB Atlas → Metrics → Connections
- [ ] Check for long-running queries (`db.currentOp()`)
- [ ] Check dyno count (`heroku ps`)
- [ ] Review slow query log in MongoDB Atlas
- [ ] Check replication lag (should be < 1s)
- [ ] Monitor connection wait times
- [ ] Set up alerts for connection pool exhaustion

