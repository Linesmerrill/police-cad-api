package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeIssues stands in for the status repository: it serves whatever issues a
// test puts in it and records what was created, commented and closed.
type fakeIssues struct {
	mu sync.Mutex

	open []statusIssue

	created    []map[string]interface{}
	comments   map[int][]string
	closed     []int
	dispatched []string

	createStatus int
	listStatus   int
}

func newFakeIssues(t *testing.T) *fakeIssues {
	t.Helper()
	f := &fakeIssues{comments: map[int][]string{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/issues", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodGet {
			if f.listStatus != 0 {
				w.WriteHeader(f.listStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(f.open)
			return
		}
		if f.createStatus != 0 {
			w.WriteHeader(f.createStatus)
			return
		}
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.created = append(f.created, body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(statusIssue{Number: 100 + len(f.created), State: "open"})
	})
	mux.HandleFunc("/repos/owner/repo/issues/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		rest := strings.TrimPrefix(r.URL.Path, "/repos/owner/repo/issues/")
		number := 0
		fmt.Sscanf(rest, "%d", &number)
		raw, _ := io.ReadAll(r.Body)
		var body map[string]interface{}
		_ = json.Unmarshal(raw, &body)

		if strings.HasSuffix(rest, "/comments") {
			f.comments[number] = append(f.comments[number], fmt.Sprint(body["body"]))
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("{}"))
			return
		}
		if r.Method == http.MethodPatch && body["state"] == "closed" {
			f.closed = append(f.closed, number)
		}
		_, _ = w.Write([]byte("{}"))
	})
	mux.HandleFunc("/repos/owner/repo/actions/workflows/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.dispatched = append(f.dispatched, strings.TrimSuffix(
			strings.TrimPrefix(r.URL.Path, "/repos/owner/repo/actions/workflows/"), "/dispatches"))
		w.WriteHeader(http.StatusNoContent)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	previous := githubAPIBaseURL
	githubAPIBaseURL = server.URL
	t.Cleanup(func() { githubAPIBaseURL = previous })

	t.Setenv(statusDispatchTokenEnv, "test-token")
	t.Setenv(statusDispatchRepoEnv, "owner/repo")
	t.Setenv(statusDispatchRefEnv, "master")
	return f
}

func TestStatusIncidentConfigured(t *testing.T) {
	t.Setenv(statusDispatchTokenEnv, "")
	assert.False(t, statusIncidentConfigured())
	t.Setenv(statusDispatchTokenEnv, "tok")
	assert.True(t, statusIncidentConfigured())
}

// Nothing open for the service: the API files the incident itself, labelled so
// the status page can place it, and marked so a later run knows it is ours.
func TestOpenStatusIncidentFilesAnIssue(t *testing.T) {
	fake := newFakeIssues(t)

	issue, err := OpenStatusIncident(context.Background(), "discord-bot", "Discord bot is not responding", "No heartbeat.")
	require.NoError(t, err)
	require.NotNil(t, issue)

	require.Len(t, fake.created, 1)
	assert.Equal(t, "Discord bot is not responding", fake.created[0]["title"])
	assert.Contains(t, fake.created[0]["body"], statusIncidentMarker)
	labels := fake.created[0]["labels"].([]interface{})
	assert.Equal(t, []interface{}{"status", "discord-bot"}, labels)
	// The page is rebuilt straight away, or the incident is invisible until
	// the next scheduled tick.
	assert.Equal(t, []string{statusSiteWorkflow}, fake.dispatched)
}

// Upptime's check usually notices the same outage. One outage is one incident,
// so the API adds what it knows to the issue that already exists.
func TestOpenStatusIncidentCommentsWhenOneIsAlreadyOpen(t *testing.T) {
	fake := newFakeIssues(t)
	fake.open = []statusIssue{{Number: 42, State: "open", Title: "Discord Bot is down", Body: "upptime"}}

	issue, err := OpenStatusIncident(context.Background(), "discord-bot", "Discord bot is not responding", "No heartbeat since 10:00.")
	require.NoError(t, err)
	require.NotNil(t, issue)
	assert.Equal(t, 42, issue.Number)

	assert.Empty(t, fake.created, "a second issue would put two incidents on the page for one outage")
	assert.Equal(t, []string{"No heartbeat since 10:00."}, fake.comments[42])
}

// Our own incident is commented on and closed.
func TestResolveStatusIncidentClosesOurOwn(t *testing.T) {
	fake := newFakeIssues(t)
	fake.open = []statusIssue{{Number: 7, State: "open", Body: "body\n\n" + statusIncidentMarker}}

	require.NoError(t, ResolveStatusIncident(context.Background(), "discord-bot", "Resolved: back after 10 minutes."))
	assert.Equal(t, []string{"Resolved: back after 10 minutes."}, fake.comments[7])
	assert.Equal(t, []int{7}, fake.closed)
}

// Upptime closes its own issues when its check recovers. Closing one from here
// would claim an observation we did not make.
func TestResolveStatusIncidentLeavesForeignIssuesOpen(t *testing.T) {
	fake := newFakeIssues(t)
	fake.open = []statusIssue{{Number: 9, State: "open", Body: "opened by upptime"}}

	require.NoError(t, ResolveStatusIncident(context.Background(), "discord-bot", "Resolved: back."))
	assert.Equal(t, []string{"Resolved: back."}, fake.comments[9])
	assert.Empty(t, fake.closed)
}

// A recovery with nothing open is normal: someone closed the incident by hand.
func TestResolveStatusIncidentWithNothingOpen(t *testing.T) {
	fake := newFakeIssues(t)

	require.NoError(t, ResolveStatusIncident(context.Background(), "discord-bot", "Resolved."))
	assert.Empty(t, fake.comments)
	assert.Empty(t, fake.closed)
}

// Without a token the status page is left alone entirely, so local dev and
// preview apps cannot post to it.
func TestStatusIncidentsAreQuietWithoutAToken(t *testing.T) {
	fake := newFakeIssues(t)
	t.Setenv(statusDispatchTokenEnv, "")

	issue, err := OpenStatusIncident(context.Background(), "discord-bot", "title", "body")
	require.NoError(t, err)
	assert.Nil(t, issue)
	require.NoError(t, ResolveStatusIncident(context.Background(), "discord-bot", "body"))
	assert.Empty(t, fake.created)
	assert.Empty(t, fake.comments)
}

// A GitHub failure has to surface, since the caller turns it into a cron alert.
func TestStatusIncidentReportsFailures(t *testing.T) {
	fake := newFakeIssues(t)
	fake.createStatus = http.StatusForbidden

	_, err := OpenStatusIncident(context.Background(), "discord-bot", "title", "body")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "test-token", "the token must never reach an error message")

	fake.createStatus = 0
	fake.listStatus = http.StatusUnauthorized
	_, err = OpenStatusIncident(context.Background(), "discord-bot", "title", "body")
	assert.ErrorContains(t, err, "open status incident")
}
