// MongoDB Index Creation Script
// Run this in MongoDB Atlas → Your Cluster → "Browse Collections" → "Shell" tab
// OR connect via mongosh and run: mongosh "YOUR_CONNECTION_STRING" < create_indexes.js

// Helper function to safely create index (skips if index with same key pattern exists)
function createIndexSafe(collection, key, options) {
  const indexes = collection.getIndexes();
  const keyStr = JSON.stringify(key);
  
  // Check if index with same key pattern already exists
  const exists = indexes.some(idx => JSON.stringify(idx.key) === keyStr);
  
  if (exists) {
    const existingIdx = indexes.find(idx => JSON.stringify(idx.key) === keyStr);
    print(`⚠️  Index already exists: ${existingIdx.name} (skipping ${options.name || 'unnamed'})`);
    return;
  }
  
  try {
    collection.createIndex(key, options);
    print(`✓ Created index: ${options.name || 'unnamed'}`);
  } catch (e) {
    if (e.code === 85 || e.message.includes("already exists") || e.message.includes("IndexOptionsConflict")) {
      print(`⚠️  Index already exists (different name): ${options.name || 'unnamed'} - skipping`);
    } else if (e.code === 11000 || e.message.includes("duplicate key")) {
      print(`⚠️  Cannot create unique index ${options.name || 'unnamed'}: duplicate keys found in collection`);
      print(`   This means there are duplicate values in the collection.`);
      print(`   You may need to clean up duplicates or make the index non-unique.`);
      // Don't throw - continue with other indexes
    } else {
      print(`❌ Error creating index ${options.name || 'unnamed'}: ${e.message}`);
      // Don't throw - continue with other indexes
    }
  }
}

// CRITICAL: User Email Index (Case-Insensitive)
// This is used in authentication on EVERY request - most critical index
// DONE
createIndexSafe(
  db.users,
  { "user.email": 1 }, 
  { 
    name: "user_email_idx",
    collation: { locale: "en", strength: 2 },  // Case-insensitive
    background: true  // Don't block operations while building
  }
);

// HIGH PRIORITY: User Communities Index
// DONE
createIndexSafe(
  db.users,
  { "user.communities.communityId": 1, "user.communities.status": 1 }, 
  {
    name: "user_communities_idx",
    background: true
  }
);

