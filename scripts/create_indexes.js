// MongoDB Index Creation Script
// Run this in MongoDB Atlas → Your Cluster → "Browse Collections" → "Shell" tab
// OR connect via mongosh and run: mongosh "YOUR_CONNECTION_STRING" < create_indexes.js

// CRITICAL: User Email Index (Case-Insensitive)
// This is used in authentication on EVERY request - most critical index
print("Creating user email index...");
db.users.createIndex(
  { "user.email": 1 }, 
  { 
    name: "user_email_idx",
    collation: { locale: "en", strength: 2 },  // Case-insensitive
    background: true  // Don't block operations while building
  }
);
print("✓ User email index created");

// HIGH PRIORITY: User Communities Index
print("Creating user communities index...");
db.users.createIndex(
  { "user.communities.communityId": 1, "user.communities.status": 1 }, 
  {
    name: "user_communities_idx",
    background: true
  }
);
print("✓ User communities index created");

// HIGH PRIORITY: User Search Text Index
print("Creating user search text index...");
db.users.createIndex(
  { 
    "user.username": "text", 
    "user.callSign": "text",
    "user.name": "text"
  },
  {
    name: "user_search_text_idx",
    background: true
  }
);
print("✓ User search text index created");

// MEDIUM PRIORITY: Community Name Text Index
print("Creating community name text index...");
db.communities.createIndex(
  { "community.name": "text" }, 
  {
    name: "community_name_text_idx",
    background: true
  }
);
print("✓ Community name text index created");

// MEDIUM PRIORITY: Community Visibility Index
print("Creating community visibility index...");
db.communities.createIndex(
  { "community.visibility": 1 }, 
  {
    name: "community_visibility_idx",
    background: true
  }
);
print("✓ Community visibility index created");

print("\n✅ All indexes created! Check index status with:");
print("db.users.getIndexes()");
print("db.communities.getIndexes()");

