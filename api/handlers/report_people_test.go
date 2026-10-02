package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNamesLookAlike(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"TROPICAL-RP", "TROPICAL RP  🏝️", true}, // punctuation, spacing and emoji ignored
		{"Los Santos RP", "los santos rp", true},
		{"Liberty County", "Liberty County Roleplay", true}, // contains
		{"San Andreas", "San Andraes", true},                // one typo
		{"PD", "LSPD Roleplay", false},                      // too short to contain-match
		{"Tropical RP", "Arctic RP", false},
		{"", "anything", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, namesLookAlike(c.a, c.b), "%q vs %q", c.a, c.b)
	}
}

func TestFindNameMatches_PairsUsernamesCommunitiesAndClaimedName(t *testing.T) {
	reporter := &reportPerson{
		Username: "Tropical-Piton11",
		Owned:    []reportPersonCommunity{{Name: "TROPICAL-RP"}},
	}
	reported := &reportPerson{
		Username: "1k-01 | government",
		Owned:    []reportPersonCommunity{{Name: "TROPICAL RP  🏝️"}},
	}
	matches := findNameMatches(reporter, reported, "1k-01")

	assert.Contains(t, matches, reportNameMatch{
		ReporterName: "TROPICAL-RP", ReporterKind: "community",
		ReportedName: "TROPICAL RP  🏝️", ReportedKind: "community",
	})
	assert.Contains(t, matches, reportNameMatch{
		ReporterName: "1k-01", ReporterKind: "claimed",
		ReportedName: "1k-01 | government", ReportedKind: "username",
	})
}

func TestFindNameMatches_NilPeople(t *testing.T) {
	assert.Empty(t, findNameMatches(nil, nil, ""))
	assert.Empty(t, findNameMatches(&reportPerson{Username: "a"}, nil, "a"))
}
