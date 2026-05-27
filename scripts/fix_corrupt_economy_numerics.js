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

// Field spec: just the safe fallback we install when we find an unreadable
// value. We ONLY repair values that the Go BSON decoder cannot read into the
// int-typed model field — doubles that overflow int64, NaN, Infinity, or
// doubles with a fractional part. We do NOT enforce conceptual minimums
// (e.g. "afkPromptIntervalSeconds >= 30"). A value of 0 is the legitimate
// zero-value for departments that never configured the economy; the Go
// decoder handles it fine, and "repairing" it would touch 99%+ of the
// population for no benefit.
const DEPT_FIELDS = {
  basePayPerHour:           { defaultIfBad: 0 },
  maxSessionMinutes:        { defaultIfBad: 120 },
  afkPromptIntervalSeconds: { defaultIfBad: 600 },
  afkGraceSeconds:          { defaultIfBad: 60 },
};

const COMMUNITY_ECONOMY_FIELDS = {
  defaultStartingBalance: { defaultIfBad: 0 },
  defaultDueDays:         { defaultIfBad: 14 },
  contestExtensionDays:   { defaultIfBad: 7 },
};

// Anything past this point is unreadable by the Go decoder targeting int64.
// (Real int64 max is 2^63-1 = 9223372036854775807, but JS can't represent it
// exactly; MAX_SAFE_INTEGER = 2^53-1 is a safer guard.)
const INT64_SAFE_MAX = Number.MAX_SAFE_INTEGER;
const INT64_SAFE_MIN = Number.MIN_SAFE_INTEGER;

// isMongoLong matches BSON Long, which mongosh surfaces as an object with
// .low/.high/.unsigned. Long is the correct int64 wire type for these
// fields, so it is always valid — including when the wrapped value is zero.
function isMongoLong(v) {
  return v !== null
    && typeof v === "object"
    && typeof v.low === "number"
    && typeof v.high === "number"
    && "unsigned" in v;
}

// detectBad returns null if the value is fine, or { newValue, reason } if it
// needs repair. Conditions treated as fine:
//   - field absent
//   - field is a BSON Long (correct type, always valid)
//   - field is a JS number that is finite, an integer, and in int64 range
// Everything else (NaN, Infinity, non-integer double, overflowing double,
// unexpected wrapper) is decoder-breaking and gets reset to the default.
function detectBad(value, spec) {
  if (value === undefined || value === null) return null;
  if (isMongoLong(value)) return null;
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      return { newValue: spec.defaultIfBad, reason: `non-finite (${String(value)})` };
    }
    if (!Number.isInteger(value)) {
      return { newValue: spec.defaultIfBad, reason: `non-integer (${value})` };
    }
    if (value > INT64_SAFE_MAX || value < INT64_SAFE_MIN) {
      return { newValue: spec.defaultIfBad, reason: `overflows int64 (${value})` };
    }
    return null; // ordinary in-range integer double — decoder handles it
  }
  return { newValue: spec.defaultIfBad, reason: `unexpected type (${typeof value})` };
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
      const bad = detectBad(econ[field], spec);
      if (bad) {
        setOps[`community.economy.${field}`] = bad.newValue;
        docFixes.push({ path: `community.economy.${field}`, was: econ[field], now: bad.newValue, reason: bad.reason });
      }
    }
  }

  // Per-department economy fields
  const depts = (doc.community && doc.community.departments) || [];
  for (let i = 0; i < depts.length; i++) {
    const dept = depts[i] || {};
    for (const [field, spec] of Object.entries(DEPT_FIELDS)) {
      const bad = detectBad(dept[field], spec);
      if (bad) {
        setOps[`community.departments.${i}.${field}`] = bad.newValue;
        docFixes.push({
          path: `community.departments.${i}.${field}`,
          deptId: dept._id,
          deptName: dept.name,
          was: dept[field],
          now: bad.newValue,
          reason: bad.reason,
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
