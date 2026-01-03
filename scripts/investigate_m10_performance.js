// MongoDB M10 Performance Investigation Script
// Run this in MongoDB shell: load("scripts/investigate_m10_performance.js")
// This script helps diagnose why queries are slow after M10 migration

print("=== M10 PERFORMANCE INVESTIGATION ===\n");
print("Investigating why queries are taking 5+ seconds after M10 migration...\n\n");

// 1. Check connection establishment times
print("1. CONNECTION ESTABLISHMENT:\n");
print("   Check MongoDB Atlas UI → Metrics → Connections");
print("   Look for:");
print("   - Connection establishment time (should be <100ms)");
print("   - Connection wait time (should be <50ms)");
print("   - Network latency between Heroku and MongoDB Atlas");
print("   → If connection establishment is slow, check network/region settings\n");

// 2. Check network latency
print("2. NETWORK LATENCY:\n");
print("   Run from Heroku dyno:");
print("   time mongo 'mongodb+srv://...' --eval 'db.runCommand({ping: 1})'");
print("   Expected: <50ms");
print("   If >200ms: Network latency issue between Heroku and MongoDB Atlas");
print("   → Check if Heroku region matches MongoDB Atlas region");
print("   → Consider MongoDB Atlas Network Peering or Private Endpoints\n");

// 3. Check MongoDB Atlas cluster status
print("3. MONGODB ATLAS CLUSTER STATUS:\n");
print("   Check MongoDB Atlas UI → Metrics:");
print("   - CPU Usage (should be <50% for M10)");
print("   - Memory Usage (should be <70% for M10)");
print("   - Disk I/O (should be low)");
print("   - Connection count (should be <1,000)");
print("   - Replication lag (should be <1s)");
print("   → If any metric is high, cluster may be overloaded\n");

// 4. Check for connection pool exhaustion
print("4. CONNECTION POOL STATUS:\n");
try {
  var status = db.serverStatus();
  print("   Current Connections: " + status.connections.current);
  print("   Available Connections: " + status.connections.available);
  print("   Active Connections: " + status.connections.active);
  print("   Total Created: " + status.connections.totalCreated);
  
  var utilization = ((status.connections.current / 1500) * 100).toFixed(1);
  print("   Connection Utilization: " + utilization + "% (M10 limit: 1,500 per node)");
  
  if (status.connections.current > 1200) {
    print("   🔴 CRITICAL: Connection count is very high (>1,200)");
    print("   → Connection pool may be exhausted");
    print("   → Queries may be waiting for available connections");
  } else if (status.connections.current > 800) {
    print("   ⚠️  WARNING: Connection count is high (>800)");
  }
  
  if (status.connections.available < 100) {
    print("   🔴 CRITICAL: Low available connections (<100)");
    print("   → Requests are likely waiting for connections");
  }
  print("");
} catch (e) {
  print("   ❌ Could not get connection status: " + e.message + "\n");
}

// 5. Check query execution times
print("5. QUERY EXECUTION TIMES:\n");
try {
  var profilerStatus = db.getProfilingStatus();
  if (profilerStatus.level === 0) {
    print("   ⚠️  Profiler is disabled");
    print("   Enable with: db.setProfilingLevel(1, { slowms: 100 })\n");
    print("   Then wait 5 minutes and check for slow queries\n");
  } else {
    var oneHourAgo = new Date(Date.now() - 60 * 60 * 1000);
    var slowQueries = db.system.profile.find({
      "ts": { $gte: oneHourAgo },
      "millis": { $gte: 1000 } // Queries taking >1s
    }).sort({ "millis": -1 }).limit(20).toArray();
    
    if (slowQueries.length === 0) {
      print("   ✅ No queries >1s found in profiler (last hour)\n");
    } else {
      print("   🔴 Found " + slowQueries.length + " slow queries (>1s):\n");
      
      // Group by collection
      var byCollection = {};
      slowQueries.forEach(function(q) {
        var collection = q.ns ? q.ns.split('.')[1] : 'unknown';
        if (!byCollection[collection]) {
          byCollection[collection] = [];
        }
        byCollection[collection].push(q);
      });
      
      Object.keys(byCollection).sort().forEach(function(collection) {
        var queries = byCollection[collection];
        var avgTime = queries.reduce(function(sum, q) { return sum + q.millis; }, 0) / queries.length;
        print("      " + collection + ": " + queries.length + " queries, avg " + avgTime.toFixed(0) + "ms");
        
        // Check for common issues
        queries.forEach(function(q) {
          var stage = q.execStats && q.execStats.executionStages ? q.execStats.executionStages.stage : 'UNKNOWN';
          if (stage === 'COLLSCAN') {
            print("        ⚠️  COLLECTION SCAN - Missing index!");
          }
          var scanned = q.docsExamined || 0;
          var returned = q.nReturned || 0;
          if (scanned > 0 && returned > 0 && (scanned / returned) > 1000) {
            print("        ⚠️  High scanned/returned ratio: " + (scanned / returned).toFixed(0) + ":1");
          }
        });
      });
      print("");
    }
  }
} catch (e) {
  print("   ❌ Could not check profiler: " + e.message + "\n");
}

