# Finance — Owner-Only P&L Dashboard (Go API)

Owner-only financial endpoints for the LPC P&L dashboard. All routes live
under `/api/v1/admin/finance/` and are gated by the `RequireOwner`
middleware (no exceptions). Secrets come from Heroku config vars; nothing
secret is stored in code or the database (the Plaid access token is never
stored in Mongo — it comes from the `PLAID_ACCESS_TOKEN` env var).

**Single source of truth: the bank.** Plaid bank sync is the SOLE
income/expense source for the P&L (cash basis). There is no manual expense
entry and no ad-network integration: `subscription_events` (Stripe +
RevenueCat) is kept only as an earned-revenue complement, and the bank
numbers are what drive `income.total`, `expenses`, and `profit`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/admin/finance/summary?from=YYYY-MM&to=YYYY-MM` | Monthly P&L (see JSON shape below). `from`/`to` default to the last 12 months. 400 on bad format. |
| POST | `/api/v1/admin/finance/plaid/link-token` | Returns `{link_token, expiration}` — Plaid Link token (products=[transactions], country_codes=[US]) for the owner to connect a bank. 503 when Plaid isn't configured. |
| POST | `/api/v1/admin/finance/plaid/exchange` | Body: `{public_token}`. Exchanges the Link public_token for an access token and returns `{access_token, item_id, message}` **once**; the message tells the owner to set the Heroku config var `PLAID_ACCESS_TOKEN`. The token is never logged and never stored in the DB. 503 when Plaid isn't configured. |
| POST | `/api/v1/admin/finance/plaid/sync` | Runs Plaid `/transactions/sync` from the stored cursor, looping while `has_more`; upserts added/modified (by `transaction_id`), deletes removed; persists the new cursor + `last_sync_at` + accounts snapshot; returns `{added, modified, removed, has_more:false}`. 503 when not connected. |
| GET | `/api/v1/admin/finance/plaid/status` | Returns `{connected, last_sync, accounts:[{name, mask, type}], item_id?}`. `connected` = `PLAID_ACCESS_TOKEN` is set. Never 503s — this is how the owner learns the bank isn't connected yet. |

Every route returns 401 for missing/invalid/expired tokens or unknown/inactive
admins, and 403 for authenticated non-owners.

### Summary JSON shape (contract with the web frontend — do not rename fields)

```json
{
  "months": [
    {
      "month": "2026-09",
      "income": { "stripe": 123.45, "iap_gross": 200.0, "iap_net": 170.0, "total": 310.20 },
      "expenses": 88.10,
      "profit": 222.10,
      "bank": { "connected": true, "income": 310.20, "expenses": 88.10 },
      "sources": {
        "stripe":     {"connected": true},
        "revenuecat": {"connected": true},
        "bank":       {"connected": true}
      }
    }
  ],
  "bank_connected": true,
  "warnings": ["bank: failed to read transactions (...)"]
}
```

`warnings` is omitted when empty. It carries non-fatal degradation notes
(e.g. Plaid failures) — the summary still renders with bank numbers at 0.

### Aggregation rules

- Month bucket = UTC month of the event's `purchasedAt` (subscription
  events) or the bank transaction's posted `date`. Events with nil
  `purchasedAt` are skipped. **Pending bank transactions are excluded.**
- `income.stripe`: sum of `priceUsd` where `provider="stripe"` and
  `eventType="invoice.payment_succeeded"`.
- `income.iap_gross`: `provider="revenuecat"` with `eventType` in
  `INITIAL_PURCHASE` / `RENEWAL`, **minus** `REFUND` sums.
- `income.iap_net` = `iap_gross` × `IAP_NET_RATE` (see assumption below).
  These three are the earned-revenue complement — they do **not** drive the
  P&L when a bank is connected.
- `bank.income`: sum of `|amount|` for bank transactions with `amount < 0`
  (Plaid sign convention: negative = money in).
- `bank.expenses`: sum of `amount` for bank transactions with `amount > 0`
  (positive = money out).
- When the bank is connected (`bank_connected: true`): `income.total` =
  `bank.income`, `expenses` = `bank.expenses` (cash basis — no
  double-counting with the subscription events).
- When the bank is NOT connected (`bank_connected: false`): `bank.*` = 0 /
  `connected: false`, `income.total` = `stripe` + `iap_net`, `expenses` = 0,
  `profit` = `income.total`. The web frontend shows a "connect your bank"
  empty state off the `bank_connected` flag.
- `profit` = `total` − `expenses`. All money values rounded to 2 decimals
  (rounded once, at the end).

### ⚠️ Cash-basis semantics (important)

Bank payouts lag earned revenue: AdSense pays ~3 weeks after month-end,
Apple/Google pay app-store proceeds ~45 days after the sale. The P&L is
intentionally cash basis (money in the bank), so a big launch month will
show subscription revenue in `income.stripe`/`iap_*` before it shows up in
`bank.income`. That is expected, not a bug.

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

## Plaid bank sync

Plaid is the bank-data pipeline. Two Mongo collections
(`databases/finance.go`, `models/finance.go`):

- `bank_transactions` (unique index on `transaction_id`, ensured async at
  startup in `api/handlers/api.go`): `transaction_id`, `account_id`,
  `account_name`, `account_mask`, `name`, `merchant_name`, `amount`
  (Plaid-signed), `direction` derived from the sign (`"in"` when amount < 0,
  `"out"` when amount > 0, `"none"` for the zero edge case), `date` (posted
  date, UTC), `pending`, Plaid `category` + `personal_finance_category`
  primary, `source="plaid"`, `created_at`/`updated_at`.
- `finance_plaid_state` (single document): `item_id`, `cursor`,
  `last_sync_at`, `accounts` snapshot (`account_id`, `name`, `mask`, `type`,
  `subtype`). **The access token is never stored here** — only in the
  `PLAID_ACCESS_TOKEN` env var.

The Plaid client is wrapped in the `plaidSyncClient` interface
(api/handlers/finance_plaid.go) so the sync logic is unit-testable with a
mock; the production implementation uses the official Plaid Go SDK
(`github.com/plaid/plaid-go/v39`, pinned in `go.mod`).

## Environment variables

| Var | Required | Notes |
|---|---|---|
| `JWT_SECRET` | yes | Existing. Signs/verifies admin JWTs. |
| `PLAID_CLIENT_ID` | for Plaid | Plaid dashboard → API keys. |
| `PLAID_SECRET` | for Plaid | Plaid dashboard → API keys. |
| `PLAID_ENV` | no | `sandbox` (default) / `development` / `production`. |
| `PLAID_ACCESS_TOKEN` | for bank sync | Set from the one-time `/plaid/exchange` output. |
| `IAP_NET_RATE` | no | Decimal fraction for `iap_net` math. Default `0.85`. |

Env var names are fixed — do not rename. Any Plaid endpoint when
unconfigured returns 503 naming the missing vars.

### Plaid setup steps for the owner

1. Sign up at [dashboard.plaid.com](https://dashboard.plaid.com) and create
   an app.
2. Copy the **sandbox** client ID and secret for testing now, and set
   `PLAID_CLIENT_ID` / `PLAID_SECRET` as Heroku config vars on the API app.
   (Production access requires Plaid's approval — apply in the dashboard
   when ready, then switch `PLAID_ENV=production`.)
3. In the Finance tab, click the connect-bank flow: the API's
   `POST /admin/finance/plaid/link-token` returns a Link token, Link opens
   for the owner to pick their bank, and the resulting `public_token` is
   sent to `POST /admin/finance/plaid/exchange`.
4. The exchange response shows the access token **once** — set it as the
   Heroku config var `PLAID_ACCESS_TOKEN`.
5. `POST /admin/finance/plaid/sync` pulls transactions; repeat after new
   bank activity (or wire a scheduler later). `GET /admin/finance/summary`
   then shows the cash-basis P&L.

## Running the tests

Go 1.25 is required (`go` was installed to `/usr/local/go` on the dev VM;
ensure it is on `PATH`).

```bash
cd ~/workspace/lpc-applications/police-cad-api
go build ./...
go vet ./...
go test ./api/handlers/ -run 'TestRequireOwner|TestBuildFinanceSummary|TestParseSummaryRange|TestIAPNetRate|TestPlaid|TestBankTransaction|TestNewPlaid|TestRunPlaidSync' -v
```

Tests live in `api/handlers/finance_test.go` and
`api/handlers/finance_plaid_test.go` and follow the repo's existing
`*_test.go` conventions (testify + `databases/mocks` + httptest, plus
in-memory fakes for the Plaid client and the bank/state DB interfaces):

- `TestRequireOwner_*`: valid owner JWT → passes; legacy `Role=="owner"` →
  passes; non-owner → 403; bad signature → 401; **tampered roles claim with
  non-owner doc → 403** (proves the claim is not trusted); inactive owner,
  expired token, missing header, non-admin scope, unknown admin → 401.
- `TestBuildFinanceSummary_*`: monthly bucketing (UTC), refund subtraction,
  net-rate math, rounding, nil-`purchasedAt` skipping, empty months, bank
  cash-basis mode (pending excluded, abs-value income, UTC bucketing,
  zero-amount edge), bank_connected true/false modes, plus
  range/date validation helpers.
- `TestPlaidDirection_*` / `TestBankTransactionFromPlaid_*`: Plaid
  sign-convention derivation (negative→in, positive→out, zero→none) and the
  transaction→document mapping.
- `TestRunPlaidSync_*`: sync upsert/delete logic against a mock Plaid
  client — add new, modify existing, remove deleted, cursor persisted
  through the `has_more` loop, accounts snapshot, error propagation.
- `TestPlaid{LinkToken,Exchange,Sync,Status}_*`: 503s when unconfigured
  (naming the missing vars), 400 on bad exchange body, the exchange
  one-time token + item-ID persistence, sync cursor persistence, status
  connected/disconnected shapes, and **RequireOwner enforced on all four
  Plaid routes** (401 without token, 403 for non-owners).

## Deferred to v2

- **AdMob / AdSense earnings breakdown**: dropped from scope. The bank is
  the single P&L source of truth; ad payouts arrive as bank deposits.
- **CSV import**: no bulk-import endpoint.
- **Scheduled sync**: `POST /admin/finance/plaid/sync` is manual today; a
  cron could trigger it automatically.
- **Transaction categorization rules**: Plaid categories are stored as-is;
  no custom mapping yet.
- **Multi-currency**: stored as-is; no FX conversion.
