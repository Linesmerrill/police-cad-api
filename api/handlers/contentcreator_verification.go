package handlers

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
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

// Channel ownership verification.
//
// Everything an applicant types about their channels — url, handle, follower
// count — is self-asserted, and someone applied with a channel they did not
// own. The applicant now places a short code in their own channel description;
// we read the public channel and look for it. Owning the channel is the only
// way to put text in its description, so finding the code proves ownership.
//
// Reading the channel also returns the real follower count, which replaces the
// number the applicant typed.

const (
	// Long enough to be worth pasting once, short enough to retype. Codes are
	// single-use per platform entry and are cleared once verified.
	channelCodeTTL = 24 * time.Hour
	channelCodeLen = 6
)

// channelCodeAlphabet omits characters that are easy to confuse when someone
// copies a code by eye out of a bio: 0/O, 1/I/L.
const channelCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func newChannelVerificationCode() (string, error) {
	b := make([]byte, channelCodeLen)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(channelCodeAlphabet))))
		if err != nil {
			return "", err
		}
		b[i] = channelCodeAlphabet[n.Int64()]
	}
	return "LPC-VERIFY-" + string(b), nil
}

// findMyPendingApplication loads the caller's application and the platform index
// they are acting on, rejecting anything already decided.
func (cc ContentCreator) findMyPendingApplication(ctx context.Context, r *http.Request) (*models.ContentCreatorApplication, int, error) {
	userIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		return nil, 0, fmt.Errorf("unauthorized")
	}
	userObjID, err := primitive.ObjectIDFromHex(userIDStr)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid user id")
	}

	idx, err := strconv.Atoi(mux.Vars(r)["index"])
	if err != nil || idx < 0 {
		return nil, 0, fmt.Errorf("invalid platform index")
	}

	app, err := cc.AppDB.FindOne(ctx, bson.M{
		"userId": userObjID,
		"status": bson.M{"$in": []string{"submitted", "under_review"}},
	})
	if err != nil || app == nil {
		return nil, 0, fmt.Errorf("no application awaiting review")
	}
	if idx >= len(app.Platforms) {
		return nil, 0, fmt.Errorf("platform index out of range")
	}
	return app, idx, nil
}

// StartChannelVerificationHandler issues a code for one platform entry.
func (cc ContentCreator) StartChannelVerificationHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	app, idx, err := cc.findMyPendingApplication(ctx, r)
	if err != nil {
		config.ErrorStatus(err.Error(), http.StatusBadRequest, w, nil)
		return
	}

	platform := app.Platforms[idx]
	if platform.IsVerified() {
		config.InfoStatus("platform already verified", http.StatusConflict, w, nil)
		return
	}

	code, err := newChannelVerificationCode()
	if err != nil {
		config.ErrorStatus("failed to generate code", http.StatusInternalServerError, w, err)
		return
	}
	expires := primitive.NewDateTimeFromTime(time.Now().Add(channelCodeTTL))
	now := primitive.NewDateTimeFromTime(time.Now())

	prefix := fmt.Sprintf("platforms.%d.", idx)
	update := bson.M{"$set": bson.M{
		prefix + "verificationCode":          code,
		prefix + "verificationCodeExpiresAt": expires,
		prefix + "verificationStatus":        models.PlatformPending,
		prefix + "verificationError":         "",
		"updatedAt":                          now,
	}}
	if err := cc.AppDB.UpdateOne(ctx, bson.M{"_id": app.ID}, update); err != nil {
		config.ErrorStatus("failed to store verification code", http.StatusInternalServerError, w, err)
		return
	}

	_, fetchErr := platforms.For(platform.Type)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"code":        code,
		"expiresAt":   expires.Time(),
		"platform":    platform.Type,
		"handle":      platform.Handle,
		"manualOnly":  fetchErr == platforms.ErrManualOnly,
		"instruction": channelInstruction(platform.Type),
	})
}

// formatCount renders a follower count the way a person would say it, since
// these strings are read by applicants rather than parsed.
func formatCount(n int) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// channelInstruction tells the applicant exactly where the code goes. Wrong
// field means a failed check and a confused applicant.
func channelInstruction(platformType string) string {
	switch strings.ToLower(platformType) {
	case "youtube":
		return "Add this code anywhere in your YouTube channel description (YouTube Studio, Customization, Profile tab, Description), save, then click Check. You can remove it once verified."
	case "twitch":
		return "Add this code anywhere in your Twitch About panel / bio (Settings, Channel, About), save, then click Check. You can remove it once verified."
	case "tiktok":
		return "Add this code anywhere in your TikTok bio and leave it there. TikTok has no public API, so a member of our team confirms it by eye during review."
	default:
		return "Add this code to your channel or profile bio and leave it there. A member of our team confirms it during review."
	}
}

