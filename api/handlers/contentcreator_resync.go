package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
	"github.com/linesmerrill/police-cad-api/platforms"
)

// Platform maintenance for an APPROVED creator.
//
// Verification used to be something that happened once, to an application, and
// then never again: the platform entries are COPIED onto the creator document
// at approval and that copy is authoritative afterwards. So a creator who
// renamed their TikTok had a profile pointing at a handle that no longer
// existed, a follower count frozen at whatever it was on approval day, and no
// route back to the code flow that proved ownership in the first place.
//
// These handlers are the siblings of the application ones in
// contentcreator_verification.go and deliberately read like them: the same
// code, the same 24h TTL, the same per-platform check budget spent before any
// outbound call. Everything they write goes to contentCreators.platforms[N] —
// never to the application, which is history by this point.
//
// What they will never do: revoke a creator, or start a grace period. A count
// we read that lands below the minimum raises a review flag for an admin and
// says so to the creator. Acting on it stays with the sync handler and the
// scheduler, which is where the grace-period rules already live.

// creatorPlatformGate is the resolved target of one of these requests: the
// caller's own creator document and the platform index they are acting on.
type creatorPlatformGate struct {
	creator *models.ContentCreator
	index   int
}

// platform is the entry being acted on. Safe after findMyActiveCreator, which
// bounds-checks the index before returning.
func (g creatorPlatformGate) platform() models.ContentCreatorPlatform {
	return g.creator.Platforms[g.index]
}

// prefix is the dotted path to this platform entry, so every update writes
// single fields rather than the whole array. Replacing the array would race
// with anything else touching another platform on the same creator.
func (g creatorPlatformGate) prefix() string {
	return fmt.Sprintf("platforms.%d.", g.index)
}

// gateError carries the status its message should be answered with. The
// application-side gate flattens every failure to 400, which tells a caller
// nothing about whether they are not a creator, asked for a platform that does
// not exist, or sent a malformed index.
type gateError struct {
	status  int
	message string
}

func (e gateError) Error() string { return e.message }

func gateFail(status int, message string) error {
	return gateError{status: status, message: message}
}

// writeGateError answers a failed gate. Expected outcomes (not a creator, no
// such platform) go out at Info level: ErrorStatus attaches a stacktrace to
// anything it logs at Error, and a client looping on a 403 would flood the logs.
func writeGateError(w http.ResponseWriter, err error) {
	var ge gateError
	if errors.As(err, &ge) {
		config.InfoStatus(ge.message, ge.status, w, nil)
		return
	}
	config.InfoStatus(err.Error(), http.StatusBadRequest, w, nil)
}

// findMyActiveCreator loads the caller's creator document and the platform
// index they are acting on. The sibling of findMyPendingApplication, against
// the creator rather than the application.
//
// The creator is resolved by the caller's own user id, so "another creator's
// platform index" is not a case this has to detect: it reads as an index past
// the end of the caller's own platform list and is refused as not found.
func (cc ContentCreator) findMyActiveCreator(ctx context.Context, r *http.Request) (creatorPlatformGate, error) {
	userIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		return creatorPlatformGate{}, gateFail(http.StatusUnauthorized, "unauthorized")
	}
	userObjID, err := primitive.ObjectIDFromHex(userIDStr)
	if err != nil {
		return creatorPlatformGate{}, gateFail(http.StatusUnauthorized, "unauthorized")
	}

	idx, err := strconv.Atoi(mux.Vars(r)["index"])
	if err != nil || idx < 0 {
		return creatorPlatformGate{}, gateFail(http.StatusBadRequest, "invalid platform index")
	}

	creator, err := cc.CCDB.FindOne(ctx, bson.M{
		"userId": userObjID,
		"status": bson.M{"$in": []string{"active", "warned"}},
	})
	if err != nil || creator == nil {
		return creatorPlatformGate{}, gateFail(http.StatusForbidden,
			"you must be an approved creator to do this")
	}
	if idx >= len(creator.Platforms) {
		return creatorPlatformGate{}, gateFail(http.StatusNotFound, "platform not found")
	}
	return creatorPlatformGate{creator: creator, index: idx}, nil
}

