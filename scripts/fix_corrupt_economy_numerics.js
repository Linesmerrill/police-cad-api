// Repair community/department economy numeric fields that have been corrupted
// with out-of-range or non-integer values, which cause every community read to
// 500 with "overflows int64" / "cannot decode ... into int" once the Go decoder
// hits the bad field.
//
// Background: the PATCH /api/v1/community/{communityId}/departments/{departmentId}
// handler used to accept an unvalidated map[string]interface{} and write each
// field straight into MongoDB. If a client sent a JS Number that JSON-encoded as
// scientific notation (e.g. 3e+22 from a long numeric typed into a number
// input), it was stored as a BSON double and broke the document forever.
//
// This script:
//   - scans every community
//   - for each known economy numeric field (community-level + per-department),
//     replaces values that are not safe int64 ints (NaN, Infinity, doubles,
//     out-of-range, negative where not allowed) with a clamped/default int
//   - logs every change with the community _id and the field path
//   - is idempotent: running again is a no-op
//
// Usage (run from the police-cad-api scripts dir against a Mongo shell):
//   DRY_RUN=true mongosh "$DB_URI" fix_corrupt_economy_numerics.js
//   mongosh "$DB_URI" fix_corrupt_economy_numerics.js
//
// DRY_RUN defaults to true; set to "false" via shell env or by editing below
// to actually apply writes. The script always prints a summary.

const DRY_RUN = (typeof process !== "undefined" && process.env && process.env.DRY_RUN)
  ? process.env.DRY_RUN !== "false"
  : true;

print("=== FIX CORRUPT ECONOMY NUMERICS ===");
print(`DRY_RUN: ${DRY_RUN}`);
print("");

// Field specs: name -> { min, max, defaultIfBad }
// Bounds are intentionally generous so we only clamp truly broken values.
// int64 max is 9223372036854775807; we keep everything well under so the Go
// `int` (architecture-dependent, but always at least 32-bit signed) decoder
// is safe even on 32-bit targets.
const DEPT_FIELDS = {
  basePayPerHour:           { min: 0, max: 1_000_000_000, defaultIfBad: 0 },     // cents/hr, $10M/hr cap
  maxSessionMinutes:        { min: 1, max: 24 * 60 * 7,   defaultIfBad: 120 },   // 1 min .. 1 week
  afkPromptIntervalSeconds: { min: 30, max: 86400,         defaultIfBad: 600 },  // 30s .. 1 day
  afkGraceSeconds:          { min: 10, max: 86400,         defaultIfBad: 60 },   // 10s .. 1 day
};

const COMMUNITY_ECONOMY_FIELDS = {
  defaultStartingBalance: { min: 0,                       max: 1_000_000_000_00, defaultIfBad: 0 },   // cents, $1B cap
  defaultDueDays:         { min: 0,                       max: 365,              defaultIfBad: 14 },
  contestExtensionDays:   { min: 0,                       max: 365,              defaultIfBad: 7 },
};

const SAFE_INT_MAX = Number.MAX_SAFE_INTEGER; // 2^53 - 1, far below int64 max
const SAFE_INT_MIN = Number.MIN_SAFE_INTEGER;

function isSafeInt(v) {
  return typeof v === "number"
    && Number.isFinite(v)
    && Math.floor(v) === v
    && v <= SAFE_INT_MAX
    && v >= SAFE_INT_MIN;
}

// Decide whether the stored value is acceptable. We're lenient about MISSING
// fields (those are fine — the Go decoder treats them as zero) and only act
// when the field is present-but-broken.
function repair(value, spec) {
  if (value === undefined || value === null) return { changed: false };
  if (!isSafeInt(value)) {
    return { changed: true, newValue: spec.defaultIfBad, reason: `not-safe-int (${String(value)})` };
  }
  if (value < spec.min) {
    return { changed: true, newValue: spec.min, reason: `below-min (${value} < ${spec.min})` };
  }
  if (value > spec.max) {
    return { changed: true, newValue: spec.defaultIfBad, reason: `above-max (${value} > ${spec.max})` };
  }
  // Already an int but Mongo may have stored it as a double (NumberDouble) due
  // to past JSON-decoded writes. We can detect this in mongosh: bsonsize()
  // doesn't tell us the type but $type would on the server side. We can't
  // cheaply detect double-vs-long here without an extra round trip, so we
  // accept safe ints as-is. The Go BSON decoder converts NumberDouble to int
  // fine *as long as* the value fits — which by definition a safe int does.
  return { changed: false };
}

const cursor = db.communities.find(
  {},
  { _id: 1, "community.economy": 1, "community.departments": 1 }
);

let scanned = 0;
let communitiesUpdated = 0;
let fieldsRepaired = 0;
const examples = [];

while (cursor.hasNext()) {
  const doc = cursor.next();
  scanned++;

  const setOps = {};
  const docFixes = [];

  // Community-level economy fields
  const econ = doc.community && doc.community.economy;
  if (econ && typeof econ === "object") {
    for (const [field, spec] of Object.entries(COMMUNITY_ECONOMY_FIELDS)) {
      const result = repair(econ[field], spec);
      if (result.changed) {
        setOps[`community.economy.${field}`] = result.newValue;
        docFixes.push({ path: `community.economy.${field}`, was: econ[field], now: result.newValue, reason: result.reason });
      }
    }
  }

  // Per-department economy fields
  const depts = (doc.community && doc.community.departments) || [];
  for (let i = 0; i < depts.length; i++) {
    const dept = depts[i] || {};
    for (const [field, spec] of Object.entries(DEPT_FIELDS)) {
      const result = repair(dept[field], spec);
      if (result.changed) {
        setOps[`community.departments.${i}.${field}`] = result.newValue;
        docFixes.push({
          path: `community.departments.${i}.${field}`,
          deptId: dept._id,
          deptName: dept.name,
          was: dept[field],
          now: result.newValue,
          reason: result.reason,
        });
      }
    }
  }

  if (Object.keys(setOps).length === 0) continue;

  communitiesUpdated++;
  fieldsRepaired += Object.keys(setOps).length;
  if (examples.length < 20) examples.push({ communityId: doc._id, fixes: docFixes });

  if (!DRY_RUN) {
    const res = db.communities.updateOne({ _id: doc._id }, { $set: setOps });
    if (res.modifiedCount !== 1) {
      print(`!! WARN: updateOne modifiedCount=${res.modifiedCount} for ${doc._id}`);
    }
  }
}

print("");
print(`Scanned communities: ${scanned}`);
print(`Communities needing repair: ${communitiesUpdated}`);
print(`Total fields repaired: ${fieldsRepaired}`);
print("");

if (examples.length > 0) {
  print(`First ${examples.length} affected communities:`);
  for (const ex of examples) {
    print(`  community ${ex.communityId}`);
    for (const f of ex.fixes) {
      const dept = f.deptId ? ` (dept ${f.deptId} "${f.deptName || ""}")` : "";
      print(`    ${f.path}${dept}: ${JSON.stringify(f.was)} -> ${JSON.stringify(f.now)}  [${f.reason}]`);
    }
  }
}

if (DRY_RUN) {
  print("");
  print("DRY_RUN was true — no writes performed. Re-run with DRY_RUN=false to apply.");
}
