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

