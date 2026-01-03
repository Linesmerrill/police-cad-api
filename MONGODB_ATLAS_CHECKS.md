# MongoDB Atlas Connection Health Checks

## What You Can Check Without Admin Privileges

Since `db.currentOp()` requires admin privileges, here's what you CAN check:

### 1. MongoDB Atlas UI (No Admin Needed!)

**Go to MongoDB Atlas → Your Cluster → Metrics Tab**

#### Connections Tab
- **Current Connections**: Should be < 200 (with new MaxPoolSize of 100)
- **Peak Connections**: Should not hit 200
- **Connection Wait Time**: Should be < 100ms
- **Connection Errors**: Should be zero

**What to Look For:**
- If current connections > 180: Pool is getting exhausted
- If wait time > 100ms: Connections are queuing
- If errors > 0: Connection pool is exhausted

#### Performance Advisor Tab
- **Slow Queries**: Shows queries taking > 100ms
- **Index Suggestions**: Shows missing indexes
- **Query Patterns**: Shows which queries are slow

**What to Look For:**
- Queries without indexes (COLLSCAN)
- Queries taking > 5 seconds
- Queries with high "Scanned Objects / Returned" ratio

#### Real-Time Performance Panel
- **Active Operations**: Shows currently running queries
- **Query Duration**: Shows how long queries are taking
- **Operation Types**: Shows read vs write operations

**What to Look For:**
- Long-running operations (> 5s)
- Many concurrent operations
- Operations stuck in "waiting" state

### 2. MongoDB Shell (Limited Access)

**Run this script:**
```bash
load("scripts/check_connection_health.js")
```

**What it checks:**
- Current connection count (if accessible)
- Database stats
- Collection sizes
- Index usage (via explain)

### 3. Check Your Application Metrics

**In your metrics dashboard (`/metrics-dashboard`):**
- **DB Query Times**: Should be < 1s for most queries
- **DB Error Rate**: Should be < 1%
- **Slow Routes**: Routes taking > 5s

**What to Look For:**
- Routes consistently timing out at 7s (query timeout)
- High DB error rate (> 10%)
- Many routes showing 100% DB errors

## What's Likely Happening

Based on your symptoms ("everything works then suddenly fails"):

### Scenario 1: Connection Pool Exhaustion
**Symptoms:**
- Current connections hitting 200 (or 300 with old settings)
- Connection wait time > 100ms
- All requests failing simultaneously

**Fix:**
- Already applied: Reduced MaxPoolSize to 100
- Monitor connections in Atlas UI
- If still hitting limit, reduce to 75 per dyno

### Scenario 2: Slow Query Cascade
**Symptoms:**
- One slow query (> 5s) holds connection
- Other queries queue behind it
- Pool fills up quickly

**Fix:**
- Check Performance Advisor for slow queries
- Add missing indexes (see `scripts/create_indexes.js`)
- Optimize slow routes (check metrics dashboard)

### Scenario 3: MongoDB Primary Node Overload
**Symptoms:**
- High CPU usage on PRIMARY node
- High memory usage
- Replication lag increasing

**Fix:**
- Check Atlas UI → Metrics → CPU/Memory
- Consider upgrading to M20+ if consistently high
- Optimize queries to reduce load

## Immediate Actions

### 1. Check MongoDB Atlas Right Now
1. Go to **Metrics → Connections**
2. Check **Current** and **Peak** connections
3. If > 200, that's your problem!

### 2. Check Performance Advisor
1. Go to **Performance Advisor**
2. Look for **slow queries** (> 100ms)
3. Check **index suggestions**
4. Create missing indexes

### 3. Check Real-Time Performance
1. Go to **Real-Time Performance Panel**
2. Look for **long-running operations** (> 5s)
3. Check **operation types** (reads vs writes)

### 4. Monitor Your Metrics Dashboard
1. Go to `/metrics-dashboard`
2. Check **DB Query Times**
3. Check **DB Error Rate**
4. Identify **slow routes** (> 5s)

## Connection Pool Math

**Current Settings (After Fix):**
- MaxPoolSize: 100 per dyno
- If 2 dynos: 100 × 2 = **200 total connections**
- M10 Limit: 1,500 per node (but you're using PRIMARY only)

**Safe Thresholds:**
- **< 150 connections**: ✅ Safe
- **150-180 connections**: ⚠️  Warning (75-90% of pool)
- **180-200 connections**: 🔴 Critical (90-100% of pool)
- **> 200 connections**: ❌ Exhausted (will fail)

**If You Have 3+ Dynos:**
- Reduce MaxPoolSize to 75 per dyno
- 3 × 75 = 225 (still safe, but closer to limit)

## What to Do If Connections Are High

### If Current Connections > 180:
1. **Reduce MaxPoolSize further** (to 75 per dyno)
2. **Check for slow queries** (Performance Advisor)
3. **Add missing indexes** (scripts/create_indexes.js)
4. **Scale down dynos** (if traffic allows)

### If Connection Wait Time > 100ms:
1. **Queries are queuing** - pool is exhausted
2. **Reduce MaxPoolSize** immediately
3. **Check for connection leaks** (cursors not closed)
4. **Optimize slow queries**

### If Everything Fails Simultaneously:
1. **Connection pool exhausted** - all 200 connections used
2. **Check MongoDB Atlas → Metrics → Connections**
3. **Reduce MaxPoolSize** to 75 per dyno
4. **Check for slow queries** holding connections

## Long-Term Solutions

1. **Upgrade to M20+**: Higher connection limits (3,500 per node)
2. **Add Redis Cache**: Reduce database load
3. **Optimize Queries**: Add indexes, optimize patterns
4. **Implement Circuit Breaker**: Fail fast when pool is exhausted
5. **Connection Pooling**: Shared pool across services (advanced)

