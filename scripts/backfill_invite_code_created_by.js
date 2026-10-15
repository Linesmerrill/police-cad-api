// Backfill createdBy onto invite codes that were saved without it.
//
// AddInviteCodeHandler used to take createdBy from the request body, and the
// website's Share flow never sent it, so 1,591 of the 11,412 invite codes hold
// createdBy "". Anything that names a code's creator (the bot's /invite list,
// the community page) can only say "Unknown" for them. The handler now takes
// the authorized actor instead, so codes minted from 2026-10-13 on are
// attributed; this fills in the history.
//
// The same handler has always written an audit_logs entry for the creation,
// with the actor on it, so the creator is recoverable for every code made since
// audit logging landed (2026-03). It matches on the audit entry's targetId,
// which is the invite code's own _id, and only trusts a 24-hex actorId. Codes
// from before the audit log (2026-01 to 2026-03) have no record of who made
// them and are left as they are.
//
// Dry run by default: it prints what it would write, and a sample. Pass
// APPLY=true to write. Idempotent: only codes with a blank createdBy are
// touched, and each update is guarded on createdBy still being blank. Every
// changed _id is printed so a write can be reviewed or reversed.
//
// DB_URI ends in connection options, so select the database instead of
// appending it to the URI:
//
//   mongosh "$DB_URI" --eval "db = db.getSiblingDB('$DB_NAME'); APPLY=false" \
//     --file scripts/backfill_invite_code_created_by.js

const apply = typeof APPLY !== "undefined" && APPLY === true;

const blankCreatedBy = {
  $or: [{ createdBy: { $exists: false } }, { createdBy: "" }, { createdBy: null }],
};
const isUserID = /^[0-9a-fA-F]{24}$/;

const codes = db.inviteCodes
  .find(blankCreatedBy, { code: 1, communityId: 1, createdAt: 1 })
  .toArray();

print(`invite codes with no createdBy: ${codes.length}`);
if (codes.length === 0) {
  print("Nothing to do.");
  quit(0);
}

// One pass over the creation audit entries for these codes, keyed by the code's
// _id. A code created twice in the log (it cannot be, but the data is old)
// keeps the earliest entry, which is the creation.
const actorByCodeID = {};
db.audit_logs
  .find(
    { action: "invite.created", targetId: { $in: codes.map((c) => c._id.toString()) } },
    { targetId: 1, actorId: 1, actorName: 1, createdAt: 1 },
  )
  .sort({ createdAt: 1 })
  .forEach((e) => {
    if (!isUserID.test(String(e.actorId || ""))) return;
    if (actorByCodeID[e.targetId]) return;
    actorByCodeID[e.targetId] = { actorId: e.actorId, actorName: e.actorName || "" };
  });

// Does the actor still have an account? A deleted user leaves the code saying
// "Unknown" anyway, so it is worth knowing how many that is before writing.
const actorIDs = Object.values(actorByCodeID).map((a) => a.actorId);
const liveActors = new Set();
for (let i = 0; i < actorIDs.length; i += 500) {
  const batch = actorIDs.slice(i, i + 500).map((id) => ObjectId(id));
  db.users
    .find({ _id: { $in: batch } }, { _id: 1 })
    .forEach((u) => liveActors.add(u._id.toString()));
}

let updated = 0, matched = 0, noAudit = 0, deletedActor = 0, guardMissed = 0;
const changed = [];
const sample = [];
const unrecoverableByMonth = {};

for (const code of codes) {
  const hit = actorByCodeID[code._id.toString()];
  if (!hit) {
    noAudit++;
    const month = code.createdAt ? code.createdAt.toISOString().slice(0, 7) : "unknown";
    unrecoverableByMonth[month] = (unrecoverableByMonth[month] || 0) + 1;
    continue;
  }
  matched++;
  if (!liveActors.has(hit.actorId)) deletedActor++;
  if (sample.length < 10) {
    sample.push(`  ${code.code}  ->  ${hit.actorId}  (${hit.actorName || "no name in audit"})`);
  }
  if (!apply) continue;

  const res = db.inviteCodes.updateOne(
    { _id: code._id, $or: blankCreatedBy.$or },
    { $set: { createdBy: hit.actorId } },
  );
  if (res.modifiedCount === 1) {
    updated++;
    changed.push(code._id.toString());
  } else {
    guardMissed++;
  }
}

print(`recoverable from the audit log: ${matched}`);
print(`  of those, the creator's account is gone: ${deletedActor}`);
print(`no creation audit entry, left alone: ${noAudit}`);
if (noAudit > 0) {
  print("  by month created:");
  Object.keys(unrecoverableByMonth)
    .sort()
    .forEach((m) => print(`    ${m}: ${unrecoverableByMonth[m]}`));
}
if (sample.length > 0) {
  print(`\nsample of the attribution:\n${sample.join("\n")}`);
}

if (!apply) {
  print(`\nDry run: nothing written. Re-run with APPLY=true to attribute ${matched} invite code(s).`);
  quit(0);
}

print(`\nupdated: ${updated}`);
if (guardMissed > 0) print(`skipped, createdBy filled in since the read: ${guardMissed}`);
print(`changed _ids:\n${changed.join("\n")}`);
