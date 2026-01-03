// MongoDB Query Targeting Analysis Script
// Run this in MongoDB shell: load("scripts/analyze_query_targeting.js")
// This script analyzes all collections to find queries with high scanned/returned ratios

print("=== MONGODB QUERY TARGETING ANALYSIS ===\n");
print("Analyzing all collections for queries with scanned/returned > 1000...\n");

// Get all collection names
var collections = db.getCollectionNames();
var problematicQueries = [];

// Common query patterns to test for each collection
function analyzeCollection(collectionName) {
  print(`\n📊 Analyzing collection: ${collectionName}`);
  var coll = db.getCollection(collectionName);
  var stats = coll.stats();
  var docCount = stats.count || 0;
  
  if (docCount === 0) {
    print(`   ⚠️  Collection is empty, skipping...`);
    return;
  }
  
  print(`   Documents: ${docCount.toLocaleString()}`);
  
  // Get indexes
  var indexes = coll.getIndexes();
  var indexFields = new Set();
  indexes.forEach(function(idx) {
    Object.keys(idx.key).forEach(function(key) {
      indexFields.add(key);
    });
  });
  
  print(`   Indexes: ${indexes.length} (fields: ${Array.from(indexFields).join(', ') || 'none'})`);
  
  // Test common query patterns based on collection name
  var testQueries = getTestQueriesForCollection(collectionName);
  
  if (testQueries.length === 0) {
    print(`   ℹ️  No standard query patterns to test`);
    return;
  }
  
  testQueries.forEach(function(testQuery) {
    try {
      var explainResult = coll.find(testQuery.filter).explain("executionStats");
      var executionStats = explainResult.executionStats;
      
      if (!executionStats) {
        print(`   ⚠️  Could not get execution stats for query`);
        return;
      }
      
      var totalDocsExamined = executionStats.totalDocsExamined || 0;
      var nReturned = executionStats.nReturned || 0;
      var executionTimeMillis = executionStats.executionTimeMillis || 0;
      var stage = executionStats.executionStages ? executionStats.executionStages.stage : 'UNKNOWN';
      
      // Calculate ratio
      var ratio = nReturned > 0 ? (totalDocsExamined / nReturned) : (totalDocsExamined > 0 ? Infinity : 0);
      
      // Check if problematic
      var isProblematic = ratio > 1000 || stage === 'COLLSCAN' || executionTimeMillis > 1000;
      
      if (isProblematic) {
        var severity = ratio > 10000 ? '🔴 CRITICAL' : ratio > 1000 ? '🟠 HIGH' : '🟡 MEDIUM';
        print(`   ${severity} Query: ${JSON.stringify(testQuery.filter)}`);
        print(`      Stage: ${stage}`);
        print(`      Scanned: ${totalDocsExamined.toLocaleString()}, Returned: ${nReturned.toLocaleString()}, Ratio: ${ratio.toFixed(2)}:1`);
        print(`      Execution Time: ${executionTimeMillis}ms`);
        
        // Suggest index if COLLSCAN
        if (stage === 'COLLSCAN' && testQuery.suggestedIndex) {
          print(`      💡 Suggested Index: ${JSON.stringify(testQuery.suggestedIndex)}`);
        }
        
        problematicQueries.push({
          collection: collectionName,
          filter: testQuery.filter,
          stage: stage,
          scanned: totalDocsExamined,
          returned: nReturned,
          ratio: ratio,
          executionTime: executionTimeMillis,
          suggestedIndex: testQuery.suggestedIndex
        });
      } else {
        print(`   ✅ Query OK: ${JSON.stringify(testQuery.filter)} (ratio: ${ratio.toFixed(2)}:1, time: ${executionTimeMillis}ms)`);
      }
    } catch (e) {
      print(`   ❌ Error testing query: ${e.message}`);
    }
  });
}

