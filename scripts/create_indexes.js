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

// CRITICAL: Vehicle Registered Owner Index (for /vehicles/registered-owner/{id})
print("Creating vehicle registered owner index...");
db.vehicles.createIndex(
  { "vehicle.linkedCivilianID": 1, "vehicle.registeredOwnerID": 1 },
  {
    name: "vehicle_registered_owner_idx",
    background: true
  }
);
print("✓ Vehicle registered owner index created");

// CRITICAL: Vehicle User ID Index (for /vehicles/user/{id})
print("Creating vehicle user ID index...");
db.vehicles.createIndex(
  { "vehicle.userID": 1, "vehicle.activeCommunityID": 1 },
  {
    name: "vehicle_user_community_idx",
    background: true
  }
);
print("✓ Vehicle user ID index created");

// CRITICAL: Civilian User ID Index (for /civilians/user/{id})
print("Creating civilian user ID index...");
db.civilians.createIndex(
  { "civilian.userID": 1, "civilian.activeCommunityID": 1 },
  {
    name: "civilian_user_community_idx",
    background: true
  }
);
print("✓ Civilian user ID index created");

// CRITICAL: Firearm Registered Owner Index (for /firearms/registered-owner/{id})
print("Creating firearm registered owner index...");
db.firearms.createIndex(
  { "firearm.linkedCivilianID": 1, "firearm.registeredOwnerID": 1 },
  {
    name: "firearm_registered_owner_idx",
    background: true
  }
);
print("✓ Firearm registered owner index created");

// CRITICAL: Call Community ID Index (for /calls/community/{id})
print("Creating call community ID index...");
db.calls.createIndex(
  { "call.communityID": 1, "call.status": 1 },
  {
    name: "call_community_status_idx",
    background: true
  }
);
print("✓ Call community ID index created");

// CRITICAL: Community Subscription Plan + Visibility Index (for elite communities queries)
print("Creating community subscription plan + visibility index...");
db.communities.createIndex(
  { "community.subscription.plan": 1, "community.visibility": 1 },
  {
    name: "community_subscription_visibility_idx",
    background: true
  }
);
print("✓ Community subscription plan + visibility index created");

// MEDIUM PRIORITY: Community Tags Index (for tag-based queries)
print("Creating community tags index...");
db.communities.createIndex(
  { "community.tags": 1 },
  {
    name: "community_tags_idx",
    background: true
  }
);
print("✓ Community tags index created");

// CRITICAL: Invite Code Index (for /community/invite/{code})
print("Creating invite code index...");
db.inviteCodes.createIndex(
  { "code": 1 },
  {
    name: "invite_code_idx",
    unique: true,
    background: true
  }
);
print("✓ Invite code index created");

print("\n✅ All indexes created! Check index status with:");
print("db.users.getIndexes()");
print("db.communities.getIndexes()");
print("db.vehicles.getIndexes()");
print("db.civilians.getIndexes()");
print("db.firearms.getIndexes()");
print("db.calls.getIndexes()");

