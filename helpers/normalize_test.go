package helpers

import "testing"

func TestNormalizeForSearch(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"ascii unchanged", "john doe", "john doe"},
		{"uppercase lowered", "John Doe", "john doe"},
		{"czech diacritics", "řáč", "rac"},
		{"full name with diacritics", "Řáč Dude", "rac dude"},
		{"accented e", "René", "rene"},
		{"german umlaut", "Björk", "bjork"},
		{"spanish tilde", "Señor", "senor"},
		{"mixed ascii and diacritics", "José García", "jose garcia"},
		{"no diacritics passthrough", "plain text 123", "plain text 123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeForSearch(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeForSearch(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
