#!/usr/bin/env python3
"""Fetch feature requests (+ full comments) for triage.

Pulls every feature request in the target statuses from the Lines Police CAD
API and writes a single raw JSON snapshot to data/feature-requests.json. The
list endpoint returns empty `comments`, so for any request with commentCount>0
we additionally fetch the detail endpoint to capture what people wrote there.

This talks to the same public read endpoints the website uses. First-party
access is granted by the Origin header (see api/handlers/gateway.go) -- this is
not a secret, it is how our own web clients reach their own API. Override the
host / origin with env vars if needed.

Usage:
    python3 fetch.py                      # open + planned + beta_testing
    FR_STATUSES=open python3 fetch.py     # just open
"""

import json
import os
import time
import urllib.error
import urllib.request
from pathlib import Path

API_BASE = os.environ.get(
    "FR_API_BASE",
    "https://police-cad-app-api-bc6d659b60b3.herokuapp.com",
).rstrip("/")
ORIGIN = os.environ.get("FR_API_ORIGIN", "https://www.linespolice-cad.com")
# Active statuses that get triaged.
STATUSES = os.environ.get("FR_STATUSES", "open,planned,beta_testing").split(",")
# Shipped statuses pulled for the "Recently Shipped" section / release notes.
# These are not triaged; they show completed with requester credit. Capped to
# the most recent SHIPPED_LIMIT so the doc doesn't accumulate all history.
SHIPPED_STATUSES = os.environ.get("FR_SHIPPED_STATUSES", "released").split(",")
SHIPPED_LIMIT = int(os.environ.get("FR_SHIPPED_LIMIT", "60"))

DATA_DIR = Path(__file__).resolve().parent / "data"
OUT_FILE = DATA_DIR / "feature-requests.json"

HEADERS = {"Origin": ORIGIN, "Accept": "application/json"}


def _get(path: str) -> dict:
    req = urllib.request.Request(API_BASE + path, headers=HEADERS)
    for attempt in range(4):
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                return json.loads(resp.read().decode("utf-8"))
        except (urllib.error.URLError, TimeoutError) as err:
            if attempt == 3:
                raise
            time.sleep(1.5 * (attempt + 1))
            print(f"  retry {attempt + 1} for {path}: {err}")
    raise RuntimeError("unreachable")


def fetch_status(status: str, sort: str = "top", cap: int | None = None) -> list:
    items, page = [], 1
    while True:
        payload = _get(
            f"/api/v2/feature-requests?status={status}&sort={sort}&page={page}&limit=100"
        )
        batch = payload.get("data", [])
        items.extend(batch)
        total = payload.get("totalCount", 0)
        if (cap and len(items) >= cap) or len(items) >= total or not batch:
            break
        page += 1
    if cap:
        items = items[:cap]
    print(f"  {status}: {len(items)} requests")
    return items


def main() -> None:
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    print(f"Fetching statuses {STATUSES} from {API_BASE} ...")

    requests = []
    for status in STATUSES:
        requests.extend(fetch_status(status.strip()))

    print(f"Fetching shipped statuses {SHIPPED_STATUSES} (cap {SHIPPED_LIMIT}) ...")
    shipped = []
    for status in SHIPPED_STATUSES:
        shipped.extend(fetch_status(status.strip(), sort="newest", cap=SHIPPED_LIMIT))

    # Hydrate comments for anything that has them (list endpoint omits them).
    hydrated = 0
    for fr in requests:
        if fr.get("commentCount", 0) > 0:
            detail = _get(f"/api/v1/feature-requests/{fr['_id']}")
            fr["comments"] = detail.get("comments", [])
            hydrated += 1
    print(f"Hydrated comments for {hydrated} active requests")

    snapshot = {
        "fetchedStatuses": [s.strip() for s in STATUSES],
        "shippedStatuses": [s.strip() for s in SHIPPED_STATUSES],
        "count": len(requests),
        "shippedCount": len(shipped),
        "requests": requests,
        "shipped": shipped,
    }
    OUT_FILE.write_text(json.dumps(snapshot, indent=2, ensure_ascii=False))
    print(f"Wrote {len(requests)} active + {len(shipped)} shipped -> {OUT_FILE}")


if __name__ == "__main__":
    main()
