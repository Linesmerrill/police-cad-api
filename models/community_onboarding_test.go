package models

import (
	"encoding/json"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Production survey (Aug 2026), over the 2,285 public communities with at least
// two members: 108 carry a Discord invite buried in their description and 225
// carry one inside an RP promotion. Neither was ever shown to a member. Of the
// ~143 public communities that put any URL in their description at all,
// essentially every one of them is a Discord invite.

func TestIsDiscordInviteURL(t *testing.T) {
	valid := []string{
		"https://discord.gg/7Wa49ZQj",
		"https://www.discord.gg/Tr3D9nSExz",
		"https://ptb.discord.com/invite/4hFbf89M",
		"https://canary.discord.com/invite/HgMTQ8w5Sj",
		"https://discordapp.com/invite/xURUyB83Ve",
	}
	for _, s := range valid {
		if !IsDiscordInviteURL(s) {
			t.Errorf("IsDiscordInviteURL(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"",
		"discord.gg/abc",                       // no scheme
		"http://discord.gg/abc",                // not https
		"https://discord.com/channels/123/456", // channel deep link, not an invite
		"https://discord.com/users/123",        // profile
		"https://discord.gg/",                  // no code
		"https://discord.gg/abc/def",           // extra path segment
		"https://example.com/discord.gg/abc",   // wrong host
		"https://notdiscord.gg/abc",            // lookalike host
	}
	for _, s := range invalid {
		if IsDiscordInviteURL(s) {
			t.Errorf("IsDiscordInviteURL(%q) = true, want false", s)
		}
	}
}

func TestExtractDiscordInvite(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "bare invite with no scheme, the common case in descriptions",
			text: "Serious RP server. Join us at discord.gg/7Wa49ZQj to apply!",
			want: "https://discord.gg/7Wa49ZQj",
		},
		{
			name: "full https url",
			text: "Apply here: https://discord.gg/Tr3D9nSExz",
			want: "https://discord.gg/Tr3D9nSExz",
		},
		{
			name: "http gets canonicalised to https",
			text: "http://discord.gg/4hFbf89M",
			want: "https://discord.gg/4hFbf89M",
		},
		{
			name: "discord.com/invite is canonicalised to discord.gg",
			text: "come say hi https://discord.com/invite/HgMTQ8w5Sj",
			want: "https://discord.gg/HgMTQ8w5Sj",
		},
		{
			name: "www prefix",
			text: "www.discord.gg/xURUyB83Ve",
			want: "https://discord.gg/xURUyB83Ve",
		},
		{
			name: "hyphenated codes survive",
			text: "discord.gg/my-server-name",
			want: "https://discord.gg/my-server-name",
		},
		{
			name: "first invite wins when there are several",
			text: "main discord.gg/aaa backup discord.gg/bbb",
			want: "https://discord.gg/aaa",
		},
		{
			name: "a channel deep link is not an invite",
			text: "see https://discord.com/channels/123/456",
			want: "",
		},
		{
			name: "the word discord alone is not a link",
			text: "We run everything through our Discord server.",
			want: "",
		},
		{name: "empty text", text: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractDiscordInvite(tt.text); got != tt.want {
				t.Errorf("ExtractDiscordInvite(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

// Whatever ExtractDiscordInvite emits must satisfy the strict validator, or the
// fallback chain would hand clients links the rest of the system rejects.
func TestExtractedInvitesAreValid(t *testing.T) {
	inputs := []string{
		"discord.gg/7Wa49ZQj",
		"http://discord.gg/Tr3D9nSExz",
		"https://discord.com/invite/4hFbf89M",
		"www.discordapp.com/invite/HgMTQ8w5Sj",
	}
	for _, in := range inputs {
		got := ExtractDiscordInvite(in)
		if got == "" {
			t.Fatalf("ExtractDiscordInvite(%q) found nothing", in)
		}
		if !IsDiscordInviteURL(got) {
			t.Errorf("ExtractDiscordInvite(%q) = %q, which fails IsDiscordInviteURL", in, got)
		}
	}
}

func TestNormalizeOnboardingSteps(t *testing.T) {
	t.Run("trims and drops blanks", func(t *testing.T) {
		got := NormalizeOnboardingSteps([]string{"  Join our Discord  ", "", "   ", "Read #rules"})
		want := []string{"Join our Discord", "Read #rules"}
		if len(got) != len(want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("step %d = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("caps the number of steps", func(t *testing.T) {
		many := make([]string, MaxOnboardingSteps+3)
		for i := range many {
			many[i] = "step"
		}
		if got := NormalizeOnboardingSteps(many); len(got) != MaxOnboardingSteps {
			t.Errorf("len = %d, want %d", len(got), MaxOnboardingSteps)
		}
	})

	t.Run("truncates an over-long step", func(t *testing.T) {
		got := NormalizeOnboardingSteps([]string{strings.Repeat("a", MaxOnboardingStepLength+50)})
		if len(got) != 1 {
			t.Fatalf("got %d steps, want 1", len(got))
		}
		if len(got[0]) > MaxOnboardingStepLength {
			t.Errorf("step length = %d, want <= %d", len(got[0]), MaxOnboardingStepLength)
		}
	})

	t.Run("all-blank input yields nil rather than an empty array", func(t *testing.T) {
		if got := NormalizeOnboardingSteps([]string{"", "  "}); got != nil {
			t.Errorf("got %#v, want nil", got)
		}
	})

	t.Run("nil in, nil out", func(t *testing.T) {
		if got := NormalizeOnboardingSteps(nil); got != nil {
			t.Errorf("got %#v, want nil", got)
		}
	})
}

func promotionPost(invite string, removed bool) RpPromotionPost {
	post := RpPromotionPost{Data: RpPromotionData{InviteURL: invite}}
	if removed {
		at := primitive.NewDateTimeFromTime(primitive.DateTime(0).Time())
		post.RemovedAt = &at
	}
	return post
}

func TestResolveDiscordInvite(t *testing.T) {
	tests := []struct {
		name       string
		details    CommunityDetails
		wantInvite string
		wantSource string
	}{
		{
			name:       "owner-set field wins",
			details:    CommunityDetails{DiscordInviteURL: "https://discord.gg/owner1"},
			wantInvite: "https://discord.gg/owner1",
			wantSource: DiscordInviteSourceOwner,
		},
		{
			name: "owner field beats a promotion and a description",
			details: CommunityDetails{
				DiscordInviteURL: "discord.gg/owner1",
				Description:      "join discord.gg/desc1",
				RpPromotion:      &RpPromotion{History: []RpPromotionPost{promotionPost("https://discord.gg/promo1", false)}},
			},
			wantInvite: "https://discord.gg/owner1",
			wantSource: DiscordInviteSourceOwner,
		},
		{
			name: "falls back to a promotion",
			details: CommunityDetails{
				Description: "join discord.gg/desc1",
				RpPromotion: &RpPromotion{History: []RpPromotionPost{promotionPost("https://discord.gg/promo1", false)}},
			},
			wantInvite: "https://discord.gg/promo1",
			wantSource: DiscordInviteSourcePromotion,
		},
		{
			name: "most recent promotion wins",
			details: CommunityDetails{RpPromotion: &RpPromotion{History: []RpPromotionPost{
				promotionPost("https://discord.gg/old", false),
				promotionPost("https://discord.gg/new", false),
			}}},
			wantInvite: "https://discord.gg/new",
			wantSource: DiscordInviteSourcePromotion,
		},
		{
			// Whatever got a promotion pulled from the shared Discord is not
			// something to start handing to new members.
			name: "a moderator-removed promotion is skipped",
			details: CommunityDetails{RpPromotion: &RpPromotion{History: []RpPromotionPost{
				promotionPost("https://discord.gg/good", false),
				promotionPost("https://discord.gg/removed", true),
			}}},
			wantInvite: "https://discord.gg/good",
			wantSource: DiscordInviteSourcePromotion,
		},
		{
			name: "removed promotion with no fallback resolves to nothing",
			details: CommunityDetails{RpPromotion: &RpPromotion{History: []RpPromotionPost{
				promotionPost("https://discord.gg/removed", true),
			}}},
			wantInvite: "",
			wantSource: "",
		},
		{
			name:       "falls back to the description, the 108-community case",
			details:    CommunityDetails{Description: "Serious RP. Apply at discord.gg/desc1 today"},
			wantInvite: "https://discord.gg/desc1",
			wantSource: DiscordInviteSourceDescription,
		},
		{
			name:       "nothing anywhere",
			details:    CommunityDetails{Description: "Serious RP server, no links here"},
			wantInvite: "",
			wantSource: "",
		},
		{
			// RpPromotion is a pointer and absent on roughly 94% of communities.
			// Dereferencing it blindly panicked on almost every read.
			name:       "absent rpPromotion does not panic",
			details:    CommunityDetails{RpPromotion: nil, Description: "discord.gg/desc1"},
			wantInvite: "https://discord.gg/desc1",
			wantSource: DiscordInviteSourceDescription,
		},
		{
			name:       "present but empty rpPromotion history",
			details:    CommunityDetails{RpPromotion: &RpPromotion{}, Description: "discord.gg/desc1"},
			wantInvite: "https://discord.gg/desc1",
			wantSource: DiscordInviteSourceDescription,
		},
		{
			name:       "an owner field holding junk falls through rather than being served",
			details:    CommunityDetails{DiscordInviteURL: "https://example.com/nope", Description: "discord.gg/desc1"},
			wantInvite: "https://discord.gg/desc1",
			wantSource: DiscordInviteSourceDescription,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invite, source := tt.details.ResolveDiscordInvite()
			if invite != tt.wantInvite {
				t.Errorf("invite = %q, want %q", invite, tt.wantInvite)
			}
			if source != tt.wantSource {
				t.Errorf("source = %q, want %q", source, tt.wantSource)
			}
		})
	}
}

func TestCommunityDetailsMarshalJSON(t *testing.T) {
	decode := func(t *testing.T, d CommunityDetails) map[string]interface{} {
		t.Helper()
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var out map[string]interface{}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return out
	}

	t.Run("a description invite is served as the resolved invite", func(t *testing.T) {
		out := decode(t, CommunityDetails{Name: "Rockford RP", Description: "join discord.gg/desc1"})
		if out["discordInviteUrl"] != "https://discord.gg/desc1" {
			t.Errorf("discordInviteUrl = %v, want the resolved link", out["discordInviteUrl"])
		}
		if out["discordInviteSource"] != DiscordInviteSourceDescription {
			t.Errorf("discordInviteSource = %v, want %q", out["discordInviteSource"], DiscordInviteSourceDescription)
		}
	})

	t.Run("no invite anywhere omits both keys", func(t *testing.T) {
		out := decode(t, CommunityDetails{Name: "Rockford RP", Description: "no links"})
		if _, ok := out["discordInviteUrl"]; ok {
			t.Errorf("discordInviteUrl should be omitted, got %v", out["discordInviteUrl"])
		}
		if _, ok := out["discordInviteSource"]; ok {
			t.Errorf("discordInviteSource should be omitted, got %v", out["discordInviteSource"])
		}
	})

	t.Run("owner-set link reports the owner source", func(t *testing.T) {
		out := decode(t, CommunityDetails{DiscordInviteURL: "https://discord.gg/owner1"})
		if out["discordInviteSource"] != DiscordInviteSourceOwner {
			t.Errorf("discordInviteSource = %v, want %q", out["discordInviteSource"], DiscordInviteSourceOwner)
		}
	})

	t.Run("steps are normalized on the way out", func(t *testing.T) {
		out := decode(t, CommunityDetails{OnboardingSteps: []string{"  Join Discord  ", "", "Read rules"}})
		steps, ok := out["onboardingSteps"].([]interface{})
		if !ok {
			t.Fatalf("onboardingSteps missing or wrong type: %#v", out["onboardingSteps"])
		}
		if len(steps) != 2 || steps[0] != "Join Discord" {
			t.Errorf("steps = %#v, want trimmed and blank-free", steps)
		}
	})

	// The rest of the document has to survive the hook, or every community read
	// silently loses fields.
	t.Run("other fields still serialize", func(t *testing.T) {
		out := decode(t, CommunityDetails{Name: "Rockford RP", Code: "ABC123", MembersCount: 42})
		if out["name"] != "Rockford RP" {
			t.Errorf("name = %v", out["name"])
		}
		if out["code"] != "ABC123" {
			t.Errorf("code = %v", out["code"])
		}
		if out["membersCount"].(float64) != 42 {
			t.Errorf("membersCount = %v", out["membersCount"])
		}
	})

	// A Community wraps CommunityDetails; the hook has to fire through the parent
	// and through a slice, which is how every list endpoint serializes.
	t.Run("fires through the parent struct and a slice", func(t *testing.T) {
		raw, err := json.Marshal([]Community{{Details: CommunityDetails{Description: "discord.gg/desc1"}}})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(raw), `"discordInviteUrl":"https://discord.gg/desc1"`) {
			t.Errorf("resolved invite missing from slice output: %s", raw)
		}
	})
}
