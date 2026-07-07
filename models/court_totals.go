package models

import "strings"

// EffectiveCharge maps a resolved charge to the ArrestCharge the sentencing
// totals should count, applying its disposition:
//   - dismissed          => zeroed (no fine, no jail)
//   - reduced / amended   => the judge's final fine + jail
//   - upheld (or legacy)  => the original fine + jail
//
// Legacy resolutions carry only Verdict (no Disposition); those are treated as
// upheld unless the verdict is "dismissed".
func (cr ChargeResolution) EffectiveCharge() ArrestCharge {
	switch strings.ToLower(strings.TrimSpace(cr.Disposition)) {
	case DispositionDismissed:
		return ArrestCharge{Name: cr.Name, Category: cr.Category}
	case DispositionReduced, DispositionAmended:
		return ArrestCharge{Name: cr.Name, Category: cr.Category, Amount: cr.FinalAmount, JailTime: cr.FinalJailTime}
	case DispositionUpheld:
		return ArrestCharge{Name: cr.Name, Category: cr.Category, Amount: cr.OriginalAmount, JailTime: cr.OriginalJailTime}
	default:
		// No disposition set — fall back to the legacy binary verdict.
		if strings.ToLower(strings.TrimSpace(cr.Verdict)) == DispositionDismissed {
			return ArrestCharge{Name: cr.Name, Category: cr.Category}
		}
		return ArrestCharge{Name: cr.Name, Category: cr.Category, Amount: cr.OriginalAmount, JailTime: cr.OriginalJailTime}
	}
}

// ComputeResolutionTotals totals the charges on a single resolved item after
// dispositions, reusing the arrest sentence calculator.
func ComputeResolutionTotals(charges []ChargeResolution, mode string) (totalFine float64, totalSeconds int64, label string) {
	effective := make([]ArrestCharge, 0, len(charges))
	for _, cr := range charges {
		effective = append(effective, cr.EffectiveCharge())
	}
	return ComputeArrestTotals(effective, mode)
}

// ComputeCourtCaseTotals totals every charge across every resolution for the
// whole case, using the case-level sentence mode. Fines always sum; jail time
// sums (consecutive) or takes the single longest charge (concurrent) across the
// entire case; any "Life" charge makes the whole sentence Life.
func ComputeCourtCaseTotals(resolutions []CaseResolution, mode string) (totalFine float64, totalSeconds int64, label string) {
	var effective []ArrestCharge
	for _, res := range resolutions {
		for _, cr := range res.ChargeResolutions {
			effective = append(effective, cr.EffectiveCharge())
		}
	}
	return ComputeArrestTotals(effective, mode)
}
