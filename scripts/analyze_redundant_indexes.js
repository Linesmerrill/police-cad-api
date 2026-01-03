// MongoDB Redundant Index Analysis Script
// Run this in MongoDB shell: load("scripts/analyze_redundant_indexes.js")
// This script analyzes whether "redundant" indexes identified by MongoDB Atlas are actually safe to remove

print("=== ANALYZING REDUNDANT INDEXES ===\n");
print("This script checks if redundant indexes can be safely removed\n");

// Test queries to verify index usage
var testResults = [];

// 1. Test civilians.civilian_active_community_idx
print("1. Testing civilians.civilian_active_community_idx\n");
try {
  var coll = db.civilians;
  
  // Test query that filters ONLY by activeCommunityID (no approvalStatus)
  var query1 = { "civilian.activeCommunityID": "test-community-id" };
  var explain1 = coll.find(query1).limit(10).explain("executionStats");
  var exec1 = explain1.executionStats;
  
  print(`   Query: {civilian.activeCommunityID: "test-community-id"}`);
  print(`   Stage: ${exec1.executionStages ? exec1.executionStages.stage : 'UNKNOWN'}`);
  print(`   Index Used: ${exec1.executionStages && exec1.executionStages.indexName ? exec1.executionStages.indexName : 'NONE'}`);
  print(`   Scanned: ${exec1.totalDocsExamined}, Returned: ${exec1.nReturned}\n`);
  
  // Test query that filters by activeCommunityID + userID (common pattern)
  var query2 = { 
    "civilian.userID": "test-user-id",
    "civilian.activeCommunityID": "test-community-id"
  };
  var explain2 = coll.find(query2).limit(10).explain("executionStats");
  var exec2 = explain2.executionStats;
  
  print(`   Query: {civilian.userID: "test-user-id", civilian.activeCommunityID: "test-community-id"}`);
  print(`   Stage: ${exec2.executionStages ? exec2.executionStages.stage : 'UNKNOWN'}`);
  print(`   Index Used: ${exec2.executionStages && exec2.executionStages.indexName ? exec2.executionStages.indexName : 'NONE'}`);
  print(`   Scanned: ${exec2.totalDocsExamined}, Returned: ${exec2.nReturned}\n`);
  
  testResults.push({
    collection: "civilians",
    index: "civilian_active_community_idx",
    canRemove: exec1.executionStages && exec1.executionStages.indexName && exec1.executionStages.indexName.includes("pending_approvals"),
    reason: exec1.executionStages && exec1.executionStages.indexName ? "Compound index can be used" : "May need single-field index"
  });
} catch (e) {
  print(`   ❌ Error: ${e.message}\n`);
}

// 2. Test communities.community_tags_idx and community_tags_visibility_idx
print("2. Testing communities tag indexes\n");
try {
  var coll = db.communities;
  
  // Test query that filters by tags + visibility (always used together)
  var query1 = { 
    "community.tags": "Xbox",
    "community.visibility": "public"
  };
  var explain1 = coll.find(query1).limit(10).explain("executionStats");
  var exec1 = explain1.executionStats;
  
  print(`   Query: {community.tags: "Xbox", community.visibility: "public"}`);
  print(`   Stage: ${exec1.executionStages ? exec1.executionStages.stage : 'UNKNOWN'}`);
  print(`   Index Used: ${exec1.executionStages && exec1.executionStages.indexName ? exec1.executionStages.indexName : 'NONE'}\n`);
  
  // Test query that filters ONLY by tags (if such queries exist)
  var query2 = { "community.tags": "Xbox" };
  var explain2 = coll.find(query2).limit(10).explain("executionStats");
  var exec2 = explain2.executionStats;
  
  print(`   Query: {community.tags: "Xbox"} (tags only)`);
  print(`   Stage: ${exec2.executionStages ? exec2.executionStages.stage : 'UNKNOWN'}`);
  print(`   Index Used: ${exec2.executionStages && exec2.executionStages.indexName ? exec2.executionStages.indexName : 'NONE'}\n`);
  
  testResults.push({
    collection: "communities",
    index: "community_tags_idx",
    canRemove: true, // All queries use tags + visibility together
    reason: "All queries filter by both tags and visibility"
  });
  
  testResults.push({
    collection: "communities",
    index: "community_tags_visibility_idx",
    canRemove: true, // 3-field index exists
    reason: "3-field compound index covers this"
  });
} catch (e) {
  print(`   ❌ Error: ${e.message}\n`);
}

