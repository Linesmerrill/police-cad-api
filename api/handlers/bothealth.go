package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Discord bot liveness endpoints.
//
// Both live under /health/ so they sit outside the versioned subrouters, the
// user-agent gateway and the read middleware: an uptime monitor must be able
// to poll the status endpoint with no credentials and no browser-shaped
// headers. The write endpoint therefore does its own authentication, below.
const (
	// botHeartbeatTokenEnv is a dedicated secret, NOT the bot's api_token.
	// api.Middleware treats an unrecognised bearer as anonymous and continues,
	// so accepting the bot's existing token would in practice accept any
	// token: anyone could post a heartbeat and keep a dead bot looking alive,
	// which would turn this monitor into something that lies. A separate
	// secret that is actually compared is the whole point.
	botHeartbeatTokenEnv = "BOT_HEARTBEAT_TOKEN"
	// botHeartbeatTokenHeader is the header the bot sends it in.
	botHeartbeatTokenHeader = "X-Bot-Heartbeat-Token"
	// botHeartbeatMaxBody caps the request body. The payload is four small
	// fields; anything larger is not a heartbeat.
	botHeartbeatMaxBody = 2048
)

// BotHealth serves the Discord bot heartbeat write endpoint and the public
// status endpoint an uptime monitor polls.
type BotHealth struct {
	DB databases.BotHealthDatabase
}

// botHealthStatusResponse is the public status body. It is an explicit struct
// rather than the stored document so that adding a field to models.BotHealth
// can never leak one: nothing reaches a caller unless it is listed here.
type botHealthStatusResponse struct {
	Status            string `json:"status"`
	LastHeartbeat     string `json:"lastHeartbeat"`
	NowAt             string `json:"nowAt"`
	StaleAfterSeconds int    `json:"staleAfterSeconds"`
	ShardID           *int   `json:"shardId,omitempty"`
	GuildCount        *int   `json:"guildCount,omitempty"`
	Version           string `json:"version,omitempty"`
	UptimeSeconds     *int64 `json:"uptimeSeconds,omitempty"`
}

// HeartbeatHandler records a heartbeat from the Discord bot.
//
// POST /health/discord-bot/heartbeat
//
//	503 when BOT_HEARTBEAT_TOKEN is not configured on this API
//	401 when the header is missing or does not match
//	400 when the body is not JSON
//	200 on success
//
// Fails closed on purpose. If the secret is not configured we refuse the write
// rather than accepting an unauthenticated one: an unauthenticated heartbeat
// is worse than no heartbeat, because it would report health that nobody
// verified. The token is never logged and never echoed back.
func (b BotHealth) HeartbeatHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	expected := os.Getenv(botHeartbeatTokenEnv)
	if expected == "" {
		zap.S().Warnw("discord bot heartbeat rejected: BOT_HEARTBEAT_TOKEN is not configured")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"unavailable","message":"Heartbeat reporting is not configured on this API."}`))
		return
	}

	provided := r.Header.Get(botHeartbeatTokenHeader)
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		// Info level, and no echo of what was sent: a wrong token is an
		// expected condition (scanners, a stale secret on the bot host), and
		// logging the attempt at error level would both flood and risk
		// writing a near-miss secret into the log.
		zap.S().Infow("discord bot heartbeat rejected: token mismatch")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized","message":"A valid heartbeat token is required."}`))
		return
	}

	if b.DB == nil {
		zap.S().Warnw("discord bot heartbeat rejected: store unavailable")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"unavailable","message":"Heartbeat storage is unavailable."}`))
		return
	}

	// Unknown fields are ignored (no DisallowUnknownFields) so a newer bot
	// build that adds a field keeps heartbeating against an older API.
	var body models.BotHeartbeat
	if r.Body != nil {
		limited := http.MaxBytesReader(w, r.Body, botHeartbeatMaxBody)
		// An empty body is fine (io.EOF): every field has a sensible zero.
		if err := json.NewDecoder(limited).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			zap.S().Infow("discord bot heartbeat rejected: bad body", "error", err)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad_request","message":"Heartbeat body must be a small JSON object."}`))
			return
		}
	}

	hb := sanitizeBotHeartbeat(body)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// The receive time is ours, not the bot's. A wrong clock on the volunteer
	// host must not be able to backdate or future-date liveness.
	if err := b.DB.RecordHeartbeat(ctx, hb, time.Now().UTC()); err != nil {
		zap.S().Errorw("failed to record discord bot heartbeat", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal_error","message":"Failed to record heartbeat."}`))
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"recorded": true,
		"nowAt":    time.Now().UTC().Format(time.RFC3339),
	})
}

