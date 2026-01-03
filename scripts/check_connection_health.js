// MongoDB Connection Health Check Script
// Run this in MongoDB shell: load("scripts/check_connection_health.js")
// Note: Some commands require admin privileges, but we'll check what we can

print("=== MONGODB CONNECTION HEALTH CHECK ===\n");

// 1. Check server status (basic info, no admin needed)
try {
  print("1. Server Status:");
  var status = db.serverStatus();
  print("   Current Connections: " + status.connections.current);
  print("   Available Connections: " + status.connections.available);
  print("   Active Connections: " + status.connections.active);
  print("   Total Created: " + status.connections.totalCreated);
  print("");
  
  if (status.connections.current > 200) {
    print("   ⚠️  WARNING: Connection count is high (>200)");
  }
  if (status.connections.available < 50) {
    print("   ⚠️  WARNING: Low available connections (<50)");
  }
} catch (e) {
  print("   ❌ Could not get server status: " + e.message);
  print("   (This requires admin privileges)");
}

// 2. Check database stats
try {
  print("2. Database Stats:");
  var stats = db.stats();
  print("   Collections: " + stats.collections);
  print("   Data Size: " + (stats.dataSize / 1024 / 1024).toFixed(2) + " MB");
  print("   Storage Size: " + (stats.storageSize / 1024 / 1024).toFixed(2) + " MB");
  print("   Index Size: " + (stats.indexSize / 1024 / 1024).toFixed(2) + " MB");
  print("");
} catch (e) {
  print("   ❌ Could not get database stats: " + e.message);
}

// 3. Check collection sizes (to identify large collections)
print("3. Collection Sizes (Top 10):");
try {
  var collections = db.getCollectionNames();
  var sizes = [];
  collections.forEach(function(name) {
    try {
      var coll = db.getCollection(name);
      var stats = coll.stats();
      sizes.push({
        name: name,
        size: stats.size || 0,
        count: stats.count || 0,
        avgObjSize: stats.avgObjSize || 0
      });
    } catch (e) {
      // Skip if can't get stats
    }
  });
  
  sizes.sort(function(a, b) { return b.size - a.size; });
  sizes.slice(0, 10).forEach(function(coll) {
    var sizeMB = (coll.size / 1024 / 1024).toFixed(2);
    print("   " + coll.name + ": " + coll.count + " docs, " + sizeMB + " MB");
  });
  print("");
} catch (e) {
  print("   ❌ Could not get collection sizes: " + e.message);
}

// 4. Check indexes (to see if queries are using indexes)
print("4. Index Usage Check:");
print("   (Run explain() on slow queries to check index usage)");
print("   Example: db.communities.find({...}).explain('executionStats')");
print("");

// 5. Recommendations
print("5. Recommendations:");
print("   ✅ Check MongoDB Atlas UI → Metrics → Connections");
print("   ✅ Check MongoDB Atlas UI → Performance Advisor");
print("   ✅ Check MongoDB Atlas UI → Real-Time Performance Panel");
print("   ✅ Monitor connection count over time");
print("   ✅ Set up alerts for connection count > 200");
print("");

print("=== DONE ===");
print("");
print("NOTE: For detailed connection info, check MongoDB Atlas UI:");
print("  - Metrics → Connections (shows current/peak connections)");
print("  - Performance Advisor (shows slow queries)");
print("  - Real-Time Performance (shows active operations)");

