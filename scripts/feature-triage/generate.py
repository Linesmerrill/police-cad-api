#!/usr/bin/env python3
"""Generate the living feature-triage doc from live data + curated triage.

Merges:
  - data/feature-requests.json   (raw snapshot from fetch.py: live API data)
  - triage.json                  (curated category/scope/effort/rationale per id)

into docs/feature-triage.md (workspace: police-cad-api/docs/).

Completion auto-syncs from live DB status: released -> shipped/checked,
beta_testing -> in-beta, planned -> planned, open -> backlog. Re-running never
loses "done" state because it's derived from the source of truth, not stored
in the markdown. Triage annotations are cheap to keep current: add an entry to
triage.json for any new request id (missing ones are flagged as UNTRIAGED).

Priority score = upvotes + 2*comments (comments weighted because they signal
engaged, detailed demand). Mirrors the spirit of the site's trending metric
while staying transparent and stable for a backlog.
"""

import json
import os
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parent
DATA_FILE = ROOT / "data" / "feature-requests.json"
TRIAGE_FILE = ROOT / "triage.json"
OUT_FILE = ROOT.parent.parent / "docs" / "feature-triage.md"

SITE = os.environ.get("FR_SITE_BASE", "https://www.linespolice-cad.com")
GH = os.environ.get("FR_GH_BASE", "https://github.com/Linesmerrill")

SCOPE_ICON = {"website": "🌐", "api": "⚙️", "mobile": "📱", "bot": "🤖"}
CATEGORY_META = {
    "quick_win": ("⚡ Quick Wins", "Low/moderate effort, clear value — tackle these first."),
    "easy": ("🟢 Easy / Low-Hanging", "Cheap to ship but modest or uncertain demand."),
    "full_feature": ("🏗️ Full Features", "Substantial builds — plan and phase these."),
    "probably_not": ("🚫 Probably Shouldn't Add", "Out of scope, infeasible, or cost far outweighs value."),
}
CATEGORY_ORDER = ["quick_win", "easy", "full_feature", "probably_not"]
STATUS_BADGE = {
    "open": "",
    "planned": " 📋 planned",
    "beta_testing": " 🧪 in beta",
    "released": " ✅ shipped",
}


def priority(fr):
    return fr.get("upvoteCount", 0) + 2 * fr.get("commentCount", 0)


def scope_str(scope):
    if not scope:
        return "—"
    return " ".join(SCOPE_ICON.get(s, s) for s in scope) + " " + "/".join(scope)


def fr_link(fr):
    title = (fr.get("title") or "Untitled").replace("|", "\\|").strip()
    return f"[{title}]({SITE}/feature-requests/{fr['_id']})"


def esc(text):
    return (text or "").replace("|", "\\|").replace("\n", " ").strip()


def pr_links(prs):
    """Render triage `prs` refs (e.g. "police-cad-api#138") as linked PR numbers."""
    parts = []
    for ref in prs or []:
        if "#" in ref:
            repo, num = ref.split("#", 1)
            parts.append(f"[{ref}]({GH}/{repo}/pull/{num})")
        else:
            parts.append(ref)
    return ", ".join(parts)


