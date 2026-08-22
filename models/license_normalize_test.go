package models

import (
	"encoding/json"
	"testing"
)

// Cases are pinned to the real production distribution (417,482 licenses,
// surveyed Aug 2026). The counts are in the case names so the scale behind
// each branch stays visible.

func TestResolvedKeys(t *testing.T) {
	tests := []struct {
		name                          string
		in                            LicenseDetails
		wantType, wantNotes, wantCivi string
	}{
		{
			name:      "modern shape uses the canonical keys (101,225 docs)",
			in:        LicenseDetails{Type: "Driver License", Notes: "n", CivilianID: "abc"},
			wantType:  "Driver License",
			wantNotes: "n",
			wantCivi:  "abc",
		},
		{
			name:      "legacy shape falls back to its own keys (316,257 docs)",
			in:        LicenseDetails{LicenseType: "driver license", AdditionalNotes: "n", OwnerID: "abc"},
			wantType:  "driver license",
			wantNotes: "n",
			wantCivi:  "abc",
		},
		{
			name:      "canonical wins when both are somehow present",
			in:        LicenseDetails{Type: "New", LicenseType: "old", CivilianID: "new", OwnerID: "old"},
			wantType:  "New",
			wantNotes: "",
			wantCivi:  "new",
		},
		{
			name:      "a blank canonical key is not preferred over a populated legacy one",
			in:        LicenseDetails{Type: "   ", LicenseType: "driver license"},
			wantType:  "driver license",
			wantNotes: "",
			wantCivi:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.ResolvedType(); got != tt.wantType {
				t.Errorf("ResolvedType() = %q, want %q", got, tt.wantType)
			}
			if got := tt.in.ResolvedNotes(); got != tt.wantNotes {
				t.Errorf("ResolvedNotes() = %q, want %q", got, tt.wantNotes)
			}
			if got := tt.in.ResolvedCivilianID(); got != tt.wantCivi {
				t.Errorf("ResolvedCivilianID() = %q, want %q", got, tt.wantCivi)
			}
		})
	}
}

