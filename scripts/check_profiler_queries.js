// MongoDB Profiler Query Analysis Script
// Run this in MongoDB shell: load("scripts/check_profiler_queries.js")
// This script checks the profiler for slow queries with high scanned/returned ratios

print("=== MONGODB PROFILER QUERY ANALYSIS ===\n");

// Check if profiler is enabled
var profilerStatus = db.getProfilingStatus();
print(`Profiler Status: ${profilerStatus.was} (level ${profilerStatus.slowms}ms threshold)`);

if (profilerStatus.was === 0) {
  print("\n⚠️  Profiler is disabled. Enabling profiler at 1000ms threshold...");
  db.setProfilingLevel(1, { slowms: 1000 });
  print("✅ Profiler enabled. Wait a few minutes for queries to accumulate, then run this script again.");
  print("   Or check MongoDB Atlas → Performance Advisor for real-time slow query analysis.");
  print("\n=== DONE ===");
  quit();
}

// Get slow queries from profiler (last hour)
var oneHourAgo = new Date(Date.now() - 60 * 60 * 1000);
var slowQueries = db.system.profile.find({
  ts: { $gte: oneHourAgo },
  millis: { $gte: 1000 } // Queries taking > 1 second
}).sort({ millis: -1 }).limit(50).toArray();

if (slowQueries.length === 0) {
  print("\n✅ No slow queries found in profiler (last hour)");
  print("   Check MongoDB Atlas → Performance Advisor for real-time analysis");
  print("\n=== DONE ===");
  quit();
}

print(`\n📊 Found ${slowQueries.length} slow queries (last hour):\n`);

var problematicQueries = [];
var queryPatterns = {};

slowQueries.forEach(function(profile) {
  var collection = profile.ns ? profile.ns.split('.')[1] : 'unknown';
  var command = profile.command || {};
  var filter = command.filter || command.query || {};
  var millis = profile.millis || 0;
  var docsExamined = profile.docsExamined || 0;
  var keysExamined = profile.keysExamined || 0;
  var nReturned = profile.nReturned || 0;
  
  // Calculate ratio
  var ratio = nReturned > 0 ? (docsExamined / nReturned) : (docsExamined > 0 ? Infinity : 0);
  
  // Create query pattern key
  var patternKey = JSON.stringify({
    collection: collection,
    filter: getFilterPattern(filter)
  });
  
  if (!queryPatterns[patternKey]) {
    queryPatterns[patternKey] = {
      collection: collection,
      filter: filter,
      count: 0,
      totalMillis: 0,
      maxMillis: 0,
      totalScanned: 0,
      totalReturned: 0,
      maxRatio: 0
    };
  }
  
  var pattern = queryPatterns[patternKey];
  pattern.count++;
  pattern.totalMillis += millis;
  pattern.maxMillis = Math.max(pattern.maxMillis, millis);
  pattern.totalScanned += docsExamined;
  pattern.totalReturned += nReturned;
  pattern.maxRatio = Math.max(pattern.maxRatio, ratio);
  
  // Check if problematic
  if (ratio > 1000 || docsExamined > 10000) {
    problematicQueries.push({
      collection: collection,
      filter: filter,
      millis: millis,
      scanned: docsExamined,
      returned: nReturned,
      ratio: ratio,
      keysExamined: keysExamined
    });
  }
});

// Group by collection
var byCollection = {};
problematicQueries.forEach(function(q) {
  if (!byCollection[q.collection]) {
    byCollection[q.collection] = [];
  }
  byCollection[q.collection].push(q);
});

if (Object.keys(byCollection).length === 0) {
  print("✅ No queries with scanned/returned > 1000 found");
} else {
  print("🔴 PROBLEMATIC QUERIES (scanned/returned > 1000):\n");
  
  Object.keys(byCollection).sort().forEach(function(collection) {
    print(`\n📁 ${collection}:`);
    byCollection[collection].forEach(function(q) {
      var severity = q.ratio > 10000 ? '🔴 CRITICAL' : '🟠 HIGH';
      print(`   ${severity} Ratio: ${q.ratio.toFixed(2)}:1`);
      print(`      Scanned: ${q.scanned.toLocaleString()}, Returned: ${q.returned.toLocaleString()}`);
      print(`      Execution Time: ${q.millis}ms`);
      print(`      Filter: ${JSON.stringify(q.filter).substring(0, 200)}...`);
    });
  });
}

// Show most frequent slow query patterns
print("\n\n📊 MOST FREQUENT SLOW QUERY PATTERNS:\n");
var patterns = Object.values(queryPatterns);
patterns.sort(function(a, b) {
  return b.totalMillis - a.totalMillis; // Sort by total time
});

patterns.slice(0, 10).forEach(function(pattern) {
  var avgMillis = pattern.totalMillis / pattern.count;
  var avgRatio = pattern.totalReturned > 0 ? (pattern.totalScanned / pattern.totalReturned) : 0;
  
  print(`\n📁 ${pattern.collection}:`);
  print(`   Executions: ${pattern.count}`);
  print(`   Avg Time: ${avgMillis.toFixed(2)}ms, Max Time: ${pattern.maxMillis}ms`);
  print(`   Avg Ratio: ${avgRatio.toFixed(2)}:1 (${pattern.totalScanned.toLocaleString()} scanned, ${pattern.totalReturned.toLocaleString()} returned)`);
  print(`   Filter Pattern: ${JSON.stringify(pattern.filter).substring(0, 200)}...`);
});

print("\n\n=== RECOMMENDED ACTIONS ===");
print("1. Review problematic queries above");
print("2. Check MongoDB Atlas → Performance Advisor for index suggestions");
print("3. Create indexes for frequently queried fields");
print("4. Run: load('scripts/create_indexes.js') to create standard indexes");
print("5. Verify index usage: db.collection.find({...}).explain('executionStats')");

print("\n=== DONE ===");

// Helper function to extract filter pattern (simplify for grouping)
function getFilterPattern(filter) {
  var pattern = {};
  Object.keys(filter).forEach(function(key) {
    if (typeof filter[key] === 'object' && filter[key] !== null && !Array.isArray(filter[key])) {
      // For complex queries, just note the operator
      pattern[key] = Object.keys(filter[key])[0] || 'object';
    } else {
      pattern[key] = typeof filter[key];
    }
  });
  return pattern;
}

