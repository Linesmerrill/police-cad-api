package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/models"
)

// Best-effort alert to the staff "alerts-website" Discord channel when a newly
// posted promotion looks like a possible duplicate, so staff know to review the
// Server Promos moderation panel. Reads DISCORD_ALERTS_WEBSITE_WEBHOOK_URL;
// silent no-op when unset. One alert per (community, match-kind) within a dedup
// window so a flag isn't announced repeatedly.
const (
	rpPromoAlertWebhookEnv  = "DISCORD_ALERTS_WEBSITE_WEBHOOK_URL"
	rpPromoModerationPanelURL = "https://www.linespolice-cad.com/admin/console#rp-promos"
	rpPromoAlertDedupWindow = 10 * time.Minute
)

var (
	rpPromoAlertMu     sync.Mutex
	rpPromoAlertRecent = map[string]time.Time{}
)

// sendRpPromoFlaggedAlert posts a single review-nudge embed to the alerts
// channel. dedupSig collapses repeats within the dedup window.
func sendRpPromoFlaggedAlert(serverName, ownerName, matchKind string, communityNames []string, inviteURL, dedupSig string) {
	webhook := os.Getenv(rpPromoAlertWebhookEnv)
	if webhook == "" {
		return
	}

	now := time.Now()
	rpPromoAlertMu.Lock()
	if t, ok := rpPromoAlertRecent[dedupSig]; ok && now.Sub(t) < rpPromoAlertDedupWindow {
		rpPromoAlertMu.Unlock()
		return
	}
	rpPromoAlertRecent[dedupSig] = now
	for k, t := range rpPromoAlertRecent {
		if now.Sub(t) > rpPromoAlertDedupWindow {
			delete(rpPromoAlertRecent, k)
		}
	}
	rpPromoAlertMu.Unlock()

	desc := fmt.Sprintf("**%s** by **%s** looks like a possible duplicate (%s).\nCommunities: %s",
		truncRpAlert(serverName, 200), truncRpAlert(ownerName, 100), matchKind, truncRpAlert(strings.Join(communityNames, ", "), 500))
	if inviteURL != "" {
		desc += "\nInvite: " + truncRpAlert(inviteURL, 200)
	}
	desc += "\n\n[Review in Server Promos](" + rpPromoModerationPanelURL + ")"

	payload := map[string]interface{}{
		"content": "🚩 A server promotion was flagged for review.",
		"embeds": []map[string]interface{}{{
			"title":       "Possible duplicate server promo",
			"description": desc,
			"color":       0xfbbf24,
			"timestamp":   now.UTC().Format(time.RFC3339),
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		zap.S().Warnw("rp promo flagged alert: marshal failed", "error", err)
		return
	}

	go func() {
		req, rErr := http.NewRequest(http.MethodPost, webhook, bytes.NewReader(body))
		if rErr != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: rpPromoHTTPDeadline}
		resp, hErr := client.Do(req)
		if hErr != nil {
			zap.S().Warnw("rp promo flagged alert: post failed", "error", hErr)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			zap.S().Warnw("rp promo flagged alert: bad response", "status", resp.StatusCode)
		}
	}()
}

func truncRpAlert(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// RP server promotion → Discord webhook.
//
// Posts a structured recruitment embed to the shared rp-servers channel.
// Reads DISCORD_RP_SERVERS_WEBHOOK_URL from the environment; callers must
// treat an empty URL as "not configured" and refuse the request so a user
// never sees a silent success.
//
// Embed styling (color, verified/featured markers, banner, image gallery)
// scales with the community's boost tier — see rpTierConfig in rp_promotion.go.
//
// Unlike the best-effort cron alerts, this POST is synchronous: we append
// ?wait=true so Discord returns the created message, letting us capture the
// message ID for a future in-place edit.

const (
	rpPromoHTTPDeadline = 10 * time.Second

	// Discord structural limits we defensively truncate against.
	rpPromoMaxTitle   = 256
	rpPromoMaxDesc    = 4096
	rpPromoMaxField   = 1024
	rpPromoMaxFooter  = 2048
	rpPromoMaxContent = 2000

	// Discord allows at most 10 embeds per message. We put the invite link in
	// the message content, so Discord appends its own auto-generated invite
	// card as an extra embed — cap our embeds at 9 to leave a slot for it.
	rpPromoMaxEmbeds = 9
)

type rpDiscordEmbedImage struct {
	URL string `json:"url"`
}

type rpDiscordEmbedFooter struct {
	Text string `json:"text"`
}

type rpDiscordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type rpDiscordEmbed struct {
	Title       string                `json:"title,omitempty"`
	URL         string                `json:"url,omitempty"`
	Description string                `json:"description,omitempty"`
	Color       int                   `json:"color,omitempty"`
	Timestamp   string                `json:"timestamp,omitempty"`
	Fields      []rpDiscordEmbedField `json:"fields,omitempty"`
	Image       *rpDiscordEmbedImage  `json:"image,omitempty"`
	Footer      *rpDiscordEmbedFooter `json:"footer,omitempty"`
}

type rpDiscordWebhookPayload struct {
	Content string           `json:"content,omitempty"`
	Embeds  []rpDiscordEmbed `json:"embeds"`
}

// rpDiscordWebhookResponse is the slice of the message object we care about —
// the message ID and its channel, used to build a jump link.
type rpDiscordWebhookResponse struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
}

func rpTrunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// rpPromoTitlePrefix returns the marker shown before the server name for the
// tier — a star for featured (elite) communities, a check for verified ones.
func rpPromoTitlePrefix(tier rpTierConfig) string {
	if tier.Featured {
		return "⭐ "
	}
	if tier.Verified {
		return "✅ "
	}
	return ""
}

// rpPromoFooterText builds the embed footer for the tier.
func rpPromoFooterText(tier rpTierConfig) string {
	switch {
	case tier.Featured:
		return "⭐ Featured community · " + tier.Label + " · Posted via Lines Police CAD"
	case tier.Verified:
		return "✅ Verified community · " + tier.Label + " · Posted via Lines Police CAD"
	case tier.Key != "free":
		return tier.Label + " · Posted via Lines Police CAD"
	default:
		return "Posted via Lines Police CAD"
	}
}

// rpPromoContentLine builds the webhook message content: a short headline
// plus the bare invite URL on its own line. Discord auto-unfurls an invite
// URL placed in the message content into its native invite card (server
// icon, live online/member counts, Join button) — a link inside an embed
// never unfurls. The handler guarantees a valid InviteURL before we get here.
func rpPromoContentLine(data models.RpPromotionData) string {
	line := "🎮 **Now recruiting — " + data.ServerName + "**\n" + data.InviteURL
	return rpTrunc(line, rpPromoMaxContent)
}

// buildRpPromotionEmbeds renders the structured promotion data into one
// content embed plus extra image-only embeds (up to the tier's image
// allowance). The extra embeds share the content embed's URL so Discord
// groups them into a single image gallery under the post.
func buildRpPromotionEmbeds(data models.RpPromotionData, tier rpTierConfig) []rpDiscordEmbed {
	fields := make([]rpDiscordEmbedField, 0, 6)
	if len(data.Consoles) > 0 {
		fields = append(fields, rpDiscordEmbedField{
			Name:   "🎮 Platform",
			Value:  rpTrunc(strings.Join(data.Consoles, " · "), rpPromoMaxField),
			Inline: true,
		})
	}
	if strings.TrimSpace(data.Game) != "" {
		fields = append(fields, rpDiscordEmbedField{
			Name:   "🕹️ Game",
			Value:  rpTrunc(data.Game, rpPromoMaxField),
			Inline: true,
		})
	}
	if len(data.Departments) > 0 {
		fields = append(fields, rpDiscordEmbedField{
			Name:   "🚓 Departments",
			Value:  rpTrunc("• "+strings.Join(data.Departments, "\n• "), rpPromoMaxField),
			Inline: false,
		})
	}
	if len(data.Features) > 0 {
		fields = append(fields, rpDiscordEmbedField{
			Name:   "🌟 What We Offer",
			Value:  rpTrunc("• "+strings.Join(data.Features, "\n• "), rpPromoMaxField),
			Inline: false,
		})
	}
	if strings.TrimSpace(data.Requirements) != "" {
		fields = append(fields, rpDiscordEmbedField{
			Name:   "📋 Requirements",
			Value:  rpTrunc(data.Requirements, rpPromoMaxField),
			Inline: false,
		})
	}
	fields = append(fields, rpDiscordEmbedField{
		Name:  "💬 Join",
		Value: data.InviteURL,
	})

	content := rpDiscordEmbed{
		Title:       rpTrunc(rpPromoTitlePrefix(tier)+data.ServerName, rpPromoMaxTitle),
		URL:         data.InviteURL,
		Description: rpTrunc(data.Description, rpPromoMaxDesc),
		Color:       tier.colorInt(),
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Fields:      fields,
		Footer:      &rpDiscordEmbedFooter{Text: rpTrunc(rpPromoFooterText(tier), rpPromoMaxFooter)},
	}

	// Primary image: a tier-allowed banner wins, else the first uploaded image.
	bannerUsed := false
	if tier.AllowBanner && strings.TrimSpace(data.BannerImage) != "" {
		content.Image = &rpDiscordEmbedImage{URL: data.BannerImage}
		bannerUsed = true
	} else if len(data.Images) > 0 {
		content.Image = &rpDiscordEmbedImage{URL: data.Images[0]}
	}

	embeds := []rpDiscordEmbed{content}

	// Remaining images become a gallery. When the banner is the hero image,
	// every uploaded image is still available for the gallery; otherwise the
	// first image was already used as the hero.
	if data.InviteURL != "" && len(data.Images) > 0 {
		start := 0
		if !bannerUsed {
			start = 1
		}
		for i := start; i < len(data.Images) && len(embeds) < rpPromoMaxEmbeds; i++ {
			if strings.TrimSpace(data.Images[i]) == "" {
				continue
			}
			embeds = append(embeds, rpDiscordEmbed{
				URL:   data.InviteURL,
				Image: &rpDiscordEmbedImage{URL: data.Images[i]},
			})
		}
	}

	return embeds
}

// sendRpPromotionWebhook posts the promotion embeds to Discord and returns the
// created message's ID and channel ID. webhookURL must be non-empty (the
// handler checks first).
func sendRpPromotionWebhook(webhookURL string, data models.RpPromotionData, tier rpTierConfig) (messageID, channelID string, err error) {
	payload := rpDiscordWebhookPayload{
		Content: rpPromoContentLine(data),
		Embeds:  buildRpPromotionEmbeds(data, tier),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("marshal webhook payload: %w", err)
	}

	// ?wait=true makes Discord return the created message so we can keep its ID.
	url := webhookURL
	if strings.Contains(url, "?") {
		url += "&wait=true"
	} else {
		url += "?wait=true"
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: rpPromoHTTPDeadline}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("post to discord: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("discord returned status %d", resp.StatusCode)
	}

	var parsed rpDiscordWebhookResponse
	// The IDs are a nice-to-have (jump link, future edits) — a decode failure
	// should not fail the user's post, since the message did go through.
	_ = json.NewDecoder(resp.Body).Decode(&parsed)
	return parsed.ID, parsed.ChannelID, nil
}

// deleteRpPromotionWebhookMessage removes a previously posted promotion from the
// Discord channel. A webhook can delete its own messages, so no bot token is
// needed — we issue DELETE {webhookURL}/messages/{messageID}. The webhook URL
// is of the form https://discord.com/api/webhooks/{id}/{token}; we strip any
// query string before appending the messages path. A 404 means the message is
// already gone, which we treat as success (the desired end state is reached).
func deleteRpPromotionWebhookMessage(webhookURL, messageID string) error {
	if strings.TrimSpace(webhookURL) == "" {
		return fmt.Errorf("webhook url not configured")
	}
	if strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("missing message id")
	}

	base := webhookURL
	if i := strings.Index(base, "?"); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimRight(base, "/")
	url := base + "/messages/" + messageID

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("build delete request: %w", err)
	}

	client := &http.Client{Timeout: rpPromoHTTPDeadline}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("delete from discord: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil // already deleted
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord returned status %d", resp.StatusCode)
	}
	return nil
}
