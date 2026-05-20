package models

import "strings"

// Tier identifiers. The canonical wire/db values are lowercase; helpers below
// normalize incoming strings so historical mixed-case values still resolve.
const (
	TierFree        = "free"
	TierBase        = "base"
	TierPremium     = "premium"
	TierPremiumPlus = "premium_plus"
)

// CommunityCapUnlimited is returned from CommunityCap for tiers with no cap.
// Use it as a sentinel — callers should compare with `< CommunityCapUnlimited`
// rather than checking specific large numbers.
const CommunityCapUnlimited = 1 << 30

// communityCaps is the canonical owned-community cap per plan. Mobile and web
// surfaces historically hardcoded these in their own copies; the API is now
// the source of truth, and both clients should read the effective cap from
// the admin/user endpoints rather than redefining it.
//
// Pending-deletion communities do not count against this cap — they're hidden
// from the owner and from any count returned by CommunityDatabase.Find/Count.
// If a staff member restores a pending community that would put the owner
// over their cap, the admin console surfaces the warning so they can resolve
// (transfer ownership, force-delete, etc.) before clicking restore.
var communityCaps = map[string]int{
	TierFree:        1,
	TierBase:        5,
	TierPremium:     10,
	TierPremiumPlus: CommunityCapUnlimited,
}

// PlanFromUser returns the user's effective community-creation tier. An
// inactive subscription falls back to free so a lapsed paid user can't keep
// creating communities at their old cap.
func PlanFromUser(u *User) string {
	if u == nil {
		return TierFree
	}
	plan := strings.ToLower(strings.TrimSpace(u.Details.Subscription.Plan))
	if plan == "" || !u.Details.Subscription.Active {
		return TierFree
	}
	return plan
}

// CommunityCap returns the owned-community cap for a given plan. Unknown
// plans fall back to the free cap — fail closed so a typo in a webhook
// payload can't accidentally grant unlimited communities.
func CommunityCap(plan string) int {
	if cap, ok := communityCaps[strings.ToLower(strings.TrimSpace(plan))]; ok {
		return cap
	}
	return communityCaps[TierFree]
}
