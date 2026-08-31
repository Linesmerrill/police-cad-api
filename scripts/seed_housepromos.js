// Seeds / updates the `housePromos` collection.
//
// Run in MongoDB Atlas -> Browse Collections -> "Shell" tab, or:
//   mongosh "YOUR_CONNECTION_STRING" < seed_housepromos.js
//
// ---------------------------------------------------------------------------
// TO SWITCH A PROMO OFF  (the kill switch -- this is the important one)
//
//   db.housePromos.updateOne({ slug: "favion-osrs" }, { $set: { active: false } })
//
// Every surface stops showing it within 5 minutes (the API sets
// Cache-Control: max-age=300). No website or mobile deploy is needed, which is
// the whole reason these live here rather than in client constants: a mobile
// release can take a day to reach users, and a promo is the kind of thing you
// sometimes need to stop showing today.
//
// TO CHANGE THE COPY OR THE LINK:
//   db.housePromos.updateOne(
//     { slug: "favion-osrs" },
//     { $set: { title: "...", ctaUrl: "https://...", updatedAt: new Date() } }
//   )
//
// TO GIVE IT AN END DATE (it then retires itself, no flag to remember):
//   db.housePromos.updateOne(
//     { slug: "favion-osrs" },
//     { $set: { endsAt: ISODate("2026-12-31T00:00:00Z"), updatedAt: new Date() } }
//   )
// ---------------------------------------------------------------------------
//
// WHERE THESE RENDER
//
// Not in a slot of their own. Both clients already collapse an ad slot that
// Google did not fill -- NativeAdCard returns null on failure in the mobile
// app, and the website adds .ad-unfilled to the container -- and a house promo
// takes that space instead. So it costs no ad revenue, it appears
// intermittently rather than on every page, and subscribers who have paid for
// fewer ads see correspondingly fewer of these: the clients reuse the existing
// ad-visibility rules unchanged.
//
// Document shape:
//   slug       unique key, e.g. "favion-osrs"
//   eyebrow    small label above the title; what marks this as ours, not bought
//   title      headline
//   body       one or two short lines
//   ctaLabel   button text; defaults to "Learn more" if omitted
//   ctaUrl     where the button goes. REQUIRED -- the API drops a promo
//              without one rather than serving a card with a dead button
//   theme      visual treatment the clients apply, e.g. "osrs"
//   surfaces   subset of ["web","mobile"]; empty means both
//   endsAt     optional; the API stops serving it after this, whatever `active` says
//   active     false hides it everywhere without deleting the record

const now = new Date();

const promos = [
  {
    slug: "favion-osrs",
    eyebrow: "A quick one from us",
    title: "2,000,000,000 GP or bust",
    body:
      "My brother lost 2 billion GP in Old School RuneScape after 704 days " +
      "played. Yes, really. We are helping him rebuild.",
    ctaLabel: "Support Favion",
    ctaUrl:
      "https://www.gofundme.com/f/help-my-brother-recover-2b-lost-runescape-gp",
    theme: "osrs",
    surfaces: ["web", "mobile"],
    active: true,
  },
];

promos.forEach(function (promo) {
  // Upsert on slug so re-running this updates the copy in place instead of
  // creating a second record that both surfaces would then show.
  const res = db.housePromos.updateOne(
    { slug: promo.slug },
    {
      $set: Object.assign({}, promo, { updatedAt: now }),
      $setOnInsert: { createdAt: now },
    },
    { upsert: true }
  );

  if (res.upsertedCount) {
    print("created house promo: " + promo.slug);
  } else {
    print("updated house promo: " + promo.slug);
  }
});

print("\n=== housePromos seeded ===");
print("Kill switch:");
print('  db.housePromos.updateOne({ slug: "favion-osrs" }, { $set: { active: false } })');
