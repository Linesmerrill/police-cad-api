// MongoDB Index Verification Script
// Run this to check which indexes exist and identify missing ones

print("=== CHECKING INDEXES ===\n");

// Communities Collection
print("📁 COMMUNITIES COLLECTION:");
print("Indexes:");
db.communities.getIndexes().forEach(idx => {
  print(`  ✓ ${idx.name}: ${JSON.stringify(idx.key)}`);
});

// Check for redundant index
const redundantIdx = db.communities.getIndexes().find(idx => 
  idx.name === "community.visibility_1_community.name_1"
);
if (redundantIdx) {
  print("\n⚠️  REDUNDANT INDEX FOUND:");
  print("  - community.visibility_1_community.name_1");
  print("  This is superseded by community_tags_visibility_name_idx");
  print("  Consider dropping it to improve write performance");
}

print("\n📁 USERS COLLECTION:");
print("Indexes:");
db.users.getIndexes().forEach(idx => {
  print(`  ✓ ${idx.name}: ${JSON.stringify(idx.key)}`);
});

print("\n📁 VEHICLES COLLECTION:");
print("Indexes:");
db.vehicles.getIndexes().forEach(idx => {
  print(`  ✓ ${idx.name}: ${JSON.stringify(idx.key)}`);
});

print("\n📁 CALLS COLLECTION:");
print("Indexes:");
db.calls.getIndexes().forEach(idx => {
  print(`  ✓ ${idx.name}: ${JSON.stringify(idx.key)}`);
});

print("\n📁 LICENSES COLLECTION:");
print("Indexes:");
db.licenses.getIndexes().forEach(idx => {
  print(`  ✓ ${idx.name}: ${JSON.stringify(idx.key)}`);
});

print("\n📁 FIREARMS COLLECTION:");
print("Indexes:");
db.firearms.getIndexes().forEach(idx => {
  print(`  ✓ ${idx.name}: ${JSON.stringify(idx.key)}`);
});

print("\n📁 CIVILIANS COLLECTION:");
print("Indexes:");
db.civilians.getIndexes().forEach(idx => {
  print(`  ✓ ${idx.name}: ${JSON.stringify(idx.key)}`);
});

print("\n=== CHECKING FOR MISSING CRITICAL INDEXES ===\n");

// Check for critical indexes
const criticalIndexes = {
  users: [
    { name: "user_email_idx", key: { "user.email": 1 } },
    { name: "user_communities_idx", key: { "user.communities.communityId": 1, "user.communities.status": 1 } },
    { name: "user_search_text_idx", key: { "user.username": "text", "user.callSign": "text", "user.name": "text" } }
  ],
  communities: [
    { name: "community_visibility_idx", key: { "community.visibility": 1 } },
    { name: "community_tags_visibility_name_idx", key: { "community.tags": 1, "community.visibility": 1, "community.name": 1 } },
    { name: "community_subscription_created_by_idx", key: { "community.subscriptionCreatedBy": 1 } }
  ],
  vehicles: [
    { name: "vehicle_user_community_idx", key: { "vehicle.userID": 1, "vehicle.activeCommunityID": 1 } },
    { name: "vehicle_registered_owner_idx", key: { "vehicle.linkedCivilianID": 1, "vehicle.registeredOwnerID": 1 } }
  ],
  calls: [
    { name: "call_community_status_idx", key: { "call.communityID": 1, "call.status": 1 } }
  ],
  licenses: [
    { name: "license_civilian_id_idx", key: { "license.civilianID": 1 } }
  ],
  firearms: [
    { name: "firearm_registered_owner_idx", key: { "firearm.linkedCivilianID": 1, "firearm.registeredOwnerID": 1 } }
  ],
  civilians: [
    { name: "civilian_user_community_idx", key: { "civilian.userID": 1, "civilian.activeCommunityID": 1 } }
  ]
};

let missingCount = 0;

Object.keys(criticalIndexes).forEach(collection => {
  const indexes = db[collection].getIndexes();
  const indexKeys = indexes.map(idx => JSON.stringify(idx.key));
  
  criticalIndexes[collection].forEach(expectedIdx => {
    const expectedKeyStr = JSON.stringify(expectedIdx.key);
    const indexExists = indexKeys.some(keyStr => keyStr === expectedKeyStr);
    const indexName = indexes.find(idx => JSON.stringify(idx.key) === expectedKeyStr)?.name;
    
    if (!indexExists) {
      print(`❌ MISSING: ${collection}.${expectedIdx.name}`);
      print(`   Key: ${JSON.stringify(expectedIdx.key)}`);
      missingCount++;
    } else if (indexName && indexName !== expectedIdx.name) {
      // Index exists but with different name (likely autocreated)
      print(`⚠️  EXISTS (different name): ${collection}.${expectedIdx.name}`);
      print(`   Found as: ${indexName}`);
      print(`   Key: ${JSON.stringify(expectedIdx.key)}`);
      print(`   (This is fine - MongoDB auto-created it)`);
    }
  });
});

if (missingCount === 0) {
  print("✅ All critical indexes exist!");
} else {
  print(`\n⚠️  Found ${missingCount} missing critical indexes`);
  print("Run: load('scripts/create_indexes.js') to create them");
}

print("\n=== DONE ===");

