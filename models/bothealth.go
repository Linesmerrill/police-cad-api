package models

import "time"

// Discord bot liveness.
//
// The bot runs on a volunteer's home machine rather than our own
// infrastructure, so we cannot watch a process or a dyno. Instead the bot
// posts a heartbeat and we treat silence as an outage. One outage went
// unnoticed until a support ticket arrived; this closes that gap to minutes.
const (
	// BotHealthDocID is the _id of the single heartbeat document. One
	// document, last write wins: with multiple shards this detects the whole
	// host going down (the real failure mode) rather than one shard of
	// several dying.
	BotHealthDocID = "discord-bot"

	// BotHealthStateUp / BotHealthStateDown are the only two values stored in
	// LastNotifiedState, and the only two values the public endpoint reports.
	BotHealthStateUp   = "up"
	BotHealthStateDown = "down"

	// BotHeartbeatStaleAfter is how long we tolerate silence before calling
	// the bot down. The bot heartbeats every 60 seconds, so this allows two
	// missed beats before we complain: long enough to ride out a single
	// dropped request or a gateway reconnect, short enough that an outage is
	// noticed in minutes.
	BotHeartbeatStaleAfter = 3 * time.Minute
)

// Field caps for heartbeat input. The heartbeat is machine-written and the
// values end up in a Discord embed, so they are bounded rather than trusted.
const (
	// BotHeartbeatMaxShardID bounds the reported shard index. discord.js shard
	// ids are small; anything larger is a bug, not a bigger fleet.
	BotHeartbeatMaxShardID = 4095
	// BotHeartbeatMaxGuildCount bounds the reported guild count.
	BotHeartbeatMaxGuildCount = 1000000
	// BotHeartbeatMaxUptimeSeconds bounds reported uptime at ten years.
	BotHeartbeatMaxUptimeSeconds = int64(10 * 365 * 24 * 60 * 60)
	// BotHeartbeatMaxVersionLen bounds the reported version string.
	BotHeartbeatMaxVersionLen = 32
)

// BotHealth is the single heartbeat document, carrying both the last thing the
// bot told us and the alerting state machine's memory of what we have already
// announced.
//
// The notified state lives here rather than in process memory because the API
// runs on more than one dyno and restarts on every deploy: in-memory state
// would re-announce an ongoing outage after each restart, and two dynos would
// each announce the same transition.
type BotHealth struct {
	ID string `json:"-" bson:"_id"`

	// LastHeartbeatAt is stamped by the API from its own clock, never taken
	// from the request body, so a wrong clock on the bot host cannot make a
	// dead bot look alive.
	LastHeartbeatAt time.Time `json:"-" bson:"lastHeartbeatAt"`
	ShardID         int       `json:"-" bson:"shardId"`
	GuildCount      int       `json:"-" bson:"guildCount"`
	BotVersion      string    `json:"-" bson:"botVersion"`
	UptimeSeconds   int64     `json:"-" bson:"uptimeSeconds"`

	// LastNotifiedState is the state we have already announced to Discord:
	// "up", "down", or empty before the first check. Empty is read as "up",
	// because the only way the document exists at all is that a heartbeat
	// arrived, and an arriving heartbeat is not news worth announcing.
	LastNotifiedState string `json:"-" bson:"lastNotifiedState"`
	// LastNotifiedAt is when that announcement went out.
	LastNotifiedAt time.Time `json:"-" bson:"lastNotifiedAt"`
	// DownSince is the last heartbeat we saw before declaring the bot down,
	// i.e. our best estimate of when it actually stopped. Cleared on
	// recovery. Used to phrase how long the outage lasted.
	DownSince time.Time `json:"-" bson:"downSince"`
}

// BotHeartbeat is the accepted, already-validated contents of one heartbeat.
// It deliberately carries no auth material: the token is checked at the edge
// of the handler and never travels further.
type BotHeartbeat struct {
	ShardID       int    `json:"shardId"`
	GuildCount    int    `json:"guildCount"`
	Version       string `json:"version"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
}
