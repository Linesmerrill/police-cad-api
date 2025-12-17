# Performance Optimization Guide

## Critical Timeout Fixes Applied

### 1. Database Connection Pooling ✅
- **Max Pool Size**: 100 connections
- **Min Pool Size**: 10 connections  
- **Connection Timeout**: 10 seconds
- **Socket Timeout**: 30 seconds
- **Server Selection Timeout**: 5 seconds
- **Idle Connection Timeout**: 30 seconds

### 2. HTTP Server Timeouts ✅
- **Read Timeout**: 30 seconds (max time to read request)
- **Write Timeout**: 30 seconds (max time to write response)
- **Idle Timeout**: 120 seconds (max time between requests)

### 3. Query Timeout Helper ✅
- Created `api/context_helper.go` with `WithQueryTimeout()` function
- Default query timeout: 10 seconds
- Use this instead of `context.Background()` for all database queries

## Remaining Performance Issues

### ⚠️ Critical: Replace `context.Background()` with Timeouts

Many handlers still use `context.Background()` which has no timeout. This causes requests to hang indefinitely.

**Find and replace:**
```go
// BAD - No timeout
cursor, err := c.UDB.Find(context.Background(), filter, options)

// GOOD - With timeout
ctx, cancel := api.WithQueryTimeout(r.Context())
defer cancel()
cursor, err := c.UDB.Find(ctx, filter, options)
```

### High Priority Endpoints to Fix:

1. **`/api/v1/community/{id}/members`** - `FetchCommunityMembersHandlerV2`
   - Uses `context.Background()` 
   - Complex `$elemMatch` query on arrays
   - Needs database index on `user.communities.communityId`

2. **`/api/v1/community/{id}/members/search`** - `SearchCommunityMembersHandler`
   - Uses regex queries without indexes
   - Needs text index on `user.callSign` and `user.username`

3. **Search endpoints** - Multiple handlers
   - Regex queries are slow without indexes
   - Consider MongoDB text search or Elasticsearch

## Database Indexes Needed

Run these MongoDB commands to create indexes:

```javascript
// Index for community members queries
db.users.createIndex({ "user.communities.communityId": 1, "user.communities.status": 1 })

// Text index for user search
db.users.createIndex({ 
  "user.username": "text", 
  "user.callSign": "text",
  "user.name": "text"
})

// Index for community visibility queries
db.communities.createIndex({ "community.visibility": 1 })

// Index for subscription queries
db.users.createIndex({ "user.subscription.active": 1, "user.subscription.plan": 1 })
```

## Monitoring

### Check Slow Queries:
```bash
# In MongoDB shell
db.setProfilingLevel(1, { slowms: 1000 })
db.system.profile.find().sort({ ts: -1 }).limit(10)
```

### Heroku Metrics to Watch:
- Request timeouts (should be < 1%)
- Response time (p50, p95, p99)
- Database connection pool usage
- Memory usage

## Quick Wins

1. ✅ **Connection pooling** - Already fixed
2. ✅ **HTTP timeouts** - Already fixed  
3. ⚠️ **Query timeouts** - Need to replace `context.Background()` in handlers
4. ⚠️ **Database indexes** - Need to create indexes for common queries
5. ⚠️ **Limit query results** - Some endpoints don't cap results

## Emergency Timeout Fix

If timeouts continue, temporarily reduce timeouts:

```go
// In main.go - reduce to 15 seconds
ReadTimeout:  15 * time.Second
WriteTimeout: 15 * time.Second

// In api/context_helper.go - reduce to 5 seconds
const QueryTimeout = 5 * time.Second
```

## Next Steps

1. Replace all `context.Background()` with `api.WithQueryTimeout(r.Context())`
2. Create database indexes (see above)
3. Add query result limits where missing
4. Monitor slow queries and optimize
5. Consider caching for frequently accessed data

