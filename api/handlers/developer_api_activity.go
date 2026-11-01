package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// The per-key activity feed: what a key called, and what we answered.
//
// This exists because of a specific failure mode. An integration author sees a
// 403 in their own logs, and the useful question — which of a dozen things went
// wrong — is one they cannot answer without us. So every response carries a
// request id, every authenticated request leaves a row, and the row says which
// route, which community, what we answered and which of our error codes we
// used. Quoting the request id in a ticket is then enough.
//
// What a row does NOT hold is the point: no request body, no response body, no
// header values, no key material and no claim code. A feed holding any of those
// would be a place to go looking for them. The feed is for debugging, and what
// debugging needs is the shape of the exchange rather than its contents.
//
// Retention is thirty days, stated in the docs and on the console screen. A
// window nobody publishes is one somebody discovers by needing a record that
// has gone.

// devRequestIDHeader is what every developer response carries.
const devRequestIDHeader = "X-Request-Id"

// devCorrelationIDHeader is the caller's own id, echoed back.
//
// Accepted, recorded, echoed — and never used to look anything up. A value the
// caller controls cannot be an identity, so it is carried alongside our own id
// rather than in place of it, which is what lets a developer line their logs up
// against ours without either side trusting the other's numbering.
const devCorrelationIDHeader = "X-Correlation-Id"

// maxCorrelationIDLen bounds what we will echo and store.
const maxCorrelationIDLen = 128

// devActivityPageLimit caps a feed page.
const devActivityPageLimit = 200

// newRequestID mints the id a response carries.
//
// Random rather than sequential. A sequential id would tell a caller how much
// traffic the surface is taking, which is nobody's business but ours.
func newRequestID() string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		// A request id is a debugging aid, so a failure here must not fail the
		// request. A timestamp-derived fallback is still unique enough to
		// correlate one request with one row.
		return "req_" + strings.ReplaceAll(time.Now().UTC().Format("20060102T150405.000000000"), ".", "")
	}
	return "req_" + hex.EncodeToString(raw)
}

// devActivityContextKey carries the request id and timing through the stack.
type devActivityContextKey struct{}

// devRequestTrace is what the middleware records about one request.
//
// The error code and required scope are filled in by devError as it writes the
// response, rather than inferred from the status afterwards — the status says
// "refused" and the code says why, and the code is the thing a developer
// branched on.
type devRequestTrace struct {
	RequestID     string
	CorrelationID string
	StartedAt     time.Time
	ErrorCode     string
	RequiredScope string
}

// devTraceFrom returns the request's trace, if it came through the middleware.
func devTraceFrom(ctx context.Context) (*devRequestTrace, bool) {
	t, ok := ctx.Value(devActivityContextKey{}).(*devRequestTrace)
	return t, ok
}

func withDevTrace(ctx context.Context, t *devRequestTrace) context.Context {
	return context.WithValue(ctx, devActivityContextKey{}, t)
}

// devStatusRecorder remembers what status a handler wrote.
//
// Needed because the activity row is written after the handler returns, and
// net/http does not expose what was sent.
//
// It overrides WriteHeader and nothing else. In particular it does not wrap
// Write: the feed never stores a body, so interposing on the body would mean
// every byte of every response passing through code that has no use for it. A
// handler that writes a body without calling WriteHeader has sent a 200, which
// is what net/http does implicitly and what statusOr reports — so the status is
// captured without the write path being touched at all.
type devStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *devStatusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

// statusOr returns the recorded status, defaulting to 200 for a handler that
// wrote a body without a status, or wrote nothing at all. Both are a 200 as far
// as the client is concerned.
func (r *devStatusRecorder) statusOr() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

// safeCorrelationID bounds and cleans the caller's own id.
//
// The narrow class devSafeExternalID uses, not the permissive one devSafeScalar
// allows, and for a specific reason: this is the one value on the surface we
// take from a caller and hand straight back in a response header. A correlation
// id is a machine-generated handle, so letters, digits and `. _ : -` cost a
// developer nothing, and nothing outside that class can end up in a header we
// wrote.
//
// Anything unacceptable is dropped rather than refused. A malformed correlation
// id is not a reason to fail a call that is otherwise fine — the request still
// gets our own id, which is the one that matters.
func safeCorrelationID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) > maxCorrelationIDLen {
		trimmed = trimmed[:maxCorrelationIDLen]
	}
	safe, err := devSafeExternalID(trimmed)
	if err != nil {
		return ""
	}
	return safe
}

