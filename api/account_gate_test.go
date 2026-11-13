package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"
)

// claimedUser is an ordinary account: an address and a password hash.
func claimedUser() *models.User {
	return &models.User{
		ID: primitive.NewObjectID().Hex(),
		Details: models.UserDetails{
			Username: "player", Email: "player@example.com",
			Password: "$2a$10$abcdefghijklmnopqrstuv",
		},
	}
}

func TestAccountSignInError(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	future := primitive.NewDateTimeFromTime(now.Add(48 * time.Hour))
	past := primitive.NewDateTimeFromTime(now.Add(-48 * time.Hour))

	suspended := claimedUser()
	suspended.Details.Suspension = &models.Suspension{Until: &future}

	lifted := claimedUser()
	lifted.Details.Suspension = &models.Suspension{Until: &past}

	permanent := claimedUser()
	permanent.Details.Suspension = &models.Suspension{}

	deactivated := claimedUser()
	deactivated.Details.IsDeactivated = true

	// A shell a developer integration provisioned and nobody has claimed: no
	// address, no password hash, and Claimed explicitly false. Nil reads as
	// claimed, because every account older than the Developer API has no such
	// field.
	notClaimed := false
	unclaimed := &models.User{
		ID:      primitive.NewObjectID().Hex(),
		Details: models.UserDetails{Claimed: &notClaimed},
	}

	cases := []struct {
		name       string
		user       *models.User
		wantStatus int
		wantIn     string
	}{
		{name: "an ordinary account may sign in", user: claimedUser()},
		{name: "a lifted suspension is no bar", user: lifted},
		{
			name: "an unclaimed shell is refused like a wrong password",
			user: unclaimed, wantStatus: http.StatusForbidden, wantIn: "invalid credentials",
		},
		{
			name: "no user at all is refused the same way",
			user: nil, wantStatus: http.StatusForbidden, wantIn: "invalid credentials",
		},
		{
			name: "a deactivated account is told so",
			user: deactivated, wantStatus: http.StatusUnauthorized, wantIn: "deactivated",
		},
		{
			name: "a suspension names the date it lifts",
			user: suspended, wantStatus: http.StatusUnauthorized, wantIn: "November 14, 2026",
		},
		{
			name: "a permanent suspension says so",
			user: permanent, wantStatus: http.StatusUnauthorized, wantIn: "permanently removed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, err := AccountSignInError(tc.user, now)
			if tc.wantStatus == 0 {
				if err != nil {
					t.Fatalf("expected sign-in to be allowed, got %d %v", status, err)
				}
				if status != 0 {
					t.Errorf("status: got %d want 0", status)
				}
				return
			}
			if err == nil {
				t.Fatal("expected sign-in to be refused")
			}
			if status != tc.wantStatus {
				t.Errorf("status: got %d want %d", status, tc.wantStatus)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("reason %q does not contain %q", err.Error(), tc.wantIn)
			}
		})
	}
}

// The exact wording of the deactivation refusal is load-bearing: the mobile
// app decides whether to show a route to support by looking for the word.
// See police-cad-app/utils/loginErrors.js.
func TestAccountSignInError_DeactivationWordingIsWhatTheAppMatches(t *testing.T) {
	user := claimedUser()
	user.Details.IsDeactivated = true
	_, err := AccountSignInError(user, time.Now())
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "deactivated") {
		t.Fatalf("the app keys off the word \"deactivated\": %v", err)
	}
}
