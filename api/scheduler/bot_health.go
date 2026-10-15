package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/models"
)

// Discord bot liveness watchdog.
//
// The bot runs on a volunteer's home machine. When it went down on
// 2026-10-14 nobody noticed until a support ticket arrived. The bot now posts
// a heartbeat to the API every minute; this job is the half that notices the
// heartbeats stopping and says so in Discord.
//
// Two rules shape the whole thing:
//
//   - Alert once per transition. The claim of a transition and the write of
//     the new notified state are a single conditional update in Mongo, so
//     when several dynos notice the same transition in the same minute
//     exactly one of them posts.
//   - Never alert when we have never seen a heartbeat. A fresh API deploy, or
//     an API running before the bot host has updated, must stay quiet. Silence
//     from something that has never spoken is not an outage.
const (
	botHealthJobName  = "discordBotHealthCheck"
	botHealthLockKey  = "discord_bot_health_check_job"
	botHealthSchedule = "@every 1m"
	// botHealthJobTTL bounds the run and the lock. Well under the schedule
	// interval: the work is one read and at most one write.
	botHealthJobTTL = 45 * time.Second
)

// checkDiscordBotHealth compares the stored heartbeat against the staleness
// window and announces a change of state, exactly once.
func (s *Scheduler) checkDiscordBotHealth(ctx context.Context) error {
	if s.BotHealthDB == nil {
		return errors.New("bot health database is not configured")
	}

	health, err := s.BotHealthDB.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to read bot health document: %w", err)
	}
	if health == nil || health.LastHeartbeatAt.IsZero() {
		// Never seen a heartbeat. Quiet is the correct behaviour here.
		zap.S().Debugw("discord bot health: no heartbeat recorded yet, nothing to compare")
		return nil
	}

	now := time.Now().UTC()
	lastHeartbeat := health.LastHeartbeatAt.UTC()

	current := models.BotHealthStateUp
	if now.Sub(lastHeartbeat) >= models.BotHeartbeatStaleAfter {
		current = models.BotHealthStateDown
	}

	previous := health.LastNotifiedState
	if previous == "" {
		// The document exists, so a heartbeat arrived at some point: the bot
		// was healthy then, and that is the baseline. Treating empty as "up"
		// means we never announce "the bot is up" (nobody needs telling), but
		// a later stale window is still a real transition we can report.
		previous = models.BotHealthStateUp
	}
	if previous == current {
		return nil
	}

	if current == models.BotHealthStateDown {
		// The bot stopped at (approximately) its last heartbeat, so that is
		// what we record as the start of the outage and what the recovery
		// message measures from.
		claimed, err := s.BotHealthDB.ClaimTransition(ctx, previous, current, now, lastHeartbeat)
		if err != nil {
			return fmt.Errorf("failed to claim bot down transition: %w", err)
		}
		if !claimed {
			// Another dyno claimed this transition and is posting the alert.
			zap.S().Debugw("discord bot health: down transition already claimed elsewhere")
			return nil
		}
		zap.S().Warnw("discord bot appears to be down", "lastHeartbeat", lastHeartbeat.Format(time.RFC3339))
		sendBotDownAlert(s.instanceID, health, lastHeartbeat, now)
		reportBotIncidentOpened(ctx, s.instanceID, lastHeartbeat, now)
		return nil
	}

	// Recovery. Measure the outage before clearing downSince.
	downFor := botOutageDuration(health, lastHeartbeat)
	claimed, err := s.BotHealthDB.ClaimTransition(ctx, previous, current, now, time.Time{})
	if err != nil {
		return fmt.Errorf("failed to claim bot recovery transition: %w", err)
	}
	if !claimed {
		zap.S().Debugw("discord bot health: recovery transition already claimed elsewhere")
		return nil
	}
	zap.S().Infow("discord bot is heartbeating again", "downFor", downFor.String())
	sendBotRecoveryAlert(s.instanceID, health, lastHeartbeat, downFor)
	reportBotIncidentResolved(ctx, s.instanceID, downFor)
	return nil
}

// botStatusSlug is the Discord bot's slug on the status page, and its label in
// the status repository.
const botStatusSlug = "discord-bot"

// reportBotIncidentOpened puts the outage on the public status page.
//
// The staff alert has already been sent by the time this runs, so a failure
// here loses the public incident, not the warning. It is reported as a cron
// failure and nothing more: the transition is already claimed, so retrying it
// would need a second transition, and announcing the outage twice to chase a
// GitHub hiccup is worse than one missing incident.
func reportBotIncidentOpened(ctx context.Context, instanceID string, lastHeartbeat, now time.Time) {
	if !statusIncidentConfigured() {
		return
	}
	silentFor := humanizeBotDuration(now.Sub(lastHeartbeat))
	body := "The Discord bot has stopped reporting in, so slash commands and community autocomplete in Discord are not responding. " +
		"The website and the mobile app are not affected.\n\n" +
		"Last heartbeat: " + lastHeartbeat.Format("2006-01-02 15:04 UTC") + ", about " + silentFor + " ago.\n\n" +
		"We are looking into it and will update this incident when the bot is back."

	if _, err := OpenStatusIncident(ctx, botStatusSlug, "Discord bot is not responding", body); err != nil {
		zap.S().Errorw("failed to open the status page incident for the bot", "error", err)
		SendCronAlert(instanceID, botHealthJobName, err, map[string]string{"phase": "status_incident_open"})
	}
}

