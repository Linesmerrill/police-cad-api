// Recount community.membersCount for communities where it went negative.
//
// Background: leaving a community decremented membersCount whether or not
// the leaver was an approved (counted) member. Combined with the V1 sign-in
// heal re-adding players to their old activeCommunity on every login, one
// player leaving the same community eight times drove its count to -7.
// Both are fixed in the API; this repairs the counts already written.
//
// membersCount counts approved members, which is what this recounts: users
// with a user.communities entry for the community whose status is "approved".
//
// Usage:
//   DRY_RUN=true  mongosh "$DB_URI" scripts/fix_negative_members_count.js   (default)
//   DRY_RUN=false mongosh "$DB_URI" scripts/fix_negative_members_count.js
//
// Idempotent. Each write is guarded on the value it read, so a count that
// changes underneath it is left alone and reported.

const DRY_RUN = (typeof process !== "undefined" && process.env && process.env.DRY_RUN)
  ? process.env.DRY_RUN !== "false"
  : true;
const d = db.getSiblingDB((typeof process !== "undefined" && process.env && process.env.DB_NAME) || db.getName());

print("=== FIX NEGATIVE MEMBERS COUNT ===");
print(`DRY_RUN: ${DRY_RUN}`);

let fixed = 0, skipped = 0;
d.communities.find({ "community.membersCount": { $lt: 0 } }, { "community.name": 1, "community.membersCount": 1 })
  .forEach((c) => {
    const id = c._id.toString();
    const was = c.community.membersCount;
    const actual = d.users.countDocuments({
      "user.communities": { $elemMatch: { communityId: id, status: "approved" } },
    });
    print(`${id}  ${JSON.stringify(c.community.name)}  ${was} -> ${actual}`);
    if (DRY_RUN) return;
    const r = d.communities.updateOne(
      { _id: c._id, "community.membersCount": was },
      { $set: { "community.membersCount": actual } },
    );
    if (r.modifiedCount === 1) fixed++; else { skipped++; print("  changed since read; left alone"); }
  });

print(DRY_RUN ? "Dry run: nothing written." : `Fixed ${fixed}, left ${skipped}.`);
