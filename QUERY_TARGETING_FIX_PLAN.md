# Query Targeting Fix Plan

## Overview
This document tracks the systematic analysis and fixes for "Query Targeting: Scanned Objects / Returned > 1000" alerts.

## Analysis Scripts

### 1. Analyze All Collections
```bash
# Run in MongoDB shell
load("scripts/analyze_query_targeting.js")
```
**What it does:**
- Tests common query patterns for each collection
- Identifies queries with scanned/returned > 1000
- Suggests indexes for COLLSCAN queries
- Groups results by collection

### 2. Check Profiler Queries
```bash
# Run in MongoDB shell
load("scripts/check_profiler_queries.js")
```
**What it does:**
- Analyzes slow queries from MongoDB profiler
- Identifies frequently executed slow queries
- Shows query patterns with high scanned/returned ratios
- Groups by collection and query pattern

### 3. MongoDB Atlas Performance Advisor
**Go to:** MongoDB Atlas → Performance Advisor

**What to check:**
- Slow queries (> 100ms)
- Index suggestions
- Query targeting alerts
- Click "Create Index" on suggested indexes

## Step-by-Step Fix Process

### Step 1: Run Analysis Scripts
```bash
# Connect to MongoDB
mongosh "YOUR_CONNECTION_STRING"

# Run analysis
load("scripts/analyze_query_targeting.js")
load("scripts/check_profiler_queries.js")
```

### Step 2: Review Results
- Identify collections with problematic queries
- Note which fields are being queried without indexes
- Check suggested indexes

### Step 3: Check Existing Indexes
```javascript
// For each problematic collection
db.collectionName.getIndexes()

// Check if suggested indexes already exist
// Look for indexes matching the suggested fields
```

### Step 4: Create Missing Indexes
```bash
# Run the index creation script
load("scripts/create_indexes.js")

# Or create indexes manually
db.collectionName.createIndex({ "field": 1 }, { name: "index_name", background: true })
```

### Step 5: Verify Index Usage
```javascript
// Test query with explain
db.collectionName.find({ "field": "value" }).explain("executionStats")

// Look for:
// - "stage": "IXSCAN" (good - using index)
// - "stage": "COLLSCAN" (bad - full collection scan)
// - "totalDocsExamined" / "nReturned" < 1000 (good ratio)
```

### Step 6: Clear Plan Cache (if needed)
```javascript
// After creating new indexes, clear plan cache
db.collectionName.getPlanCache().clear()
```

### Step 7: Monitor Results
- Check MongoDB Atlas → Performance Advisor (wait 10-15 minutes)
- Verify alerts are gone or reduced
- Check metrics dashboard for improved query times

## Collections to Check (Based on Size)

### Large Collections (High Priority)
1. **vehicles** (2.2M docs, 787 MB)
   - Check: `vehicle.userID`, `vehicle.registeredOwnerID`, `vehicle.linkedCivilianID`
   - Indexes: Already created in `scripts/create_indexes.js`

2. **civilians** (1.7M docs, 691 MB)
   - Check: `civilian.userID`, `civilian.activeCommunityID`
   - Indexes: Already created in `scripts/create_indexes.js`

3. **firearms** (1.3M docs, 385 MB)
   - Check: `firearm.registeredOwnerID`, `firearm.linkedCivilianID`
   - Indexes: Already created in `scripts/create_indexes.js`

4. **sessions** (1.1M docs, 202 MB)
   - Check: Queries by user, expiration
   - Consider: TTL index for auto-cleanup

5. **users** (804K docs, 307 MB)
   - Check: `user.email`, `user.communities.communityId`, `user.isOnline`
   - Indexes: Already created in `scripts/create_indexes.js`

### Medium Collections
6. **communities** (156K docs, 342 MB)
   - Check: `community.visibility`, `community.tags`, `community.subscriptionCreatedBy`
   - Indexes: Already created in `scripts/create_indexes.js`

7. **tickets** (418K docs, 148 MB)
   - Check: Queries by user, community, status
   - May need indexes if frequently queried

8. **arrestreports** (266K docs, 140 MB)
   - Check: `arrestReport.arrestee.id`
   - Indexes: Already created in `scripts/create_indexes.js`

9. **licenses** (360K docs, 107 MB)
   - Check: `license.civilianID`
   - Indexes: Already created in `scripts/create_indexes.js`

10. **warrants** (203K docs, 60 MB)
    - Check: `warrant.accusedID`, `warrant.status`
    - Indexes: Already created in `scripts/create_indexes.js`

## Common Query Patterns to Check

### 1. User Queries
```javascript
// Email lookup
db.users.find({ "user.email": "test@example.com" })
// Index: { "user.email": 1 }

// Community membership
db.users.find({ "user.communities.communityId": "id" })
// Index: { "user.communities.communityId": 1, "user.communities.status": 1 }

// Online status
db.users.find({ "user.isOnline": true })
// Index: { "user.isOnline": 1 }
```

### 2. Community Queries
```javascript
// Visibility filter
db.communities.find({ "community.visibility": "public" })
// Index: { "community.visibility": 1 }

// Tag filter
db.communities.find({ "community.tags": "Xbox" })
// Index: { "community.tags": 1, "community.visibility": 1 }
```

### 3. Vehicle/Firearm/Civilian Queries
```javascript
// User ID lookup
db.vehicles.find({ "vehicle.userID": "id" })
// Index: { "vehicle.userID": 1, "vehicle.activeCommunityID": 1 }

// Registered owner lookup
db.vehicles.find({ "vehicle.registeredOwnerID": "id" })
// Index: { "vehicle.registeredOwnerID": 1 }
```

## Index Creation Checklist

- [ ] Run `load("scripts/analyze_query_targeting.js")`
- [ ] Run `load("scripts/check_profiler_queries.js")`
- [ ] Check MongoDB Atlas Performance Advisor
- [ ] Review problematic queries by collection
- [ ] Verify existing indexes: `db.collection.getIndexes()`
- [ ] Create missing indexes: `load("scripts/create_indexes.js")`
- [ ] Verify index usage: `db.collection.find({...}).explain("executionStats")`
- [ ] Clear plan cache if needed: `db.collection.getPlanCache().clear()`
- [ ] Monitor Performance Advisor (wait 10-15 minutes)
- [ ] Verify alerts are gone or reduced

## Expected Results

After fixes:
- **Query Targeting ratio**: < 1000 (ideally < 10)
- **Query execution time**: < 100ms (most queries)
- **Index usage**: "IXSCAN" instead of "COLLSCAN"
- **Alerts**: Should disappear within 10-15 minutes

## Troubleshooting

### If Indexes Don't Help
1. Check query pattern - may need compound index
2. Verify index order matches query order
3. Check for regex queries without prefix (can't use index)
4. Consider text indexes for search queries

### If Queries Still Slow
1. Check MongoDB Atlas → Metrics → CPU/Memory
2. Verify replication lag is low (< 1s)
3. Consider upgrading MongoDB tier (M10 → M20+)
4. Optimize query patterns (avoid $or, $regex without prefix)

## Next Steps

1. Run analysis scripts to identify all problematic queries
2. Create comprehensive list of missing indexes
3. Create indexes using `scripts/create_indexes.js`
4. Monitor and verify improvements
5. Document any additional indexes needed

