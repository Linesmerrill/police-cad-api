package handlers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/linesmerrill/police-cad-api/models"
)

// Validation for the owner-authored onboarding fields.
//
// UpdateCommunityFieldHandler is a catch-all that prefixes any key it is given
// with "community." and $sets it, so without these an owner could store an
// unparseable invite and every reader downstream would have to cope with it.
// Both are validated rather than silently dropped: the owner typed something,
// and a link that quietly vanishes is worse than one that is refused.

// normalizeDiscordInvitePatch validates and canonicalises a discordInviteUrl
// value from a settings patch. An empty value is allowed and means "clear it".
func normalizeDiscordInvitePatch(raw interface{}) (string, error) {
	if raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", errors.New("discordInviteUrl must be a string")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	invite := models.NormalizeDiscordInviteURL(value)
	if invite == "" {
		return "", errors.New("discordInviteUrl must be a Discord invite link, for example https://discord.gg/your-code")
	}
	return invite, nil
}

// normalizeOnboardingStepsPatch validates an onboardingSteps value from a
// settings patch. It returns an empty slice rather than nil when the owner
// clears every step, so the $set writes an empty array instead of null.
func normalizeOnboardingStepsPatch(raw interface{}) ([]string, error) {
	if raw == nil {
		return []string{}, nil
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil, errors.New("onboardingSteps must be a list of strings")
	}
	if len(items) > models.MaxOnboardingSteps {
		return nil, fmt.Errorf("onboardingSteps allows at most %d steps", models.MaxOnboardingSteps)
	}
	steps := make([]string, 0, len(items))
	for _, item := range items {
		step, ok := item.(string)
		if !ok {
			return nil, errors.New("onboardingSteps must be a list of strings")
		}
		steps = append(steps, step)
	}
	normalized := models.NormalizeOnboardingSteps(steps)
	if normalized == nil {
		return []string{}, nil
	}
	return normalized, nil
}