// Get test queries based on collection name
function getTestQueriesForCollection(collectionName) {
  var queries = [];
  
  // Users collection
  if (collectionName === 'users') {
    queries.push({
      filter: { "user.email": "test@example.com" },
      suggestedIndex: { "user.email": 1 }
    });
    queries.push({
      filter: { "user.communities.communityId": "test-id" },
      suggestedIndex: { "user.communities.communityId": 1, "user.communities.status": 1 }
    });
    queries.push({
      filter: { "user.isOnline": true },
      suggestedIndex: { "user.isOnline": 1 }
    });
  }
  
  // Communities collection
  if (collectionName === 'communities') {
    queries.push({
      filter: { "community.visibility": "public" },
      suggestedIndex: { "community.visibility": 1 }
    });
    queries.push({
      filter: { "community.tags": "Xbox" },
      suggestedIndex: { "community.tags": 1, "community.visibility": 1 }
    });
    queries.push({
      filter: { "community.subscriptionCreatedBy": "test-id" },
      suggestedIndex: { "community.subscriptionCreatedBy": 1 }
    });
  }
  
  // Vehicles collection
  if (collectionName === 'vehicles') {
    queries.push({
      filter: { "vehicle.userID": "test-id" },
      suggestedIndex: { "vehicle.userID": 1, "vehicle.activeCommunityID": 1 }
    });
    queries.push({
      filter: { "vehicle.registeredOwnerID": "test-id" },
      suggestedIndex: { "vehicle.registeredOwnerID": 1 }
    });
    queries.push({
      filter: { "vehicle.linkedCivilianID": "test-id" },
      suggestedIndex: { "vehicle.linkedCivilianID": 1 }
    });
  }
  
  // Civilians collection
  if (collectionName === 'civilians') {
    queries.push({
      filter: { "civilian.userID": "test-id" },
      suggestedIndex: { "civilian.userID": 1 }
    });
    queries.push({
      filter: { "civilian.activeCommunityID": "test-id" },
      suggestedIndex: { "civilian.activeCommunityID": 1 }
    });
  }
  
  // Firearms collection
  if (collectionName === 'firearms') {
    queries.push({
      filter: { "firearm.registeredOwnerID": "test-id" },
      suggestedIndex: { "firearm.registeredOwnerID": 1 }
    });
    queries.push({
      filter: { "firearm.linkedCivilianID": "test-id" },
      suggestedIndex: { "firearm.linkedCivilianID": 1 }
    });
  }
  
  // Calls collection
  if (collectionName === 'calls') {
    queries.push({
      filter: { "call.communityID": "test-id" },
      suggestedIndex: { "call.communityID": 1 }
    });
    queries.push({
      filter: { "call.status": "active" },
      suggestedIndex: { "call.status": 1 }
    });
  }
  
  // Licenses collection
  if (collectionName === 'licenses') {
    queries.push({
      filter: { "license.civilianID": "test-id" },
      suggestedIndex: { "license.civilianID": 1 }
    });
  }
  
  // Arrest Reports collection
  if (collectionName === 'arrestreports') {
    queries.push({
      filter: { "arrestReport.arrestee.id": "test-id" },
      suggestedIndex: { "arrestReport.arrestee.id": 1 }
    });
  }
  
  // Warrants collection
  if (collectionName === 'warrants') {
    queries.push({
      filter: { "warrant.accusedID": "test-id" },
      suggestedIndex: { "warrant.accusedID": 1, "warrant.status": 1 }
    });
  }
  
  // Announcements collection
  if (collectionName === 'announcements') {
    queries.push({
      filter: { "announcement.community": "test-id" },
      suggestedIndex: { "announcement.community": 1, "announcement.isActive": 1, "announcement.createdAt": -1 }
    });
  }
  
  // Invite Codes collection
  if (collectionName === 'inviteCodes') {
    queries.push({
      filter: { "code": "TEST123" },
      suggestedIndex: { "code": 1 }
    });
  }
  
  return queries;
}

// Analyze all collections
collections.forEach(function(collectionName) {
  // Skip system collections
  if (collectionName.startsWith('system.') || collectionName === 'local' || collectionName === 'admin') {
    return;
  }
  
  analyzeCollection(collectionName);
});

// Summary
print("\n\n=== SUMMARY ===");
if (problematicQueries.length === 0) {
  print("✅ No problematic queries found!");
} else {
  print(`\n🔴 Found ${problematicQueries.length} problematic queries:\n`);
  
  // Group by collection
  var byCollection = {};
  problematicQueries.forEach(function(q) {
    if (!byCollection[q.collection]) {
      byCollection[q.collection] = [];
    }
    byCollection[q.collection].push(q);
  });
  
  Object.keys(byCollection).sort().forEach(function(collection) {
    print(`\n📁 ${collection}:`);
    byCollection[collection].forEach(function(q) {
      var severity = q.ratio > 10000 ? '🔴 CRITICAL' : q.ratio > 1000 ? '🟠 HIGH' : '🟡 MEDIUM';
      print(`   ${severity} Ratio: ${q.ratio.toFixed(2)}:1 (${q.scanned.toLocaleString()} scanned, ${q.returned.toLocaleString()} returned)`);
      print(`      Filter: ${JSON.stringify(q.filter)}`);
      if (q.suggestedIndex) {
        print(`      💡 Index: ${JSON.stringify(q.suggestedIndex)}`);
      }
    });
  });
  
  print("\n\n=== RECOMMENDED ACTIONS ===");
  print("1. Review the problematic queries above");
  print("2. Create the suggested indexes using: load('scripts/create_indexes.js')");
  print("3. Verify indexes exist: db.collection.getIndexes()");
  print("4. Test queries again: db.collection.find({...}).explain('executionStats')");
  print("5. Check MongoDB Atlas Performance Advisor for additional suggestions");
}

print("\n=== DONE ===");

