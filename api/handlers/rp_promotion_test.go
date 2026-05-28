package handlers

import (
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"
)

func validRpData() models.RpPromotionData {
	return models.RpPromotionData{
		ServerName:   "Georgia State Roleplay",
		Consoles:     []string{"PS5", "Xbox"},
		Game:         "GTA RP",
		Description:  "A structured, immersive roleplay community.",
		Departments:  []string{"Police", "EMS"},
		Features:     []string{"Custom CAD", "Active staff"},
		Requirements: "Working mic",
		InviteURL:    "https://discord.gg/abc123",
	}
}

func TestSanitizeRpPromotionData_Valid(t *testing.T) {
	data := validRpData()
	if err := sanitizeRpPromotionData(&data, rpTiers["free"]); err != nil {
		t.Fatalf("expected valid data to pass, got: %v", err)
	}
}

func TestSanitizeRpPromotionData_AcceptsInviteVariants(t *testing.T) {
	cases := []string{
		"https://discord.gg/abc123",
		"https://discord.com/invite/abc123",
		"https://discordapp.com/invite/abc123",
		"https://www.discord.com/invite/abc123",
		"https://ptb.discord.com/invite/abc123",
		"https://canary.discord.com/invite/abc123",
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			data := validRpData()
			data.InviteURL = u
			if err := sanitizeRpPromotionData(&data, rpTiers["free"]); err != nil {
				t.Errorf("expected %q to be accepted, got: %v", u, err)
			}
		})
	}
}

func TestSanitizeRpPromotionData_RequiredFields(t *testing.T) {
	cases := map[string]func(*models.RpPromotionData){
		"missing server name": func(d *models.RpPromotionData) { d.ServerName = "  " },
		"missing game":        func(d *models.RpPromotionData) { d.Game = "" },
		"missing description": func(d *models.RpPromotionData) { d.Description = "" },
		"no consoles":         func(d *models.RpPromotionData) { d.Consoles = nil },
		"non-discord invite":  func(d *models.RpPromotionData) { d.InviteURL = "https://example.com/x" },
		"non-https invite":    func(d *models.RpPromotionData) { d.InviteURL = "http://discord.gg/x" },
		"channel deep-link": func(d *models.RpPromotionData) {
			d.InviteURL = "https://discord.com/channels/1508945617169285264/1508948755867635823"
		},
		"discord profile URL": func(d *models.RpPromotionData) {
			d.InviteURL = "https://discord.com/users/123456789"
		},
		"discord.gg with no code": func(d *models.RpPromotionData) { d.InviteURL = "https://discord.gg/" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			data := validRpData()
			mutate(&data)
			if err := sanitizeRpPromotionData(&data, rpTiers["free"]); err == nil {
				t.Errorf("%s: expected validation error, got nil", name)
			}
		})
	}
}

func TestSanitizeRpPromotionData_FreeTierStripsBoostFields(t *testing.T) {
	data := validRpData()
	data.BannerImage = "https://cdn.example.com/banner.png"
	data.Images = []string{
		"https://cdn.example.com/a.png",
		"https://cdn.example.com/b.png",
		"https://cdn.example.com/c.png",
	}
	if err := sanitizeRpPromotionData(&data, rpTiers["free"]); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.BannerImage != "" {
		t.Errorf("free tier should drop the banner image, got %q", data.BannerImage)
	}
	if len(data.Images) != rpTiers["free"].ImagesMax {
		t.Errorf("free tier should cap images at %d, got %d", rpTiers["free"].ImagesMax, len(data.Images))
	}
}

func TestSanitizeRpPromotionData_EliteKeepsBoostFields(t *testing.T) {
	data := validRpData()
	data.BannerImage = "https://cdn.example.com/banner.png"
	data.Images = []string{"https://cdn.example.com/a.png", "https://cdn.example.com/b.png"}
	if err := sanitizeRpPromotionData(&data, rpTiers["elite"]); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.BannerImage == "" {
		t.Error("elite tier should keep the banner image")
	}
	if len(data.Images) != 2 {
		t.Errorf("elite tier should keep both images, got %d", len(data.Images))
	}
}

