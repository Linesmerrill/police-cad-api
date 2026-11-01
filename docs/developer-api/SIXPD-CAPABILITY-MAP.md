# Capability map: SixPD FiveM AI dispatch

An answer to [issue #277](https://github.com/Linesmerrill/police-cad-api/issues/277).

Three columns: **available** today on the Developer API, **needs extension**
(the surface exists, the shape does not), and **not supported** (either not
built, or deliberately out of bounds).

The division of responsibilities in the issue is the right one, and the
constraint that matters most — *AI prose must not create fictional evidence or
silently approve legal records* — is already how the surface is built. A key can
draft and record; it cannot approve, grant a permission, or act as anyone but a
player who has the right to act.

---

## P0

### Developer key, server-to-server, least privilege, rotation, revocation, quotas

**Available.**

| | |
|---|---|
| Credential | `Authorization: Bearer lpc_live_…` |
| Scoping | one key, a set of communities, an explicit scope list |
| Rotation | `POST /developer/keys/{id}/rotate` — new key issued, communities carried across, old key revoked after, so rotation is not an outage |
| Revocation | by the developer, or by any community owner withdrawing their own grant. Effective on the **next request**: the key is read from the database every time, with no cache |
| Quotas | Free 60/min and 10,000/day; Pro 600/min and 250,000/day. Separate, much lower daily caps on creating accounts (25) and communities (2) |
| Headers | `X-RateLimit-Limit`, `-Remaining`, `-Reset`, `-Scope`; `Retry-After` on 429 |

**How it relates to the existing credentials — this is the part worth reading
carefully.** There are now three, and they share nothing:

| credential | who holds it | where it goes | what it is for |
|---|---|---|---|
| `X-API-Key` (`API_GATEWAY_KEY`) | **us only** | `X-API-Key` header | our own website's server talking to our own API |
| JWT (`JWT_SECRET`) | signed-in staff | `Authorization: Bearer <jwt>` | the owner-only finance surface |
| **developer key** | **you** | `Authorization: Bearer lpc_live_…` | this API |

A developer key sent in `X-API-Key` is rejected — it is compared against the
gateway secret with a constant-time compare and will not match. A session token
sent as a developer key is rejected before it is even hashed, because it lacks
the `lpc_live_` prefix. There is a test for each of those, and for the fact that
`/api/v1/developer/*` (management: minting keys, approving communities) is on
the *session* surface where a developer key cannot reach it. **A key can never
mint another key or widen its own scopes.**

You are right to insist the key never reaches client Lua. The
[quickstart](QUICKSTART.md) says to use `set` and not `setr` in `server.cfg`,
because `setr` replicates a convar to every connected client.

### Per-key audit trail

**Needs extension (small).** Today: per-key per-day usage counts, including
refusals by status, on the developer dashboard; and community audit-log entries
for membership and community actions, each tagged with the key id. What does not
exist is a queryable per-key feed of *what the key did*. Worth adding; not
blocking.

### Sandbox, examples, versioned OpenAPI, error format, rate limits, pagination

**Mostly available.**

- **Sandbox:** a key with `communities:provision` creates its own community,
  owned by the developer, private by default. That *is* the isolated test
  tenant — no production data, no shared sandbox to contend over. A separate
  sandbox *host* is a Merrill decision (below).
- **OpenAPI:** [`openapi.yaml`](openapi.yaml), 20 paths, hand-written so it
  describes the contract rather than our structs. **Needs extension:** it is a
  file in the repo, not served at a stable versioned URL.
- **Error format:** stable `error` code plus a human `message`, documented in
  the quickstart. Available.
- **Rate limits and pagination:** available; list endpoints take `page` and
  `limit` and are always sorted, so paging cannot repeat or skip rows.

### Dispatch calls with external reference

**Available as of the dispatch PR**, and built to the shape the issue asks for.

```
POST   /dev/communities/{id}/dispatch/calls                   create or update
GET    /dev/communities/{id}/dispatch/calls/{externalId}      read back
POST   /dev/communities/{id}/dispatch/calls/{externalId}/close
```

Addressed by **your** id, not ours. `(community, source, externalId)` is the
key, so posting the same incident twice updates rather than duplicating, and a
reconnect that replays a queue is safe. `source` defaults to the key's name and
can be set explicitly so your dev and production instances keep separate id
spaces.

Carries type, priority, location, status, assigned units, disposition and
timestamps, plus a `link` that deep-links into the CAD. Absent fields on an
update are **left alone**, not cleared, so a status update need not resend
everything. Assignment is all-or-nothing: if one unit is not a member the write
is refused, because a dispatch board that is quietly wrong is worse than one
that rejects you.

Closing an already-closed call returns 200 with `closed: false` rather than an
error, so a retry after a timeout is not something you have to special-case.

### Officer/unit identity and duty status

**Available.**

```
GET /dev/communities/{id}/units
PUT /dev/communities/{id}/units/status/bulk     ← use this one
PUT /dev/communities/{id}/units/{playerRef}/status
GET /dev/communities/{id}/ten-codes
GET /dev/communities/{id}/departments
```

10-8, 10-7, en route, on scene and clear are **the community's own ten-codes**,
not an enum we define, so read them from `/ten-codes` rather than hardcoding —
owners rename them. Identity is tied to community and department, and a status
write is refused for anyone who is not an approved member.

**Use the bulk endpoint on a tick.** 50 players at one request each per second
is 3,000 a minute, over even the paid limit; one bulk call per tick is 60. It is
partial, so one unknown player does not discard the other forty-nine.

### Civilian and vehicle lookup

**Available**, scoped to the community in the path by the query itself.

```
GET /dev/communities/{id}/civilians?name=
GET /dev/communities/{id}/vehicles?plate=
GET /dev/communities/{id}/firearms?serial=
```

Vehicle flags come back as resolved booleans (`stolen`, `validRegistration`,
`validInsurance`), because the stored values use two historical encodings with
opposite polarity and hand-rolling the truth test gets them backwards.

**NPC separation: needs extension.** Records a key creates already carry
`source.keyId`, so synthetic data is findable and removable as a set. What is
missing is an explicit `synthetic: true` flag and a filter, so NPCs do not
appear in a real roster or a real search. Small, and worth doing before anyone
points this at a live community.

### Draft incident / arrest / citation reports

**Not supported yet — the biggest P0 gap, and the right second PR.**

To answer your question about *which* system: not the moderation reports
(those are player-conduct reports to our staff, a different thing entirely).
The right homes are the **arrest report** model and the **configurable forms**
system, which already has templates, versions and submissions. Both support a
draft-then-submit shape, which is exactly what you need.

The constraint you named is the one we would build to: a draft created by a key
is **a draft**, visible to an authorized officer, and only a person submits it.
No scope would allow approval.

### Idempotent writes, predictable errors

**Available** for players (unique index on the game identifier) and dispatch
calls (external id). **Needs extension** for reports once they exist, by the
same external-id mechanism.

---

## P1

| ask | status |
|---|---|
| BOLOs read/write | **Available** (create, list). Warrants: **not supported** on this surface yet. The community's existing `warrantApprovalMode` (auto / random / require-judge) already encodes "no auto-approval", and a key would respect it |
| Report attachment metadata and links | **Not supported.** Link-and-metadata only is the right shape; no binary uploads |
| EMS/fire types and statuses | **Partly available** — departments carry templates including EMS and fire, and ten-codes are per community. Incident/medical forms fall under the reports work |
| Scoped signed webhooks or streaming | **Not supported.** See Merrill decisions |
| Rank mapping that never grants permissions | **Already guaranteed.** A key **cannot assign roles at all** — sending `roleId` is a 403, because roles carry permissions. Departments and ranks can be set, and both are validated against the community. In-game promotion can move a rank; it can never grant a permission |

---

## Decisions for Merrill

None of the P0 asks conflict with the security rules — no money, auth, admin or
cross-community access is requested. These four are judgement calls:

1. **A shared sandbox host.** A developer can already create their own private
   test community on production, which is isolated and costs nothing. A
   separate sandbox *environment* (a public base URL on `police-cad-dev`) means
   another deploy target, another database and third-party traffic on
   infrastructure we use for our own testing. Recommend: no, start with
   per-developer test communities.
2. **Webhooks or streaming (P1).** Outbound HTTP to developer-controlled URLs
   is an SSRF surface and an ongoing operational burden — signing, retries,
   replay, dead-letters, and somebody to watch it. Recommend: polling first,
   and only build this if polling proves inadequate.
3. **Synthetic NPC data.** Do NPC civilians count against a community's
   creation limits, and should they be hidden from real players' searches?
   Recommend: exempt from limits, hidden by default, removable as a set.
4. **How much of the reports work to take on.** The draft-report surface is the
   largest remaining piece and touches the forms system, which real communities
   use daily. Worth scoping on its own rather than folding into this stack.

---

## Recommended first PR, and the acceptance test

Their suggested end-to-end test is a good one, and most of it runs today.

| step | status |
|---|---|
| 1. Player marks 10-8 | ✅ `PUT /units/status/bulk` |
| 2. SixPD creates one CAD call with a stable external id | ✅ the dispatch PR |
| 3. Player accepts; unit status updates | ✅ status + `assignedUnits` |
| 4. SixPD prepares a reviewable draft report linked to the call | ❌ **the gap** |
| 5. Player submits; refresh/retry does not duplicate; outage does not stop play | ✅ for calls and status; ❌ for reports |

**So: steps 1 through 3 and 5 can be built against today.** The recommended
first PR on their side is the adapter for exactly that, which proves the key,
the quotas, the idempotency and the reconciliation path without waiting on us.

**The recommended first PR on our side** is the draft-report surface, scoped to
arrest reports and citations over the existing forms system, draft-only, with
external ids for idempotency. That unblocks step 4 and completes the loop.
