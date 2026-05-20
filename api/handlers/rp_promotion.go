package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
)

// RP server promotion endpoints.
//
//	GET  /api/v2/community/{communityId}/rp-promotion  — posting status + last post
//	POST /api/v2/community/{communityId}/rp-promotion  — submit a new promotion
//
// Both are owner/administrator-only. POST enforces a once-per-cooldown gate
// (RP_PROMOTION_COOLDOWN_HOURS, default 24) so a community can't flood the
// shared Discord rp-servers channel.

const (
	rpPromoWebhookEnv         = "DISCORD_RP_SERVERS_WEBHOOK_URL"
	rpPromoCooldownEnv        = "RP_PROMOTION_COOLDOWN_HOURS"
	rpPromoDefaultCooldownHrs = 24

	// Field caps. Free communities get a deliberately smaller allowance so
	// boosting is a visible upgrade; both tiers stay well inside Discord's
	// hard embed limits.
	rpPromoMaxServerName      = 100
	rpPromoMaxDescFree        = 600
	rpPromoMaxDescBoosted     = 3500
	rpPromoMaxFeaturesFree    = 6
	rpPromoMaxFeaturesBoosted = 15
	rpPromoMaxDepartments     = 12
	rpPromoMaxItemLen         = 120
	rpPromoMaxImagesFree      = 1
	rpPromoMaxImagesBoosted   = 3
)

// rpPromotionCooldown returns the configured posting cooldown. Falls back to
// the 24h default when the env var is missing or unparseable.
func rpPromotionCooldown() time.Duration {
	hrs := rpPromoDefaultCooldownHrs
	if v := os.Getenv(rpPromoCooldownEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			hrs = n
		}
	}
	return time.Duration(hrs) * time.Hour
}

// communityIsBoosted reports whether a community currently has an active
// (non-free) subscription, which unlocks the richer promotion styling.
func communityIsBoosted(c *models.Community) bool {
	return c.Details.Subscription.Active && c.Details.Subscription.Plan != "" && c.Details.Subscription.Plan != "basic"
}

// GetRpPromotionHandler returns the community's last promotion post and whether
// a new post can be made yet. Owner/administrator only.
func (c Community) GetRpPromotionHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	communityObjID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid communityId", http.StatusBadRequest, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": communityObjID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}
	if !userHasCommunityPermission(community, actorID) {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, fmt.Errorf("user is not an owner or administrator"))
		return
	}

	cooldown := rpPromotionCooldown()
	resp := map[string]interface{}{
		"boosted":       communityIsBoosted(community),
		"canPostNow":    true,
		"cooldownHours": int(cooldown.Hours()),
		"configured":    os.Getenv(rpPromoWebhookEnv) != "",
	}
	if rp := community.Details.RpPromotion; rp != nil {
		resp["lastData"] = rp.LastData
		resp["lastPostedBy"] = rp.LastPostedBy
		if rp.LastPostedAt != nil {
			postedAt := rp.LastPostedAt.Time()
			resp["lastPostedAt"] = postedAt.UTC().Format(time.RFC3339)
			nextAt := postedAt.Add(cooldown)
			if time.Now().Before(nextAt) {
				resp["canPostNow"] = false
				resp["nextAvailableAt"] = nextAt.UTC().Format(time.RFC3339)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// PostRpPromotionHandler validates and posts a community's RP server promotion
// to the shared Discord channel, enforcing the per-community cooldown.
func (c Community) PostRpPromotionHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	communityObjID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid communityId", http.StatusBadRequest, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	var data models.RpPromotionData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}

	webhookURL := os.Getenv(rpPromoWebhookEnv)
	if webhookURL == "" {
		config.ErrorStatus("promotion posting is not configured", http.StatusServiceUnavailable, w,
			fmt.Errorf("%s not set", rpPromoWebhookEnv))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": communityObjID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}
	if !userHasCommunityPermission(community, actorID) {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, fmt.Errorf("user is not an owner or administrator"))
		return
	}

	boosted := communityIsBoosted(community)

	// Cooldown gate — one promotion per community per cooldown window.
	cooldown := rpPromotionCooldown()
	if rp := community.Details.RpPromotion; rp != nil && rp.LastPostedAt != nil {
		nextAt := rp.LastPostedAt.Time().Add(cooldown)
		if time.Now().Before(nextAt) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":           "this community already promoted recently",
				"nextAvailableAt": nextAt.UTC().Format(time.RFC3339),
				"cooldownHours":   int(cooldown.Hours()),
			})
			return
		}
	}

	// Validate + normalize the submitted content (also applies tier limits).
	if vErr := sanitizeRpPromotionData(&data, boosted); vErr != nil {
		config.ErrorStatus(vErr.Error(), http.StatusBadRequest, w, vErr)
		return
	}

	// Post to Discord. This is synchronous so we can surface a failure to the
	// user and capture the message ID; nothing is persisted if it fails.
	messageID, err := sendRpPromotionWebhook(webhookURL, data, boosted)
	if err != nil {
		zap.S().Errorw("rp promotion: discord post failed", "community_id", communityID, "error", err)
		config.ErrorStatus("failed to post promotion to Discord", http.StatusBadGateway, w, err)
		return
	}

	now := time.Now()
	nowDT := primitive.NewDateTimeFromTime(now)
	rp := models.RpPromotion{
		LastPostedAt:  &nowDT,
		LastMessageID: messageID,
		LastPostedBy:  actorID,
		LastData:      &data,
	}
	if err := c.DB.UpdateOne(ctx, bson.M{"_id": communityObjID},
		bson.M{"$set": bson.M{"community.rpPromotion": rp}}); err != nil {
		// The Discord post already succeeded — log loudly but still report
		// success so the user isn't told it failed when it didn't. The next
		// cooldown check just won't see this post; acceptable degradation.
		zap.S().Errorw("rp promotion: posted to discord but failed to persist", "community_id", communityID, "error", err)
	}

	logAudit(c.ALDB, communityObjID, "rp_promotion.posted", "community", actorID, resolveActorName(c.UDB, actorID), "", "",
		map[string]interface{}{"serverName": data.ServerName, "boosted": boosted})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":         "promotion posted",
		"postedAt":        now.UTC().Format(time.RFC3339),
		"nextAvailableAt": now.Add(cooldown).UTC().Format(time.RFC3339),
		"messageId":       messageID,
	})
}

