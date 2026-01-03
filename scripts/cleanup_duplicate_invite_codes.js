// Script to automatically clean up duplicate invite codes
// Keeps the most recent one (by createdAt) and deletes older duplicates
// Run this BEFORE creating the unique index

print("=== CLEANING UP DUPLICATE INVITE CODES ===\n");

// Find all duplicate codes
const duplicates = db.inviteCodes.aggregate([
  {
    $group: {
      _id: "$code",
      count: { $sum: 1 },
      docs: { $push: { id: "$_id", createdAt: "$createdAt", remainingUses: "$remainingUses" } }
    }
  },
  {
    $match: {
      count: { $gt: 1 }
    }
  }
]).toArray();

if (duplicates.length === 0) {
  print("✅ No duplicate codes found!");
  print("You can safely create the unique index.");
} else {
  print(`Found ${duplicates.length} duplicate code(s). Cleaning up...\n`);
  
  let totalDeleted = 0;
  
  duplicates.forEach((dup, idx) => {
    // Sort by createdAt (most recent first), then by remainingUses (most uses first)
    const sortedDocs = dup.docs.sort((a, b) => {
      const dateA = a.createdAt ? new Date(a.createdAt) : new Date(0);
      const dateB = b.createdAt ? new Date(b.createdAt) : new Date(0);
      
      // Most recent first
      if (dateB.getTime() !== dateA.getTime()) {
        return dateB.getTime() - dateA.getTime();
      }
      
      // If same date, prefer one with more remaining uses
      const usesA = a.remainingUses || 0;
      const usesB = b.remainingUses || 0;
      return usesB - usesA;
    });
    
    // Keep the first one (most recent or most uses)
    const keepId = sortedDocs[0].id;
    const deleteIds = sortedDocs.slice(1).map(d => d.id);
    
    print(`${idx + 1}. Code: "${dup._id}"`);
    print(`   Keeping: ${keepId} (Created: ${sortedDocs[0].createdAt || 'N/A'}, Uses: ${sortedDocs[0].remainingUses || 'N/A'})`);
    print(`   Deleting: ${deleteIds.length} duplicate(s)`);
    
    // Delete duplicates
    const result = db.inviteCodes.deleteMany({
      code: dup._id,
      _id: { $in: deleteIds }
    });
    
    totalDeleted += result.deletedCount;
    print(`   ✓ Deleted ${result.deletedCount} duplicate(s)\n`);
  });
  
  print(`\n=== SUMMARY ===`);
  print(`Total duplicates found: ${duplicates.length}`);
  print(`Total documents deleted: ${totalDeleted}`);
  print(`\n✅ Cleanup complete!`);
  print(`You can now create the unique index:`);
  print(`db.inviteCodes.createIndex({ code: 1 }, { name: "invite_code_idx", unique: true, background: true })`);
}

print("\n=== DONE ===");

