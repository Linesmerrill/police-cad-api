# MongoDB Atlas Degraded Status Troubleshooting

## Current Situation
- **Connections**: 182 (within M10 limit of ~350)
- **Status**: All 3 nodes showing warning icons (including PRIMARY)
- **Disk Usage**: 6.6 GB / 10 GB (66% - getting high)
- **Operations**: R: 30.3, W: 10.7 ops/sec (low to moderate)

## Critical: Warning Icons on All Nodes

Warning icons on PRIMARY and both SECONDARY nodes indicate cluster health issues. This is likely causing:
- Slow query responses
- Connection timeouts
- "100% DB error" status
- 15s+ load times

## Immediate Actions

### 1. Check MongoDB Atlas Alerts
Go to **Alerts** tab in MongoDB Atlas and check for:
- Replication lag warnings
- Disk space alerts
- CPU/Memory pressure
- Network connectivity issues
- Health check failures

### 2. Check Replication Lag
In MongoDB Atlas, go to **Metrics** → **Replication Lag**:
- **Normal**: < 1 second
- **Warning**: 1-5 seconds
- **Critical**: > 5 seconds

High replication lag can cause:
- Slow reads from SECONDARY nodes
- Write conflicts
- Data inconsistency

### 3. Check Heroku Dyno Count
**CRITICAL**: Each Heroku dyno creates its own connection pool!

If you have **2 dynos**:
- Each dyno: MaxPoolSize 200 = 400 total connections
- Current: 182 connections = ~91 per dyno (reasonable)

If you have **3+ dynos**:
- 3 dynos × 200 = 600 connections (EXCEEDS M10 limit of 350!)
- This would cause connection pool exhaustion and degraded status

**Check dyno count:**
```bash
heroku ps --app your-app-name
```

**If multiple dynos, reduce MaxPoolSize:**
```go
// For 2 dynos: MaxPoolSize 150 (300 total, under 350 limit)
// For 3 dynos: MaxPoolSize 100 (300 total, under 350 limit)
SetMaxPoolSize(150).  // Adjust based on dyno count
```

### 4. Monitor Disk Usage
**Current: 6.6 GB / 10 GB (66%)**

**Actions:**
- Check for large collections or unindexed queries
- Consider archiving old data
- Plan for disk space upgrade if approaching 80%+

### 5. Check Connection Pool Metrics
In MongoDB Atlas → **Metrics** → **Connections**:
- **Current**: 182
- **Peak**: Check if it's hitting limits
- **Wait Time**: Should be minimal (< 100ms)

## Connection Pool Calculation

**Formula:**
```
Total Connections = Number of Dynos × MaxPoolSize
```

**M10 Limit:** ~350 connections

**Recommended Settings:**

| Dynos | MaxPoolSize | Total Connections | Status |
|-------|-------------|-------------------|--------|
| 1     | 200         | 200               | ✅ Safe |
| 2     | 150         | 300               | ✅ Safe |
| 3     | 100         | 300               | ✅ Safe |
| 4     | 80          | 320               | ⚠️ Close to limit |
| 5+    | 60          | 300+              | ⚠️ Consider M20 upgrade |

## Current Configuration Analysis

**Current Settings:**
- MaxPoolSize: 200
- MinPoolSize: 20
- MaxConnecting: 10
- Query Timeout: 10s

**If 2 dynos are running:**
- Total pool capacity: 400 connections
- Current usage: 182 connections
- **This exceeds M10 limit and could cause degraded status!**

## Recommended Fixes

### Option 1: Reduce MaxPoolSize (If Multiple Dynos)
```go
// In databases/database.go
SetMaxPoolSize(100).  // For 2 dynos: 200 total (safe)
// OR
SetMaxPoolSize(150).  // For 2 dynos: 300 total (safe)
```

### Option 2: Check for Connection Leaks
Look for handlers that:
- Don't close cursors (`defer cursor.Close(ctx)`)
- Use `context.Background()` without timeouts
- Create new clients per request

### Option 3: Monitor Query Performance
Slow queries can hold connections longer:
- Check slow query log in MongoDB Atlas
- Ensure all indexes are created (see `MONGODB_INDEXES.md`)
- Review query patterns in metrics dashboard

## Next Steps

1. **Check Heroku dyno count** - Most critical!
2. **Review MongoDB Atlas alerts** - See what warnings are about
3. **Check replication lag** - Should be < 1s
4. **Monitor disk usage** - Plan for cleanup if > 70%
5. **Adjust MaxPoolSize** if multiple dynos are running
6. **Review slow queries** - Optimize or add indexes

## Emergency Actions

If cluster is severely degraded:

1. **Reduce MaxPoolSize immediately:**
   ```go
   SetMaxPoolSize(100)  // Reduces connection pressure
   ```

2. **Increase query timeout temporarily:**
   ```go
   // In api/context_helper.go
   const QueryTimeout = 15 * time.Second  // Give more time for slow queries
   ```

3. **Check for stuck queries:**
   ```javascript
   // In MongoDB shell
   db.currentOp({"active": true, "secs_running": {"$gt": 5}})
   ```

4. **Consider scaling down dynos** if connection pool is exhausted

## Long-term Solutions

1. **Upgrade to M20+** if consistently hitting connection limits
2. **Implement connection pooling at application level** (if using multiple services)
3. **Add Redis cache** to reduce database load
4. **Archive old data** to reduce disk usage
5. **Optimize queries** with proper indexes

