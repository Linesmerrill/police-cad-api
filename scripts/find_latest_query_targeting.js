// MongoDB Latest Query Targeting Issue Finder
// Run this in MongoDB shell: load("scripts/find_latest_query_targeting.js")
// This script identifies the most recent queries causing "Query Targeting" alerts

print("=== FINDING LATEST QUERY TARGETING ISSUES ===\n");
print("This script will identify recent queries with scanned/returned ratio > 1000\n");

// Check if profiler is enabled
var profilerStatus = db.getProfilingStatus();
print(`Profiler Status: Level ${profilerStatus.level} (0=off, 1=slow, 2=all)\n`);

if (profilerStatus.level === 0) {
  print("⚠️  Profiler is disabled. Checking MongoDB Atlas Performance Advisor instead...");
  print("   Or enable profiler: db.setProfilingLevel(1, { slowms: 100 })\n");
}

var problematicQueries = [];

// 1. Check profiler for recent queries (last 2 hours)
if (profilerStatus.level > 0) {
  print("📊 Checking profiler for recent queries...\n");
  
  var twoHoursAgo = new Date(Date.now() - 2 * 60 * 60 * 1000);
  var profilerQueries = db.system.profile.find({
    ts: { $gte: twoHoursAgo },
    ns: { $not: /^system\./ }
  }).sort({ ts: -1 }).limit(200).toArray();
  
  print(`Found ${profilerQueries.length} recent profiler entries\n`);
  
  profilerQueries.forEach(function(entry) {
    var collection = entry.ns ? entry.ns.split('.')[1] : 'unknown';
    var command = entry.command || {};
    var millis = entry.millis || 0;
    
    // Check find operations
    if (command.find) {
      var filter = command.filter || {};
      var docsExamined = entry.docsExamined || 0;
      var keysExamined = entry.keysExamined || 0;
      var nReturned = entry.nReturned || 0;
      var ratio = nReturned > 0 ? (docsExamined / nReturned) : (docsExamined > 0 ? Infinity : 0);
      
      if (ratio > 1000 || docsExamined > 10000) {
        problematicQueries.push({
          type: "find",
          collection: collection,
          filter: filter,
          scanned: docsExamined,
          returned: nReturned,
          ratio: ratio,
          millis: millis,
          timestamp: entry.ts,
          stage: entry.execStats ? (entry.execStats.executionStages ? entry.execStats.executionStages.stage : 'UNKNOWN') : 'UNKNOWN'
        });
      }
    }
    
    // Check aggregation operations
    if (command.aggregate) {
      var pipeline = command.pipeline || [];
      var docsExamined = entry.docsExamined || 0;
      var keysExamined = entry.keysExamined || 0;
      var nReturned = entry.nReturned || 0;
      var ratio = nReturned > 0 ? (docsExamined / nReturned) : (docsExamined > 0 ? Infinity : 0);
      
      if (ratio > 1000 || docsExamined > 10000) {
        problematicQueries.push({
          type: "aggregate",
          collection: collection,
          pipeline: pipeline,
          scanned: docsExamined,
          returned: nReturned,
          ratio: ratio,
          millis: millis,
          timestamp: entry.ts
        });
      }
    }
    
    // Check update operations
    if (command.update) {
      var filter = command.updates && command.updates[0] ? command.updates[0].q : {};
      var docsExamined = entry.docsExamined || 0;
      var keysExamined = entry.keysExamined || 0;
      var nMatched = entry.nMatched || 0;
      var ratio = nMatched > 0 ? (docsExamined / nMatched) : (docsExamined > 0 ? Infinity : 0);
      
      if (ratio > 1000 || docsExamined > 10000) {
        problematicQueries.push({
          type: "update",
          collection: collection,
          filter: filter,
          scanned: docsExamined,
          matched: nMatched,
          ratio: ratio,
          millis: millis,
          timestamp: entry.ts
        });
      }
    }
  });
}

