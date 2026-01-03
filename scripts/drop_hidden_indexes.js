// MongoDB Drop Hidden Indexes Script
// Run this in MongoDB shell: load("scripts/drop_hidden_indexes.js")
// This script drops indexes that were hidden and tested (only if performance was acceptable)

print("=== DROPPING HIDDEN INDEXES ===\n");
print("⚠️  WARNING: This will permanently delete the indexes!\n");
print("Only run this after confirming performance is acceptable with hidden indexes.\n\n");

var indexesToDrop = [
  { collection: "communities", indexName: "community_tags_idx", size: "604 KB" },
  { collection: "communities", indexName: "community_tags_visibility_idx", size: "764 KB" }
];

print("Indexes to drop:\n");
indexesToDrop.forEach(function(item) {
  print(`   - ${item.collection}.${item.indexName} (${item.size})`);
});
print(`\nTotal space to free: ~1.37 MB\n`);

// Uncomment the code below to actually drop the indexes
// Or run the commands manually after verification

/*
var droppedIndexes = [];
var failedIndexes = [];

indexesToDrop.forEach(function(item) {
  try {
    var coll = db.getCollection(item.collection);
    
    // Check if index exists
    var indexes = coll.getIndexes({includeHidden: true});
    var indexExists = indexes.some(function(idx) {
      return idx.name === item.indexName;
    });
    
    if (!indexExists) {
      print(`⚠️  Index ${item.collection}.${item.indexName} does not exist (may already be dropped)\n`);
      return;
    }
    
    // Drop the index
    coll.dropIndex(item.indexName);
    print(`✅ Dropped: ${item.collection}.${item.indexName} (freed ${item.size})\n`);
    
    droppedIndexes.push(item);
  } catch (e) {
    print(`❌ Failed to drop ${item.collection}.${item.indexName}: ${e.message}\n`);
    failedIndexes.push({item: item, error: e.message});
  }
});

if (droppedIndexes.length > 0) {
  print(`\n✅ Successfully dropped ${droppedIndexes.length} indexes\n`);
  print(`Total space freed: ~1.37 MB\n`);
}

if (failedIndexes.length > 0) {
  print(`\n❌ Failed to drop ${failedIndexes.length} indexes\n`);
}
*/

print("=== MANUAL DROP COMMANDS ===\n");
print("To drop the indexes, run these commands:\n");
indexesToDrop.forEach(function(item) {
  print(`   db.${item.collection}.dropIndex("${item.indexName}");`);
});

print("\n=== VERIFICATION ===\n");
print("After dropping, verify indexes are gone:\n");
print("   db.communities.getIndexes();\n");

print("\n=== DONE ===\n");

