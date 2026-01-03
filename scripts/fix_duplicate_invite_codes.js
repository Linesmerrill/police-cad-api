// Script to find and fix duplicate invite codes
// Run this BEFORE creating the unique index if you get duplicate key errors

print("=== FINDING DUPLICATE INVITE CODES ===\n");

// Find all duplicate codes
const duplicates = db.inviteCodes.aggregate([
  {
    $group: {
      _id: "$code",
      count: { $sum: 1 },
      docs: { $push: "$$ROOT" }
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
  print(`⚠️  Found ${duplicates.length} duplicate code(s):\n`);
  
  duplicates.forEach((dup, idx) => {
    print(`${idx + 1}. Code: "${dup._id}" appears ${dup.count} times`);
    print(`   Document IDs:`);
    dup.docs.forEach((doc, docIdx) => {
      print(`     - ${doc._id} (Community: ${doc.communityId || 'N/A'}, Created: ${doc.createdAt || 'N/A'})`);
    });
    print("");
  });
  
  print("=== RECOMMENDED FIX ===\n");
  print("Option 1: Keep the most recent one, delete others");
  print("Option 2: Keep the one with the most remaining uses");
  print("Option 3: Keep all but mark duplicates as inactive (if you add an 'active' field)");
  print("\nTo manually fix, run:");
  print(`db.inviteCodes.deleteMany({ code: "${duplicates[0]._id}", _id: { $ne: ObjectId("KEEP_THIS_ID") } })`);
  print("\nOr create a non-unique index instead:");
  print(`db.inviteCodes.createIndex({ code: 1 }, { name: "invite_code_idx", background: true })`);
}

print("\n=== DONE ===");

