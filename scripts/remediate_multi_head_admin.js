// remediate_multi_head_admin.js
//
// One-off remediation: enforce the Head Admin invariant retroactively.
//
// The Head Admin role is meant to hold exactly one member: the community owner.
// Before the "Head Admin is locked" change shipped, owners could freely add
// members to it, so 141 legacy communities ended up with multiple people in
// that role. The UI/mobile now lock the role, which froze that membership and
// left those owners unable to prune it. This script resets every such role to
// owner-only, matching what a freshly-created community looks like.
//
// Safety:
//   - Before any write, the current member list of each affected role is
//     snapshotted into the `head_admin_reset_backup` collection so the change
//     is fully reversible.
//   - Head Admin roles are identified by permission signature (enabled
//     "administrator" perm whose description is "Head Admin"), NOT by name,
//     mirroring models.IsHeadAdminRole in the API.
//   - Roles are matched for update by their stable _id, not array index.
//
// Usage (from a shell with the prod connection string):
//   DRY=true  mongosh "$DB_URI" scripts/remediate_multi_head_admin.js   # report only
//   DRY=false mongosh "$DB_URI" scripts/remediate_multi_head_admin.js   # execute

const DRY = (process.env.DRY !== "false"); // default to dry-run; must opt in to write
const BACKUP = "head_admin_reset_backup";

function isHeadAdminRole(role) {
  const perms = role.permissions || [];
  return perms.some(p => p.name === "administrator" && p.description === "Head Admin" && p.enabled === true);
}

const cursor = db.communities.find(
  { "community.roles": { $exists: true } },
  { "community.name": 1, "community.ownerID": 1, "community.roles": 1 }
);

const worklist = [];
cursor.forEach(c => {
  const roles = (c.community && c.community.roles) || [];
  const owner = c.community && c.community.ownerID;
  const ha = roles.find(isHeadAdminRole);
  if (!ha) return;
  const members = ha.members || [];
  if (members.length <= 1) return;
  if (!owner) return; // no owner to reset to — skip, should not happen
  worklist.push({
    communityId: c._id,
    communityName: (c.community && c.community.name) || "(no name)",
    roleId: ha._id,
    roleName: ha.name,
    ownerID: owner,
    previousMembers: members,
    ownerWasInRole: members.includes(owner),
  });
});

print(`Mode: ${DRY ? "DRY RUN (no writes)" : "EXECUTE"}`);
print(`Communities to remediate: ${worklist.length}`);
let removed = 0;
worklist.forEach(w => { removed += w.previousMembers.filter(m => m !== w.ownerID).length; });
print(`Member-slots to remove: ${removed}`);
print(`Communities where owner was NOT in the role (owner added, net): ${worklist.filter(w => !w.ownerWasInRole).length}`);

if (DRY) {
  print("\n-- sample of first 5 --");
  worklist.slice(0, 5).forEach(w => print(JSON.stringify({
    community: String(w.communityId), name: w.communityName, role: w.roleName,
    before: w.previousMembers.length, ownerWasInRole: w.ownerWasInRole,
  })));
  print("\nDRY RUN complete. Re-run with DRY=false to apply.");
  quit(0);
}

// EXECUTE: backup first, then reset each role to owner-only.
const stamp = new Date();
let backedUp = 0, updated = 0;
worklist.forEach(w => {
  db.getCollection(BACKUP).insertOne({
    communityId: w.communityId,
    communityName: w.communityName,
    roleId: w.roleId,
    roleName: w.roleName,
    ownerID: w.ownerID,
    previousMembers: w.previousMembers,
    ownerWasInRole: w.ownerWasInRole,
    resetAt: stamp,
    reason: "remediate_multi_head_admin: enforce owner-only Head Admin role",
  });
  backedUp++;

  const res = db.communities.updateOne(
    { _id: w.communityId, "community.roles._id": w.roleId },
    { $set: { "community.roles.$.members": [w.ownerID], "community.updatedAt": stamp } }
  );
  updated += res.modifiedCount;
});

print(`\nBacked up: ${backedUp} into "${BACKUP}"`);
print(`Communities updated: ${updated}`);
print("Done.");
