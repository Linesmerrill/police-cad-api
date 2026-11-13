package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/linesmerrill/police-cad-api/models"
)

// Whether an account may be signed into at all, independent of how the person
// proved they own it.
//
// There are now two ways in — a password, and a provider the account is linked
// to — and both have to refuse the same accounts. Keeping the three checks in
// one function is what makes that true by construction: a suspension that
// stops the password path but not Sign in with Google is not a smaller bug
// than no suspension at all, it is the same bug with a longer way round.

// errAccountUnclaimed is the refusal for a provisioned shell nobody has
// claimed. Deliberately the same wording a wrong password gets: an account a
// developer integration created is not the caller's to learn anything about.
var errAccountUnclaimed = errors.New("invalid credentials")

// errAccountDeactivated is the refusal for an account its owner deactivated.
// The wording is what the login screen shows, and the mobile app keys its
// "deactivated" branch off the word, so it is a fixed string rather than
// something to reword freely (see police-cad-app/utils/loginErrors.js).
var errAccountDeactivated = errors.New("account is deactivated. Please contact support to restore access")

// AccountSignInError reports why an account may not be signed into, with the
// status to answer with.
//
// Returns (0, nil) when sign-in may proceed.
//
// The status differs by case on purpose. An unclaimed account is answered like
// a failed credential check, because saying more would describe somebody else's
// account to whoever asked. Deactivation and suspension are only ever reached
// after ownership has been proved, so those say exactly what is wrong and when
// it lifts — a player who is not told assumes they have been hacked.
func AccountSignInError(user *models.User, now time.Time) (int, error) {
	if user == nil || !user.CanAuthenticate() {
		return http.StatusForbidden, errAccountUnclaimed
	}
	if user.Details.IsDeactivated {
		return http.StatusUnauthorized, errAccountDeactivated
	}
	// Expiry is computed here rather than flipped by a cron, so a suspension
	// lifts itself the moment it elapses. See SuspensionLoginError.
	if err := SuspensionLoginError(user.Details.Suspension, now); err != nil {
		return http.StatusUnauthorized, err
	}
	return 0, nil
}
