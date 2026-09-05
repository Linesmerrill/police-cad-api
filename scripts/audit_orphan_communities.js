// Reports communities that have no usable owner.
//
//   mongosh "YOUR_CONNECTION_STRING" < audit_orphan_communities.js
//
// ---------------------------------------------------------------------------
// WHY THESE EXIST
//
// CreateCommunityHandler used to validate community.ownerID only AFTER
// InsertOne. A create with no usable ownerID -- a signed-out web visitor
// (/communities has no auth gate, so the page rendered an empty user), or a
// mobile client whose SecureStore "userId" had been cleared -- inserted the
// community, then failed ObjectIDFromHex and returned 400 to the caller.
//
// The user saw "failed to create community" and tried again, and each retry
// left another document behind. Nobody owns these: they are not linked from
// any user.communities array, so no one can see, administer or delete them
// through the product.
//
// The handler now resolves the owner before it writes anything, so the set
// below is closed -- it cannot grow. This script is for clearing what the old
// behaviour already produced.
//
// THIS SCRIPT ONLY READS. It prints what it finds and stops. Deleting is a
// separate, deliberate step -- the command is at the bottom.
// ---------------------------------------------------------------------------

const HEX24 = /^[0-9a-fA-F]{24}$/;

// An owner reference that ObjectIDFromHex would have rejected. Everything the
// old handler let through lands in one of these buckets.
const unusableOwner = {
  $or: [
    { "community.ownerID": { $exists: false } },
    { "community.ownerID": null },
    { "community.ownerID": "" },
  ],
};

const missing = db.communities.countDocuments(unusableOwner);
print("communities with a missing or empty ownerID: " + missing);

// A well-formed ownerID can still point at a user who no longer exists.
let malformed = 0;
let danglingOwner = 0;
const danglingSamples = [];

db.communities
  .find({ "community.ownerID": { $exists: true, $nin: [null, ""] } },
        { "community.ownerID": 1, "community.name": 1, "community.createdAt": 1 })
  .forEach((c) => {
    const owner = String(c.community.ownerID);
    if (!HEX24.test(owner)) {
      malformed++;
      return;
    }
    if (!db.users.findOne({ _id: ObjectId(owner) }, { _id: 1 })) {
      danglingOwner++;
      if (danglingSamples.length < 10) {
        danglingSamples.push({
          _id: c._id,
          name: c.community.name,
          ownerID: owner,
          createdAt: c.community.createdAt,
        });
      }
    }
  });

print("communities with a malformed (non-ObjectID) ownerID: " + malformed);
print("communities whose owner no longer exists:           " + danglingOwner);

if (missing > 0) {
  print("");
  print("--- sample of missing/empty ownerID (up to 10) ---");
  db.communities
    .find(unusableOwner, { "community.name": 1, "community.createdAt": 1, "community.membersCount": 1 })
    .limit(10)
    .forEach((c) => printjson(c));
}

if (danglingSamples.length > 0) {
  print("");
  print("--- sample of deleted owners (up to 10) ---");
  danglingSamples.forEach((c) => printjson(c));
}

print("");
print("Read-only. Nothing was changed.");
print("");
print("Before deleting anything, confirm none of these are real communities");
print("someone is using -- check membersCount and createdAt in the samples above.");
print("A community with a missing/empty ownerID was never reachable by any user,");
print("so those are the safe ones. A dangling owner may just mean the account was");
print("deleted while the community stayed in use; do NOT bulk-delete that bucket.");
print("");
print("To remove only the unreachable ones, run by hand:");
print("");
print('  db.communities.deleteMany({ $or: [');
print('    { "community.ownerID": { $exists: false } },');
print('    { "community.ownerID": null },');
print('    { "community.ownerID": "" },');
print("  ] })");
