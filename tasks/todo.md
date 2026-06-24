# Reset stats on promotion (community toggle) — builds on PR #133

## Goal
Community-level toggle. **Default OFF = today's all-time behavior (no breakage).**
When ON: rank requirement progress is measured **since the member's last promotion**
(`RankAssignedAt`), custom requirements reset on promotion, and the UI shows BOTH
all-time and since-promotion numbers with progress labeled "since promotion".

## Data model
- [ ] Add `RankSettings` to `CommunityDetails` (new field, keeps existing layout):
      `community.rankSettings.resetStatsOnPromotion bool` (default false / absent = off).

## API (police-cad-api)
- [ ] `computeOfficerMetrics`: add optional `since *time.Time`. When set, inject
      `{"<prefix>.createdAt": {"$gte": since}}` into each metric `$match`
      (paths verified: civilian.criminalHistory.createdAt, arrestReport.createdAt,
      call.createdAt, bolo.createdAt, warrant.createdAt, medicalReport.createdAt).
      Empty/zero `since` => no filter (all-time).
- [ ] Add helper to read the flag + resolve a member's "since" time:
      flag ON && member.RankAssignedAt set => since = RankAssignedAt;
      flag ON && no RankAssignedAt (default-rank members) => all-time fallback;
      flag OFF => all-time.
- [ ] `GetRankProgressHandler`:
      - compute since-promotion metrics for requirement eval when flag ON.
      - also compute all-time metrics for display.
      - response: add `resetStatsOnPromotion` (top level); `OfficerMetric` and
        `RankProgress` gain `allTimeValue` (currentValue stays the eval value).
      - reset-mode: promote at most ONE rank per check (after promo, stats reset to ~0,
        so no instant chain-promote — this is the core fix).
- [ ] `CheckAllPromotionsHandler`: same since-promotion eval + one-step-per-check when ON.
- [ ] PR #133 reconcile: gate the `customRequirementsMet` reset on the flag
      (OFF = custom reqs persist / all-time philosophy; ON = reset on promotion).
- [ ] Tests: since-promotion eval, all-time fallback, one-step promotion, flag-gated
      custom reset. Keep existing tests green.

## Website (police-cad)
- [ ] Toggle in BOTH the community settings modal AND the Manage Ranks panel header.
      Both bind to the same community-wide value (community.rankSettings.resetStatsOnPromotion);
      changing one reflects in the other. Persisted via the community update path.
      ddModal/standard patterns; immediate UI update.
- [ ] Pending-promotions + per-member progress: when flag ON, label bar "since promotion"
      and show all-time as secondary text (e.g. `2 / 2 since promotion · 4 all-time`).
- [ ] Playwright coverage for the toggle + labeled display.

## Mobile (police-cad-app)
- [ ] OfficerStatsScreen: read `resetStatsOnPromotion` + `allTimeValue` from API.
      When ON, label progress "since promotion" and show all-time alongside. Read-only.

## Decisions locked
- Scope: community-level (one switch for the whole community).
- Default OFF; existing communities unchanged.
- No backfill: use existing `RankAssignedAt`; empty => all-time fallback for that member.
- Logic lives server-side; clients just render returned values + flag.

## Review
(to be filled in after implementation)
