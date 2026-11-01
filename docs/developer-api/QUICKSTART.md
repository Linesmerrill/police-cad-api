# LPC Developer API quickstart

From nothing to a player holding a character they made in your game, in five
calls.

- Base URL: `https://api.linespolice-cad.com/api/v1/dev`

> **Base URL.** Defined once, in `models.DeveloperAPIBaseURL`, and overridable
> with `DEVELOPER_API_PUBLIC_BASE_URL`. Every example here, the OpenAPI
> `servers` list and the console all read that one value, so the host cannot be
> right in one place and stale in another, which is exactly how this hostname
> came to be documented before it had a DNS record.

- Auth: `Authorization: Bearer lpc_live_…` on every request
- Everything is JSON

Get a key from [your developer page](https://www.linespolice-cad.com/developer).
You see the key once, at creation. We store only a hash of it, so if you lose it
you rotate it rather than recover it.

---

## 0. Check your key

```bash
curl https://api.linespolice-cad.com/api/v1/dev/whoami \
  -H "Authorization: Bearer $LPC_KEY"
```

```json
{
  "key": { "id": "6a3b…", "name": "My FiveM server", "scopes": ["communities:provision", "players:provision", "civilians:write"] },
  "plan": "free",
  "limits": { "requestsPerMinute": 60, "requestsPerDay": 10000, "accountsPerKeyDay": 25 },
  "communities": [],
  "usage": { "requestsToday": 1, "accountsCreatedToday": 0 }
}
```

Start here whenever something is not working. It answers "is my key live, what
can it do, and which communities can it see" in one call, and it needs no
scopes, so it works even when the thing you are debugging is a missing one.

## 1. Create a community

Needs `communities:provision`. The community is owned by **your** LPC account
and counts against your plan, exactly like one you made on the website.

```bash
curl -X POST https://api.linespolice-cad.com/api/v1/dev/communities \
  -H "Authorization: Bearer $LPC_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"Grapeseed RP","description":"Serious RP on FiveM"}'
```

```json
{ "community": { "id": "6b1c…", "name": "Grapeseed RP", "visibility": "private" } }
```

Keep that id. It goes in every path below.

Communities start **private**. Make it public from the community's settings on
the website when you are ready for it to appear in the public listing.

If you would rather use a community that already exists, skip this: ask its
owner to approve your key from **Community settings → API keys**, and it will
show up in `whoami`.

## 2. Upsert a player

Needs `players:provision`. You name players by their **game identifier**: a
FiveM license or a Discord ID, and never by an LPC user id or an email.

```bash
curl -X POST https://api.linespolice-cad.com/api/v1/dev/players \
  -H "Authorization: Bearer $LPC_KEY" \
  -H "Content-Type: application/json" \
  -d '{"provider":"fivem","identifier":"license:1a2b3c4d5e6f7a8b","username":"Dave"}'
```

```json
{ "player": { "playerRef": "fivem:1a2b3c4d5e6f7a8b", "playerId": "6b2d…", "claimed": false, "created": true } }
```

**This is idempotent.** Call it on every connect. The same identifier always
returns the same account, and calling it twice does not make two players, even
if two of your servers call it at the same instant.

`license:` and the bare value are the same player, and so are different cases,
so you do not have to normalize before sending.

The account we create **cannot be logged into**. It has no email and no
password, and that is deliberate: an email you sent us would be an email you
control, so taking one would hand you a way into your players' accounts. The
player takes ownership themselves in step 4.

## 3. Create their character

Needs `civilians:write`. Pass the player by reference, not by id.

```bash
curl -X POST https://api.linespolice-cad.com/api/v1/dev/communities/6b1c…/civilians \
  -H "Authorization: Bearer $LPC_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "player": "fivem:1a2b3c4d5e6f7a8b",
    "firstName": "John", "lastName": "Smith",
    "birthday": "1998-04-10", "gender": "Male",
    "address": "Grapeseed Farmhouse"
  }'
```

```json
{ "civilian": { "id": "6b3e…", "name": "John Smith", "approvalStatus": "" }, "pendingApproval": false }
```

Vehicles, firearms and licenses hang off that character:

```bash
curl -X POST .../communities/6b1c…/vehicles -d '{
  "player":"fivem:1a2b3c4d5e6f7a8b", "civilianId":"6b3e…",
  "plate":"49KLM221", "make":"Declasse", "model":"Sabre", "year":"2004", "color":"Black"
}'
```

If `pendingApproval` is true, the community has its approval system on and a
person has to approve the character. Your key does not get to skip that, and
nor does the community's per-player character cap: a key lets a player act, it
does not override the settings the owner chose.

## 4. Show the player their claim code

Needs `players:provision`. This is how the account becomes theirs.

```bash
curl -X POST https://api.linespolice-cad.com/api/v1/dev/players/fivem/1a2b3c4d5e6f7a8b/claim-code \
  -H "Authorization: Bearer $LPC_KEY"
```

```json
{
  "code": "K7M2-QX9P-4T",
  "expiresAt": "2026-10-31T18:30:00Z",
  "url": "https://www.linespolice-cad.com/developer/claim"
}
```

Show the code and the URL in game. The player opens the URL, signs in or signs
up, enters the code, and everything you made for them is on their account. They
can then see it on the website and in the mobile app.

- Codes last **30 minutes**, work **once**, and asking for a new one kills the
  old one.
- Players who already have an LPC account sign in first and then enter the
  code; their characters move onto the account they already had.
- Players who have linked Discord to LPC, and whose player you provisioned by
  Discord ID, can claim with no code at all.

## 5. Go live

Set a unit's status, raise calls, run lookups. See the
[OpenAPI spec](openapi.yaml) for every endpoint.

---

## Rate limits, and the one mistake to avoid

| | burst | daily |
|---|---|---|
| Free | 60 / min | 10,000 / day |
| Pro | 600 / min | 250,000 / day |

Every response tells you where you stand:

```
X-RateLimit-Scope: minute
X-RateLimit-Limit: 60
X-RateLimit-Remaining: 41
X-RateLimit-Reset: 1762041600
```

A 429 adds `Retry-After` in seconds and a body saying which limit you hit.

**Do not loop over your players.** A server ticking once a second with 64
players is 64 requests a second, 3,840 a minute, which is over even the paid
limit. Send one **bulk** call per tick instead:

```bash
curl -X PUT .../communities/6b1c…/units/status/bulk -d '{
  "units": [
    {"player":"fivem:1a2b…","tenCodeId":"6b4f…","location":"Grapeseed"},
    {"player":"fivem:9z8y…","tenCodeId":"6b50…","location":"Sandy Shores"}
  ]
}'
```

Up to 100 units per call, which is 60 requests a minute for a full server and
comfortably inside the free limit. The call is **partial**: each unit reports
its own result, so one unknown player does not discard the other sixty-three.

```json
{ "applied": 63, "failed": 1, "units": [ {"player":"fivem:1a2b…","ok":true}, … ] }
```

Get the ten-code ids from `GET /communities/{id}/ten-codes` rather than
hardcoding them. Owners rename and add codes, and hardcoded ids break the day
they do.

---

## A working FiveM resource

Lua, server side. Drop this in a resource and it covers connect, character
creation and the status tick.

```lua
-- server.lua
local LPC_BASE      = "https://api.linespolice-cad.com/api/v1/dev"
local LPC_KEY       = GetConvar("lpc_api_key", "")          -- set in server.cfg
local LPC_COMMUNITY = GetConvar("lpc_community_id", "")

-- Small wrapper so every call carries the key and one place parses the body.
local function lpc(method, path, body, cb)
  PerformHttpRequest(LPC_BASE .. path, function(status, text, _)
    local ok, decoded = pcall(json.decode, text or "")
    if not ok then decoded = nil end

    if status == 429 then
      -- Back off rather than hammering. The body says which limit and when it
      -- resets; honour it instead of retrying in a tight loop.
      print(("[lpc] rate limited: %s"):format(text or ""))
    elseif status >= 400 then
      print(("[lpc] %s %s -> %d %s"):format(method, path, status, text or ""))
    end

    if cb then cb(status, decoded) end
  end, method, body and json.encode(body) or "", {
    ["Content-Type"]  = "application/json",
    ["Authorization"] = "Bearer " .. LPC_KEY,
  })
end

-- FiveM gives several identifiers per player; the license is the stable one.
local function licenseOf(src)
  for _, id in ipairs(GetPlayerIdentifiers(src)) do
    if id:sub(1, 8) == "license:" then return id end
  end
  return nil
end

local players = {}   -- src -> { ref = "fivem:…", claimed = bool }

AddEventHandler("playerJoining", function()
  local src     = source
  local license = licenseOf(src)
  if not license then return end

  -- Idempotent: safe on every single connect.
  lpc("POST", "/players", {
    provider   = "fivem",
    identifier = license,
    username   = GetPlayerName(src),
  }, function(status, body)
    if status ~= 200 and status ~= 201 then return end

    players[src] = { ref = body.player.playerRef, claimed = body.player.claimed }

    -- A player who has not claimed their account cannot see their characters
    -- on the website yet, so tell them how.
    if not body.player.claimed then
      lpc("POST", ("/players/fivem/%s/claim-code"):format(license:sub(9)), nil,
        function(codeStatus, codeBody)
          if codeStatus ~= 201 then return end
          TriggerClientEvent("chat:addMessage", src, {
            color = { 255, 200, 0 },
            args  = { "LPC", ("Link your characters at %s with code %s (30 minutes)")
                              :format(codeBody.url, codeBody.code) },
          })
        end)
    end
  end)
end)

AddEventHandler("playerDropped", function() players[source] = nil end)

RegisterCommand("createchar", function(src, args)
  local player = players[src]
  if not player then return end
  if #args < 2 then
    TriggerClientEvent("chat:addMessage", src, { args = { "LPC", "/createchar First Last" } })
    return
  end

  lpc("POST", ("/communities/%s/civilians"):format(LPC_COMMUNITY), {
    player    = player.ref,
    firstName = args[1],
    lastName  = args[2],
  }, function(status, body)
    if status ~= 201 then return end
    local message = ("Created %s"):format(body.civilian.name)
    if body.pendingApproval then
      message = message .. " (waiting for approval)"
    end
    TriggerClientEvent("chat:addMessage", src, { args = { "LPC", message } })
  end)
end, false)

-- One bulk call per tick, never one call per player.
local ON_DUTY_TEN_CODE = nil   -- fill from GET /communities/{id}/ten-codes

CreateThread(function()
  while true do
    Wait(15000)   -- 4 a minute: well inside the free limit, plenty for a board

    local units = {}
    for src, player in pairs(players) do
      local ped = GetPlayerPed(src)
      if ped and ped ~= 0 then
        local zone = GetNameOfZone(table.unpack(GetEntityCoords(ped)))
        units[#units + 1] = {
          player    = player.ref,
          tenCodeId = ON_DUTY_TEN_CODE,
          location  = zone,
        }
      end
    end

    if #units > 0 then
      -- Chunk at 100, the per-call maximum.
      for i = 1, #units, 100 do
        local chunk = {}
        for j = i, math.min(i + 99, #units) do chunk[#chunk + 1] = units[j] end
        lpc("PUT", ("/communities/%s/units/status/bulk"):format(LPC_COMMUNITY),
            { units = chunk })
      end
    end
  end
end)
```

```cfg
# server.cfg
set lpc_api_key "lpc_live_your_key_here"
set lpc_community_id "6b1c…"
```

Use `set` rather than `setr` for the key: `setr` replicates a convar to every
connected client, which would hand your key to anyone who joins.

---

## 6. Write a draft report for a person to file

A game server can see what happened. It cannot decide whether what happened was
an arrest, that has jail time, a fine and a court case behind it, and it
belongs to a person in the community. So you write a **draft**, and someone with
the community's permission reads it and files it on the website.

```bash
curl -sX POST "$BASE/communities/$COMMUNITY/reports/drafts" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{
    "externalId": "sixpd-incident-42",
    "kind": "arrest",
    "callExternalId": "sixpd-incident-42",
    "reportingOfficer": "fivem:1a2b3c4d5e6f7a8b",
    "subject": { "player": "fivem:9f8e7d6c5b4a3210" },
    "facts": {
      "occurredAt": "2026-11-01T21:14:00Z",
      "location": "Route 68 northbound",
      "summary": "Vehicle failed to stop, pursuit ended at the rail crossing.",
      "forceUsed": false
    },
    "narrative": {
      "text": "The driver accelerated away from the stop and ...",
      "generated": true,
      "generatedBy": "sixpd-llm"
    },
    "charges": ["Reckless Driving"]
  }'
```

Four things worth knowing before you build against it:

- **Nothing here files a report.** A `201` means the draft is in the queue.
  `filed` is `false` and stays false until a person acts; poll
  `GET /communities/$COMMUNITY/reports/drafts/sixpd-incident-42` to find out
  what they decided, and show your officer the `reviewNotes` if it was
  rejected.
- **`reportingOfficer` is what gets checked.** The key's scope decides whether
  it may propose a draft at all. Whether *this* draft is allowed is decided by
  that player's access: an approved, unbanned member who is in a department. A
  key never grants more than the player it reports for.
- **Facts and narrative stay apart.** `facts` is what your server observed;
  `narrative` is prose about it. **A narrative with no `generated` field is
  recorded as generated**: send `"generated": false` only when a person typed
  the text. There is no way to mark a narrative as evidence, and there will not
  be.
- **You name charges; the community prices them.** Send the violation names
  from the community's own penal code and we fill in the fine and the jail time
  from it. A name the community does not have is refused, so you cannot invent
  an amount, and a community that retunes its codes sees new drafts priced the
  new way without you changing anything.

Retries are safe: the same `externalId` updates the draft rather than adding a
second one to somebody's queue. Once a person has filed or rejected it, a late
retry gets `409 draft_already_reviewed` and their decision is left alone.

## 7. Ask for a warrant

```bash
curl -sX POST "$BASE/communities/$COMMUNITY/warrants/requests" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{
    "externalId": "sixpd-warrant-7",
    "warrantType": "arrest",
    "requestingOfficer": "fivem:1a2b3c4d5e6f7a8b",
    "accused": "fivem:9f8e7d6c5b4a3210",
    "probableCause": "Fled a lawful traffic stop and struck a parked vehicle.",
    "charges": ["Reckless Driving"]
  }'
```

**A request always lands `pending`.** That is deliberately not what the website
does: a community can set its warrant mode to auto-approve, and most have, so
an officer clicking the button in the CAD gets an approved warrant at once.
That is a convenience for a person who is already in the CAD and can see the
result. A game server fires on a trigger, retries, and nobody is watching, so
a key gets the pending queue the community already has, a person reviews it
with the tools they already use, and you read the outcome back.

Poll `GET /communities/$COMMUNITY/warrants?accused=fivem:9f8e…&status=approved`
to answer "is there a warrant out for this person?" in one request. `approved`
and `awaitingReview` are spelled out on each warrant so you do not have to
decide whether `executed` counts.

## 8. Populate a city without burying the players in it

Mark a simulated character once, when you provision it:

```bash
curl -sX POST "$BASE/players" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"provider":"fivem","identifier":"npc-0001-hex","username":"Store Clerk","synthetic":true}'
```

Everything that character owns inherits the mark, so you do not have to repeat
it on each civilian, vehicle and firearm. Synthetic records are left out of the
searches and rosters players see, including the website's own name database,
unless a caller passes `includeSynthetic=true`.

They count against your key's daily provisioning cap, so a runaway loop is
still bounded. They do not count against a player's own per-community character
cap: an NPC is nobody's character, and counting it both ways would mean your
scenery consuming your players' allowances.

When a test run is done, clean up after yourself:

```bash
curl -sX DELETE "$BASE/communities/$COMMUNITY/synthetic-records" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"confirm":"'"$COMMUNITY"'","source":"SixPD"}'
```

`confirm` has to be the community id. This deletes rows, so it will not run
against an id you did not state, which is what stops a cleanup aimed at your
test community running against a live one because a config value was stale. It
only ever touches synthetic records your own key created, in the community in
the path. When `more` comes back true, call it again.

## 9. Stop sweeping: ask what changed

```bash
# First call: no cursor. You get a position, not a week of history.
CURSOR=$(curl -s "$BASE/communities/$COMMUNITY/changes" \
  -H "Authorization: Bearer $KEY" | jq -r .cursor)

# Then poll with it. Each response hands you the next one.
curl -s "$BASE/communities/$COMMUNITY/changes?since=$CURSOR&excludeSelf=true" \
  -H "Authorization: Bearer $KEY"
```

This is the difference between one request and a scan. Without it, finding out
whether a call was closed means re-reading every call, which on a busy
community is a sweep per tick.

Two things to get right:

- **Pass `excludeSelf=true`** unless you want your own writes back. Close a
  call, receive your own `call.closed`, close it again: that is the loop this
  parameter exists to prevent.
- **Keep the cursor.** Events are kept 7 days. A cursor staler than that means
  you should re-read current state rather than replay a week — so persist it
  somewhere that survives a restart, and treat a long outage as a resync rather
  than a catch-up.

`hasMore` tells you to call again. There is no total, because you cannot act on
one.

## 10. Or have them pushed to you

Register a webhook in [the console](https://www.linespolice-cad.com/developer).
You get a signing secret once, and deliveries carry the **same event shape**
the cursor endpoint returns — so moving from polling to webhooks does not mean
rewriting your handler, and you can run both while you migrate because the
event id is stable and the duplicate is recognisable.

Verify every delivery. In Node:

```js
const crypto = require('crypto');

function verify(rawBody, header, secret) {
  const parts = Object.fromEntries(
    header.split(',').map((p) => p.split('='))
  );
  const expected = crypto
    .createHmac('sha256', secret)
    .update(`${parts.t}.${rawBody}`)
    .digest('hex');

  // Constant time, or the comparison itself leaks the signature.
  const ok = crypto.timingSafeEqual(
    Buffer.from(expected, 'hex'),
    Buffer.from(parts.v1, 'hex')
  );
  // And check the age. This is the step that stops a captured delivery being
  // replayable tomorrow; without it the signature alone proves nothing about
  // when the request was made.
  const fresh = Math.abs(Date.now() / 1000 - Number(parts.t)) < 300;
  return ok && fresh;
}
```

Use the **raw body**, before any JSON parsing. A re-serialised body will not
match, and that is the single most common reason a first webhook integration
rejects everything.

What else arrives with it:

| header | what it is |
|---|---|
| `X-LPC-Signature` | `t=<unix>,v1=<hex>` — verify as above |
| `X-LPC-Event-Id` | stable across retries, so you can recognise a duplicate |
| `X-LPC-Event-Type` | the same `type` as in the body |
| `X-LPC-Delivery-Id` | this attempt |
| `X-LPC-Attempt` | which attempt this is |

**Answer `2xx` quickly and do your work afterwards.** You have 10 seconds. A
non-2xx is retried after 10s, 30s, 2m, 5m and 15m; an endpoint that fails 20
deliveries in a row is switched off, with the reason in the console.

Nothing is lost when a delivery fails: the event is still readable from the
cursor endpoint for its 7 days, which is why webhooks are a transport over the
event log rather than a second source of truth.

A few rules on the endpoint itself, so there are no surprises when you save it:

- **https only.** An http endpoint would ship the signature and the body in
  clear.
- **A public host.** We refuse loopback, private ranges, link-local and the
  cloud metadata address, checked again immediately before every request.
- **No redirects.** We will not follow one. If your URL needs to change, change
  it in the console.
- **No credentials in the URL.** The signature is how you authenticate us.

Rotating the secret keeps the endpoint and its subscription, so it is not an
outage — but the next delivery is signed with the new secret, so update your
receiver first.

## 11. When something is not working

Every response carries `X-Request-Id`. Send `X-Correlation-Id` with your own id
and we echo it back and record it beside ours — letters, digits and `. _ : -`,
up to 128 characters, since it is the one value we hand straight back.

```bash
curl -s "$BASE/activity?outcome=denied" -H "Authorization: Bearer $KEY"
```

Needs no scope — a key that is being refused has to be able to find out why.
Each row says which route, which community, what we answered, and on a
`missing_scope` refusal **which scope was missing**, which is the thing a bare
403 in your own logs does not tell you.

Rows are kept 30 days. They hold no request body, no response body and no
header values, so if you need the contents of a call you made, log it your end.

Quote a request id in a ticket and we can find exactly that request.

## Errors

Every error has a stable `error` code and a message written for a person.

| status | code | what to do |
|---|---|---|
| 401 | `invalid_key` | the key is wrong, revoked, or you sent it in the wrong header, it goes in `Authorization: Bearer`, not `X-API-Key` |
| 403 | `missing_scope` | the response names the scope; add it to the key |
| 403 | `community_not_granted` | the key is not approved for that community |
| 403 | `community_pending_deletion` | the owner deleted it; it is read-only for 30 days |
| 403 | `acting_player_lacks_permission` | the player you are acting for cannot do that. A key never grants more than the player it acts for |
| 404 | `player_not_found` | upsert the player first |
| 409 | `already_claimed` | the player owns their account; they sign in normally |
| 422 | `ten_code_not_found` | get ids from `/ten-codes` rather than hardcoding |
| 400 | `charge_not_in_penal_code` | read the community's penal code and send the names it lists |
| 403 | `reporting_officer_has_no_department` | put the officer in a department; they cannot open a report form either |
| 403 | `template_not_permitted` | the community's form gates exclude that officer |
| 409 | `draft_already_reviewed` | a person has filed or rejected it. Write a new draft with a new `externalId` |
| 400 | `confirm_required` | a cleanup needs `confirm` set to the community id |
| 400 | `invalid_cursor` | `since` must be a cursor from a previous response |
| 400 | `invalid_event_type` | read the type list rather than guessing at it |
| 400 | `invalid_attachment` | an unknown kind, no url, credentials in the url — or `kind: "evidence"`, which does not exist |
| 409 | `attachment_limit_reached` | 10 per record |
| 429 | `rate_limited` | honour `Retry-After` |
| 429 | `provision_cap_reached` | the daily account or community cap; resets at midnight UTC |

## Things a key can never do

Not a matter of scopes, these are not on the surface at all:

- change a password, an email, or anything else about a claimed account
- touch money: wallets, balances, transfers, economy payouts
- billing, subscriptions or boosts
- community settings, roles, permissions or deletion
- assign a role to a member (roles carry permissions; departments and ranks are
  fine)
- read any community your key is not tied to
- **file a report.** A draft is a proposal; the verb that turns one into an
  arrest, citation or incident record is on the website, behind the community's
  own permission. There is no `reports:file` scope and there will not be one
- **issue a warrant.** A request lands pending for the community's own
  reviewer, whatever its approval mode says

## Getting help

Open a ticket in [our Discord](https://discord.gg/Y9ytW2ZMp4). Include your
key's **display prefix** (`lpc_live_a1b2c3d4`), never the key itself, and the
`error` code you got.