func TestSanitizeRpPromotionData_BannerOnlyOnAllowedTiers(t *testing.T) {
	// standard does not allow a banner; premium does.
	for _, tc := range []struct {
		tier       string
		bannerKept bool
	}{
		{"basic", false},
		{"standard", false},
		{"premium", true},
		{"elite", true},
	} {
		data := validRpData()
		data.BannerImage = "https://cdn.example.com/banner.png"
		if err := sanitizeRpPromotionData(&data, rpTiers[tc.tier]); err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.tier, err)
		}
		got := data.BannerImage != ""
		if got != tc.bannerKept {
			t.Errorf("%s: banner kept = %v, want %v", tc.tier, got, tc.bannerKept)
		}
	}
}

func TestSanitizeRpPromotionData_FeatureCapScalesWithTier(t *testing.T) {
	data := validRpData()
	for i := 0; i < rpTiers["free"].FeaturesMax+3; i++ {
		data.Features = append(data.Features, "feature")
	}
	if err := sanitizeRpPromotionData(&data, rpTiers["free"]); err == nil {
		t.Error("expected free tier to reject too many features")
	}
	if err := sanitizeRpPromotionData(&data, rpTiers["elite"]); err != nil {
		t.Errorf("elite tier should accept more features: %v", err)
	}
}

func TestSanitizeRpPromotionData_DescriptionCapScalesWithTier(t *testing.T) {
	data := validRpData()
	data.Description = strings.Repeat("x", rpTiers["free"].DescMax+50)
	if err := sanitizeRpPromotionData(&data, rpTiers["free"]); err == nil {
		t.Error("expected free tier to reject an over-long description")
	}
	if err := sanitizeRpPromotionData(&data, rpTiers["elite"]); err != nil {
		t.Errorf("elite tier should accept the longer description: %v", err)
	}
}

func TestSanitizeRpPromotionData_GalleryCappedToRenderLimit(t *testing.T) {
	imgs := []string{
		"https://cdn.example.com/a.png", "https://cdn.example.com/b.png",
		"https://cdn.example.com/c.png", "https://cdn.example.com/d.png",
		"https://cdn.example.com/e.png",
	}

	// With a banner, Discord renders 4 images total → banner + 3 gallery.
	withBanner := validRpData()
	withBanner.BannerImage = "https://cdn.example.com/banner.png"
	withBanner.Images = append([]string{}, imgs...)
	if err := sanitizeRpPromotionData(&withBanner, rpTiers["elite"]); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(withBanner.Images) != rpPromoMaxRenderedImages-1 {
		t.Errorf("banner present: gallery should cap at %d, got %d", rpPromoMaxRenderedImages-1, len(withBanner.Images))
	}

	// Without a banner, the gallery itself may use all 4 render slots.
	noBanner := validRpData()
	noBanner.Images = append([]string{}, imgs...)
	if err := sanitizeRpPromotionData(&noBanner, rpTiers["elite"]); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(noBanner.Images) != rpPromoMaxRenderedImages {
		t.Errorf("no banner: gallery should cap at %d, got %d", rpPromoMaxRenderedImages, len(noBanner.Images))
	}
}

func TestSanitizeRpPromotionData_RejectsNonHttpsImage(t *testing.T) {
	data := validRpData()
	data.Images = []string{"http://cdn.example.com/a.png"}
	if err := sanitizeRpPromotionData(&data, rpTiers["elite"]); err == nil {
		t.Error("expected non-https image URL to be rejected")
	}
}

