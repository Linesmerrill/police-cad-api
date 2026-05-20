package scheduler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Best-effort Discord webhook alerts for scheduler job failures.
//
// Reads DISCORD_CRON_ERROR_WEBHOOK_URL from the environment. Silent
// no-op when unset so local dev / preview apps cost nothing.
//
// Includes a 60-second dedup window per (job, error-message) signature
// so a hot bug at 3 AM doesn't dump 100 messages while the cron retries.

const (
	cronAlertWebhookEnv   = "DISCORD_CRON_ERROR_WEBHOOK_URL"
	cronAlertDedupWindow  = 60 * time.Second
	cronAlertHTTPDeadline = 5 * time.Second
)

var (
	cronAlertMu     sync.Mutex
	cronAlertRecent = map[string]time.Time{}
	cronAlertClient = &http.Client{Timeout: cronAlertHTTPDeadline}
)

type discordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Color       int            `json:"color,omitempty"`
	Timestamp   string         `json:"timestamp,omitempty"`
	Footer      *discordFooter `json:"footer,omitempty"`
	Fields      []discordField `json:"fields,omitempty"`
}

type discordFooter struct {
	Text string `json:"text"`
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type discordWebhookPayload struct {
	Embeds []discordEmbed `json:"embeds"`
}

func truncForDiscord(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// SendCronAlert posts an error embed to Discord for a failed scheduler job.
// `jobName` identifies which job (e.g. "processCommunityPendingDeletions"),
// `err` is the failure, and `fields` are arbitrary key/value pairs included
// inline on the embed. Best-effort: if anything about the POST goes wrong,
// we log via zap and move on.
//
// Exposed as a package function so handlers outside the scheduler (e.g. the
// admin smoke-test endpoint) can fire alerts without holding a Scheduler
// reference. Callers should pass the running dyno's DYNO env, or empty
// string if not relevant.
func SendCronAlert(instanceID, jobName string, err error, fields map[string]string) {
	webhook := os.Getenv(cronAlertWebhookEnv)
	if webhook == "" || err == nil {
		return
	}

	sig := jobName + "|" + err.Error()
	now := time.Now()

	cronAlertMu.Lock()
	if t, ok := cronAlertRecent[sig]; ok && now.Sub(t) < cronAlertDedupWindow {
		cronAlertMu.Unlock()
		return
	}
	cronAlertRecent[sig] = now
	// Prune entries older than the dedup window so the map can't grow
	// without bound.
	for k, t := range cronAlertRecent {
		if now.Sub(t) > cronAlertDedupWindow {
			delete(cronAlertRecent, k)
		}
	}
	cronAlertMu.Unlock()

	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "unknown"
	}
	dyno := os.Getenv("DYNO")
	if dyno == "" {
		dyno = "local"
	}

	if instanceID == "" {
		instanceID = dyno
	}
	embedFields := []discordField{
		{Name: "Job", Value: jobName, Inline: true},
		{Name: "Instance", Value: instanceID, Inline: true},
	}
	for k, v := range fields {
		embedFields = append(embedFields, discordField{
			Name:   truncForDiscord(k, 256),
			Value:  truncForDiscord(v, 1024),
			Inline: false,
		})
	}

	embed := discordEmbed{
		Title:       fmt.Sprintf("⚠️ Cron failure · %s", truncForDiscord(err.Error(), 200)),
		Description: fmt.Sprintf("```\n%s\n```", truncForDiscord(err.Error(), 1800)),
		Color:       0xef4444,
		Timestamp:   now.UTC().Format(time.RFC3339),
		Footer:      &discordFooter{Text: "police-cad-api · " + env + " · " + dyno},
		Fields:      embedFields,
	}

	payload := discordWebhookPayload{Embeds: []discordEmbed{embed}}
	body, mErr := json.Marshal(payload)
	if mErr != nil {
		zap.S().Errorw("discord cron alert: marshal failed", "error", mErr)
		return
	}

	go func() {
		req, rErr := http.NewRequest(http.MethodPost, webhook, bytes.NewReader(body))
		if rErr != nil {
			zap.S().Warnw("discord cron alert: request build failed", "error", rErr)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, hErr := cronAlertClient.Do(req)
		if hErr != nil {
			zap.S().Warnw("discord cron alert: post failed", "error", hErr)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			zap.S().Warnw("discord cron alert: bad response", "status", resp.StatusCode)
		}
	}()
}