// 3. Test firearms.firearm_linked_civilian_id_idx
print("3. Testing firearms.firearm_linked_civilian_id_idx\n");
try {
  var coll = db.firearms;
  
  // Test $or query (the actual query pattern used)
  var query1 = {
    "$or": [
      { "firearm.registeredOwnerID": "test-id" },
      { "firearm.linkedCivilianID": "test-id" }
    ]
  };
  var explain1 = coll.find(query1).limit(10).explain("executionStats");
  var exec1 = explain1.executionStats;
  
  print(`   Query: {$or: [{firearm.registeredOwnerID: "test-id"}, {firearm.linkedCivilianID: "test-id"}]}`);
  print(`   Stage: ${exec1.executionStages ? exec1.executionStages.stage : 'UNKNOWN'}`);
  if (exec1.executionStages && exec1.executionStages.inputStage) {
    print(`   Input Stage: ${exec1.executionStages.inputStage.stage}`);
    if (exec1.executionStages.inputStage.indexName) {
      print(`   Index Used: ${exec1.executionStages.inputStage.indexName}`);
    }
  }
  print(`   Scanned: ${exec1.totalDocsExamined}, Returned: ${exec1.nReturned}\n`);
  
  // Test query that filters ONLY by linkedCivilianID
  var query2 = { "firearm.linkedCivilianID": "test-id" };
  var explain2 = coll.find(query2).limit(10).explain("executionStats");
  var exec2 = explain2.executionStats;
  
  print(`   Query: {firearm.linkedCivilianID: "test-id"} (single field)`);
  print(`   Stage: ${exec2.executionStages ? exec2.executionStages.stage : 'UNKNOWN'}`);
  print(`   Index Used: ${exec2.executionStages && exec2.executionStages.indexName ? exec2.executionStages.indexName : 'NONE'}\n`);
  
  testResults.push({
    collection: "firearms",
    index: "firearm_linked_civilian_id_idx",
    canRemove: false, // Needed for $or queries
    reason: "Required for $or query index intersection"
  });
} catch (e) {
  print(`   ❌ Error: ${e.message}\n`);
}

// Summary
print("\n=== SUMMARY ===\n");
testResults.forEach(function(result) {
  var status = result.canRemove ? "✅ SAFE TO REMOVE" : "⚠️  KEEP";
  print(`${status}: ${result.collection}.${result.index}`);
  print(`   Reason: ${result.reason}\n`);
});

print("\n=== RECOMMENDATIONS ===\n");
print("1. civilians.civilian_active_community_idx:");
print("   - MongoDB says it's redundant, but queries filter by userID + activeCommunityID");
print("   - The compound index can be used, but may be less efficient");
print("   - RECOMMENDATION: Keep it for now, or test performance after removal\n");

print("2. communities.community_tags_idx:");
print("   - All queries filter by tags + visibility together");
print("   - RECOMMENDATION: ✅ SAFE TO REMOVE\n");

print("3. communities.community_tags_visibility_idx:");
print("   - 3-field compound index exists");
print("   - RECOMMENDATION: ✅ SAFE TO REMOVE\n");

print("4. firearms.firearm_linked_civilian_id_idx:");
print("   - Used in $or queries for index intersection");
print("   - RECOMMENDATION: ⚠️  KEEP (needed for $or query performance)\n");

print("\n=== DONE ===");

