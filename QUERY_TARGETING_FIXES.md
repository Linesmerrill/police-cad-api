# Query Targeting Fixes - Analysis Results

## Analysis Date
January 3, 2026

## Summary
Found **5 problematic queries** across 4 collections. All critical issues have been addressed with new indexes.

## Critical Issues Found & Fixed

### 1. 🔴 Users Collection - `user.isOnline` Query
**Problem:**
- **Collection**: users (804,288 docs)
- **Query**: `{"user.isOnline": true}`
- **Issue**: COLLSCAN, 804,288 scanned, 12,854 returned
- **Ratio**: 62.57:1 (should be < 1:1)
- **Execution Time**: 33.4 seconds
- **Impact**: CRITICAL - Large collection, high ratio

**Fix:**
```javascript
db.users.createIndex({ "user.isOnline": 1 }, { name: "user_is_online_idx", background: true })
```

**Status**: ✅ Index added to `scripts/create_indexes.js`

---

### 2. 🔴 Users Collection - `user.emailVerificationToken` Query
**Problem:**
- **Collection**: users (804,288 docs)
- **Query**: `{"user.emailVerificationToken": "...", "user.emailVerificationExpires": {"$gt": "..."}}`
- **Issue**: COLLSCAN, 804,288 scanned, 0 returned
- **Ratio**: Infinity:1
- **Execution Time**: 35-37 seconds
- **Impact**: CRITICAL - Large collection, very high ratio, found in profiler

**Fix:**
```javascript
db.users.createIndex(
  { "user.emailVerificationToken": 1, "user.emailVerificationExpires": 1 },
  { name: "user_email_verification_token_idx", background: true }
)
```

**Status**: ✅ Index added to `scripts/create_indexes.js`

---

### 3. 🔴 Calls Collection - `call.status` Query
**Problem:**
- **Collection**: calls (43,927 docs)
- **Query**: `{"call.status": "active"}`
- **Issue**: COLLSCAN, 43,927 scanned, 0 returned
- **Ratio**: Infinity:1
- **Execution Time**: 4.3 seconds
- **Impact**: CRITICAL - Medium collection, very high ratio

**Note**: Compound index `{call.communityID: 1, call.status: 1}` exists, but MongoDB needs single-field index for status-only queries.

**Fix:**
```javascript
db.calls.createIndex({ "call.status": 1 }, { name: "call_status_idx", background: true })
```

**Status**: ✅ Index added to `scripts/create_indexes.js`

---

### 4. 🔴 Announcements Collection - `announcement.community` Query
**Problem:**
- **Collection**: announcements (107 docs)
- **Query**: `{"announcement.community": "test-id"}`
- **Issue**: COLLSCAN, 107 scanned, 0 returned
- **Ratio**: Infinity:1
- **Execution Time**: 73ms
- **Impact**: CRITICAL - Small collection but COLLSCAN indicates missing index

**Note**: Compound index `{community: 1, isActive: 1, createdAt: -1}` exists, but field name might be different (`community` vs `announcement.community`).

**Fix:**
```javascript
db.announcements.createIndex(
  { "announcement.community": 1, "announcement.isActive": 1, "announcement.createdAt": -1 },
  { name: "announcement_community_active_created_idx_v2", background: true }
)
```

**Status**: ✅ Index added to `scripts/create_indexes.js`

---

## Medium Priority Issues (Not Causing Alerts)

### 5. 🟡 Communities Collection - Visibility & Tags Queries
**Problem:**
- **Collection**: communities (155,723 docs)
- **Queries**: 
  - `{"community.visibility": "public"}` - 3,826 scanned, 3,826 returned, 22.6s
  - `{"community.tags": "Xbox"}` - 2,274 scanned, 2,274 returned, 14.9s
- **Issue**: Using FETCH stage (index exists), but slow execution time
- **Ratio**: 1.00:1 (good ratio, not causing alert)
- **Impact**: MEDIUM - Indexes exist but queries are slow (possibly due to sorting or other operations)

**Status**: ⚠️ Indexes exist, but queries are slow. May need optimization of query patterns or compound indexes.

**Existing Indexes:**
- `community.visibility_1` exists
- `community_tags_visibility_name_idx` exists

**Recommendation**: 
- Check if queries include sorting that doesn't match index order
- Consider adding compound indexes that match exact query patterns
- Monitor Performance Advisor for suggestions

---

## Indexes Already Created (From Previous Fixes)

