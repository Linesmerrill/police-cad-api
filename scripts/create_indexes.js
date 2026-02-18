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

// CRITICAL: Vehicle Active Community ID Index (single field for queries filtering only by activeCommunityID)
// Needed for queries that filter by activeCommunityID without userID
// This fixes Query Targeting alerts for vehicles queries
createIndexSafe(
  db.vehicles,
  { "vehicle.activeCommunityID": 1 },
  {
    name: "vehicle_active_community_idx",
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

// CRITICAL: Civilian Pending Approvals Index (for /civilian/pending-approvals)
// Query filters by activeCommunityID + approvalStatus (pending/requested_review)
// Compound index optimizes this exact query pattern
createIndexSafe(
  db.civilians,
  { "civilian.activeCommunityID": 1, "civilian.approvalStatus": 1, "civilian.createdAt": -1 },
  {
    name: "civilian_pending_approvals_idx",
    background: true
  }
);

// CRITICAL: Civilian Active Community ID Index (single field for queries filtering only by activeCommunityID)
// Needed for queries that filter by activeCommunityID without approvalStatus
// This fixes Query Targeting alerts for civilians queries
createIndexSafe(
  db.civilians,
  { "civilian.activeCommunityID": 1 },
  {
    name: "civilian_active_community_idx",
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

// CRITICAL: Firearm Active Community ID Index (for queries filtering by activeCommunityID)
// Needed for queries that filter by activeCommunityID
// This fixes Query Targeting alerts for firearms queries
createIndexSafe(
  db.firearms,
  { "firearm.activeCommunityID": 1 },
  {
    name: "firearm_active_community_idx",
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
// DONE
createIndexSafe(
  db.warrants,
  { "warrant.accusedID": 1, "warrant.status": 1 },
  {
    name: "warrant_accused_id_status_idx",
    background: true
  }
);

// HIGH PRIORITY: Medical Report Civilian + Community Index (for /medical-reports/civilian/{id})
// 14.5K docs with COLLSCAN — every medical report lookup scans entire collection
// Triggers MongoDB >1000 object scan alert
createIndexSafe(
  db.medicalreports,
  { "report.civilianID": 1, "report.activeCommunityID": 1 },
  {
    name: "medical_civilian_community_idx",
    background: true
  }
);

// HIGH PRIORITY: Warrant Community + Status Index (for warrant stats + search)
// Judicial dashboard fires 4 count queries on every load filtering by activeCommunityID + status
// Also used by WarrantsSearchHandler and PendingWarrantsHandler
createIndexSafe(
  db.warrants,
  { "warrant.activeCommunityID": 1, "warrant.status": 1 },
  {
    name: "warrant_community_status_idx",
    background: true
  }
);

// CRITICAL: User IsOnline Index (for /community/{id}/online-users)
// Large collection (804K docs) - COLLSCAN found: 804,288 scanned, 12,854 returned (62.57:1 ratio, 33.4s)
createIndexSafe(
  db.users,
  { "user.isOnline": 1 },
  {
    name: "user_is_online_idx",
    background: true
  }
);

// CRITICAL: User Email Verification Token Index (for email verification queries)
// Large collection (804K docs) - COLLSCAN found: 804,288 scanned, 0 returned (Infinity ratio, 35-37s)
// Compound index includes expires for efficient token + expiration queries
createIndexSafe(
  db.users,
  { "user.emailVerificationToken": 1, "user.emailVerificationExpires": 1 },
  {
    name: "user_email_verification_token_idx",
    background: true
  }
);

// CRITICAL: Call Status Index (for queries filtering by call status)
// Medium collection (44K docs) - COLLSCAN found: 43,927 scanned, 0 returned (Infinity ratio, 4.3s)
// Note: Compound index on call.communityID + call.status exists, but single-field needed for status-only queries
createIndexSafe(
  db.calls,
  { "call.status": 1 },
  {
    name: "call_status_idx",
    background: true
  }
);

// CRITICAL: Announcement Community Index (for /community/{id}/announcements)
// Small collection (107 docs) but COLLSCAN found - ensure index exists
// Note: Compound index exists on {community, isActive, createdAt}, but verify field names match
// The query uses "announcement.community" but index might use "community" - check actual schema
createIndexSafe(
  db.announcements,
  { "announcement.community": 1, "announcement.isActive": 1, "announcement.createdAt": -1 },
  {
    name: "announcement_community_active_created_idx_v2",
    background: true
  }
);

// ============================================================================
// PERFORMANCE ADVISOR RECOMMENDED INDEXES
// Based on MongoDB Performance Advisor analysis of slow queries
// ============================================================================

// HIGH PRIORITY: User Username + _id Index (Performance Advisor)
// Expected Impact: Can reduce up to 269.1 MB of disk reads from 13.08 queries/hour
// Avg Execution Time: 44474 ms, Avg Docs Scanned: 705303
createIndexSafe(
  db.users,
  { "user.username": 1, "_id": 1 },
  {
    name: "user_username_id_idx",
    background: true
  }
);

// HIGH PRIORITY: User Name + _id Index (Performance Advisor)
// Expected Impact: Can reduce up to 269.1 MB of disk reads from 13.08 queries/hour
// Avg Execution Time: 44474 ms, Avg Docs Scanned: 705303
createIndexSafe(
  db.users,
  { "user.name": 1, "_id": 1 },
  {
    name: "user_name_id_idx",
    background: true
  }
);

// HIGH PRIORITY: User Email Index (Performance Advisor)
// Expected Impact: Can reduce up to 306.8 MB of disk reads from 9.46 queries/hour
// Avg Execution Time: 60026 ms, Avg Docs Scanned: 804243
// NOTE: This is for top-level "email" field, different from "user.email" index above
createIndexSafe(
  db.users,
  { "email": 1 },
  {
    name: "email_idx",
    background: true
  }
);

// HIGH PRIORITY: Community Visibility + Name Index (Performance Advisor)
// Expected Impact: 15.5 queries/hour
// Avg Execution Time: 15056 ms, Avg Docs Scanned: 37670
createIndexSafe(
  db.communities,
  { "community.visibility": 1, "community.name": 1 },
  {
    name: "community_visibility_name_idx",
    background: true
  }
);

// MEDIUM PRIORITY: Community Visibility + _id Index (Performance Advisor)
// Expected Impact: 4.5 queries/hour
// Avg Execution Time: 2556 ms, Avg Docs Scanned: 3825
createIndexSafe(
  db.communities,
  { "community.visibility": 1, "_id": 1 },
  {
    name: "community_visibility_id_idx",
    background: true
  }
);

// LOW PRIORITY: Community Visibility + MembersCount Index (Performance Advisor)
// Expected Impact: 0.13 queries/hour
// Avg Execution Time: 1092 ms, Avg Docs Scanned: 3825
createIndexSafe(
  db.communities,
  { "community.visibility": 1, "community.membersCount": 1 },
  {
    name: "community_visibility_membersCount_idx",
    background: true
  }
);

// HIGH PRIORITY: Pending Verification Email Index (Performance Advisor)
// Expected Impact: 4.46 queries/hour
// Avg Execution Time: 774 ms, Avg Docs Scanned: 6214
createIndexSafe(
  db.pendingVerifications,
  { "email": 1 },
  {
    name: "email_idx",
    background: true
  }
);

// MEDIUM PRIORITY: Invite Code + Remaining Uses Index (Performance Advisor)
// Expected Impact: 0.71 queries/hour
// Avg Execution Time: 1047 ms, Avg Docs Scanned: 6680
createIndexSafe(
  db.inviteCodes,
  { "code": 1, "remainingUses": 1 },
  {
    name: "code_remainingUses_idx",
    background: true
  }
);

// LOW PRIORITY: Invite Code CommunityId + CreatedAt Index (Performance Advisor)
// Expected Impact: 0.04 queries/hour
// Avg Execution Time: 472 ms, Avg Docs Scanned: 7808
createIndexSafe(
  db.inviteCodes,
  { "communityId": 1, "createdAt": -1 },
  {
    name: "communityId_createdAt_idx",
    background: true
  }
);

// LOW PRIORITY: EMS Vehicle ActiveCommunityID + UserID Index (Performance Advisor)
// Expected Impact: 0.13 queries/hour
// Avg Execution Time: 522 ms, Avg Docs Scanned: 38803
createIndexSafe(
  db.emsvehicles,
  { "vehicle.activeCommunityID": 1, "vehicle.userID": 1 },
  {
    name: "vehicle_activeCommunityID_userID_idx",
    background: true
  }
);

// LOW PRIORITY: EMS Persona ActiveCommunityID + UserID Index (Performance Advisor)
// Expected Impact: 0.08 queries/hour
// Avg Execution Time: 6208 ms, Avg Docs Scanned: 48919
createIndexSafe(
  db.ems,
  { "persona.activeCommunityID": 1, "persona.userID": 1 },
  {
    name: "persona_activeCommunityID_userID_idx",
    background: true
  }
);

// MEDIUM PRIORITY: Vehicle ActiveCommunityID + VIN Index (Performance Advisor)
// Expected Impact: 2.46 queries/hour
// Avg Execution Time: 1824 ms, Avg Docs Scanned: 957
createIndexSafe(
  db.vehicles,
  { "vehicle.activeCommunityID": 1, "vehicle.vin": 1 },
  {
    name: "vehicle_activeCommunityID_vin_idx",
    background: true
  }
);

// MEDIUM PRIORITY: Vehicle ActiveCommunityID + Make Index (Performance Advisor)
// Expected Impact: 2.46 queries/hour
// Avg Execution Time: 1824 ms, Avg Docs Scanned: 957
createIndexSafe(
  db.vehicles,
  { "vehicle.activeCommunityID": 1, "vehicle.make": 1 },
  {
    name: "vehicle_activeCommunityID_make_idx",
    background: true
  }
);

// MEDIUM PRIORITY: Vehicle ActiveCommunityID + Model Index (Performance Advisor)
// Expected Impact: 2.46 queries/hour
// Avg Execution Time: 1824 ms, Avg Docs Scanned: 957
createIndexSafe(
  db.vehicles,
  { "vehicle.activeCommunityID": 1, "vehicle.model": 1 },
  {
    name: "vehicle_activeCommunityID_model_idx",
    background: true
  }
);

// MEDIUM PRIORITY: Civilian ActiveCommunityID + Name Index (Performance Advisor)
// Expected Impact: 1.04 queries/hour
// Avg Execution Time: 1197 ms, Avg Docs Scanned: 799
createIndexSafe(
  db.civilians,
  { "civilian.activeCommunityID": 1, "civilian.name": 1 },
  {
    name: "civilian_activeCommunityID_name_idx",
    background: true
  }
);

print("\n=== All indexes (including Performance Advisor recommendations) processed ===");

