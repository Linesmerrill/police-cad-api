// MongoDB Unhide Indexes Script
// Run this in MongoDB shell: load("scripts/unhide_indexes.js")
// This script unhides indexes that were hidden for testing

print("=== UNHIDING INDEXES ===\n");

var indexesToUnhide = [
  { collection: "communities", indexName: "community_tags_idx" },
  { collection: "communities", indexName: "community_tags_visibility_idx" },
  { collection: "civilians", indexName: "civilian_active_community_idx" }
];

var unhiddenIndexes = [];
var failedIndexes = [];

indexesToUnhide.forEach(function(item) {
  try {
    var coll = db.getCollection(item.collection);
    
    // Check if index exists and is hidden
    var indexes = coll.getIndexes({includeHidden: true});
    var indexInfo = indexes.find(function(idx) {
      return idx.name === item.indexName;
    });
    
    if (!indexInfo) {
      print(`⚠️  Index ${item.collection}.${item.indexName} does not exist\n`);
      return;
    }
    
    if (!indexInfo.hidden) {
      print(`ℹ️  Index ${item.collection}.${item.indexName} is not hidden\n`);
      return;
    }
    
    // Unhide the index
    coll.unhideIndex(item.indexName);
    print(`✅ Unhidden: ${item.collection}.${item.indexName}\n`);
    
    unhiddenIndexes.push(item);
  } catch (e) {
    print(`❌ Failed to unhide ${item.collection}.${item.indexName}: ${e.message}\n`);
    failedIndexes.push({item: item, error: e.message});
  }
});

if (unhiddenIndexes.length > 0) {
  print(`\n✅ Successfully unhidden ${unhiddenIndexes.length} indexes\n`);
}

if (failedIndexes.length > 0) {
  print(`\n❌ Failed to unhide ${failedIndexes.length} indexes\n`);
}

print("=== DONE ===\n");

