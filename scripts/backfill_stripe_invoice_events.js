// Backfill the amount and paid date onto Stripe payment events recorded
// before the webhook stored them.
//
// The Stripe webhook logged every invoice.payment_succeeded event without a
// priceUsd or purchasedAt, so the Finance tab, which sums priceUsd by
// purchasedAt, showed $0 for Stripe. Each event's full invoice is in its
// rawPayload, so both can be read back out. Mirrors stripeInvoicePayment in
// api/handlers/stripe_invoice_payment.go: amount_paid / 100, USD only, dated
// status_transitions.paid_at, else created.
//
// Dry run by default: it prints what it would write. Pass APPLY=true to write.
// Idempotent: only events with no priceUsd are touched.
//
// DB_URI ends in connection options, so select the database instead of
// appending it to the URI:
//
//   mongosh "$DB_URI" --eval "db = db.getSiblingDB('$DB_NAME'); APPLY=false" \
//     --file scripts/backfill_stripe_invoice_events.js

const apply = typeof APPLY !== "undefined" && APPLY === true;

const filter = {
  provider: "stripe",
  eventType: "invoice.payment_succeeded",
  $or: [{ priceUsd: { $exists: false } }, { priceUsd: 0 }, { priceUsd: null }],
};

let seen = 0, updated = 0, skipped = 0, totalUsd = 0;
const skippedReasons = {};
const byMonth = {};

db.subscription_events.find(filter, { rawPayload: 1, environment: 1 }).forEach((e) => {
  seen++;
  let inv = null;
  try {
    const raw = typeof e.rawPayload === "string" ? JSON.parse(e.rawPayload) : e.rawPayload;
    inv = raw && raw.data && raw.data.object;
  } catch (err) {
    inv = null;
  }
  const skip = (why) => { skipped++; skippedReasons[why] = (skippedReasons[why] || 0) + 1; };
  if (!inv || inv.object !== "invoice") return skip("no invoice in payload");
  if (!(inv.amount_paid > 0)) return skip("nothing paid");
  if (String(inv.currency || "").toLowerCase() !== "usd") return skip("not USD");
  const ts = (inv.status_transitions && inv.status_transitions.paid_at) || inv.created;
  if (!ts) return skip("no date");

  const priceUsd = inv.amount_paid / 100;
  const paidAt = new Date(ts * 1000);
  if (e.environment !== "SANDBOX") {
    totalUsd += priceUsd;
    const m = paidAt.toISOString().slice(0, 7);
    byMonth[m] = (byMonth[m] || 0) + priceUsd;
  }
  if (apply) {
    db.subscription_events.updateOne(
      { _id: e._id, $or: filter.$or },
      { $set: { priceUsd: priceUsd, purchasedAt: paidAt, currency: "usd" } }
    );
  }
  updated++;
});

print((apply ? "APPLIED" : "DRY RUN") + ": " + seen + " Stripe payment events without an amount");
print("  " + (apply ? "updated" : "would update") + ": " + updated + ", skipped: " + skipped + " " + JSON.stringify(skippedReasons));
print("  live revenue recovered: $" + totalUsd.toFixed(2));
Object.keys(byMonth).sort().forEach((m) => print("    " + m + "  $" + byMonth[m].toFixed(2)));
