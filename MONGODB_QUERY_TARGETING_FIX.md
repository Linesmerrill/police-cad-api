# Fix "Query Targeting: Scanned Objects / Returned > 1000" Alert

## What This Alert Means

**"Query Targeting: Scanned Objects / Returned has gone above 1000"** means:
- MongoDB is scanning **more than 1000 documents** for every document returned
- This indicates **inefficient queries** that aren't using indexes properly
- Example: Scanning 10,000 documents to return 5 results = 2000:1 ratio (BAD!)

**Why It Happens:**
- Missing indexes on queried fields
- Queries using operators that can't use indexes (`$regex` without prefix, `$text` without text index)
- Compound queries where index order doesn't match query order
- Queries filtering on non-indexed fields

## Step 1: Identify the Problematic Queries

### Option A: MongoDB Atlas Performance Advisor (Easiest)

1. Go to **MongoDB Atlas** → Your Cluster → **Performance Advisor** tab
2. Look for:
   - **"Query Targeting"** section
   - Queries with high "Scanned / Returned" ratio
   - Click on each query to see details

### Option B: Check Profiler (More Detailed)

1. Go to **MongoDB Atlas** → Your Cluster → **Profiler** tab
2. Filter by:
   - **Duration**: > 100ms
   - **Scanned Documents**: > 1000
3. Review the query patterns

### Option C: Run Explain on Common Queries

Run these in MongoDB Atlas → Data Explorer:

```javascript
// Check community queries
db.communities.find({ "community.visibility": "public" }).explain("executionStats")
db.communities.find({ "community.tags": "Xbox" }).explain("executionStats")
db.communities.find({ "community.subscriptionCreatedBy": "some-id" }).explain("executionStats")

// Check user queries
db.users.find({ "user.communities.communityId": "some-id" }).explain("executionStats")
db.users.find({ "user.email": "test@example.com" }).explain("executionStats")

// Check vehicle queries
db.vehicles.find({ "vehicle.userID": "some-id" }).explain("executionStats")
db.vehicles.find({ "vehicle.registeredOwnerID": "some-id" }).explain("executionStats")

// Check call queries
db.calls.find({ "call.communityID": "some-id" }).explain("executionStats")

// Check license queries
db.licenses.find({ "license.civilianID": "some-id" }).explain("executionStats")
```

**Look for:**
- `"stage": "COLLSCAN"` = ❌ **BAD** (full collection scan)
- `"stage": "IXSCAN"` = ✅ **GOOD** (using index)
- `"executionTimeMillis"` > 100ms = Slow query
- `"totalDocsExamined"` / `"nReturned"` > 1000 = Query targeting issue

## Step 2: Verify Existing Indexes

Check which indexes exist:

```javascript
// Check all collections
db.users.getIndexes()
db.communities.getIndexes()
db.vehicles.getIndexes()
db.calls.getIndexes()
db.licenses.getIndexes()
db.firearms.getIndexes()
db.civilians.getIndexes()
```

## Step 3: Create Missing Indexes

Run the index creation script:

```bash
# Connect to MongoDB Atlas shell
mongosh "YOUR_CONNECTION_STRING"

# Run the index script
load("scripts/create_indexes.js")
```

Or manually create indexes from `scripts/create_indexes.js`:

