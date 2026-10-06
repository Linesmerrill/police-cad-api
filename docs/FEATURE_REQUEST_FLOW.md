# Feature request flow

How a request on [linespolice-cad.com/feature-requests](https://www.linespolice-cad.com/feature-requests) moves from idea to shipped, and how to move it.

## Lifecycle

```
open ──► planned ──► beta_testing ──► released
  │         │              │
  └─────────┴──────────────┴────────► declined
```

| Status | Label on the site | Meaning | When to use it |
|---|---|---|---|
| `open` | Open | New, not yet looked at or not scheduled. | Default for every new request. |
| `planned` | Planned | We've picked it up and are building it. | As soon as work starts. Post the "picked up" comment first. |
| `beta_testing` | In Beta Testing | Built and live for testers (beta toggle, beta build, or a soft launch), not yet announced. | Only if it goes through beta. Otherwise skip from `planned` to `released`. |
| `released` | Released | Live in production for everyone. Shows under **Recently Shipped**. | When it's deployed (web and API live; mobile builds approved in the stores, or at least in review). |
| `declined` | Declined | We won't build it. | With a comment explaining why. |
| `merged` | Merged | A duplicate folded into another request. | Set by **Merge**, not by the status menu. |

Every step except `merged` gets a comment so voters know what happened.

**Order matters: comment first, then change the status.** Comments are blocked once a request is `released`, `declined` or `merged`. If you release first, you can't post the "what shipped" comment without moving it back.

## Who can do it

Any LPC admin: an account in the admin console's `admin_users` whose linked LPC account (or email) matches the account you're logged in with on the main site.

One-time setup:
1. Admin console, **Profile**, **Link LPC account**. Link the LPC account you use on linespolice-cad.com.
2. Log in to linespolice-cad.com with that LPC account.

Your comments then show an **Admin** badge automatically. The status menu only appears for admins.

## Doing it in the website (the normal way)

1. Open the request: `https://www.linespolice-cad.com/feature-requests/<id>`.
2. **Comment:** use the normal comment box at the bottom. Admin comments are badged automatically.
3. **Status:** click the status badge at the top of the request (for example "Open"). A menu lists Open, Planned, In Beta Testing, Released and Declined. Pick one; it saves immediately and updates live for everyone viewing.
4. **Merge a duplicate:** use the Merge action on the request you want to keep.

## Doing it through the API

The website's own routes are the supported path. They use your logged-in session, so they need the browser cookie of an admin logged in on linespolice-cad.com:

| Action | Request (on `https://www.linespolice-cad.com`) | Body |
|---|---|---|
| Comment | `POST /api/v1/feature-requests/<id>/comments` | `{"content": "..."}` |
| Change status | `PUT /api/v1/feature-requests/<id>/status` | `{"status": "planned"}` (one of `open`, `planned`, `beta_testing`, `released`, `declined`) |
| Merge | `POST /api/v1/feature-requests/<targetId>/merge` (the request to keep) | `{"sourceId": "<duplicate id>"}` |

These proxy to the Go API (`api/handlers/featurerequest.go`: `AddCommentHandler`, `UpdateStatusHandler`, `MergeHandler`) with the logged-in account's identity, and broadcast the change to anyone viewing the page.

Don't call the Go API's feature-request write endpoints directly with a hand-built `?userId=`. Identity there comes from the website session; going around it isn't a supported admin path.

## Recently Shipped

The Recently Shipped carousel shows the 8 most recently released requests, by **release date**. The release date (`releasedAt`) is stamped the first time a request becomes `released`. Setting `released` again leaves it alone, and moving off `released` clears it.

Requests released before `releasedAt` existed (October 2026) fall back to their last-updated date.

## Comment templates

Keep comments short and specific. No emojis or em dashes. Sign as the team, not a person.

**Picked up** (then set `planned`):
> We're working on this now. Plan: <one or two lines on what we're building>. We'll update this request when it's out.

**Beta** (then set `beta_testing`):
> This is now in beta: <how to get it: setting / beta toggle / app build>. Tell us here if anything's off before it rolls out to everyone.

**Released** (post this, then set `released`):
> Shipped on <Mon D, YYYY> (website, and mobile <x.y.z>). What's new: <bullets of what users can do now>. Where to find it: <screen / setting>.

**Declined** (post this, then set `declined`):
> We're not going to build this. <The reason in one or two sentences, and an alternative if there is one.>

For a duplicate, merge it into the original rather than declining it.

## Checklist per request

- [ ] Picked up: comment, then `planned`
- [ ] (If beta) Beta: comment, then `beta_testing`
- [ ] Live in production (web + API deployed; mobile version noted)
- [ ] Released: comment with what shipped + version/date, then `released`
- [ ] Or declined: comment with the reason, then `declined`