// platformFetcher returns the fetcher resolver. Production uses platforms.For;
// tests substitute canned channel responses through the unexported field so
// these handlers can be exercised without reaching YouTube or TikTok.
func (cc ContentCreator) platformFetcher() channelFetcher {
	if cc.fetcherFor != nil {
		return cc.fetcherFor
	}
	return platforms.For
}

// platformLabel is the platform's name as a person writes it, for messages the
// creator reads. A bare type slug ("tiktok") in a sentence looks like a bug.
func platformLabel(platformType string) string {
	switch strings.ToLower(strings.TrimSpace(platformType)) {
	case "youtube":
		return "YouTube"
	case "twitch":
		return "Twitch"
	case "tiktok":
		return "TikTok"
	default:
		return "That platform"
	}
}

// platformBlockedMessage is what we say when the platform refused to serve US.
// Shared by the application check and the creator resync so the two can never
// disagree about what happened, and explicit that nothing changed: the previous
// wording ("temporarily unavailable") sent people looking for a mistake in
// their own bio that was never there.
func platformBlockedMessage(platformType string) string {
	return fmt.Sprintf(
		"%s would not let us read that channel just now. Nothing was changed, try again later.",
		platformLabel(platformType))
}

// platformManualOnlyMessage covers the platforms nothing reads. There is no
// number to resync, and saying so beats a spinner that never resolves.
func platformManualOnlyMessage(platformType string) string {
	return fmt.Sprintf(
		"We cannot read %s automatically, so there is nothing to sync. An admin can confirm this platform manually.",
		platformLabel(platformType))
}

// platformUnavailableMessage is for our own outages: no API key, an exhausted
// quota. Never recorded against the creator.
func platformUnavailableMessage(platformType string) string {
	return fmt.Sprintf(
		"Reading %s is temporarily unavailable on our side. Nothing was changed, and nothing is wrong with your channel.",
		platformLabel(platformType))
}

const platformNotFoundMessage = "We could not find that channel. Check the handle and try again."

// creatorFetchBudget is how long one platform read may take. The platform
// clients carry their own ~12s timeouts, which is longer than the whole
// api.QueryTimeout a handler gets for its database work, so a read on the
// handler's query context would be cut off mid-flight and, worse, would leave
// no context to write the result with. Fetching on its own budget and writing
// on a fresh query context keeps those two concerns apart.
const creatorFetchBudget = 8 * time.Second

// creatorSyncFetchBudget bounds a whole follower sync, which reads every
// platform a creator has. Five platforms at the per-read budget would outlast
// the server's write timeout, so the sync stops reading when this runs out and
// leaves the remaining platforms on their stored counts for the next pass.
const creatorSyncFetchBudget = 20 * time.Second

// followerSnapshotSource names where a snapshot's numbers came from. "manual"
// is a creator's own figure; "api" means at least one number in it was read by
// us, which is what makes a snapshot something the follower minimum can be
// judged on later.
func followerSnapshotSource(anyTrustedRead bool) string {
	if anyTrustedRead {
		return "api"
	}
	return "manual"
}

// resolveFetcher picks the fetcher for a platform, answering the request itself
// when there is nothing to read. Returns nil when it has already written.
//
// Deliberately before the budget spend: a platform we never call costs no
// platform quota, and the contract for these endpoints is that a manual-only
// platform changes nothing at all.
func (cc ContentCreator) resolveFetcher(w http.ResponseWriter, platformType string) platforms.Fetcher {
	fetcher, err := cc.platformFetcher()(platformType)
	switch {
	case errors.Is(err, platforms.ErrManualOnly):
		config.InfoStatus(platformManualOnlyMessage(platformType), http.StatusBadRequest, w, nil)
		return nil
	case errors.Is(err, platforms.ErrNotConfigured):
		config.InfoStatus(platformUnavailableMessage(platformType), http.StatusBadRequest, w, nil)
		return nil
	case err != nil || fetcher == nil:
		config.InfoStatus(platformManualOnlyMessage(platformType), http.StatusBadRequest, w, nil)
		return nil
	}
	return fetcher
}

