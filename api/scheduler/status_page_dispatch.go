package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Drives the Upptime status page from here instead of from GitHub's scheduler.
//
// police-cad-status asks GitHub for a check every 5 minutes. GitHub throttles
// scheduled workflows on a low-activity repository hard, and in practice the
// check fired roughly every 3 to 5 hours: on 2026-10-15 it ran at 00:42,
// 05:11, 09:52 and 14:27. That is why the alerts channel said the bot was back
// while the status page still showed it down. The schedule in the status repo
// is a request; a workflow_dispatch is not, so this job asks for the run
// explicitly and the page tracks reality.
//
// Two workflows matter. uptime.yml performs the checks and commits the result,
// and status-page.yml rebuilds the published page. Running the page build on a timer
// would deploy it dozens of times a day over nothing, so it is dispatched only
// when a check actually changed the repository, with a floor between rebuilds.
//
// Silent no-op without STATUS_DISPATCH_TOKEN, so local dev and preview apps
// cost nothing and never touch the real status page. The status repo keeps its
// own daily schedules, which remain the backstop if this job or the token ever
// goes away.
const (
	statusDispatchJobName  = "statusPageDispatch"
	statusDispatchLockKey  = "status_page_dispatch_job"
	statusDispatchSchedule = "@every 5m"
	// statusDispatchJobTTL bounds the run and the lock. Three GitHub calls at
	// worst, each with its own short deadline.
	statusDispatchJobTTL = 90 * time.Second

	statusDispatchTokenEnv = "STATUS_DISPATCH_TOKEN"
	statusDispatchRepoEnv  = "STATUS_DISPATCH_REPO"
	statusDispatchRefEnv   = "STATUS_DISPATCH_REF"

	statusDispatchDefaultRepo = "Linesmerrill/police-cad-status"
	statusDispatchDefaultRef  = "master"

	statusUptimeWorkflow = "uptime.yml"
	// statusSiteWorkflow is the status repo's own page build, which replaced
	// Upptime's generated site.yml when the page was redesigned.
	statusSiteWorkflow = "status-page.yml"

	// statusSiteRebuildFloor is the shortest gap between two page rebuilds.
	// A flapping service could otherwise change the repository on every check.
	statusSiteRebuildFloor = 10 * time.Minute

	statusDispatchHTTPDeadline = 10 * time.Second
)

// githubAPIBaseURL is a variable so tests can point the job at a local server.
var githubAPIBaseURL = "https://api.github.com"

var statusDispatchClient = &http.Client{Timeout: statusDispatchHTTPDeadline}

// What the last run saw, so a page rebuild happens on a real change rather
// than on a timer. Per dyno and in memory on purpose: losing it costs at most
// one skipped rebuild, which the status repo's daily schedule covers, and it
// cannot wedge anything.
var statusDispatchState = struct {
	sync.Mutex
	// lastSeenSHA is the status repo's head the last time we looked. Empty
	// means we have not looked yet.
	lastSeenSHA string
	// lastSiteDispatch is when we last asked for a page rebuild.
	lastSiteDispatch time.Time
}{}

// statusDispatchConfigured reports whether the job has a token to act with.
func statusDispatchConfigured() bool {
	return strings.TrimSpace(os.Getenv(statusDispatchTokenEnv)) != ""
}

func statusDispatchRepo() string {
	if repo := strings.TrimSpace(os.Getenv(statusDispatchRepoEnv)); repo != "" {
		return repo
	}
	return statusDispatchDefaultRepo
}

func statusDispatchRef() string {
	if ref := strings.TrimSpace(os.Getenv(statusDispatchRefEnv)); ref != "" {
		return ref
	}
	return statusDispatchDefaultRef
}

// dispatchStatusPageWorkflows asks for an uptime check, and for a page rebuild
// when the last check changed something.
func (s *Scheduler) dispatchStatusPageWorkflows(ctx context.Context) error {
	token := strings.TrimSpace(os.Getenv(statusDispatchTokenEnv))
	if token == "" {
		// Unset between registration and this run. Nothing to do, and nothing
		// worth alerting about.
		return nil
	}
	repo, ref := statusDispatchRepo(), statusDispatchRef()

	if err := githubDispatchWorkflow(ctx, token, repo, statusUptimeWorkflow, ref); err != nil {
		return fmt.Errorf("failed to dispatch %s: %w", statusUptimeWorkflow, err)
	}
	zap.S().Debugw("status page: dispatched uptime check", "repo", repo, "ref", ref)

	// The head here is from before the run we just asked for: a check commits
	// its result, so the change this reads is the previous one. That one tick
	// of lag is why the page is at most two ticks behind reality, which is
	// minutes rather than the hours it was.
	head, err := githubHeadSHA(ctx, token, repo, ref)
	if err != nil {
		return fmt.Errorf("failed to read %s head: %w", repo, err)
	}

	statusDispatchState.Lock()
	first := statusDispatchState.lastSeenSHA == ""
	changed := !first && statusDispatchState.lastSeenSHA != head
	tooSoon := time.Since(statusDispatchState.lastSiteDispatch) < statusSiteRebuildFloor
	statusDispatchState.lastSeenSHA = head
	rebuild := changed && !tooSoon
	if rebuild {
		statusDispatchState.lastSiteDispatch = time.Now()
	}
	statusDispatchState.Unlock()

	if !rebuild {
		return nil
	}
	if err := githubDispatchWorkflow(ctx, token, repo, statusSiteWorkflow, ref); err != nil {
		// Roll the floor back so the next tick tries again rather than waiting
		// it out after a failure.
		statusDispatchState.Lock()
		statusDispatchState.lastSiteDispatch = time.Time{}
		statusDispatchState.Unlock()
		return fmt.Errorf("failed to dispatch %s: %w", statusSiteWorkflow, err)
	}
	zap.S().Infow("status page: dispatched page rebuild", "repo", repo, "head", head)
	return nil
}

// githubDispatchWorkflow runs one workflow on a branch, through the REST API's
// workflow_dispatch endpoint. A successful dispatch answers 204.
func githubDispatchWorkflow(ctx context.Context, token, repo, workflow, ref string) error {
	url := fmt.Sprintf("%s/repos/%s/actions/workflows/%s/dispatches", githubAPIBaseURL, repo, workflow)
	body := strings.NewReader(fmt.Sprintf(`{"ref":%q}`, ref))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	setGitHubHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")

	res, err := statusDispatchClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("github answered %s", res.Status)
	}
	return nil
}

// githubHeadSHA is the commit a branch currently points at.
func githubHeadSHA(ctx context.Context, token, repo, ref string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/commits/%s", githubAPIBaseURL, repo, ref)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	setGitHubHeaders(req, token)

	res, err := statusDispatchClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github answered %s", res.Status)
	}

	var payload struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.SHA == "" {
		return "", fmt.Errorf("github returned no sha for %s", ref)
	}
	return payload.SHA, nil
}

// setGitHubHeaders applies the token and the API version GitHub asks callers to
// pin. The token is never logged or returned in an error.
func setGitHubHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}
