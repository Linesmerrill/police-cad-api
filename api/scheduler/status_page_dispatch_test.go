package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// fakeGitHub stands in for the REST API: it records the workflows that were
// dispatched and serves whatever head sha the test sets.
type fakeGitHub struct {
	mu          sync.Mutex
	dispatched  []string
	refs        []string
	tokens      []string
	head        string
	dispatchErr int
	headErr     int
}

func (f *fakeGitHub) record(workflow, ref, token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatched = append(f.dispatched, workflow)
	f.refs = append(f.refs, ref)
	f.tokens = append(f.tokens, token)
}

func (f *fakeGitHub) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.dispatched...)
}

func (f *fakeGitHub) setHead(sha string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.head = sha
}

// newFakeGitHub points the job at a local server for the duration of the test
// and clears the job's remembered state, which is package level.
func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	fake := &fakeGitHub{head: "sha-one"}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/actions/workflows/", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Ref string `json:"ref"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		workflow := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/repos/owner/repo/actions/workflows/"), "/dispatches")
		fake.record(workflow, body.Ref, r.Header.Get("Authorization"))
		if fake.dispatchErr != 0 {
			w.WriteHeader(fake.dispatchErr)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/repos/owner/repo/commits/", func(w http.ResponseWriter, r *http.Request) {
		if fake.headErr != 0 {
			w.WriteHeader(fake.headErr)
			return
		}
		fake.mu.Lock()
		head := fake.head
		fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"sha":%q}`, head)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	previous := githubAPIBaseURL
	githubAPIBaseURL = server.URL
	t.Cleanup(func() { githubAPIBaseURL = previous })

	t.Setenv(statusDispatchTokenEnv, "test-token")
	t.Setenv(statusDispatchRepoEnv, "owner/repo")
	t.Setenv(statusDispatchRefEnv, "master")

	statusDispatchState.Lock()
	statusDispatchState.lastSeenSHA = ""
	statusDispatchState.lastSiteDispatch = time.Time{}
	statusDispatchState.Unlock()
	t.Cleanup(func() {
		statusDispatchState.Lock()
		statusDispatchState.lastSeenSHA = ""
		statusDispatchState.lastSiteDispatch = time.Time{}
		statusDispatchState.Unlock()
	})

	return fake
}

func TestStatusDispatchConfigured(t *testing.T) {
	t.Setenv(statusDispatchTokenEnv, "")
	assert.False(t, statusDispatchConfigured(), "no token means the job must not register")
	t.Setenv(statusDispatchTokenEnv, "  ")
	assert.False(t, statusDispatchConfigured(), "whitespace is not a token")
	t.Setenv(statusDispatchTokenEnv, "tok")
	assert.True(t, statusDispatchConfigured())
}

func TestStatusDispatchDefaults(t *testing.T) {
	t.Setenv(statusDispatchRepoEnv, "")
	t.Setenv(statusDispatchRefEnv, "")
	assert.Equal(t, statusDispatchDefaultRepo, statusDispatchRepo())
	assert.Equal(t, statusDispatchDefaultRef, statusDispatchRef())
}

// The first run has nothing to compare against, so it checks and remembers the
// head without rebuilding a page that is already current.
func TestStatusDispatchFirstRunChecksOnly(t *testing.T) {
	fake := newFakeGitHub(t)
	s := &Scheduler{}

	assert.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	assert.Equal(t, []string{statusUptimeWorkflow}, fake.calls())
	assert.Equal(t, []string{"Bearer test-token"}, fake.tokens)
	assert.Equal(t, []string{"master"}, fake.refs)
}

// A check that changed the repository is what a page rebuild waits for.
func TestStatusDispatchRebuildsWhenHeadMoves(t *testing.T) {
	fake := newFakeGitHub(t)
	s := &Scheduler{}

	assert.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	fake.setHead("sha-two")
	assert.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))

	assert.Equal(t, []string{statusUptimeWorkflow, statusUptimeWorkflow, statusSiteWorkflow}, fake.calls())
}

// An unchanged head means nothing happened worth publishing.
func TestStatusDispatchSkipsRebuildWhenUnchanged(t *testing.T) {
	fake := newFakeGitHub(t)
	s := &Scheduler{}

	for i := 0; i < 3; i++ {
		assert.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	}
	assert.Equal(t, []string{statusUptimeWorkflow, statusUptimeWorkflow, statusUptimeWorkflow}, fake.calls())
}

// A flapping service changes the repository on every check; the floor keeps
// that from becoming a page deploy every five minutes.
func TestStatusDispatchHonoursRebuildFloor(t *testing.T) {
	fake := newFakeGitHub(t)
	s := &Scheduler{}

	assert.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	fake.setHead("sha-two")
	assert.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	fake.setHead("sha-three")
	assert.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))

	assert.Equal(t,
		[]string{statusUptimeWorkflow, statusUptimeWorkflow, statusSiteWorkflow, statusUptimeWorkflow},
		fake.calls(),
		"the second change falls inside the floor, so only the check runs")

	// Once the floor has passed, the next change rebuilds again.
	statusDispatchState.Lock()
	statusDispatchState.lastSiteDispatch = time.Now().Add(-2 * statusSiteRebuildFloor)
	statusDispatchState.Unlock()
	fake.setHead("sha-four")
	assert.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	assert.Equal(t, statusSiteWorkflow, fake.calls()[len(fake.calls())-1])
}

// A failed rebuild must not consume the floor, or one bad response would mute
// the page for ten minutes.
func TestStatusDispatchFailedRebuildRetriesNextTick(t *testing.T) {
	fake := newFakeGitHub(t)
	s := &Scheduler{}

	assert.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	fake.setHead("sha-two")
	fake.dispatchErr = http.StatusUnprocessableEntity
	err := s.dispatchStatusPageWorkflows(context.Background())
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), "test-token", "the token must never reach an error message")

	statusDispatchState.Lock()
	floorCleared := statusDispatchState.lastSiteDispatch.IsZero()
	statusDispatchState.Unlock()
	assert.True(t, floorCleared, "a failed rebuild leaves the floor open for the next tick")
}

// The job's errors are what RunLocked turns into a Discord cron alert, so each
// GitHub failure has to surface rather than be swallowed.
func TestStatusDispatchReportsGitHubFailures(t *testing.T) {
	fake := newFakeGitHub(t)
	s := &Scheduler{}

	fake.dispatchErr = http.StatusUnauthorized
	assert.ErrorContains(t, s.dispatchStatusPageWorkflows(context.Background()), statusUptimeWorkflow)

	fake.dispatchErr = 0
	fake.headErr = http.StatusNotFound
	assert.ErrorContains(t, s.dispatchStatusPageWorkflows(context.Background()), "head")
}

// Losing the token between registration and a run is not a failure to alert on.
func TestStatusDispatchQuietWithoutToken(t *testing.T) {
	fake := newFakeGitHub(t)
	t.Setenv(statusDispatchTokenEnv, "")
	s := &Scheduler{}

	assert.NoError(t, s.dispatchStatusPageWorkflows(context.Background()))
	assert.Empty(t, fake.calls())
}
