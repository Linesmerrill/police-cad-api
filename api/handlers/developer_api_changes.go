package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// "What changed since I last asked?"
//
// The thing this replaces: an integration that wants to know whether a call was
// closed has to re-read every call. On a busy community that is a sweep per
// tick, and it is why polling got a reputation for being expensive. One cursor
// read returns the handful of things that actually changed, so polling costs a
// request rather than a scan — which is the whole of Hiceguy's item 5 before
// webhooks enter it.
//
// The cursor is an event id, and the events are the same rows the webhooks
// deliver. That is deliberate: an integration can start on the cursor, move to
// webhooks later without rewriting the handler that consumes an event, and run
// both while it migrates — because a stable event id makes the duplicate
// detectable rather than a problem.
//
// Events are written here by a single helper, called from the write paths that
// matter, rather than inferred from a change stream. A change stream would be
// more complete and would also mean this feature depending on a replica-set
// feature and a long-lived connection per dyno; a helper at the write path is
// something a reader can follow and a test can drive.

// devChangesPageLimit caps one page of changes.
//
// Generous, because the point is to let an integration catch up after an
// outage in a few requests rather than a hundred.
const devChangesPageLimit = 200

// devCursorHeader lets a client read the next cursor without parsing the body,
// which a Lua HTTP callback appreciates.
const devCursorHeader = "X-LPC-Cursor"

// DevChangesHandler returns the community's changes after a cursor.
//
// GET /api/v1/dev/communities/{communityId}/changes — community:read
//
// Scoped to the community in the path, which the key had to be granted to reach
// this handler, so there is no cross-community leak to defend against here
// beyond the one ResolveCommunity already closed.
//
// With no cursor it returns nothing and hands back the current position. That
// is deliberate: a first call should not replay a week of history that the
// client is about to read in full anyway. It says so in the response.
func (d DeveloperAPI) DevChangesHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}
	if d.EventDB == nil {
		devError(w, http.StatusInternalServerError, "server_error", "The change feed is not available.", nil)
		return
	}

	filter := bson.M{"communityId": devEq(cc.Community.ID.Hex())}

	rawCursor := strings.TrimSpace(r.URL.Query().Get("since"))
	primed := rawCursor != ""
	if primed {
		_, oid, err := devSafeObjectID(rawCursor)
		if err != nil {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_cursor",
				"since must be a cursor from a previous response.", nil)
			return
		}
		// Strictly greater than, so the event the cursor names is not handed
		// back again. An inclusive bound would redeliver one event on every
		// poll forever.
		filter["_id"] = bson.M{"$gt": oid}
	}

	if raw := strings.TrimSpace(r.URL.Query().Get("types")); raw != "" {
		types, err := devSafeEventTypes(raw)
		if err != nil {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_event_type", err.Error(),
				map[string]interface{}{"eventTypes": models.DeveloperEventTypes()})
			return
		}
		filter["type"] = bson.M{"$in": types}
	}

	// Excluding a key's own writes is the difference between a tight loop and a
	// working integration: a mod that closes a call, receives its own
	// "call.closed", and acts on it will close it again.
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("excludeSelf")), "true") {
		filter["actorKeyId"] = bson.M{"$ne": dc.Key.ID.Hex()}
	}

	_, limit := devPageParams(r, devChangesPageLimit)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Sorted by _id ascending, which for ObjectIDs is generation order. No
	// skip: the cursor is the position, which is the whole reason this is
	// cheaper than a page number — a client that falls behind does not pay for
	// the rows it already has.
	events := []models.DeveloperAPIEvent{}
	if primed {
		found, err := d.EventDB.Find(ctx, filter, options.Find().
			SetSort(bson.D{{Key: "_id", Value: 1}}).
			SetLimit(int64(limit)))
		if err != nil {
			devError(w, http.StatusInternalServerError, "server_error", "Could not read the change feed.", nil)
			return
		}
		events = found
	}

	cursor := rawCursor
	if len(events) > 0 {
		cursor = events[len(events)-1].ID.Hex()
	} else if !primed {
		// Priming: hand back a cursor that points at now, so the next call
		// returns only what has happened since.
		cursor = d.currentEventCursor(ctx, cc.Community.ID.Hex())
	}

	views := make([]devEventView, 0, len(events))
	for _, event := range events {
		views = append(views, devEventViewOf(event))
	}

	// hasMore rather than a total count. A total would mean a second query on
	// every poll for a number the client cannot act on; "call again" is what it
	// actually needs to know.
	hasMore := len(events) == limit

	w.Header().Set(devCursorHeader, cursor)
	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data":          views,
		"cursor":        cursor,
		"hasMore":       hasMore,
		"primed":        !primed,
		"retentionDays": models.DeveloperEventRetentionDays,
	})
}

// currentEventCursor returns a cursor meaning "everything up to now".
//
// The newest event's id when there is one, and otherwise an ObjectID minted
// from the current time — which sorts after every event written before now, so
// a community with no history yet still gets a usable position instead of an
// empty string the client has to special-case.
func (d DeveloperAPI) currentEventCursor(ctx context.Context, communityID string) string {
	newest, err := d.EventDB.Find(ctx, bson.M{"communityId": devEq(communityID)},
		options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}).SetLimit(1))
	if err == nil && len(newest) == 1 {
		return newest[0].ID.Hex()
	}
	return primitive.NewObjectIDFromTimestamp(time.Now()).Hex()
}

