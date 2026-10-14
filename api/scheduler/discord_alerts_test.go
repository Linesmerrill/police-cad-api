package scheduler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// postDiscordAlert grew a webhook-env parameter and two optional copy
// overrides so that bot-health status alerts could reuse its embed, goroutine
// and 5-second client instead of copy-pasting them. Nothing in SendCronAlert
// or SendWarningAlert was meant to change, and there were no tests here to
// prove it, so these pin the exact payload both of them put on the wire.

// captureCronAlerts points DISCORD_CRON_ERROR_WEBHOOK_URL at a local server
// and returns a drain for whatever was posted.
func captureCronAlerts(t *testing.T) func() []discordWebhookPayload {
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

	t.Setenv(cronAlertWebhookEnv, srv.URL)
	t.Setenv(websiteAlertsWebhookEnv, "")
	// Pinned so the footer is deterministic.
	t.Setenv("APP_ENV", "test")
	t.Setenv("DYNO", "web.9")

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

func TestSendCronAlert_PayloadUnchanged(t *testing.T) {
	drain := captureCronAlerts(t)

	SendCronAlert("web.3", "processGracePeriods",
		errors.New("cron alert payload pin: grace period sweep exploded"),
		map[string]string{"phase": "run"})

	alerts := drain()
	assert.Len(t, alerts, 1)
	embed := alerts[0].Embeds[0]

	assert.Equal(t, "⚠️ Cron failure · cron alert payload pin: grace period sweep exploded", embed.Title)
	assert.Equal(t, "```\ncron alert payload pin: grace period sweep exploded\n```", embed.Description)
	assert.Equal(t, 0xef4444, embed.Color)
	assert.Equal(t, "police-cad-api · test · web.9", embed.Footer.Text)
	assert.Equal(t, discordField{Name: "Job", Value: "processGracePeriods", Inline: true}, embed.Fields[0])
	assert.Equal(t, discordField{Name: "Instance", Value: "web.3", Inline: true}, embed.Fields[1])
	assert.Equal(t, discordField{Name: "phase", Value: "run"}, embed.Fields[2])
}

func TestSendWarningAlert_PayloadUnchanged(t *testing.T) {
	drain := captureCronAlerts(t)

	SendWarningAlert("", "tiktokScreening",
		errors.New("warning alert payload pin: fell back to the slow path"),
		nil)

	alerts := drain()
	assert.Len(t, alerts, 1)
	embed := alerts[0].Embeds[0]

	assert.Equal(t, "⚠️ Warning · warning alert payload pin: fell back to the slow path", embed.Title)
	assert.Equal(t, "```\nwarning alert payload pin: fell back to the slow path\n```", embed.Description)
	assert.Equal(t, 0xf59e0b, embed.Color)
	assert.Equal(t, "police-cad-api · test · web.9", embed.Footer.Text)
	assert.Equal(t, "Source", embed.Fields[0].Name)
	// An empty instanceID still falls back to DYNO.
	assert.Equal(t, discordField{Name: "Instance", Value: "web.9", Inline: true}, embed.Fields[1])
}

func TestSendCronAlert_NilError_PostsNothing(t *testing.T) {
	drain := captureCronAlerts(t)
	SendCronAlert("web.3", "someJob", nil, nil)
	assert.Empty(t, drain())
}

func TestSendCronAlert_DedupsRepeats(t *testing.T) {
	drain := captureCronAlerts(t)

	err := errors.New("cron alert dedup pin: the same failure twice")
	SendCronAlert("web.3", "someJob", err, nil)
	SendCronAlert("web.3", "someJob", err, nil)

	assert.Len(t, drain(), 1, "a hot bug retrying must not dump a message per attempt")
}

func TestPostDiscordAlert_NoWebhookEnvNamed_PostsNothing(t *testing.T) {
	drain := captureCronAlerts(t)

	// A caller that forgets to name its channel must not silently land in
	// whichever channel happens to be configured.
	postDiscordAlert(discordAlertOptions{
		name:   "someJob",
		detail: errors.New("no webhook env pin"),
		title:  "Cron failure",
	})

	assert.Empty(t, drain())
}
