// Remediation: cap out-of-int64-range economy pay values on communities.
//
// A rank payRatePerHour (or department basePayPerHour) stored as a huge BSON
// double (e.g. 1e32, from a bad website/Mongoose write) overflows the Go int64
// field and makes the ENTIRE community fail to decode — 500ing every
// community-scoped read (page load, panic alerts, units, signal-100, ...).
//
// This finds every community with an overflowing value and sets it to 0 (which
// the app treats as "unset" → falls back to base pay). The Cents defensive
// decoder in models/community.go now prevents this from bricking reads going
// forward; this script cleans up existing bad data.
//
// Usage: mongosh "<CONNECTION_STRING>" scripts/remediate_payrate_overflow.js
// Idempotent — safe to re-run.

var INT64_MAX = 9223372036854775807;
var comms = 0, fields = 0;

db.communities.find(
  {
    $or: [
      { "community.departments.ranks.payRatePerHour": { $gt: INT64_MAX } },
      { "community.departments.basePayPerHour": { $gt: INT64_MAX } }
    ]
  },
  { "community.departments": 1, "community.name": 1 }
).forEach(function (c) {
  var set = {};
  (c.community.departments || []).forEach(function (d, di) {
    if (typeof d.basePayPerHour === "number" && Math.abs(d.basePayPerHour) > INT64_MAX) {
      set["community.departments." + di + ".basePayPerHour"] = 0;
    }
    (d.ranks || []).forEach(function (r, ri) {
      if (typeof r.payRatePerHour === "number" && Math.abs(r.payRatePerHour) > INT64_MAX) {
        set["community.departments." + di + ".ranks." + ri + ".payRatePerHour"] = 0;
      }
    });
  });
  if (Object.keys(set).length) {
    db.communities.updateOne({ _id: c._id }, { $set: set });
    print("Fixed " + Object.keys(set).length + " field(s) on " + c._id + " (" + (c.community.name || "?") + ")");
    comms++;
    fields += Object.keys(set).length;
  }
});

print("\nDone: fixed " + fields + " field(s) across " + comms + " communities");
