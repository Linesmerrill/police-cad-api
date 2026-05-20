package handlers

import (
	"strings"
	"testing"

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
	if err := sanitizeRpPromotionData(&data, false); err != nil {
		t.Fatalf("expected valid data to pass, got: %v", err)
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
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			data := validRpData()
			mutate(&data)
			if err := sanitizeRpPromotionData(&data, false); err == nil {
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
	if err := sanitizeRpPromotionData(&data, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.BannerImage != "" {
		t.Errorf("free tier should drop the banner image, got %q", data.BannerImage)
	}
	if len(data.Images) != rpPromoMaxImagesFree {
		t.Errorf("free tier should cap images at %d, got %d", rpPromoMaxImagesFree, len(data.Images))
	}
}

func TestSanitizeRpPromotionData_BoostedKeepsBoostFields(t *testing.T) {
	data := validRpData()
	data.BannerImage = "https://cdn.example.com/banner.png"
	data.Images = []string{"https://cdn.example.com/a.png", "https://cdn.example.com/b.png"}
	if err := sanitizeRpPromotionData(&data, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.BannerImage == "" {
		t.Error("boosted tier should keep the banner image")
	}
	if len(data.Images) != 2 {
		t.Errorf("boosted tier should keep both images, got %d", len(data.Images))
	}
}

func TestSanitizeRpPromotionData_FeatureCap(t *testing.T) {
	data := validRpData()
	for i := 0; i < rpPromoMaxFeaturesFree+3; i++ {
		data.Features = append(data.Features, "feature")
	}
	if err := sanitizeRpPromotionData(&data, false); err == nil {
		t.Error("expected free tier to reject too many features")
	}
	if err := sanitizeRpPromotionData(&data, true); err != nil {
		t.Errorf("boosted tier should accept more features: %v", err)
	}
}

func TestSanitizeRpPromotionData_RejectsNonHttpsImage(t *testing.T) {
	data := validRpData()
	data.Images = []string{"http://cdn.example.com/a.png"}
	if err := sanitizeRpPromotionData(&data, true); err == nil {
		t.Error("expected non-https image URL to be rejected")
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

func TestBuildRpPromotionEmbeds_FreeSingleEmbed(t *testing.T) {
	data := validRpData()
	data.Images = []string{"https://cdn.example.com/a.png"}
	embeds := buildRpPromotionEmbeds(data, false)
	if len(embeds) != 1 {
		t.Fatalf("free tier should produce exactly 1 embed, got %d", len(embeds))
	}
	if embeds[0].Color != rpPromoColorFree {
		t.Errorf("free embed color = %d, want %d", embeds[0].Color, rpPromoColorFree)
	}
	if embeds[0].Image == nil || embeds[0].Image.URL != data.Images[0] {
		t.Error("free embed should use the single image as the content image")
	}
}

func TestBuildRpPromotionEmbeds_BoostedGallery(t *testing.T) {
	data := validRpData()
	data.BannerImage = "https://cdn.example.com/banner.png"
	data.Images = []string{"https://cdn.example.com/a.png", "https://cdn.example.com/b.png"}
	embeds := buildRpPromotionEmbeds(data, true)
	if len(embeds) != 3 {
		t.Fatalf("boosted with banner + 2 images should produce 3 embeds, got %d", len(embeds))
	}
	if embeds[0].Color != rpPromoColorBoosted {
		t.Errorf("boosted embed color = %d, want %d", embeds[0].Color, rpPromoColorBoosted)
	}
	if embeds[0].Image == nil || embeds[0].Image.URL != data.BannerImage {
		t.Error("boosted content embed should use the banner image")
	}
	for i := 1; i < len(embeds); i++ {
		if embeds[i].URL != data.InviteURL {
			t.Errorf("gallery embed %d must share the invite URL to group", i)
		}
	}
}