def main():
    snap = json.loads(DATA_FILE.read_text())
    triage_doc = json.loads(TRIAGE_FILE.read_text())
    triage = triage_doc.get("triage", {})
    # Unfiled asks: ideas captured (e.g. from Discord) that don't have a formal
    # feature request yet, so they can't be keyed by id in `triage`. Held here
    # until they're filed on the site (then move them into `triage`).
    unfiled = triage_doc.get("unfiled", [])
    active = snap.get("requests", [])
    shipped = snap.get("shipped", [])

    # Bucket active requests by triage category.
    buckets = {c: [] for c in CATEGORY_ORDER}
    untriaged = []
    for fr in active:
        t = triage.get(fr["_id"])
        if not t:
            untriaged.append(fr)
            continue
        cat = t.get("category", "full_feature")
        buckets.setdefault(cat, []).append((fr, t))

    for cat in buckets:
        buckets[cat].sort(key=lambda pair: priority(pair[0]), reverse=True)

    now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    total_active = len(active)
    total_votes = sum(fr.get("upvoteCount", 0) for fr in active)

    lines = []
    w = lines.append

    w("# Feature Request Triage — Living Doc")
    w("")
    w(f"_Auto-generated {now} from live feature-request data. "
      "Do not hand-edit — update `triage.json` and re-run `generate.py` "
      "(see [README](../scripts/feature-triage/README.md))._")
    w("")
    w(f"**{total_active}** active requests · **{total_votes}** total upvotes · "
      f"**{len(shipped)}** shipped. Statuses sync from the DB: an item marked "
      "released/beta on the site moves here automatically.")
    w("")
    w("**Legend** — Scope: 🌐 website · ⚙️ API · 📱 mobile · 🤖 bot. "
      "Effort: S (<1d) · M (few days) · L (1–2wk) · XL (multi-wk). "
      "Priority = upvotes + 2×comments.")
    w("")

    # ---- Suggested next up -------------------------------------------------
    candidates = [p for p in (buckets["quick_win"] + buckets["easy"])
                  if p[0].get("status") == "open" and not p[1].get("prs")]
    candidates.sort(key=lambda pair: priority(pair[0]), reverse=True)
    w("## 🎯 Suggested next up")
    w("")
    w("Highest-value low-effort items (quick wins & easy), by priority:")
    w("")
    if candidates:
        w("| Priority | Feature | ▲ | 💬 | Effort | Scope |")
        w("|---:|---|---:|---:|:--:|---|")
        for fr, t in candidates[:8]:
            w(f"| {priority(fr)} | {fr_link(fr)} | {fr.get('upvoteCount',0)} | "
              f"{fr.get('commentCount',0)} | {t.get('effort','?')} | "
              f"{scope_str(t.get('scope'))} |")
    else:
        w("_None triaged yet._")
    w("")

    # ---- In progress (open PRs) -------------------------------------------
    in_progress = [(fr, t) for cat in CATEGORY_ORDER for (fr, t) in buckets[cat]
                   if t.get("prs")]
    in_progress.sort(key=lambda pair: priority(pair[0]), reverse=True)
    if in_progress:
        w("## 🔀 In Progress (open PRs)")
        w("")
        w("_Feature requests with open PRs — track these through to merge._")
        w("")
        w("| Feature | PRs | Effort | Scope |")
        w("|---|---|:--:|---|")
        for fr, t in in_progress:
            w(f"| {fr_link(fr)} | {pr_links(t.get('prs'))} | "
              f"{t.get('effort','?')} | {scope_str(t.get('scope'))} |")
        w("")

    # ---- Summary ----------------------------------------------------------
    w("## 📊 Summary")
    w("")
    w("| Category | Count | Upvotes |")
    w("|---|---:|---:|")
    for cat in CATEGORY_ORDER:
        items = buckets[cat]
        votes = sum(fr.get("upvoteCount", 0) for fr, _ in items)
        w(f"| {CATEGORY_META[cat][0]} | {len(items)} | {votes} |")
    if untriaged:
        w(f"| ⚠️ Untriaged | {len(untriaged)} | "
          f"{sum(fr.get('upvoteCount',0) for fr in untriaged)} |")
    w("")

    # Scope tally across active triaged items.
    scope_tally = {k: 0 for k in SCOPE_ICON}
    for cat in CATEGORY_ORDER:
        for _, t in buckets[cat]:
            for s in t.get("scope", []):
                if s in scope_tally:
                    scope_tally[s] += 1
    w("**Surface impact** (active triaged items touching each surface): "
      + " · ".join(f"{SCOPE_ICON[k]} {k} {v}" for k, v in scope_tally.items()))
    w("")

    # ---- Category sections ------------------------------------------------
    for cat in CATEGORY_ORDER:
        title, desc = CATEGORY_META[cat]
        items = buckets[cat]
        w(f"## {title}")
        w("")
        w(f"_{desc}_")
        w("")
        if not items:
            w("_None._")
            w("")
            continue
        w("| ✓ | Feature | ▲ | 💬 | Effort | Scope | Requested by | Assessment |")
        w("|:--:|---|---:|---:|:--:|---|---|---|")
        for fr, t in items:
            status = fr.get("status", "open")
            check = "x" if status == "released" else " "
            badge = STATUS_BADGE.get(status, "")
            if t.get("prs"):
                badge += " 🔀 PR open"
            assessment = esc(t.get("rationale", ""))
            risks = esc(t.get("risks_or_deps", ""))
            dup = esc(t.get("possible_duplicate", ""))
            if risks:
                assessment += f" _Risks/deps: {risks}_"
            if dup and dup.lower() != "none":
                assessment += f" _Possible dup: {dup}_"
            author = esc((fr.get("author") or {}).get("username", "unknown"))
            w(f"| {check} | {fr_link(fr)}{badge} | {fr.get('upvoteCount',0)} | "
              f"{fr.get('commentCount',0)} | {t.get('effort','?')} | "
              f"{scope_str(t.get('scope'))} | {author} | {assessment} |")
        w("")

    # ---- Untriaged --------------------------------------------------------
    if untriaged:
        w("## ⚠️ Untriaged (new since last triage)")
        w("")
        w("_Add an entry to `triage.json` for each, then re-run._")
        w("")
        w("| Feature | ▲ | 💬 | Requested by |")
        w("|---|---:|---:|---|")
        for fr in sorted(untriaged, key=priority, reverse=True):
            author = esc((fr.get("author") or {}).get("username", "unknown"))
            w(f"| {fr_link(fr)} | {fr.get('upvoteCount',0)} | "
              f"{fr.get('commentCount',0)} | {author} |")
        w("")

    # ---- Unfiled asks -----------------------------------------------------
    if unfiled:
        w("## 📥 Unfiled Asks (not yet feature requests)")
        w("")
        w("_Captured from Discord/DMs before a formal request exists — file them "
          "on the site to add upvotes, then move the entry into `triage`._")
        w("")
        w("| Ask | Source | Effort | Scope | Notes |")
        w("|---|---|:--:|---|---|")
        for u in unfiled:
            notes = esc(u.get("rationale", ""))
            risks = esc(u.get("risks_or_deps", ""))
            rel = esc(u.get("possible_relation", ""))
            if risks:
                notes += f" _Risks/deps: {risks}_"
            if rel:
                notes += f" _Related: {rel}_"
            w(f"| {esc(u.get('title','(untitled)'))} | {esc(u.get('source',''))} | "
              f"{u.get('effort','?')} | {scope_str(u.get('scope'))} | {notes} |")
        w("")

    # ---- Shipped ----------------------------------------------------------
    w("## ✅ Recently Shipped")
    w("")
    w("_Completed requests, with credit to the requester — handy for release notes._")
    w("")
    if shipped:
        w("| Feature | ▲ | Requested by |")
        w("|---|---:|---|")
        for fr in sorted(shipped, key=lambda x: x.get("upvoteCount", 0), reverse=True):
            author = esc((fr.get("author") or {}).get("username", "unknown"))
            w(f"| {fr_link(fr)} | {fr.get('upvoteCount',0)} | {author} |")
    else:
        w("_None in the current snapshot._")
    w("")

    OUT_FILE.parent.mkdir(parents=True, exist_ok=True)
    OUT_FILE.write_text("\n".join(lines))
    print(f"Wrote {OUT_FILE} ({len(active)} active, {len(shipped)} shipped, "
          f"{len(untriaged)} untriaged)")


if __name__ == "__main__":
    main()
