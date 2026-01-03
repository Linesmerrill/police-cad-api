// MongoDB Query Targeting Issue Finder
// Run this in MongoDB shell: load("scripts/find_query_targeting_issues.js")
// This script identifies queries causing "Query Targeting" alerts (>1000 scanned/returned)

print("=== FINDING QUERY TARGETING ISSUES ===\n");
print("This script will identify queries with scanned/returned ratio > 1000\n");

// Check if profiler is enabled
var profilerStatus = db.getProfilingStatus();
print(`Profiler Status: ${profilerStatus.level} (0=off, 1=slow, 2=all)\n`);

if (profilerStatus.level === 0) {
  print("⚠️  Profiler is disabled. Enabling for slow queries only...");
  print("   Run: db.setProfilingLevel(1, { slowms: 100 }) to enable");
  print("   Then wait a few minutes and run this script again.\n");
}

// Get slow queries from profiler (if enabled)
var slowQueries = [];
if (profilerStatus.level > 0) {
  print("📊 Analyzing profiler data...\n");
  
  // Get slow queries from profiler
  var profilerData = db.system.profile.find({
    "ns": { $not: /^system\./ }, // Exclude system collections
    "command.find": { $exists: true },
    "millis": { $gte: 100 } // Queries taking >100ms
  }).sort({ "ts": -1 }).limit(100).toArray();
  
  print(`Found ${profilerData.length} slow queries in profiler\n`);
  
  profilerData.forEach(function(entry) {
    var command = entry.command || {};
    var filter = command.filter || {};
    var collection = entry.ns.split('.')[1];
    
    // Get execution stats if available
    if (entry.execStats) {
      var scanned = entry.execStats.totalDocsExamined || 0;
      var returned = entry.execStats.nReturned || 0;
      var ratio = returned > 0 ? (scanned / returned) : (scanned > 0 ? Infinity : 0);
      
      if (ratio > 1000 || scanned > 10000) {
        slowQueries.push({
          collection: collection,
          filter: filter,
          scanned: scanned,
          returned: returned,
          ratio: ratio,
          millis: entry.millis || 0,
          stage: entry.execStats.executionStages ? entry.execStats.executionStages.stage : 'UNKNOWN'
        });
      }
    }
  });
}

// If no profiler data, test common problematic query patterns
if (slowQueries.length === 0) {
  print("⚠️  No profiler data found. Testing common query patterns...\n");
  
  // Test queries that commonly cause issues
  var testPatterns = [
    {
      collection: "civilians",
      name: "Pending approvals by community",
      query: { 
        "civilian.activeCommunityID": { $exists: true },
        "civilian.approvalStatus": { $in: ["pending", "requested_review"] }
      },
      suggestedIndex: { "civilian.activeCommunityID": 1, "civilian.approvalStatus": 1, "civilian.createdAt": -1 }
    },
    {
      collection: "communities",
      name: "Communities by subscription creator",
      query: { "community.subscriptionCreatedBy": { $exists: true } },
      suggestedIndex: { "community.subscriptionCreatedBy": 1 }
    },
    {
      collection: "users",
      name: "Users by community membership",
      query: { 
        "user.communities": { 
          $elemMatch: { 
            "communityId": { $exists: true },
            "status": "approved" 
          } 
        } 
      },
      suggestedIndex: { "user.communities.communityId": 1, "user.communities.status": 1 }
    },
    {
      collection: "vehicles",
      name: "Vehicles by active community",
      query: { "vehicle.activeCommunityID": { $exists: true } },
      suggestedIndex: { "vehicle.activeCommunityID": 1 }
    },
    {
      collection: "firearms",
      name: "Firearms by active community",
      query: { "firearm.activeCommunityID": { $exists: true } },
      suggestedIndex: { "firearm.activeCommunityID": 1 }
    },
    {
      collection: "civilians",
      name: "Civilians by active community",
      query: { "civilian.activeCommunityID": { $exists: true } },
      suggestedIndex: { "civilian.activeCommunityID": 1 }
    }
  ];
  
  testPatterns.forEach(function(test) {
    try {
      var coll = db.getCollection(test.collection);
      var explain = coll.find(test.query).limit(10).explain("executionStats");
      var execStats = explain.executionStats;
      
      if (!execStats) return;
      
      var scanned = execStats.totalDocsExamined || 0;
      var returned = execStats.nReturned || 0;
      var ratio = returned > 0 ? (scanned / returned) : (scanned > 0 ? Infinity : 0);
      var stage = execStats.executionStages ? execStats.executionStages.stage : 'UNKNOWN';
      
      if (ratio > 1000 || stage === 'COLLSCAN') {
        slowQueries.push({
          collection: test.collection,
          name: test.name,
          filter: test.query,
          scanned: scanned,
          returned: returned,
          ratio: ratio,
          millis: execStats.executionTimeMillis || 0,
          stage: stage,
          suggestedIndex: test.suggestedIndex
        });
      }
    } catch (e) {
      // Skip errors
    }
  });
}

