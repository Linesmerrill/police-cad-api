# MongoDB Large Collections Analysis

## Your Collection Sizes

Based on the health check output:

| Collection | Documents | Size | Priority |
|------------|-----------|------|----------|
| **vehicles** | 2.2M | 787 MB | 🔴 Critical |
| **civilians** | 1.7M | 691 MB | 🔴 Critical |
| **firearms** | 1.3M | 385 MB | 🔴 Critical |
| **sessions** | 1.1M | 202 MB | 🟡 Medium |
| **communities** | 156K | 342 MB | 🟡 Medium |
| **users** | 804K | 307 MB | 🟡 Medium |
| **tickets** | 418K | 148 MB | 🟢 Low |
| **arrestreports** | 266K | 140 MB | 🟢 Low |
| **licenses** | 360K | 107 MB | 🟢 Low |
| **warrants** | 203K | 60 MB | 🟢 Low |

## Why Large Collections Cause Connection Pool Exhaustion

### The Problem:
1. **Slow Queries**: Large collections without indexes = full collection scans
2. **Long Query Times**: Scanning 2.2M documents takes 5-10+ seconds
3. **Connection Hold Time**: Each slow query holds a connection for 7-10s
4. **Cascade Failure**: 30 slow queries = 200 connections used = pool exhausted

### Example:
- **vehicles** collection: 2.2M documents
- Query without index: Scans all 2.2M documents = 5-10s
- 30 concurrent users querying vehicles = 30 connections held for 10s
- Pool exhausted = all new requests fail

## Critical Indexes Needed

### Already Created (from `scripts/create_indexes.js`):
- ✅ `vehicle.userID` + `vehicle.activeCommunityID`
- ✅ `vehicle.registeredOwnerID` + `vehicle.linkedCivilianID`
- ✅ `civilian.userID` + `civilian.activeCommunityID`
- ✅ `firearm.registeredOwnerID` + `firearm.linkedCivilianID`
- ✅ `warrant.accusedID` + `warrant.status` (just added)

### Check These Are Being Used:
```javascript
// Verify indexes exist
db.vehicles.getIndexes()
db.civilians.getIndexes()
db.firearms.getIndexes()
db.warrants.getIndexes()

// Test query performance
db.vehicles.find({ "vehicle.userID": "some-id" }).explain("executionStats")
// Should show: "stage": "IXSCAN" (not "COLLSCAN")
```

## What to Check in MongoDB Atlas

### 1. Performance Advisor
**Go to:** MongoDB Atlas → Performance Advisor

**Look for:**
- Queries on `vehicles`, `civilians`, `firearms` collections
- Queries showing "COLLSCAN" (collection scan)
- Suggested indexes for these collections

**Action:** Create any suggested indexes immediately

### 2. Real-Time Performance Panel
**Go to:** MongoDB Atlas → Real-Time Performance

**Look for:**
- Long-running queries (> 5s) on large collections
- Queries on `vehicles`, `civilians`, `firearms`
- Operations stuck in "waiting" state

**Action:** Identify slow queries and add indexes

### 3. Query Targeting Alerts
**Go to:** MongoDB Atlas → Alerts

**Look for:**
- "Query Targeting: Scanned Objects / Returned > 1000"
- Which collections are triggering alerts
- Which queries are inefficient

**Action:** Add indexes for those queries

## Connection Pool Math

**With Large Collections:**
- Slow query (5-10s) × 30 concurrent users = 30 connections held
- Your pool: 200 connections max
- **30 slow queries = 15% of pool**
- **If 200 slow queries happen simultaneously = pool exhausted**

**After Fixes:**
- Fast query (< 100ms) × 30 concurrent users = 30 connections held briefly
- Connections release quickly (< 1s)
- Pool can handle many more concurrent requests

## Immediate Actions

### 1. Verify Indexes Are Created
```bash
# Run in MongoDB shell
load("scripts/create_indexes.js")
```

### 2. Check Query Performance
```javascript
// Test queries on large collections
db.vehicles.find({ "vehicle.userID": "test-id" }).explain("executionStats")
db.civilians.find({ "civilian.userID": "test-id" }).explain("executionStats")
db.firearms.find({ "firearm.registeredOwnerID": "test-id" }).explain("executionStats")

// Should show "IXSCAN" not "COLLSCAN"
```

### 3. Monitor MongoDB Atlas
- **Performance Advisor**: Check for slow queries
- **Real-Time Performance**: Watch for long-running operations
- **Metrics → Connections**: Monitor connection count

### 4. Check Your Metrics Dashboard
- Routes querying large collections
- DB query times > 1s
- Routes with high DB error rates

## Why This Causes "Sudden" Failures

### The Pattern:
1. **Normal Traffic**: 50 concurrent users, queries are fast (< 100ms)
2. **Traffic Spike**: 200 concurrent users hit the API
3. **Slow Queries**: Some queries on large collections are slow (5-10s)
4. **Pool Fills**: 200 slow queries = 200 connections used
5. **Everything Fails**: New requests can't get connections
6. **Recovery**: After 7s timeout, connections release, pool recovers

### Why It's "Sudden":
- **Traffic Spike**: More users = more concurrent queries
- **Slow Query**: One slow query holds connection for 10s
- **Cascade**: Many slow queries = pool exhausted quickly
- **Recovery**: Connections release after timeout, cycle repeats

## Long-Term Solutions

### 1. Add Missing Indexes
- Run `scripts/create_indexes.js` to create all indexes
- Monitor Performance Advisor for new suggestions
- Add indexes for any slow queries

### 2. Optimize Queries
- Use aggregation pipelines efficiently
- Add limits to queries (don't fetch all documents)
- Use pagination for large result sets

### 3. Consider Archiving
- **sessions**: 1.1M docs - consider TTL index to auto-delete old sessions
- **tickets**: 418K docs - archive old tickets
- **arrestreports**: 266K docs - archive old reports

### 4. Upgrade MongoDB Tier
- **M10**: 1,500 connections per node
- **M20**: 3,500 connections per node (2.3x more)
- **M30**: 5,000 connections per node (3.3x more)

## TTL Indexes for Auto-Cleanup

Consider adding TTL indexes to auto-delete old data:

```javascript
// Auto-delete sessions older than 30 days
db.sessions.createIndex(
  { "expiresAt": 1 },
  { expireAfterSeconds: 0, name: "sessions_ttl_idx" }
)

// Auto-delete old tickets (if you have createdAt field)
// db.tickets.createIndex(
//   { "createdAt": 1 },
//   { expireAfterSeconds: 2592000, name: "tickets_ttl_idx" } // 30 days
// )
```

## Summary

**Root Cause:** Large collections (2.2M+ docs) without proper indexes = slow queries = connection pool exhaustion

**Fix:**
1. ✅ Verify all indexes are created (`load("scripts/create_indexes.js")`)
2. ✅ Check Performance Advisor for slow queries
3. ✅ Monitor connection count in Atlas UI
4. ✅ Add indexes for any slow queries found
5. ✅ Consider archiving old data (sessions, tickets)

**Expected Result:** Queries should be < 100ms instead of 5-10s, connections release faster, pool exhaustion prevented.

