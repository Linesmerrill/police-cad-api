package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func susUntil(at time.Time) *primitive.DateTime {
	d := primitive.NewDateTimeFromTime(at)
	return &d
}

func TestSuspensionLoginError(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

	t.Run("no suspension lets the login through", func(t *testing.T) {
		if err := SuspensionLoginError(nil, now); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("an elapsed suspension lifts itself without a cron", func(t *testing.T) {
		s := &models.Suspension{Until: susUntil(now.Add(-time.Second))}
		if err := SuspensionLoginError(s, now); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("a running suspension names the date access returns", func(t *testing.T) {
		s := &models.Suspension{Until: susUntil(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))}
		err := SuspensionLoginError(s, now)
		if err == nil {
			t.Fatal("expected the login to be refused")
		}
		if !strings.Contains(err.Error(), "October 1, 2026") {
			t.Errorf("message does not name the lift date: %q", err)
		}
		// A player who is not told they need do nothing will contact support.
		if !strings.Contains(err.Error(), "do not need to do anything") {
			t.Errorf("message does not say access returns on its own: %q", err)
		}
	})

	t.Run("a permanent suspension says so without inventing a date", func(t *testing.T) {
		err := SuspensionLoginError(&models.Suspension{}, now)
		if err == nil {
			t.Fatal("expected the login to be refused")
		}
		if !strings.Contains(err.Error(), "permanently removed") {
			t.Errorf("message = %q", err)
		}
		if strings.Contains(err.Error(), "1, 1970") || strings.Contains(err.Error(), "until") {
			t.Errorf("a permanent suspension must not render a lift date: %q", err)
		}
	})
}

// The reason now carries text written for a player rather than a fixed string,
// so it has to survive being put in a JSON body.
func TestUnauthorizedBody(t *testing.T) {
	tests := []struct {
		name   string
		reason string
	}{
		{"plain", "invalid credentials"},
		{"with a suspension date", "this account is suspended until October 1, 2026."},
		{"with quotes", `he said "no" \ then left`},
		{"with a newline", "line one\nline two"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := unauthorizedBody(tt.reason)

			var got map[string]string
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("body is not valid JSON: %v (%s)", err, body)
			}
			if got["message"] != tt.reason {
				t.Errorf("message = %q, want %q", got["message"], tt.reason)
			}
			if got["error"] != "unauthorized" {
				t.Errorf("error = %q, want unauthorized", got["error"])
			}
		})
	}
}
