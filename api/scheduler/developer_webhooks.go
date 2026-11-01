package scheduler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/models"
)

// Delivering developer webhooks.
//
// A job rather than a goroutine per event, for one reason: an endpoint that is
// down must not be retried from twenty dynos at once. The job takes a lock,
// walks each active endpoint forward from its own cursor, and advances the
// cursor only on success — so a failure leaves the endpoint exactly where it
// was and the next run retries from there.
//
// That cursor is the same one the polling endpoint uses, which is what makes a
// missed delivery degrade to the polling case instead of to a gap: the event is
// still readable for its retention window whether or not we ever handed it over.
//
// What this job does NOT do is retry a single event in a tight loop. The
// backoff is expressed as "how long since this endpoint last failed", checked
// before each run, so a dead endpoint costs one request per backoff step rather
// than per event in its queue.

// webhookBatchPerEndpoint bounds how far one run advances one endpoint.
//
// Small on purpose. An endpoint that has been off for an hour catches up over
// several runs rather than receiving three hundred requests in one burst, which
// is a thing that gets an integration rate-limited by its own host.
const webhookBatchPerEndpoint = 25

// webhookDeliveryTimeout is how long a receiver has to answer.
//
// Short. A receiver should acknowledge and queue, not do its work inline, and
// holding a connection open for longer would let one slow endpoint delay
// everybody else's deliveries.
const webhookDeliveryTimeout = 10 * time.Second

// webhookUserAgent identifies us to a receiver's logs.
const webhookUserAgent = "LinesPoliceCAD-Webhooks/1"

// deliverDeveloperWebhooks is the scheduled job.
func (s *Scheduler) deliverDeveloperWebhooks() {
	const jobName = "deliverDeveloperWebhooks"
	s.recordStart(jobName)

	if s.WebhookDB == nil || s.EventDB == nil {
		s.recordSkipped(jobName)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	acquired, err := s.LockDB.TryAcquireLock(ctx, "developer_webhook_delivery", s.instanceID, 90*time.Second)
	if err != nil {
		zap.S().Errorw("failed to acquire lock for webhook delivery", "error", err)
		s.recordError(jobName, err)
		SendCronAlert(s.instanceID, jobName, err, map[string]string{"phase": "lock_acquire"})
		return
	}
	if !acquired {
		s.recordSkipped(jobName)
		return
	}
	defer s.LockDB.ReleaseLock(ctx, "developer_webhook_delivery", s.instanceID)

	hooks, err := s.WebhookDB.Find(ctx, bson.M{"status": bson.M{"$eq": models.WebhookStatusActive}})
	if err != nil {
		zap.S().Errorw("failed to load webhooks", "error", err)
		s.recordError(jobName, err)
		SendCronAlert(s.instanceID, jobName, err, map[string]string{"phase": "load_webhooks"})
		return
	}

	delivered := 0
	for _, hook := range hooks {
		if ctx.Err() != nil {
			// Out of time for this run. The cursors are where they were, so
			// the next run picks up exactly here.
			break
		}
		if !s.webhookDueForDelivery(hook) {
			continue
		}
		n, hookErr := s.deliverForWebhook(ctx, hook)
		delivered += n
		if hookErr != nil {
			// One endpoint failing is normal operation, not a job failure.
			// Alerting on it would page us about somebody else's outage.
			zap.S().Infow("developer webhook delivery failed",
				"webhookId", hook.ID.Hex(), "url", hook.URL, "error", hookErr)
		}
	}

	if delivered > 0 {
		zap.S().Infow("developer webhooks delivered", "count", delivered)
	}
	s.recordSuccess(jobName)
}

// webhookDueForDelivery applies the published backoff.
//
// Expressed as "how long since the last attempt", rather than a per-event
// schedule, so a dead endpoint costs one request per backoff step however many
// events are queued behind it.
func (s *Scheduler) webhookDueForDelivery(hook models.DeveloperWebhook) bool {
	if hook.ConsecutiveFailures == 0 || hook.LastDeliveryAt == nil {
		return true
	}
	backoff := models.WebhookRetryBackoff()
	step := hook.ConsecutiveFailures - 1
	if step >= len(backoff) {
		step = len(backoff) - 1
	}
	return time.Since(hook.LastDeliveryAt.Time()) >= backoff[step]
}

// deliverForWebhook walks one endpoint forward from its cursor.
func (s *Scheduler) deliverForWebhook(ctx context.Context, hook models.DeveloperWebhook) (int, error) {
	// The key first, re-read now. The endpoint's own status and the community
	// grants are not enough: revoking a key stops its requests on the next
	// call, and it has to stop its deliveries too, or a revoked integration
	// would carry on being told everything that happens. Same for a suspended
	// developer, which stops every key they hold at once on the request path.
	live, err := s.keyIsLive(ctx, hook.KeyID)
	if err != nil {
		return 0, err
	}
	if !live {
		// Not an error, and the cursor is deliberately left where it is: if the
		// key is reinstated the endpoint resumes rather than having silently
		// skipped everything that happened while it was off.
		return 0, nil
	}

	filter := bson.M{}
	if hook.Cursor != "" {
		oid, err := primitive.ObjectIDFromHex(hook.Cursor)
		if err == nil {
			filter["_id"] = bson.M{"$gt": oid}
		}
	}
	if len(hook.Communities) > 0 {
		filter["communityId"] = bson.M{"$in": hook.Communities}
	}
	if len(hook.Events) > 0 {
		filter["type"] = bson.M{"$in": hook.Events}
	}

	events, err := s.EventDB.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "_id", Value: 1}}).
		SetLimit(webhookBatchPerEndpoint))
	if err != nil {
		return 0, err
	}
	if len(events) == 0 {
		return 0, nil
	}

	// The key's grants, re-read now rather than trusted from the subscription.
	// A community owner who withdrew their grant this morning must stop
	// receiving deliveries about their community, exactly as their withdrawal
	// stops requests — and a subscription saved last month cannot know that.
	granted, grantErr := s.grantedCommunities(ctx, hook.KeyID)
	if grantErr != nil {
		return 0, grantErr
	}

	sent := 0
	for _, event := range events {
		if ctx.Err() != nil {
			break
		}
		// Not granted: skip the event and advance past it. Holding the cursor
		// would stall the endpoint on an event it is never allowed to receive.
		if !containsStr(granted, event.CommunityID) {
			if err := s.advanceWebhookCursor(ctx, hook.ID, event.ID.Hex()); err != nil {
				return sent, err
			}
			continue
		}

		status, duration, deliverErr := s.sendWebhook(ctx, hook, event)
		s.recordDelivery(ctx, hook, event, status, duration, deliverErr)

		if deliverErr != nil {
			if err := s.noteWebhookFailure(ctx, hook, status, deliverErr); err != nil {
				return sent, err
			}
			// Stop at the first failure. Carrying on would deliver out of
			// order, and an integration that receives "call.closed" before
			// "call.created" has to handle a case we could simply not create.
			return sent, deliverErr
		}

		if err := s.advanceWebhookCursor(ctx, hook.ID, event.ID.Hex()); err != nil {
			return sent, err
		}
		sent++
	}

	if sent > 0 {
		if err := s.clearWebhookFailures(ctx, hook.ID); err != nil {
			return sent, err
		}
	}
	return sent, nil
}