// sanitizeRpPromotionData validates required fields, trims/caps every field to
// the community's tier allowance, and strips boost-only fields for free
// communities. Mutates data in place. Returns a user-facing error on failure.
func sanitizeRpPromotionData(data *models.RpPromotionData, boosted bool) error {
	data.ServerName = strings.TrimSpace(data.ServerName)
	data.Game = strings.TrimSpace(data.Game)
	data.Description = strings.TrimSpace(data.Description)
	data.Requirements = strings.TrimSpace(data.Requirements)
	data.InviteURL = strings.TrimSpace(data.InviteURL)

	if data.ServerName == "" {
		return fmt.Errorf("server name is required")
	}
	if len([]rune(data.ServerName)) > rpPromoMaxServerName {
		return fmt.Errorf("server name must be %d characters or fewer", rpPromoMaxServerName)
	}
	if data.Game == "" {
		return fmt.Errorf("game is required")
	}
	if data.Description == "" {
		return fmt.Errorf("description is required")
	}
	descCap := rpPromoMaxDescFree
	if boosted {
		descCap = rpPromoMaxDescBoosted
	}
	if len([]rune(data.Description)) > descCap {
		return fmt.Errorf("description must be %d characters or fewer (boost your community for more room)", descCap)
	}

	// Invite URL must be an https Discord link.
	lowerURL := strings.ToLower(data.InviteURL)
	if !strings.HasPrefix(lowerURL, "https://") || !strings.Contains(lowerURL, "discord") {
		return fmt.Errorf("a valid https Discord invite link is required")
	}

	data.Consoles = cleanStringSlice(data.Consoles, 8)
	if len(data.Consoles) == 0 {
		return fmt.Errorf("select at least one platform")
	}

	data.Departments = cleanStringSlice(data.Departments, rpPromoMaxDepartments)

	featureCap := rpPromoMaxFeaturesFree
	if boosted {
		featureCap = rpPromoMaxFeaturesBoosted
	}
	if len(cleanStringSlice(data.Features, 1000)) > featureCap {
		return fmt.Errorf("free communities can list up to %d features — boost to add more", featureCap)
	}
	data.Features = cleanStringSlice(data.Features, featureCap)

	if len([]rune(data.Requirements)) > rpPromoMaxItemLen*4 {
		data.Requirements = string([]rune(data.Requirements)[:rpPromoMaxItemLen*4])
	}

	// Image / banner tier rules.
	data.Images = cleanStringSlice(data.Images, rpPromoMaxImagesBoosted)
	for _, img := range data.Images {
		if !strings.HasPrefix(strings.ToLower(img), "https://") {
			return fmt.Errorf("image URLs must be https")
		}
	}
	if boosted {
		if len(data.Images) > rpPromoMaxImagesBoosted {
			data.Images = data.Images[:rpPromoMaxImagesBoosted]
		}
		data.BannerImage = strings.TrimSpace(data.BannerImage)
		if data.BannerImage != "" && !strings.HasPrefix(strings.ToLower(data.BannerImage), "https://") {
			return fmt.Errorf("banner image URL must be https")
		}
	} else {
		// Free tier: no banner, single image.
		data.BannerImage = ""
		if len(data.Images) > rpPromoMaxImagesFree {
			data.Images = data.Images[:rpPromoMaxImagesFree]
		}
	}

	return nil
}

// cleanStringSlice trims each entry, drops blanks, caps individual length, and
// limits the slice to max items. Returns a non-nil (possibly empty) slice.
func cleanStringSlice(in []string, max int) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if len([]rune(s)) > rpPromoMaxItemLen {
			s = string([]rune(s)[:rpPromoMaxItemLen])
		}
		out = append(out, s)
		if len(out) >= max {
			break
		}
	}
	return out
}
