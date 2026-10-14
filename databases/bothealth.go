package databases

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/models"
)

const botHealthCollectionName = "botHealth"

// BotHealthDatabase is the single-document store behind Discord bot liveness.
//
// Everything is keyed on models.BotHealthDocID, so no index is needed beyond
// the _id index Mongo creates for us.
type BotHealthDatabase interface {
	// Get returns the heartbeat document, or (nil, nil) when it does not
	// exist yet. A missing document means we have never seen a heartbeat,
	// which is a normal state (fresh deploy, or a bot host that has not
	// updated yet) and must never be treated as an outage.
	Get(ctx context.Context) (*models.BotHealth, error)

	// RecordHeartbeat upserts the latest heartbeat. `at` is the API's own
	// receive time. On insert it seeds the notified state to "up" so the
	// first check has a healthy baseline to transition away from and nothing
	// to announce.
	RecordHeartbeat(ctx context.Context, hb models.BotHeartbeat, at time.Time) error

	// ClaimTransition atomically moves the notified state from `from` to `to`
	// and reports whether this caller is the one that made the move.
	//
	// This is the whole concurrency story for alerting: the compare and the
	// write are one conditional update, so when several dynos notice the same
	// transition in the same minute exactly one of them gets true back and
	// only that one posts to Discord. A caller that gets false has already
	// been beaten to it and must stay quiet.
	//
	// `downSince` is stored when moving to "down" (our estimate of when the
	// bot actually stopped) and cleared when moving back to "up".
	ClaimTransition(ctx context.Context, from, to string, at time.Time, downSince time.Time) (bool, error)
}

type botHealthDatabase struct {
	db DatabaseHelper
}

// NewBotHealthDatabase wires up the collection.
func NewBotHealthDatabase(db DatabaseHelper) BotHealthDatabase {
	return &botHealthDatabase{db: db}
}

func (b *botHealthDatabase) Get(ctx context.Context) (*models.BotHealth, error) {
	var health models.BotHealth
	err := b.db.Collection(botHealthCollectionName).
		FindOne(ctx, bson.M{"_id": models.BotHealthDocID}).
		Decode(&health)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &health, nil
}

func (b *botHealthDatabase) RecordHeartbeat(ctx context.Context, hb models.BotHeartbeat, at time.Time) error {
	update := bson.M{
		"$set": bson.M{
			"lastHeartbeatAt": at,
			"shardId":         hb.ShardID,
			"guildCount":      hb.GuildCount,
			"botVersion":      hb.Version,
			"uptimeSeconds":   hb.UptimeSeconds,
		},
		// Only on the very first heartbeat ever. Seeding "up" rather than
		// leaving it empty means the first scheduler pass sees no transition
		// and says nothing, instead of announcing a bot that just came up.
		"$setOnInsert": bson.M{"lastNotifiedState": models.BotHealthStateUp},
	}
	_, err := b.db.Collection(botHealthCollectionName).UpdateOne(
		ctx,
		bson.M{"_id": models.BotHealthDocID},
		update,
		options.Update().SetUpsert(true),
	)
	return err
}

func (b *botHealthDatabase) ClaimTransition(ctx context.Context, from, to string, at time.Time, downSince time.Time) (bool, error) {
	stateFilter := interface{}(from)
	if from == models.BotHealthStateUp {
		// A document written by an older build, or hand-created in Mongo, may
		// have no lastNotifiedState at all. nil in $in matches both an
		// explicit null and a missing field, so such a document still counts
		// as the healthy baseline the caller read it as.
		stateFilter = bson.M{"$in": bson.A{models.BotHealthStateUp, "", nil}}
	}

	set := bson.M{
		"lastNotifiedState": to,
		"lastNotifiedAt":    at,
	}
	update := bson.M{"$set": set}
	if downSince.IsZero() {
		update["$unset"] = bson.M{"downSince": ""}
	} else {
		set["downSince"] = downSince
	}

	// No upsert: if the document has vanished there is no heartbeat history
	// to reason about, and inventing one here could manufacture an alert.
	res, err := b.db.Collection(botHealthCollectionName).UpdateOne(
		ctx,
		bson.M{"_id": models.BotHealthDocID, "lastNotifiedState": stateFilter},
		update,
	)
	if err != nil {
		return false, err
	}
	if res == nil {
		return false, nil
	}
	return res.ModifiedCount > 0, nil
}
