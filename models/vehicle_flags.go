package models

import "encoding/json"

// Vehicle flag encodings.
//
// The four boolean-ish vehicle flags are stored in two different encodings.
// Newer records use the strings "true"/"false". Older records use a 1-based
// *select index* left over from the original form — which means the numeric
// encoding is NOT a boolean, and its polarity depends on the order the options
// appeared in that form:
//
//	Valid Registration?  <option>Yes</option><option>No</option>   -> "1" = valid
//	Valid Insurance?     <option>Yes</option><option>No</option>   -> "1" = valid
//	Marked Stolen?       <option value="false">No</option>
//	                     <option value="true">Yes</option>         -> "2" = stolen
//
// So "1" means yes for registration/insurance and no for stolen. Applying one
// shared "is it truthy" test to all four fields is what inverted the stolen
// flag across several clients.
//
// As of Aug 2026 roughly 92% of 2.25M vehicles still carry the numeric
// encoding. Rather than ask every client to understand both, the API resolves
// them here: reads always emit the canonical "true"/"false" form (see
// MarshalJSON), and writes persist the canonical form, so records heal as they
// are touched.
const (
	// FlagTrue is the canonical true value for a vehicle flag.
	FlagTrue = "true"
	// FlagFalse is the canonical false value for a vehicle flag.
	FlagFalse = "false"
)

// flagYesIsOne reports the polarity where select option 1 means yes.
func flagYesIsOne(v string) bool {
	return v == "1" || v == FlagTrue
}

// flagString renders a bool in the canonical encoding.
func flagString(b bool) string {
	if b {
		return FlagTrue
	}
	return FlagFalse
}

// isDeptDashboardRecord reports whether this record's flags were written by the
// website's department dashboard rather than the original form.
//
// That dashboard (shipped Feb 2026) is the only writer that ever produced a
// numeric IsExempt, and it always saved all four flags together — so a numeric
// IsExempt marks the whole record as its work. That matters because it used the
// opposite polarity for IsStolen: it wrote "1" for stolen where the original
// form wrote "2".
//
// ~3.1k records out of 2.25M. Once every record has been normalized this check
// stops matching anything and can be removed.
func isDeptDashboardRecord(d VehicleDetails) bool {
	return d.IsExempt == "1" || d.IsExempt == "2"
}

// RegistrationValid reports whether the vehicle's registration is valid,
// resolving either storage encoding.
func (d VehicleDetails) RegistrationValid() bool {
	return flagYesIsOne(d.ValidRegistration)
}

// InsuranceValid reports whether the vehicle's insurance is valid, resolving
// either storage encoding.
func (d VehicleDetails) InsuranceValid() bool {
	return flagYesIsOne(d.ValidInsurance)
}

// Stolen reports whether the vehicle is flagged stolen, resolving either
// storage encoding. Note this is the opposite polarity to RegistrationValid
// for numeric values, and that it depends on IsExempt to tell which writer
// produced the record.
func (d VehicleDetails) Stolen() bool {
	switch d.IsStolen {
	case FlagTrue:
		return true
	case FlagFalse, "":
		return false
	}
	if isDeptDashboardRecord(d) {
		return d.IsStolen == "1"
	}
	return d.IsStolen == "2"
}

// Exempt reports whether the vehicle is exempt. IsExempt has no legacy form
// behind it — the only writer of its numeric values is the department
// dashboard, which wrote "1" for exempt.
func (d VehicleDetails) Exempt() bool {
	return flagYesIsOne(d.IsExempt)
}

// NormalizeFlags rewrites the four flags into the canonical "true"/"false"
// encoding. It is idempotent, so it is safe to apply on every read and write.
func (d *VehicleDetails) NormalizeFlags() {
	// Resolve stolen first: its numeric polarity is decided by IsExempt, so it
	// has to be read before IsExempt is rewritten.
	stolen := d.Stolen()

	d.ValidRegistration = flagString(d.RegistrationValid())
	d.ValidInsurance = flagString(d.InsuranceValid())
	d.IsExempt = flagString(d.Exempt())
	d.IsStolen = flagString(stolen)
}

// MarshalJSON emits the canonical flag encoding on every read path, so clients
// never have to know about the legacy one. This is why the fix reaches the
// already-released mobile app without a new build.
func (d VehicleDetails) MarshalJSON() ([]byte, error) {
	// A local type strips the method set, avoiding infinite recursion.
	type vehicleDetails VehicleDetails

	normalized := d
	normalized.NormalizeFlags()

	return json.Marshal(vehicleDetails(normalized))
}
