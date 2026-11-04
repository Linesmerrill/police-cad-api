package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linesmerrill/police-cad-api/databases"
)

// capturedAlert is one call to an alert helper, recorded instead of posted.
type capturedAlert struct {
	name   string
	detail string
	fields map[string]string
}

// captureGitHubAlerts swaps the alert seams and clears the in-memory streaks,
// so each test starts with nothing counted and nothing sent. Narrower than
// bot_health_test's captureAlerts, which exercises the webhook itself: here
// what matters is which helper was called, and how often.
func captureGitHubAlerts(t *testing.T) (warnings, crons *[]capturedAlert) {
	t.Helper()

	var warn, cron []capturedAlert
	previousWarn, previousCron := sendWarningAlertFn, sendCronAlertFn
	sendWarningAlertFn = func(instanceID, source string, detail error, fields map[string]string) {
		warn = append(warn, capturedAlert{name: source, detail: detail.Error(), fields: fields})
	}
	sendCronAlertFn = func(instanceID, jobName string, err error, fields map[string]string) {
		cron = append(cron, capturedAlert{name: jobName, detail: err.Error(), fields: fields})
	}

	previousStreaks := memoryGitHubFailures
	memoryGitHubFailures = newMemoryFailureStreaks()

	t.Cleanup(func() {
		sendWarningAlertFn, sendCronAlertFn = previousWarn, previousCron
		memoryGitHubFailures = previousStreaks
	})
	return &warn, &cron
}

// --- classification -------------------------------------------------------

func TestGitHubFailureIsTransient_ServerAndRateLimits(t *testing.T) {
	transient := []*githubStatusError{
		{StatusCode: 500, Status: "500 Internal Server Error"},
		{StatusCode: 502, Status: "502 Bad Gateway"},
		{StatusCode: 503, Status: "503 Service Unavailable"},
		{StatusCode: 504, Status: "504 Gateway Timeout"},
		{StatusCode: 429, Status: "429 Too Many Requests"},
		{StatusCode: 403, Status: "403 Forbidden", RateLimited: true},
	}
	for _, err := range transient {
		assert.True(t, githubFailureIsTransient(err),
			"%s is GitHub's problem and clears on its own", err.Status)
		// Still transient once the job has wrapped it with which call failed.
		wrapped := fmt.Errorf("failed to dispatch uptime.yml: %w", err)
		assert.True(t, githubFailureIsTransient(wrapped), "wrapping must not lose the classification")
	}
}

func TestGitHubFailureIsTransient_NeedsAHuman(t *testing.T) {
	permanent := []*githubStatusError{
		{StatusCode: 401, Status: "401 Unauthorized"},
		{StatusCode: 403, Status: "403 Forbidden"},
		{StatusCode: 404, Status: "404 Not Found"},
		{StatusCode: 422, Status: "422 Unprocessable Entity"},
		{StatusCode: 410, Status: "410 Gone"},
	}
	for _, err := range permanent {
		assert.False(t, githubFailureIsTransient(err),
			"%s means a token, a permission or a name is wrong, which waiting will not fix", err.Status)
	}
}

// A timeout, a reset and a refusal never reached GitHub, so there is nothing
// for a person to fix either.
func TestGitHubFailureIsTransient_NetworkErrors(t *testing.T) {
	timeout := &url.Error{Op: "Post", URL: "https://api.github.com/x", Err: context.DeadlineExceeded}
	for _, err := range []error{
		timeout,
		context.DeadlineExceeded,
		fmt.Errorf("failed to read head: %w", timeout),
		&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED},
		&url.Error{Op: "Get", Err: syscall.ECONNRESET},
		io.ErrUnexpectedEOF,
	} {
		assert.True(t, githubFailureIsTransient(err), "%v is a failure to reach GitHub", err)
	}

	assert.False(t, githubFailureIsTransient(nil))
	assert.False(t, githubFailureIsTransient(errors.New("github returned no sha for master")),
		"an answer we could not make sense of is not a network blip")
}

