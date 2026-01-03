// Comprehensive Performance Analysis for All Collections
// Run this in MongoDB shell: load("scripts/analyze_all_collections_performance.js")
// This script analyzes ALL collections for performance issues

print("=== COMPREHENSIVE COLLECTION PERFORMANCE ANALYSIS ===\n");
print("Analyzing all collections for slow queries, missing indexes, and performance issues...\n\n");

// Get all collections
var collections = db.getCollectionNames().filter(function(name) {
  return !name.startsWith("system."); // Exclude system collections
});

print(`Found ${collections.length} collections to analyze:\n`);
collections.forEach(function(name) {
  print(`  - ${name}`);
});
print("");

var allIssues = [];

collections.forEach(function(collectionName) {
  print(`\n${"=".repeat(60)}`);
  print(`ANALYZING: ${collectionName}`);
  print(`${"=".repeat(60)}\n`);
  
  try {
    var coll = db.getCollection(collectionName);
    var stats = coll.stats();
    var docCount = stats.count || 0;
    var sizeMB = ((stats.size || 0) / 1024 / 1024).toFixed(2);
    
    print(`📊 Collection Stats:`);
    print(`   Documents: ${docCount.toLocaleString()}`);
    print(`   Size: ${sizeMB} MB`);
    print(`   Avg Doc Size: ${stats.avgObjSize ? (stats.avgObjSize / 1024).toFixed(2) + " KB" : "N/A"}\n`);
    
    // Check indexes
    print(`📑 Indexes:`);
    var indexes = coll.getIndexes();
    var indexCount = indexes.length;
    print(`   Total: ${indexCount} indexes`);
    
    var hasTextIndex = false;
    var hasEmailIndex = false;
    var hasIDIndex = false;
    
    indexes.forEach(function(idx) {
      var keyStr = JSON.stringify(idx.key);
      if (keyStr.includes('"text"') || keyStr.includes("text:")) {
        hasTextIndex = true;
        print(`   ✓ Text Index: ${idx.name}`);
      }
      if (keyStr.includes("email") || keyStr.includes("Email")) {
        hasEmailIndex = true;
        print(`   ✓ Email Index: ${idx.name}`);
      }
      if (keyStr.includes('"_id":1') || keyStr === '{"_id":1}') {
        hasIDIndex = true;
      }
    });
    
    if (!hasIDIndex) {
      print(`   ⚠️  WARNING: No _id index (should always exist)`);
      allIssues.push({
        collection: collectionName,
        severity: "HIGH",
        issue: "Missing _id index",
        fix: "This should always exist - check MongoDB configuration"
      });
    }
    print("");
    
    // Check profiler for slow queries on this collection
    print(`🔍 Slow Queries (from profiler):`);
    try {
      var profilerStatus = db.getProfilingStatus();
      if (profilerStatus.level === 0) {
        print(`   ⚠️  Profiler disabled - enable with: db.setProfilingLevel(1, { slowms: 100 })`);
      } else {
        var oneHourAgo = new Date(Date.now() - 60 * 60 * 1000);
        var slowQueries = db.system.profile.find({
          "ns": { $regex: new RegExp(`\\.${collectionName}$`) },
          "ts": { $gte: oneHourAgo },
          "millis": { $gte: 1000 } // >1s
        }).sort({ "millis": -1 }).limit(10).toArray();
        
        if (slowQueries.length === 0) {
          print(`   ✅ No slow queries found (last hour)`);
        } else {
          print(`   🔴 Found ${slowQueries.length} slow queries (>1s):\n`);
          
          var byPattern = {};
          slowQueries.forEach(function(q) {
            var filter = q.command ? JSON.stringify(q.command.filter || q.command.query || {}).substring(0, 80) : 'N/A';
            var key = filter;
            
            if (!byPattern[key]) {
              byPattern[key] = {
                count: 0,
                totalMillis: 0,
                maxMillis: 0,
                scanned: 0,
                returned: 0,
                stage: 'UNKNOWN'
              };
            }
            
            byPattern[key].count++;
            byPattern[key].totalMillis += q.millis || 0;
            byPattern[key].maxMillis = Math.max(byPattern[key].maxMillis, q.millis || 0);
            byPattern[key].scanned += q.docsExamined || 0;
            byPattern[key].returned += q.nReturned || 0;
            
            if (q.execStats && q.execStats.executionStages) {
              byPattern[key].stage = q.execStats.executionStages.stage;
            }
          });
          
          Object.keys(byPattern).forEach(function(pattern) {
            var stats = byPattern[pattern];
            var avgMillis = stats.totalMillis / stats.count;
            var ratio = stats.returned > 0 ? (stats.scanned / stats.returned) : stats.scanned;
            
            print(`   Pattern: ${pattern}`);
            print(`      Count: ${stats.count}, Avg: ${(avgMillis / 1000).toFixed(2)}s, Max: ${(stats.maxMillis / 1000).toFixed(2)}s`);
            print(`      Scanned: ${stats.scanned.toLocaleString()}, Returned: ${stats.returned.toLocaleString()}, Ratio: ${ratio.toFixed(0)}:1`);
            print(`      Stage: ${stats.stage}`);
            
            // Identify issues
            if (stats.stage === 'COLLSCAN') {
              print(`      🔴 COLLECTION SCAN - Missing index!`);
              allIssues.push({
                collection: collectionName,
                severity: "CRITICAL",
                issue: "Collection scan detected",
                pattern: pattern,
                avgTime: avgMillis,
                fix: "Add index on filter fields"
              });
            }
            
            if (ratio > 1000) {
              print(`      🔴 High scanned/returned ratio (>1000:1) - Inefficient query`);
              allIssues.push({
                collection: collectionName,
                severity: "HIGH",
                issue: "High scanned/returned ratio",
                pattern: pattern,
                ratio: ratio,
                fix: "Add/optimize index"
              });
            }
            
            if (pattern.includes("$regex") && !hasTextIndex) {
              print(`      🔴 Regex query without text index`);
              allIssues.push({
                collection: collectionName,
                severity: "HIGH",
                issue: "Regex query without text index",
                pattern: pattern,
                fix: "Add text index or use $text search"
              });
            }
            
            if (pattern === "{}" || pattern === "[]") {
              print(`      🔴 Empty filter - scanning entire collection!`);
              allIssues.push({
                collection: collectionName,
                severity: "CRITICAL",
                issue: "Empty filter query",
                pattern: pattern,
                fix: "Add filter or limit query"
              });
            }
            
            print("");
          });
        }
      }
    } catch (e) {
      print(`   ⚠️  Could not check profiler: ${e.message}`);
    }
    
    // Check for common missing indexes
    print(`💡 Common Index Suggestions:`);
    var suggestions = [];
    
    // Check for email field
    try {
      var sample = coll.findOne({});
      if (sample) {
        var sampleStr = JSON.stringify(sample);
        
        // Email field
        if ((sampleStr.includes("email") || sampleStr.includes("Email")) && !hasEmailIndex) {
          suggestions.push({
            field: "email",
            index: '{ "email": 1 } or { "user.email": 1 }',
            reason: "Email lookups are common and need index"
          });
        }
        
        // Name/username fields (for search)
        if ((sampleStr.includes("name") || sampleStr.includes("username")) && !hasTextIndex) {
          suggestions.push({
            field: "name/username",
            index: 'Text index on name/username fields',
            reason: "Search queries need text index for performance"
          });
        }
      }
    } catch (e) {
      // Skip if can't get sample
    }
    
    if (suggestions.length === 0) {
      print(`   ✅ No obvious missing indexes detected`);
    } else {
      suggestions.forEach(function(s) {
        print(`   ⚠️  Consider: ${s.index}`);
        print(`      Reason: ${s.reason}`);
        allIssues.push({
          collection: collectionName,
          severity: "MEDIUM",
          issue: `Missing ${s.field} index`,
          fix: s.index
        });
      });
    }
    
  } catch (e) {
    print(`❌ Error analyzing ${collectionName}: ${e.message}\n`);
  }
});

