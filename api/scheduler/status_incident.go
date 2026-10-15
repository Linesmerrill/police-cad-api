package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Files an incident on the public status page when the API notices an outage,
// and resolves it when the outage ends.
//
// The status page builds its incident history out of the status repository's
// issues, so an issue there is an incident customers can read. Upptime opens
// one by itself for anything it can reach over HTTP; this is for what only the
// API knows, starting with the Discord bot going quiet, where the API can say
// when the last heartbeat was and what stops working rather than "a URL
// returned 503".
//
// Two rules keep it honest when both are watching the same thing:
//
//   - Never open a second issue for an outage that already has one. If an open
//     issue is already labelled with the service, this comments on it instead.
//   - Only close what we opened. Upptime closes its own issues when its check
//     recovers; closing them from here would be lying about who observed what.
//
// Reuses STATUS_DISPATCH_TOKEN, which therefore needs Issues: read and write
// alongside the Actions permission it already has. Silent no-op while the
// token is unset, like the dispatch job, so local dev and preview apps never
// post to the real status page.
const (
	statusIncidentLabel = "status"
	// statusIncidentMarker goes in the body of every issue we open, so a later
	// run can tell our incident from one Upptime filed.
	statusIncidentMarker = "<!-- opened-by: police-cad-api -->"

	statusIncidentHTTPDeadline = 10 * time.Second
)

var statusIncidentClient = &http.Client{Timeout: statusIncidentHTTPDeadline}

// statusIncidentConfigured reports whether incidents can be filed at all.
func statusIncidentConfigured() bool {
	return strings.TrimSpace(os.Getenv(statusDispatchTokenEnv)) != ""
}

type statusIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	URL    string `json:"html_url"`
}

// OpenStatusIncident files an incident for a service, or adds to the one that
// is already open.
//
// slug is the service's slug on the status page, which is also its label in
// the status repository ("discord-bot"). Returns the issue it acted on.
func OpenStatusIncident(ctx context.Context, slug, title, body string) (*statusIssue, error) {
	token := strings.TrimSpace(os.Getenv(statusDispatchTokenEnv))
	if token == "" {
		return nil, nil
	}
	repo := statusDispatchRepo()

	existing, err := openIncidentFor(ctx, token, repo, slug)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		// Something is already reporting this outage. Add what we know to it
		// rather than putting two incidents on the page for one event.
		if err := commentOnIncident(ctx, token, repo, existing.Number, body); err != nil {
			return nil, err
		}
		dispatchStatusPageBuild(ctx, token, repo)
		return existing, nil
	}

	payload := map[string]interface{}{
		"title":  title,
		"body":   body + "\n\n" + statusIncidentMarker,
		"labels": []string{statusIncidentLabel, slug},
	}
	var created statusIssue
	if err := githubJSON(ctx, token, http.MethodPost, fmt.Sprintf("/repos/%s/issues", repo), payload, &created); err != nil {
		return nil, fmt.Errorf("failed to open status incident: %w", err)
	}
	zap.S().Infow("status incident opened", "service", slug, "issue", created.Number)

	dispatchStatusPageBuild(ctx, token, repo)
	return &created, nil
}

// ResolveStatusIncident closes the incident this API opened for a service, with
// a closing update.
//
// An open issue that we did not open is commented on and left open: whoever
// opened it decides when it is over.
func ResolveStatusIncident(ctx context.Context, slug, body string) error {
	token := strings.TrimSpace(os.Getenv(statusDispatchTokenEnv))
	if token == "" {
		return nil
	}
	repo := statusDispatchRepo()

	issue, err := openIncidentFor(ctx, token, repo, slug)
	if err != nil {
		return err
	}
	if issue == nil {
		// Nothing open: the incident was closed by hand, or there never was
		// one. Either way there is nothing to resolve.
		return nil
	}

	if err := commentOnIncident(ctx, token, repo, issue.Number, body); err != nil {
		return err
	}
	if !strings.Contains(issue.Body, statusIncidentMarker) {
		zap.S().Infow("status incident updated but left open: opened elsewhere", "service", slug, "issue", issue.Number)
		dispatchStatusPageBuild(ctx, token, repo)
		return nil
	}

	patch := map[string]interface{}{"state": "closed", "state_reason": "completed"}
	if err := githubJSON(ctx, token, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/%d", repo, issue.Number), patch, nil); err != nil {
		return fmt.Errorf("failed to close status incident: %w", err)
	}
	zap.S().Infow("status incident resolved", "service", slug, "issue", issue.Number)

	dispatchStatusPageBuild(ctx, token, repo)
	return nil
}

// openIncidentFor is the open issue for a service, if there is one.
func openIncidentFor(ctx context.Context, token, repo, slug string) (*statusIssue, error) {
	path := fmt.Sprintf(
		"/repos/%s/issues?state=open&per_page=100&labels=%s",
		repo,
		url.QueryEscape(statusIncidentLabel+","+slug),
	)
	var issues []statusIssue
	if err := githubJSON(ctx, token, http.MethodGet, path, nil, &issues); err != nil {
		return nil, fmt.Errorf("failed to look for an open status incident: %w", err)
	}
	for i := range issues {
		if issues[i].State == "open" || issues[i].State == "" {
			return &issues[i], nil
		}
	}
	return nil, nil
}

func commentOnIncident(ctx context.Context, token, repo string, number int, body string) error {
	payload := map[string]interface{}{"body": body}
	if err := githubJSON(ctx, token, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, number), payload, nil); err != nil {
		return fmt.Errorf("failed to comment on status incident: %w", err)
	}
	return nil
}

// dispatchStatusPageBuild publishes the page now rather than at the next tick,
// because an incident nobody can see yet is not much of a status page. Failure
// is logged and no more: the scheduled dispatch will pick it up.
func dispatchStatusPageBuild(ctx context.Context, token, repo string) {
	if err := githubDispatchWorkflow(ctx, token, repo, statusSiteWorkflow, statusDispatchRef()); err != nil {
		zap.S().Warnw("status page rebuild after an incident change failed", "error", err.Error())
	}
}

// githubJSON performs one GitHub REST call, decoding the response when out is
// given. The token is never logged or returned in an error.
func githubJSON(ctx context.Context, token, method, path string, payload interface{}, out interface{}) error {
	var body *bytes.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	} else {
		body = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, githubAPIBaseURL+path, body)
	if err != nil {
		return err
	}
	setGitHubHeaders(req, token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := statusIncidentClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("github answered %s", res.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}
