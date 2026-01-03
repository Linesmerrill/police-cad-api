// MongoDB Query Diagnosis Script
// Run this to identify queries causing "Query Targeting" alerts

print("=== DIAGNOSING SLOW QUERIES ===\n");
print("This script will check common query patterns and their execution plans\n");

// Common queries to check
const testQueries = [
  {
    collection: "communities",
    name: "Public communities",
    query: { "community.visibility": "public" }
  },
  {
    collection: "communities",
    name: "Communities by tag",
    query: { "community.tags": "Xbox", "community.visibility": "public" }
  },
  {
    collection: "communities",
    name: "Communities by subscription creator",
    query: { "community.subscriptionCreatedBy": "test-user-id" }
  },
  {
    collection: "users",
    name: "Users by email",
    query: { "user.email": "test@example.com" }
  },
  {
    collection: "users",
    name: "Users by community",
    query: { 
      "user.communities": { 
        $elemMatch: { 
          "communityId": "test-community-id", 
          "status": "approved" 
        } 
      } 
    }
  },
  {
    collection: "vehicles",
    name: "Vehicles by user",
    query: { "vehicle.userID": "test-user-id" }
  },
  {
    collection: "vehicles",
    name: "Vehicles by registered owner",
    query: { "vehicle.registeredOwnerID": "test-owner-id" }
  },
  {
    collection: "calls",
    name: "Calls by community",
    query: { "call.communityID": "test-community-id" }
  },
  {
    collection: "licenses",
    name: "Licenses by civilian",
    query: { "license.civilianID": "test-civilian-id" }
  },
  {
    collection: "firearms",
    name: "Firearms by registered owner",
    query: { "firearm.registeredOwnerID": "test-owner-id" }
  },
  {
    collection: "civilians",
    name: "Civilians by user",
    query: { "civilian.userID": "test-user-id" }
  }
];

let issuesFound = 0;

testQueries.forEach(({ collection, name, query }) => {
  try {
    const explain = db[collection].find(query).limit(10).explain("executionStats");
    const executionStats = explain.executionStats;
    const winningPlan = explain.queryPlanner.winningPlan;
    
    // Check execution stage
    const stage = winningPlan.stage || (winningPlan.inputStage && winningPlan.inputStage.stage);
    const isCollectionScan = stage === "COLLSCAN" || 
                            (winningPlan.inputStage && winningPlan.inputStage.stage === "COLLSCAN");
    
    // Calculate ratio
    const docsExamined = executionStats.totalDocsExamined || 0;
    const docsReturned = executionStats.nReturned || 0;
    const ratio = docsReturned > 0 ? (docsExamined / docsReturned) : docsExamined;
    const executionTime = executionStats.executionTimeMillis || 0;
    
    // Check if there's an issue
    const hasIssue = isCollectionScan || ratio > 1000 || executionTime > 100;
    
    if (hasIssue) {
      issuesFound++;
      print(`❌ ${collection}.${name}:`);
      print(`   Stage: ${stage} ${isCollectionScan ? "(COLLECTION SCAN!)" : ""}`);
      print(`   Docs Examined: ${docsExamined}`);
      print(`   Docs Returned: ${docsReturned}`);
      print(`   Ratio: ${ratio.toFixed(2)}:1 ${ratio > 1000 ? "(> 1000!)" : ""}`);
      print(`   Execution Time: ${executionTime}ms ${executionTime > 100 ? "(SLOW!)" : ""}`);
      
      if (winningPlan.inputStage && winningPlan.inputStage.indexName) {
        print(`   Index Used: ${winningPlan.inputStage.indexName}`);
      } else if (isCollectionScan) {
        print(`   ⚠️  NO INDEX USED - Full collection scan!`);
      }
      print("");
    } else {
      print(`✅ ${collection}.${name}:`);
      print(`   Stage: ${stage}`);
      print(`   Ratio: ${ratio.toFixed(2)}:1`);
      print(`   Execution Time: ${executionTime}ms`);
      if (winningPlan.inputStage && winningPlan.inputStage.indexName) {
        print(`   Index: ${winningPlan.inputStage.indexName}`);
      }
      print("");
    }
  } catch (e) {
    print(`⚠️  Error checking ${collection}.${name}: ${e.message}`);
    print("");
  }
});

if (issuesFound === 0) {
  print("✅ No issues found! All queries are using indexes efficiently.");
} else {
  print(`\n⚠️  Found ${issuesFound} queries with issues`);
  print("Check the queries above and:");
  print("1. Create missing indexes (see scripts/create_indexes.js)");
  print("2. Clear plan cache: db.collection.getPlanCache().clear()");
  print("3. Optimize query patterns (avoid regex without prefix, etc.)");
}

print("\n=== DONE ===");