// 2. Test common problematic query patterns if no profiler data
if (problematicQueries.length === 0) {
  print("⚠️  No profiler data found. Testing common query patterns...\n");
  
  var testPatterns = [
    {
      collection: "civilians",
      name: "Civilians by active community",
      query: { "civilian.activeCommunityID": { $exists: true } },
      suggestedIndex: { "civilian.activeCommunityID": 1 }
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
      collection: "civilians",
      name: "Civilians pending approvals",
      query: { 
        "civilian.activeCommunityID": { $exists: true },
        "civilian.approvalStatus": { $in: ["pending", "requested_review"] }
      },
      suggestedIndex: { "civilian.activeCommunityID": 1, "civilian.approvalStatus": 1, "civilian.createdAt": -1 }
    },
    {
      collection: "calls",
      name: "Calls by community",
      query: { "call.communityID": { $exists: true } },
      suggestedIndex: { "call.communityID": 1 }
    },
    {
      collection: "announcements",
      name: "Announcements by community",
      query: { "announcement.community": { $exists: true } },
      suggestedIndex: { "announcement.community": 1 }
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
        problematicQueries.push({
          type: "find",
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
if (problematicQueries.length === 0) {
  print("✅ No queries with ratio > 1000 found!\n");
  print("If you're still seeing alerts, they may be from:");
  print("1. Queries that ran before indexes were created");
  print("2. Aggregation pipelines (check MongoDB Atlas Performance Advisor)");
  print("3. Update/Delete operations");
  print("4. Queries on collections not tested above");
} else {
  // Sort by timestamp (most recent first) or ratio (highest first)
  problematicQueries.sort(function(a, b) {
    if (a.timestamp && b.timestamp) {
      return b.timestamp - a.timestamp; // Most recent first
    }
    return b.ratio - a.ratio; // Highest ratio first
  });
  
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
      var severity = q.ratio > 10000 ? '🔴 CRITICAL' : '🟠 HIGH';
      var typeLabel = q.type ? `[${q.type.toUpperCase()}]` : '';
      print(`   ${severity} ${typeLabel} ${q.name || 'Query'}`);
      if (q.timestamp) {
        print(`      Time: ${q.timestamp}`);
      }
      if (q.filter) {
        print(`      Filter: ${JSON.stringify(q.filter).substring(0, 150)}${JSON.stringify(q.filter).length > 150 ? '...' : ''}`);
      }
      if (q.pipeline) {
        print(`      Pipeline: ${JSON.stringify(q.pipeline).substring(0, 150)}...`);
      }
      print(`      Stage: ${q.stage || 'N/A'} ${q.stage === 'COLLSCAN' ? '(COLLECTION SCAN!)' : ''}`);
      print(`      Scanned: ${q.scanned.toLocaleString()}, Returned: ${q.returned || q.matched || 0}, Ratio: ${q.ratio.toFixed(2)}:1`);
      print(`      Execution Time: ${q.millis}ms`);
      if (q.suggestedIndex) {
        print(`      💡 Suggested Index: ${JSON.stringify(q.suggestedIndex)}`);
      }
      print("");
    });
  });
  
  print("\n=== RECOMMENDED ACTIONS ===");
  print("1. Review the queries above (most recent/highest ratio first)");
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
  { collection: "civilians", index: { "civilian.activeCommunityID": 1 }, name: "civilian_active_community_idx" },
  { collection: "civilians", index: { "civilian.activeCommunityID": 1, "civilian.approvalStatus": 1, "civilian.createdAt": -1 }, name: "civilian_pending_approvals_idx" },
  { collection: "vehicles", index: { "vehicle.activeCommunityID": 1 }, name: "vehicle_active_community_idx" },
  { collection: "firearms", index: { "firearm.activeCommunityID": 1 }, name: "firearm_active_community_idx" },
  { collection: "communities", index: { "community.subscriptionCreatedBy": 1 }, name: "community_subscription_created_by_idx" },
  { collection: "users", index: { "user.communities.communityId": 1, "user.communities.status": 1 }, name: "user_communities_idx" },
  { collection: "calls", index: { "call.communityID": 1 }, name: "call_community_id_idx" },
  { collection: "announcements", index: { "announcement.community": 1 }, name: "announcement_community_idx" }
];

var missingIndexes = [];

indexChecks.forEach(function(check) {
  try {
    var coll = db.getCollection(check.collection);
    var indexes = coll.getIndexes();
    var indexKeyStr = JSON.stringify(check.index);
    var exists = indexes.some(function(idx) {
      return JSON.stringify(idx.key) === indexKeyStr;
    });
    
    if (!exists) {
      missingIndexes.push(check);
      print(`⚠️  Missing: ${check.collection}.${check.name}`);
      print(`   Index: ${JSON.stringify(check.index)}\n`);
    } else {
      print(`✅ Exists: ${check.collection}.${check.name}`);
    }
  } catch (e) {
    print(`❌ Error checking ${check.collection}: ${e.message}`);
  }
});

if (missingIndexes.length > 0) {
  print("\n=== ADD THESE TO create_indexes.js ===\n");
  missingIndexes.forEach(function(check) {
    print(`createIndexSafe(`);
    print(`  db.${check.collection},`);
    print(`  ${JSON.stringify(check.index)},`);
    print(`  { name: "${check.name}", background: true }`);
    print(`);`);
    print("");
  });
}

print("\n=== DONE ===");

