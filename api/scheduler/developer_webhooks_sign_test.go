package scheduler_test

import (
	"testing"
	"time"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/api/scheduler"
)

// The two signing implementations have to agree byte for byte.
//
// There are two because api/handlers imports api/scheduler, so the scheduler
// cannot import the handlers back for a six-line helper without a cycle. An
// external test package can import both, which is what this file is for: the
// duplication is deliberate, and this is the thing that stops it drifting.
//
// If this fails, a receiver verifying against the documented scheme will start
// rejecting deliveries — which is the worst failure mode this feature has,
// because it looks like a receiver bug.
func TestWebhookSigningMatchesTheHandlers(t *testing.T) {
	cases := []struct {
		name   string
		secret string
		body   string
	}{
		{"an ordinary delivery", "whsec_abc123", `{"event":{"id":"6ae7","type":"call.created"}}`},
		{"an empty body", "whsec_abc123", ``},
		{"a body with a quote in it", "whsec_abc123", `{"data":{"title":"He said \"stop\""}}`},
		{"a different secret", "whsec_rotated", `{"event":{"id":"6ae7"}}`},
	}
	at := time.Unix(1762000111, 0)

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fromHandlers := handlers.SignWebhookPayload(c.secret, at, []byte(c.body))
			fromWorker := scheduler.SignWebhookBodyForTest(c.secret, at, []byte(c.body))
			if fromHandlers != fromWorker {
				t.Fatalf("the two implementations disagree\\nhandlers: %s\\n worker: %s",
					fromHandlers, fromWorker)
			}
		})
	}
}

// The address gate has two copies for the same reason, and the ranges that
// matter have to agree. A hostname the console accepted and the worker then
// refuses is a webhook that silently never fires.
func TestWebhookAddressGateMatchesTheHandlers(t *testing.T) {
	for _, url := range []string{
		"https://127.0.0.1/hook",
		"https://10.0.0.5/hook",
		"https://169.254.169.254/latest/meta-data/",
		"https://100.64.0.1/hook",
		"https://[::1]/hook",
		"http://example.com/hook",
	} {
		handlerErr := handlers.ValidateWebhookURLForTest(url)
		workerErr := scheduler.AssertPublicWebhookURLForTest(url)
		if (handlerErr == nil) != (workerErr == nil) {
			t.Errorf("%s: the console and the worker disagree (console=%v worker=%v)",
				url, handlerErr, workerErr)
		}
		if handlerErr == nil {
			t.Errorf("%s must be refused", url)
		}
	}
}
