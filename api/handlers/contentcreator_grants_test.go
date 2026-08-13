package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/linesmerrill/police-cad-api/models"
)

// The Content Creator Program grants free subscriptions. The rule that keeps
// that safe is: never touch a subscription somebody is paying for. Overwriting
// subscription.id with our cc_program_ marker would detach the Stripe or
// app-store record while the billing kept running.

func TestIsSelfFundedSubscription(t *testing.T) {
	cases := []struct {
		name string
		sub  models.Subscription
		want bool
	}{
		{
			name: "active stripe subscription is self funded",
			sub:  models.Subscription{ID: "sub_1PabcXYZ", Plan: "premium_plus", Active: true, Source: "stripe"},
			want: true,
		},
		{
			name: "active app store subscription is self funded",
			sub:  models.Subscription{ID: "1000000123456789", Plan: "premium", Active: true, Source: "app_store"},
			want: true,
		},
		{
			name: "legacy subscription with no source is still self funded",
			sub:  models.Subscription{ID: "legacy-abc", Plan: "base", Active: true},
			want: true,
		},
		{
			name: "our own program grant is not self funded",
			sub:  models.Subscription{ID: ccProgramSubscriptionPrefix + "665f1a2b3c4d5e6f7a8b9c0d", Plan: "premium_plus", Active: true},
			want: false,
		},
		{
			name: "lapsed paid subscription is replaceable",
			sub:  models.Subscription{ID: "sub_1PabcXYZ", Plan: "premium", Active: false, Source: "stripe"},
			want: false,
		},
		{
			name: "empty subscription is replaceable",
			sub:  models.Subscription{},
			want: false,
		},
		{
			name: "active but id-less record is replaceable",
			sub:  models.Subscription{Plan: "base", Active: true},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isSelfFundedSubscription(tc.sub))
		})
	}
}

// The backfill classifies each existing grant before touching anything. Getting
// this wrong either detaches live billing or silently skips a creator.
func TestBackfillSubscriptionDisposition(t *testing.T) {
	// Mirrors the switch in AdminBackfillCreatorTiersHandler.
	disposition := func(sub models.Subscription) string {
		switch {
		case isSelfFundedSubscription(sub):
			return "left-alone-self-funded"
		case isProgramGrant(sub):
			return "upgraded"
		default:
			return "not-a-program-grant"
		}
	}

	cases := []struct {
		name string
		sub  models.Subscription
		want string
	}{
		{
			name: "creator paying for their own premium plus keeps it",
			sub:  models.Subscription{ID: "sub_1Pxyz", Plan: "premium_plus", Active: true, Source: "stripe"},
			want: "left-alone-self-funded",
		},
		{
			name: "existing program grant on the old base plan gets raised",
			sub:  models.Subscription{ID: ccProgramSubscriptionPrefix + "665f1a2b3c4d5e6f7a8b9c0d", Plan: "base", Active: true},
			want: "upgraded",
		},
		{
			name: "community never claimed its benefit",
			sub:  models.Subscription{},
			want: "not-a-program-grant",
		},
		{
			name: "lapsed paid subscription is not resurrected",
			sub:  models.Subscription{ID: "sub_1Pxyz", Plan: "premium", Active: false, Source: "stripe"},
			want: "not-a-program-grant",
		},
		{
			// The plan is raised but `active` is never written, so a dormant
			// grant stays dormant — it is corrected, not resurrected.
			name: "inactive program grant has its plan corrected, staying inactive",
			sub:  models.Subscription{ID: ccProgramSubscriptionPrefix + "abc", Plan: "base", Active: false},
			want: "upgraded",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, disposition(tc.sub))
		})
	}
}

func TestCCProgramPlanForTargetType(t *testing.T) {
	assert.Equal(t, ccProgramUserPlan, ccProgramPlanFor("user"))
	assert.Equal(t, ccProgramCommunityPlan, ccProgramPlanFor("community"))
	// Fail closed: an unknown target must not receive a plan at all, so the
	// backfill reports it instead of writing a nonsense value.
	assert.Equal(t, "", ccProgramPlanFor("department"))
	assert.Equal(t, "", ccProgramPlanFor(""))
}

func TestIsProgramGrant(t *testing.T) {
	assert.True(t, isProgramGrant(models.Subscription{ID: ccProgramSubscriptionPrefix + "abc"}))
	assert.False(t, isProgramGrant(models.Subscription{ID: "sub_stripe"}))
	assert.False(t, isProgramGrant(models.Subscription{}))
}

// Revocation keys off the cc_program_ marker rather than the plan name. Keying
// off the plan meant that changing what the program grants silently stranded
// every existing grant as un-revokable.
func TestCCProgramGrantFilterMatchesOnlyProgramIDs(t *testing.T) {
	filter := ccProgramGrantFilter("user.subscription.id")

	inner, ok := filter["user.subscription.id"].(bson.M)
	assert.True(t, ok, "filter must constrain the subscription id field")
	assert.Equal(t, "^"+ccProgramSubscriptionPrefix, inner["$regex"],
		"must anchor to the program prefix so a user-chosen id cannot match")
}

// The two halves of the perk use different vocabularies. A user's plan is a
// subscription tier; a community's plan is a BOOST tier. Writing a subscription
// tier into the community field yields a value the boost system does not rank.
func TestProgramGrantPlansUseTheRightVocabulary(t *testing.T) {
	assert.Equal(t, models.TierPremiumPlus, ccProgramUserPlan,
		"personal grant must be the highest user subscription tier")

	validBoostTiers := map[string]bool{"basic": true, "standard": true, "premium": true, "elite": true}
	assert.True(t, validBoostTiers[ccProgramCommunityPlan],
		"community grant must be a real boost tier, got %q", ccProgramCommunityPlan)

	assert.NotEqual(t, ccProgramUserPlan, ccProgramCommunityPlan,
		"a subscription tier key is not a valid community boost tier")
}