// Report findings
if (slowQueries.length === 0) {
  print("✅ No queries with ratio > 1000 found!\n");
  print("If you're still seeing alerts, they may be from:");
  print("1. Queries that ran before indexes were created");
  print("2. Aggregation pipelines (check with db.collection.aggregate([...]).explain())");
  print("3. Update/Delete operations (check system.profile for 'update' or 'delete' commands)");
} else {
  print(`\n🔴 Found ${slowQueries.length} queries with ratio > 1000:\n`);
  
  // Group by collection
  var byCollection = {};
  slowQueries.forEach(function(q) {
    if (!byCollection[q.collection]) {
      byCollection[q.collection] = [];
    }
    byCollection[q.collection].push(q);
  });
  
  Object.keys(byCollection).sort().forEach(function(collection) {
    print(`\n📁 ${collection}:`);
    byCollection[collection].forEach(function(q) {
      var severity = q.ratio > 10000 ? '🔴 CRITICAL' : '🟠 HIGH';
      print(`   ${severity} ${q.name || 'Query'}`);
      print(`      Filter: ${JSON.stringify(q.filter).substring(0, 100)}${JSON.stringify(q.filter).length > 100 ? '...' : ''}`);
      print(`      Stage: ${q.stage} ${q.stage === 'COLLSCAN' ? '(COLLECTION SCAN!)' : ''}`);
      print(`      Scanned: ${q.scanned.toLocaleString()}, Returned: ${q.returned.toLocaleString()}, Ratio: ${q.ratio.toFixed(2)}:1`);
      print(`      Execution Time: ${q.millis}ms`);
      if (q.suggestedIndex) {
        print(`      💡 Suggested Index: ${JSON.stringify(q.suggestedIndex)}`);
      }
      print("");
    });
  });
  
  print("\n=== RECOMMENDED ACTIONS ===");
  print("1. Review the queries above");
  print("2. Create the suggested indexes:");
  print("   load('scripts/create_indexes.js')");
  print("3. For missing indexes, add them to scripts/create_indexes.js");
  print("4. Clear plan cache after creating indexes:");
  print("   db.collection.getPlanCache().clear()");
  print("5. Verify fixes:");
  print("   db.collection.find({...}).explain('executionStats')");
}

// Check for common missing indexes
print("\n=== CHECKING FOR COMMON MISSING INDEXES ===\n");

var indexChecks = [
  { collection: "civilians", index: { "civilian.activeCommunityID": 1, "civilian.approvalStatus": 1, "civilian.createdAt": -1 }, name: "civilian_pending_approvals_idx" },
  { collection: "communities", index: { "community.subscriptionCreatedBy": 1 }, name: "community_subscription_created_by_idx" },
  { collection: "vehicles", index: { "vehicle.activeCommunityID": 1 }, name: "vehicle_active_community_idx" },
  { collection: "firearms", index: { "firearm.activeCommunityID": 1 }, name: "firearm_active_community_idx" },
  { collection: "civilians", index: { "civilian.activeCommunityID": 1 }, name: "civilian_active_community_idx" }
];

indexChecks.forEach(function(check) {
  try {
    var coll = db.getCollection(check.collection);
    var indexes = coll.getIndexes();
    var indexKeyStr = JSON.stringify(check.index);
    var exists = indexes.some(function(idx) {
      return JSON.stringify(idx.key) === indexKeyStr;
    });
    
    if (!exists) {
      print(`⚠️  Missing index: ${check.collection}.${check.name}`);
      print(`   Index: ${JSON.stringify(check.index)}`);
      print(`   Add to create_indexes.js:\n`);
      print(`   createIndexSafe(`);
      print(`     db.${check.collection},`);
      print(`     ${JSON.stringify(check.index)},`);
      print(`     { name: "${check.name}", background: true }`);
      print(`   );`);
      print("");
    } else {
      print(`✅ Index exists: ${check.collection}.${check.name}`);
    }
  } catch (e) {
    print(`❌ Error checking ${check.collection}: ${e.message}`);
  }
});

print("\n=== DONE ===");

