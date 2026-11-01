# police-cad-api (Backend API)

Go + MongoDB (gorilla/mux). Serves both the website and the mobile app.
Secrets live in `.envrc` at the repo root.

This project has active customers in production. Code must be
production-quality: correct, reliable and safe.

## Deploys: a merge to `main` is a release

**Merging to `main` deploys to Heroku immediately. Merged means live.**

Back-to-back merges mean back-to-back deploys, and each deploy disrupts live
API calls for customers who are mid-session. One feature spread over five PRs
merged into `main` is five disruptions, not one.

## Never target `main` directly — use one integration branch per workstream

1. Pick **one integration branch** for the workstream, e.g. `feature/developer-api`.
2. Every PR in that workstream targets the **integration branch**, never `main`.
3. Merrill merges those PRs into the integration branch and tests the branch as a whole.
4. **One** PR takes the integration branch into `main`, so the feature deploys once.

Only the integration branch gets a `-> main` PR. Several PRs from one
workstream open against `main` is a mistake to fix by retargeting them, not a
reason to merge them one at a time.

Never push to `main` directly, not even for a hotfix — a hotfix is still a PR,
and still goes through an integration branch when it is part of a larger piece
of work.

### Consequence: no CI on the stacked PRs

Every workflow here is gated on `pull_request: branches: [main]`
(`.github/workflows/go.yml`, `codeql.yml`, `procfile-check.yml`), so a PR
targeting an integration branch gets **no checks at all** — not `build`, not
CodeQL. That is expected. Do not retarget a PR to `main` to make CI run.

Verify those PRs locally instead:

```bash
go build ./...
go test ./...
```

The integration branch's own `-> main` PR is the CI and CodeQL gate.

Note: `vendor/` is untracked and can be stale against `go.mod`, which makes the
default `-mod=vendor` fail locally. CI has no `vendor/` directory, so pass
`-mod=mod` to match it.

## Conventions

- **Formatting:** this repo is not `gofmt`-clean. Never run `gofmt -w` over a
  directory or an existing file; format only files you created.
- **New indexes** go in `scripts/create_indexes.js` as a `createIndexSafe(...)` call.
- **Error envelope:** `response.message`. v1 endpoints return flat arrays;
  v2 endpoints return `{ data, totalCount, page, limit }`.
- **Cron jobs** must call `scheduler.SendCronAlert(...)` on failure paths.
- **Expected not-found** uses `config.InfoStatus` with a 404. `config.ErrorStatus`
  reserves zap's Error level — and the multi-KB stacktrace production attaches to
  it — for genuine 5xx faults, so don't reach for it to report something expected.