func TestSanitizeRpPromotionData_PreservesLongImageURL(t *testing.T) {
	// A real Cloudinary URL easily exceeds rpPromoMaxItemLen; sanitizing must
	// not truncate it (a truncated URL is a broken image).
	longURL := "https://res.cloudinary.com/dqtwwvm7p/image/upload/v1747054441/" +
		"community-promotions/community-promotions/" +
		strings.Repeat("abcdef0123456789", 6) + ".jpg"
	if len(longURL) <= rpPromoMaxItemLen {
		t.Fatalf("test URL is %d chars, expected longer than %d", len(longURL), rpPromoMaxItemLen)
	}
	data := validRpData()
	data.Images = []string{longURL}
	if err := sanitizeRpPromotionData(&data, rpTiers["free"]); err != nil {
		t.Fatalf("sanitize returned error: %v", err)
	}
	if len(data.Images) != 1 || data.Images[0] != longURL {
		t.Errorf("long image URL was not preserved intact: got %v", data.Images)
	}
}

func TestRpPromotionTierForCommunity(t *testing.T) {
	cases := []struct {
		name   string
		active bool
		plan   string
		want   string
	}{
		{"no subscription", false, "", "free"},
		{"inactive premium", false, "premium", "free"},
		{"active basic", true, "basic", "basic"},
		{"active standard", true, "standard", "standard"},
		{"active elite uppercase", true, "Elite", "elite"},
		{"active unknown plan", true, "mystery", "free"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &models.Community{}
			c.Details.Subscription.Active = tc.active
			c.Details.Subscription.Plan = tc.plan
			if got := rpPromotionTierForCommunity(c); got.Key != tc.want {
				t.Errorf("got tier %q, want %q", got.Key, tc.want)
			}
		})
	}
}

func TestRpPromotionHistoryNewestFirst(t *testing.T) {
	// nil promotion → empty (non-nil) slice.
	if got := rpPromotionHistoryNewestFirst(&models.Community{}, nil); got == nil || len(got) != 0 {
		t.Errorf("expected empty non-nil slice for no promotion, got %#v", got)
	}

	// History is stored oldest→newest; the response must be newest→oldest.
	c := &models.Community{}
	c.Details.RpPromotion = &models.RpPromotion{
		History: []models.RpPromotionPost{
			{ID: "oldest", PostedAt: primitive.NewDateTimeFromTime(time.Unix(1000, 0))},
			{ID: "middle", PostedAt: primitive.NewDateTimeFromTime(time.Unix(2000, 0))},
			{ID: "newest", PostedAt: primitive.NewDateTimeFromTime(time.Unix(3000, 0))},
		},
	}
	got := rpPromotionHistoryNewestFirst(c, nil)
	want := []string{"newest", "middle", "oldest"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("entry %d: got %q, want %q", i, got[i].ID, id)
		}
		if got[i].PostedAt == "" {
			t.Errorf("entry %d: postedAt should be a formatted string", i)
		}
	}
}

func TestRpPromotionHistoryUsesStoredPostedByName(t *testing.T) {
	// A post with PostedByName set must surface that name directly, with no
	// user-database lookup (udb is nil here, so a lookup would panic).
	c := &models.Community{}
	c.Details.RpPromotion = &models.RpPromotion{
		History: []models.RpPromotionPost{
			{ID: "p1", PostedBy: "507f1f77bcf86cd799439011", PostedByName: "ChiefWiggum",
				PostedAt: primitive.NewDateTimeFromTime(time.Unix(1000, 0))},
		},
	}
	got := rpPromotionHistoryNewestFirst(c, nil)
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].PostedByName != "ChiefWiggum" {
		t.Errorf("postedByName: got %q, want %q", got[0].PostedByName, "ChiefWiggum")
	}
}

func TestRpPromotionMessageLink(t *testing.T) {
	// No guild configured → no link.
	if got := rpPromotionMessageLink("chan", "msg"); got != "" {
		t.Errorf("expected empty link without guild env, got %q", got)
	}

	t.Setenv("DISCORD_RP_SERVERS_GUILD_ID", "111")
	if got := rpPromotionMessageLink("222", "333"); got != "https://discord.com/channels/111/222/333" {
		t.Errorf("unexpected link: %q", got)
	}
	// Missing channel or message ID → no link even with a guild set.
	if got := rpPromotionMessageLink("", "333"); got != "" {
		t.Errorf("expected empty link with missing channel, got %q", got)
	}
	if got := rpPromotionMessageLink("222", ""); got != "" {
		t.Errorf("expected empty link with missing message, got %q", got)
	}
}