// reportBotIncidentResolved closes the public incident, with how long it ran.
func reportBotIncidentResolved(ctx context.Context, instanceID string, downFor time.Duration) {
	if !statusIncidentConfigured() {
		return
	}
	body := "Resolved: the Discord bot is reporting in again"
	if downFor > 0 {
		body += " after " + humanizeBotDuration(downFor)
	}
	body += ". Slash commands and autocomplete are working normally."

	if err := ResolveStatusIncident(ctx, botStatusSlug, body); err != nil {
		zap.S().Errorw("failed to resolve the status page incident for the bot", "error", err)
		SendCronAlert(instanceID, botHealthJobName, err, map[string]string{"phase": "status_incident_resolve"})
	}
}

// botOutageDuration is how long the bot was actually silent: from the last
// heartbeat before it went quiet (recorded when we announced the outage) to
// the heartbeat that brought it back.
//
// Falls back to the time we announced the outage if downSince is missing,
// which can only happen for a document written before that field existed.
// Never negative.
func botOutageDuration(health *models.BotHealth, recoveredAt time.Time) time.Duration {
	start := health.DownSince
	if start.IsZero() {
		start = health.LastNotifiedAt
	}
	if start.IsZero() {
		return 0
	}
	d := recoveredAt.Sub(start.UTC())
	if d < 0 {
		return 0
	}
	return d
}

// sendBotDownAlert announces an outage to the staff alerts channel.
func sendBotDownAlert(instanceID string, health *models.BotHealth, lastHeartbeat, now time.Time) {
	silentFor := humanizeBotDuration(now.Sub(lastHeartbeat))
	description := "The Discord bot has stopped reporting in. Last heartbeat " +
		lastHeartbeat.Format("2006-01-02 15:04 UTC") + ", about " + silentFor +
		" ago. Slash commands will fail until it is back. The bot runs on a volunteer host, so it most likely needs a restart there."

	postDiscordAlert(discordAlertOptions{
		webhookEnv:          websiteAlertsWebhookEnv,
		instanceID:          instanceID,
		name:                "Discord bot",
		detail:              errors.New("no heartbeat for " + silentFor),
		fields:              botAlertFields(health, lastHeartbeat),
		title:               "Discord bot offline",
		titleOverride:       "Discord bot offline",
		descriptionOverride: description,
		color:               0xef4444,
		nameLabel:           "Component",
		// No dedup window: the database claim already guarantees one alert
		// per transition, and a second genuine transition is news.
		dedup: 0,
	})
}

// sendBotRecoveryAlert announces that the bot is heartbeating again, and how
// long the outage lasted.
func sendBotRecoveryAlert(instanceID string, health *models.BotHealth, recoveredAt time.Time, downFor time.Duration) {
	humanized := humanizeBotDuration(downFor)
	description := "The Discord bot is reporting in again. It was down for " + humanized +
		". Slash commands should be working now."

	postDiscordAlert(discordAlertOptions{
		webhookEnv:          websiteAlertsWebhookEnv,
		instanceID:          instanceID,
		name:                "Discord bot",
		detail:              errors.New("recovered after " + humanized),
		fields:              botAlertFields(health, recoveredAt),
		title:               "Discord bot recovered",
		titleOverride:       "Discord bot back online",
		descriptionOverride: description,
		color:               0x22c55e,
		nameLabel:           "Component",
		dedup:               0,
	})
}

// botAlertFields decorates an alert with what the bot last told us. All of it
// is bot-reported and bounded at the write endpoint; none of it is secret.
func botAlertFields(health *models.BotHealth, heartbeatAt time.Time) map[string]string {
	fields := map[string]string{
		"Last heartbeat": heartbeatAt.Format(time.RFC3339),
		"Shard":          strconv.Itoa(health.ShardID),
		"Guilds":         strconv.Itoa(health.GuildCount),
	}
	if health.BotVersion != "" {
		fields["Bot version"] = health.BotVersion
	}
	return fields
}

// humanizeBotDuration phrases an outage the way a person would say it: "7
// minutes", "2 hours 5 minutes", "3 days 1 hour". Rounded to the minute,
// because the detection window itself is minutes wide and false precision
// would only invite arguments about it.
func humanizeBotDuration(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}
	d = d.Round(time.Minute)

	days := int(d / (24 * time.Hour))
	hours := int(d % (24 * time.Hour) / time.Hour)
	minutes := int(d % time.Hour / time.Minute)

	switch {
	case days > 0:
		out := pluralizeBotUnit(days, "day")
		if hours > 0 {
			out += " " + pluralizeBotUnit(hours, "hour")
		}
		return out
	case hours > 0:
		out := pluralizeBotUnit(hours, "hour")
		if minutes > 0 {
			out += " " + pluralizeBotUnit(minutes, "minute")
		}
		return out
	default:
		return pluralizeBotUnit(minutes, "minute")
	}
}

func pluralizeBotUnit(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}