// writeFetchFailure answers a failed read. Each sentinel gets its own status
// and its own honest sentence: whose problem this is differs, and collapsing
// them is what left creators troubleshooting an outage of ours.
//
// None of these branches write to the creator. A platform that would not answer
// is not evidence about the channel, so the stored count, the stored handle and
// any pending handle all stay exactly as they were.
func writeFetchFailure(w http.ResponseWriter, platformType string, err error) {
	switch {
	case errors.Is(err, platforms.ErrPlatformBlocked):
		config.InfoStatus(platformBlockedMessage(platformType), http.StatusServiceUnavailable, w, nil)
	case errors.Is(err, platforms.ErrChannelNotFound):
		config.InfoStatus(platformNotFoundMessage, http.StatusNotFound, w, nil)
	case errors.Is(err, platforms.ErrManualOnly):
		config.InfoStatus(platformManualOnlyMessage(platformType), http.StatusBadRequest, w, nil)
	case errors.Is(err, platforms.ErrNotConfigured):
		zap.S().Errorw("creator platform read unavailable: platform not configured",
			"platform", platformType, "error", err)
		config.InfoStatus(platformUnavailableMessage(platformType), http.StatusBadRequest, w, nil)
	default:
		zap.S().Warnw("creator platform read failed", "platform", platformType, "error", err)
		config.InfoStatus(platformUnavailableMessage(platformType), http.StatusBadGateway, w, nil)
	}
}

// spendCheckBudget charges one unit of the per-platform check budget before any
// outbound call, and answers 429 with Retry-After when it is exhausted.
//
// The same budget the application check spends, against the creator's platform
// entry: a cooldown a real person never notices plus a rolling daily cap, so one
// creator holding the button cannot spend a meaningful share of a platform
// quota that everybody shares. Keyed to the document rather than the caller's
// IP, which rotating IPs would defeat and a shared NAT would be punished by.
//
// Spent whether or not the fetch that follows succeeds: a failed lookup still
// costs us the call. Returns false when it has already written the response.
func (cc ContentCreator) spendCheckBudget(ctx context.Context, w http.ResponseWriter, g creatorPlatformGate) bool {
	p := g.platform()
	now := time.Now()
	if ok, retryAfter, reason := p.VerifyCheckBudget(now); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		config.InfoStatus(reason, http.StatusTooManyRequests, w, nil)
		return false
	}

	countAfter, windowFrom := p.NextVerifyCheckCounters(now)
	prefix := g.prefix()
	_ = cc.CCDB.UpdateOne(ctx, bson.M{"_id": g.creator.ID}, bson.M{"$set": bson.M{
		prefix + "verificationLastCheckedAt":   primitive.NewDateTimeFromTime(now),
		prefix + "verificationCheckCount":      countAfter,
		prefix + "verificationCheckWindowFrom": primitive.NewDateTimeFromTime(windowFrom),
	}})
	return true
}

// applyFollowerReviewFlag folds the below-minimum review flag into an update
// that is about to persist a count WE read, and returns what to tell the
// creator.
//
// Judged on the creator's HIGHEST platform count with the new number in place,
// which is how the minimum is judged everywhere else in the program (the sync
// handler and the scheduler both use the maximum). Judging the single platform
// in isolation would flag a creator whose YouTube has fifty thousand followers
// because a side TikTok reads eighty, and every one of those is manual triage
// for an admin over a creator who was never close to the line.
//
// Only ever called for a platform platforms.Measurable says we can rely on, and
// only after a successful read. A number nobody read is not evidence.
func applyFollowerReviewFlag(set bson.M, creator *models.ContentCreator, index, newCount int, now primitive.DateTime) (bool, string) {
	highest := newCount
	for i, p := range creator.Platforms {
		if i == index {
			continue
		}
		if p.FollowerCount > highest {
			highest = p.FollowerCount
		}
	}
	return setFollowerReviewFlag(set, creator, highest, now)
}