func TestCleanStringSlice(t *testing.T) {
	got := cleanStringSlice([]string{" a ", "", "b", "  ", "c"}, 10)
	want := []string{"a", "b", "c"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
	if capped := cleanStringSlice([]string{"a", "b", "c", "d"}, 2); len(capped) != 2 {
		t.Errorf("expected cap to 2, got %d", len(capped))
	}
}

func TestRpPromoContentLine(t *testing.T) {
	data := validRpData()
	got := rpPromoContentLine(data)
	if !strings.Contains(got, data.ServerName) {
		t.Errorf("content line should include the server name, got %q", got)
	}
	// The bare invite URL must be present so Discord unfurls its invite card.
	if !strings.Contains(got, data.InviteURL) {
		t.Errorf("content line should include the invite URL, got %q", got)
	}
	if len([]rune(got)) > rpPromoMaxContent {
		t.Errorf("content line exceeds Discord's %d-char limit: %d", rpPromoMaxContent, len([]rune(got)))
	}
}

func TestBuildRpPromotionEmbeds_FreeSingleEmbed(t *testing.T) {
	data := validRpData()
	data.Images = []string{"https://cdn.example.com/a.png"}
	embeds := buildRpPromotionEmbeds(data, rpTiers["free"])
	if len(embeds) != 1 {
		t.Fatalf("free tier with 1 image should produce exactly 1 embed, got %d", len(embeds))
	}
	if embeds[0].Color != rpTiers["free"].colorInt() {
		t.Errorf("free embed color = %d, want %d", embeds[0].Color, rpTiers["free"].colorInt())
	}
	if embeds[0].Image == nil || embeds[0].Image.URL != data.Images[0] {
		t.Error("free embed should use the single image as the content image")
	}
}

func TestBuildRpPromotionEmbeds_EliteGalleryAndMarkers(t *testing.T) {
	data := validRpData()
	data.BannerImage = "https://cdn.example.com/banner.png"
	data.Images = []string{"https://cdn.example.com/a.png", "https://cdn.example.com/b.png"}
	embeds := buildRpPromotionEmbeds(data, rpTiers["elite"])
	// banner = hero embed; both images = 2 gallery embeds → 3 total.
	if len(embeds) != 3 {
		t.Fatalf("elite with banner + 2 images should produce 3 embeds, got %d", len(embeds))
	}
	if embeds[0].Color != rpTiers["elite"].colorInt() {
		t.Errorf("elite embed color = %d, want %d", embeds[0].Color, rpTiers["elite"].colorInt())
	}
	if embeds[0].Image == nil || embeds[0].Image.URL != data.BannerImage {
		t.Error("elite content embed should use the banner image as the hero")
	}
	if !strings.HasPrefix(embeds[0].Title, "⭐") {
		t.Errorf("elite (featured) title should start with the star marker, got %q", embeds[0].Title)
	}
	for i := 1; i < len(embeds); i++ {
		if embeds[i].URL != data.InviteURL {
			t.Errorf("gallery embed %d must share the invite URL to group", i)
		}
	}
}

func TestBuildRpPromotionEmbeds_VerifiedMarker(t *testing.T) {
	data := validRpData()
	embeds := buildRpPromotionEmbeds(data, rpTiers["standard"])
	if !strings.HasPrefix(embeds[0].Title, "✅") {
		t.Errorf("standard (verified) title should start with the check marker, got %q", embeds[0].Title)
	}
	if strings.HasPrefix(embeds[0].Title, "⭐") {
		t.Error("standard tier should not get the featured star")
	}
}
