# Finance — Owner-Only P&L Dashboard (Go API)

Owner-only financial endpoints for the LPC P&L dashboard. All routes live
under `/api/v1/admin/finance/` and are gated by the `RequireOwner`
middleware — with one deliberate exception: the AdSense OAuth callback
(see below), which Google's servers hit via the owner's browser redirect
and which is instead authorized by its single-use OAuth `state`. Secrets come from Heroku config vars; nothing secret is stored
in code or the database (except encrypted-at-rest config on Heroku's side).

## Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/admin/finance/summary?from=YYYY-MM&to=YYYY-MM` | Monthly P&L (see JSON shape below). `from`/`to` default to the last 12 months. 400 on bad format. |
| GET | `/api/v1/admin/finance/expenses?from=YYYY-MM-DD&to=YYYY-MM-DD` | List expenses; defaults to current month-to-date. Returns `{expenses:[...], total}`. |
| POST | `/api/v1/admin/finance/expenses` | Create an expense. Body: `{date, amount, currency, category, vendor, notes, source}`. |
| PUT | `/api/v1/admin/finance/expenses/{id}` | Replace editable fields; returns the updated doc. 404 if missing. |
| DELETE | `/api/v1/admin/finance/expenses/{id}` | Delete; returns `{success:true}`. |
| GET | `/api/v1/admin/finance/adsense/oauth/start` | Returns `{url}` — the Google consent URL for AdSense. |
| GET | `/api/v1/admin/finance/adsense/oauth/callback?code=&state=` | Exchanges the code; returns the refresh token **once** with instructions to store it as `ADSENSE_REFRESH_TOKEN`. |

Every route returns 401 for missing/invalid/expired tokens or unknown/inactive
admins, and 403 for authenticated non-owners.

### Summary JSON shape (contract with the web frontend — do not rename fields)

```json
{
  "months": [
    {
      "month": "2026-09",
      "income": { "stripe": 123.45, "iap_gross": 200.0, "iap_net": 170.0,
                  "adsense": 0, "admob": 0, "total": 293.45 },
      "expenses": 50.0,
      "profit": 243.45,
      "sources": {
        "stripe":     {"connected": true},
        "revenuecat": {"connected": true},
        "adsense":    {"connected": false},
        "admob":      {"connected": false}
      }
    }
  ],
  "warnings": ["adsense: failed to refresh access token (...)"]
}
```

`warnings` is omitted when empty. It carries non-fatal degradation notes
(e.g. AdSense failures) — the summary still renders with that source at 0.

### Aggregation rules

- Month bucket = UTC month of the event's `purchasedAt`. Events with nil
  `purchasedAt` are skipped.
- `income.stripe`: sum of `priceUsd` where `provider="stripe"` and
  `eventType="invoice.payment_succeeded"`.
- `income.iap_gross`: `provider="revenuecat"` with `eventType` in
  `INITIAL_PURCHASE` / `RENEWAL`, **minus** `REFUND` sums.
- `income.iap_net` = `iap_gross` × `IAP_NET_RATE` (see assumption below).
- `income.total` = `stripe` + `iap_net` + `adsense` + `admob`.
- `expenses`: sum of `amount` from the `finance_expenses` collection by UTC
  month of `date`.
- `profit` = `total` − `expenses`. All money values rounded to 2 decimals
  (rounded once, at the end).
- `sources.stripe` / `sources.revenuecat` are always `connected: true`
  (DB-backed). AdSense is connected only when fully configured *and* the
  fetch succeeds. AdMob is a stub: always `connected: false`, income 0.

### ⚠️ IAP net-rate assumption (important)

Apple/Google never report per-event net payouts, so `iap_net` is an
**estimate**: `iap_gross × IAP_NET_RATE` (default **0.85**). The 15% figure
matches the standard small-business / second-year subscription cut; first-year
Apple subscriptions can be 30%, so treat this as approximate. Override with
the `IAP_NET_RATE` env var (a decimal fraction, e.g. `0.85`). Invalid or
out-of-range values fall back to 0.85.

## How RequireOwner works

`Finance.RequireOwner` (api/handlers/finance.go) is standard
`func(http.Handler) http.Handler` middleware, applied at route registration
in `api/handlers/api.go`:

1. Reads `Authorization: Bearer <token>` (401 if missing/malformed).
2. Verifies HS256 with `JWT_SECRET`; rejects unexpected signing methods and
   expired tokens (401).
3. Requires the `scope` claim to be `"admin"` (401 for user tokens).
4. Loads the `admin_users` document by `ObjectID(sub)` via
   `databases.NewAdminDatabase`; unknown or `active=false` → 401.
5. Requires `"owner"` in the document's `Roles` **or** legacy `Role=="owner"`
   (403 otherwise).

The `roles` claim in the JWT is **never trusted on its own** — the document
is re-read on every request, so a tampered or stale claim cannot escalate.
The verified `*models.AdminUser` is placed in the request context for
downstream handlers (used for expense `createdBy`).

## Expenses

Stored in the `finance_expenses` collection (`databases/finance.go`,
`models/finance.go`):

| Field | Notes |
|---|---|
| `date` | Date of the expense; API accepts `YYYY-MM-DD` (or RFC3339), returns `YYYY-MM-DD` |
| `amount` | Float > 0 (400 otherwise), rounded to 2 decimals |
| `currency` | Default `"USD"`, uppercased |
| `category` / `vendor` / `notes` | Free-form strings |
| `source` | Enum `manual` / `csv` / `plaid` (default `manual`; 400 on anything else). `plaid` is accepted but reserved for the v2 bank auto-sync — nothing writes it yet. |
| `createdBy` | `<owner admin id>|<email>` of the creating owner |
| `createdAt` | Server timestamp |

PUT replaces all editable fields; `createdBy`/`createdAt` are preserved.

## AdSense OAuth (user-consent flow)

AdSense does not support service accounts, so connection is a one-time
owner consent flow:

1. `GET .../adsense/oauth/start` → `{url}`. The `state` is a 32-byte random
   value stored in-memory, single-use, 10-minute expiry.
2. The owner approves at Google and lands on the redirect URI, which should
   forward `code` + `state` to `GET .../adsense/oauth/callback`.
3. The callback verifies state (400 if missing/expired/reused), exchanges the
   code at Google's token endpoint, and returns `{refresh_token, message}`
   **displayed once**. The message tells the owner to set the Heroku config
   var `ADSENSE_REFRESH_TOKEN`. The token is never logged.

When `ADSENSE_CLIENT_ID`, `ADSENSE_CLIENT_SECRET`, `ADSENSE_REDIRECT_URI`
and `ADSENSE_REFRESH_TOKEN` are all set, the summary endpoint exchanges the
refresh token for an access token, calls AdSense `accounts.list`, then
`reports:generate` (DATE dimension × EARNINGS metric, USD) over the requested
range, and aggregates earnings by month into `income.adsense`. Any failure
degrades gracefully: `adsense=0`, `connected=false`, plus a `warnings` entry.

## Environment variables

| Var | Required | Notes |
|---|---|---|
| `JWT_SECRET` | yes | Existing. Signs/verifies admin JWTs. |
| `IAP_NET_RATE` | no | Decimal fraction for `iap_net` math. Default `0.85`. |
| `ADSENSE_CLIENT_ID` | for AdSense | Google OAuth client ID. |
| `ADSENSE_CLIENT_SECRET` | for AdSense | Google OAuth client secret. |
| `ADSENSE_REDIRECT_URI` | for AdSense | Must exactly match the URI registered in Google Cloud Console. |
| `ADSENSE_REFRESH_TOKEN` | for AdSense earnings | Set from the one-time OAuth callback output. |

Env var names are fixed — do not rename.

## Running the tests

Go 1.25 is required (`go` was installed to `/usr/local/go` on the dev VM;
ensure it is on `PATH`).

```bash
cd ~/workspace/lpc-applications/police-cad-api
go build ./...
go vet ./...
go test ./api/handlers/ -run 'TestRequireOwner|TestBuildFinanceSummary|TestParseSummaryRange|TestIAPNetRate|TestParseExpenseDate|TestValidExpenseSource' -v
```

Tests live in `api/handlers/finance_test.go` and follow the repo's existing
`*_test.go` conventions (testify + `databases/mocks` + httptest):

- `TestRequireOwner_*`: valid owner JWT → passes; legacy `Role=="owner"` →
  passes; non-owner → 403; bad signature → 401; **tampered roles claim with
  non-owner doc → 403** (proves the claim is not trusted); inactive owner,
  expired token, missing header, non-admin scope, unknown admin → 401.
- `TestBuildFinanceSummary_*`: monthly bucketing (UTC), refund subtraction,
  net-rate math, rounding, nil-`purchasedAt` skipping, empty months,
  AdSense aggregation, plus range/date/source validation helpers.

## Deferred to v2

- **Plaid bank auto-sync**: bank is the source of truth for money in/out
  (per 2026-09-30 decision). A Plaid connection would auto-import
  transactions into `finance_expenses` with `source="plaid"` and reconcile
  against Stripe/AdSense/Apple payouts. The `plaid` source enum value is
  already accepted and reserved.
- **AdMob**: currently a stub (`connected:false`, income 0). Needs the
  AdMob API with its own OAuth setup.
- **CSV import endpoint**: bulk expense import (`source="csv"` enum exists;
  no upload endpoint yet).
- **Expense categories taxonomy**: currently free-form strings.
- **Multi-currency expenses**: stored as-is; no FX conversion.
