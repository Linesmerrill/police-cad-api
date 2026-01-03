// MongoDB Hide Redundant Indexes Script
// Run this in MongoDB shell: load("scripts/hide_redundant_indexes.js")
// This script hides redundant indexes to test performance impact before dropping them

print("=== HIDING REDUNDANT INDEXES FOR TESTING ===\n");
print("Hiding indexes allows you to test performance impact without dropping them.\n");
print("If performance degrades, you can unhide them instead of recreating.\n\n");

var indexesToHide = [
  {
    collection: "communities",
    indexName: "community_tags_idx",
    reason: "All queries use tags + visibility together (3-field index covers it)"
  },
  {
    collection: "communities",
    indexName: "community_tags_visibility_idx",
    reason: "3-field compound index (tags + visibility + name) covers this"
  }
];

// Optional: Test these indexes (keep for now, but can hide to test)
var optionalIndexesToTest = [
  {
    collection: "civilians",
    indexName: "civilian_active_community_idx",
    reason: "Compound index can be used, but single-field may be more efficient"
  }
];

print("=== HIDING SAFE-TO-REMOVE INDEXES ===\n");

var hiddenIndexes = [];
var failedIndexes = [];

indexesToHide.forEach(function(item) {
  try {
    var coll = db.getCollection(item.collection);
    
    // Check if index exists
    var indexes = coll.getIndexes();
    var indexExists = indexes.some(function(idx) {
      return idx.name === item.indexName;
    });
    
    if (!indexExists) {
      print(`⚠️  Index ${item.collection}.${item.indexName} does not exist (may already be dropped)\n`);
      return;
    }
    
    // Hide the index
    coll.hideIndex(item.indexName);
    print(`✅ Hidden: ${item.collection}.${item.indexName}`);
    print(`   Reason: ${item.reason}\n`);
    
    hiddenIndexes.push(item);
  } catch (e) {
    print(`❌ Failed to hide ${item.collection}.${item.indexName}: ${e.message}\n`);
    failedIndexes.push({item: item, error: e.message});
  }
});

print("\n=== OPTIONAL: INDEXES TO TEST (NOT HIDDEN) ===\n");
print("These indexes are recommended to keep, but you can hide them to test:\n");
optionalIndexesToTest.forEach(function(item) {
  print(`   ${item.collection}.${item.indexName}`);
  print(`   Reason: ${item.reason}\n`);
  print(`   To hide: db.${item.collection}.hideIndex("${item.indexName}")\n`);
});

print("\n=== NEXT STEPS ===\n");
print("1. Monitor your application performance for the next 24-48 hours\n");
print("2. Check MongoDB Atlas Performance Advisor for any new slow queries\n");
print("3. Monitor these endpoints specifically:\n");
print("   - GET /api/v2/communities/tag/{tag}\n");
print("   - GET /api/v1/communities/tag/{tag}\n");
print("   - Any other endpoints that query by community tags\n");

if (hiddenIndexes.length > 0) {
  print("\n=== IF PERFORMANCE IS GOOD ===\n");
  print("After confirming performance is acceptable, drop the hidden indexes:\n");
  hiddenIndexes.forEach(function(item) {
    print(`   db.${item.collection}.dropIndex("${item.indexName}");`);
  });
  
  print("\n=== IF PERFORMANCE DEGRADES ===\n");
  print("If you see performance issues, unhide the indexes:\n");
  hiddenIndexes.forEach(function(item) {
    print(`   db.${item.collection}.unhideIndex("${item.indexName}");`);
  });
}

print("\n=== VERIFICATION ===\n");
print("To check which indexes are hidden:\n");
print("   db.collection.getIndexes() // Hidden indexes will show hidden: true\n");
print("\nTo list all indexes (including hidden):\n");
print("   db.collection.getIndexes({includeHidden: true})\n");

if (hiddenIndexes.length > 0) {
  print("\n=== HIDDEN INDEXES SUMMARY ===\n");
  hiddenIndexes.forEach(function(item) {
    print(`✅ ${item.collection}.${item.indexName}`);
  });
  print(`\nTotal hidden: ${hiddenIndexes.length} indexes\n`);
}

if (failedIndexes.length > 0) {
  print("\n=== FAILED TO HIDE ===\n");
  failedIndexes.forEach(function(fail) {
    print(`❌ ${fail.item.collection}.${fail.item.indexName}: ${fail.error}`);
  });
}

print("\n=== DONE ===\n");
print("Remember: Hidden indexes still consume storage space but are not used by queries.\n");
print("Drop them after confirming performance is acceptable to free up space.\n");

