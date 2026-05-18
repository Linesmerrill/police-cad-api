package handlers

import "testing"

func TestMapStoreToSource(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"PLAY_STORE", "play_store"},
		{"APP_STORE", "app_store"},
		{"MAC_APP_STORE", "app_store"},
		{"STRIPE", "stripe"},
		{"PROMOTIONAL", "promotional"},
		{"AMAZON", "amazon"},
		{"play_store", "play_store"},  // mobile lowercase
		{"app_store", "app_store"},    // mobile lowercase
		{"  PLAY_STORE  ", "play_store"},
		{"", ""},
		{"UNKNOWN", ""},
		{"something_else", ""},
	}
	for _, tc := range cases {
		if got := mapStoreToSource(tc.in); got != tc.want {
			t.Errorf("mapStoreToSource(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseProductID(t *testing.T) {
	cases := []struct {
		in         string
		wantPlan   string
		wantAnnual bool
		wantOK     bool
	}{
		// Every currently-sold product.
		{"base_monthly", "base", false, true},
		{"base_annual", "base", true, true},
		{"premium_monthly", "premium", false, true},
		{"premium_annual", "premium", true, true},
		{"premium_plus_monthly", "premium_plus", false, true},
		{"premium_plus_annual", "premium_plus", true, true},

		// Play Store base:offer form.
		{"premium_plus_monthly:premium-plus-monthly", "premium_plus", false, true},
		{"base_annual:base-annual", "base", true, true},

		// Bundle/sku prefix (iOS occasionally).
		{"com.linesmerrill.lpc.premium_monthly", "premium", false, true},

		// Case + whitespace tolerance.
		{"  PREMIUM_PLUS_ANNUAL  ", "premium_plus", true, true},

		// Garbage falls through — must NOT silently downgrade.
		{"", "", false, false},
		{"unknown_product", "", false, false},
		{"premium_weekly", "", false, false},
		{"super_premium_monthly", "", false, false},
		{"premium_monthly_foo", "", false, false},
	}
	for _, tc := range cases {
		gotPlan, gotAnnual, gotOK := parseProductID(tc.in)
		if gotPlan != tc.wantPlan || gotAnnual != tc.wantAnnual || gotOK != tc.wantOK {
			t.Errorf("parseProductID(%q) = (%q, %v, %v), want (%q, %v, %v)",
				tc.in, gotPlan, gotAnnual, gotOK, tc.wantPlan, tc.wantAnnual, tc.wantOK)
		}
	}
}
