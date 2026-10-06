package templates

import (
	"strings"
	"testing"
	"time"
)

func TestDeletionTimeLeft(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{24 * time.Hour, "in 24 hours"},
		{23*time.Hour + 40*time.Minute, "in 24 hours"},
		{23*time.Hour + 5*time.Minute, "in 23 hours"},
		// The email that prompted this: sent at 03:00 UTC for a 04:17 UTC deadline.
		{77 * time.Minute, "in 1 hour"},
		{29 * time.Minute, "in under an hour"},
		{0, "in under an hour"},
		{-time.Hour, "in under an hour"},
	}
	for _, c := range cases {
		if got := DeletionTimeLeft(c.d); got != c.want {
			t.Errorf("DeletionTimeLeft(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestReminderEmailUsesTimeLeft(t *testing.T) {
	at := time.Date(2026, 10, 6, 4, 17, 0, 0, time.UTC)
	html := RenderCommunityPendingDeletionReminderEmail("Sam", "Test1234567", at, "in 1 hour")
	if !strings.Contains(html, "Last chance: Test1234567 deletes in 1 hour") {
		t.Error("heading should carry the real time left")
	}
	if strings.Contains(html, "24 hours") {
		t.Error("heading must not claim 24 hours")
	}
	if !strings.Contains(html, "Tue, Oct 6 2026 04:17 UTC") {
		t.Error("deadline should still be stated")
	}
}