// CheckChannelVerificationHandler reads the public channel and looks for the code.
func (cc ContentCreator) CheckChannelVerificationHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	app, idx, err := cc.findMyPendingApplication(ctx, r)
	if err != nil {
		config.ErrorStatus(err.Error(), http.StatusBadRequest, w, nil)
		return
	}

	platform := app.Platforms[idx]
	if platform.IsVerified() {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"verified": true, "alreadyVerified": true})
		return
	}
	if platform.VerificationCode == "" {
		config.ErrorStatus("no verification code issued yet", http.StatusBadRequest, w, nil)
		return
	}
	if platform.VerificationCodeExpiresAt != nil && time.Now().After(platform.VerificationCodeExpiresAt.Time()) {
		config.InfoStatus("verification code expired, request a new one", http.StatusGone, w, nil)
		return
	}

	// Budget check BEFORE any outbound call. Every Check press spends a unit of
	// a shared daily platform quota, so this is what stops one applicant
	// hammering the button and exhausting verification for everyone. It is keyed
	// to the application rather than the caller's IP, so rotating IPs does not
	// buy more budget and people behind one NAT do not share it.
	if ok, retryAfter, reason := platform.VerifyCheckBudget(time.Now()); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		config.InfoStatus(reason, http.StatusTooManyRequests, w, nil)
		return
	}

	// Spend one unit of the budget whether or not the fetch below succeeds:
	// a failed lookup still costs us a platform API call.
	countAfter, windowFrom := platform.NextVerifyCheckCounters(time.Now())
	nowDT := primitive.NewDateTimeFromTime(time.Now())
	windowDT := primitive.NewDateTimeFromTime(windowFrom)
	budgetPrefix := fmt.Sprintf("platforms.%d.", idx)
	_ = cc.AppDB.UpdateOne(ctx, bson.M{"_id": app.ID}, bson.M{"$set": bson.M{
		budgetPrefix + "verificationLastCheckedAt":   nowDT,
		budgetPrefix + "verificationCheckCount":      countAfter,
		budgetPrefix + "verificationCheckWindowFrom": windowDT,
	}})

	fetcher, err := platforms.For(platform.Type)
	if err == platforms.ErrManualOnly {
		// Not a failure: the code stays pending until a human confirms it.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"verified": false, "manualOnly": true,
			"message": "This platform is confirmed by our team during review. Leave the code in your bio.",
		})
		return
	}

	info, fetchErr := fetcher.Fetch(ctx, platform.Handle)
	prefix := fmt.Sprintf("platforms.%d.", idx)
	now := primitive.NewDateTimeFromTime(time.Now())

	// The response reports the two questions separately -- did we find the
	// channel, and did we find the code on it -- rather than collapsing both
	// into one "could not verify". A single message left the applicant unable to
	// tell a wrong handle from an unsaved description, and that ambiguity is
	// what turns into a support ping.
	if fetchErr != nil {
		if fetchErr == platforms.ErrChannelNotFound {
			_ = cc.AppDB.UpdateOne(ctx, bson.M{"_id": app.ID}, bson.M{"$set": bson.M{
				prefix + "verificationStatus": models.PlatformFailed,
				prefix + "verificationError":  "channel not found",
				"updatedAt":                   now,
			}})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"verified":       false,
				"channelFound":   false,
				"codeFound":      false,
				"channelMessage": fmt.Sprintf("We could not find a %s channel at \"%s\". Open the link above — if it does not go to your channel, the handle on your application is wrong.", platform.Type, platform.Handle),
				"codeMessage":    "We could not check for your code, because we could not reach the channel.",
			})
			return
		}

		// Anything else is our side: no API key, exhausted quota, network. Never
		// recorded against the applicant, and phrased so they do not go hunting
		// for a mistake they did not make.
		if strings.Contains(fetchErr.Error(), platforms.ErrNotConfigured.Error()) {
			zap.S().Errorw("channel verification unavailable: platform not configured",
				"platform", platform.Type, "error", fetchErr)
		}
		config.InfoStatus("Verification is temporarily unavailable on our side. Nothing is wrong with your channel — we keep checking automatically, so you can leave the code in place.",
			http.StatusBadGateway, w, nil)
		return
	}

	if !platforms.ContainsCode(info.Description, platform.VerificationCode) {
		_ = cc.AppDB.UpdateOne(ctx, bson.M{"_id": app.ID}, bson.M{"$set": bson.M{
			prefix + "verificationStatus": models.PlatformPending,
			prefix + "verificationError":  "code not found in channel description",
			"updatedAt":                   now,
		}})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"verified":       false,
			"channelFound":   true,
			"codeFound":      false,
			"channelMessage": fmt.Sprintf("Found your channel — %s followers.", formatCount(info.FollowerCount)),
			"codeMessage":    "The code is not in that channel's description yet. Make sure you saved the change, then try again — it can take a moment to show up.",
		})
		return
	}

	// Verified. Keep what they claimed for comparison and replace the live count
	// with the real one, so the follower requirement means something.
	set := bson.M{
		prefix + "verificationStatus": models.PlatformVerified,
		prefix + "verificationMethod": "api",
		prefix + "verifiedAt":         now,
		prefix + "verificationCode":   "",
		prefix + "verificationError":  "",
		"updatedAt":                   now,
	}
	if platform.ReportedFollowerCount == 0 {
		set[prefix+"reportedFollowerCount"] = platform.FollowerCount
	}
	set[prefix+"followerCount"] = info.FollowerCount
	if info.ProfileURL != "" {
		set[prefix+"url"] = info.ProfileURL
	}
	if err := cc.AppDB.UpdateOne(ctx, bson.M{"_id": app.ID}, bson.M{"$set": set}); err != nil {
		config.ErrorStatus("failed to record verification", http.StatusInternalServerError, w, err)
		return
	}

	zap.S().Infow("channel ownership verified",
		"applicationId", app.ID.Hex(), "platform", platform.Type,
		"handle", platform.Handle, "claimed", platform.FollowerCount, "actual", info.FollowerCount)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"verified":       true,
		"channelFound":   true,
		"codeFound":      true,
		"channelMessage": fmt.Sprintf("Found your channel — %s followers.", formatCount(info.FollowerCount)),
		"codeMessage":    "Code found. This channel is verified — you can remove the code now.",
		"followerCount":  info.FollowerCount,
		"claimedCount":   platform.FollowerCount,
		"profileUrl":     info.ProfileURL,
	})
}

