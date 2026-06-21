# Deployment Guide

## Critical: Procfile Requirements

⚠️ **IMPORTANT**: The `Procfile` is **REQUIRED** for Heroku deployment. Without it, the application will fail to start and cause a complete outage.

### Procfile Requirements

1. **Must exist**: The `Procfile` file must be present in the root directory
2. **Must contain web process**: Must have a `web:` process type defined
3. **Must be committed**: The Procfile must be committed to git and pushed to Heroku

### Current Procfile

```
web: bin/police-cad-api
```

### Validation

Before pushing to Heroku, always run:

```bash
make check-procfile
```

This will verify:
- ✅ Procfile exists
- ✅ Contains `web:` process type
- ✅ Points to correct binary: `bin/police-cad-api`

### Pre-Push Checklist

Before pushing to Heroku, run:

```bash
make pre-push
```

This runs:
1. Procfile validation
2. Mock generation
3. Tests

### CI/CD Protection

The GitHub Actions workflow automatically validates the Procfile on every push and pull request. If the Procfile is missing or invalid, the CI will fail and prevent merging.

### What Happens If Procfile Is Missing?

- ❌ Heroku cannot determine how to start the application
- ❌ No `web` process type available
- ❌ Error: `H14 - No web processes running`
- ❌ **Complete application outage**

### Emergency Recovery

If the Procfile is accidentally deleted or modified incorrectly:

1. **Immediately restore the Procfile**:
   ```bash
   echo "web: bin/police-cad-api" > Procfile
   ```

2. **Commit and push**:
   ```bash
   git add Procfile
   git commit -m "Restore Procfile"
   git push heroku main
   ```

3. **Scale web dyno**:
   ```bash
   heroku ps:scale web=1 --app police-cad-app-api
   ```

### Preventing Future Issues

1. ✅ Procfile is tracked in git (not in .gitignore)
2. ✅ CI/CD validates Procfile on every push
3. ✅ Makefile has `check-procfile` target
4. ✅ Pre-push hook validates Procfile
5. ✅ This documentation exists

### Never Delete or Modify Procfile Without:

- Understanding the consequences
- Having a backup
- Testing locally first
- Following this deployment guide

## API Access Lockdown (Gateway Key)

The API is restricted to our own website and mobile app via a shared secret
checked by the `ApiKeyGateway` middleware (the outermost handler in `main.go`).

### Config var

| Var | Where | Purpose |
| --- | --- | --- |
| `API_GATEWAY_KEY` | API Heroku app | Shared secret. **Empty = gateway disabled (fail-open).** |

The website backend must send the same value in the `X-API-Key` header (its own
`POLICE_CAD_API_KEY` config var on the police-cad app).

### How requests are allowed

When `API_GATEWAY_KEY` is set, a request is allowed if **any** of these hold:

1. It's a CORS preflight (`OPTIONS`) or a health check (`/health*`).
2. It sends a matching `X-API-Key` header (website backend → API).
3. Its `Origin`/`Referer` is one of our web origins (browser traffic from the
   website — so the secret never ships to the browser).
4. It does **not** look like a browser or a generic scripting tool — this is the
   temporary **mobile-app exemption** (the published app sends no secret and
   can't be updated instantly). Browsers (`Mozilla`) and common tools
   (curl/wget/python/postman/etc.) without the secret get a `403`.

> ⚠️ This is the "quick and dirty" part-1 lockdown. The Origin and User-Agent
> checks are spoofable; the mobile path is intentionally left open. Part 2 is
> proper per-client authentication.

### Rolling it out safely

1. Generate a secret, e.g. `openssl rand -hex 32`.
2. Set it on the **website** first so its calls start carrying the header:
   ```bash
   heroku config:set POLICE_CAD_API_KEY=<secret> --app <police-cad web app>
   ```
3. Then enable enforcement on the **API**:
   ```bash
   heroku config:set API_GATEWAY_KEY=<secret> --app police-cad-app-api
   ```
4. To disable instantly (rollback), unset the API var:
   ```bash
   heroku config:unset API_GATEWAY_KEY --app police-cad-app-api
   ```

## Write Authentication (block anonymous mutations)

The gateway controls *who can reach* the API; it cannot stop someone who rides
our own website origin (or spoofs it) from calling a mutating endpoint with a
victim's `userId`. Many handlers historically performed **no authorization** and
trusted a `userId` in the request. `RequireWriteAuth` closes the anonymous case:
every mutating request (`POST`/`PUT`/`PATCH`/`DELETE`) must be authenticated.

A write is allowed when it:

- targets a public endpoint (signup / login / email verification — see
  `publicWritePaths` in `api/write_auth.go`);
- presents a valid `X-API-Key` (the website backend's server-to-server calls);
- originates from one of our own web origins via `Origin`/`Referer` (browser
  writes from the website — its browser JS has no API token, so we trust the
  origin, same as the read gateway); or
- carries a valid bearer token (mobile calls).

Otherwise it gets a `401`. Reads are never affected.

> Like the gateway, the Origin/Referer trust is spoofable and does NOT stop a
> user acting from our own site (e.g. via devtools). It blocks random/tooling
> writes (curl, python, cross-origin). Stopping a logged-in user from mutating
> data they don't own requires per-endpoint ownership checks — the remaining
> part-2 work.

### Persistent token store (prerequisite)

Bearer tokens are now stored in MongoDB (collection `auth_tokens`) instead of an
in-memory cache, so they **survive restarts and are shared across dynos**. Without
this, enforcing token validity would 401 every active user after each deploy.

| Var | Where | Purpose |
| --- | --- | --- |
| `ENFORCE_WRITE_AUTH` | API | `true` enables write enforcement. **Anything else = disabled (fail-open).** |
| `AUTH_TOKEN_TTL_HOURS` | API (optional) | Token lifetime; default `720` (30 days). A TTL index purges expired tokens. |

### Rolling it out safely

Do this **after** the gateway key is configured (write enforcement relies on the
website's `X-API-Key` for server-to-server calls):

1. Deploy this code. Tokens immediately begin persisting to Mongo; nothing is
   enforced yet (`ENFORCE_WRITE_AUTH` unset).
2. Confirm `POLICE_CAD_API_KEY` (website) and `API_GATEWAY_KEY` (API) are set and
   matching, and that logins are writing to the `auth_tokens` collection.
3. Enable enforcement:
   ```bash
   heroku config:set ENFORCE_WRITE_AUTH=true --app police-cad-app-api
   ```
4. Rollback instantly if needed:
   ```bash
   heroku config:unset ENFORCE_WRITE_AUTH --app police-cad-app-api
   ```

> This blocks *anonymous* writes. It does NOT yet stop a logged-in user from
> mutating another user's data (e.g. editing a community they don't own) — that
> requires per-endpoint ownership checks, the remaining part-2 work.

