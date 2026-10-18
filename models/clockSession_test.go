package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// The bug these pin: a shift started from Discord /clock-in never receives a
// heartbeat, so the AFK rules swept it as "abandoned" a minute in and paid it
// for that minute. Discord shifts run until clock-out or the max session;
// website and mobile shifts keep the AFK rules unchanged.

func sessionFixture(source string, start time.Time) ClockSession {
	return ClockSession{
		Source:            source,
		StartedAt:         primitive.NewDateTimeFromTime(start),
		LastHeartbeatAt:   primitive.NewDateTimeFromTime(start),
		MaxSessionMinutes: 120,
		AfkGraceSeconds:   60,
	}
}

func TestClockSessionTracksAfk(t *testing.T) {
	web := sessionFixture("", time.Now())
	discord := sessionFixture(ClockSourceDiscord, time.Now())
	assert.True(t, web.TracksAfk())
	assert.False(t, discord.TracksAfk())
}

func TestClockSessionSweepStatus(t *testing.T) {
	start := time.Date(2026, 10, 17, 13, 9, 0, 0, time.UTC)

	t.Run("discord shift is not swept for missing heartbeats", func(t *testing.T) {
		s := sessionFixture(ClockSourceDiscord, start)
		assert.Equal(t, "", s.SweepStatus(start.Add(61*time.Second)))
		assert.Equal(t, "", s.SweepStatus(start.Add(119*time.Minute)))
	})

	t.Run("discord shift expires at the max session window", func(t *testing.T) {
		s := sessionFixture(ClockSourceDiscord, start)
		assert.Equal(t, "expired", s.SweepStatus(start.Add(120*time.Minute)))
	})

	t.Run("web shift is abandoned once the AFK grace passes", func(t *testing.T) {
		s := sessionFixture("", start)
		assert.Equal(t, "", s.SweepStatus(start.Add(59*time.Second)))
		assert.Equal(t, "abandoned", s.SweepStatus(start.Add(60*time.Second)))
	})

	t.Run("web shift with fresh heartbeats stays active", func(t *testing.T) {
		s := sessionFixture("", start)
		s.LastHeartbeatAt = primitive.NewDateTimeFromTime(start.Add(30 * time.Minute))
		assert.Equal(t, "", s.SweepStatus(start.Add(30*time.Minute+30*time.Second)))
	})

	t.Run("web shift past max session is expired, not abandoned", func(t *testing.T) {
		s := sessionFixture("", start)
		assert.Equal(t, "expired", s.SweepStatus(start.Add(3*time.Hour)))
	})
}

func TestClockSessionPayableUntil(t *testing.T) {
	start := time.Date(2026, 10, 17, 13, 9, 0, 0, time.UTC)

	t.Run("discord shift is paid to clock-out time", func(t *testing.T) {
		s := sessionFixture(ClockSourceDiscord, start)
		out := start.Add(45 * time.Minute)
		assert.WithinDuration(t, out, s.PayableUntil(out), 0)
	})

	t.Run("discord shift is capped at the max session", func(t *testing.T) {
		s := sessionFixture(ClockSourceDiscord, start)
		assert.WithinDuration(t, start.Add(120*time.Minute), s.PayableUntil(start.Add(5*time.Hour)), 0)
	})

	t.Run("web shift is capped at last heartbeat plus grace", func(t *testing.T) {
		s := sessionFixture("", start)
		assert.WithinDuration(t, start.Add(60*time.Second), s.PayableUntil(start.Add(45*time.Minute)), 0)
	})

	t.Run("web shift with no heartbeat recorded is capped only by max session", func(t *testing.T) {
		s := sessionFixture("", start)
		s.LastHeartbeatAt = 0
		out := start.Add(45 * time.Minute)
		assert.WithinDuration(t, out, s.PayableUntil(out), 0)
	})
}