```javascript
// Critical indexes (if missing):
db.users.createIndex({ "user.email": 1 }, { name: "user_email_idx", collation: { locale: "en", strength: 2 }, background: true })
db.users.createIndex({ "user.communities.communityId": 1, "user.communities.status": 1 }, { name: "user_communities_idx", background: true })
db.communities.createIndex({ "community.visibility": 1 }, { name: "community_visibility_idx", background: true })
db.communities.createIndex({ "community.tags": 1, "community.visibility": 1, "community.name": 1 }, { name: "community_tags_visibility_name_idx", background: true })
db.communities.createIndex({ "community.subscriptionCreatedBy": 1 }, { name: "community_subscription_created_by_idx", background: true })
db.vehicles.createIndex({ "vehicle.userID": 1, "vehicle.activeCommunityID": 1 }, { name: "vehicle_user_community_idx", background: true })
db.vehicles.createIndex({ "vehicle.linkedCivilianID": 1, "vehicle.registeredOwnerID": 1 }, { name: "vehicle_registered_owner_idx", background: true })
db.calls.createIndex({ "call.communityID": 1, "call.status": 1 }, { name: "call_community_status_idx", background: true })
db.licenses.createIndex({ "license.civilianID": 1 }, { name: "license_civilian_id_idx", background: true })
db.firearms.createIndex({ "firearm.linkedCivilianID": 1, "firearm.registeredOwnerID": 1 }, { name: "firearm_registered_owner_idx", background: true })
db.civilians.createIndex({ "civilian.userID": 1, "civilian.activeCommunityID": 1 }, { name: "civilian_user_community_idx", background: true })
```

## Step 4: Verify Indexes Are Being Used

After creating indexes, verify they're being used:

```javascript
// Re-run explain() on the same queries
db.communities.find({ "community.visibility": "public" }).explain("executionStats")

// Should now show:
// - "stage": "IXSCAN" ✅
// - "indexName": "community_visibility_idx" ✅
// - "executionTimeMillis": < 100ms ✅
// - "totalDocsExamined" / "nReturned" < 10 ✅
```

## Step 5: Clear Plan Cache (If Indexes Still Not Used)

Sometimes MongoDB caches old query plans. Clear the cache:

```javascript
// Clear plan cache for each collection
db.communities.getPlanCache().clear()
db.users.getPlanCache().clear()
db.vehicles.getPlanCache().clear()
db.calls.getPlanCache().clear()
db.licenses.getPlanCache().clear()
db.firearms.getPlanCache().clear()
db.civilians.getPlanCache().clear()

// Or clear all at once
db.runCommand({ planCacheClear: "communities" })
db.runCommand({ planCacheClear: "users" })
db.runCommand({ planCacheClear: "vehicles" })
db.runCommand({ planCacheClear: "calls" })
db.runCommand({ planCacheClear: "licenses" })
db.runCommand({ planCacheClear: "firearms" })
db.runCommand({ planCacheClear: "civilians" })
```

## Step 6: Monitor After Fixes

1. Wait 5-10 minutes after creating indexes
2. Check **Performance Advisor** again
3. Verify alerts are gone or reduced
4. Check **Metrics** → **Query Targeting** chart should show < 1000 ratio

## Common Query Patterns That Cause This

### ❌ BAD: Regex without prefix
```javascript
db.communities.find({ "community.name": { $regex: "test", $options: "i" } })
// Can't use index - scans entire collection
```

### ✅ GOOD: Prefix regex (can use index)
```javascript
db.communities.find({ "community.name": { $regex: "^test", $options: "i" } })
// Can use index if name field is indexed
```

### ✅ BETTER: Text search with text index
```javascript
db.communities.find({ $text: { $search: "test" } })
// Uses text index - very fast
```

### ❌ BAD: Query on non-indexed field
```javascript
db.users.find({ "user.customField": "value" })
// Scans entire collection if customField not indexed
```

### ✅ GOOD: Query on indexed field
```javascript
db.users.find({ "user.email": "test@example.com" })
// Uses user_email_idx - fast!
```

## Quick Checklist

- [ ] Check Performance Advisor for slow queries
- [ ] Run `explain("executionStats")` on common queries
- [ ] Verify indexes exist: `db.collection.getIndexes()`
- [ ] Create missing indexes from `scripts/create_indexes.js`
- [ ] Clear plan cache: `db.collection.getPlanCache().clear()`
- [ ] Re-run `explain()` to verify indexes are used
- [ ] Monitor Performance Advisor for 10 minutes
- [ ] Check Query Targeting metrics

## Expected Results After Fix

- **Query Targeting ratio**: < 1000 (ideally < 10)
- **Query execution time**: < 100ms (most queries)
- **Index usage**: "IXSCAN" instead of "COLLSCAN"
- **Alerts**: Should disappear within 10-15 minutes