✅ **vehicles**: `vehicle.userID`, `vehicle.registeredOwnerID`, `vehicle.linkedCivilianID`
✅ **civilians**: `civilian.userID`, `civilian.activeCommunityID`
✅ **firearms**: `firearm.registeredOwnerID`, `firearm.linkedCivilianID`
✅ **users**: `user.email`, `user.communities.communityId`, `user.communities.status`
✅ **communities**: `community.visibility`, `community.tags`, `community.subscriptionCreatedBy`
✅ **calls**: `call.communityID`, `call.status` (compound)
✅ **licenses**: `license.civilianID`
✅ **arrestreports**: `arrestReport.arrestee.id`
✅ **warrants**: `warrant.accusedID`, `warrant.status`
✅ **announcements**: `community`, `isActive`, `createdAt` (compound)

---

## Next Steps

### 1. Create the New Indexes
```bash
# Run in MongoDB shell
load("scripts/create_indexes.js")
```

### 2. Verify Indexes Were Created
```javascript
// Check each collection
db.users.getIndexes()
db.calls.getIndexes()
db.announcements.getIndexes()
```

### 3. Test Queries Again
```javascript
// Test user.isOnline query
db.users.find({ "user.isOnline": true }).explain("executionStats")
// Should show: "stage": "IXSCAN" (not "COLLSCAN")

// Test call.status query
db.calls.find({ "call.status": "active" }).explain("executionStats")
// Should show: "stage": "IXSCAN" (not "COLLSCAN")

// Test emailVerificationToken query
db.users.find({ 
  "user.emailVerificationToken": "test", 
  "user.emailVerificationExpires": { "$gt": new Date() } 
}).explain("executionStats")
// Should show: "stage": "IXSCAN" (not "COLLSCAN")
```

### 4. Clear Plan Cache (if needed)
```javascript
// After creating indexes, clear plan cache
db.users.getPlanCache().clear()
db.calls.getPlanCache().clear()
db.announcements.getPlanCache().clear()
```

### 5. Monitor Results
- Check MongoDB Atlas → Performance Advisor (wait 10-15 minutes)
- Verify "Query Targeting" alerts are gone
- Check metrics dashboard for improved query times

---

## Expected Results

After creating indexes:
- **user.isOnline queries**: Should be < 100ms (was 33.4s)
- **emailVerificationToken queries**: Should be < 100ms (was 35-37s)
- **call.status queries**: Should be < 100ms (was 4.3s)
- **announcement.community queries**: Should be < 10ms (was 73ms)
- **Query Targeting alerts**: Should disappear within 10-15 minutes

---

## Collections Status Summary

| Collection | Docs | Status | Issues Found | Fixes Applied |
|------------|------|--------|--------------|---------------|
| **users** | 804K | 🔴 Critical | 2 (isOnline, emailVerificationToken) | ✅ 2 indexes added |
| **calls** | 44K | 🔴 Critical | 1 (status) | ✅ 1 index added |
| **announcements** | 107 | 🔴 Critical | 1 (community) | ✅ 1 index added |
| **communities** | 156K | 🟡 Medium | 2 (slow but good ratio) | ⚠️ Indexes exist, may need optimization |
| **vehicles** | 2.2M | ✅ OK | 0 | ✅ Indexes already exist |
| **civilians** | 1.7M | ✅ OK | 0 | ✅ Indexes already exist |
| **firearms** | 1.3M | ✅ OK | 0 | ✅ Indexes already exist |

---

## Notes

1. **Field Name Mismatch**: The announcements collection might use `community` instead of `announcement.community`. The script added both patterns to be safe.

2. **Compound vs Single-Field**: Some queries need single-field indexes even when compound indexes exist (e.g., `call.status`).

3. **Slow but Good Ratio**: Communities queries have good ratios (1:1) but are slow (14-22s). This suggests indexes exist but queries may need optimization (sorting, aggregation, etc.).

4. **Profiler Queries**: The `emailVerificationToken` query was found in the profiler, indicating it's being executed frequently. This index is critical.

---

## Verification Commands

```bash
# Run analysis again after creating indexes
load("scripts/analyze_query_targeting.js")

# Check profiler for slow queries
load("scripts/check_profiler_queries.js")

# Verify indexes exist
db.users.getIndexes() | grep -E "is_online|email_verification"
db.calls.getIndexes() | grep "status"
db.announcements.getIndexes() | grep "community"
```