// setFollowerReviewFlag is the flag decision itself, given the creator's
// highest count after a trusted read. Shared with the follower sync, which
// reads every platform at once and so already knows the highest.
func setFollowerReviewFlag(set bson.M, creator *models.ContentCreator, highest int, now primitive.DateTime) (bool, string) {
	if highest >= models.MinFollowers {
		// A later trusted read at or above the bar clears the flag. Without
		// this the flag is a one-way door and the console fills with creators
		// who recovered weeks ago.
		if creator.FollowerReviewFlaggedAt != nil {
			set["followerReviewFlaggedAt"] = nil
			set["followerReviewReason"] = ""
		}
		return false, ""
	}

	// Keep the first flagged timestamp: how long this has been true is the
	// thing an admin is judging, so refreshing it on every resync would erase
	// exactly that.
	if creator.FollowerReviewFlaggedAt == nil {
		set["followerReviewFlaggedAt"] = now
	}
	set["followerReviewReason"] = fmt.Sprintf("read %d followers, below the %d minimum", highest, models.MinFollowers)

	return true, fmt.Sprintf(
		"We read %s, which is below the %d minimum. Nothing has changed on your account, and an admin will take a look.",
		followersLabel(highest), models.MinFollowers)
}

// writeFollowerSnapshot records the counts after a trusted read, in the shape
// the existing follower history already uses so the admin console and the
// scheduler see one series rather than two.
func (cc ContentCreator) writeFollowerSnapshot(ctx context.Context, creator *models.ContentCreator, platformsAfter []models.ContentCreatorPlatform, now primitive.DateTime) {
	if cc.SnapDB == nil {
		return
	}
	var total, max int
	for _, p := range platformsAfter {
		total += p.FollowerCount
		if p.FollowerCount > max {
			max = p.FollowerCount
		}
	}
	snapshot := models.ContentCreatorFollowerSnapshot{
		ID:               primitive.NewObjectID(),
		ContentCreatorID: creator.ID,
		Platforms:        platformsAfter,
		TotalFollowers:   total,
		MaxFollowers:     max,
		// "api" because we read these numbers ourselves. "manual" is reserved
		// for the counts a creator typed, which is what this feature replaces.
		Source:     "api",
		RecordedAt: now,
		RecordedBy: creator.UserID,
	}
	if _, err := cc.SnapDB.InsertOne(ctx, snapshot); err != nil {
		// History is useful, not load-bearing. The count itself is already
		// written by the time this runs.
		zap.S().Warnw("failed to record creator follower snapshot",
			"error", err, "creatorId", creator.ID.Hex())
	}
}

// platformsWith copies the creator's platform list with one follower count
// replaced, for the snapshot. The stored document is updated field by field;
// this is only ever a local copy.
func platformsWith(creator *models.ContentCreator, index, count int) []models.ContentCreatorPlatform {
	out := make([]models.ContentCreatorPlatform, len(creator.Platforms))
	copy(out, creator.Platforms)
	if index >= 0 && index < len(out) {
		out[index].FollowerCount = count
	}
	return out
}