// GitHub reports both rate limits as 403, so only the headers or the body say
// whether a 403 is worth telling somebody about.
func TestNewGitHubStatusError_TellsRateLimitsFromRefusals(t *testing.T) {
	cases := []struct {
		name    string
		header  map[string]string
		body    string
		limited bool
	}{
		{name: "remaining zero", header: map[string]string{"X-RateLimit-Remaining": "0"}, limited: true},
		{name: "retry after", header: map[string]string{"Retry-After": "60"}, limited: true},
		{
			name:    "secondary limit in the body",
			body:    `{"message":"You have exceeded a secondary rate limit"}`,
			limited: true,
		},
		{
			name: "a real refusal",
			body: `{"message":"Resource not accessible by personal access token"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := &http.Response{
				StatusCode: http.StatusForbidden,
				Status:     "403 Forbidden",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}
			for k, v := range tc.header {
				res.Header.Set(k, v)
			}
			err := newGitHubStatusError(res)
			assert.Equal(t, tc.limited, err.RateLimited)
			assert.Equal(t, tc.limited, githubFailureIsTransient(err))
		})
	}
}

func TestGitHubStatusError_NeverCarriesTheToken(t *testing.T) {
	err := &githubStatusError{StatusCode: 401, Status: "401 Unauthorized"}
	assert.Equal(t, "github answered 401 Unauthorized", err.Error())
}

// --- the dispatch job -----------------------------------------------------

// GitHub answering 500 is the case that filled alerts-website. The tick must
// end clean, with nothing sent.
func TestStatusDispatch_ServerErrorRaisesNoAlert(t *testing.T) {
	fake := newFakeGitHub(t)
	warnings, crons := captureGitHubAlerts(t)
	fake.dispatchErr = http.StatusInternalServerError
	s := &Scheduler{}

	assert.NoError(t, s.dispatchStatusPageWorkflows(context.Background()),
		"a 500 from GitHub must not be reported as a cron failure")
	assert.Empty(t, *warnings, "one blip is not worth a warning")
	assert.Empty(t, *crons)
}

// A call that never got an answer is the same kind of nothing.
func TestStatusDispatch_TimeoutRaisesNoAlert(t *testing.T) {
	warnings, crons := captureGitHubAlerts(t)

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(slow.Close)

	previousURL, previousClient := githubAPIBaseURL, statusDispatchClient
	githubAPIBaseURL = slow.URL
	statusDispatchClient = &http.Client{Timeout: 40 * time.Millisecond}
	t.Cleanup(func() { githubAPIBaseURL, statusDispatchClient = previousURL, previousClient })

	t.Setenv(statusDispatchTokenEnv, "test-token")
	t.Setenv(statusDispatchRepoEnv, "owner/repo")
	t.Setenv(statusDispatchRefEnv, "master")

	s := &Scheduler{}
	err := s.dispatchStatusPageWorkflows(context.Background())
	assert.NoError(t, err, "a timeout reaching GitHub must not be reported as a cron failure")
	assert.Empty(t, *warnings)
	assert.Empty(t, *crons)
}

// An hour of GitHub failing means the status page really is behind, which is
// worth telling somebody once -- not twelve times, and not again on the
// thirteenth tick.
func TestStatusDispatch_AnHourOfFailuresWarnsOnce(t *testing.T) {
	fake := newFakeGitHub(t)
	warnings, crons := captureGitHubAlerts(t)
	fake.dispatchErr = http.StatusBadGateway
	s := &Scheduler{}

	for i := 0; i < statusDispatchTransientAlertAfter-1; i++ {
		require.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	}
	assert.Empty(t, *warnings, "eleven ticks is under the hour")

	require.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	require.Len(t, *warnings, 1, "the twelfth tick is an hour, and says so once")
	assert.Equal(t, statusDispatchStaleWarning, (*warnings)[0].detail)
	assert.Equal(t, statusDispatchJobName, (*warnings)[0].name)
	assert.Equal(t, "12", (*warnings)[0].fields["consecutive failures"])

	for i := 0; i < 5; i++ {
		require.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	}
	assert.Len(t, *warnings, 1, "one warning per streak, not one per tick")
	assert.Empty(t, *crons, "a long outage is amber, never a cron failure")
}

// A tick that worked ends the streak, so the next outage is judged on its own
// length rather than inheriting the last one's.
func TestStatusDispatch_SuccessResetsTheStreak(t *testing.T) {
	fake := newFakeGitHub(t)
	warnings, _ := captureGitHubAlerts(t)
	fake.dispatchErr = http.StatusInternalServerError
	s := &Scheduler{}

	for i := 0; i < statusDispatchTransientAlertAfter-1; i++ {
		require.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	}

	fake.dispatchErr = 0
	require.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	streak, err := memoryGitHubFailures.RecordFailure(context.Background(), statusDispatchStreakKey, time.Now())
	require.NoError(t, err)
	assert.Equal(t, 1, streak.Count, "the successful tick cleared the count")

	memoryGitHubFailures = newMemoryFailureStreaks()
	fake.dispatchErr = http.StatusInternalServerError
	for i := 0; i < statusDispatchTransientAlertAfter-1; i++ {
		require.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	}
	assert.Empty(t, *warnings, "eleven more failures after a success is still under the hour")
}

// The failures somebody has to act on keep behaving exactly as they did: the
// job returns the error, and RunLocked turns it into a Cron failure.
func TestStatusDispatch_BadTokenAndMissingRepoStillFail(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			fake := newFakeGitHub(t)
			warnings, _ := captureGitHubAlerts(t)
			fake.dispatchErr = status
			s := &Scheduler{}

			err := s.dispatchStatusPageWorkflows(context.Background())
			require.Error(t, err, "this one will not fix itself, so it must still alert")
			assert.Contains(t, err.Error(), "failed to dispatch "+statusUptimeWorkflow)
			assert.NotContains(t, err.Error(), "test-token")
			assert.Empty(t, *warnings, "it is a failure, not a degradation")
		})
	}
}

// The head read is the job's other GitHub call, and it is classified the same.
func TestStatusDispatch_HeadReadIsClassifiedToo(t *testing.T) {
	fake := newFakeGitHub(t)
	_, crons := captureGitHubAlerts(t)
	fake.headErr = http.StatusServiceUnavailable
	s := &Scheduler{}

	assert.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	assert.Empty(t, *crons)

	fake.headErr = http.StatusNotFound
	assert.Error(t, s.dispatchStatusPageWorkflows(context.Background()),
		"a repository or branch that is not there needs a person")
}

// --- the bot's status incident -------------------------------------------

func TestBotIncident_TransientGitHubDoesNotAlert(t *testing.T) {
	_, crons := captureGitHubAlerts(t)

	var status int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	previous := githubAPIBaseURL
	githubAPIBaseURL = server.URL
	t.Cleanup(func() { githubAPIBaseURL = previous })

	t.Setenv(statusDispatchTokenEnv, "test-token")
	t.Setenv(statusDispatchRepoEnv, "owner/repo")

	now := time.Now().UTC()

	status = http.StatusBadGateway
	reportBotIncidentOpened(context.Background(), "web.1", now.Add(-10*time.Minute), now)
	reportBotIncidentResolved(context.Background(), "web.1", 10*time.Minute)
	assert.Empty(t, *crons, "GitHub being down is not the bot health job failing")

	status = http.StatusUnauthorized
	reportBotIncidentOpened(context.Background(), "web.1", now.Add(-10*time.Minute), now)
	require.Len(t, *crons, 1, "a token we cannot use still has to be reported")
	assert.Equal(t, "status_incident_open", (*crons)[0].fields["phase"])

	reportBotIncidentResolved(context.Background(), "web.1", 10*time.Minute)
	require.Len(t, *crons, 2)
	assert.Equal(t, "status_incident_resolve", (*crons)[1].fields["phase"])
}

// --- the streak store -----------------------------------------------------

func TestMemoryFailureStreaks_CountsMarksAndClears(t *testing.T) {
	store := newMemoryFailureStreaks()
	ctx := context.Background()
	start := time.Now().UTC()

	first, err := store.RecordFailure(ctx, "k", start)
	require.NoError(t, err)
	assert.Equal(t, 1, first.Count)
	assert.Equal(t, start, first.FirstFailedAt)
	assert.False(t, first.Alerted())

	second, err := store.RecordFailure(ctx, "k", start.Add(5*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 2, second.Count)
	assert.Equal(t, start, second.FirstFailedAt, "the streak keeps the time it began")

	require.NoError(t, store.MarkAlerted(ctx, "k", start.Add(time.Hour)))
	third, err := store.RecordFailure(ctx, "k", start.Add(time.Hour))
	require.NoError(t, err)
	assert.True(t, third.Alerted())

	require.NoError(t, store.ClearFailures(ctx, "k"))
	after, err := store.RecordFailure(ctx, "k", start.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1, after.Count)
	assert.False(t, after.Alerted(), "a new streak has not been announced")
}

// The streak is only useful if it is the same streak after the job moves dyno,
// which is what the Mongo store is for. The scheduler must prefer it.
func TestScheduler_PrefersTheSharedStreakStore(t *testing.T) {
	shared := newMemoryFailureStreaks()
	s := &Scheduler{JobStateDB: shared}
	var _ databases.SchedulerJobStateDatabase = shared

	_, err := s.recordGitHubFailure(context.Background(), "k", time.Now())
	require.NoError(t, err)

	streak, err := shared.RecordFailure(context.Background(), "k", time.Now())
	require.NoError(t, err)
	assert.Equal(t, 2, streak.Count, "the scheduler counted into the shared store, not its own memory")
}