// Summary
print(`\n${"=".repeat(60)}`);
print(`SUMMARY`);
print(`${"=".repeat(60)}\n`);

var critical = allIssues.filter(function(i) { return i.severity === "CRITICAL"; });
var high = allIssues.filter(function(i) { return i.severity === "HIGH"; });
var medium = allIssues.filter(function(i) { return i.severity === "MEDIUM"; });

print(`🔴 CRITICAL Issues: ${critical.length}`);
critical.forEach(function(issue) {
  print(`   - ${issue.collection}: ${issue.issue}`);
  if (issue.pattern) print(`     Pattern: ${issue.pattern.substring(0, 60)}...`);
  print(`     Fix: ${issue.fix}`);
});

print(`\n🟠 HIGH Priority Issues: ${high.length}`);
high.forEach(function(issue) {
  print(`   - ${issue.collection}: ${issue.issue}`);
  if (issue.pattern) print(`     Pattern: ${issue.pattern.substring(0, 60)}...`);
  print(`     Fix: ${issue.fix}`);
});

print(`\n🟡 MEDIUM Priority Issues: ${medium.length}`);
medium.forEach(function(issue) {
  print(`   - ${issue.collection}: ${issue.issue}`);
  print(`     Fix: ${issue.fix}`);
});

print(`\n${"=".repeat(60)}`);
print(`NEXT STEPS:`);
print(`1. Review CRITICAL issues first`);
print(`2. Check MongoDB Atlas → Performance Advisor`);
print(`3. Add missing indexes`);
print(`4. Replace regex queries with \$text search where possible`);
print(`5. Fix empty filter queries`);
print(`${"=".repeat(60)}\n`);

print("=== DONE ===\n");

