package models

import (
	"encoding/json"
	"strings"
	"time"
)

// License storage shapes.
//
// The licenses collection has been written by two different clients that never
// agreed on a key set, and neither has ever migrated:
//
//	legacy (website Mongoose model, ~316k docs)  modern (this API, ~101k docs)
//	  license.licenseType                          license.type
//	  license.additionalNotes                      license.notes
//	  license.ownerID                              license.civilianID
//	  license.ownerName / activeCommunityID / userID
//
// No document carries both key sets. Because the list handler only ever
// filtered on license.civilianID, every legacy license was invisible to the
// API — which is why ~170k civilians showed an empty license list on mobile
// while the website showed their records fine.
//
// Two more fields drifted in format rather than in name:
//
//	status          "valid" (legacy, lowercase) vs "Valid" (modern, title case)
//	expirationDate  "2033-11-11" (website date input) vs "01/01/1995" (mobile)
//
// Reads resolve all of this here so no client has to know about it — see
// MarshalJSON. That is what repairs the already-released mobile build without
// shipping a new one.
//
// IMPORTANT: the legacy keys are deliberately NOT removed from the database.
// The website's police and dispatch dashboards still render licenses straight
// from the Mongoose model, and their <select> options are the lowercase legacy
// values (views/license-list.ejs, views/police-dashboard.ejs). Rewriting
// license.status or license.licenseType in place would blank those dropdowns
// on a live CAD screen. Normalization is a read and write-path concern only.

// ExpirationDateFormat is the canonical expiration date layout. It is what the
// website's <input type="date"> requires, what dd-civilians.js assumes when it
// slices the first ten characters, and it sorts lexicographically.
const ExpirationDateFormat = "2006-01-02"

// Canonical license statuses. This set mirrors the website's license form
// (views/civ-dashboard.ejs, public/js/dd-civilians.js), which is the surface
// that changes most often. Expired has no option in that form but is written
// by the legacy police and dispatch dashboards, so it is recognised here.
const (
	LicenseStatusPending   = "Pending"
	LicenseStatusValid     = "Valid"
	LicenseStatusApproved  = "Approved"
	LicenseStatusSuspended = "Suspended"
	LicenseStatusRevoked   = "Revoked"
	LicenseStatusExpired   = "Expired"
)

// canonicalStatuses maps a case-folded status onto its canonical spelling.
// Only the casing is normalized — Valid and Approved mean different things to
// different communities and are never merged.
var canonicalStatuses = map[string]string{
	"pending":   LicenseStatusPending,
	"valid":     LicenseStatusValid,
	"approved":  LicenseStatusApproved,
	"suspended": LicenseStatusSuspended,
	"revoked":   LicenseStatusRevoked,
	"expired":   LicenseStatusExpired,
}

// expirationLayouts are the non-canonical layouts seen in production, tried in
// order. Go resolves a two digit year 00-68 to 20xx, which matches how these
// were entered ("11/23/27" is 2027).
var expirationLayouts = []string{
	"01/02/2006",
	"1/2/2006",
	"01/02/06",
	"1/2/06",
	time.RFC3339,
}

// ResolvedType returns the license type from whichever key holds it.
func (d LicenseDetails) ResolvedType() string {
	if strings.TrimSpace(d.Type) != "" {
		return d.Type
	}
	return d.LicenseType
}

// ResolvedNotes returns the notes from whichever key holds them.
func (d LicenseDetails) ResolvedNotes() string {
	if strings.TrimSpace(d.Notes) != "" {
		return d.Notes
	}
	return d.AdditionalNotes
}

// ResolvedCivilianID returns the owning civilian from whichever key holds it.
func (d LicenseDetails) ResolvedCivilianID() string {
	if strings.TrimSpace(d.CivilianID) != "" {
		return d.CivilianID
	}
	return d.OwnerID
}

// NormalizedStatus returns the canonical spelling of the status. An
// unrecognised status is passed through untouched rather than guessed at, so a
// community using its own vocabulary keeps it.
func (d LicenseDetails) NormalizedStatus() string {
	if canonical, ok := canonicalStatuses[strings.ToLower(strings.TrimSpace(d.Status))]; ok {
		return canonical
	}
	return d.Status
}

// NormalizedExpiration returns the expiration date as yyyy-mm-dd.
//
// Anything that cannot be parsed unambiguously is returned unchanged. Roughly
// 600 records hold values like "2045" or "11/25" where the intended day or
// century is genuinely unknowable; silently inventing one would be worse than
// showing the user what was actually typed.
func (d LicenseDetails) NormalizedExpiration() string {
	raw := strings.TrimSpace(d.ExpirationDate)
	if raw == "" {
		return d.ExpirationDate
	}

	if _, err := time.Parse(ExpirationDateFormat, raw); err == nil {
		return raw
	}

	for _, layout := range expirationLayouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.Format(ExpirationDateFormat)
		}
	}

	return d.ExpirationDate
}

// NormalizeFields copies each legacy key into its canonical home and rewrites
// status and expiration into their canonical forms. It is idempotent.
//
// The legacy keys are left populated on purpose; see the note at the top of
// this file.
func (d *LicenseDetails) NormalizeFields() {
	d.Type = d.ResolvedType()
	d.Notes = d.ResolvedNotes()
	d.CivilianID = d.ResolvedCivilianID()
	d.Status = d.NormalizedStatus()
	d.ExpirationDate = d.NormalizedExpiration()
}

// MarshalJSON emits the canonical shape on every read path, so clients never
// have to know which writer produced a record.
func (d LicenseDetails) MarshalJSON() ([]byte, error) {
	// A local type strips the method set, avoiding infinite recursion.
	type licenseDetails LicenseDetails

	normalized := d
	normalized.NormalizeFields()

	// These now live in their canonical fields. Emitting both would give a
	// client two places to look and a way to pick the wrong one.
	normalized.LicenseType = ""
	normalized.AdditionalNotes = ""
	normalized.OwnerID = ""

	return json.Marshal(licenseDetails(normalized))
}
