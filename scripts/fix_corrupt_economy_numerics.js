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

// Field spec:
//   nonsenseFallback — value to install when the field is NaN/Infinity/
//     non-integer or some unexpected wrapper type. No way to recover the
//     user's intent in those cases, so we reset to the schema default.
//   overflowCap       — value to install when the field is a finite integer
//     that exceeds int64. Used in place of the nonsense fallback because the
//     user clearly intended a very high number; clamping to the API's
//     documented cap preserves intent rather than zeroing it out.
//
// We do NOT enforce conceptual minimums (e.g. afkPromptIntervalSeconds >= 30)
// and we do NOT touch finite integers that fit in int64 — even if huge —
// because the Go decoder reads those without complaint. Caps match what the
// hardened UpdateDepartmentDetailsHandler now enforces on new writes.
const DEPT_FIELDS = {
  basePayPerHour:           { nonsenseFallback: 0,   overflowCap: 1_000_000_000 }, // cents/hr ($10M/hr)
  maxSessionMinutes:        { nonsenseFallback: 120, overflowCap: 24 * 60 * 7 },   // 1 week
  afkPromptIntervalSeconds: { nonsenseFallback: 600, overflowCap: 86400 },         // 1 day
  afkGraceSeconds:          { nonsenseFallback: 60,  overflowCap: 86400 },         // 1 day
};

const COMMUNITY_ECONOMY_FIELDS = {
  defaultStartingBalance: { nonsenseFallback: 0,  overflowCap: 1_000_000_000_000 }, // $10B in cents
  defaultDueDays:         { nonsenseFallback: 14, overflowCap: 3650 },              // 10 years
  contestExtensionDays:   { nonsenseFallback: 7,  overflowCap: 3650 },
};

// Actual int64 limits. JS can't represent 2^63-1 exactly (it rounds to
// 9223372036854776000), but as a *threshold* that approximation is fine —
// anything strictly greater is unambiguously over the int64 ceiling.
const INT64_MAX = 9223372036854775807;
const INT64_MIN = -9223372036854775808;

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

// detectBad returns null if the value is fine, or { newValue, reason } if
// it needs repair. Conditions treated as fine:
//   - field absent
//   - field is a BSON Long (correct int64 wire type)
//   - field is a JS number that is finite, an integer, and in int64 range
//     (even if huge — Go decodes it cleanly)
// Repairs:
//   - NaN / Infinity / non-integer / unexpected wrapper → nonsenseFallback
//   - > int64 range → overflowCap (preserves "they wanted a very high value")
function detectBad(value, spec) {
  if (value === undefined || value === null) return null;
  if (isMongoLong(value)) return null;
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      return { newValue: spec.nonsenseFallback, reason: `non-finite (${String(value)})` };
    }
    if (!Number.isInteger(value)) {
      return { newValue: spec.nonsenseFallback, reason: `non-integer (${value})` };
    }
    if (value > INT64_MAX) {
      return { newValue: spec.overflowCap, reason: `overflows int64, clamped (${value})` };
    }
    if (value < INT64_MIN) {
      return { newValue: spec.nonsenseFallback, reason: `underflows int64 (${value})` };
    }
    return null; // ordinary in-range integer (even if very large) — leave alone
  }
  return { newValue: spec.nonsenseFallback, reason: `unexpected type (${typeof value})` };
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
