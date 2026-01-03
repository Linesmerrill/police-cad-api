# MongoDB Performance Diagnosis & Immediate Fixes

## 🚨 IMMEDIATE ACTIONS (Do These First)

### 1. Check MongoDB Atlas Performance Advisor

**Steps:**
1. Go to MongoDB Atlas → Your Cluster → **Performance Advisor** tab
2. Look for:
   - **Slow Queries** (queries taking > 1s)
   - **Index Suggestions** (queries that need indexes)
   - **Query Targeting Alerts** (scanned/returned > 1000)

**What to Look For:**
- Queries showing "COLLSCAN" (collection scan) - these are slow
- Queries showing "IXSCAN" (index scan) - these are fast
- Queries with high "executionTimeMillis"

**Fix:**
- Click "Create Index" on any suggested indexes
- Or run the index creation script: `mongo < scripts/create_indexes.js`

### 2. Verify Indexes Are Being Used

**Check Query Execution Plans:**

```javascript
// In MongoDB Atlas → Data Explorer → Run this query
// Replace with your actual query

// Example: Check if community queries use index
db.communities.find({ "community.visibility": "public" }).explain("executionStats")

// Look for:
// - "stage": "IXSCAN" = ✅ Using index (GOOD)
// - "stage": "COLLSCAN" = ❌ Full collection scan (BAD)
// - "executionTimeMillis": Should be < 100ms
```

**Check All Critical Collections:**

```javascript
// Check users collection
db.users.find({ "user.email": "test@example.com" }).explain("executionStats")

// Check communities collection  
db.communities.find({ "community.tags": "Xbox" }).explain("executionStats")

// Check vehicles collection
db.vehicles.find({ "vehicle.userID": "some-id" }).explain("executionStats")
```

**If you see COLLSCAN:**
1. Check if index exists: `db.collection.getIndexes()`
2. Create missing index (see `scripts/create_indexes.js`)
3. Verify index is used: Run explain again

### 3. Monitor Replication Lag

**Steps:**
1. Go to MongoDB Atlas → Your Cluster → **Metrics** tab
2. Click **Replication Lag** chart
3. Check all 3 nodes (PRIMARY + 2 SECONDARY)

**What to Look For:**
- **PRIMARY**: Should be 0ms (always)
- **SECONDARY nodes**: Should be < 1s (ideally < 100ms)
- **If lag > 1s**: This is causing stale reads and slow queries

**Current Status:**
- Your code is set to `Primary()` reads only (good!)
- This bypasses lagging secondaries
- But if PRIMARY is overloaded, queries will still be slow

**Fix if Lag is High:**
```javascript
// In MongoDB shell, check replication status
rs.printReplicationInfo()
rs.printSlaveReplicationInfo()

// If lag persists:
// 1. Check disk I/O on SECONDARY nodes
// 2. Check network latency between nodes
// 3. Consider upgrading cluster tier (M10 → M20)
```

### 4. Check Connection Pool Metrics

**Steps:**
1. Go to MongoDB Atlas → Your Cluster → **Metrics** tab
2. Click **Connections** chart
3. Check:
   - **Current Connections**: Should be < 300 (M10 limit is ~350)
   - **Peak Connections**: Should not hit 350
   - **Connection Wait Time**: Should be < 100ms

**Current Configuration:**
- MaxPoolSize: 200 per dyno
- If you have 2 dynos: 200 × 2 = 400 connections (EXCEEDS LIMIT!)

**Check Your Dyno Count:**
```bash
heroku ps --app your-app-name
# Count the "web" dynos
```

**Fix if Connection Pool Exhausted:**

**Option A: Reduce MaxPoolSize (if multiple dynos)**
```go
// In databases/database.go, line 112
SetMaxPoolSize(100).  // For 2 dynos: 200 total (safe)
// OR
SetMaxPoolSize(150).  // For 2 dynos: 300 total (safe)
```

**Option B: Check for Connection Leaks**
```javascript
// In MongoDB shell, check active connections
db.serverStatus().connections

// Check for long-running operations holding connections
db.currentOp({
  "active": true,
  "secs_running": { "$gt": 5 }
})
```

## 🔧 IMMEDIATE CODE FIXES

### Fix 1: Reduce MaxPoolSize if Multiple Dynos

**Check dyno count first:**
```bash
heroku ps --app your-app-name
```