// ResyncPlatformHandler re-reads one linked platform and stores the real count.
// POST /api/v1/content-creators/me/platforms/{index}/resync
func (cc ContentCreator) ResyncPlatformHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	g, err := cc.findMyActiveCreator(ctx, r)
	if err != nil {
		writeGateError(w, err)
		return
	}
	p := g.platform()

	fetcher := cc.resolveFetcher(w, p.Type)
	if fetcher == nil {
		return
	}
	if !cc.spendCheckBudget(ctx, w, g) {
		return
	}

	fetchCtx, cancelFetch := context.WithTimeout(r.Context(), creatorFetchBudget)
	info, fetchErr := fetcher.Fetch(fetchCtx, p.Handle)
	cancelFetch()
	if fetchErr != nil {
		writeFetchFailure(w, p.Type, fetchErr)
		return
	}

	// A fresh context for the writes: the read above may have spent most of the
	// one the gate was resolved on.
	writeCtx, cancelWrite := api.WithQueryTimeout(r.Context())
	defer cancelWrite()

	now := primitive.NewDateTimeFromTime(time.Now())
	prefix := g.prefix()
	set := bson.M{
		prefix + "followerCount": info.FollowerCount,
		prefix + "lastSyncedAt":  now,
		"lastSyncedAt":           now,
		"updatedAt":              now,
	}

	// Ownership is not re-proved here, so no verification field is touched. A
	// resync answers "how many followers", never "is this channel yours".
	belowMinimum := false
	message := fmt.Sprintf("Your %s follower count is now %s.",
		platformLabel(p.Type), followersLabel(info.FollowerCount))
	if platforms.Measurable(p.Type) {
		if below, warning := applyFollowerReviewFlag(set, g.creator, g.index, info.FollowerCount, now); below {
			belowMinimum = true
			message = warning
		}
	}

	if err := cc.CCDB.UpdateOne(writeCtx, bson.M{"_id": g.creator.ID}, bson.M{"$set": set}); err != nil {
		config.ErrorStatus("failed to store follower count", http.StatusInternalServerError, w, err)
		return
	}
	cc.writeFollowerSnapshot(writeCtx, g.creator, platformsWith(g.creator, g.index, info.FollowerCount), now)

	zap.S().Infow("creator platform resynced",
		"creatorId", g.creator.ID.Hex(), "platform", p.Type, "handle", p.Handle,
		"previous", p.FollowerCount, "current", info.FollowerCount, "belowMinimum", belowMinimum)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"platform": map[string]interface{}{
			"type":          p.Type,
			"handle":        p.Handle,
			"followerCount": info.FollowerCount,
			"lastSyncedAt":  now.Time(),
		},
		"previousFollowerCount": p.FollowerCount,
		"belowMinimum":          belowMinimum,
		"minFollowers":          models.MinFollowers,
		"message":               message,
	})
}