// devEventView is one change on the wire.
//
// The same shape the webhook body carries, so a handler written for one works
// for the other.
type devEventView struct {
	ID                 string            `json:"id"`
	Type               string            `json:"type"`
	CommunityID        string            `json:"communityId"`
	ResourceID         string            `json:"resourceId,omitempty"`
	ResourceExternalID string            `json:"resourceExternalId,omitempty"`
	ResourceSource     string            `json:"resourceSource,omitempty"`
	Actor              string            `json:"actor"`
	ActorKeyID         string            `json:"actorKeyId,omitempty"`
	Data               map[string]string `json:"data,omitempty"`
	At                 string            `json:"at"`
}

func devEventViewOf(event models.DeveloperAPIEvent) devEventView {
	return devEventView{
		ID:                 event.ID.Hex(),
		Type:               event.Type,
		CommunityID:        event.CommunityID,
		ResourceID:         event.ResourceID,
		ResourceExternalID: event.ResourceExternalID,
		ResourceSource:     event.ResourceSource,
		Actor:              event.Actor,
		ActorKeyID:         event.ActorKeyID,
		Data:               event.Data,
		At:                 formatDevDateTime(event.At),
	}
}

// devSafeEventTypes validates a comma-separated type filter against our own
// constants, so what reaches the query is always a list of known literals.
func devSafeEventTypes(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		wanted := strings.TrimSpace(part)
		if wanted == "" {
			continue
		}
		matched := ""
		for _, known := range models.DeveloperEventTypes() {
			if known == wanted {
				matched = known
				break
			}
		}
		if matched == "" {
			return nil, fmt.Errorf("%q is not an event type we emit", wanted)
		}
		out = append(out, matched)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("types was empty")
	}
	return out, nil
}

// ---- emitting ----

// devEvent describes a change to record.
type devEvent struct {
	Type               string
	CommunityID        string
	ResourceID         string
	ResourceExternalID string
	ResourceSource     string
	Actor              string
	ActorKeyID         string
	Data               map[string]string
}

// EmitDeveloperEvent records a change for the cursor feed and the webhooks.
//
// Exported because the website's own handlers call it too. That is the point:
// an integration polling for "what changed" cares about a call a dispatcher
// closed on the website every bit as much as one it closed itself, and a feed
// that only saw the API's own writes would be a feed of the integration's own
// echo.
//
// Best effort and never on the critical path. A change that happened and was
// not recorded costs an integration a stale cursor; a write that failed because
// the event log was unhappy would cost a player their call. So failures are
// logged and swallowed.
//
// Nothing sensitive goes in Data. It is a flat map of ids and short labels, and
// the one rule is that a value here is something the resource's own read
// endpoint would already have returned to this caller.
func EmitDeveloperEvent(eventDB databases.DeveloperEventDatabase, e devEvent) {
	if eventDB == nil || e.CommunityID == "" || e.Type == "" {
		return
	}
	if !models.IsValidDeveloperEventType(e.Type) {
		// A typo in an event type would produce a row nobody can subscribe to.
		// Louder than silence, because it is a programming error rather than a
		// runtime condition.
		zap.S().Errorw("developer api: refusing to emit an unknown event type", "type", e.Type)
		return
	}

	now := time.Now().UTC()
	event := models.DeveloperAPIEvent{
		ID:                 primitive.NewObjectID(),
		Type:               e.Type,
		CommunityID:        e.CommunityID,
		ResourceID:         e.ResourceID,
		ResourceExternalID: e.ResourceExternalID,
		ResourceSource:     e.ResourceSource,
		Actor:              e.Actor,
		ActorKeyID:         e.ActorKeyID,
		Data:               e.Data,
		At:                 primitive.NewDateTimeFromTime(now),
		ExpiresAt: primitive.NewDateTimeFromTime(
			now.Add(time.Duration(models.DeveloperEventRetentionDays) * 24 * time.Hour)),
	}

	// Off the request's own context: the caller has already been answered, or
	// is about to be, and a cancelled request must not drop the event.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := eventDB.InsertOne(ctx, event); err != nil {
			zap.S().Warnw("developer api: could not record a change event",
				"type", event.Type, "communityId", event.CommunityID, "error", err)
		}
	}()
}

// emit is the method form, for the developer handlers that already have d.
func (d DeveloperAPI) emit(e devEvent) {
	EmitDeveloperEvent(d.EventDB, e)
}

// devKeyEvent fills in the actor fields for a change a key caused.
func devKeyEvent(dc *DevAPIContext, eventType, communityID string) devEvent {
	e := devEvent{
		Type:        eventType,
		CommunityID: communityID,
		Actor:       models.EventActorDeveloperAPI,
	}
	if dc != nil {
		e.ActorKeyID = dc.Key.ID.Hex()
	}
	return e
}
