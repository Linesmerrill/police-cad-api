#!/usr/bin/env python3
"""
SolarWinds Observability log triage for police-cad-app-api.

Pulls Heroku app + router logs from the SolarWinds Observability (SWO) Logs API
over a time window, parses the two log shapes we emit (structured zap JSON from
the Go app, and heroku/router access lines), aggregates them, and prints a
triage report: recurring code errors, error-prone endpoints, client retry
loops / hot pollers, slow endpoints, and status-code distribution. Each flagged
issue carries a severity (P1/P2/P3) and a proposed action.

Read-only: it only reads logs, never writes.

Usage:
    export SWO_API_TOKEN=...            # required; do NOT hardcode
    python3 scripts/swo_log_triage.py --hours 6
    python3 scripts/swo_log_triage.py --hours 24 --markdown report.md

Designed to run unattended (e.g. a scheduled GitHub Action) — token from env,
stdlib only (no pip installs), bounded runtime via --max-pages.
"""

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter, defaultdict
from datetime import datetime, timedelta, timezone

DEFAULT_HOST = "api.na-01.cloud.solarwinds.com"

# ---- Triage thresholds (tunable) -------------------------------------------
# A single client IP hitting a single endpoint more than this many times in the
# window looks like a retry loop / broken client rather than organic traffic.
POLLER_MIN_HITS = 200
# An endpoint with at least this many 5xx responses is a code bug to chase.
SERVER_ERROR_MIN = 1
# An endpoint with this share of 4xx (and enough volume) suggests a client
# contract mismatch worth a look.
CLIENT_ERROR_RATE = 0.5
CLIENT_ERROR_MIN_VOLUME = 50
# Endpoints slower than this (ms, at the max observed) are flagged.
SLOW_MS = 1000

OBJECTID_RE = re.compile(r"[0-9a-fA-F]{24}")
LONGNUM_RE = re.compile(r"\b\d{4,}\b")
KV_RE = re.compile(r'(\w+)=("[^"]*"|\S+)')


def normalize_path(path):
    """Collapse IDs so /users/<id>/notifications and siblings aggregate."""
    path = path.split("?", 1)[0]
    path = OBJECTID_RE.sub(":id", path)
    path = LONGNUM_RE.sub(":id", path)
    return path


def parse_router_line(msg):
    """heroku/router access line -> dict, or None if it isn't one."""
    if "method=" not in msg or "status=" not in msg:
        return None
    kv = {}
    for m in KV_RE.finditer(msg):
        kv[m.group(1)] = m.group(2).strip('"')
    if "path" not in kv or "status" not in kv:
        return None
    try:
        status = int(kv.get("status", 0))
    except ValueError:
        status = 0
    service = kv.get("service", "0ms")
    try:
        service_ms = int(service.replace("ms", ""))
    except ValueError:
        service_ms = 0
    return {
        "method": kv.get("method", "?"),
        "path": kv.get("path", ""),
        "status": status,
        "ip": kv.get("fwd", "?"),
        "dyno": kv.get("dyno", "?"),
        "service_ms": service_ms,
    }


def parse_app_line(msg):
    """Structured zap JSON line -> dict, or None if it isn't JSON."""
    s = msg.strip()
    if not s.startswith("{"):
        return None
    try:
        obj = json.loads(s)
    except (ValueError, TypeError):
        return None
    if "level" not in obj and "msg" not in obj:
        return None
    # The zap `caller` is almost always config/config.go (where ErrorStatus logs),
    # not the real origin. Recover the actual handler from the stacktrace.
    source = obj.get("caller", "?")
    st = obj.get("stacktrace", "")
    m = re.search(r"api/handlers/[\w./]+\.go:\d+", st)
    if m:
        source = m.group(0)
    return {
        "level": obj.get("level", "?"),
        "source": source,
        "msg": obj.get("msg", "?"),
        "error": obj.get("error", ""),
    }


def fetch_logs(host, token, start, end, max_pages, page_size, log_filter):
    """Follow the SWO nextPage cursor until the window is covered or capped."""
    params = f"?pageSize={page_size}&startTime={start}&endTime={end}"
    if log_filter:
        params += "&filter=" + urllib.parse.quote(log_filter)
    url = f"/v1/logs{params}"
    out = []
    for _ in range(max_pages):
        req = urllib.request.Request(
            f"https://{host}{url}",
            headers={
                "Authorization": f"Bearer {token}",
                # SWO sits behind Cloudflare, which 403s (error 1010) on
                # urllib's default UA. A curl-style UA is accepted.
                "User-Agent": "police-cad-log-triage/1.0 (+curl)",
                "Accept": "application/json",
            },
        )
        try:
            with urllib.request.urlopen(req, timeout=45) as resp:
                data = json.loads(resp.read().decode())
        except urllib.error.HTTPError as e:
            sys.stderr.write(f"HTTP {e.code}: {e.read().decode()[:300]}\n")
            break
        except (urllib.error.URLError, TimeoutError) as e:
            sys.stderr.write(f"network error: {e}\n")
            break
        logs = data.get("logs", [])
        out.extend(logs)
        nxt = (data.get("pageInfo") or {}).get("nextPage", "")
        if not nxt or not logs:
            break
        url = nxt
    return out