**If you have 2+ dynos, reduce MaxPoolSize:**

```go
// In databases/database.go, line 112
// Change from:
SetMaxPoolSize(200).

// To (for 2 dynos):
SetMaxPoolSize(100).  // 2 × 100 = 200 total (safe)

// Or (for 3 dynos):
SetMaxPoolSize(80).   // 3 × 80 = 240 total (safe)
```

### Fix 2: Temporarily Increase Query Timeout

**If queries are legitimately slow (not hanging):**

```go
// In api/context_helper.go, line 9
// Change from:
const QueryTimeout = 10 * time.Second

// To (temporarily):
const QueryTimeout = 15 * time.Second  // Give more time for slow queries
```

**⚠️ WARNING:** Only do this if queries are legitimately slow, not hanging. If they're hanging, fix the root cause instead.

### Fix 3: Check for Stuck Queries

**Run this in MongoDB shell:**
```javascript
// Find queries running longer than 5 seconds
db.currentOp({
  "active": true,
  "secs_running": { "$gt": 5 }
})

// Kill stuck queries (if needed)
db.killOp(<opid>)
```

## 📊 MONITORING CHECKLIST

### Daily Checks:
- [ ] MongoDB Atlas → Metrics → Connections (should be < 300)
- [ ] MongoDB Atlas → Metrics → Replication Lag (should be < 1s)
- [ ] MongoDB Atlas → Performance Advisor → Slow Queries
- [ ] Your metrics dashboard → Average DB query time (should be < 1s)

### Weekly Checks:
- [ ] MongoDB Atlas → Metrics → Disk Usage (should be < 80%)
- [ ] MongoDB Atlas → Alerts → Review all alerts
- [ ] Check for new index suggestions in Performance Advisor

## 🎯 QUICK WINS

### 1. Create Missing Indexes (5 minutes)
```bash
# Connect to MongoDB and run:
mongo "your-connection-string" < scripts/create_indexes.js
```

### 2. Reduce MaxPoolSize if Multiple Dynos (2 minutes)
```go
// Edit databases/database.go
SetMaxPoolSize(100).  // Adjust based on dyno count
```

### 3. Check Current Connections (1 minute)
```bash
# In MongoDB Atlas → Metrics → Connections
# Should be < 300 for M10 cluster
```

## 🚨 EMERGENCY FIXES

### If Cluster is Severely Degraded:

**1. Reduce Connection Pool Immediately:**
```go
SetMaxPoolSize(50).  // Drastically reduce to free up connections
```

**2. Kill Stuck Queries:**
```javascript
// In MongoDB shell
db.currentOp({"active": true, "secs_running": {"$gt": 10}}).forEach(
  function(op) { db.killOp(op.opid); }
)
```

**3. Scale Down Dynos Temporarily:**
```bash
heroku ps:scale web=1 --app your-app-name
# Reduces total connections: 1 dyno × 200 = 200 (safe)
```

**4. Check Disk Space:**
```javascript
// In MongoDB shell
db.stats()
// If disk usage > 90%, you need to free space or upgrade
```

## 📈 EXPECTED METRICS (After Fixes)

- **Connection Count**: < 300 (for M10)
- **Replication Lag**: < 1s
- **Average Query Time**: < 1s
- **Query Timeout Rate**: < 1%
- **Index Usage**: > 95% of queries using indexes

## 🔍 TROUBLESHOOTING

### Problem: Queries timing out at exactly 10s
**Cause:** MongoDB is taking longer than 10s to respond
**Fix:** 
1. Check if indexes are being used (see #2 above)
2. Check replication lag (see #3 above)
3. Check connection pool exhaustion (see #4 above)
4. Consider increasing timeout temporarily (see Fix 2)

### Problem: High connection count (> 300)
**Cause:** Multiple dynos × MaxPoolSize exceeds limit
**Fix:** Reduce MaxPoolSize based on dyno count (see Fix 1)

### Problem: High replication lag (> 1s)
**Cause:** SECONDARY nodes can't keep up with PRIMARY
**Fix:** 
1. Check disk I/O on SECONDARY nodes
2. Check network latency
3. Consider upgrading cluster tier
4. Your code already forces PRIMARY reads (good!)

### Problem: Queries using COLLSCAN instead of IXSCAN
**Cause:** Missing indexes
**Fix:** Create indexes from `scripts/create_indexes.js`

