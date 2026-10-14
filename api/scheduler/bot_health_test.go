package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// The watchdog's two promises: exactly one alert per transition, and total
// silence about a bot we have never heard from. Both are easy to get wrong in
// a way nobody notices until the monitor cries wolf, so they are pinned here.

// captureAlerts points the alerts-website webhook at a local server and
// returns a function that drains whatever was posted. postDiscordAlert fires
// in a goroutine, so the drain waits briefly rather than reading immediately.
func captureAlerts(t *testing.T) func() []discordWebhookPayload {
	t.Helper()

	posted := make(chan discordWebhookPayload, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload discordWebhookPayload
		if err := json.Unmarshal(body, &payload); err == nil {
			posted <- payload
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	t.Setenv(websiteAlertsWebhookEnv, srv.URL)
	// Keep the cron channel out of it: nothing in these tests should reach it,
	// and an unset env makes that a silent no-op rather than a stray request.
	t.Setenv(cronAlertWebhookEnv, "")

	// Collect everything that lands inside a fixed settle window rather than
	// returning on the first post, so "exactly one alert" is a real assertion
	// and not a race we happened to win.
	return func() []discordWebhookPayload {
		var out []discordWebhookPayload
		settle := time.NewTimer(500 * time.Millisecond)
		defer settle.Stop()
		for {
			select {
			case p := <-posted:
				out = append(out, p)
			case <-settle.C:
				return out
			}
		}
	}
}

// drainQuiet asserts nothing was posted.
func drainQuiet(t *testing.T, drain func() []discordWebhookPayload) {
	t.Helper()
	assert.Empty(t, drain(), "no alert should have been posted")
}

func botHealthScheduler(botDB *mocks.BotHealthDatabase) *Scheduler {
	return &Scheduler{
		BotHealthDB: botDB,
		instanceID:  "test.1",
		jobStats:    map[string]*JobStat{},
	}
}

func TestCheckDiscordBotHealth_NeverHeartbeated_StaysQuiet(t *testing.T) {
	drain := captureAlerts(t)

	botDB := &mocks.BotHealthDatabase{}
	// No document at all: a fresh deploy, or a bot host that has not updated
	// yet. Nothing has ever been seen, so nothing is down.
	botDB.On("Get", mock.Anything).Return(nil, nil)

	assert.NoError(t, botHealthScheduler(botDB).checkDiscordBotHealth(context.Background()))
	botDB.AssertNotCalled(t, "ClaimTransition", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	drainQuiet(t, drain)
}

func TestCheckDiscordBotHealth_ZeroHeartbeatTimestamp_StaysQuiet(t *testing.T) {
	drain := captureAlerts(t)

	botDB := &mocks.BotHealthDatabase{}
	// A document exists but carries no heartbeat time. Same reasoning: there
	// is no healthy baseline to have fallen away from.
	botDB.On("Get", mock.Anything).Return(&models.BotHealth{ID: models.BotHealthDocID}, nil)

	assert.NoError(t, botHealthScheduler(botDB).checkDiscordBotHealth(context.Background()))
	botDB.AssertNotCalled(t, "ClaimTransition", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	drainQuiet(t, drain)
}

func TestCheckDiscordBotHealth_UpToDown_AlertsExactlyOnce(t *testing.T) {
	drain := captureAlerts(t)

	lastHeartbeat := time.Now().UTC().Add(-7 * time.Minute)
	botDB := &mocks.BotHealthDatabase{}
	botDB.On("Get", mock.Anything).Return(&models.BotHealth{
		ID:                models.BotHealthDocID,
		LastHeartbeatAt:   lastHeartbeat,
		ShardID:           0,
		GuildCount:        137,
		BotVersion:        "4.0.8",
		LastNotifiedState: models.BotHealthStateUp,
	}, nil)

	var gotFrom, gotTo string
	var gotDownSince time.Time
	botDB.On("ClaimTransition", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			gotFrom = args.Get(1).(string)
			gotTo = args.Get(2).(string)
			gotDownSince = args.Get(4).(time.Time)
		}).
		Return(true, nil)

	assert.NoError(t, botHealthScheduler(botDB).checkDiscordBotHealth(context.Background()))

	assert.Equal(t, models.BotHealthStateUp, gotFrom)
	assert.Equal(t, models.BotHealthStateDown, gotTo)
	assert.True(t, gotDownSince.Equal(lastHeartbeat),
		"the outage is measured from the last heartbeat, not from when we noticed")

	alerts := drain()
	assert.Len(t, alerts, 1, "exactly one alert per transition")
	assert.Len(t, alerts[0].Embeds, 1)
	embed := alerts[0].Embeds[0]
	assert.Equal(t, "Discord bot offline", embed.Title)
	assert.Contains(t, embed.Description, "stopped reporting in")
	assert.Contains(t, embed.Description, "Slash commands will fail")
	assert.Contains(t, embed.Description, "7 minutes")
	assert.NotContains(t, embed.Description, "```", "a status alert is prose, not a stack dump")
	assert.Equal(t, 0xef4444, embed.Color)
	assert.Contains(t, fieldValues(embed), "4.0.8", "the alert carries what the bot last told us")
}

func TestCheckDiscordBotHealth_AlreadyNotifiedDown_StaysQuiet(t *testing.T) {
	drain := captureAlerts(t)

	botDB := &mocks.BotHealthDatabase{}
	botDB.On("Get", mock.Anything).Return(&models.BotHealth{
		ID:                models.BotHealthDocID,
		LastHeartbeatAt:   time.Now().UTC().Add(-3 * time.Hour),
		LastNotifiedState: models.BotHealthStateDown,
		LastNotifiedAt:    time.Now().UTC().Add(-2 * time.Hour),
		DownSince:         time.Now().UTC().Add(-3 * time.Hour),
	}, nil)

	// Called every minute for as long as the outage lasts. One alert, not
	// sixty an hour.
	for i := 0; i < 3; i++ {
		assert.NoError(t, botHealthScheduler(botDB).checkDiscordBotHealth(context.Background()))
	}
	botDB.AssertNotCalled(t, "ClaimTransition", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	drainQuiet(t, drain)
}

func TestCheckDiscordBotHealth_TransitionClaimedElsewhere_StaysQuiet(t *testing.T) {
	drain := captureAlerts(t)

	botDB := &mocks.BotHealthDatabase{}
	botDB.On("Get", mock.Anything).Return(&models.BotHealth{
		ID:                models.BotHealthDocID,
		LastHeartbeatAt:   time.Now().UTC().Add(-10 * time.Minute),
		LastNotifiedState: models.BotHealthStateUp,
	}, nil)
	// Another dyno got the conditional update in first. This dyno saw the same
	// transition but must not double-post.
	botDB.On("ClaimTransition", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(false, nil)

	assert.NoError(t, botHealthScheduler(botDB).checkDiscordBotHealth(context.Background()))
	drainQuiet(t, drain)
}

func TestCheckDiscordBotHealth_DownToUp_PostsRecoveryWithDuration(t *testing.T) {
	drain := captureAlerts(t)

	now := time.Now().UTC()
	botDB := &mocks.BotHealthDatabase{}
	botDB.On("Get", mock.Anything).Return(&models.BotHealth{
		ID:                models.BotHealthDocID,
		LastHeartbeatAt:   now.Add(-20 * time.Second), // back, and reporting in
		GuildCount:        137,
		BotVersion:        "4.0.8",
		LastNotifiedState: models.BotHealthStateDown,
		LastNotifiedAt:    now.Add(-2 * time.Hour),
		DownSince:         now.Add(-2*time.Hour - 5*time.Minute),
	}, nil)

	var gotTo string
	var gotDownSince time.Time
	botDB.On("ClaimTransition", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			gotTo = args.Get(2).(string)
			gotDownSince = args.Get(4).(time.Time)
		}).
		Return(true, nil)

	assert.NoError(t, botHealthScheduler(botDB).checkDiscordBotHealth(context.Background()))

	assert.Equal(t, models.BotHealthStateUp, gotTo)
	assert.True(t, gotDownSince.IsZero(), "recovery clears the outage start")

	alerts := drain()
	assert.Len(t, alerts, 1)
	embed := alerts[0].Embeds[0]
	assert.Equal(t, "Discord bot back online", embed.Title)
	// From downSince to the heartbeat that brought it back: 2h 5m less the 20s
	// the heartbeat predates "now", rounded to the minute.
	assert.Contains(t, embed.Description, "down for 2 hours 5 minutes")
	assert.Equal(t, 0x22c55e, embed.Color)
}

func TestCheckDiscordBotHealth_MissingNotifiedState_TreatedAsHealthyBaseline(t *testing.T) {
	drain := captureAlerts(t)

	botDB := &mocks.BotHealthDatabase{}
	// A document with no notified state and a fresh heartbeat: the bot is
	// fine. We must not announce "back online" for something we never said
	// was down.
	botDB.On("Get", mock.Anything).Return(&models.BotHealth{
		ID:              models.BotHealthDocID,
		LastHeartbeatAt: time.Now().UTC().Add(-10 * time.Second),
	}, nil)

	assert.NoError(t, botHealthScheduler(botDB).checkDiscordBotHealth(context.Background()))
	botDB.AssertNotCalled(t, "ClaimTransition", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	drainQuiet(t, drain)
}

func TestCheckDiscordBotHealth_StaleBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		age       time.Duration
		wantClaim bool
	}{
		{"just inside the window", models.BotHeartbeatStaleAfter - 5*time.Second, false},
		{"just outside the window", models.BotHeartbeatStaleAfter + 5*time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drain := captureAlerts(t)

			botDB := &mocks.BotHealthDatabase{}
			botDB.On("Get", mock.Anything).Return(&models.BotHealth{
				ID:                models.BotHealthDocID,
				LastHeartbeatAt:   time.Now().UTC().Add(-tc.age),
				LastNotifiedState: models.BotHealthStateUp,
			}, nil)
			if tc.wantClaim {
				botDB.On("ClaimTransition", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(true, nil)
			}

			assert.NoError(t, botHealthScheduler(botDB).checkDiscordBotHealth(context.Background()))
			if tc.wantClaim {
				assert.Len(t, drain(), 1)
			} else {
				drainQuiet(t, drain)
			}
		})
	}
}

func TestCheckDiscordBotHealth_StoreErrorsSurfaceToTheJob(t *testing.T) {
	drain := captureAlerts(t)

	botDB := &mocks.BotHealthDatabase{}
	botDB.On("Get", mock.Anything).Return(nil, errors.New("mongo is unreachable"))

	// Returned, not swallowed: RunLocked records it and raises a cron alert,
	// which is how we find out the watchdog itself is broken.
	err := botHealthScheduler(botDB).checkDiscordBotHealth(context.Background())
	assert.Error(t, err)
	drainQuiet(t, drain)
}

func TestCheckDiscordBotHealth_NoDatabase_Errors(t *testing.T) {
	assert.Error(t, (&Scheduler{instanceID: "test.1"}).checkDiscordBotHealth(context.Background()))
}

func TestBotOutageDuration(t *testing.T) {
	recovered := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

	assert.Equal(t, 30*time.Minute, botOutageDuration(
		&models.BotHealth{DownSince: recovered.Add(-30 * time.Minute)}, recovered))

	// No downSince (a document from before that field existed): fall back to
	// when we announced the outage.
	assert.Equal(t, 20*time.Minute, botOutageDuration(
		&models.BotHealth{LastNotifiedAt: recovered.Add(-20 * time.Minute)}, recovered))

	// Nothing to measure from, and never negative.
	assert.Equal(t, time.Duration(0), botOutageDuration(&models.BotHealth{}, recovered))
	assert.Equal(t, time.Duration(0), botOutageDuration(
		&models.BotHealth{DownSince: recovered.Add(time.Hour)}, recovered))
}

func TestHumanizeBotDuration(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{10 * time.Second, "less than a minute"},
		{59 * time.Second, "less than a minute"},
		{time.Minute, "1 minute"},
		{7 * time.Minute, "7 minutes"},
		{7*time.Minute + 20*time.Second, "7 minutes"},
		{time.Hour, "1 hour"},
		{time.Hour + time.Minute, "1 hour 1 minute"},
		{2*time.Hour + 5*time.Minute, "2 hours 5 minutes"},
		{24 * time.Hour, "1 day"},
		{49 * time.Hour, "2 days 1 hour"},
	} {
		assert.Equal(t, tc.want, humanizeBotDuration(tc.in), "input %s", tc.in)
	}
}

func fieldValues(e discordEmbed) []string {
	out := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		out = append(out, f.Value)
	}
	return out
}

// The job is registered through RunLocked, so the distributed lock is part of
// the "alert once" story on a multi-dyno deploy: the dyno that loses the race
// for the lock does not even look at the document.
func TestRunLocked_BotHealth_SkipsWhenAnotherDynoHoldsTheLock(t *testing.T) {
	drain := captureAlerts(t)

	lockDB := &mocks.SchedulerLockDatabase{}
	lockDB.On("TryAcquireLock", mock.Anything, botHealthLockKey, "test.1", botHealthJobTTL).
		Return(false, nil)

	botDB := &mocks.BotHealthDatabase{}

	s := &Scheduler{BotHealthDB: botDB, LockDB: lockDB, instanceID: "test.1", jobStats: map[string]*JobStat{}}
	s.RunLocked(botHealthJobName, botHealthLockKey, botHealthJobTTL, s.checkDiscordBotHealth)

	botDB.AssertNotCalled(t, "Get", mock.Anything)
	lockDB.AssertExpectations(t)
	drainQuiet(t, drain)
}

func TestRunLocked_BotHealth_RunsAndReleasesTheLock(t *testing.T) {
	drain := captureAlerts(t)

	lockDB := &mocks.SchedulerLockDatabase{}
	lockDB.On("TryAcquireLock", mock.Anything, botHealthLockKey, "test.1", botHealthJobTTL).
		Return(true, nil)
	lockDB.On("ReleaseLock", mock.Anything, botHealthLockKey, "test.1").Return(nil)

	botDB := &mocks.BotHealthDatabase{}
	botDB.On("Get", mock.Anything).Return(nil, nil)

	s := &Scheduler{BotHealthDB: botDB, LockDB: lockDB, instanceID: "test.1", jobStats: map[string]*JobStat{}}
	s.RunLocked(botHealthJobName, botHealthLockKey, botHealthJobTTL, s.checkDiscordBotHealth)

	botDB.AssertExpectations(t)
	lockDB.AssertExpectations(t)
	drainQuiet(t, drain)

	stats := s.SnapshotJobStats()
	assert.Equal(t, int64(1), stats[botHealthJobName].SuccessCount)
}