// 6. Check for network issues
print("6. NETWORK ISSUES:\n");
print("   Check MongoDB Atlas UI → Metrics → Network:");
print("   - Network latency (should be <50ms from Heroku region)");
print("   - Network throughput (should be sufficient)");
print("   - Connection errors (should be 0)");
print("   → If network latency is high, consider:");
print("     - MongoDB Atlas Network Peering");
print("     - Private Endpoints");
print("     - Moving Heroku dynos to same region as MongoDB Atlas\n");

// 7. Check for index usage
print("7. INDEX USAGE:\n");
print("   Run explain() on a slow query:");
print("   db.collection.find({...}).explain('executionStats')");
print("   Look for:");
print("   - executionStats.executionStages.stage: 'IXSCAN' (good)");
print("   - executionStats.executionStages.stage: 'COLLSCAN' (bad - missing index)");
print("   - executionStats.totalDocsExamined vs nReturned (should be close)");
print("   - executionStats.executionTimeMillis (actual query time)\n");

// 8. Check replication lag
print("8. REPLICATION LAG:\n");
print("   Check MongoDB Atlas UI → Metrics → Replication Lag");
print("   - Should be <1s for healthy cluster");
print("   - If >5s: CRITICAL - secondary nodes are lagging");
print("   → This can cause slow reads if using PrimaryPreferred()");
print("   → Check secondary node health and network connectivity\n");

// 9. Specific M10 considerations
print("9. M10 SPECIFIC CHECKS:\n");
print("   M10 cluster specs:");
print("   - 2GB RAM per node");
print("   - 10GB storage");
print("   - 1,500 concurrent connections per node");
print("   Check if:");
print("   - RAM usage is high (>80%) → May need M20 or higher");
print("   - Storage is full → May need to increase storage");
print("   - Connection count is high (>1,000) → May need to optimize connection pool");
print("   - CPU is high (>70%) → May need M20 or higher\n");

// 10. Recommendations
print("10. IMMEDIATE ACTIONS:\n");
print("   1. Check MongoDB Atlas UI → Metrics → Real-Time Performance");
print("      - Look for slow operations");
print("      - Check connection wait times");
print("      - Check query execution times\n");
print("   2. Check MongoDB Atlas UI → Performance Advisor");
print("      - Look for missing index suggestions");
print("      - Check for slow query patterns\n");
print("   3. Check MongoDB Atlas UI → Metrics → Connections");
print("      - Current connection count");
print("      - Connection establishment times");
print("      - Connection wait times\n");
print("   4. Check Heroku logs for connection errors:");
print("      - 'connection pool timeout'");
print("      - 'server selection timeout'");
print("      - 'context deadline exceeded'\n");
print("   5. Test network latency from Heroku:");
print("      - Run: time mongo 'mongodb+srv://...' --eval 'db.runCommand({ping: 1})'");
print("      - Expected: <50ms");
print("      - If >200ms: Network latency issue\n");

print("=== DONE ===\n");
print("Next steps:");
print("1. Review MongoDB Atlas UI metrics (all tabs)");
print("2. Check Heroku logs for connection errors");
print("3. Test network latency from Heroku dyno");
print("4. Review slow queries in profiler");
print("5. Check for missing indexes");

