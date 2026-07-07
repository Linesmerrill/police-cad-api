package models

import "testing"

func TestChargeResolutionEffectiveCharge(t *testing.T) {
	base := ChargeResolution{
		Name: "Speeding", Category: "Traffic",
		OriginalAmount: 100, OriginalJailTime: "2 minutes",
		FinalAmount: 40, FinalJailTime: "30 seconds",
	}
	cases := []struct {
		disp   string
		amount float64
		jail   string
	}{
		{DispositionUpheld, 100, "2 minutes"},
		{DispositionDismissed, 0, ""},
		{DispositionReduced, 40, "30 seconds"},
		{DispositionAmended, 40, "30 seconds"},
	}
	for _, c := range cases {
		cr := base
		cr.Disposition = c.disp
		ec := cr.EffectiveCharge()
		if ec.Amount != c.amount || ec.JailTime != c.jail {
			t.Errorf("%s => amount %v jail %q; want %v %q", c.disp, ec.Amount, ec.JailTime, c.amount, c.jail)
		}
	}

	// Legacy resolutions (no Disposition) fall back to the binary Verdict.
	if ec := (ChargeResolution{Verdict: "dismissed", OriginalAmount: 100, OriginalJailTime: "1 minute"}).EffectiveCharge(); ec.Amount != 0 || ec.JailTime != "" {
		t.Errorf("legacy dismissed => %v %q; want 0 \"\"", ec.Amount, ec.JailTime)
	}
	if ec := (ChargeResolution{Verdict: "upheld", OriginalAmount: 100, OriginalJailTime: "1 minute"}).EffectiveCharge(); ec.Amount != 100 || ec.JailTime != "1 minute" {
		t.Errorf("legacy upheld => %v %q; want 100 \"1 minute\"", ec.Amount, ec.JailTime)
	}
}

func TestComputeResolutionTotals(t *testing.T) {
	charges := []ChargeResolution{
		{Disposition: DispositionUpheld, OriginalAmount: 100, OriginalJailTime: "30 seconds"},
		{Disposition: DispositionReduced, OriginalAmount: 500, OriginalJailTime: "5 minutes", FinalAmount: 200, FinalJailTime: "1 minute"},
		{Disposition: DispositionDismissed, OriginalAmount: 250, OriginalJailTime: "10 minutes"},
	}
	// consecutive: fine 100+200=300; jail 30+60=90s
	fine, secs, label := ComputeResolutionTotals(charges, "consecutive")
	if fine != 300 || secs != 90 || label != "1 minute 30 seconds" {
		t.Errorf("consecutive => (%v, %d, %q); want (300, 90, \"1 minute 30 seconds\")", fine, secs, label)
	}
	// concurrent: jail max(30,60)=60
	if _, secs, _ = ComputeResolutionTotals(charges, "concurrent"); secs != 60 {
		t.Errorf("concurrent secs = %d, want 60", secs)
	}
}

func TestComputeCourtCaseTotals_LifeOverride(t *testing.T) {
	resolutions := []CaseResolution{
		{ChargeResolutions: []ChargeResolution{
			{Disposition: DispositionUpheld, OriginalAmount: 100, OriginalJailTime: "1 minute"},
		}},
		{ChargeResolutions: []ChargeResolution{
			{Disposition: DispositionAmended, FinalAmount: 1000, FinalJailTime: "Life"},
			{Disposition: DispositionDismissed, OriginalAmount: 50, OriginalJailTime: "5 minutes"},
		}},
	}
	// fine 100 + 1000 = 1100; any Life charge => Life sentence
	fine, secs, label := ComputeCourtCaseTotals(resolutions, "consecutive")
	if fine != 1100 || secs != LifeSentinelSeconds || label != LifeLabel {
		t.Errorf("case totals => (%v, %d, %q); want (1100, %d, %q)", fine, secs, label, LifeSentinelSeconds, LifeLabel)
	}
}
