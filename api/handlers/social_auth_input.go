package handlers

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/linesmerrill/police-cad-api/models"
)

// Input validation for the social sign-in surface.
//
// Same rule as the Developer API's validators, for the same reason: a filter is
// built only from values this file returned, and what it returns is always a
// freshly constructed scalar rather than the caller's own string. Checking a
// value and then using the original leaves the raw input in the query; checking
// it and using what the check produced cannot.
//
// The values here arrive from two places, and both are outside our control. A
// request body is ours to shape, so those structs are all `string` and `bool`
// fields and a JSON object where a string belongs fails to decode. A token's
// claims are not: `claims` is a map of `interface{}`, so a provider — or
// anything pretending to be one before the signature check — can put an object
// or an array where a string belongs. That is why the claim validators take
// `interface{}` and refuse anything that is not a string, rather than taking a
// string and trusting a cast upstream.

var (
	// A provider subject is opaque and provider-defined. Google's is decimal
	// digits, Apple's is digits with a dotted suffix. Rather than encode either
	// shape, this allows the conservative set both live inside and bounds the
	// length, so a subject stays something that can only ever be compared.
	authSubjectPattern = regexp.MustCompile(`^[A-Za-z0-9_.|:-]{1,255}$`)
	authEmailPattern   = regexp.MustCompile(`^[^@\s]{1,128}@[A-Za-z0-9.-]{1,128}\.[A-Za-z]{2,24}$`)
	// A nonce is ours: we generate it, so we can insist on the shape we emit.
	authNoncePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
)

const authMaxDisplayNameLen = 120

// safeAuthProvider resolves a provider name to one of our own constants.
//
// Returning the constant rather than the caller's string means the value that
// reaches a filter is one of exactly two known literals.
func safeAuthProvider(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case models.AuthProviderGoogle:
		return models.AuthProviderGoogle, nil
	case models.AuthProviderApple:
		return models.AuthProviderApple, nil
	}
	return "", fmt.Errorf("provider must be google or apple")
}

// safeAuthSubject validates a `sub` claim.
//
// Takes interface{} because that is how a claim arrives. A claim that is an
// object, an array or a number is refused rather than coerced: a subject we
// cannot read as a string is not a subject, and guessing at one would be
// guessing at an identity.
func safeAuthSubject(raw interface{}) (string, error) {
	subject, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("token subject is not valid")
	}
	trimmed := strings.TrimSpace(subject)
	if trimmed == "" {
		return "", fmt.Errorf("token has no subject")
	}
	if strings.HasPrefix(trimmed, devReservedPrefix) {
		return "", fmt.Errorf("token subject is not valid")
	}
	if !authSubjectPattern.MatchString(trimmed) {
		return "", fmt.Errorf("token subject is not valid")
	}
	return devRematerialize(trimmed), nil
}

// safeAuthEmail validates an address that is about to be compared against a
// stored one.
//
// Lowercased first, because the stored form is lowercased and validating the
// raw form would accept a value that normalizes into something else.
func safeAuthEmail(raw string) (string, error) {
	email := normalizeAuthEmail(raw)
	if email == "" {
		return "", fmt.Errorf("no email address")
	}
	if strings.HasPrefix(email, devReservedPrefix) {
		return "", fmt.Errorf("that email address is not valid")
	}
	if !authEmailPattern.MatchString(email) {
		return "", fmt.Errorf("that email address is not valid")
	}
	return devRematerialize(email), nil
}

// normalizeAuthEmail is the single spelling of how an address is stored and
// compared, so a write and a later read cannot disagree.
func normalizeAuthEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// safeAuthNonce validates the nonce echoed back to us.
func safeAuthNonce(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("this sign-in is missing its nonce")
	}
	if !authNoncePattern.MatchString(trimmed) {
		return "", fmt.Errorf("this sign-in is missing its nonce")
	}
	return devRematerialize(trimmed), nil
}

// safeAuthDisplayName cleans a name claim for storage.
//
// Never an error: a name is cosmetic, and Apple omits it on every sign-in after
// the first. A claim we cannot read, or one that is empty or oversized, simply
// yields no name — which the caller must already handle, because that is the
// normal case for a returning Apple user.
func safeAuthDisplayName(raw interface{}) string {
	name, ok := raw.(string)
	if !ok {
		return ""
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || len(trimmed) > authMaxDisplayNameLen {
		return ""
	}
	if strings.ContainsFunc(trimmed, func(r rune) bool { return r < 32 || r == 127 }) {
		return ""
	}
	return devRematerialize(trimmed)
}
