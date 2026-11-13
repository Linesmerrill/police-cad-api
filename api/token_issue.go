package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/shaj13/go-guardian/auth"
	"github.com/shaj13/go-guardian/auth/strategies/bearer"
)

// Minting a bearer token.
//
// Extracted from CreateToken so the password login and the social sign-in
// issue the same thing in the same way. A second spelling of this would be a
// second set of rules about what a session is.

// IssueBearerToken mints a bearer token for an account and persists it in the
// token store, returning the token.
//
// r is passed through to the store because go-guardian's Append takes the
// request; nothing is read off it here, and in particular nothing about the
// caller contributes to the token.
//
// A failure to persist is returned rather than swallowed. A token that was
// never stored authenticates nothing, so handing one to a client produces a
// session where every subsequent request is a 401 — which looks to the player
// like the app is broken rather than like a sign-in that failed.
func IssueBearerToken(r *http.Request, email, userID string) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", errors.New("cannot issue a token without an account")
	}
	if authenticator == nil {
		return "", errors.New("authentication is not set up")
	}
	strategy := authenticator.Strategy(bearer.CachedStrategyKey)
	if strategy == nil {
		return "", errors.New("the bearer strategy is not enabled")
	}
	token := uuid.New().String()
	if err := auth.Append(strategy, token, auth.NewDefaultUser(email, userID, nil, nil), r); err != nil {
		return "", fmt.Errorf("storing the new token: %w", err)
	}
	return token, nil
}