// sendWebhook makes one signed request.
func (s *Scheduler) sendWebhook(ctx context.Context, hook models.DeveloperWebhook, event models.DeveloperAPIEvent) (int, time.Duration, error) {
	// The SSRF gate again, immediately before the request and not only when
	// the endpoint was saved. DNS is not ours: a hostname that resolved
	// publicly when a developer saved it can point at 127.0.0.1 now.
	if err := assertPublicWebhookURL(hook.URL); err != nil {
		return 0, 0, err
	}

	deliveryID := primitive.NewObjectID().Hex()
	body, err := json.Marshal(map[string]interface{}{
		"deliveryId": deliveryID,
		// The event, in the same shape the polling endpoint returns, so one
		// handler serves both transports.
		"event": map[string]interface{}{
			"id":                 event.ID.Hex(),
			"type":               event.Type,
			"communityId":        event.CommunityID,
			"resourceId":         event.ResourceID,
			"resourceExternalId": event.ResourceExternalID,
			"resourceSource":     event.ResourceSource,
			"actor":              event.Actor,
			"actorKeyId":         event.ActorKeyID,
			"data":               event.Data,
			"at":                 event.At.Time().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return 0, 0, err
	}

	secret, err := s.webhookSigningSecret(ctx, hook)
	if err != nil {
		return 0, 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	now := time.Now()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", webhookUserAgent)
	req.Header.Set("X-LPC-Signature", signWebhookBody(secret, now, body))
	req.Header.Set("X-LPC-Event-Id", event.ID.Hex())
	req.Header.Set("X-LPC-Event-Type", event.Type)
	req.Header.Set("X-LPC-Delivery-Id", deliveryID)
	req.Header.Set("X-LPC-Attempt", strconv.Itoa(hook.ConsecutiveFailures+1))

	start := time.Now()
	resp, err := webhookClient().Do(req)
	duration := time.Since(start)
	if err != nil {
		return 0, duration, err
	}
	defer resp.Body.Close()
	// The body is read and discarded rather than kept. A receiver's error page
	// is their content, and storing it would make our delivery log a place to
	// find whatever they happened to print.
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, duration, fmt.Errorf("endpoint answered %d", resp.StatusCode)
	}
	return resp.StatusCode, duration, nil
}

// webhookClient is the HTTP client deliveries go out on.
//
// Redirects are refused outright, which is the other half of the SSRF gate: a
// public URL that 302s to the metadata address is the whole trick, and
// re-checking the target of every hop is more fragile than declining to follow
// one. A receiver that needs a different URL can say so in the console.
func webhookClient() *http.Client {
	return &http.Client{
		Timeout: webhookDeliveryTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errWebhookRedirect
		},
	}
}

var errWebhookRedirect = errors.New("webhook endpoints must not redirect")

// signWebhookBody mirrors handlers.SignWebhookPayload.
//
// Duplicated rather than imported because api/handlers imports nothing from
// here and a scheduler importing handlers would make a cycle out of a six-line
// function. The signature format is documented, and there is a test that the
// two agree byte for byte.
func signWebhookBody(secret string, at time.Time, body []byte) string {
	timestamp := strconv.FormatInt(at.UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "t=" + timestamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// webhookSigningSecret is the secret a delivery is signed with.
//
// We store a keyed hash of the secret rather than the secret, so the signature
// is computed over the hash. That is sound — it is a 256-bit value the receiver
// also holds, because the console told it the plaintext and the receiver
// derives the same hash — but it is the one place the scheme needs stating, and
// it is stated in the docs.
func (s *Scheduler) webhookSigningSecret(_ context.Context, hook models.DeveloperWebhook) (string, error) {
	if strings.TrimSpace(hook.SecretHash) == "" {
		return "", errors.New("this webhook has no signing secret")
	}
	return hook.SecretHash, nil
}

// assertPublicWebhookURL re-runs the address checks before a request.
func assertPublicWebhookURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") {
		return errors.New("a webhook URL must be https")
	}
	host := parsed.Hostname()
	if host == "" {
		return errors.New("that URL has no host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !publicIP(ip) {
			return errors.New("that host is not on the public internet")
		}
		return nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil || len(addrs) == 0 {
		return errors.New("that host does not resolve")
	}
	for _, ip := range addrs {
		if !publicIP(ip) {
			return errors.New("that host resolves off the public internet")
		}
	}
	return nil
}

// publicIP mirrors handlers.isPublicIP, for the same reason signWebhookBody is
// duplicated. A test asserts the two agree on the ranges that matter.
func publicIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() ||
		ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, blocked := range []string{"169.254.169.254", "fd00:ec2::254"} {
		if ip.Equal(net.ParseIP(blocked)) {
			return false
		}
	}
	for _, cidr := range []string{"100.64.0.0/10", "198.18.0.0/15", "192.0.0.0/24", "::/128"} {
		if _, network, err := net.ParseCIDR(cidr); err == nil && network.Contains(ip) {
			return false
		}
	}
	return true
}

// ---- bookkeeping ----

func (s *Scheduler) advanceWebhookCursor(ctx context.Context, webhookID primitive.ObjectID, cursor string) error {
	_, err := s.WebhookDB.UpdateOne(ctx, bson.M{"_id": bson.M{"$eq": webhookID}},
		bson.M{"$set": bson.M{"cursor": cursor, "updatedAt": primitive.NewDateTimeFromTime(time.Now())}})
	return err
}

func (s *Scheduler) clearWebhookFailures(ctx context.Context, webhookID primitive.ObjectID) error {
	now := primitive.NewDateTimeFromTime(time.Now())
	_, err := s.WebhookDB.UpdateOne(ctx, bson.M{"_id": bson.M{"$eq": webhookID}}, bson.M{"$set": bson.M{
		"consecutiveFailures": 0,
		"lastError":           "",
		"lastDeliveryAt":      now,
		"updatedAt":           now,
	}})
	return err
}

// noteWebhookFailure records a failure and switches an endpoint off when it has
// stopped answering entirely.
//
// Switched off rather than retried forever. An endpoint that has refused
// twenty consecutive deliveries is not having a transient problem, and
// continuing to call it spends our delivery capacity on somebody else's
// outage. The console says why, and saving the endpoint turns it back on.
func (s *Scheduler) noteWebhookFailure(ctx context.Context, hook models.DeveloperWebhook, status int, cause error) error {
	failures := hook.ConsecutiveFailures + 1
	now := primitive.NewDateTimeFromTime(time.Now())

	set := bson.M{
		"consecutiveFailures": failures,
		"lastStatus":          status,
		// Our own description of what went wrong, bounded. Never the
		// receiver's response body.
		"lastError":      truncateWebhookError(cause.Error()),
		"lastDeliveryAt": now,
		"updatedAt":      now,
	}
	if failures >= models.WebhookDisableAfterConsecutiveFailures {
		set["status"] = models.WebhookStatusDisabled
		set["disabledReason"] = fmt.Sprintf(
			"Turned off after %d deliveries in a row failed. Fix the endpoint and save it again to switch it back on.",
			failures)
		zap.S().Warnw("developer webhook disabled after repeated failures",
			"webhookId", hook.ID.Hex(), "url", hook.URL, "failures", failures)
	}

	_, err := s.WebhookDB.UpdateOne(ctx, bson.M{"_id": bson.M{"$eq": hook.ID}}, bson.M{"$set": set})
	return err
}

func (s *Scheduler) recordDelivery(ctx context.Context, hook models.DeveloperWebhook, event models.DeveloperAPIEvent, status int, duration time.Duration, cause error) {
	if s.WebhookDeliveryDB == nil {
		return
	}
	now := time.Now().UTC()
	row := models.DeveloperWebhookDelivery{
		ID:         primitive.NewObjectID(),
		WebhookID:  hook.ID.Hex(),
		KeyID:      hook.KeyID,
		EventID:    event.ID.Hex(),
		EventType:  event.Type,
		Attempt:    hook.ConsecutiveFailures + 1,
		Status:     status,
		Succeeded:  cause == nil,
		DurationMS: int(duration.Milliseconds()),
		At:         primitive.NewDateTimeFromTime(now),
		ExpiresAt: primitive.NewDateTimeFromTime(
			now.Add(time.Duration(models.DeveloperWebhookDeliveryRetentionDays) * 24 * time.Hour)),
	}
	if cause != nil {
		row.Error = truncateWebhookError(cause.Error())
	}
	if _, err := s.WebhookDeliveryDB.InsertOne(ctx, row); err != nil {
		zap.S().Debugw("could not record a webhook delivery", "error", err)
	}
}

// keyIsLive reports whether a key may still act at all.
//
// Checked per run rather than cached, exactly as the request path checks it per
// request, so revocation takes effect on the next delivery attempt rather than
// whenever something happened to refresh. The two stores are nil-checked
// because the scheduler can be wired without them, and a missing store means
// "cannot prove it is live", which stops deliveries rather than allowing them.
func (s *Scheduler) keyIsLive(ctx context.Context, keyID string) (bool, error) {
	if s.KeyDB == nil {
		return false, errors.New("no key store configured")
	}
	oid, err := primitive.ObjectIDFromHex(keyID)
	if err != nil {
		return false, nil
	}
	key, err := s.KeyDB.FindOne(ctx, bson.M{"_id": bson.M{"$eq": oid}})
	if err != nil || key == nil {
		// A key that no longer exists delivers nothing.
		return false, nil
	}
	if key.Status != models.DeveloperKeyActive {
		return false, nil
	}
	if s.DevAccountDB == nil {
		return false, errors.New("no developer account store configured")
	}
	accountOID, err := primitive.ObjectIDFromHex(key.DeveloperID)
	if err != nil {
		return false, nil
	}
	account, err := s.DevAccountDB.FindOne(ctx, bson.M{"_id": bson.M{"$eq": accountOID}})
	if err != nil || account == nil {
		return false, nil
	}
	return account.Status == models.DeveloperAccountActive, nil
}

// grantedCommunities lists the communities a key may currently act in.
func (s *Scheduler) grantedCommunities(ctx context.Context, keyID string) ([]string, error) {
	if s.GrantDB == nil {
		return nil, errors.New("no grant store configured")
	}
	cur, err := s.GrantDB.Find(ctx, bson.M{
		"keyId":  bson.M{"$eq": keyID},
		"status": bson.M{"$eq": models.DeveloperKeyActive},
	})
	if err != nil {
		return nil, err
	}
	var grants []models.DeveloperCommunityGrant
	if err := cur.All(ctx, &grants); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(grants))
	for _, g := range grants {
		out = append(out, g.CommunityID)
	}
	return out, nil
}

func containsStr(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func truncateWebhookError(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

// SignWebhookBodyForTest and AssertPublicWebhookURLForTest expose this file's
// two duplicated helpers so an external test can check them against the
// handlers' copies. Named for what they are rather than hidden behind an
// export_test.go, because the duplication they guard is a deliberate design
// decision and a reader should be able to find the thing that holds it in
// place.
func SignWebhookBodyForTest(secret string, at time.Time, body []byte) string {
	return signWebhookBody(secret, at, body)
}

// AssertPublicWebhookURLForTest runs the worker's own address gate.
func AssertPublicWebhookURLForTest(raw string) error {
	return assertPublicWebhookURL(raw)
}
