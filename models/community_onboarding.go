package models

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

// Owner-authored onboarding content, and where a community's Discord invite was
// found.
//
// Communities here are run on Discord and the CAD is supplemental: a member who
// has just requested to join needs to be sent to the community's own server for
// whatever training or application the owner requires. Until these fields
// existed there was nowhere to put that link, so owners smuggled it into the
// free-text description instead. A production survey of the 2,285 public
// communities with at least two members found 108 with an invite buried in the
// description and 225 more carrying one inside an RP promotion, none of which
// was ever shown to a prospective member.
const (
	// MaxOnboardingSteps caps how many steps an owner can publish. Five is about
	// what a 10-to-15 year old will actually read before joining.
	MaxOnboardingSteps = 5
	// MaxOnboardingStepLength caps one step. Long enough for "Join our Discord
	// and open the #start-here channel", short enough to stay a step.
	MaxOnboardingStepLength = 120
)

// Sources a Discord invite can be resolved from, reported to clients so an owner
// settings form can tell an inherited link from one the owner actually set.
const (
	DiscordInviteSourceOwner       = "owner"
	DiscordInviteSourcePromotion   = "promotion"
	DiscordInviteSourceDescription = "description"
)

// discordInvitePattern matches an invite however an owner happened to type it:
// with or without a scheme, with or without www/ptb/canary, and in either the
// discord.gg or discord.com/invite form. Validated against production, it
// extracted a code from 145 of 145 descriptions containing an invite.
var discordInvitePattern = regexp.MustCompile(
	`(?i)(?:https?://)?(?:www\.|ptb\.|canary\.)?(?:discord\.gg|discord(?:app)?\.com/invite)/([A-Za-z0-9-]+)`,
)

// IsDiscordInviteURL reports whether s is a real Discord *invite* link that
// Discord itself will resolve, rather than any discord.com URL. It requires
// https, a known invite host, and exactly one path segment of invite code.
func IsDiscordInviteURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "ptb.")
	host = strings.TrimPrefix(host, "canary.")
	path := strings.Trim(u.Path, "/")
	switch host {
	case "discord.gg":
		return path != "" && !strings.Contains(path, "/")
	case "discord.com", "discordapp.com":
		const prefix = "invite/"
		if !strings.HasPrefix(path, prefix) {
			return false
		}
		code := strings.TrimPrefix(path, prefix)
		return code != "" && !strings.Contains(code, "/")
	}
	return false
}

// ExtractDiscordInvite pulls the first Discord invite out of free text and
// returns it in canonical https://discord.gg/<code> form, or "" if there is
// none. Both invite hosts resolve the same code, so emitting one shape keeps
// every caller from having to handle three.
func ExtractDiscordInvite(text string) string {
	match := discordInvitePattern.FindStringSubmatch(text)
	if len(match) < 2 || match[1] == "" {
		return ""
	}
	return "https://discord.gg/" + match[1]
}

// NormalizeDiscordInviteURL canonicalises an invite an owner typed by hand,
// accepting the shapes people actually paste (no scheme, http, discord.com/invite)
// and returning "" for anything that is not an invite. Storing the canonical form
// means readers never have to re-parse.
func NormalizeDiscordInviteURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return ExtractDiscordInvite(s)
}

// NormalizeOnboardingSteps trims, drops blanks, truncates over-long steps and
// caps the count. Enforcing this on the model rather than in a handler means
// the create form, the settings form and the backfill cannot disagree.
func NormalizeOnboardingSteps(steps []string) []string {
	if len(steps) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(steps))
	for _, step := range steps {
		step = strings.TrimSpace(step)
		if step == "" {
			continue
		}
		if len(step) > MaxOnboardingStepLength {
			step = strings.TrimSpace(step[:MaxOnboardingStepLength])
		}
		normalized = append(normalized, step)
		if len(normalized) == MaxOnboardingSteps {
			break
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// ResolveDiscordInvite returns the community's Discord invite and the source it
// came from, preferring what the owner set explicitly.
//
// The fallback chain is what lets this ship useful on day one instead of waiting
// for 2,000 owners to fill in a new field: only 13.5% of live public communities
// have a link we can find at all, but 52% of boosted ones do, and those are the
// communities a new member is steered toward first.
func (d CommunityDetails) ResolveDiscordInvite() (invite string, source string) {
	if url := NormalizeDiscordInviteURL(d.DiscordInviteURL); url != "" {
		return url, DiscordInviteSourceOwner
	}

	// Most recent promotion first. RpPromotion is a pointer and is absent on the
	// overwhelming majority of communities, so this must not be dereferenced
	// blindly. A promotion removed by staff moderation is skipped: whatever got it
	// pulled from the shared Discord is not something to start handing to new
	// members.
	if d.RpPromotion != nil {
		for i := len(d.RpPromotion.History) - 1; i >= 0; i-- {
			post := d.RpPromotion.History[i]
			if post.RemovedAt != nil {
				continue
			}
			if url := NormalizeDiscordInviteURL(post.Data.InviteURL); url != "" {
				return url, DiscordInviteSourcePromotion
			}
		}
	}

	if url := ExtractDiscordInvite(d.Description); url != "" {
		return url, DiscordInviteSourceDescription
	}

	return "", ""
}

// MarshalJSON resolves the Discord invite on the way out so every read path
// serves the same answer.
//
// Doing this on the model rather than per-handler is the pattern that fixed the
// vehicle-flag and license splits: there is no handler left that can forget to
// call it, and the already-released mobile build picks it up without a new
// release. discordInviteSource travels alongside so an owner settings form can
// say "we found this in your description" rather than presenting an inherited
// link as one they set.
func (d CommunityDetails) MarshalJSON() ([]byte, error) {
	// A local type strips the method set, avoiding infinite recursion.
	type communityDetails CommunityDetails

	resolved := d
	invite, source := d.ResolveDiscordInvite()
	resolved.DiscordInviteURL = invite
	resolved.OnboardingSteps = NormalizeOnboardingSteps(d.OnboardingSteps)

	// Embedding the alias and adding the provenance alongside it keeps this to a
	// single marshal pass. The source is carried in the wrapper rather than on
	// CommunityDetails itself so it has no bson tag to write back: a client that
	// round-trips a community document cannot persist a derived value as though
	// the owner had set it.
	return json.Marshal(struct {
		communityDetails
		DiscordInviteSource string `json:"discordInviteSource,omitempty"`
	}{
		communityDetails:    communityDetails(resolved),
		DiscordInviteSource: source,
	})
}
