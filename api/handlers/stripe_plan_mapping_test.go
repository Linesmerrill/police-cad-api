package handlers

import "testing"

// setStripePriceEnv configures a full, representative set of V1 and V2 Stripe
// price-id env vars for the duration of a test. t.Setenv restores them after.
func setStripePriceEnv(t *testing.T) {
	t.Helper()
	vars := map[string]string{
		// V1 (original) price ids.
		"STRIPE_BASE_MONTHLY_PRICE_ID":         "price_base_m_v1",
		"STRIPE_BASE_ANNUAL_PRICE_ID":          "price_base_a_v1",
		"STRIPE_PREMIUM_MONTHLY_PRICE_ID":      "price_premium_m_v1",
		"STRIPE_PREMIUM_ANNUAL_PRICE_ID":       "price_premium_a_v1",
		"STRIPE_PREMIUM_PLUS_MONTHLY_PRICE_ID": "price_premiumplus_m_v1",
		"STRIPE_PREMIUM_PLUS_ANNUAL_PRICE_ID":  "price_premiumplus_a_v1",
		// V2 (post-price-drop) price ids — the ones the buggy checkout switch missed.
		"STRIPE_BASE_V2_MONTHLY_PRICE_ID":         "price_base_m_v2",
		"STRIPE_BASE_V2_ANNUAL_PRICE_ID":          "price_base_a_v2",
		"STRIPE_PREMIUM_V2_MONTHLY_PRICE_ID":      "price_premium_m_v2",
		"STRIPE_PREMIUM_V2_ANNUAL_PRICE_ID":       "price_premium_a_v2",
		"STRIPE_PREMIUM_PLUS_V2_MONTHLY_PRICE_ID": "price_premiumplus_m_v2",
		"STRIPE_PREMIUM_PLUS_V2_ANNUAL_PRICE_ID":  "price_premiumplus_a_v2",
	}
	for k, v := range vars {
		t.Setenv(k, v)
	}
}

func TestMapStripePriceIDToPlan(t *testing.T) {
	setStripePriceEnv(t)

	cases := []struct {
		name       string
		priceID    string
		wantPlan   string
		wantAnnual bool
	}{
		// V1 ids.
		{"v1 base monthly", "price_base_m_v1", "base", false},
		{"v1 base annual", "price_base_a_v1", "base", true},
		{"v1 premium monthly", "price_premium_m_v1", "premium", false},
		{"v1 premium annual", "price_premium_a_v1", "premium", true},
		{"v1 premium_plus monthly", "price_premiumplus_m_v1", "premium_plus", false},
		{"v1 premium_plus annual", "price_premiumplus_a_v1", "premium_plus", true},

		// V2 ids — the regression that caused plan="unknown" on new checkouts.
		{"v2 base monthly", "price_base_m_v2", "base", false},
		{"v2 base annual", "price_base_a_v2", "base", true},
		{"v2 premium monthly", "price_premium_m_v2", "premium", false},
		{"v2 premium annual", "price_premium_a_v2", "premium", true},
		{"v2 premium_plus monthly", "price_premiumplus_m_v2", "premium_plus", false},
		{"v2 premium_plus annual", "price_premiumplus_a_v2", "premium_plus", true},

		// Unmapped / empty must surface as the explicit "unknown" sentinel —
		// never silently downgrade to a real plan.
		{"empty", "", "unknown", false},
		{"unmapped price id", "price_does_not_exist", "unknown", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPlan, gotAnnual := mapStripePriceIDToPlan(tc.priceID)
			if gotPlan != tc.wantPlan || gotAnnual != tc.wantAnnual {
				t.Errorf("mapStripePriceIDToPlan(%q) = (%q, %v), want (%q, %v)",
					tc.priceID, gotPlan, gotAnnual, tc.wantPlan, tc.wantAnnual)
			}
		})
	}
}

func TestPlanFromStripePriceID(t *testing.T) {
	setStripePriceEnv(t)

	cases := []struct {
		name       string
		priceID    string
		wantPlan   string
		wantAnnual bool
	}{
		// Resolves real plans for BOTH V1 and V2 ids.
		{"v1 premium_plus monthly", "price_premiumplus_m_v1", "premium_plus", false},
		{"v2 premium_plus monthly", "price_premiumplus_m_v2", "premium_plus", false},
		{"v2 base annual", "price_base_a_v2", "base", true},

		// Unmapped ids return the "" sentinel (NOT "unknown") so the admin UI
		// can fall back to showing the raw price id.
		{"empty", "", "", false},
		{"unmapped price id", "price_does_not_exist", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPlan, gotAnnual := planFromStripePriceID(tc.priceID)
			if gotPlan != tc.wantPlan || gotAnnual != tc.wantAnnual {
				t.Errorf("planFromStripePriceID(%q) = (%q, %v), want (%q, %v)",
					tc.priceID, gotPlan, gotAnnual, tc.wantPlan, tc.wantAnnual)
			}
		})
	}
}

// TestStripePlanMappersAgree is the guard against the bug ever returning:
// every price id that mapStripePriceIDToPlan resolves to a real plan MUST
// resolve to the same plan/cadence via planFromStripePriceID (and vice-versa,
// modulo the "unknown" -> "" sentinel translation). If a future change adds a
// price id to one mapper but not the other, this fails.
func TestStripePlanMappersAgree(t *testing.T) {
	setStripePriceEnv(t)

	priceIDs := []string{
		"price_base_m_v1", "price_base_a_v1",
		"price_premium_m_v1", "price_premium_a_v1",
		"price_premiumplus_m_v1", "price_premiumplus_a_v1",
		"price_base_m_v2", "price_base_a_v2",
		"price_premium_m_v2", "price_premium_a_v2",
		"price_premiumplus_m_v2", "price_premiumplus_a_v2",
		"", "price_unmapped",
	}

	for _, id := range priceIDs {
		mapPlan, mapAnnual := mapStripePriceIDToPlan(id)
		adminPlan, adminAnnual := planFromStripePriceID(id)

		// Translate the sentinels into a common shape before comparing.
		wantAdminPlan := mapPlan
		if mapPlan == "unknown" {
			wantAdminPlan = ""
		}
		if adminPlan != wantAdminPlan {
			t.Errorf("mappers disagree on plan for %q: map=%q admin=%q", id, mapPlan, adminPlan)
		}
		// Cadence only meaningful when a real plan resolved.
		if wantAdminPlan != "" && adminAnnual != mapAnnual {
			t.Errorf("mappers disagree on cadence for %q: map=%v admin=%v", id, mapAnnual, adminAnnual)
		}
	}
}
