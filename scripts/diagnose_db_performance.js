// MongoDB Database Performance Diagnostic Script
// Run this in MongoDB shell: load("scripts/diagnose_db_performance.js")
// This script helps diagnose why DB queries are taking 5+ seconds

print("=== MONGODB PERFORMANCE DIAGNOSTICS ===\n");
print("Diagnosing why DB queries are taking 5+ seconds...\n\n");

// 1. Check connection pool status
print("1. CONNECTION POOL STATUS:\n");
try {
  var status = db.serverStatus();
  print("   Current Connections: " + status.connections.current);
  print("   Available Connections: " + status.connections.available);
  print("   Active Connections: " + status.connections.active);
  print("   Total Created: " + status.connections.totalCreated);
  
  var utilization = ((status.connections.current / 1500) * 100).toFixed(1);
  print("   Connection Utilization: " + utilization + "% (M10 limit: 1,500 per node)");
  
  if (status.connections.current > 1000) {
    print("   ⚠️  WARNING: Connection count is very high (>1000)");
    print("   → This can cause connection pool exhaustion and slow queries");
  }
  if (status.connections.available < 100) {
    print("   ⚠️  WARNING: Low available connections (<100)");
    print("   → Requests may be waiting for available connections");
  }
  print("");
} catch (e) {
  print("   ❌ Could not get connection status: " + e.message + "\n");
}

// 2. Check replication lag
print("2. REPLICATION STATUS:\n");
try {
  var replStatus = db.printReplicationInfo();
  print("   (Check MongoDB Atlas UI → Metrics → Replication Lag)");
  print("   If replication lag > 1s, consider using PrimaryPreferred() read preference");
  print("   If replication lag > 5s, CRITICAL: Check secondary node health\n");
} catch (e) {
  print("   ℹ️  Run rs.printReplicationInfo() manually for detailed replication info\n");
}

// 3. Check for slow queries in profiler
print("3. SLOW QUERIES (from profiler):\n");
try {
  var profilerStatus = db.getProfilingStatus();
  if (profilerStatus.level === 0) {
    print("   ⚠️  Profiler is disabled");
    print("   Enable with: db.setProfilingLevel(1, { slowms: 100 })\n");
  } else {
    var twoHoursAgo = new Date(Date.now() - 2 * 60 * 60 * 1000);
    var slowQueries = db.system.profile.find({
      "ts": { $gte: twoHoursAgo },
      "millis": { $gte: 1000 } // Queries taking >1s
    }).sort({ "millis": -1 }).limit(10).toArray();
    
    if (slowQueries.length === 0) {
      print("   ✅ No queries >1s found in profiler (last 2 hours)\n");
    } else {
      print("   🔴 Found " + slowQueries.length + " slow queries (>1s):\n");
      slowQueries.forEach(function(q) {
        var collection = q.ns ? q.ns.split('.')[1] : 'unknown';
        var stage = q.execStats && q.execStats.executionStages ? q.execStats.executionStages.stage : 'UNKNOWN';
        print("      - " + collection + ": " + q.millis + "ms, Stage: " + stage);
        if (stage === 'COLLSCAN') {
          print("        ⚠️  COLLECTION SCAN - Missing index!");
        }
      });
      print("");
    }
  }
} catch (e) {
  print("   ❌ Could not check profiler: " + e.message + "\n");
}

// 4. Check for queries with high scanned/returned ratio
print("4. QUERY TARGETING ISSUES:\n");
try {
  var profilerStatus = db.getProfilingStatus();
  if (profilerStatus.level > 0) {
    var twoHoursAgo = new Date(Date.now() - 2 * 60 * 60 * 1000);
    var targetingIssues = db.system.profile.find({
      "ts": { $gte: twoHoursAgo },
      "$expr": {
        "$gt": [
          { "$divide": ["$docsExamined", { "$ifNull": ["$nReturned", 1] }] },
          1000
        ]
      }
    }).limit(5).toArray();
    
    if (targetingIssues.length === 0) {
      print("   ✅ No query targeting issues found\n");
    } else {
      print("   🔴 Found queries with scanned/returned ratio > 1000:\n");
      targetingIssues.forEach(function(q) {
        var collection = q.ns ? q.ns.split('.')[1] : 'unknown';
        var scanned = q.docsExamined || 0;
        var returned = q.nReturned || 0;
        var ratio = returned > 0 ? (scanned / returned) : scanned;
        print("      - " + collection + ": " + scanned + " scanned, " + returned + " returned (ratio: " + ratio.toFixed(0) + ":1)");
      });
      print("");
    }
  } else {
    print("   ℹ️  Profiler disabled - enable to check query targeting\n");
  }
} catch (e) {
  print("   ⚠️  Could not check query targeting: " + e.message + "\n");
}

// 5. Check index usage
print("5. INDEX USAGE CHECK:\n");
print("   Run explain() on slow queries to verify index usage:");
print("   db.collection.find({...}).explain('executionStats')");
print("   Look for:");
print("   - executionStats.executionStages.stage: 'IXSCAN' (good)");
print("   - executionStats.executionStages.stage: 'COLLSCAN' (bad - missing index)");
print("   - executionStats.totalDocsExamined vs nReturned (should be close)\n");

// 6. Check for connection wait times
print("6. CONNECTION WAIT TIMES:\n");
print("   Check MongoDB Atlas UI → Metrics → Connections");
print("   Look for:");
print("   - High connection count (>1000)");
print("   - Connection wait times");
print("   - Connection pool exhaustion alerts\n");

// 7. Recommendations
print("7. RECOMMENDATIONS:\n");
print("   ✅ Reduce query timeout from 10s to 5s (queries should be fast)");
print("   ✅ Reduce SocketTimeout to match query timeout (6s)");
print("   ✅ Increase MinPoolSize to 50 (more warm connections)");
print("   ✅ Increase MaxConnecting to 20 (faster connection establishment)");
print("   ✅ Reduce MaxConnIdleTime to 15s (release idle connections faster)");
print("   ✅ Use PrimaryPreferred() read preference (better load distribution)");
print("   ✅ Check MongoDB Atlas → Performance Advisor for index suggestions");
print("   ✅ Monitor connection pool metrics in MongoDB Atlas UI");
print("   ✅ Check for missing indexes on frequently queried fields");
print("   ✅ Verify replication lag is < 1s (if using PrimaryPreferred)\n");

print("=== DONE ===\n");
print("Next steps:");
print("1. Review MongoDB Atlas → Metrics → Connections");
print("2. Review MongoDB Atlas → Performance Advisor");
print("3. Check for missing indexes on slow queries");
print("4. Monitor connection pool utilization");