def build_report(logs, window_desc):
    err_by_source = Counter()          # (caller, msg) -> count  (zap error/warn)
    err_examples = {}
    unstructured_app = Counter()
    status_dist = Counter()
    ep_total = Counter()               # normalized path -> hits
    ep_status = defaultdict(Counter)   # normalized path -> {status class: n}
    ep_5xx = Counter()
    ep_max_ms = defaultdict(int)
    poller = Counter()                 # (ip, normalized path) -> hits

    for entry in logs:
        program = entry.get("program", "")
        msg = entry.get("message", "")
        if "router" in program:
            r = parse_router_line(msg)
            if not r:
                continue
            np = normalize_path(r["path"])
            status_dist[r["status"]] += 1
            ep_total[np] += 1
            ep_status[np][r["status"] // 100 * 100] += 1
            if r["status"] >= 500:
                ep_5xx[np] += 1
            ep_max_ms[np] = max(ep_max_ms[np], r["service_ms"])
            poller[(r["ip"], np)] += 1
        elif "app" in program:
            a = parse_app_line(msg)
            if a is None:
                # plain log.Printf etc. — bucket a short signature
                sig = re.sub(r"\d+", "#", msg[:80])
                unstructured_app[sig] += 1
                continue
            if a["level"] in ("error", "warn", "dpanic", "panic", "fatal"):
                key = (a["source"], a["msg"])
                err_by_source[key] += 1
                if key not in err_examples:
                    err_examples[key] = a["error"] or a["msg"]

    lines = []
    w = lines.append
    w(f"# SWO log triage — {window_desc}")
    w(f"_generated {datetime.now(timezone.utc).isoformat()} · {len(logs)} log lines_\n")

    # ---- Triage findings ----
    findings = []  # (severity, title, detail, action)

    for np, n in ep_5xx.most_common():
        if n >= SERVER_ERROR_MIN:
            findings.append((
                "P1", f"5xx on `{np}` ({n}x)",
                "Endpoint is returning server errors.",
                "Open the handler; a 5xx is a code fault (bad query, nil deref, "
                "or an expected not-found miscoded as 500). Fix or downgrade.",
            ))

    for (caller, msg), n in err_by_source.most_common(10):
        sev = "P1" if n >= 100 else "P2"
        findings.append((
            sev, f"{n}x error/warn at `{caller}` — \"{msg}\"",
            f"example: {err_examples.get((caller, msg), '')[:160]}",
            "Recurring log from one code site; confirm it's expected. If it's a "
            "benign not-found, route it through InfoStatus (no stacktrace).",
        ))

    for (ip, np), n in poller.most_common():
        if n >= POLLER_MIN_HITS:
            findings.append((
                "P2", f"Possible retry loop: {ip} → `{np}` ({n}x)",
                "One client IP is hammering one endpoint far above organic rates.",
                "Likely a broken/logged-out client or a probe. Check the client "
                "polls only with valid inputs; consider rate-limiting the route.",
            ))

    for np, n in ep_total.items():
        if n >= CLIENT_ERROR_MIN_VOLUME:
            c4 = ep_status[np].get(400, 0)
            if c4 / n >= CLIENT_ERROR_RATE:
                findings.append((
                    "P3", f"High 4xx on `{np}` ({c4}/{n})",
                    "Most requests to this endpoint are client errors.",
                    "Likely an API contract mismatch (stale param, auth, bad id). "
                    "Reconcile client and server expectations.",
                ))

    for np, ms in sorted(ep_max_ms.items(), key=lambda x: -x[1]):
        if ms >= SLOW_MS:
            findings.append((
                "P3", f"Slow endpoint `{np}` (max {ms}ms)",
                "At least one request was slow.",
                "Check for a missing index or an unbounded query/scan.",
            ))

    if not findings:
        w("## ✅ No issues crossed triage thresholds in this window.\n")
    else:
        order = {"P1": 0, "P2": 1, "P3": 2}
        findings.sort(key=lambda f: order[f[0]])
        w("## 🚩 Triage findings\n")
        for sev, title, detail, action in findings:
            w(f"### [{sev}] {title}")
            w(f"- {detail}")
            w(f"- **Action:** {action}\n")

    # ---- Reference tables ----
    w("## Status codes")
    for s, n in sorted(status_dist.items()):
        w(f"- {s}: {n}")
    w("\n## Top endpoints (by requests)")
    for np, n in ep_total.most_common(15):
        cls = " ".join(f"{k}:{v}" for k, v in sorted(ep_status[np].items()))
        w(f"- {n:>6}  `{np}`  ({cls})")
    if unstructured_app:
        w("\n## Unstructured app logs (log.Printf etc. — bypass zap levels)")
        for sig, n in unstructured_app.most_common(10):
            w(f"- {n:>6}  {sig}")
    return "\n".join(lines)


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--hours", type=float, default=6.0, help="window size back from now")
    ap.add_argument("--host", default=os.environ.get("SWO_API_HOST", DEFAULT_HOST))
    ap.add_argument("--max-pages", type=int, default=30, help="pagination cap (safety)")
    ap.add_argument("--page-size", type=int, default=1000)
    ap.add_argument("--filter", default="", help="optional SWO filter expression")
    ap.add_argument("--markdown", default="", help="also write the report to this file")
    args = ap.parse_args()

    token = os.environ.get("SWO_API_TOKEN")
    if not token:
        sys.stderr.write("SWO_API_TOKEN env var is required\n")
        sys.exit(2)

    end = datetime.now(timezone.utc)
    start = end - timedelta(hours=args.hours)
    start_s = start.strftime("%Y-%m-%dT%H:%M:%SZ")
    end_s = end.strftime("%Y-%m-%dT%H:%M:%SZ")

    logs = fetch_logs(args.host, token, start_s, end_s,
                      args.max_pages, args.page_size, args.filter)
    report = build_report(logs, f"last {args.hours:g}h ({start_s} → {end_s})")
    print(report)
    if args.markdown:
        with open(args.markdown, "w") as fh:
            fh.write(report)
        sys.stderr.write(f"\nwrote {args.markdown}\n")


if __name__ == "__main__":
    main()