// ReverifyStartHandler issues a code for a handle the creator wants to move to.
// POST /api/v1/content-creators/me/platforms/{index}/reverify-start
//
// The live handle is left alone. A creator who renamed their channel still has
// a working profile while they prove the new one, and a creator who starts this
// by mistake has lost nothing.
func (cc ContentCreator) ReverifyStartHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	g, err := cc.findMyActiveCreator(ctx, r)
	if err != nil {
		writeGateError(w, err)
		return
	}
	p := g.platform()

	var req struct {
		Handle string `json:"handle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.InfoStatus("invalid request body", http.StatusBadRequest, w, nil)
		return
	}

	handle := platforms.NormalizeHandle(p.Type, req.Handle)
	if handle == "" {
		config.InfoStatus("enter the new handle for this platform", http.StatusBadRequest, w, nil)
		return
	}
	// Re-proving a handle that is already proved is the one case worth
	// refusing. An UNVERIFIED entry may run this against its own handle: that
	// is how a platform added on the profile page becomes verified at all, and
	// how a failed verification is retried.
	if p.IsVerified() && strings.EqualFold(handle, p.Handle) {
		config.InfoStatus("that is already the verified handle on this platform", http.StatusBadRequest, w, nil)
		return
	}

	code, err := newChannelVerificationCode()
	if err != nil {
		config.ErrorStatus("failed to generate code", http.StatusInternalServerError, w, err)
		return
	}
	expires := primitive.NewDateTimeFromTime(time.Now().Add(channelCodeTTL))
	now := primitive.NewDateTimeFromTime(time.Now())

	prefix := g.prefix()
	set := bson.M{
		prefix + "pendingHandle":             handle,
		prefix + "pendingUrl":                submittedProfileURL(req.Handle),
		prefix + "verificationCode":          code,
		prefix + "verificationCodeExpiresAt": expires,
		prefix + "verificationError":         "",
		"updatedAt":                          now,
	}
	// verificationStatus is NOT moved to pending: the live handle keeps whatever
	// standing it already had until the new one is proved. Flipping it here
	// would un-verify a working channel for the length of the attempt, and an
	// abandoned attempt would leave it that way.
	if err := cc.CCDB.UpdateOne(ctx, bson.M{"_id": g.creator.ID}, bson.M{"$set": set}); err != nil {
		config.ErrorStatus("failed to store verification code", http.StatusInternalServerError, w, err)
		return
	}

	_, fetchErr := cc.platformFetcher()(p.Type)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"code":          code,
		"expiresAt":     expires.Time(),
		"platform":      p.Type,
		"currentHandle": p.Handle,
		"pendingHandle": handle,
		"manualOnly":    errors.Is(fetchErr, platforms.ErrManualOnly),
		"instruction":   channelInstruction(p.Type),
		"message": fmt.Sprintf(
			"Add the code to the bio of %s, then press Check. Your current handle keeps working until we find it.",
			handle),
	})
}

// submittedProfileURL keeps a link the creator pasted, so an admin confirming a
// blocked platform by hand has the thing they typed rather than a handle to
// reconstruct it from. A bare handle yields no URL: guessing a profile URL per
// platform is how you end up storing links that 404.
func submittedProfileURL(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if strings.Contains(v, "://") {
		return v
	}
	// "tiktok.com/@name" without a scheme is a link too: a first segment with a
	// dot in it is a hostname, and a handle never has one.
	if i := strings.IndexByte(v, '/'); i > 0 && strings.Contains(v[:i], ".") {
		return "https://" + v
	}
	return ""
}

// ReverifyCheckHandler looks for the code on the PENDING handle and, if it is
// there, promotes that handle to live.
// POST /api/v1/content-creators/me/platforms/{index}/reverify-check
func (cc ContentCreator) ReverifyCheckHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	g, err := cc.findMyActiveCreator(ctx, r)
	if err != nil {
		writeGateError(w, err)
		return
	}
	p := g.platform()

	if p.PendingHandle == "" {
		config.InfoStatus("no handle change is waiting on a code", http.StatusBadRequest, w, nil)
		return
	}
	if p.VerificationCode == "" {
		config.InfoStatus("no verification code issued yet", http.StatusBadRequest, w, nil)
		return
	}
	if p.VerificationCodeExpiresAt != nil && time.Now().After(p.VerificationCodeExpiresAt.Time()) {
		config.InfoStatus("verification code expired, request a new one", http.StatusGone, w, nil)
		return
	}

	fetcher := cc.resolveFetcher(w, p.Type)
	if fetcher == nil {
		return
	}
	if !cc.spendCheckBudget(ctx, w, g) {
		return
	}

	// The PENDING handle, not the live one. The whole point is to read the
	// channel the creator says is theirs now.
	fetchCtx, cancelFetch := context.WithTimeout(r.Context(), creatorFetchBudget)
	info, fetchErr := fetcher.Fetch(fetchCtx, p.PendingHandle)
	cancelFetch()
	if fetchErr != nil {
		// Nothing is promoted and nothing is lost: the pending handle stays
		// pending, with its code, so the creator can press Check again.
		writeFetchFailure(w, p.Type, fetchErr)
		return
	}

	writeCtx, cancelWrite := api.WithQueryTimeout(r.Context())
	defer cancelWrite()

	now := primitive.NewDateTimeFromTime(time.Now())
	if !platforms.ContainsCode(info.Description, p.VerificationCode) {
		// 200, matching the application check: the request worked, the answer
		// is "not yet". The live handle is untouched and the pending one is
		// still waiting, which is what the copy has to make obvious.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":       true,
			"verified":      false,
			"channelFound":  true,
			"codeFound":     false,
			"currentHandle": p.Handle,
			"pendingHandle": p.PendingHandle,
			"message": "We reached that channel but could not find the code in its bio. Make sure you saved the change, then try again. " +
				"Your current handle keeps working in the meantime.",
		})
		return
	}

	// One update promotes everything together. A partial promotion is the
	// failure that matters here: a handle moved without its count, or a count
	// moved without its verification, is a profile nobody can reason about.
	prefix := g.prefix()
	set := bson.M{
		prefix + "handle":                    p.PendingHandle,
		prefix + "followerCount":             info.FollowerCount,
		prefix + "verificationStatus":        models.PlatformVerified,
		prefix + "verificationMethod":        "api",
		prefix + "verifiedAt":                now,
		prefix + "lastSyncedAt":              now,
		prefix + "verificationCode":          "",
		prefix + "verificationCodeExpiresAt": nil,
		prefix + "pendingHandle":             "",
		prefix + "pendingUrl":                "",
		prefix + "verificationError":         "",
		// An admin's vouch was for the handle that has just been replaced, so
		// it does not carry over to the new one. We proved this one ourselves.
		prefix + "verifiedByAdmin": false,
		prefix + "verifiedBy":      nil,
		"lastSyncedAt":             now,
		"updatedAt":                now,
	}
	url := info.ProfileURL
	if url == "" {
		url = p.PendingURL
	}
	if url != "" {
		set[prefix+"url"] = url
	}

	belowMinimum := false
	message := fmt.Sprintf("Code found. %s is now verified as yours, with %s.",
		p.PendingHandle, followersLabel(info.FollowerCount))
	if platforms.Measurable(p.Type) {
		if below, warning := applyFollowerReviewFlag(set, g.creator, g.index, info.FollowerCount, now); below {
			belowMinimum = true
			message = fmt.Sprintf("Code found. %s is now verified as yours. %s", p.PendingHandle, warning)
		}
	}

	if err := cc.CCDB.UpdateOne(writeCtx, bson.M{"_id": g.creator.ID}, bson.M{"$set": set}); err != nil {
		config.ErrorStatus("failed to record verification", http.StatusInternalServerError, w, err)
		return
	}
	cc.writeFollowerSnapshot(writeCtx, g.creator, platformsWith(g.creator, g.index, info.FollowerCount), now)
	cc.auditHandleChange(r, g.creator, p, p.PendingHandle, info.FollowerCount, "api")

	zap.S().Infow("creator platform handle re-verified",
		"creatorId", g.creator.ID.Hex(), "platform", p.Type,
		"from", p.Handle, "to", p.PendingHandle, "followers", info.FollowerCount)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"verified":       true,
		"channelFound":   true,
		"codeFound":      true,
		"previousHandle": p.Handle,
		"platform": map[string]interface{}{
			"type":          p.Type,
			"handle":        p.PendingHandle,
			"followerCount": info.FollowerCount,
			"lastSyncedAt":  now.Time(),
			"url":           url,
		},
		"belowMinimum": belowMinimum,
		"minFollowers": models.MinFollowers,
		"message":      message,
	})
}

// AdminVerifyCreatorPlatformHandler is the manual fallback for a platform that
// will not let us read it: an admin who has confirmed the channel by eye
// promotes the pending handle themselves.
// POST /api/v1/admin/content-creators/{id}/platforms/{index}/verify
//
// Authorized the same way the sibling admin content-creator endpoints are: the
// website's admin routes are the only caller and they gate on admin status
// before proxying.
func (cc ContentCreator) AdminVerifyCreatorPlatformHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	adminIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	adminObjID, err := primitive.ObjectIDFromHex(adminIDStr)
	if err != nil {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, err)
		return
	}

	creatorObjID, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		config.ErrorStatus("invalid creator ID", http.StatusBadRequest, w, err)
		return
	}
	idx, err := strconv.Atoi(mux.Vars(r)["index"])
	if err != nil || idx < 0 {
		config.ErrorStatus("invalid platform index", http.StatusBadRequest, w, err)
		return
	}

	creator, err := cc.CCDB.FindOne(ctx, bson.M{"_id": creatorObjID})
	if err != nil || creator == nil {
		config.InfoStatus("creator not found", http.StatusNotFound, w, nil)
		return
	}
	if idx >= len(creator.Platforms) {
		config.InfoStatus("platform index out of range", http.StatusBadRequest, w, nil)
		return
	}
	p := creator.Platforms[idx]

	var req struct {
		FollowerCount int `json:"followerCount"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	now := primitive.NewDateTimeFromTime(time.Now())
	prefix := fmt.Sprintf("platforms.%d.", idx)
	set := bson.M{
		prefix + "verificationStatus":        models.PlatformVerified,
		prefix + "verificationMethod":        "admin",
		prefix + "verifiedAt":                now,
		prefix + "verifiedBy":                adminObjID,
		prefix + "verifiedByAdmin":           true,
		prefix + "verificationCode":          "",
		prefix + "verificationCodeExpiresAt": nil,
		prefix + "verificationError":         "",
		"updatedAt":                          now,
	}

	// Promote the pending handle if one is waiting. An admin confirming a
	// blocked platform is confirming the handle the creator asked to move to.
	newHandle := p.Handle
	if p.PendingHandle != "" {
		newHandle = p.PendingHandle
		set[prefix+"handle"] = p.PendingHandle
		set[prefix+"pendingHandle"] = ""
		set[prefix+"pendingUrl"] = ""
		if p.PendingURL != "" {
			set[prefix+"url"] = p.PendingURL
		}
	}

	// An admin who counted the followers themselves can correct the number.
	// Never a trusted read, so it does not touch the review flag.
	newCount := p.FollowerCount
	if req.FollowerCount > 0 {
		newCount = req.FollowerCount
		set[prefix+"followerCount"] = req.FollowerCount
	}

	if err := cc.CCDB.UpdateOne(ctx, bson.M{"_id": creatorObjID}, bson.M{"$set": set}); err != nil {
		config.ErrorStatus("failed to record verification", http.StatusInternalServerError, w, err)
		return
	}
	cc.auditHandleChange(r, creator, p, newHandle, newCount, "admin")

	zap.S().Infow("creator platform manually verified by admin",
		"creatorId", creatorObjID.Hex(), "platformIndex", idx,
		"adminId", adminIDStr, "handle", newHandle)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"verified": true,
		"handle":   newHandle,
	})
}

