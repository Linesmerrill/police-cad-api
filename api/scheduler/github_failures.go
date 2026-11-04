package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/databases"
)

// Tells GitHub having a bad morning apart from us holding a bad token.
//
// The status page jobs call GitHub every five minutes, so every GitHub blip
// used to arrive in alerts-website as "Cron failure · failed to dispatch
// uptime.yml: github answered 500". Nobody can act on that: it is GitHub's
// outage, it clears by itself, and the status repo's own daily schedule is the
// backstop meanwhile. A revoked token or a renamed workflow is the opposite --
// it will never clear on its own, and it goes on failing silently until
// somebody is told.
//
// So failures are split. A transient one logs a warning and the tick returns
// clean. A failure that needs a person is returned, and RunLocked alerts it as
// a Cron failure exactly as before.
//
// The quiet half still cannot be silent forever: if GitHub keeps failing for
// an hour the status page really is behind, which is worth one amber warning.
// The count lives in Mongo rather than in memory because the job runs under
// RunLocked and the next tick may land on another dyno.
const (
	// statusDispatchStreakKey names this job's streak in scheduler_job_state.
	statusDispatchStreakKey = "statusPageDispatch:github"

	// statusDispatchTransientAlertAfter is how many consecutive transient
	// failures amount to an hour: twelve ticks of the 5-minute schedule.
	statusDispatchTransientAlertAfter = 12

	// statusDispatchStaleWarning is the warning's text, and the only
	// user-visible string this file adds.
	statusDispatchStaleWarning = "GitHub has failed for over an hour. The status page may be behind."
)

// Seams for the tests. The alert helpers post to a webhook, so a test asserting
// "one warning, not twelve" swaps them rather than standing up a server.
var (
	sendWarningAlertFn = SendWarningAlert
	sendCronAlertFn    = SendCronAlert
)

// githubStatusError is a non-2xx answer from GitHub, carrying enough to tell a
// rate limit from a refusal. The token is never part of it.
type githubStatusError struct {
	StatusCode  int
	Status      string
	RateLimited bool
}

func (e *githubStatusError) Error() string {
	return fmt.Sprintf("github answered %s", e.Status)
}

// newGitHubStatusError classifies one response. It reads a bounded piece of the
// body because GitHub reports both the primary and the secondary rate limit as
// 403, and only the body and the headers say which 403 this is.
func newGitHubStatusError(res *http.Response) *githubStatusError {
	err := &githubStatusError{StatusCode: res.StatusCode, Status: res.Status}
	if res.StatusCode != http.StatusForbidden {
		return err
	}
	if res.Header.Get("Retry-After") != "" || res.Header.Get("X-RateLimit-Remaining") == "0" {
		err.RateLimited = true
		return err
	}
	snippet, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
	err.RateLimited = strings.Contains(strings.ToLower(string(snippet)), "rate limit")
	return err
}

// githubFailureIsTransient reports whether an error is GitHub's problem rather
// than ours: something that the next tick may well find working.
//
// Transient: 5xx, 429, a rate-limited 403, and anything that never reached an
// answer -- timeouts, resets, refusals, DNS. Needs a person: 401, 403, 404,
// 422, and any other 4xx, because a token, a permission, a repository name or
// a workflow file is wrong and no amount of waiting fixes it.
func githubFailureIsTransient(err error) bool {
	if err == nil {
		return false
	}

	var statusErr *githubStatusError
	if errors.As(err, &statusErr) {
		switch {
		case statusErr.StatusCode >= 500:
			return true
		case statusErr.StatusCode == http.StatusTooManyRequests:
			return true
		case statusErr.StatusCode == http.StatusForbidden && statusErr.RateLimited:
			return true
		default:
			return false
		}
	}

	// Never got an answer. A client timeout arrives as *url.Error wrapping
	// either context.DeadlineExceeded or a net.Error, and a dropped connection
	// as an unexpected EOF or a syscall error.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.EHOSTUNREACH) {
		return true
	}
	return false
}

// noteGitHubFailure decides what one failed GitHub call is worth.
//
// cause is the error from the call itself, which carries the classification;
// wrapped is what the job would have returned, which says which call it was.
// Returns wrapped for a failure that needs a person, and nil for one that does
// not, having logged it and counted it.
func (s *Scheduler) noteGitHubFailure(ctx context.Context, key, jobName string, wrapped, cause error) error {
	if !githubFailureIsTransient(cause) {
		return wrapped
	}

	streak, err := s.recordGitHubFailure(ctx, key, time.Now().UTC())
	if err != nil {
		// Losing the count is not worth an alert of its own, and must not turn
		// GitHub's outage back into a Cron failure.
		zap.S().Warnw("could not count a transient github failure",
			"job", jobName, "error", err.Error())
		zap.S().Warnw("github call failed, retrying next tick",
			"job", jobName, "error", wrapped.Error())
		return nil
	}

	zap.S().Warnw("github call failed, retrying next tick",
		"job", jobName, "error", wrapped.Error(),
		"consecutive", streak.Count,
		"since", streak.FirstFailedAt.Format(time.RFC3339))

	if streak.Count >= statusDispatchTransientAlertAfter && !streak.Alerted() {
		sendWarningAlertFn(s.instanceID, jobName, errors.New(statusDispatchStaleWarning), map[string]string{
			"consecutive failures": fmt.Sprintf("%d", streak.Count),
			"failing since":        streak.FirstFailedAt.Format("2006-01-02 15:04 UTC"),
			"last error":           wrapped.Error(),
		})
		if err := s.markGitHubFailureAlerted(ctx, key, time.Now().UTC()); err != nil {
			zap.S().Warnw("could not record that the github warning was sent",
				"job", jobName, "error", err.Error())
		}
	}
	return nil
}

// Streak storage. Mongo when the scheduler has a database, which is every
// deployment; an in-memory count otherwise, so tests and a database-less local
// run still behave, at the cost of the count being per-process.
var memoryGitHubFailures = newMemoryFailureStreaks()

func (s *Scheduler) failureStreaks() databases.SchedulerJobStateDatabase {
	if s.JobStateDB != nil {
		return s.JobStateDB
	}
	return memoryGitHubFailures
}

func (s *Scheduler) recordGitHubFailure(ctx context.Context, key string, now time.Time) (databases.JobFailureStreak, error) {
	return s.failureStreaks().RecordFailure(ctx, key, now)
}

func (s *Scheduler) markGitHubFailureAlerted(ctx context.Context, key string, now time.Time) error {
	return s.failureStreaks().MarkAlerted(ctx, key, now)
}

// clearGitHubFailures ends the streak after a call that worked. Best effort:
// a failure here costs one duplicated warning at worst, and must not fail a
// tick that otherwise succeeded.
func (s *Scheduler) clearGitHubFailures(ctx context.Context, key string) {
	if err := s.failureStreaks().ClearFailures(ctx, key); err != nil {
		zap.S().Warnw("could not clear a github failure streak", "key", key, "error", err.Error())
	}
}