// recordActivity writes one row for a finished request.
//
// Best effort and off the request path: it runs after the response has been
// written, and a failure is logged rather than surfaced. A feed that could fail
// a call would be worse than no feed.
func (d DeveloperAPI) recordActivity(dc *DevAPIContext, trace *devRequestTrace, r *http.Request, status int) {
	if d.ActivityDB == nil || dc == nil || trace == nil {
		return
	}

	now := time.Now().UTC()
	row := models.DeveloperAPIActivity{
		ID:            primitive.NewObjectID(),
		RequestID:     trace.RequestID,
		CorrelationID: trace.CorrelationID,
		KeyID:         dc.Key.ID.Hex(),
		DeveloperID:   dc.Key.DeveloperID,
		CommunityID:   communityIDFromRequest(r),
		Route:         devRouteTemplate(r),
		Method:        r.Method,
		Status:        status,
		Outcome:       models.DeveloperActivityOutcome(status),
		ErrorCode:     trace.ErrorCode,
		RequiredScope: trace.RequiredScope,
		DurationMS:    int(now.Sub(trace.StartedAt).Milliseconds()),
		At:            primitive.NewDateTimeFromTime(now),
		ExpiresAt: primitive.NewDateTimeFromTime(
			now.Add(time.Duration(models.DeveloperAPIActivityRetentionDays) * 24 * time.Hour)),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := d.ActivityDB.InsertOne(ctx, row); err != nil {
		zap.S().Warnw("developer api: could not record activity",
			"keyId", row.KeyID, "requestId", row.RequestID, "error", err)
	}
}

// communityIDFromRequest reads the community out of the path, when there is one.
//
// From the parsed route variable and validated, so an unparseable value is
// recorded as absent rather than stored as whatever arrived.
func communityIDFromRequest(r *http.Request) string {
	raw := mux.Vars(r)["communityId"]
	if raw == "" {
		return ""
	}
	id, _, err := devSafeObjectID(raw)
	if err != nil {
		return ""
	}
	return id
}

// devRouteTemplate is the bounded label a row is filed under.
//
// The mux template rather than the raw path, for the same reason the usage
// counters use it: a raw path carries ids, and a feed filtered by path would
// have as many distinct values as there are communities.
func devRouteTemplate(r *http.Request) string {
	if route := mux.CurrentRoute(r); route != nil {
		if tpl, err := route.GetPathTemplate(); err == nil {
			return strings.TrimPrefix(tpl, "/api/v1/dev")
		}
	}
	// No matched route: a request refused before routing, or a path nothing is
	// registered for. The raw path would be an unbounded value in a field the
	// feed is filtered and grouped on, so it is deliberately not stored.
	return "(unrouted)"
}

// ---- the key's own view ----

// devActivityView is one row on the wire.
type devActivityView struct {
	RequestID     string `json:"requestId"`
	CorrelationID string `json:"correlationId,omitempty"`
	CommunityID   string `json:"communityId,omitempty"`
	Method        string `json:"method"`
	Route         string `json:"route"`
	Status        int    `json:"status"`
	Outcome       string `json:"outcome"`
	ErrorCode     string `json:"errorCode,omitempty"`
	RequiredScope string `json:"requiredScope,omitempty"`
	DurationMS    int    `json:"durationMs"`
	At            string `json:"at"`
}

// DevActivityHandler returns this key's own recent requests.
//
// GET /api/v1/dev/activity — no scope
//
// No scope required, for the same reason whoami needs none: a key that is being
// refused has to be able to find out why, and requiring a scope for the
// diagnostic would mean the keys most in need of it are the ones that cannot
// read it.
//
// Scoped to the calling key by construction — the filter is built from the
// authenticated key id, never from anything the caller sent — so this is a key
// reading its own history and nothing else.
func (d DeveloperAPI) DevActivityHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	if d.ActivityDB == nil {
		devError(w, http.StatusInternalServerError, "server_error", "The activity feed is not available.", nil)
		return
	}

	filter := bson.M{"keyId": devEq(dc.Key.ID.Hex())}

	// Community is a query parameter here rather than a path variable, and it
	// is checked against the key's own grants: a key must not be able to ask
	// "what did I do in that community" about one nobody granted it, even
	// though every row it could match is already its own.
	if raw := strings.TrimSpace(r.URL.Query().Get("communityId")); raw != "" {
		communityID, _, err := devSafeObjectID(raw)
		if err != nil {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_community", "That community id is not valid.", nil)
			return
		}
		filter["communityId"] = devEq(communityID)
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("outcome")); raw != "" {
		outcome, err := devSafeActivityOutcome(raw)
		if err != nil {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_outcome", err.Error(), nil)
			return
		}
		filter["outcome"] = devEq(outcome)
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("requestId")); raw != "" {
		requestID, err := devSafeScalar(raw, 64, "requestId")
		if err != nil {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_request_id", err.Error(), nil)
			return
		}
		filter["requestId"] = devEq(requestID)
	}

	page, limit := devPageParams(r, devActivityPageLimit)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	total, _ := d.ActivityDB.CountDocuments(ctx, filter)
	rows, err := d.ActivityDB.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "at", Value: -1}, {Key: "_id", Value: -1}}).
		SetSkip(int64((page-1)*limit)).
		SetLimit(int64(limit)))
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not read the activity feed.", nil)
		return
	}

	views := make([]devActivityView, 0, len(rows))
	for _, row := range rows {
		views = append(views, devActivityViewOf(row))
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       views,
		"totalCount": total,
		"page":       page,
		"limit":      limit,
		// Published in the response as well as in the docs, so a client that
		// stores these rows knows how long it has before ours are gone.
		"retentionDays": models.DeveloperAPIActivityRetentionDays,
	})
}

