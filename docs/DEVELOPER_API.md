# LPC Developer API

Community-scoped API keys so a third party can build against Lines Police CAD
without us running their integration. The first consumer is a FiveM mod: players
create characters and records in the game, the mod pushes them to us, and the
player later opens the website or the app and finds everything they made.

This document is the plan. It is written to be read before the code, and kept
true afterwards.

---

## Merrill decisions

Things I could not decide for you. Everything else in here I have decided and
built; say the word and I will change any of it.

1. **Stripe product and prices for the paid tier.** The existing pattern is one
   env var per price ID, mapped in `mapStripePriceIDToPlan` (`api/handlers/user.go:4371`).
   The paid developer tier needs a product (suggested: "LPC Developer API Pro")
   with a monthly price, optionally annual, and then
   `STRIPE_DEV_API_PRO_MONTHLY_PRICE_ID` / `STRIPE_DEV_API_PRO_ANNUAL_PRICE_ID`
   set on `police-cad-app-api`. Until they exist the upgrade path is hidden and
   every developer is on Free. Nothing breaks; there is just no way to pay.
   **Also needs a price.** I have not guessed one.

2. **Legal copy for provisioned accounts.** The mod creates LPC accounts for
   players who have not seen our Terms or Privacy Policy, and our audience skews
   young. I have built the safeguards (an unclaimed account cannot log in, holds
   no email, and is purgeable), and the developer must accept Developer Terms to
   enable a key. But the Terms and the Privacy Policy need a clause covering
   accounts created on a player's behalf by a third-party integration, and what
   a developer may and may not do with them. That is a wording and possibly a
   legal-review call, not a code call. The checkbox and the docs section are in
   place and point at `/terms-and-conditions`.

3. **Retention for accounts nobody claims.** I propose: purge an unclaimed
   account after 90 days if it holds no records, keep it if it does, and never
   purge a claimed one. Not implemented yet — it wants a cron job and your
   agreement on the number.

