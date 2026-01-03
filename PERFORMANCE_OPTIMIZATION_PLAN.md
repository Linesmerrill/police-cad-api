# Performance Optimization Plan

## Collections to Optimize

### 1. ✅ users (FIXED)
- **Issue**: Regex queries scanning 804k documents (607s)
- **Fix**: Use `$text` search with `user_search_text_idx`
- **Status**: ✅ COMPLETE

### 2. communities
- **Issues Found**:
  - Regex on `community.name` in search/admin handlers
  - Text index exists but not always used
- **Fix**: Use `$text` search for queries >=3 chars

### 3. calls
- **Issues Found**:
  - Empty filter `bson.D{}` scanning entire collection
  - Uses `context.TODO()` instead of timeout
- **Fix**: Add proper filter or limit, use `api.WithQueryTimeout()`

### 4. firearms
- **Issues Found**:
  - Empty filter `bson.D{}` scanning entire collection
  - Regex on `firearm.name` and `firearm.serialNumber`
  - Uses `context.TODO()` instead of timeout
- **Fix**: Add text index or optimize regex, use `api.WithQueryTimeout()`

### 5. civilians
- **Issues Found**:
  - Empty filter `bson.D{}` scanning entire collection
  - Regex on `civilian.firstName`, `civilian.lastName`, `civilian.name`
  - Uses `context.TODO()` instead of timeout
- **Fix**: Add text index or optimize regex, use `api.WithQueryTimeout()`

### 6. vehicles
- **Issues Found**:
  - Empty filter `bson.D{}` scanning entire collection
  - Uses `context.TODO()` instead of timeout
- **Fix**: Add proper filter or limit, use `api.WithQueryTimeout()`

### 7. warrants
- **Issues Found**:
  - Empty filter `bson.D{}` scanning entire collection
- **Fix**: Add proper filter or limit, use `api.WithQueryTimeout()`

### 8. spotlight
- **Issues Found**:
  - Empty filter `bson.D{}` scanning entire collection
- **Fix**: Add proper filter or limit, use `api.WithQueryTimeout()`

### 9. ems
- **Issues Found**:
  - Empty filter `bson.D{}` scanning entire collection
  - Uses `context.TODO()` instead of timeout
- **Fix**: Add proper filter or limit, use `api.WithQueryTimeout()`

### 10. announcements
- **Check**: Verify no regex queries or empty filters

### 11. bolos
- **Check**: Verify no regex queries or empty filters

### 12. medicalreports
- **Check**: Verify no regex queries or empty filters

### 13. licenses
- **Check**: Verify no regex queries or empty filters

### 14. arrestreports
- **Check**: Verify no regex queries or empty filters

## Priority Order

1. **CRITICAL**: Empty filter queries (scanning entire collections)
2. **HIGH**: Regex queries without text indexes
3. **MEDIUM**: Missing `api.WithQueryTimeout()` context
4. **LOW**: Other optimizations

## Next Steps

1. Run `load("scripts/analyze_all_collections_performance.js")` in MongoDB
2. Fix empty filter queries first (CRITICAL)
3. Fix regex queries (HIGH)
4. Fix context timeouts (MEDIUM)
5. Verify improvements in metrics dashboard