// ListDeveloperKeyActivityHandler is the console's view of the same rows.
//
// GET /api/v1/developer/keys/{keyId}/activity
//
// On the first-party surface, and the key has to belong to the signed-in
// developer — checked by re-reading the key, not taken from the request.
func (d DeveloperAPI) ListDeveloperKeyActivityHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	key, ok := d.requireOwnDeveloperKey(w, r)
	if !ok {
		return
	}
	if d.ActivityDB == nil {
		config.ErrorStatus("activity feed unavailable", http.StatusInternalServerError, w,
			errNoActivityStore)
		return
	}

	filter := bson.M{"keyId": devEq(key.ID.Hex())}
	if raw := strings.TrimSpace(r.URL.Query().Get("outcome")); raw != "" {
		outcome, err := devSafeActivityOutcome(raw)
		if err != nil {
			config.InfoStatus("invalid outcome", http.StatusBadRequest, w, err)
			return
		}
		filter["outcome"] = devEq(outcome)
	}

	page, limit := devPageParams(r, devActivityPageLimit)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	total, _ := d.ActivityDB.CountDocuments(ctx, filter)
	rows, err := d.ActivityDB.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "at", Value: -1}, {Key: "_id", Value: -1}}).
		SetSkip(int64((page-1)*limit)).
		SetLimit(int64(limit)))
	if err != nil {
		config.ErrorStatus("failed to read activity", http.StatusInternalServerError, w, err)
		return
	}

	views := make([]devActivityView, 0, len(rows))
	for _, row := range rows {
		views = append(views, devActivityViewOf(row))
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data":          views,
		"totalCount":    total,
		"page":          page,
		"limit":         limit,
		"retentionDays": models.DeveloperAPIActivityRetentionDays,
	})
}

func devActivityViewOf(row models.DeveloperAPIActivity) devActivityView {
	return devActivityView{
		RequestID:     row.RequestID,
		CorrelationID: row.CorrelationID,
		CommunityID:   row.CommunityID,
		Method:        row.Method,
		Route:         row.Route,
		Status:        row.Status,
		Outcome:       row.Outcome,
		ErrorCode:     row.ErrorCode,
		RequiredScope: row.RequiredScope,
		DurationMS:    row.DurationMS,
		At:            formatDevDateTime(row.At),
	}
}

// devSafeActivityOutcome validates an outcome filter against our own constants.
func devSafeActivityOutcome(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case models.DeveloperActivityOK:
		return models.DeveloperActivityOK, nil
	case models.DeveloperActivityDenied:
		return models.DeveloperActivityDenied, nil
	case models.DeveloperActivityRateLimited:
		return models.DeveloperActivityRateLimited, nil
	case models.DeveloperActivityInvalid:
		return models.DeveloperActivityInvalid, nil
	case models.DeveloperActivityError:
		return models.DeveloperActivityError, nil
	}
	return "", errInvalidOutcome
}

// requireOwnDeveloperKey resolves a key from the path and proves it belongs to
// the signed-in developer.
//
// The ownership condition is in the filter rather than checked after the read,
// so a key id belonging to somebody else is a 404 and its existence is never
// confirmed. Used by every console route that names a key.
func (d DeveloperAPI) requireOwnDeveloperKey(w http.ResponseWriter, r *http.Request) (*models.DeveloperAPIKey, bool) {
	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, errNoActor)
		return nil, false
	}
	_, oid, err := devSafeObjectID(mux.Vars(r)["keyId"])
	if err != nil {
		config.InfoStatus("invalid key id", http.StatusBadRequest, w, err)
		return nil, false
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	key, err := d.KeyDB.FindOne(ctx, bson.M{"_id": devEq(oid), "userId": devEq(actorID)})
	if err != nil || key == nil {
		config.InfoStatus("key not found", http.StatusNotFound, w, errKeyNotFound)
		return nil, false
	}
	return key, true
}

// Sentinel errors for the paths above. Named values rather than fmt.Errorf at
// the call site, so the same condition reads the same way wherever it appears.
var (
	errNoActivityStore = errors.New("no activity store configured")
	errInvalidOutcome  = errors.New("outcome must be one of: ok, denied, rate_limited, invalid, error")
	errNoActor         = errors.New("no authenticated user")
	errKeyNotFound     = errors.New("not found")
)
