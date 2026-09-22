package api

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/linesmerrill/police-cad-api/models"
)

// suspensionDateFormat is how a lift date is written to a player. Spelled out
// rather than numeric, because the audience is largely children and "10/1"
// reads differently on either side of the Atlantic.
const suspensionDateFormat = "January 2, 2006"

// SuspensionLoginError returns the error a suspended account sees when it tries
// to sign in, or nil when the suspension is not in force.
//
// Enforcement is a single check here rather than a scheduled job: the mobile
// app re-authenticates with stored credentials roughly every ten minutes, and
// the website checks on login and again after auth, so a suspension takes hold
// within one token cycle and lifts itself when it expires.
//
// The message names the date deliberately. A player who is not told when they
// get back in contacts support, and at this age most of them assume they have
// been hacked.
func SuspensionLoginError(s *models.Suspension, now time.Time) error {
	if !s.InForce(now) {
		return nil
	}
	if s.Until == nil {
		return fmt.Errorf("this account has been permanently removed for breaching our Terms of Service")
	}
	return fmt.Errorf("this account is suspended until %s. Access returns automatically, you do not need to do anything",
		s.Until.Time().UTC().Format(suspensionDateFormat))
}

// unauthorizedBody builds the 401 response body. The reason is marshalled
// rather than interpolated: a message containing a quote would otherwise
// produce a malformed body, and these messages are now written for players to
// read rather than being fixed strings.
func unauthorizedBody(reason string) []byte {
	body, err := json.Marshal(map[string]string{
		"error":   "unauthorized",
		"message": reason,
	})
	if err != nil {
		return []byte(`{"error": "unauthorized", "message": "authentication failed"}`)
	}
	return body
}