// AdminVerifyPlatformHandler lets an admin vouch for a platform that cannot be
// checked automatically (TikTok, "other"), recording who did it.
func (cc ContentCreator) AdminVerifyPlatformHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	adminIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	adminObjID, _ := primitive.ObjectIDFromHex(adminIDStr)

	appObjID, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		config.ErrorStatus("invalid application ID", http.StatusBadRequest, w, err)
		return
	}
	idx, err := strconv.Atoi(mux.Vars(r)["index"])
	if err != nil || idx < 0 {
		config.ErrorStatus("invalid platform index", http.StatusBadRequest, w, err)
		return
	}

	app, err := cc.AppDB.FindOne(ctx, bson.M{"_id": appObjID})
	if err != nil || app == nil {
		config.ErrorStatus("application not found", http.StatusNotFound, w, err)
		return
	}
	if idx >= len(app.Platforms) {
		config.ErrorStatus("platform index out of range", http.StatusBadRequest, w, nil)
		return
	}

	var req struct {
		FollowerCount int `json:"followerCount"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	now := primitive.NewDateTimeFromTime(time.Now())
	prefix := fmt.Sprintf("platforms.%d.", idx)
	set := bson.M{
		prefix + "verificationStatus": models.PlatformVerified,
		prefix + "verificationMethod": "admin",
		prefix + "verifiedAt":         now,
		prefix + "verifiedBy":         adminObjID,
		prefix + "verifiedByAdmin":    true,
		prefix + "verificationCode":   "",
		prefix + "verificationError":  "",
		"updatedAt":                   now,
	}
	// An admin who counted the followers themselves can correct the claim.
	if req.FollowerCount > 0 {
		if app.Platforms[idx].ReportedFollowerCount == 0 {
			set[prefix+"reportedFollowerCount"] = app.Platforms[idx].FollowerCount
		}
		set[prefix+"followerCount"] = req.FollowerCount
	}

	if err := cc.AppDB.UpdateOne(ctx, bson.M{"_id": appObjID}, bson.M{"$set": set}); err != nil {
		config.ErrorStatus("failed to record verification", http.StatusInternalServerError, w, err)
		return
	}

	zap.S().Infow("platform manually verified by admin",
		"applicationId", appObjID.Hex(), "platformIndex", idx, "adminId", adminIDStr)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "verified": true})
}

// unverifiedPlatforms lists the platform entries still lacking proof of
// ownership. Approval is refused while this is non-empty.
func unverifiedPlatforms(app *models.ContentCreatorApplication) []string {
	var pending []string
	for _, p := range app.Platforms {
		if p.IsVerified() {
			continue
		}
		label := p.Handle
		if label == "" {
			label = p.URL
		}
		pending = append(pending, fmt.Sprintf("%s (%s)", p.Type, label))
	}
	return pending
}
