# Capability map: SixPD FiveM AI dispatch

An answer to [issue #277](https://github.com/Linesmerrill/police-cad-api/issues/277).

Three columns: **available** today on the Developer API, **needs extension**
(the surface exists, the shape does not), and **not supported** (either not
built, or deliberately out of bounds).

The division of responsibilities in the issue is the right one, and the
constraint that matters most, *AI prose must not create fictional evidence or
silently approve legal records*, is already how the surface is built. A key can
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
| Rotation | `POST /developer/keys/{id}/rotate`: new key issued, communities carried across, old key revoked after, so rotation is not an outage |
| Revocation | by the developer, or by any community owner withdrawing their own grant. Effective on the **next request**: the key is read from the database every time, with no cache |
| Quotas | Per key, and they follow the website subscription, there is no separate developer plan. Four tiers, not two: see [the plan table](https://www.linespolice-cad.com/pricing) and the `tiers` array the account endpoint returns, which is generated from the same values the middleware enforces. Creating player accounts and communities has its own, much lower, daily cap |
| Headers | `X-RateLimit-Limit`, `-Remaining`, `-Reset`, `-Scope`; `Retry-After` on 429 |

**How it relates to the existing credentials, this is the part worth reading
carefully.** There are now three, and they share nothing:

| credential | who holds it | where it goes | what it is for |
|---|---|---|---|
| `X-API-Key` (`API_GATEWAY_KEY`) | **us only** | `X-API-Key` header | our own website's server talking to our own API |
| JWT (`JWT_SECRET`) | signed-in staff | `Authorization: Bearer <jwt>` | the owner-only finance surface |
| **developer key** | **you** | `Authorization: Bearer lpc_live_…` | this API |

A developer key sent in `X-API-Key` is rejected, it is compared against the
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

**Available.** Three layers, each answering a different question.

Per-key per-day usage counts, including refusals by status, on the developer
dashboard. Community audit-log entries for membership, record and community
actions, each tagged with the key id. And a queryable per-key feed of what the
key did, which is what was missing: see **Per-key activity history** below.

### Sandbox, examples, versioned OpenAPI, error format, rate limits, pagination

**Mostly available.**

- **Sandbox:** a key with `communities:provision` creates its own community,
  owned by the developer, private by default. That *is* the isolated test
  tenant, no production data, no shared sandbox to contend over. A separate
  sandbox *host* is a Merrill decision (below).
- **OpenAPI:** [`openapi.yaml`](openapi.yaml), 34 paths, hand-written so it
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
not an enum we define, so read them from `/ten-codes` rather than hardcoding,
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

**NPC separation: available.** Records a key creates carry `source.keyId`, so a
key's output is findable and removable as a set, and an explicit
`synthetic: true` declaration keeps NPCs out of real rosters and real searches.
See **Synthetic NPC records** below for the declaration, the inheritance, the
limits question and both cleanup paths.

### Draft incident / arrest / citation reports

**Available.**

`POST /communities/{communityId}/reports/drafts`, scopes `reports:read` and
`reports:write`.

The constraint you named is the one we built to, and it is structural rather
than a rule we check. Drafts live in their own collection; nothing that reads a
filed report reads it; the only status a key can write is `draft`; and **there
is no endpoint on this API that files one**. A person with the community's own
permission opens the draft on the website and files it there. We also added a
test that fails if anyone ever adds a `reports:file`-shaped scope, so the
absence is deliberate rather than pending.

What it carries:

| | |
|---|---|
| Idempotency | on `(community, source, externalId)`, the same mechanism as a dispatch call. A retry updates; it never adds a second draft to somebody's queue |
| Kinds | `incident`, `arrest`, `citation`: one collection, because the review surface, the idempotency rule and the permission check are identical and only the fact sheet differs |
| Facts vs narrative | separate fields that never share one. `facts` is what the server observed; `narrative` is prose about it, and a narrative with no `generated` field is **recorded as generated**: the safer default for an API whose callers are automated |
| Evidence | there is no way to mark a narrative, or an attachment, as evidence. A narrative is an account of events; evidence is a thing that exists |
| Call link | by `callExternalId` (your id, the one you still have after a restart) or `callId` (ours) |
| Templates | name a `templateSlug` and the community's own form gates apply, role gates, per-department rank gates, and the department's enable/disable toggle, checked against the reporting officer with the same helper the website uses |
| Charges | you send violation **names**; the community's own penal code supplies the fine and the jail time. A name the community does not have is refused, so a key can never invent an amount, and a community that retunes its codes sees new drafts priced the new way without you changing anything |
| Who may write one | `reportingOfficer` is required, and it is **that player's** access that is checked: an approved, unbanned member who is in a department. The key's scope only decides whether it may propose at all |
| Frozen after review | once a person files or rejects it, a late retry gets `409 draft_already_reviewed` and their decision is left alone |
| Reconciliation | `GET …/reports/drafts/{externalId}` returns `status`, `reviewNotes`, `filedRecordId` and `filedRecordKind`, so you can tell a player what happened instead of guessing |
| Withdrawal | `DELETE …/reports/drafts/{externalId}` takes an unreviewed draft out of the queue. Withdrawn, not deleted, and never for one a person has acted on |

The review half is on our own first-party surface, gated on the community's
`manage records` permission and on the website's server-side gateway secret, so
no key can reach it:

* `GET  /api/v1/developer/community/{communityId}/report-drafts`: the queue
* `POST …/report-drafts/{draftId}/file`: files it. An `arrest` draft becomes an
  **ArrestReport in the same collection the CAD writes to**, so it appears in
  the arrest list, the court flow and the totals exactly like one typed by hand;
  an `incident` or `citation` draft becomes a FormSubmission against the named
  template, or the built-in Incident Report. Idempotent: a double-click returns
  the record that exists rather than filing a second one
* `POST …/report-drafts/{draftId}/reject`: notes required, and your integration
  can read them

A generated narrative keeps its label when it is filed, so whoever reads the
report in three months can still see a model wrote it.

### Warrants

**Available.** `warrants:read` and `warrants:write`.

* `GET /communities/{communityId}/warrants`: community-scoped, paginated,
  filterable by `status` and by `accused`, so "is there a warrant out for this
  person?" is one request rather than a scan.
* `GET /communities/{communityId}/warrants/{warrantId}`: the community is part
  of the lookup, so an id from elsewhere is a 404 and its existence is never
  confirmed.
* `POST /communities/{communityId}/warrants/requests`: requests one.

One thing to know, because it is deliberately *not* what the website does: **a
request through a key always lands `pending`**, whatever the community's
`warrantApprovalMode` says.

Auto-approve is the product default and most communities run it, so an officer
clicking "request warrant" in the CAD gets an approved warrant immediately.
That is a convenience for a person who is already in the CAD, has already
passed the officer check, and is making one decision they can see the result
of. A game server is none of those: it fires on a trigger, it retries, and
nobody is watching the moment it happens. A key that could trip auto-approve
could manufacture approved arrest warrants on a loop, and no community that
turned auto-approve on for its officers was agreeing to that.

So the request goes into the pending queue the community already has, a person
reviews it with the tools they already use, and you poll for the outcome.
`judgeNotes` comes back on a denial, which is the only thing that explains one
to your officer. The request is idempotent on `(community, source, externalId)`,
and a retry after a decision hands you the decision rather than raising a second
request.

### Synthetic NPC records

**Available.** Declared at `POST /players` with `"synthetic": true`.

| | |
|---|---|
| Declared, not inferred | the same key provisions accounts for real players arriving from the game, so "a key created it" is not the claim "this is not a person". You say so |
| Inherited | a record whose owner is synthetic is synthetic whether or not you repeat the flag, so you mark each NPC once rather than on four record types |
| Hidden by default | left out of the dev surface's civilian, vehicle and firearm searches, out of the roster, and out of the website's own name database (v1 and v2 civilian search). `includeSynthetic=true` widens a result to everything; there is no way to ask for NPCs alone |
| Record limits | they **count** against this key's daily provisioning cap, so a runaway loop is still bounded. They **do not count** against a player's own per-community character cap: an NPC is nobody's character, and counting it both ways would mean your scenery consuming your players' allowances |
| Claiming wins | if a person ever signs in and takes a provisioned account over, they are a player from then on and the flag is cleared on both the account and the identity row |
| Your own cleanup | `DELETE /communities/{communityId}/synthetic-records`. Synthetic only, this key only, this community only, and `confirm` must be the community id, so a cleanup aimed at a test community cannot run against a live one because a config value was stale. Narrow it with `source` and `externalId`; batches are bounded and `more` tells you to call again |
| The owner's cleanup | a community owner can remove everything a key wrote, from the community's settings on the website, with `scope: "synthetic"` or `"all"`. That path exists for an integration that has stopped answering, so it does not need the key's cooperation, and it clears any unreviewed drafts too, because a draft left in a reviewer's queue by a revoked integration is one somebody would file |

### Per-key activity history

**Available.** `GET /api/v1/dev/activity`, and in the developer console under
each key.

No scope required, deliberately: a key that is being refused has to be able to
find out why, and gating the diagnostic would withhold it from exactly the keys
that need it. Scoped to the calling key by construction.

| | |
|---|---|
| Request ids | every response from this API carries `X-Request-Id`, including the ones the middleware refuses, a 401 that cannot be traced is the one you most need to trace. Quote one in a ticket and we can find the row |
| Correlation ids | send `X-Correlation-Id` and we echo it back and record it beside ours. Never used to look anything up on our side: a value you control cannot be an identity, so it sits alongside our id rather than in place of it |
| What a row says | route (the template, not your path), community, method, status, a coarse outcome, our own `error` code, and on a `missing_scope` refusal **which scope was missing**: the single most common thing this feed exists to explain |
| What a row does not say | no request body, no response body, no header values, no key material, no claim code. A feed holding any of those would be a place to go looking for them |
| Retention | **30 days**, published in the docs, in the response, and on the console screen. Enforced by a TTL index rather than a job, because a window that depends on a cron is one that quietly stops being true the first time the cron is wedged |

### Incremental polling, and webhooks

**Both available, and this reverses our earlier answer on #277.**

The reasons for declining webhooks were real: outbound HTTP to
developer-controlled URLs is an SSRF surface, and delivery brings signing,
retries, replay protection and dead-letters with it. None of those arguments
goes away by shipping, so they are answered rather than avoided, see the table
below.

**The cursor first, because it is what makes polling cheap.**
`GET /api/v1/dev/communities/{id}/changes?since=<cursor>` returns only what has
changed. The cursor is an event id; the first call returns a position rather
than a week of history; `excludeSelf=true` leaves out your own writes, which is
the difference between an integration and a loop.

**The same events, pushed.** Register an endpoint per key in the console. The
delivery body carries the event in the same shape the cursor endpoint returns,
so moving from one to the other does not mean rewriting your handler, and you
can run both while you migrate, because the event id is stable and the
duplicate is recognisable.

Events emitted: `call.created`, `call.updated`, `call.closed`,
`call.assigned`, `unit.status_changed`, `record.created`,
`report_draft.filed`, `report_draft.rejected`, `warrant.requested`,
`warrant.reviewed`. Named after what happened in the product rather than after
the endpoint that caused it, **the website's own handlers emit too**, so a call
a dispatcher closed in the CAD reaches you like one you closed yourself. A feed
that only saw the API's own writes would be a feed of your own echo.

| concern | how it is answered |
|---|---|
| SSRF | https only; the resolved address is checked against every private, loopback, link-local, CGNAT and cloud-metadata range; **every** A record has to pass, not the first; redirects are refused outright, because a public URL that 302s to `169.254.169.254` is the whole trick; and the check runs again immediately before each request, because DNS is not ours |
| Signing | HMAC-SHA256 over `"<t>." + rawBody`, in `X-LPC-Signature: t=<unix>,v1=<hex>`. Per-endpoint secret, shown once, rotatable without losing the endpoint or its subscription |
| Replay | the timestamp is inside the signed material and the tolerance is published at **300 seconds**, so a captured delivery stops being replayable five minutes later |
| Retries | published and finite: 10s, 30s, 2m, 5m, 15m. Expressed as "how long since this endpoint last failed", so a dead endpoint costs one request per step however many events are queued |
| Giving up | 20 consecutive failures switches the endpoint off with a reason in the console. An endpoint that 500s forever is not a transient failure, and retrying it indefinitely spends our capacity on somebody else's outage |
| Dead-letters | **nothing is lost.** A failed delivery leaves the endpoint's cursor where it was, so the event is still readable from the polling endpoint for its 7 days. Webhooks are a transport over the event log, not a second source of truth, so a missed delivery degrades to the polling case rather than to a gap |
| Ordering | deliveries stop at the first failure rather than carrying on, because an integration that receives `call.closed` before `call.created` has to handle a case we can simply not create |
| Grants | re-read per delivery, not trusted from the subscription. A community owner withdrawing their grant stops deliveries about their community immediately, exactly as it stops requests |
| Debugging | `GET /developer/keys/{id}/webhooks/{id}/deliveries` is the attempt log, with our status and our error text, never the receiver's response body, which is their content |

Event retention is **7 days**, shorter than the activity feed on purpose: a
cursor staler than that means you should re-read current state rather than
replay a week, and saying so is better than letting you try.

### Report/evidence attachment metadata

**Available.** On a report draft and on a dispatch call.

Metadata and a link. **No binary upload, and that is the answer rather than a
first step**: accepting third-party binaries would mean scanning, storage,
quotas, a takedown path and a legal position on content we host, and none of it
is needed for a reviewer clicking through to the clip your server already
recorded. We never fetch the URL either, so it is a link a person opens rather
than a request we make.

**`kind` is never `evidence`**, and trying it returns an error that says why
rather than just listing the enum. A link we have not seen, on a server we do
not control, has not earned that standing in a system that produces jail time;
a reviewer can look at a clip and decide for themselves what it is. The same
applies to a generated narrative, it is labelled as generated on the review
screen and in the filed report, and there is no field that would call it
evidence.

Limits: `clip` / `screenshot` / `log` / `document` / `other`, 10 per record,
1024-character URL, http or https, no credentials in it, and a bounded declared
size so a typo cannot render "4 PB" on a reviewer's screen. Every attach and
detach is audited.

### Idempotent writes, predictable errors

**Available** throughout, on the same external-id mechanism: players (unique
index on the game identifier), dispatch calls, report drafts (unique index on
community + source + externalId), warrant requests, and attachments
(idempotent on the URL). A retry never produces a second anything.

---

## P1

| ask | status |
|---|---|
| BOLOs read/write | **Available** (create, list) |
| Warrants | **Available.** Community-scoped lookup, and a request flow that always lands pending for the community's own reviewer, see above for why a key does not get the auto-approve shortcut |
| Report attachment metadata and links | **Available.** A link plus its kind, label, content type and declared size, on a report draft and on a dispatch call: no binary upload, we never fetch the URL, 10 per record, every attach and detach audited, and `kind` is never `evidence` |
| EMS/fire types and statuses | **Partly available**: departments carry templates including EMS and fire, and ten-codes are per community. Incident/medical forms fall under the reports work |
| Scoped signed webhooks or streaming | **Available**, and this reverses our earlier answer. `GET …/changes?since=<cursor>` makes polling one request instead of a sweep, and signed webhooks deliver the same events: HMAC-SHA256 over a signed timestamp and the body, stable event ids, a published 300-second replay window, a published finite retry schedule, a rotatable signing secret, and per-key endpoint configuration in the console |
| Rank mapping that never grants permissions | **Already guaranteed.** A key **cannot assign roles at all**: sending `roleId` is a 403, because roles carry permissions. Departments and ranks can be set, and both are validated against the community. In-game promotion can move a rank; it can never grant a permission |

---

## Decisions for Merrill

None of the P0 asks conflict with the security rules, no money, auth, admin or
cross-community access is requested. These four are judgement calls:

1. **A shared sandbox host.** A developer can already create their own private
   test community on production, which is isolated and costs nothing. A
   separate sandbox *environment* (a public base URL on `police-cad-dev`) means
   another deploy target, another database and third-party traffic on
   infrastructure we use for our own testing. Recommend: no, start with
   per-developer test communities.
2. **Webhooks or streaming (P1).** ~~Recommend: polling first.~~ **Decided:
   both, and this reverses the earlier answer.** A documented incremental
   cursor lands first, because it is what makes polling cheap, and signed
   webhooks follow. The SSRF and operational concerns were real and are
   answered in the design rather than by declining: HMAC signatures, stable
   event ids, a timestamp with replay protection, documented retries, a
   rotatable signing secret, and endpoints configured per key in the console.
3. **Synthetic NPC data.** **Decided, and built.** Hidden by default,
   removable as a set, and the limits question split: they count against the
   key's daily provisioning cap (so a loop is bounded) but not against a
   player's own character cap (an NPC is nobody's character). See the section
   above.
4. **How much of the reports work to take on.** **Decided: all of it**, and it
   is built. Drafts are their own collection rather than an extension of the
   forms system, which is what makes "a key cannot file a report" structural
   instead of a check, but a draft still honours the community's form
   templates, their role and rank gates, and their per-department toggles.

---

## Recommended first PR, and the acceptance test

Their suggested end-to-end test is a good one, and most of it runs today.

| step | status |
|---|---|
| 1. Player marks 10-8 | ✅ `PUT /units/status/bulk` |
| 2. SixPD creates one CAD call with a stable external id | ✅ the dispatch PR |
| 3. Player accepts; unit status updates | ✅ status + `assignedUnits` |
| 4. SixPD prepares a reviewable draft report linked to the call | ✅ `POST /reports/drafts`, linked by `callExternalId` |
| 5. Player submits; refresh/retry does not duplicate; outage does not stop play | ✅ for calls, status and reports. The player submits on the website, the API has no verb that files a report |

**So: steps 1 through 3 and 5 can be built against today.** The recommended
first PR on their side is the adapter for exactly that, which proves the key,
the quotas, the idempotency and the reconciliation path without waiting on us.

**The recommended first PR on our side** is the draft-report surface, scoped to
arrest reports and citations over the existing forms system, draft-only, with
external ids for idempotency. That unblocks step 4 and completes the loop.