func TestNormalizedStatus(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"lowercase valid, the legacy website's default (305,274 docs)", "valid", LicenseStatusValid},
		{"already canonical", "Valid", LicenseStatusValid},
		{"approved, written only by the API (71,817 docs)", "approved", LicenseStatusApproved},
		{"suspended (15,527 docs)", "suspended", LicenseStatusSuspended},
		{"pending (14,793 docs)", "pending", LicenseStatusPending},
		{"revoked (5,113 docs)", "revoked", LicenseStatusRevoked},
		{"expired, legacy dashboards only (4,958 docs)", "expired", LicenseStatusExpired},
		{"surrounding whitespace is tolerated", "  valid  ", LicenseStatusValid},
		{"shouty input still maps", "REVOKED", LicenseStatusRevoked},
		{"an unknown status is left alone, not guessed at", "Provisional", "Provisional"},
		{"empty stays empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (LicenseDetails{Status: tt.in}).NormalizedStatus(); got != tt.want {
				t.Errorf("NormalizedStatus(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Valid and Approved are semantically close but are never merged — that would
// rewrite what 71,817 records mean.
func TestNormalizedStatusDoesNotMergeValidAndApproved(t *testing.T) {
	if (LicenseDetails{Status: "approved"}).NormalizedStatus() == LicenseStatusValid {
		t.Error("Approved must not collapse into Valid")
	}
}

func TestNormalizedExpiration(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"canonical passes through (345,709 docs)", "2033-11-11", "2033-11-11"},
		{"mobile's mm/dd/yyyy (70,851 docs)", "01/01/1995", "1995-01-01"},
		{"single digit month and day", "3/18/2029", "2029-03-18"},
		{"two digit year resolves forward", "11/23/27", "2027-11-23"},
		{"single digit with two digit year", "5/17/31", "2031-05-17"},
		{"zero padded two digit year", "05/07/28", "2028-05-07"},
		{"ISO datetime keeps only the date", "2027-04-01T00:00:00Z", "2027-04-01"},
		{"a bare year is ambiguous and is left alone", "2045", "2045"},
		{"a month/year pair is ambiguous and is left alone", "11/25", "11/25"},
		{"an impossible date is left alone rather than rolled over", "02/30/2025", "02/30/2025"},
		{"free text is left alone", "whenever", "whenever"},
		{"empty stays empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (LicenseDetails{ExpirationDate: tt.in}).NormalizedExpiration(); got != tt.want {
				t.Errorf("NormalizedExpiration(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeFieldsIsIdempotent(t *testing.T) {
	d := LicenseDetails{
		LicenseType:     "driver license",
		AdditionalNotes: "renewed",
		OwnerID:         "64bda9599e282f000235e861",
		Status:          "valid",
		ExpirationDate:  "11/23/27",
	}

	d.NormalizeFields()
	once := d
	d.NormalizeFields()

	if d != once {
		t.Errorf("second NormalizeFields changed the value:\n first: %+v\nsecond: %+v", once, d)
	}
	if d.Type != "driver license" || d.Notes != "renewed" || d.CivilianID != "64bda9599e282f000235e861" {
		t.Errorf("legacy keys did not land in their canonical homes: %+v", d)
	}
	if d.Status != LicenseStatusValid || d.ExpirationDate != "2027-11-23" {
		t.Errorf("status/expiration not canonical: %+v", d)
	}
}

// The legacy keys stay populated on purpose: the website's police and dispatch
// dashboards still read them straight out of Mongo.
func TestNormalizeFieldsKeepsLegacyKeys(t *testing.T) {
	d := LicenseDetails{LicenseType: "driver license", OwnerID: "abc", AdditionalNotes: "n"}
	d.NormalizeFields()

	if d.LicenseType == "" || d.OwnerID == "" || d.AdditionalNotes == "" {
		t.Errorf("NormalizeFields must not clear the legacy keys, got %+v", d)
	}
}

func TestMarshalJSONEmitsCanonicalShape(t *testing.T) {
	lic := License{Details: LicenseDetails{
		LicenseType:       "driver license",
		AdditionalNotes:   "renewed",
		OwnerID:           "64bda9599e282f000235e861",
		OwnerName:         "jackson dean",
		ActiveCommunityID: "64754da6e18e8900027e1fc5",
		Status:            "valid",
		ExpirationDate:    "11/23/27",
	}}

	raw, err := json.Marshal(lic)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got struct {
		License map[string]interface{} `json:"license"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for key, want := range map[string]string{
		"type":           "driver license",
		"notes":          "renewed",
		"civilianID":     "64bda9599e282f000235e861",
		"status":         LicenseStatusValid,
		"expirationDate": "2027-11-23",
	} {
		if got.License[key] != want {
			t.Errorf("license.%s = %v, want %q", key, got.License[key], want)
		}
	}

	// One home per value: the duplicated legacy keys are suppressed on the way out.
	for _, key := range []string{"licenseType", "additionalNotes", "ownerID"} {
		if _, present := got.License[key]; present {
			t.Errorf("license.%s should not be emitted, clients would have two places to look", key)
		}
	}

	// These have no canonical twin and must survive.
	if got.License["ownerName"] != "jackson dean" {
		t.Errorf("ownerName was dropped: %v", got.License["ownerName"])
	}
	if got.License["activeCommunityID"] != "64754da6e18e8900027e1fc5" {
		t.Errorf("activeCommunityID was dropped: %v", got.License["activeCommunityID"])
	}
}

// The list endpoint marshals a slice, so the hook has to fire per element.
func TestMarshalJSONAppliesToSlices(t *testing.T) {
	licenses := []License{
		{Details: LicenseDetails{LicenseType: "weapon license", Status: "suspended"}},
		{Details: LicenseDetails{Type: "Pilot License", Status: "Revoked"}},
	}

	raw, err := json.Marshal(licenses)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got []struct {
		License struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"license"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got[0].License.Type != "weapon license" || got[0].License.Status != LicenseStatusSuspended {
		t.Errorf("legacy element not normalized: %+v", got[0].License)
	}
	if got[1].License.Type != "Pilot License" || got[1].License.Status != LicenseStatusRevoked {
		t.Errorf("modern element changed unexpectedly: %+v", got[1].License)
	}
}
