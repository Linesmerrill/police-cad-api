package models

import "testing"

func TestParseJailTimeSeconds(t *testing.T) {
	cases := []struct {
		in      string
		seconds int64
		life    bool
	}{
		{"", 0, false},
		{"N/A", 0, false},
		{"none", 0, false},
		{"0", 0, false},
		{"30 seconds", 30, false},
		{"45 sec", 45, false},
		{"20 minutes", 1200, false},
		{"2 hours", 7200, false},
		{"3 days", 259200, false},
		{"6 months", 6 * 2592000, false},
		{"1 year", 31536000, false},
		{"1 year 2 months", 31536000 + 2*2592000, false},
		{"Life", 0, true},
		{"life in prison", 0, true},
		{"death", 0, true},
		{"banana", 0, false}, // unrecognised unit -> 0, not life
	}
	for _, c := range cases {
		gotSecs, gotLife := ParseJailTimeSeconds(c.in)
		if gotSecs != c.seconds || gotLife != c.life {
			t.Errorf("ParseJailTimeSeconds(%q) = (%d, %v), want (%d, %v)",
				c.in, gotSecs, gotLife, c.seconds, c.life)
		}
	}
}

func TestComputeArrestTotals(t *testing.T) {
	charges := []ArrestCharge{
		{Name: "Speeding", Amount: 100, JailTime: "30 seconds"},
		{Name: "Reckless", Amount: 250, JailTime: "2 minutes"},
	}

	// consecutive: 30 + 120 = 150s; fine 350
	fine, secs, label := ComputeArrestTotals(charges, "consecutive")
	if fine != 350 || secs != 150 || label != "2 minutes 30 seconds" {
		t.Errorf("consecutive = (%v, %d, %q); want (350, 150, \"2 minutes 30 seconds\")", fine, secs, label)
	}

	// concurrent: max(30, 120) = 120
	_, secs, label = ComputeArrestTotals(charges, "concurrent")
	if secs != 120 || label != "2 minutes" {
		t.Errorf("concurrent = (%d, %q); want (120, \"2 minutes\")", secs, label)
	}

	// unspecified mode defaults to consecutive
	if _, secs, _ = ComputeArrestTotals(charges, ""); secs != 150 {
		t.Errorf("default mode secs = %d, want 150", secs)
	}

	// any Life charge overrides the numeric total
	lifeCharges := append(charges, ArrestCharge{Name: "Murder", Amount: 0, JailTime: "Life"})
	if fine, secs, label = ComputeArrestTotals(lifeCharges, "consecutive"); secs != LifeSentinelSeconds || label != LifeLabel || fine != 350 {
		t.Errorf("life = (%v, %d, %q); want (350, %d, %q)", fine, secs, label, LifeSentinelSeconds, LifeLabel)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[int64]string{
		0:        "None",
		30:       "30 seconds",
		60:       "1 minute",
		150:      "2 minutes 30 seconds",
		2592000:  "1 month",
		2592045:  "1 month 45 seconds",
		31536000: "1 year",
	}
	for secs, want := range cases {
		if got := FormatDuration(secs); got != want {
			t.Errorf("FormatDuration(%d) = %q, want %q", secs, got, want)
		}
	}
}
