// Analyze Slow Users Collection Queries
// Run this in MongoDB shell: load("scripts/analyze_slow_users_queries.js")
// This script analyzes the slow queries on the users collection (avg 572s!)

print("=== ANALYZING SLOW USERS QUERIES ===\n");
print("Found queries averaging 572 seconds - this is the root cause!\n\n");

try {
  var profilerStatus = db.getProfilingStatus();
  if (profilerStatus.level === 0) {
    print("❌ Profiler is disabled. Enable with:");
    print("   db.setProfilingLevel(1, { slowms: 100 })\n");
    print("Then wait 5 minutes and run this script again.\n");
  } else {
    var oneHourAgo = new Date(Date.now() - 60 * 60 * 1000);
    
    // Get all slow queries on users collection
    var slowQueries = db.system.profile.find({
      "ns": { $regex: /\.users$/ },
      "ts": { $gte: oneHourAgo },
      "millis": { $gte: 1000 } // >1s
    }).sort({ "millis": -1 }).limit(50).toArray();
    
    if (slowQueries.length === 0) {
      print("✅ No slow queries found in profiler (last hour)");
      print("   Check if profiler was recently enabled\n");
    } else {
      print(`🔴 Found ${slowQueries.length} slow queries on users collection:\n`);
      
      // Group by operation type and filter
      var byOperation = {};
      slowQueries.forEach(function(q) {
        var opType = q.op || 'unknown';
        var filter = q.command ? JSON.stringify(q.command.filter || q.command.query || {}).substring(0, 100) : 'N/A';
        var key = opType + " | " + filter;
        
        if (!byOperation[key]) {
          byOperation[key] = {
            count: 0,
            totalMillis: 0,
            maxMillis: 0,
            minMillis: Infinity,
            examples: []
          };
        }
        
        byOperation[key].count++;
        byOperation[key].totalMillis += q.millis || 0;
        byOperation[key].maxMillis = Math.max(byOperation[key].maxMillis, q.millis || 0);
        byOperation[key].minMillis = Math.min(byOperation[key].minMillis, q.millis || 0);
        
        if (byOperation[key].examples.length < 3) {
          byOperation[key].examples.push({
            millis: q.millis,
            filter: filter,
            stage: q.execStats && q.execStats.executionStages ? q.execStats.executionStages.stage : 'UNKNOWN',
            scanned: q.docsExamined || 0,
            returned: q.nReturned || 0
          });
        }
      });
      
      // Sort by average time
      var sorted = Object.keys(byOperation).map(function(key) {
        var stats = byOperation[key];
        return {
          key: key,
          count: stats.count,
          avgMillis: stats.totalMillis / stats.count,
          maxMillis: stats.maxMillis,
          minMillis: stats.minMillis,
          examples: stats.examples
        };
      }).sort(function(a, b) { return b.avgMillis - a.avgMillis; });
      
      sorted.forEach(function(item, idx) {
        print(`${idx + 1}. Query Pattern:`);
        print(`   Operation: ${item.key.split(' | ')[0]}`);
        print(`   Filter: ${item.key.split(' | ')[1]}`);
        print(`   Count: ${item.count} queries`);
        print(`   Avg Time: ${(item.avgMillis / 1000).toFixed(2)}s`);
        print(`   Max Time: ${(item.maxMillis / 1000).toFixed(2)}s`);
        print(`   Min Time: ${(item.minMillis / 1000).toFixed(2)}s`);
        
        if (item.examples.length > 0) {
          var example = item.examples[0];
          print(`   Stage: ${example.stage}`);
          if (example.stage === 'COLLSCAN') {
            print(`   ⚠️  COLLECTION SCAN - Missing index!`);
          }
          print(`   Scanned: ${example.scanned.toLocaleString()}, Returned: ${example.returned.toLocaleString()}`);
          if (example.scanned > 0 && example.returned > 0) {
            var ratio = example.scanned / example.returned;
            if (ratio > 1000) {
              print(`   🔴 CRITICAL: Scanned/Returned ratio: ${ratio.toFixed(0)}:1 (should be <10:1)`);
            }
          }
        }
        print("");
      });
      
      // Get most common slow query pattern
      if (sorted.length > 0) {
        var worst = sorted[0];
        print("=== WORST OFFENDER ===\n");
        print(`Query: ${worst.key}`);
        print(`Average Time: ${(worst.avgMillis / 1000).toFixed(2)}s`);
        print(`Total Time Wasted: ${(worst.totalMillis / 1000 / 60).toFixed(2)} minutes\n`);
        
        print("=== RECOMMENDED FIX ===\n");
        print("1. Run explain() on this query:");
        print(`   db.users.find(${worst.key.split(' | ')[1]}).explain('executionStats')`);
        print("2. Check executionStats.executionStages.stage:");
        print("   - If 'COLLSCAN': Add index on filter fields");
        print("   - If 'IXSCAN': Check if index is optimal");
        print("3. Check executionStats.totalDocsExamined vs nReturned:");
        print("   - Should be close (within 10x)");
        print("   - If not, add/optimize index\n");
      }
    }
  }
} catch (e) {
  print(`❌ Error: ${e.message}\n`);
  print("Stack: " + e.stack + "\n");
}

print("=== DONE ===\n");

