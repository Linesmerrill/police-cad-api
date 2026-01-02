# MongoDB Indexes Required

## Critical Indexes to Create

These indexes are needed to fix the "Query Targeting: Scanned Objects / Returned has gone above 1000" alert.

### 1. User Email Index (Case-Insensitive)
**Collection:** `users`  
**Index:**
```javascript
db.users.createIndex({ "user.email": 1 }, { 
  name: "user_email_idx",
  collation: { locale: "en", strength: 2 }  // Case-insensitive
})
```

**Why:** The `$expr` with `$toLower` queries scan the entire collection. This index allows efficient email lookups.

**Note:** After creating this index, you can optimize the query to use direct field lookup instead of `$expr`.

### 2. User Communities Index
**Collection:** `users`  
**Index:**
```javascript
db.users.createIndex({ "user.communities.communityId": 1, "user.communities.status": 1 }, {
  name: "user_communities_idx"
})
```

**Why:** Many queries filter by `user.communities.communityId` without an index, causing full collection scans.

### 3. User Search Text Index
**Collection:** `users`  
**Index:**
```javascript
db.users.createIndex({ 
  "user.username": "text", 
  "user.callSign": "text",
  "user.name": "text"
}, {
  name: "user_search_text_idx"
})
```

**Why:** Regex queries on `user.name`, `user.username` scan the entire collection. Text index enables efficient search.

### 4. Community Name Search Index
**Collection:** `communities`  
**Index:**
```javascript
db.communities.createIndex({ "community.name": "text" }, {
  name: "community_name_text_idx"
})
```

**Why:** Regex queries on `community.name` scan the entire collection.

### 5. Community Visibility Index
**Collection:** `communities`  
**Index:**
```javascript
db.communities.createIndex({ "community.visibility": 1 }, {
  name: "community_visibility_idx"
})
```

**Why:** Many queries filter by visibility without an index.

### 6. Vehicle Search Indexes (Already Created)
✅ `vehicle.registeredOwnerID_1`  
✅ `vehicle.linkedCivilianID_1`

## How to Create These Indexes

1. Go to MongoDB Atlas → Your Cluster → "Browse Collections"
2. Select the collection (e.g., `users`)
3. Click "Indexes" tab
4. Click "Create Index"
5. Use the JSON format above, or use the UI to add fields

## Expected Impact

After creating these indexes:
- Query performance should improve significantly
- "Scanned Objects / Returned" ratio should drop below 1000
- Database load should decrease
- Response times should improve

## Priority Order

1. **User Email Index** - Most critical (used in authentication)
2. **User Communities Index** - High priority (used in many queries)
3. **User Search Text Index** - High priority (used in search)
4. **Community Name Text Index** - Medium priority
5. **Community Visibility Index** - Medium priority

