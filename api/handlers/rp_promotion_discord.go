package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/linesmerrill/police-cad-api/models"
)

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
	rpPromoMaxTitle  = 256
	rpPromoMaxDesc   = 4096
	rpPromoMaxField  = 1024
	rpPromoMaxFooter = 2048

	// Discord allows at most 10 embeds per message.
	rpPromoMaxEmbeds = 10
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

// rpDiscordWebhookResponse is the slice of the message object we care about.
type rpDiscordWebhookResponse struct {
	ID string `json:"id"`
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
// created message ID. webhookURL must be non-empty (the handler checks first).
func sendRpPromotionWebhook(webhookURL string, data models.RpPromotionData, tier rpTierConfig) (string, error) {
	payload := rpDiscordWebhookPayload{Embeds: buildRpPromotionEmbeds(data, tier)}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal webhook payload: %w", err)
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
		return "", fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: rpPromoHTTPDeadline}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("post to discord: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("discord returned status %d", resp.StatusCode)
	}

	var parsed rpDiscordWebhookResponse
	// The message ID is a nice-to-have for future edits — a decode failure
	// should not fail the user's post, since the message did go through.
	_ = json.NewDecoder(resp.Body).Decode(&parsed)
	return parsed.ID, nil
}
