// Seeds / updates the `countdowns` collection.
//
// Run in MongoDB Atlas -> Browse Collections -> "Shell" tab, or:
//   mongosh "YOUR_CONNECTION_STRING" < seed_countdowns.js
//
// ---------------------------------------------------------------------------
// TO CHANGE THE LAUNCH DATE (e.g. Rockstar delays GTA 6)
//
//   db.countdowns.updateOne(
//     { slug: "gta6" },
//     { $set: { launchDate: "2027-03-04", launchesAt: ISODate("2027-03-03T23:00:00Z"), updatedAt: new Date() } }
//   )
//
// Every surface picks it up within 5 minutes (the API sets Cache-Control:
// max-age=300). No website or mobile deploy is needed, which is the whole
// reason the date lives here instead of in client constants: a mobile release
// can take a day to reach users.
//
// TO PULL THE COUNTDOWN DOWN ENTIRELY:
//   db.countdowns.updateOne({ slug: "gta6" }, { $set: { active: false } })
// ---------------------------------------------------------------------------
//
// Document shape:
//   slug             unique key, e.g. "gta6"
//   title            headline, e.g. "Grand Theft Auto VI"
//   subtitle         one short line under the title
//   launchDate       "YYYY-MM-DD" wall-clock date. Used by mode "localMidnight".
//   launchesAt       absolute instant. Used by mode "instant", and by the
//                    Discord bot, whose timestamps localize per viewer.
//   mode             "localMidnight" | "instant"   (see below)
//   theme            visual treatment the clients apply, e.g. "gta6"
//   surfaces         subset of ["web","mobile","bot"]; empty means all
//   postLaunchHours  how long to show "OUT NOW" before hiding entirely
//   active           false hides it everywhere without deleting the record
//
// About mode: console storefronts list GTA 6 at local midnight in every
// market rather than one synchronized worldwide moment, so "localMidnight"
// counts down to midnight on launchDate in each viewer's own timezone and
// everybody's timer reaches zero when their region actually unlocks. Switch to
// "instant" only if Rockstar announces a single global unlock time.

const now = new Date();

const countdowns = [
  {
    slug: "gta6",
    title: "Grand Theft Auto VI",
    subtitle: "Back to Vice City.",
    launchDate: "2026-11-19",
    // 11:00 PM UTC Nov 18 is midnight Nov 19 in central Europe, which is the
    // value console storefronts carry. Only used in "instant" mode and by the
    // bot's relative timestamp.
    launchesAt: ISODate("2026-11-18T23:00:00Z"),
    mode: "localMidnight",
    theme: "gta6",
    surfaces: ["web", "mobile", "bot"],
    postLaunchHours: 72,
    ctaLabel: "",
    ctaUrl: "",
    active: true,
  },
];

countdowns.forEach(function (c) {
  const res = db.countdowns.updateOne(
    { slug: c.slug },
    {
      $set: Object.assign({}, c, { updatedAt: now }),
      $setOnInsert: { createdAt: now },
    },
    { upsert: true }
  );

  if (res.upsertedCount) {
    print("✓ Created countdown: " + c.slug + " (" + c.launchDate + ")");
  } else if (res.modifiedCount) {
    print("✓ Updated countdown: " + c.slug + " (" + c.launchDate + ")");
  } else {
    print("• Countdown already current: " + c.slug);
  }
});

print("\n=== Countdowns seeded ===");
