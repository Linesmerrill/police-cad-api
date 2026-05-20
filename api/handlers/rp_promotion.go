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
//
// The richness of a promotion scales with the community's boost tier — see
// rpTiers below. Tier keys/colors mirror the community-pricing page
// (app/community-pricing/page.tsx in police-cad).

const (
	rpPromoWebhookEnv         = "DISCORD_RP_SERVERS_WEBHOOK_URL"
	rpPromoCooldownEnv        = "RP_PROMOTION_COOLDOWN_HOURS"
	rpPromoDefaultCooldownHrs = 24

	// Flat caps that don't scale with tier.
	rpPromoMaxServerName  = 100
	rpPromoMaxDepartments = 12
	rpPromoMaxItemLen     = 120

	// How many past posts to retain per community for the history panel.
	rpPromoHistoryMax = 20
)

// rpTierConfig defines how rich a promotion a given boost tier may post and
// how its Discord embed is styled. Free communities get the smallest
// allowance so each paid tier is a visible, tangible upgrade.
type rpTierConfig struct {
	Key         string `json:"key"`         // "free" | "basic" | "standard" | "premium" | "elite"
	Label       string `json:"label"`       // human label, also used in the embed footer
	ColorHex    string `json:"color"`       // embed accent + website preview color
	DescMax     int    `json:"descMax"`     // description character cap
	FeaturesMax int    `json:"featuresMax"` // max "What We Offer" bullets
	ImagesMax   int    `json:"imagesMax"`   // max uploaded images
	AllowBanner bool   `json:"allowBanner"` // dedicated banner image
	Verified    bool   `json:"verified"`    // ✓ verified-community marker
	Featured    bool   `json:"featured"`    // ⭐ featured marker (elite)
}

// colorInt converts the tier's hex color to the integer form Discord wants.
func (t rpTierConfig) colorInt() int {
	n, err := strconv.ParseInt(strings.TrimPrefix(t.ColorHex, "#"), 16, 32)
	if err != nil {
		return 0x38bdf8
	}
	return int(n)
}

// rpTiers is the canonical tier ladder. Colors match the community-pricing
// tier cards: free=cyan, basic=blue, standard=emerald, premium=indigo,
// elite=gold.
var rpTiers = map[string]rpTierConfig{
	"free":     {Key: "free", Label: "Free", ColorHex: "#38bdf8", DescMax: 600, FeaturesMax: 6, ImagesMax: 1, AllowBanner: false, Verified: false, Featured: false},
	"basic":    {Key: "basic", Label: "Basic Boost", ColorHex: "#3b82f6", DescMax: 1000, FeaturesMax: 8, ImagesMax: 1, AllowBanner: false, Verified: false, Featured: false},
	"standard": {Key: "standard", Label: "Standard Boost", ColorHex: "#10b981", DescMax: 1500, FeaturesMax: 10, ImagesMax: 2, AllowBanner: false, Verified: true, Featured: false},
	"premium":  {Key: "premium", Label: "Premium Boost", ColorHex: "#667eea", DescMax: 2500, FeaturesMax: 12, ImagesMax: 3, AllowBanner: true, Verified: true, Featured: false},
	"elite":    {Key: "elite", Label: "Elite Boost", ColorHex: "#fbbf24", DescMax: 3500, FeaturesMax: 15, ImagesMax: 5, AllowBanner: true, Verified: true, Featured: true},
}

// rpPromotionTierForCommunity resolves a community's effective promotion tier.
// An inactive or unrecognized subscription falls back to the free tier.
func rpPromotionTierForCommunity(c *models.Community) rpTierConfig {
	if !c.Details.Subscription.Active {
		return rpTiers["free"]
	}
	key := strings.ToLower(strings.TrimSpace(c.Details.Subscription.Plan))
	if t, ok := rpTiers[key]; ok {
		return t
	}
	return rpTiers["free"]
}

// rpPromotionHistoryEntry is a history post shaped for the API response —
// PostedAt is rendered as an RFC3339 string for the client.
type rpPromotionHistoryEntry struct {
	ID        string                 `json:"id"`
	PostedAt  string                 `json:"postedAt"`
	PostedBy  string                 `json:"postedBy"`
	Tier      string                 `json:"tier"`
	MessageID string                 `json:"messageId,omitempty"`
	Data      models.RpPromotionData `json:"data"`
}

// rpPromotionHistoryNewestFirst returns the community's promotion history
// ordered most-recent-first for display. Always non-nil.
func rpPromotionHistoryNewestFirst(c *models.Community) []rpPromotionHistoryEntry {
	out := []rpPromotionHistoryEntry{}
	if c.Details.RpPromotion == nil {
		return out
	}
	h := c.Details.RpPromotion.History
	for i := len(h) - 1; i >= 0; i-- {
		out = append(out, rpPromotionHistoryEntry{
			ID:        h[i].ID,
			PostedAt:  h[i].PostedAt.Time().UTC().Format(time.RFC3339),
			PostedBy:  h[i].PostedBy,
			Tier:      h[i].Tier,
			MessageID: h[i].MessageID,
			Data:      h[i].Data,
		})
	}
	return out
}

// rpPromotionAutofill builds the form defaults the website seeds a new
// promotion with, sourced from the community's current settings.
func rpPromotionAutofill(c *models.Community) map[string]interface{} {
	deptNames := []string{}
	for _, d := range c.Details.Departments {
		if strings.TrimSpace(d.Name) != "" {
			deptNames = append(deptNames, d.Name)
		}
	}
	desc := c.Details.PromotionalDescription
	if strings.TrimSpace(desc) == "" {
		desc = c.Details.Description
	}
	platforms := c.Details.Tags
	if platforms == nil {
		platforms = []string{}
	}
	return map[string]interface{}{
		"serverName":  c.Details.Name,
		"description": desc,
		"departments": deptNames,
		"platforms":   platforms,
	}
}

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

