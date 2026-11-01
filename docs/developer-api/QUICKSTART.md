# LPC Developer API quickstart

From nothing to a player holding a character they made in your game, in five
calls.

- Base URL: `https://api.linespolice-cad.com/api/v1/dev`
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

Needs `players:provision`. You name players by their **game identifier** — a
FiveM license or a Discord ID — and never by an LPC user id or an email.

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
returns the same account, and calling it twice does not make two players — even
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
players is 64 requests a second — 3,840 a minute — which is over even the paid
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

## Errors

Every error has a stable `error` code and a message written for a person.

| status | code | what to do |
|---|---|---|
| 401 | `invalid_key` | the key is wrong, revoked, or you sent it in the wrong header — it goes in `Authorization: Bearer`, not `X-API-Key` |
| 403 | `missing_scope` | the response names the scope; add it to the key |
| 403 | `community_not_granted` | the key is not approved for that community |
| 403 | `community_pending_deletion` | the owner deleted it; it is read-only for 30 days |
| 403 | `acting_player_lacks_permission` | the player you are acting for cannot do that. A key never grants more than the player it acts for |
| 404 | `player_not_found` | upsert the player first |
| 409 | `already_claimed` | the player owns their account; they sign in normally |
| 422 | `ten_code_not_found` | get ids from `/ten-codes` rather than hardcoding |
| 429 | `rate_limited` | honour `Retry-After` |
| 429 | `provision_cap_reached` | the daily account or community cap; resets at midnight UTC |

## Things a key can never do

Not a matter of scopes — these are not on the surface at all:

- change a password, an email, or anything else about a claimed account
- touch money: wallets, balances, transfers, economy payouts
- billing, subscriptions or boosts
- community settings, roles, permissions or deletion
- assign a role to a member (roles carry permissions; departments and ranks are
  fine)
- read any community your key is not tied to

## Getting help

Open a ticket in [our Discord](https://discord.gg/Y9ytW2ZMp4). Include your
key's **display prefix** (`lpc_live_a1b2c3d4`) — never the key itself — and the
`error` code you got.