// HIGH PRIORITY: User Search Text Index
// DONE
createIndexSafe(
  db.users,
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
createIndexSafe(
  db.communities,
  { "community.name": "text" }, 
  {
    name: "community_name_text_idx",
    background: true
  }
);

// MEDIUM PRIORITY: Community Visibility Index
// DONE
createIndexSafe(
  db.communities,
  { "community.visibility": 1 }, 
  {
    name: "community_visibility_idx",
    background: true
  }
);

// CRITICAL: Vehicle Registered Owner Index (for /vehicles/registered-owner/{id})
// DONE
createIndexSafe(
  db.vehicles,
  { "vehicle.linkedCivilianID": 1, "vehicle.registeredOwnerID": 1 },
  {
    name: "vehicle_registered_owner_idx",
    background: true
  }
);

// CRITICAL: Vehicle User ID Index (for /vehicles/user/{id})
// DONE
createIndexSafe(
  db.vehicles,
  { "vehicle.userID": 1, "vehicle.activeCommunityID": 1 },
  {
    name: "vehicle_user_community_idx",
    background: true
  }
);

// CRITICAL: Civilian User ID Index (for /civilians/user/{id})
// DONE
createIndexSafe(
  db.civilians,
  { "civilian.userID": 1, "civilian.activeCommunityID": 1 },
  {
    name: "civilian_user_community_idx",
    background: true
  }
);

// CRITICAL: Firearm Registered Owner Index (for /firearms/registered-owner/{id})
// The query uses $or with both fields, so we need separate indexes for each
// DONE
createIndexSafe(
  db.firearms,
  { "firearm.linkedCivilianID": 1, "firearm.registeredOwnerID": 1 },
  {
    name: "firearm_registered_owner_idx",
    background: true
  }
);

// CRITICAL: Separate indexes for $or queries (MongoDB can't use compound index efficiently for $or)
// These allow MongoDB to use index intersection for $or queries
// DONE
createIndexSafe(
  db.firearms,
  { "firearm.registeredOwnerID": 1 },
  {
    name: "firearm_registered_owner_id_idx",
    background: true
  }
);
createIndexSafe(
  db.firearms,
  { "firearm.linkedCivilianID": 1 },
  {
    name: "firearm_linked_civilian_id_idx",
    background: true
  }
);

// CRITICAL: Call Community ID Index (for /calls/community/{id})
// DONE
createIndexSafe(
  db.calls,
  { "call.communityID": 1, "call.status": 1 },
  {
    name: "call_community_status_idx",
    background: true
  }
);

// CRITICAL: Community Subscription Plan + Visibility Index (for elite communities queries)
// DONE
createIndexSafe(
  db.communities,
  { "community.subscription.plan": 1, "community.visibility": 1 },
  {
    name: "community_subscription_visibility_idx",
    background: true
  }
);

// MEDIUM PRIORITY: Community Tags Index (for tag-based queries)
// DONE
createIndexSafe(
  db.communities,
  { "community.tags": 1 },
  {
    name: "community_tags_idx",
    background: true
  }
);

// CRITICAL: Community Tags + Visibility Compound Index (for /communities/tag/{tag})
// DONE
createIndexSafe(
  db.communities,
  { "community.tags": 1, "community.visibility": 1 },
  {
    name: "community_tags_visibility_idx",
    background: true
  }
);

// CRITICAL: Community Tags + Visibility + Name Compound Index (for /communities/tag/{tag} with sorting)
// MongoDB was using visibility+name index and filtering tags in memory (5.4s slow!)
// This index allows MongoDB to use tag filter AND sort by name efficiently
// DONE
createIndexSafe(
  db.communities,
  { "community.tags": 1, "community.visibility": 1, "community.name": 1 },
  {
    name: "community_tags_visibility_name_idx",
    background: true
  }
);

// CRITICAL: Invite Code Index (for /community/invite/{code})
// NOTE: If this fails with duplicate key error, you need to clean up duplicate codes first:
// db.inviteCodes.aggregate([{$group: {_id: "$code", count: {$sum: 1}, docs: {$push: "$$ROOT"}}}, {$match: {count: {$gt: 1}}}])
// Then remove duplicates before creating the unique index
createIndexSafe(
  db.inviteCodes,
  { "code": 1 },
  {
    name: "invite_code_idx",
    unique: true,
    background: true
  }
);

// CRITICAL: Announcement Community + isActive + createdAt Index (for /community/{id}/announcements)
// DONE
createIndexSafe(
  db.announcements,
  { "community": 1, "isActive": 1, "createdAt": -1 },
  {
    name: "announcement_community_active_created_idx",
    background: true
  }
);

// CRITICAL: Community Subscription Created By Index (for /community/{user_id}/subscriptions)
// DONE
createIndexSafe(
  db.communities,
  { "community.subscriptionCreatedBy": 1 },
  {
    name: "community_subscription_created_by_idx",
    background: true
  }
);

// CRITICAL: License Civilian ID Index (for /licenses/civilian/{id})
// DONE
createIndexSafe(
  db.licenses,
  { "license.civilianID": 1 },
  {
    name: "license_civilian_id_idx",
    background: true
  }
);

// CRITICAL: Arrest Report Arrestee ID Index (for /arrest-report/arrestee/{id})
// DONE
createIndexSafe(
  db.arrestreports,
  { "arrestReport.arrestee.id": 1 },
  {
    name: "arrest_report_arrestee_id_idx",
    background: true
  }
);

// CRITICAL: Warrant Accused ID + Status Index (for /warrants/user/{id})
// Large collection (203K docs) - needs index for efficient queries
createIndexSafe(
  db.warrants,
  { "warrant.accusedID": 1, "warrant.status": 1 },
  {
    name: "warrant_accused_id_status_idx",
    background: true
  }
);