// StatusHandler reports whether the Discord bot is currently heartbeating.
//
// GET /health/discord-bot
//
//	200 {"status":"up",...}   last heartbeat is newer than the stale window
//	503 {"status":"down",...} last heartbeat is older, or there has never been one
//
// Public and unauthenticated so an external uptime monitor can poll it. It
// exposes only what the bot itself reports (shard, guild count, version,
// uptime) and timestamps. No secrets, no auth state, no user data.
func (b BotHealth) StatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	now := time.Now().UTC()

	resp := botHealthStatusResponse{
		Status:            models.BotHealthStateDown,
		NowAt:             now.Format(time.RFC3339),
		StaleAfterSeconds: int(models.BotHeartbeatStaleAfter / time.Second),
	}

	if b.DB == nil {
		// Treated as down rather than as a server error: a monitor wants one
		// bit of signal, and "we cannot tell" is not "it is fine".
		zap.S().Warnw("discord bot status: store unavailable")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	health, err := b.DB.Get(ctx)
	if err != nil {
		zap.S().Errorw("failed to read discord bot health", "error", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	if health == nil || health.LastHeartbeatAt.IsZero() {
		// Never heartbeated. Reported as down (a monitor should not show
		// green for something we have no evidence about), but logged at info
		// rather than error: it is the expected state before the bot host
		// first updates, and an uptime monitor polls this forever.
		zap.S().Infow("discord bot status: no heartbeat recorded yet")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	last := health.LastHeartbeatAt.UTC()
	resp.LastHeartbeat = last.Format(time.RFC3339)
	shard, guilds, uptime := health.ShardID, health.GuildCount, health.UptimeSeconds
	resp.ShardID, resp.GuildCount, resp.UptimeSeconds = &shard, &guilds, &uptime
	resp.Version = health.BotVersion

	if now.Sub(last) < models.BotHeartbeatStaleAfter {
		resp.Status = models.BotHealthStateUp
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	zap.S().Infow("discord bot status: heartbeat is stale", "lastHeartbeat", resp.LastHeartbeat)
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(resp)
}

// sanitizeBotHeartbeat bounds every reported value.
//
// Out-of-range numbers are clamped rather than rejected: the fields are
// cosmetic (they decorate an alert), while the heartbeat itself is the signal
// we actually care about. Refusing the write over a bad guild count would turn
// a cosmetic bot bug into a false outage.
func sanitizeBotHeartbeat(in models.BotHeartbeat) models.BotHeartbeat {
	out := models.BotHeartbeat{
		ShardID:       clampInt(in.ShardID, 0, models.BotHeartbeatMaxShardID),
		GuildCount:    clampInt(in.GuildCount, 0, models.BotHeartbeatMaxGuildCount),
		UptimeSeconds: clampInt64(in.UptimeSeconds, 0, models.BotHeartbeatMaxUptimeSeconds),
		Version:       sanitizeBotVersion(in.Version),
	}
	return out
}

// sanitizeBotVersion keeps printable ASCII only and truncates. The value is
// bot-supplied and ends up in a Discord embed, so it is treated as untrusted
// display text.
func sanitizeBotVersion(v string) string {
	out := make([]rune, 0, models.BotHeartbeatMaxVersionLen)
	for _, c := range v {
		if c < 0x20 || c > 0x7e {
			continue
		}
		out = append(out, c)
		if len(out) == models.BotHeartbeatMaxVersionLen {
			break
		}
	}
	return string(out)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt64(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