4. **The limit numbers.** My proposal is in [Limits](#limits-and-tiers). They are
   set in one place and are env-overridable, so changing them is a config change.

5. **Rate-limit store.** No decision needed unless you want to spend money. See
   [Rate-limit store](#rate-limit-store): I used MongoDB, which costs nothing and
   is correct across dynos. A Redis add-on would be marginally faster and is a
   drop-in swap later.

6. **Do key-created communities count against the developer's own plan?** I built
   it as yes: a community a key creates is owned by the developer's LPC account
   and counts against `models.CommunityCap` exactly like one they made by hand.
   Confirm that is what you meant.

7. **Should a mod bypass community limits?** I built it as no: mod-created
   civilians respect the community's civilian approval system and creation
   limits. A key is not above the community's own settings.

---

## What a key can and cannot do

A key is a developer's credential, scoped to the communities it is allowed to
touch and to the scopes the developer picked.

**Never, by any key, at any tier:**

- auth or account changes on a claimed account (password, email, deactivation)
- anything to do with money: wallets, balances, transfers, economy payouts
- billing, Stripe, subscriptions, boosts
- the admin console and every `/admin/` endpoint
- community settings, roles, permissions, or deletion
- reading any community the key is not tied to, or any user outside a tied
  community

These are enforced by the surface, not by convention: the developer routes are
their own subrouter, and nothing in the list above is registered on it.

---

## Two levels of scope

A key has a **parent developer** and a **set of communities**.

```
developer account  (one per LPC user)
  └── key  (lpc_live_…)
        ├── grant → community A   (created by this key)
        ├── grant → community B   (created by this key)
        └── grant → community C   (an owner/admin approved this key)
```

- A key with `communities:provision` can **create** a community. The new
  community is owned by the developer's LPC user, counts against their plan, and
  is granted to the key automatically.
- An owner or administrator of an existing community (or a member with
  `manage community settings`) can **approve** a key for their community. A
  developer can request this; the key stays `pending` and does nothing until
  someone with authority approves it.
- Every grant is its own row, so the community side can revoke its grant without
  touching the developer's key, and the developer can delete their key without
  needing the community's help. Both sides see the same list.
- A key created from the website for a single community gets exactly one grant
  and can never acquire another unless it also holds `communities:provision`.
  That keeps the simple case simple: **one key, one community.**

Every request re-reads the grant from the database and re-checks the scope. A
revoked grant stops working on the next request, not at the end of a session.

---

## Data model

New collections. Field names are bson.

### `developer_accounts`

One per LPC user who turned on a developer account.

| field | type | note |
|---|---|---|
| `_id` | ObjectID | |
| `userId` | string | the LPC user, unique |
| `plan` | string | `free` \| `pro` |
| `planActive` | bool | |
| `stripeCustomerId`, `stripeSubscriptionId` | string | |
| `appName`, `appUrl` | string | what they are building, for support |
| `termsAcceptedAt`, `termsVersion` | date, string | Developer Terms acceptance |
| `status` | string | `active` \| `suspended` (staff) |
| `suspendedReason`, `suspendedBy`, `suspendedAt` | | |
| `createdAt`, `updatedAt` | date | |

### `developer_api_keys`

| field | type | note |
|---|---|---|
| `_id` | ObjectID | the key id, used everywhere else |
| `developerId`, `userId` | string | |
| `name` | string | developer's label |
| `keyHash` | string | SHA-256 hex of the full key. **The key itself is never stored.** |
| `displayPrefix` | string | `lpc_live_a1b2c3d4` — enough to recognise, not to use |
| `lastFour` | string | |
| `scopes` | []string | |
| `status` | string | `pending` \| `active` \| `revoked` |
| `rotatedFromKeyId` | string | set on the new key after a rotate |
| `lastUsedAt` | date | |
| `revokedBy`, `revokedAt`, `revokeReason` | | |
| `createdAt`, `updatedAt` | date | |

**Why HMAC-SHA256, keyed with a server-side secret.** Three decisions, each of
which looks wrong from a distance:

- **Not bcrypt or argon2.** Those make guessing a *low-entropy* secret
  expensive. This key is 32 bytes straight from `crypto/rand`, so there is no
  dictionary and no human pattern, and the search space is already out of
  reach. A slow hash buys nothing here and costs on every single request, where
  its deliberate cost would become the request budget.
- **Not a bare digest either.** A plain SHA-256 is confirmable by anyone who
  already holds a candidate key: with a database dump they could check "is this
  key one of yours" offline, and match a key leaked from elsewhere against the
  table. Keying the hash with a secret the database does not contain removes
  that — a dump on its own proves nothing and verifies nothing.
- **Deterministic, not salted per row**, because this value is looked up by
  equality on every request. A per-row salt would mean scanning the table.

The secret is `DEV_API_KEY_PEPPER`. When the surface is enabled without it, the
middleware refuses with a 503 and logs why, rather than hashing everything with
an empty key — a silent fallback would also mean every key minted before the
secret appeared stopped validating the moment it was set.

### `developer_community_grants`

| field | type | note |
|---|---|---|
| `_id` | ObjectID | |
| `keyId`, `developerId`, `communityId` | string | unique on (keyId, communityId) |
| `source` | string | `created_by_key` \| `owner_approved` |
| `status` | string | `pending` \| `active` \| `revoked` |
| `requestedAt`, `approvedBy`, `approvedAt` | | who approved, for the audit trail |
| `revokedBy`, `revokedAt`, `revokeReason` | | |

### `developer_api_usage`

Per key per day. This is both the dashboard's source and the daily cap counter,
so there is one number, not two that can disagree.

`_id` is `<keyId>:<YYYY-MM-DD>`, which makes every write an `_id`-targeted
upsert — the cheapest write MongoDB offers.

| field | note |
|---|---|
| `requests`, `allowed` | |
| `rejected429`, `rejected401`, `rejected403` | the dashboard's "recent errors" |
| `accountsCreated`, `communitiesCreated` | the provisioning caps |
| `byRoute` | map of route name → count, a bounded set |
| `lastRequestAt` | |
| `expiresAt` | TTL, 400 days, so a year of history is visible |

Aggregated counts only. We never store request bodies.

### `developer_api_rate`

The per-minute burst counter. `_id` is `<keyId>:<unix-minute>`, TTL two minutes.
One small document per key per active minute, swept by Mongo.

### `player_identities`

The map from a game identifier to an LPC user. **Unique on (provider,
identifier)**, which is what makes the upsert idempotent and settles two mods
racing on the same player: one insert wins, the loser gets a duplicate-key error
and re-reads the winner's row.

| field | note |
|---|---|
| `provider` | `fivem` \| `discord` \| `steam` |
| `identifier` | normalized (lowercased, `license:` prefix stripped for fivem) |
| `userId` | the LPC user |
| `createdByKeyId`, `createdByDeveloperId` | for bulk cleanup |
| `claimed` | denormalized from the user, for cheap reads |

### `developer_claim_codes`

| field | note |
|---|---|
| `codeHash` | SHA-256 of the code; the code is shown in-game and never stored |
| `userId`, `keyId`, `communityId` | |
| `expiresAt` | TTL, 30 minutes |
| `usedAt`, `usedByUserId` | single use |

### Changes to existing documents

On the **user**: `developerProvisioned`, `claimed` (`*bool`),
`provisionedByKeyId`, `provisionedByDeveloperId`, `provisionedAt`,
`gameIdentities`. Also `discord` and `discordConnected`, which the website has
written for years but the Go struct never declared.

On **records a key creates** (civilians, vehicles, firearms, licenses): a
`source` subdocument holding `type: "developer_api"`, `keyId`, `developerId`,
`provider`, `identifier`, `createdAt`. That is what makes a misbehaving key's
output findable and bulk-removable.

---

## Identity: how a mod names a player

**We never accept a raw LPC user id from a key holder as proof of anything.**

The mod knows a player by their game identifier: a FiveM license, or a Discord
ID. It sends that. We resolve it through `player_identities`, and then the
resolved user still has to be an approved member of the target community before
any write lands on them.

A player reference in a request path or body is `provider:identifier`:

```
fivem:1a2b3c4d5e6f7a8b
discord:198573466634
```

`lpc:<userId>` is also accepted, but it buys the caller nothing: the user must
still be an approved member of the community the key is acting in, which is the
real authorization boundary. A forged id just fails that check.

### Upserting a player

```
POST /api/v1/dev/players
{ "provider": "fivem", "identifier": "license:1a2b…", "username": "Dave" }
```

- Identifier already known → the same LPC user comes back. Idempotent, every
  time, by the unique index.
- Not known → we create an **unclaimed** user: no email, no password hash,
  `claimed: false`, tagged with the key that made it. Then the identity row.

### Why an unclaimed account cannot be logged into

Three independent reasons, because one is not enough for something this
sensitive:

1. It has no password hash, and `bcrypt.CompareHashAndPassword` fails on an
   empty hash. Both login paths bcrypt before they succeed
   (`api/handlers/user.go:192`, `api/middleware.go:226`).
2. It has no email, and every login and password-reset path looks a user up *by*
   email. There is nothing to type.
3. An explicit `claimed == false` check in the auth path, so a future code path
   that sets an email cannot quietly open the hole.

A mod cannot set an email on an account it provisions. That is not an oversight
to be fixed later; it is the point. An email the mod supplies is an email the mod
controls, and accepting it would hand the developer a way into a player's
account.

### Claiming

The player ends up proving control of both sides.

**Claim code.** The mod asks for a code and shows it in the game:

```
POST /api/v1/dev/players/fivem/1a2b…/claim-code
→ { "code": "K7M2-QX9P-4T", "expiresAt": "…", "url": "https://…/developer/claim" }
```

The player opens that URL and enters the code.

- **No LPC account yet:** they set an email and password on the provisioned
  account. It becomes claimed, and it is theirs.
- **They already have an LPC account:** they sign in first, then enter the code.
  Their in-game records are moved onto their real account — civilians,
  vehicles, firearms, licenses, identity rows and memberships are re-pointed —
  and the provisioned shell is deleted. The two accounts are never matched by
  email.

Codes are 10 characters of Crockford base32 (50 bits), stored as a hash, single
use, 30-minute expiry, with the claim endpoint rate-limited per IP. A code is
shown on a screen that other players may be able to see, so it is deliberately
short-lived rather than deliberately long.

**Discord.** If the identity is a Discord ID and the signed-in LPC account's
`user.discord.id` matches it, the claim completes without a code — they have
already proved they hold that Discord account to us, and to the game.

---

## Scopes

Small on purpose, and driven by what a FiveM mod actually needs.

| scope | grants |
|---|---|
| `community:read` | community profile, departments, ten-codes |
| `units:read` | the on-duty roster |
| `units:write` | set a member's status, ten-code and location |
| `calls:read` / `calls:write` | list calls / create, update, close |
| `bolos:read` / `bolos:write` | |
| `civilians:read` / `civilians:write` | look up by name / create and update |
| `vehicles:read` / `vehicles:write` | by plate |
| `firearms:read` / `firearms:write` | by serial |
| `licenses:write` | issue a license to a civilian |
| `players:provision` | upsert players, issue claim codes |
| `communities:provision` | create a community owned by the developer |
| `members:read` / `members:write` | roster, invite, approve, set role, remove |

A scope the key does not hold is a 403 that names the scope it needed.

---

## Routes

The developer surface is `/api/v1/dev/…`, its own `mux` subrouter with its own
middleware. It shares no authentication with the website or the app.

A key authenticates as `Authorization: Bearer lpc_live_…`. It is **not** sent in
`X-API-Key`: that header is the first-party gateway secret
(`api/write_auth.go:20`), and a developer key must never satisfy it.

```
GET    /api/v1/dev/whoami                                     — key, scopes, communities, limits
GET    /api/v1/dev/communities                                — the communities this key may touch
POST   /api/v1/dev/communities                                communities:provision
GET    /api/v1/dev/communities/{id}                           community:read
GET    /api/v1/dev/communities/{id}/departments               community:read
GET    /api/v1/dev/communities/{id}/ten-codes                 community:read

GET    /api/v1/dev/communities/{id}/units                     units:read
PUT    /api/v1/dev/communities/{id}/units/status/bulk          units:write   — up to 100 players
PUT    /api/v1/dev/communities/{id}/units/{playerRef}/status   units:write

GET    /api/v1/dev/communities/{id}/calls                     calls:read
POST   /api/v1/dev/communities/{id}/calls                     calls:write
POST   /api/v1/dev/communities/{id}/calls/{callId}/close       calls:write

GET    /api/v1/dev/communities/{id}/bolos                     bolos:read
POST   /api/v1/dev/communities/{id}/bolos                     bolos:write

GET    /api/v1/dev/communities/{id}/civilians?name=           civilians:read
POST   /api/v1/dev/communities/{id}/civilians                 civilians:write
GET    /api/v1/dev/communities/{id}/vehicles?plate=           vehicles:read
POST   /api/v1/dev/communities/{id}/vehicles                  vehicles:write
GET    /api/v1/dev/communities/{id}/firearms?serial=          firearms:read
POST   /api/v1/dev/communities/{id}/firearms                  firearms:write
POST   /api/v1/dev/communities/{id}/licenses                  licenses:write

POST   /api/v1/dev/players                                    players:provision
GET    /api/v1/dev/players/{provider}/{identifier}            players:provision
POST   /api/v1/dev/players/{provider}/{identifier}/claim-code  players:provision

GET    /api/v1/dev/communities/{id}/members                   members:read
POST   /api/v1/dev/communities/{id}/members                   members:write
PATCH  /api/v1/dev/communities/{id}/members/{playerRef}        members:write
DELETE /api/v1/dev/communities/{id}/members/{playerRef}        members:write
POST   /api/v1/dev/communities/{id}/members/{playerRef}/approve members:write
```

**Not in v1:** clocking a member in and out. It is coupled to the economy
system — it needs `community.economy.enabled` and the department's own economy
toggle — which most communities have off, so the scope would be selectable and
then do nothing for nearly everyone. The scope string is reserved.

The **bulk status** endpoint matters more than it looks. A FiveM server ticking
once a second with 64 players is 64 requests per second if the mod loops, which
is 3,840 a minute and over even the paid limit. One bulk call per tick is 60 a
minute and inside the free limit. The docs lead with this.

### Management routes (first-party, the website calls these)

These live on the normal authenticated surface, not the developer one.

```
GET  /api/v1/developer/account                      POST /api/v1/developer/account/enable
                                                    POST /api/v1/developer/account/disable
GET  /api/v1/developer/keys                         POST /api/v1/developer/keys
POST /api/v1/developer/keys/{id}/rotate             DELETE /api/v1/developer/keys/{id}
GET  /api/v1/developer/keys/{id}/usage?from&to
POST /api/v1/developer/keys/{id}/request-community
GET  /api/v1/developer/community/{communityId}/keys        — owner/admin view
POST /api/v1/developer/community/{communityId}/keys/{keyId}/approve
POST /api/v1/developer/community/{communityId}/keys/{keyId}/revoke
POST /api/v1/developer/claim                               — a signed-in player redeems a code
```

### Where the new surface sits in the middleware chain

The chain today is, outermost first (`main.go:43-46`):

```
ApiKeyGateway → CorsMiddleware → RequireWriteAuth → MetricsMiddleware → router
```

Two of those would otherwise break the developer surface, and both need an
explicit, narrow exemption:

- **`ApiKeyGateway`** (`api/handlers/gateway.go:76`) blocks requests whose
  User-Agent looks like a CLI tool. A mod calling us with curl's UA would be
  turned away before it reached its own authentication.
- **`RequireWriteAuth`** (`api/write_auth.go:107`) rejects mutating requests
  that carry neither the gateway secret, nor our Origin, nor a bearer session.
  A developer key is none of those.

Both now exempt exactly the `/api/v1/dev/` prefix, and both say in a comment
that the prefix carries its own stricter gate. This is the one place where the
design makes the generic protection weaker, so it is worth being blunt about
why it is safe: the exemption hands the request to
`RequireDeveloperAPIKey`, which demands a valid unrevoked key, an active
developer account, an active grant for the community in the path, and the scope
for the route — a strictly higher bar than the gate it replaced. Nothing else
is registered under that prefix, so nothing else can reach it.

---

## Limits and tiers

The goal is that roughly 80% of developers never hit a wall.

### Request limits, per key

| | burst | daily |
|---|---|---|
| **Free** | 60 / min | 10,000 / day |
| **Pro** | 600 / min | 250,000 / day |

### Provisioning limits, per key per day

Deliberately much lower: these create rows that are hard to undo, and a bug in a
mod's loop should not be able to make ten thousand accounts.

| | player accounts | communities |
|---|---|---|
| **Free** | 25 / day | 2 / day, 3 total |
| **Pro** | 500 / day | 10 / day, 25 total |

### Keys per developer

Free 2, Pro 10.

### What a client sees

Every developer response carries:

```
X-RateLimit-Limit: 60
X-RateLimit-Remaining: 41
X-RateLimit-Reset: 1762041600
X-RateLimit-Scope: minute
```

and a 429 adds `Retry-After` in seconds. The body names which limit was hit and
when it resets, because a mod author reading a log needs to know whether to slow
down or to wait until tomorrow.

### Rate-limit store

**MongoDB counters. No new add-on, no new cost, and no cache.**

The reasoning, since this is the kind of choice that is hard to revisit:

- Production (`police-cad-app-api`) runs **two** Standard-1X web dynos and has
  no Redis add-on — only Papertrail. An in-memory limiter is per-dyno, so a
  60/min limit would really be up to 120/min, and it would drift every time the
  dyno count changed. For a limit whose job is protecting the backend, that is
  not good enough.
- Mongo is **already** the cross-dyno shared store for bearer tokens
  (`api/token_store.go`), using most of this shape: a deterministic `_id` and a
  TTL index. Reusing a proven pattern beats introducing a second kind of
  infrastructure.
- **No cache, deliberately**, which is where this departs from the token store.
  The key document is read on every request. That is one indexed read, and it
  buys a property worth more than the read: revoking a key, or a community
  withdrawing a grant, takes effect on the very next request rather than
  whenever a cache entry happened to expire. A limiter counter cannot be cached
  either without reintroducing the per-dyno drift the store exists to avoid.
- The cost is two `_id`-targeted `$inc` upserts per developer request. `_id` is
  the primary index, so these are the cheapest writes available, and the minute
  documents are swept by a two-minute TTL so the collection stays small.
- The limiter sits behind a `rateLimitStore` interface. If developer traffic ever
  justifies it, Heroku Key-Value Store drops in without touching a handler. That
  is a cost decision for later, not a prerequisite now.

A global safety valve, `DEV_API_GLOBAL_RPM`, caps the whole developer surface
regardless of per-key limits, so one pathological integration cannot spend the
database on everyone else's behalf.

---

## Abuse handling

- Every provisioned record carries the key that made it, so
  `source.keyId = …` finds a misbehaving key's entire output.
- Revoking a key stops it on the next request. Suspending the developer account
  stops all of their keys.
- Separate daily caps on account and community creation, above.
- Claim codes are single-use, hashed, short-lived, and rate-limited per IP.

---

## Rollout

1. **PR 1 (this one)** ships the foundation behind `DEV_API_ENABLED`, default
   off: developer accounts, key issue / rotate / revoke, the community grant
   model and both sides of its approval, the authenticating middleware, the rate
   limiter, the usage counters, and `whoami` + `communities` on the developer
   surface. With the flag off, `/api/v1/dev/…` returns 404 and nothing else in
   the product changes. The domain endpoints, provisioning, the claim flow and
   membership follow in the PRs stacked on this one.
2. Set `DEV_API_KEY_PEPPER` to a long random string (`openssl rand -hex 32`).
   The surface refuses to serve without it. Treat it like `JWT_SECRET`:
   changing it later invalidates every issued key, so it is set once.
3. Run `scripts/create_indexes.js` for the new collections.
4. Turn it on on `police-cad-dev`. Your brother gets the first key and the
   quickstart.
5. Turn it on in production. Developer accounts are opt-in, and staff can
   suspend one.
6. Paid tier when the Stripe product exists.

---

## Quickstart

See [`docs/developer-api/QUICKSTART.md`](developer-api/QUICKSTART.md) for the
create community → upsert player → create civilian → show claim code walkthrough
with a FiveM Lua example, and
[`docs/developer-api/openapi.yaml`](developer-api/openapi.yaml) for the spec.
