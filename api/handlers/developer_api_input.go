package handlers

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"
)

// Input validation for the Developer API.
//
// Every value a third party sends passes through here before it can reach a
// database filter. The rule is narrow and absolute: a filter is built only from
// values this file has returned, and what it returns is always a freshly
// constructed scalar rather than the caller's own string.
//
// That last part is what makes the difference. Checking a value and then using
// the original is not the same as checking it and using what the check
// produced — the first leaves the raw input in the query, the second cannot.
// An ObjectID is re-serialized from the parsed value; an identifier is rebuilt
// from the characters that survived the allowlist. The caller's bytes never
// make it to the driver.
//
// Request bodies are decoded into structs whose fields are all `string`,
// `[]string` or `bool`. Nothing is decoded into `interface{}`, `bson.M` or a
// map, so a JSON object where a string belongs fails to decode and the handler
// answers 400 before any of this runs. These checks are the layer behind that.

// devReservedPrefix is the one character a value must never start with. Mongo
// gives it meaning in a document, and while a value in the position we use it
// is read literally, a value that looks like an operator has no legitimate
// reason to arrive and is cheaper to refuse than to reason about.
const devReservedPrefix = "$"

// Anchored patterns. Each one matches the WHOLE string or nothing: an unanchored
// pattern would pass a value that merely contains something acceptable.
var (
	devObjectIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{24}$`)
	devDayPattern      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	// FiveM licences and Steam ids are hex or decimal; Discord ids are a
	// snowflake. One permissive-but-bounded class covers all three, and each
	// provider narrows it further below.
	devIdentifierPattern = regexp.MustCompile(`^[a-z0-9_.:-]{1,128}$`)
	devDiscordIDPattern  = regexp.MustCompile(`^[0-9]{5,32}$`)
	devFiveMIDPattern    = regexp.MustCompile(`^[a-f0-9]{6,128}$`)
	devSteamIDPattern    = regexp.MustCompile(`^[a-z0-9]{6,64}$`)
	devExternalIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:\-]{1,128}$`)
)

// devMaxSearchTermLen bounds a free-text search term.
const devMaxSearchTermLen = 120

// devRematerialize builds a new string from the runes of a value that has
// already passed an allowlist.
//
// The point is not the characters, which are unchanged — it is the provenance.
// A value that was checked and then passed through is still the caller's
// string; a value rebuilt here is one this package constructed from runes it
// has individually seen and accepted. That distinction is what lets a reader,
// and a static analyser, follow where a query's contents actually came from.
//
// Only ever called after a check that bounds the character set, so it cannot
// launder anything: if the allowlist is right, every rune it copies is one the
// allowlist already permitted.
func devRematerialize(validated string) string {
	var b strings.Builder
	b.Grow(len(validated))
	for _, r := range validated {
		b.WriteRune(r)
	}
	return b.String()
}

// devSafeObjectID parses an id and returns its canonical hex form.
//
// The returned string comes from re-serializing the parsed ObjectID, so it is
// 24 lowercase hex characters by construction whatever arrived. Callers use the
// return value in filters, never their own input.
func devSafeObjectID(raw string) (string, primitive.ObjectID, error) {
	trimmed := strings.TrimSpace(raw)
	if !devObjectIDPattern.MatchString(trimmed) {
		return "", primitive.NilObjectID, fmt.Errorf("that id is not valid")
	}
	oid, err := primitive.ObjectIDFromHex(trimmed)
	if err != nil {
		return "", primitive.NilObjectID, fmt.Errorf("that id is not valid")
	}
	return oid.Hex(), oid, nil
}

// devSafeScalar checks a plain string that is about to be compared against a
// stored value.
//
// Used where the value has no fixed shape to validate against. It bounds the
// length, refuses control characters, and refuses anything beginning with the
// reserved prefix.
func devSafeScalar(raw string, maxLen int, what string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%s is required", what)
	}
	if len(trimmed) > maxLen {
		return "", fmt.Errorf("%s must be %d characters or fewer", what, maxLen)
	}
	if strings.HasPrefix(trimmed, devReservedPrefix) {
		return "", fmt.Errorf("%s is not valid", what)
	}
	if strings.ContainsFunc(trimmed, func(r rune) bool { return r < 32 || r == 127 }) {
		return "", fmt.Errorf("%s must not contain control characters", what)
	}
	return devRematerialize(trimmed), nil
}

