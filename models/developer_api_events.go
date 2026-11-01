package models

import (
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// What a key did, what changed because of it, and how a developer finds out.
//
// Three shapes here and they are deliberately separate, because they answer
// three different questions:
//
//	DeveloperAPIActivity  — "what did my key call, and what did you answer?"
//	                        A debugging record. One row per request.
//	DeveloperAPIEvent     — "what changed in this community?"
//	                        A change record. One row per thing that happened,
//	                        whoever caused it.
//	DeveloperWebhook      — "tell me when it happens, instead of me asking."
//
// The second and third share one event stream on purpose. The cursor endpoint
// and the webhooks deliver the same DeveloperAPIEvent rows with the same ids,
// so an integration can move from polling to webhooks without rewriting the
// handler that consumes them — and can run both while it migrates, because a
// stable event id makes the duplicate detectable.

// ---- activity: one row per request ----

// Activity outcomes, coarser than a status code on purpose. A developer
// filtering their feed wants "show me what was refused", not "show me the
// 403s and also the 401s and also the 422s".
const (
	DeveloperActivityOK          = "ok"
	DeveloperActivityDenied      = "denied"
	DeveloperActivityRateLimited = "rate_limited"
	DeveloperActivityInvalid     = "invalid"
	DeveloperActivityError       = "error"
)

// DeveloperActivityOutcome maps a status code to an outcome.
func DeveloperActivityOutcome(status int) string {
	switch {
	case status == 429:
		return DeveloperActivityRateLimited
	case status == 401 || status == 403:
		return DeveloperActivityDenied
	case status >= 500:
		return DeveloperActivityError
	case status >= 400:
		return DeveloperActivityInvalid
	default:
		return DeveloperActivityOK
	}
}

// DeveloperAPIActivityRetentionDays is how long a row lives.
//
// Published, in the docs and on the console screen, because a retention window
// nobody states is one somebody will discover by needing a record that is gone.
// Thirty days is long enough to reconcile a bad weekend and short enough that
// the collection does not become the largest thing in the database.
const DeveloperAPIActivityRetentionDays = 30

// DeveloperAPIActivity is one request a key made.
//
// What it does NOT hold is the point: no request body, no response body, no
// header values, no key material, no claim code. A feed that held any of those
// would be a place to go looking for them, and the thing a developer actually
// needs in order to debug is which route, which community, what we answered and
// which request it was.
type DeveloperAPIActivity struct {
	ID primitive.ObjectID `json:"id" bson:"_id"`
	// RequestID is ours, returned on every response in X-Request-Id. Quoting it
	// in a support ticket is enough for us to find this row.
	RequestID string `json:"requestId" bson:"requestId"`
	// CorrelationID is the caller's own, echoed from X-Correlation-Id when they
	// sent one. It is never used to look anything up on our side — a value the
	// caller controls cannot be an identity — but carrying it means a developer
	// can line their logs up against ours.
	CorrelationID string `json:"correlationId,omitempty" bson:"correlationId,omitempty"`

	KeyID       string `json:"keyId" bson:"keyId"`
	DeveloperID string `json:"developerId" bson:"developerId"`
	CommunityID string `json:"communityId,omitempty" bson:"communityId,omitempty"`

	// Route is the mux path template, not the raw path. A raw path carries
	// community and player ids, which would make this field unbounded and
	// turn a grouped count into an infinite set of keys.
	Route  string `json:"route" bson:"route"`
	Method string `json:"method" bson:"method"`
	Status int    `json:"status" bson:"status"`

	Outcome string `json:"outcome" bson:"outcome"`
	// ErrorCode is our own stable code, the one the response body carried, so a
	// developer reading the feed sees the same string their code branched on.
	ErrorCode string `json:"errorCode,omitempty" bson:"errorCode,omitempty"`
	// RequiredScope is filled in on a missing_scope refusal, which is the most
	// common thing anyone needs this feed to explain.
	RequiredScope string `json:"requiredScope,omitempty" bson:"requiredScope,omitempty"`

	DurationMS int                `json:"durationMs" bson:"durationMs"`
	At         primitive.DateTime `json:"at" bson:"at"`
	ExpiresAt  primitive.DateTime `json:"-" bson:"expiresAt"`
}

// ---- events: one row per thing that changed ----

// Event types. Named after what happened in the product rather than after the
// endpoint that caused it, because the same change can arrive from the website,
// the mobile app, the bot or a key, and an integration cares about the change.
const (
	EventCallCreated         = "call.created"
	EventCallUpdated         = "call.updated"
	EventCallClosed          = "call.closed"
	EventCallAssigned        = "call.assigned"
	EventUnitStatusChanged   = "unit.status_changed"
	EventRecordCreated       = "record.created"
	EventReportDraftFiled    = "report_draft.filed"
	EventReportDraftRejected = "report_draft.rejected"
	EventWarrantRequested    = "warrant.requested"
	EventWarrantReviewed     = "warrant.reviewed"
)

// DeveloperEventTypes is every type we emit, for validating a webhook's
// subscription list and for the docs.
func DeveloperEventTypes() []string {
	return []string{
		EventCallCreated,
		EventCallUpdated,
		EventCallClosed,
		EventCallAssigned,
		EventUnitStatusChanged,
		EventRecordCreated,
		EventReportDraftFiled,
		EventReportDraftRejected,
		EventWarrantRequested,
		EventWarrantReviewed,
	}
}

// IsValidDeveloperEventType reports whether a type is one we emit.
func IsValidDeveloperEventType(eventType string) bool {
	for _, t := range DeveloperEventTypes() {
		if t == strings.TrimSpace(eventType) {
			return true
		}
	}
	return false
}

// DeveloperEventRetentionDays is how long an event stays readable.
//
// Shorter than the activity feed, and published for the same reason. Seven days
// is the window a cursor can safely be stale for: a server that has been off
// longer than that should re-read the current state rather than replay a week
// of changes, and saying so is better than letting it try.
const DeveloperEventRetentionDays = 7

// DeveloperAPIEvent is one change in a community.
//
// Its _id is the cursor. ObjectIDs embed a timestamp and a counter and sort in
// generation order, so "give me everything after this id" is one indexed range
// scan with no extra sequence to maintain and nothing to get out of step.
//
// Data is a small, flat, known-shaped map rather than the changed document.
// Sending the document would mean this collection holding a copy of every call
// in the product, and it would leak whatever fields the document happens to
// gain later. An integration gets the ids and the handful of fields it needs
// to act, and reads the full resource if it wants more.
type DeveloperAPIEvent struct {
	ID          primitive.ObjectID `json:"id" bson:"_id"`
	Type        string             `json:"type" bson:"type"`
	CommunityID string             `json:"communityId" bson:"communityId"`

	// ResourceID and ResourceExternalID name the thing that changed, in our
	// terms and in the integration's. The second is empty for anything a key
	// did not create, which is most of what the website does.
	ResourceID         string `json:"resourceId,omitempty" bson:"resourceId,omitempty"`
	ResourceExternalID string `json:"resourceExternalId,omitempty" bson:"resourceExternalId,omitempty"`
	ResourceSource     string `json:"resourceSource,omitempty" bson:"resourceSource,omitempty"`

	// Actor is who caused it, as a kind rather than a name: "website",
	// "developer_api", "mobile", "bot". An integration reconciling wants to
	// know whether it was itself, not who the person was.
	Actor string `json:"actor" bson:"actor"`
	// ActorKeyID is set when a key caused it, so an integration can ignore its
	// own writes rather than processing them twice.
	ActorKeyID string `json:"actorKeyId,omitempty" bson:"actorKeyId,omitempty"`

	Data map[string]string `json:"data,omitempty" bson:"data,omitempty"`

	At        primitive.DateTime `json:"at" bson:"at"`
	ExpiresAt primitive.DateTime `json:"-" bson:"expiresAt"`
}

// Actor kinds.
const (
	EventActorWebsite      = "website"
	EventActorDeveloperAPI = "developer_api"
	EventActorMobile       = "mobile"
	EventActorBot          = "bot"
	EventActorSystem       = "system"
)

// ---- webhooks ----

// Webhook lifecycle.
const (
	WebhookStatusActive   = "active"
	WebhookStatusDisabled = "disabled"
)

// Webhook limits, published in the docs.
const (
	// MaxWebhooksPerKey keeps one key from fanning out to a dozen endpoints.
	MaxWebhooksPerKey = 3
	// WebhookMaxAttempts is how many times a delivery is retried before it is
	// given up on. With the backoff below that spans about half an hour, which
	// covers a restart or a deploy without holding a failing endpoint open for
	// a day.
	WebhookMaxAttempts = 6
	// WebhookDisableAfterConsecutiveFailures turns an endpoint off when it has
	// stopped answering entirely. An endpoint that 500s forever is not a
	// transient failure, and retrying it indefinitely spends our capacity on
	// somebody else's outage.
	WebhookDisableAfterConsecutiveFailures = 20
	// WebhookSignatureToleranceSeconds is how far out of date a signature may
	// be when a receiver checks it. Five minutes: long enough for clock skew
	// and a slow queue, short enough that a captured request is not replayable
	// tomorrow.
	WebhookSignatureToleranceSeconds = 300
)

// WebhookRetryBackoff is the delay before each attempt, in order.
//
// Published rather than computed, so the docs can state it exactly and a
// receiver can reason about when it will be called again. Front-loaded,
// because most failures are a restart.
func WebhookRetryBackoff() []time.Duration {
	return []time.Duration{
		10 * time.Second,
		30 * time.Second,
		2 * time.Minute,
		5 * time.Minute,
		15 * time.Minute,
	}
}

// DeveloperWebhook is one endpoint a key wants events sent to.
//
// SecretHash holds an HMAC of the signing secret and the secret itself is
// shown exactly once, like the key. That is the same decision made for the same
// reason: a database dump must not hand anyone the ability to forge a delivery
// that a receiver would accept.
type DeveloperWebhook struct {
	ID          primitive.ObjectID `json:"id" bson:"_id"`
	KeyID       string             `json:"keyId" bson:"keyId"`
	DeveloperID string             `json:"developerId" bson:"developerId"`
	URL         string             `json:"url" bson:"url"`
	SecretHash  string             `json:"-" bson:"secretHash"`
	// SecretPrefix is the first few characters, so the console can tell two
	// secrets apart on screen without holding either.
	SecretPrefix string `json:"secretPrefix" bson:"secretPrefix"`
	// Events is what this endpoint wants. Empty means every type, which is
	// what most integrations want and saves them keeping the list in step with
	// ours.
	Events []string `json:"events" bson:"events"`
	Status string   `json:"status" bson:"status"`

	// Communities scopes the endpoint. Empty means every community the key is
	// granted, re-read per delivery — so a community owner withdrawing their
	// grant stops deliveries about their community immediately, exactly as it
	// stops requests.
	Communities []string `json:"communities,omitempty" bson:"communities,omitempty"`

	ConsecutiveFailures int                 `json:"consecutiveFailures" bson:"consecutiveFailures"`
	LastDeliveryAt      *primitive.DateTime `json:"lastDeliveryAt,omitempty" bson:"lastDeliveryAt,omitempty"`
	LastStatus          int                 `json:"lastStatus,omitempty" bson:"lastStatus,omitempty"`
	LastError           string              `json:"lastError,omitempty" bson:"lastError,omitempty"`
	DisabledReason      string              `json:"disabledReason,omitempty" bson:"disabledReason,omitempty"`

	// Cursor is the last event this endpoint was sent. The delivery worker
	// reads forward from it, which is the same mechanism as the polling
	// endpoint — so a webhook that was disabled and re-enabled resumes rather
	// than replaying everything or skipping a gap.
	Cursor string `json:"-" bson:"cursor,omitempty"`

	SecretRotatedAt *primitive.DateTime `json:"secretRotatedAt,omitempty" bson:"secretRotatedAt,omitempty"`
	CreatedAt       primitive.DateTime  `json:"createdAt" bson:"createdAt"`
	UpdatedAt       primitive.DateTime  `json:"updatedAt" bson:"updatedAt"`
}

// WantsEvent reports whether this endpoint has asked for a type.
func (w DeveloperWebhook) WantsEvent(eventType string) bool {
	if len(w.Events) == 0 {
		return true
	}
	for _, t := range w.Events {
		if t == eventType {
			return true
		}
	}
	return false
}

// WantsCommunity reports whether this endpoint has asked for a community.
func (w DeveloperWebhook) WantsCommunity(communityID string) bool {
	if len(w.Communities) == 0 {
		return true
	}
	for _, c := range w.Communities {
		if c == communityID {
			return true
		}
	}
	return false
}

// DeveloperWebhookDelivery is one attempt to hand an event over.
//
// Kept so the console can show a developer why their endpoint is not receiving
// anything, which is the question every webhook integration eventually asks.
// It holds the status and our own error text, never the response body: a
// receiver's error page is their content, and storing it would make this a
// place to find whatever they happened to print.
type DeveloperWebhookDelivery struct {
	ID         primitive.ObjectID `json:"id" bson:"_id"`
	WebhookID  string             `json:"webhookId" bson:"webhookId"`
	KeyID      string             `json:"keyId" bson:"keyId"`
	EventID    string             `json:"eventId" bson:"eventId"`
	EventType  string             `json:"eventType" bson:"eventType"`
	Attempt    int                `json:"attempt" bson:"attempt"`
	Status     int                `json:"status" bson:"status"`
	Succeeded  bool               `json:"succeeded" bson:"succeeded"`
	Error      string             `json:"error,omitempty" bson:"error,omitempty"`
	DurationMS int                `json:"durationMs" bson:"durationMs"`
	At         primitive.DateTime `json:"at" bson:"at"`
	ExpiresAt  primitive.DateTime `json:"-" bson:"expiresAt"`
}

// DeveloperWebhookDeliveryRetentionDays is how long a delivery record lives.
const DeveloperWebhookDeliveryRetentionDays = 14
