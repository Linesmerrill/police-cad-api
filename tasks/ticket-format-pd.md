# Feature: Ticket Format for the PD

Source: feature request `6a3d4781bd5933e6c9b8badf` (▲10). Enrich the police citation
into a proper "ticket" with more structured fields, multi-officer support, and a
court-date status, plus a readable ticket view.

## Decisions (confirmed with owner)
1. **Fields**: all proposed. Autofill what we can (reporting officer, civilian
   contact); allow editing. Support **multiple attached officers** on a citation.
2. **Phone**: new optional field on the civilian record; citation phone autofills
   from it but is independently editable (can add another / update).
3. **Court date**: derive from the linked court case. Show "Pending" / "No date
   assigned" when none. No new stored field — computed from `CourtCaseID`.
4. **Field set is fixed**, but most new fields are optional/"advanced" (collapsed)
   to avoid clutter.

## Metrics interpretation (IMPORTANT)
Officer stats are computed live via Mongo aggregation (`computeOfficerMetrics`),
not stored counters. So multi-officer credit = a query change, no backfill.
- Add `assistingOfficerIDs []string` to a citation. The reporting officer stays
  `officerID`.
- Extend the `citations_issued` / `warnings_issued` aggregations to match
  `officerID == userID` **OR** `userID ∈ assistingOfficerIDs`. Every listed
  officer then gets credit. (Interpreting "counts as a completed call for both
  officers" as: each attached officer is credited for the citation in their
  activity stats. If a distinct `calls_completed` metric is wanted instead, that
  is a small follow-up — a new metric type over the same records.)

## Phasing (incremental PRs, soak model)
- **Phase 1 — API (this PR)**: shared contract. `CriminalHistory` gains optional
  `phone`, `stopLocation`, `incidentTime`, `officerBadge`, `vehicle{plate,make,
  model,color}`, `assistingOfficerIDs[]`. Civilian gains optional `phone`. Store
  them in `AddCriminalHistoryHandler`/update. Extend citation/warning metric
  aggregations to credit assisting officers. Additive + schemaless → safe.
- **Phase 2 — Website**: citation form advanced section + autofill (reporting
  officer, civilian phone) + additional-officers picker + print-friendly ticket
  view with court-date status.
- **Phase 3 — Mobile**: `IssueActionScreen` fields + autofill + ticket detail
  view + restyle screen to the `C` palette.

## Guardrails
- Do NOT reorder `Fines[]` — court resolution writes fine status back by index.
- New fields `omitempty`; default `assistingOfficerIDs` to empty (not nil-crash).
- Court-date status is a client read concern (look up `courtCaseID` scheduled date).
