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
| POST | `/api/v1/admin/finance/plaid/link-token` | Body (optional): `{mode: "update" \| "new_accounts"}`. Returns `{link_token, expiration}`. With no mode: a token to connect a bank (products=[transactions], country_codes=[US], webhook=`PLAID_WEBHOOK_URL`, `transactions.days_requested=730`, since history is fixed at connection time and Plaid's default is 90 days). `update`: Link in update mode on the existing item, to repair it. `new_accounts`: update mode with account selection, to add accounts. 409 for a mode when no bank is connected. 503 when Plaid isn't configured. |
| POST | `/api/v1/admin/finance/plaid/exchange` | Body: `{public_token}`. Exchanges the Link public_token for an access token and returns `{access_token, item_id, message}` **once**; the message tells the owner to set the Heroku config var `PLAID_ACCESS_TOKEN`. The token is never logged and never stored in the DB. 503 when Plaid isn't configured. |
| POST | `/api/v1/admin/finance/plaid/sync` | Runs Plaid `/transactions/sync` from the stored cursor, looping while `has_more`; upserts added/modified (by `transaction_id`), deletes removed; persists the new cursor + `last_sync_at` + accounts snapshot; returns `{added, modified, removed, has_more:false}`. 503 when not connected. |
| GET | `/api/v1/admin/finance/plaid/status` | Returns `{connected, last_sync, accounts:[{name, mask, type}], item_id?, item_status, consent_expires_at?, new_accounts_available, sandbox}`. `connected` = `PLAID_ACCESS_TOKEN` is set. When connected it also calls `/accounts/get` (refreshing accounts and catching a broken login) and points the item's webhooks at `PLAID_WEBHOOK_URL` if needed. Never 503s — this is how the owner learns the bank isn't connected yet. |
| POST | `/api/v1/admin/finance/plaid/update-complete` | Called when Link in update mode succeeds: `item_status=ok`, clears `consent_expires_at` and `new_accounts_available`, and starts a sync. |
| POST | `/api/v1/admin/finance/plaid/sandbox-webhook` | Body: `{code}` (default `NEW_ACCOUNTS_AVAILABLE`). Fires a Plaid test webhook at the item. Sandbox only (403 in production). |
| POST | `/api/v1/admin/finance/plaid/disconnect` | Body: `{delete_data}`. Calls Plaid `/item/remove` (the token stops working and billing stops), marks `disconnected_at`, clears the cursor and accounts, and with `delete_data` deletes every Plaid bank transaction. Tags and merchant rules are kept. A connected bank means `PLAID_ACCESS_TOKEN` is set **and** not disconnected; switching banks through `/exchange` removes the previous item. |
| POST | `/api/v1/webhooks/plaid` | **Public.** Plaid's webhooks, verified by the `Plaid-Verification` JWT (ES256, key from `/webhook_verification_key/get`, body SHA-256 must match, under 5 minutes old); 401 otherwise. See "Webhooks and update mode". |

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
- **Moves between your own linked accounts are excluded from both.** When the
  same amount leaves one linked account and arrives in another within 4 days,
  both ends are skipped: a checking-to-savings transfer is not income or an
  expense, and paying off a linked credit card would otherwise count every
  card purchase twice. Pairing is by amount, account and date, not by Plaid's
  category, because a card payment is the only record of card spending when
  the card is *not* linked, and Plaid labels some real revenue payouts as
  transfers. A transfer to an account that is not linked (for example your
  personal account) still counts, since that money did leave the business.
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

## Tags, hiding and merchant rules

The owner labels bank transactions (Steam, Google Ads, ...) and hides the ones
that don't belong in the business's books. One tag per transaction, so the
by-tag totals add up to the P&L.

| Method | Path | Notes |
|---|---|---|
| GET | `/admin/finance/transactions` | `from`, `to` (YYYY-MM), `page` (1-based), `limit` (≤100), `tag` (id or `untagged`), `hidden` (`exclude` default, `include`, `only`), `search`. Returns `{data, totalCount, page, limit}`, newest first; each row has `internal_transfer`. |
| PATCH | `/admin/finance/transactions/{transaction_id}` | `{hidden?, tag_id?, apply_to_merchant?}`. `tag_id: ""` removes the tag. `apply_to_merchant` saves a rule and tags the merchant's other **untagged** transactions. |
| GET / POST | `/admin/finance/tags` | `{name, color?}`; names unique ignoring case; colour `#rrggbb`, else the next palette colour. |
| PATCH / DELETE | `/admin/finance/tags/{id}` | Deleting untags its transactions and removes its rules. |
| GET | `/admin/finance/tag-rules` | Merchant → tag. |
| DELETE | `/admin/finance/tag-rules/{id}` | Transactions it already tagged keep their tag. |

- **Hidden** transactions are left out of the P&L and the by-tag totals, like
  pending ones and internal transfers.
- **The summary** gains `by_tag: {income: [...], expenses: [...]}`, each
  `{tag_id, name, color, amount}` largest first, with an Untagged slice last.
- **Plaid sync never touches the owner's fields.** It `$set`s only the fields
  Plaid owns; `created_at`, `hidden` and a rule's tag are `$setOnInsert`.
- **Rules** match `merchant_key`: the merchant name (or the description when
  Plaid has none), lowercased with whitespace collapsed. A new transaction
  gets its merchant's tag on arrival.

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

## Webhooks and update mode

`api/handlers/finance_plaid_webhook.go`. `finance_plaid_state` also keeps
`item_status` (`ok`, `login_required`, `pending_expiration`,
`pending_disconnect`, `revoked`), `item_error_code`, `consent_expires_at`,
`new_accounts_available`, `webhook_url` and `last_webhook_at`.

| Webhook | Effect |
|---|---|
| `TRANSACTIONS SYNC_UPDATES_AVAILABLE` | Sync in the background |
| `ITEM ERROR` (`ITEM_LOGIN_REQUIRED` and other login errors) | `login_required` |
| `ITEM PENDING_EXPIRATION` | `pending_expiration`, saves `consent_expires_at` |
| `ITEM PENDING_DISCONNECT` | `pending_disconnect` |
| `ITEM USER_PERMISSION_REVOKED`, `USER_ACCOUNT_REVOKED` | `revoked` |
| `ITEM LOGIN_REPAIRED` | `ok` |
| `ITEM NEW_ACCOUNTS_AVAILABLE` | `new_accounts_available=true` |

Moving into any status other than `ok` posts one amber Discord warning.
Webhooks for a different `item_id` than the stored one are ignored.

Syncs (button, page load, webhook) share one mutex, and a sync that hits
`TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION` restarts from the stored
cursor (up to 3 times). A sync or `/accounts/get` that fails with a login
error marks the item `login_required`; `/accounts/get` succeeding again
clears it. Exchanging a token for a **different** item resets the cursor and
status.

The Finance tab turns the bank line amber for any status other than `ok`
and offers **Fix connection** (Link with `mode=update`), or **Add accounts**
(`mode=new_accounts`) when new accounts are available. Success calls
`update-complete`.

## Environment variables

| Var | Required | Notes |
|---|---|---|
| `JWT_SECRET` | yes | Existing. Signs/verifies admin JWTs. |
| `PLAID_CLIENT_ID` | for Plaid | Plaid dashboard → API keys. |
| `PLAID_SECRET` | for Plaid | Plaid dashboard → API keys. |
| `PLAID_ENV` | no | `sandbox` (default) or `production`. Plaid retired its Development environment in 2024; any other value means sandbox. |
| `PLAID_ACCESS_TOKEN` | for bank sync | Set from the one-time `/plaid/exchange` output. |
| `PLAID_WEBHOOK_URL` | for webhooks | Public URL of `/api/v1/webhooks/plaid` on this API, e.g. `https://<api-host>/api/v1/webhooks/plaid`. Sent on Link tokens and set on the existing item. |
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