// GetRpPromotionHandler returns the community's last promotion post, its boost
// tier allowance, and whether a new post can be made yet. Owner/admin only.
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

	tier := rpPromotionTierForCommunity(community)
	cooldown := rpPromotionCooldown()
	resp := map[string]interface{}{
		"tier":           tier,
		"boosted":        tier.Key != "free",
		"canPostNow":     true,
		"cooldownHours":  int(cooldown.Hours()),
		"configured":     os.Getenv(rpPromoWebhookEnv) != "",
		"maxDepartments": rpPromoMaxDepartments,
		"history":        rpPromotionHistoryNewestFirst(community),
		// Fresh-from-DB defaults for seeding a new post — the website prefers
		// these over its page-rendered copy, which can be stale after the
		// admin edits community settings in the same session.
		"autofill": rpPromotionAutofill(community),
	}
	if rp := community.Details.RpPromotion; rp != nil && rp.LastPostedAt != nil {
		postedAt := rp.LastPostedAt.Time()
		resp["lastPostedAt"] = postedAt.UTC().Format(time.RFC3339)
		nextAt := postedAt.Add(cooldown)
		if time.Now().Before(nextAt) {
			resp["canPostNow"] = false
			resp["nextAvailableAt"] = nextAt.UTC().Format(time.RFC3339)
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

	tier := rpPromotionTierForCommunity(community)

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

	// Validate + normalize the submitted content against the tier allowance.
	if vErr := sanitizeRpPromotionData(&data, tier); vErr != nil {
		config.ErrorStatus(vErr.Error(), http.StatusBadRequest, w, vErr)
		return
	}

	// Post to Discord. This is synchronous so we can surface a failure to the
	// user and capture the message ID; nothing is persisted if it fails.
	messageID, err := sendRpPromotionWebhook(webhookURL, data, tier)
	if err != nil {
		zap.S().Errorw("rp promotion: discord post failed", "community_id", communityID, "error", err)
		config.ErrorStatus("failed to post promotion to Discord", http.StatusBadGateway, w, err)
		return
	}

	now := time.Now()
	nowDT := primitive.NewDateTimeFromTime(now)
	post := models.RpPromotionPost{
		ID:        primitive.NewObjectID().Hex(),
		PostedAt:  nowDT,
		PostedBy:  actorID,
		Tier:      tier.Key,
		MessageID: messageID,
		Data:      data,
	}
	// Append to history (capped to the most recent rpPromoHistoryMax) and bump
	// the cooldown timestamp. $push/$set create community.rpPromotion if absent.
	update := bson.M{
		"$set": bson.M{"community.rpPromotion.lastPostedAt": nowDT},
		"$push": bson.M{"community.rpPromotion.history": bson.M{
			"$each":  []models.RpPromotionPost{post},
			"$slice": -rpPromoHistoryMax,
		}},
	}
	if err := c.DB.UpdateOne(ctx, bson.M{"_id": communityObjID}, update); err != nil {
		// The Discord post already succeeded — log loudly but still report
		// success so the user isn't told it failed when it didn't. The next
		// cooldown check just won't see this post; acceptable degradation.
		zap.S().Errorw("rp promotion: posted to discord but failed to persist", "community_id", communityID, "error", err)
	}

	logAudit(c.ALDB, communityObjID, "rp_promotion.posted", "community", actorID, resolveActorName(c.UDB, actorID), "", "",
		map[string]interface{}{"serverName": data.ServerName, "tier": tier.Key})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":         "promotion posted",
		"postedAt":        now.UTC().Format(time.RFC3339),
		"nextAvailableAt": now.Add(cooldown).UTC().Format(time.RFC3339),
		"messageId":       messageID,
		"tier":            tier.Key,
	})
}

// sanitizeRpPromotionData validates required fields, trims/caps every field to
// the community's tier allowance, and strips banner/extra images the tier does
// not permit. Mutates data in place. Returns a user-facing error on failure.
func sanitizeRpPromotionData(data *models.RpPromotionData, tier rpTierConfig) error {
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
	if len([]rune(data.Description)) > tier.DescMax {
		return fmt.Errorf("description must be %d characters or fewer on the %s tier", tier.DescMax, tier.Label)
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

	if len(cleanStringSlice(data.Features, 1000)) > tier.FeaturesMax {
		return fmt.Errorf("the %s tier allows up to %d features — boost to add more", tier.Label, tier.FeaturesMax)
	}
	data.Features = cleanStringSlice(data.Features, tier.FeaturesMax)

	if len([]rune(data.Requirements)) > rpPromoMaxItemLen*4 {
		data.Requirements = string([]rune(data.Requirements)[:rpPromoMaxItemLen*4])
	}

	// Image / banner tier rules.
	data.Images = cleanStringSlice(data.Images, tier.ImagesMax)
	for _, img := range data.Images {
		if !strings.HasPrefix(strings.ToLower(img), "https://") {
			return fmt.Errorf("image URLs must be https")
		}
	}
	if tier.AllowBanner {
		data.BannerImage = strings.TrimSpace(data.BannerImage)
		if data.BannerImage != "" && !strings.HasPrefix(strings.ToLower(data.BannerImage), "https://") {
			return fmt.Errorf("banner image URL must be https")
		}
	} else {
		data.BannerImage = ""
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
