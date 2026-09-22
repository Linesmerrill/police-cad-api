// Backfill the moderation queue fields onto reports written before the queue
// existed.
//
// Reports created from the mobile app only ever carried active:true and an
// actionTaken that was never written. The console sorts by severityRank and
// filters by status, so the existing reports need both to appear in the right
// place. Everything else about them is left exactly as submitted.
//
// Idempotent: only documents missing a field are touched, so re-running it
// cannot reclassify a report a staff member has already decided.
//
// DB_URI ends in connection options (?retryWrites=...&w=majority), so the
// database name cannot be appended to it: "$DB_URI/$DB_NAME" turns the write
// concern into "majority/<dbname>" and every write fails to acknowledge. Connect
// with the bare URI and select the database first:
//
//   mongosh "$DB_URI" --eval "db = db.getSiblingDB('$DB_NAME')" \
//     --file scripts/backfill_report_queue.js
//
// Mirrors models.ReportTierForIssue / models.ReportSeverityRank. If the tiers
// there change, a report already triaged keeps the tier it was judged under:
// this script never overwrites one that is set.

const TIERS = {
  "child safety": { tier: "escalate", rank: 0 },
  "suicide or self-harm": { tier: "welfare", rank: 1 },
  "hate": { tier: "serious", rank: 2 },
  "abuse & harassment": { tier: "serious", rank: 2 },
  "violent speech": { tier: "serious", rank: 2 },
  "violent & hateful entities": { tier: "serious", rank: 2 },
  "illegal & regulated behavior": { tier: "serious", rank: 2 },
  "privacy": { tier: "serious", rank: 2 },
  "spam": { tier: "minor", rank: 3 },
  "impersonation": { tier: "minor", rank: 3 },
  "sensitive or disturbing media": { tier: "minor", rank: 3 },
};

// An issue we do not recognise falls back to the minor tier, whose first rung
// is a warning. That is the recoverable direction to be wrong in.
const FALLBACK = { tier: "minor", rank: 3 };

function classify(issue) {
  const key = String(issue || "").trim().toLowerCase();
  return TIERS[key] || FALLBACK;
}

const cursor = db.reports.find({
  $or: [
    { status: { $exists: false } },
    { severityRank: { $exists: false } },
    { tier: { $exists: false } },
  ],
});

const ops = [];
const byTier = {};
let unclassified = 0;

cursor.forEach((doc) => {
  const c = classify(doc.reportedIssue);
  if (c === FALLBACK && !TIERS[String(doc.reportedIssue || "").trim().toLowerCase()]) {
    unclassified += 1;
    print("  unclassified issue, defaulting to minor: " + JSON.stringify(doc.reportedIssue));
  }

  const set = {};
  // A report with no status has never been looked at, which is what "new"
  // means. Anything already decided keeps its status.
  if (doc.status === undefined) set.status = "new";
  if (doc.severityRank === undefined) set.severityRank = c.rank;
  if (doc.tier === undefined) set.tier = c.tier;

  if (Object.keys(set).length === 0) return;

  byTier[c.tier] = (byTier[c.tier] || 0) + 1;
  ops.push({ updateOne: { filter: { _id: doc._id }, update: { $set: set } } });
});

print("reports needing backfill: " + ops.length);
if (ops.length > 0) {
  const res = db.reports.bulkWrite(ops, { ordered: false });
  print("modified: " + res.modifiedCount);
}
print("by tier: " + JSON.stringify(byTier));
if (unclassified > 0) {
  print("NOTE: " + unclassified + " report(s) had an issue string not in the tier map.");
}

print("\nqueue after backfill:");
["escalate", "welfare", "serious", "minor"].forEach((t) => {
  print("  " + t + ": " + db.reports.countDocuments({ tier: t }));
});
print("  status=new: " + db.reports.countDocuments({ status: "new" }));
