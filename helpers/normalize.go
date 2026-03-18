package helpers

import (
	"strings"
	"unicode"

	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// NormalizeForSearch strips diacritics/accents and lowercases the input string.
// e.g., "Řáč Dude" -> "rac dude"
func NormalizeForSearch(s string) string {
	t := transform.Chain(norm.NFD, transform.RemoveFunc(func(r rune) bool {
		return unicode.Is(unicode.Mn, r) // Mn: nonspacing marks (accents/diacritics)
	}), norm.NFC)
	result, _, _ := transform.String(t, s)
	return strings.ToLower(result)
}