// auditHandleChange records a handle promotion through the same admin_audit
// trail the rest of the content creator handlers use. A verified platform
// changing hands is exactly the kind of event that has to be answerable later,
// whether a creator drove it with a code or an admin vouched for it.
func (cc ContentCreator) auditHandleChange(
	r *http.Request,
	creator *models.ContentCreator,
	before models.ContentCreatorPlatform,
	newHandle string,
	newCount int,
	method string,
) {
	if cc.AuditDB == nil {
		return
	}
	actorID, _ := getUserIDFromRequest(r)
	audit := models.AdminAudit{
		AdminID: actorID,
		Action:  "content_creator_platform_handle_verified",
		Before: map[string]interface{}{
			"platform":           before.Type,
			"handle":             before.Handle,
			"followerCount":      before.FollowerCount,
			"verificationStatus": before.VerificationStatus,
		},
		After: map[string]interface{}{
			"platform":           before.Type,
			"handle":             newHandle,
			"followerCount":      newCount,
			"verificationStatus": models.PlatformVerified,
			"verificationMethod": method,
		},
		Details:   "creator " + creator.ID.Hex() + " verified " + before.Type + " handle " + newHandle,
		Timestamp: time.Now(),
		IP:        getClientIP(r),
	}
	if _, err := cc.AuditDB.InsertOne(r.Context(), audit); err != nil {
		zap.S().Errorw("failed to write creator platform verification audit log", "error", err)
	}
}
