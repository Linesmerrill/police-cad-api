// Normalize firearm `isStolen` values that were written with the wrong
// encoding, which made not-stolen firearms show up as STOLEN on LEO surfaces.
//
// Background: the canonical firearm `isStolen` value across the whole product
// is the STRING "true"/"false". The mobile app, the API model
// (FirearmDetails.IsStolen string), the legacy civ/LEO dashboards, and the
// modern firearm search all read/write "true"/"false".
//
// The new department-dashboard firearm UI (police-cad public/js/dd-firearms.js)
// regressed this: its boolToObj helper wrote "1" for stolen and "2" for not
// stolen. "2" then collided with legacy LEO readers (search-database.js,
// details-modal.js, modern-dashboard.js) that treat "2" as STOLEN — so a
// firearm a civilian created as NOT stolen displayed as stolen to officers.
//
// dd-firearms.js has been fixed to write "true"/"false". This script repairs
// the documents already written with "1"/"2":
//   "1"  -> "true"   (was marked stolen)
//   "2"  -> "false"  (was marked not stolen)
//
// No other firearm writer ever produced "1"/"2", so this mapping is
// unambiguous. Values already stored as "true"/"false" (or anything else) are
// left untouched. The script is idempotent: a second run is a no-op.
//
// Usage (run from the police-cad-api scripts dir against a Mongo shell):
//   DRY_RUN=true  mongosh "$DB_URI" fix_firearm_stolen_status.js   # preview
//   DRY_RUN=false mongosh "$DB_URI" fix_firearm_stolen_status.js   # apply
//
// DRY_RUN defaults to true; you must explicitly set DRY_RUN=false to write.
// Always run the dry run first and eyeball the sample before applying.

const DRY_RUN = (typeof process !== "undefined" && process.env && process.env.DRY_RUN)
  ? process.env.DRY_RUN !== "false"
  : true;

print("=== FIX FIREARM STOLEN STATUS ===");
print(`DRY_RUN: ${DRY_RUN}`);
print("");

// Legacy value -> canonical value. Only these exact string values are touched.
const REMAP = { "1": "true", "2": "false" };

// Match only the documents that actually need repair so the dry-run counts and
// the applied write target the same set.
const filter = { "firearm.isStolen": { $in: Object.keys(REMAP) } };

const total = db.firearms.countDocuments(filter);
print(`Firearms with legacy "1"/"2" isStolen: ${total}`);

if (total === 0) {
  print("Nothing to repair — every firearm already uses canonical values.");
} else {
  // Per-value breakdown + a small sample, so the operator can sanity-check the
  // mapping direction before applying.
  for (const [from, to] of Object.entries(REMAP)) {
    const n = db.firearms.countDocuments({ "firearm.isStolen": from });
    print(`  "${from}" -> "${to}": ${n}`);
  }

  print("");
  print("Sample (up to 10):");
  const sample = db.firearms
    .find(filter, { _id: 1, "firearm.serialNumber": 1, "firearm.isStolen": 1 })
    .limit(10);
  while (sample.hasNext()) {
    const d = sample.next();
    const was = d.firearm && d.firearm.isStolen;
    const serial = (d.firearm && d.firearm.serialNumber) || "(no serial)";
    print(`  ${d._id}  serial=${serial}  ${JSON.stringify(was)} -> ${JSON.stringify(REMAP[was])}`);
  }

  if (!DRY_RUN) {
    print("");
    let modified = 0;
    for (const [from, to] of Object.entries(REMAP)) {
      const res = db.firearms.updateMany(
        { "firearm.isStolen": from },
        { $set: { "firearm.isStolen": to } }
      );
      print(`  applied "${from}" -> "${to}": modified ${res.modifiedCount}`);
      modified += res.modifiedCount;
    }
    print("");
    print(`Total firearms repaired: ${modified}`);
  }
}

print("");
if (DRY_RUN) {
  print("DRY_RUN was true — no writes performed. Re-run with DRY_RUN=false to apply.");
}
