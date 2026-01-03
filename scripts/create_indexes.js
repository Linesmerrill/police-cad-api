// MongoDB Index Creation Script
// Run this in MongoDB Atlas → Your Cluster → "Browse Collections" → "Shell" tab
// OR connect via mongosh and run: mongosh "YOUR_CONNECTION_STRING" < create_indexes.js

// CRITICAL: User Email Index (Case-Insensitive)
// This is used in authentication on EVERY request - most critical index
// DONE
db.users.createIndex(
  { "user.email": 1 }, 
  { 
    name: "user_email_idx",
    collation: { locale: "en", strength: 2 },  // Case-insensitive
    background: true  // Don't block operations while building
  }
);

// HIGH PRIORITY: User Communities Index
// DONE
db.users.createIndex(
  { "user.communities.communityId": 1, "user.communities.status": 1 }, 
  {
    name: "user_communities_idx",
    background: true
  }
);

// HIGH PRIORITY: User Search Text Index
// DONE
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

// MEDIUM PRIORITY: Community Name Text Index
// DONE
db.communities.createIndex(
  { "community.name": "text" }, 
  {
    name: "community_name_text_idx",
    background: true
  }
);

// MEDIUM PRIORITY: Community Visibility Index
// DONE
db.communities.createIndex(
  { "community.visibility": 1 }, 
  {
    name: "community_visibility_idx",
    background: true
  }
);

// CRITICAL: Vehicle Registered Owner Index (for /vehicles/registered-owner/{id})
// DONE
db.vehicles.createIndex(
  { "vehicle.linkedCivilianID": 1, "vehicle.registeredOwnerID": 1 },
  {
    name: "vehicle_registered_owner_idx",
    background: true
  }
);

// CRITICAL: Vehicle User ID Index (for /vehicles/user/{id})
// DONE
db.vehicles.createIndex(
  { "vehicle.userID": 1, "vehicle.activeCommunityID": 1 },
  {
    name: "vehicle_user_community_idx",
    background: true
  }
);

// CRITICAL: Civilian User ID Index (for /civilians/user/{id})
// DONE
db.civilians.createIndex(
  { "civilian.userID": 1, "civilian.activeCommunityID": 1 },
  {
    name: "civilian_user_community_idx",
    background: true
  }
);

// CRITICAL: Firearm Registered Owner Index (for /firearms/registered-owner/{id})
// DONE
db.firearms.createIndex(
  { "firearm.linkedCivilianID": 1, "firearm.registeredOwnerID": 1 },
  {
    name: "firearm_registered_owner_idx",
    background: true
  }
);

// CRITICAL: Call Community ID Index (for /calls/community/{id})
// DONE
db.calls.createIndex(
  { "call.communityID": 1, "call.status": 1 },
  {
    name: "call_community_status_idx",
    background: true
  }
);

// CRITICAL: Community Subscription Plan + Visibility Index (for elite communities queries)
// DONE
db.communities.createIndex(
  { "community.subscription.plan": 1, "community.visibility": 1 },
  {
    name: "community_subscription_visibility_idx",
    background: true
  }
);

// MEDIUM PRIORITY: Community Tags Index (for tag-based queries)
// DONE
db.communities.createIndex(
  { "community.tags": 1 },
  {
    name: "community_tags_idx",
    background: true
  }
);

// CRITICAL: Community Tags + Visibility Compound Index (for /communities/tag/{tag})
// DONE
db.communities.createIndex(
  { "community.tags": 1, "community.visibility": 1 },
  {
    name: "community_tags_visibility_idx",
    background: true
  }
);

// CRITICAL: Community Tags + Visibility + Name Compound Index (for /communities/tag/{tag} with sorting)
// MongoDB was using visibility+name index and filtering tags in memory (5.4s slow!)
// This index allows MongoDB to use tag filter AND sort by name efficiently
print("Creating community tags + visibility + name compound index...");
db.communities.createIndex(
  { "community.tags": 1, "community.visibility": 1, "community.name": 1 },
  {
    name: "community_tags_visibility_name_idx",
    background: true
  }
);
print("✓ Community tags + visibility + name compound index created");

// CRITICAL: Invite Code Index (for /community/invite/{code})
// DONE
db.inviteCodes.createIndex(
  { "code": 1 },
  {
    name: "invite_code_idx",
    unique: true,
    background: true
  }
);

// CRITICAL: Announcement Community + isActive + createdAt Index (for /community/{id}/announcements)
// DONE
db.announcements.createIndex(
  { "community": 1, "isActive": 1, "createdAt": -1 },
  {
    name: "announcement_community_active_created_idx",
    background: true
  }
);

// CRITICAL: License Civilian ID Index (for /licenses/civilian/{id})
print("Creating license civilian ID index...");
db.licenses.createIndex(
  { "license.civilianID": 1 },
  {
    name: "license_civilian_id_idx",
    background: true
  }
);
print("✓ License civilian ID index created");

