package models

import (
	"encoding/json"
	"testing"
)

// Cases are pinned to the real production distribution (2,248,869 vehicles,
// surveyed Aug 2026). The counts are in the case names so that anyone tempted
// to collapse this into one "is it truthy" test can see how much data each
// branch covers, and that the polarity genuinely differs per field.

func TestRegistrationAndInsuranceValid(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{`"1" is valid, the old select listed Yes first (2,068,404 docs)`, "1", true},
		{`"2" is invalid (57,567 docs)`, "2", false},
		{`"true" is valid (120,976 docs)`, "true", true},
		{`"false" is invalid (1,922 docs)`, "false", false},
		{"empty is invalid", "", false},
		{"anything else is invalid", "yes", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (VehicleDetails{ValidRegistration: tt.value}).RegistrationValid(); got != tt.want {
				t.Errorf("RegistrationValid(%q) = %v, want %v", tt.value, got, tt.want)
			}
			if got := (VehicleDetails{ValidInsurance: tt.value}).InsuranceValid(); got != tt.want {
				t.Errorf("InsuranceValid(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestStolen(t *testing.T) {
	tests := []struct {
		name     string
		isStolen string
		isExempt string
		want     bool
	}{
		{`"1" is NOT stolen (2,027,625 docs)`, "1", "", false},
		{`"2" IS stolen, the old select listed No first (95,592 docs)`, "2", "", true},
		{`"true" is stolen (2,055 docs)`, "true", "", true},
		{`"false" is not stolen (34,681 docs)`, "false", "", false},
		{`empty is not stolen (86,074 docs)`, "", "", false},
		// The department dashboard wrote the opposite polarity. A numeric
		// IsExempt is that writer's signature.
		{`dept dashboard: "1" IS stolen (111 docs)`, "1", "2", true},
		{`dept dashboard: "2" is NOT stolen (2,731 docs)`, "2", "2", false},
		{`dept dashboard: explicit "true" still wins`, "true", "1", true},
		{`dept dashboard: explicit "false" still wins`, "false", "1", false},
		{`a modern IsExempt is not that signature`, "2", "false", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := VehicleDetails{IsStolen: tt.isStolen, IsExempt: tt.isExempt}
			if got := d.Stolen(); got != tt.want {
				t.Errorf("Stolen(isStolen=%q, isExempt=%q) = %v, want %v",
					tt.isStolen, tt.isExempt, got, tt.want)
			}
		})
	}
}

// The bug this all came from: one "is it truthy" test cannot serve both fields.
func TestStolenIsTheOppositePolarityToRegistration(t *testing.T) {
	d := VehicleDetails{ValidRegistration: "1", IsStolen: "1"}
	if !d.RegistrationValid() {
		t.Error(`ValidRegistration "1" should be valid`)
	}
	if d.Stolen() {
		t.Error(`IsStolen "1" should NOT be stolen`)
	}
}

func TestExempt(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"1", true},   // 202 docs
		{"2", false},  // 2,926 docs
		{"true", true},  // 2,437 docs
		{"false", false}, // 52,052 docs
		{"", false},      // 2,188,200 docs (field absent)
	}
	for _, tt := range tests {
		if got := (VehicleDetails{IsExempt: tt.value}).Exempt(); got != tt.want {
			t.Errorf("Exempt(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestNormalizeFlags(t *testing.T) {
	tests := []struct {
		name string
		in   VehicleDetails
		want VehicleDetails
	}{
		{
			name: "legacy record, valid and not stolen (the 90% case)",
			in:   VehicleDetails{ValidRegistration: "1", ValidInsurance: "1", IsStolen: "1"},
			want: VehicleDetails{ValidRegistration: "true", ValidInsurance: "true", IsStolen: "false", IsExempt: "false"},
		},
		{
			name: "legacy record, genuinely stolen",
			in:   VehicleDetails{ValidRegistration: "1", ValidInsurance: "1", IsStolen: "2"},
			want: VehicleDetails{ValidRegistration: "true", ValidInsurance: "true", IsStolen: "true", IsExempt: "false"},
		},
		{
			name: "legacy record, invalid registration and insurance",
			in:   VehicleDetails{ValidRegistration: "2", ValidInsurance: "2", IsStolen: "1"},
			want: VehicleDetails{ValidRegistration: "false", ValidInsurance: "false", IsStolen: "false", IsExempt: "false"},
		},
		{
			name: "department dashboard record keeps its own stolen polarity",
			in:   VehicleDetails{ValidRegistration: "1", ValidInsurance: "1", IsStolen: "2", IsExempt: "2"},
			want: VehicleDetails{ValidRegistration: "true", ValidInsurance: "true", IsStolen: "false", IsExempt: "false"},
		},
		{
			name: "department dashboard record that really is stolen and exempt",
			in:   VehicleDetails{ValidRegistration: "1", ValidInsurance: "1", IsStolen: "1", IsExempt: "1"},
			want: VehicleDetails{ValidRegistration: "true", ValidInsurance: "true", IsStolen: "true", IsExempt: "true"},
		},
		{
			name: "already canonical, left alone",
			in:   VehicleDetails{ValidRegistration: "true", ValidInsurance: "false", IsStolen: "true", IsExempt: "true"},
			want: VehicleDetails{ValidRegistration: "true", ValidInsurance: "false", IsStolen: "true", IsExempt: "true"},
		},
		{
			name: "empty record becomes explicit falses",
			in:   VehicleDetails{},
			want: VehicleDetails{ValidRegistration: "false", ValidInsurance: "false", IsStolen: "false", IsExempt: "false"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in
			got.NormalizeFlags()
			if got.ValidRegistration != tt.want.ValidRegistration ||
				got.ValidInsurance != tt.want.ValidInsurance ||
				got.IsStolen != tt.want.IsStolen ||
				got.IsExempt != tt.want.IsExempt {
				t.Errorf("NormalizeFlags()\n got reg=%q ins=%q stolen=%q exempt=%q\nwant reg=%q ins=%q stolen=%q exempt=%q",
					got.ValidRegistration, got.ValidInsurance, got.IsStolen, got.IsExempt,
					tt.want.ValidRegistration, tt.want.ValidInsurance, tt.want.IsStolen, tt.want.IsExempt)
			}
		})
	}
}

func TestNormalizeFlagsIsIdempotent(t *testing.T) {
	d := VehicleDetails{ValidRegistration: "1", ValidInsurance: "2", IsStolen: "2", IsExempt: "1"}
	d.NormalizeFlags()
	once := d
	d.NormalizeFlags()
	if d != once {
		t.Errorf("second NormalizeFlags changed the value: %+v -> %+v", once, d)
	}
}

// MarshalJSON is what lets the already-released mobile app read correct values
// without a new build, so it needs to hold for every read path.
func TestMarshalJSONEmitsCanonicalFlags(t *testing.T) {
	// The exact vehicle from the bug report: plate 28TWQ102 / VIN
	// BMDBY6CAVD7JMLUCA. Stored 1/1/2/2, which is one department dashboard
	// save: registration valid, insurance valid, not stolen, not exempt.
	d := VehicleDetails{
		Plate:             "28TWQ102",
		Vin:               "BMDBY6CAVD7JMLUCA",
		ValidRegistration: "1",
		ValidInsurance:    "1",
		IsStolen:          "2",
		IsExempt:          "2",
	}

	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := map[string]string{
		"validRegistration": "true",
		"validInsurance":    "true",
		"isStolen":          "false",
		"isExempt":          "false",
	}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("%s = %v, want %q", k, out[k], v)
		}
	}
	// Non-flag fields must survive untouched.
	if out["plate"] != "28TWQ102" {
		t.Errorf("plate = %v, want 28TWQ102", out["plate"])
	}
	if out["vin"] != "BMDBY6CAVD7JMLUCA" {
		t.Errorf("vin = %v, want BMDBY6CAVD7JMLUCA", out["vin"])
	}
}

// A Vehicle wraps VehicleDetails, so the hook has to fire through the wrapper
// and through slices of it, which is how the list endpoints serialize.
func TestMarshalJSONThroughVehicleAndSlice(t *testing.T) {
	vehicles := []Vehicle{
		{Details: VehicleDetails{ValidRegistration: "1", IsStolen: "2"}},
		{Details: VehicleDetails{ValidRegistration: "2", IsStolen: "1"}},
	}

	b, err := json.Marshal(vehicles)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out []struct {
		Vehicle struct {
			ValidRegistration string `json:"validRegistration"`
			IsStolen          string `json:"isStolen"`
		} `json:"vehicle"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out[0].Vehicle.ValidRegistration != "true" || out[0].Vehicle.IsStolen != "true" {
		t.Errorf("first vehicle = %+v, want registration true and stolen true", out[0].Vehicle)
	}
	if out[1].Vehicle.ValidRegistration != "false" || out[1].Vehicle.IsStolen != "false" {
		t.Errorf("second vehicle = %+v, want registration false and stolen false", out[1].Vehicle)
	}
}