// devSafeProvider validates a game identity provider against the allowlist.
func devSafeProvider(raw string) (string, error) {
	provider := strings.ToLower(strings.TrimSpace(raw))
	switch provider {
	case models.PlayerProviderFiveM:
		return models.PlayerProviderFiveM, nil
	case models.PlayerProviderDiscord:
		return models.PlayerProviderDiscord, nil
	case models.PlayerProviderSteam:
		return models.PlayerProviderSteam, nil
	}
	// Returning our own constant rather than the caller's string means the
	// value in a filter is one of exactly three known literals.
	return "", fmt.Errorf("provider must be one of: fivem, discord, steam")
}

// devSafeIdentifier normalizes a game identifier and checks it against the
// pattern for its provider.
//
// Normalization runs first, because the stored form is the normalized one and
// validating the raw form would accept values that normalize into something
// else. The returned string is the normalized, validated value.
func devSafeIdentifier(provider, raw string) (string, error) {
	identifier := models.NormalizePlayerIdentifier(provider, raw)
	if identifier == "" {
		return "", fmt.Errorf("identifier is required")
	}
	if !devIdentifierPattern.MatchString(identifier) {
		return "", fmt.Errorf("that identifier is not valid")
	}

	switch provider {
	case models.PlayerProviderDiscord:
		if !devDiscordIDPattern.MatchString(identifier) {
			return "", fmt.Errorf("a Discord id is digits only")
		}
	case models.PlayerProviderFiveM:
		if !devFiveMIDPattern.MatchString(identifier) {
			return "", fmt.Errorf("a FiveM licence is hexadecimal")
		}
	case models.PlayerProviderSteam:
		if !devSteamIDPattern.MatchString(identifier) {
			return "", fmt.Errorf("that Steam id is not valid")
		}
	}
	return devRematerialize(identifier), nil
}

// devSafeExternalID validates an integration's own identifier for a record.
//
// Deliberately a narrow character class rather than "anything printable". An
// external id is a machine-generated handle, so a restrictive class costs a
// developer nothing and leaves nothing to reason about.
func devSafeExternalID(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("externalId is required: it is what makes these writes idempotent")
	}
	if !devExternalIDPattern.MatchString(trimmed) {
		return "", fmt.Errorf("externalId may contain letters, digits, and . _ : - only, up to 128 characters")
	}
	return devRematerialize(trimmed), nil
}

// devSafeDay validates a YYYY-MM-DD date used to bound a usage query, and
// returns it re-formatted from the parsed date.
//
// Parsing and re-formatting rather than returning the matched input, for the
// same reason an id is re-serialized: the returned value is then built from the
// parsed date rather than from the caller's bytes.
//
// It also catches what the pattern alone cannot. "2026-13-45" is the right
// shape and not a date, and a range bound that is not a date would quietly
// match nothing rather than erroring.
func devSafeDay(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if !devDayPattern.MatchString(trimmed) {
		return "", fmt.Errorf("dates are YYYY-MM-DD")
	}
	parsed, err := time.Parse(devDayLayout, trimmed)
	if err != nil {
		return "", fmt.Errorf("that is not a real date")
	}
	return parsed.Format(devDayLayout), nil
}

// devDayLayout is the single spelling of the day format, so the parse and the
// format cannot drift apart.
const devDayLayout = "2006-01-02"

// devSafeSearchTerm validates a free-text search term.
//
// Bounded and operator-free here; the caller still escapes it before it becomes
// part of a regular expression, which is a separate concern — this stops an
// oversized or operator-shaped term, that stops a term with regex meaning.
func devSafeSearchTerm(raw, what string) (string, error) {
	return devSafeScalar(raw, devMaxSearchTermLen, what)
}

// devEq builds an explicit equality condition.
//
// `{"field": {"$eq": v}}` rather than `{"field": v}`. The two behave
// identically for a string, and the explicit form states at the call site that
// equality is the whole of the comparison, which is the property that matters
// when the value came from outside.
func devEq(value interface{}) bson.M {
	return bson.M{"$eq": value}
}

// devEqAny builds an explicit membership condition over a fixed, internal set.
//
// The values here are always our own constants, never caller input; this exists
// so a filter never needs a bare `$in` written inline next to a validated
// value.
func devEqAny(values ...string) bson.M {
	return bson.M{"$in": values}
}
