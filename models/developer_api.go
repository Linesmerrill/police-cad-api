package models

import (
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// The Developer API: community-scoped API keys that let a third party build
// against us. See docs/DEVELOPER_API.md for the design and the reasoning.
//
// The shapes here are deliberately boring. The interesting decisions are about
// what a key may touch, and those live in the scope list and the grant model
// rather than in any one struct.

// DeveloperAPIKeyPrefix is what every issued key starts with, so a leaked key
// is recognisable on sight in a log, a paste or a public repository.
const DeveloperAPIKeyPrefix = "lpc_live_"

// Developer plans. A plan decides limits and nothing else: it never widens what
// a key is allowed to reach.
const (
	DeveloperPlanFree = "free"
	DeveloperPlanPro  = "pro"
)

// Developer account status. Suspended is a staff action and stops every key the
// developer holds at once.
const (
	DeveloperAccountActive    = "active"
	DeveloperAccountSuspended = "suspended"
)

// Key and grant lifecycle. A key or grant that is not active does nothing, and
// the check happens per request, so revoking takes effect immediately rather
// than when some session ends.
const (
	DeveloperKeyPending = "pending"
	DeveloperKeyActive  = "active"
	DeveloperKeyRevoked = "revoked"
)

// How a key came to be allowed into a community. Worth recording separately:
// "an owner let this key in" and "this key made this community" carry different
// weight when someone is working out what happened.
const (
	DeveloperGrantCreatedByKey  = "created_by_key"
	DeveloperGrantOwnerApproved = "owner_approved"
)

// Game identity providers a mod can name a player by.
const (
	PlayerProviderFiveM   = "fivem"
	PlayerProviderDiscord = "discord"
	PlayerProviderSteam   = "steam"
)

// The scopes a key can hold. Small on purpose: every one of these is a thing a
// FiveM mod actually needs, and a scope that exists is a scope we have to
// defend.
//
// There is no scope for auth, money, billing, admin, roles or community
// settings, and there never will be. Those are not missing from the list; they
// are absent from the surface.
const (
	ScopeCommunityRead  = "community:read"
	ScopeUnitsRead      = "units:read"
	ScopeUnitsWrite     = "units:write"
	ScopeCallsRead      = "calls:read"
	ScopeCallsWrite     = "calls:write"
	ScopeBolosRead      = "bolos:read"
	ScopeBolosWrite     = "bolos:write"
	ScopeCiviliansRead  = "civilians:read"
	ScopeCiviliansWrite = "civilians:write"
	ScopeVehiclesRead   = "vehicles:read"
	ScopeVehiclesWrite  = "vehicles:write"
	ScopeFirearmsRead   = "firearms:read"
	ScopeFirearmsWrite  = "firearms:write"
	ScopeLicensesWrite  = "licenses:write"
	// ScopeShiftsWrite is reserved, not offered. Clocking in is coupled to the
	// economy system (it needs community.economy.enabled and the department's
	// own economy toggle), which most communities have off, so a scope for it
	// would be selectable and then do nothing for nearly everyone. Left here so
	// the string is not reused for something else.
	ScopeShiftsWrite          = "shifts:write"
	ScopePlayersProvision     = "players:provision"
	ScopeCommunitiesProvision = "communities:provision"
	ScopeMembersRead          = "members:read"
	ScopeMembersWrite         = "members:write"
)

// developerScopeDescriptions is every valid scope and what it grants, in the
// order the website shows them. It is the single source of truth: the key
// creation endpoint validates against it and the website renders from it, so a
// new scope cannot be offered in the UI without existing here.
var developerScopeDescriptions = []struct {
	Scope       string
	Description string
}{
	{ScopeCommunityRead, "Read the community's profile, departments and ten-codes"},
	{ScopeUnitsRead, "See who is on duty"},
	{ScopeUnitsWrite, "Set a member's status, ten-code and location"},
	{ScopeCallsRead, "Read calls"},
	{ScopeCallsWrite, "Create, update and close calls"},
	{ScopeBolosRead, "Read BOLOs"},
	{ScopeBolosWrite, "Create BOLOs"},
	{ScopeCiviliansRead, "Look up civilians by name"},
	{ScopeCiviliansWrite, "Create and update civilians for linked players"},
	{ScopeVehiclesRead, "Look up vehicles by plate"},
	{ScopeVehiclesWrite, "Register vehicles for linked players"},
	{ScopeFirearmsRead, "Look up firearms by serial"},
	{ScopeFirearmsWrite, "Register firearms for linked players"},
	{ScopeLicensesWrite, "Issue licenses to civilians"},
	{ScopePlayersProvision, "Create and link player accounts from game identifiers"},
	{ScopeCommunitiesProvision, "Create communities owned by the developer"},
	{ScopeMembersRead, "Read the community's member roster"},
	{ScopeMembersWrite, "Invite, approve, promote and remove members"},
}

// DeveloperScope is one offerable scope, as the website renders it.
type DeveloperScope struct {
	Scope       string `json:"scope"`
	Description string `json:"description"`
}

// DeveloperScopes returns every scope a developer can ask for.
func DeveloperScopes() []DeveloperScope {
	out := make([]DeveloperScope, 0, len(developerScopeDescriptions))
	for _, s := range developerScopeDescriptions {
		out = append(out, DeveloperScope{Scope: s.Scope, Description: s.Description})
	}
	return out
}

// IsValidDeveloperScope reports whether a scope is one we issue. Anything else
// is refused at key creation rather than silently stored, so a typo in a
// developer's request cannot become a key that mysteriously fails later.
func IsValidDeveloperScope(scope string) bool {
	for _, s := range developerScopeDescriptions {
		if s.Scope == scope {
			return true
		}
	}
	return false
}

// DeveloperLimits is one plan's allowance.
//
// Request limits protect the backend. The provisioning limits are separate and
// much lower, because those calls create rows that are awkward to undo, and a
// mod looping by mistake should not be able to make ten thousand accounts
// before anyone notices.
//
// Note what is deliberately absent: how many communities a developer may own.
// A developer account is the person's ordinary LPC account with developer mode
// switched on, so that number is already decided by their website plan
// (CommunityCap), and a second cap here would have meant a paying customer
// being refused their sixth community because a separate, unrelated tier said
// three. A community made with a key counts exactly like one made by hand.
//
// The tier governs rate: how fast requests may come, how fast rows may be
// provisioned, and how many keys one developer may hold.
type DeveloperLimits struct {
	RequestsPerMinute    int `json:"requestsPerMinute"`
	RequestsPerDay       int `json:"requestsPerDay"`
	KeysPerDeveloper     int `json:"keysPerDeveloper"`
	AccountsPerKeyDay    int `json:"accountsPerKeyDay"`
	CommunitiesPerKeyDay int `json:"communitiesPerKeyDay"`
}

var developerPlanLimits = map[string]DeveloperLimits{
	DeveloperPlanFree: {
		RequestsPerMinute:    60,
		RequestsPerDay:       10000,
		KeysPerDeveloper:     2,
		AccountsPerKeyDay:    25,
		CommunitiesPerKeyDay: 2,
	},
	DeveloperPlanPro: {
		RequestsPerMinute:    600,
		RequestsPerDay:       250000,
		KeysPerDeveloper:     10,
		AccountsPerKeyDay:    500,
		CommunitiesPerKeyDay: 10,
	},
}

// DeveloperTier is one developer tier's published allowance.
type DeveloperTier struct {
	Plan   string          `json:"plan"`
	Limits DeveloperLimits `json:"limits"`
}

// DeveloperTiers lists every developer tier and its limits, cheapest first.
//
// Exists so the developer page can render the comparison table from the same
// values the middleware enforces. A page with its own copy of these numbers
// drifts the first time one is tuned, and the people reading it are the ones
// who would then write code against the wrong figure.
func DeveloperTiers() []DeveloperTier {
	order := []string{DeveloperPlanFree, DeveloperPlanPro}
	out := make([]DeveloperTier, 0, len(order))
	for _, plan := range order {
		out = append(out, DeveloperTier{Plan: plan, Limits: LimitsForPlan(plan)})
	}
	return out
}

// LimitsForPlan returns a plan's allowance, falling back to Free for anything
// unrecognised. Falling back down rather than up matters: a plan string we
// cannot read should cost a developer throughput, never hand them more.
func LimitsForPlan(plan string) DeveloperLimits {
	if l, ok := developerPlanLimits[plan]; ok {
		return l
	}
	return developerPlanLimits[DeveloperPlanFree]
}

// DeveloperAccount is one LPC user's developer registration.
type DeveloperAccount struct {
	ID                   primitive.ObjectID  `json:"id" bson:"_id"`
	UserID               string              `json:"userId" bson:"userId"`
	Plan                 string              `json:"plan" bson:"plan"`
	PlanActive           bool                `json:"planActive" bson:"planActive"`
	StripeCustomerID     string              `json:"-" bson:"stripeCustomerId,omitempty"`
	StripeSubscriptionID string              `json:"-" bson:"stripeSubscriptionId,omitempty"`
	AppName              string              `json:"appName,omitempty" bson:"appName,omitempty"`
	AppURL               string              `json:"appUrl,omitempty" bson:"appUrl,omitempty"`
	TermsAcceptedAt      *primitive.DateTime `json:"termsAcceptedAt,omitempty" bson:"termsAcceptedAt,omitempty"`
	TermsVersion         string              `json:"termsVersion,omitempty" bson:"termsVersion,omitempty"`
	Status               string              `json:"status" bson:"status"`
	SuspendedReason      string              `json:"suspendedReason,omitempty" bson:"suspendedReason,omitempty"`
	SuspendedBy          string              `json:"-" bson:"suspendedBy,omitempty"`
	SuspendedAt          *primitive.DateTime `json:"suspendedAt,omitempty" bson:"suspendedAt,omitempty"`
	CreatedAt            primitive.DateTime  `json:"createdAt" bson:"createdAt"`
	UpdatedAt            primitive.DateTime  `json:"updatedAt" bson:"updatedAt"`
}

// EffectivePlan is the plan the developer's own subscription pays for.
//
// A plan that is not active bills as Free, so a lapsed subscription degrades
// throughput instead of locking a developer out of their own integration
// mid-session.
//
// This is the standalone subscription only. A website Premium Plus plan also
// grants Pro, and that cannot be seen from here because it lives on the user
// record — use DeveloperPlanFor, which is the answer every caller actually
// wants. This method remains because it is what "did they buy the add-on"
// means, which is a different question from "what are they entitled to".
func (d DeveloperAccount) EffectivePlan() string {
	if d.Plan == DeveloperPlanPro && d.PlanActive {
		return DeveloperPlanPro
	}
	return DeveloperPlanFree
}

// How a developer came to be on their tier.
const (
	DeveloperEntitlementNone       = "none"
	DeveloperEntitlementStandalone = "standalone"
	DeveloperEntitlementWebsite    = "website_plan"
)

// DeveloperEntitlement is the tier a developer is on, and why.
//
// The "why" is not decoration. Premium Plus includes Developer API Pro, so
// someone can be on Pro without ever having bought it, and someone else can be
// paying for both. Only a reason distinguishes "upgrade to Pro" from "you
// already have Pro" from "you are paying twice, here is how to stop".
type DeveloperEntitlement struct {
	Plan   string `json:"plan"`
	Source string `json:"source"`
	// IncludedWith names the website plan granting Pro, when one does.
	IncludedWith string `json:"includedWith,omitempty"`
	// StandaloneActive is whether their own Developer API subscription is live,
	// regardless of whether it is what is granting their tier.
	StandaloneActive bool `json:"standaloneActive"`
	// Redundant is the double-charge case: a live standalone subscription that
	// the website plan already covers. The page offers to cancel it.
	Redundant bool `json:"redundant"`
}

// DeveloperPlanFor resolves the tier to apply, given the developer's own
// subscription and the website plan on their LPC account.
//
// Premium Plus includes Developer API Pro. It is the same person and the same
// account, so charging them a second subscription for throughput they are
// already paying for would be indefensible.
//
// A lapse falls back rather than locking out: Premium Plus ending drops them to
// their standalone subscription if they have one, and to Free if they do not.
// Free is a working tier, so a lapse costs rate and nothing else.
//
// An unrecognised website plan grants nothing. Falling back rather than up is
// the only safe direction for a value that arrives from a webhook payload.
func DeveloperPlanFor(account *DeveloperAccount, websitePlan string) DeveloperEntitlement {
	standalone := account != nil && account.EffectivePlan() == DeveloperPlanPro
	includedWith := ""
	if WebsitePlanIncludesDeveloperPro(websitePlan) {
		includedWith = TierPremiumPlus
	}

	switch {
	case includedWith != "":
		return DeveloperEntitlement{
			Plan:             DeveloperPlanPro,
			Source:           DeveloperEntitlementWebsite,
			IncludedWith:     includedWith,
			StandaloneActive: standalone,
			// Paying for both. Worth saying out loud rather than quietly
			// taking the money.
			Redundant: standalone,
		}
	case standalone:
		return DeveloperEntitlement{
			Plan:             DeveloperPlanPro,
			Source:           DeveloperEntitlementStandalone,
			StandaloneActive: true,
		}
	default:
		return DeveloperEntitlement{
			Plan:   DeveloperPlanFree,
			Source: DeveloperEntitlementNone,
		}
	}
}

// WebsitePlanIncludesDeveloperPro reports whether a website plan comes with
// Developer API Pro.
//
// One function so the gate, the page and the billing copy cannot disagree about
// which plans are included.
func WebsitePlanIncludesDeveloperPro(websitePlan string) bool {
	return strings.ToLower(strings.TrimSpace(websitePlan)) == TierPremiumPlus
}

// DeveloperAPIKey is one issued key.
//
// KeyHash is a SHA-256 of the full key and the key itself is never stored, so a
// database dump does not hand anyone a working credential. SHA-256 rather than
// bcrypt is deliberate: the key is 32 bytes of crypto/rand, so there is no
// dictionary to attack, and it is verified on every single request, where
// bcrypt's cost would become the request budget.
type DeveloperAPIKey struct {
	ID               primitive.ObjectID  `json:"id" bson:"_id"`
	DeveloperID      string              `json:"developerId" bson:"developerId"`
	UserID           string              `json:"userId" bson:"userId"`
	Name             string              `json:"name" bson:"name"`
	KeyHash          string              `json:"-" bson:"keyHash"`
	DisplayPrefix    string              `json:"displayPrefix" bson:"displayPrefix"`
	LastFour         string              `json:"lastFour" bson:"lastFour"`
	Scopes           []string            `json:"scopes" bson:"scopes"`
	Status           string              `json:"status" bson:"status"`
	RotatedFromKeyID string              `json:"rotatedFromKeyId,omitempty" bson:"rotatedFromKeyId,omitempty"`
	LastUsedAt       *primitive.DateTime `json:"lastUsedAt,omitempty" bson:"lastUsedAt,omitempty"`
	RevokedBy        string              `json:"revokedBy,omitempty" bson:"revokedBy,omitempty"`
	RevokedAt        *primitive.DateTime `json:"revokedAt,omitempty" bson:"revokedAt,omitempty"`
	RevokeReason     string              `json:"revokeReason,omitempty" bson:"revokeReason,omitempty"`
	CreatedAt        primitive.DateTime  `json:"createdAt" bson:"createdAt"`
	UpdatedAt        primitive.DateTime  `json:"updatedAt" bson:"updatedAt"`
}

// HasScope reports whether this key may do something needing the given scope.
func (k DeveloperAPIKey) HasScope(scope string) bool {
	for _, s := range k.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// DeveloperCommunityGrant ties one key to one community.
//
// Its own collection, rather than a list on the key, so the two sides stay
// independent: a community owner can withdraw their grant without touching the
// developer's key, and a developer can delete a key without asking the
// community. Both of them are looking at the same rows.
type DeveloperCommunityGrant struct {
	ID           primitive.ObjectID  `json:"id" bson:"_id"`
	KeyID        string              `json:"keyId" bson:"keyId"`
	DeveloperID  string              `json:"developerId" bson:"developerId"`
	CommunityID  string              `json:"communityId" bson:"communityId"`
	Source       string              `json:"source" bson:"source"`
	Status       string              `json:"status" bson:"status"`
	RequestedAt  primitive.DateTime  `json:"requestedAt" bson:"requestedAt"`
	ApprovedBy   string              `json:"approvedBy,omitempty" bson:"approvedBy,omitempty"`
	ApprovedAt   *primitive.DateTime `json:"approvedAt,omitempty" bson:"approvedAt,omitempty"`
	RevokedBy    string              `json:"revokedBy,omitempty" bson:"revokedBy,omitempty"`
	RevokedAt    *primitive.DateTime `json:"revokedAt,omitempty" bson:"revokedAt,omitempty"`
	RevokeReason string              `json:"revokeReason,omitempty" bson:"revokeReason,omitempty"`
}

// DeveloperAPIUsage is one key's activity for one day.
//
// This document is both the dashboard's source and the daily cap counter, so
// there is one number rather than two that can disagree. Its _id is
// "<keyId>:<YYYY-MM-DD>", which makes every write an _id-targeted upsert: the
// cheapest write MongoDB offers, and the reason this works without Redis.
//
// Counts only. Request bodies are never stored.
type DeveloperAPIUsage struct {
	ID                 string             `json:"id" bson:"_id"`
	KeyID              string             `json:"keyId" bson:"keyId"`
	DeveloperID        string             `json:"developerId" bson:"developerId"`
	Day                string             `json:"day" bson:"day"`
	Requests           int                `json:"requests" bson:"requests"`
	Allowed            int                `json:"allowed" bson:"allowed"`
	Rejected429        int                `json:"rejected429" bson:"rejected429"`
	Rejected401        int                `json:"rejected401" bson:"rejected401"`
	Rejected403        int                `json:"rejected403" bson:"rejected403"`
	AccountsCreated    int                `json:"accountsCreated" bson:"accountsCreated"`
	CommunitiesCreated int                `json:"communitiesCreated" bson:"communitiesCreated"`
	ByRoute            map[string]int     `json:"byRoute,omitempty" bson:"byRoute,omitempty"`
	LastRequestAt      primitive.DateTime `json:"lastRequestAt" bson:"lastRequestAt"`
	ExpiresAt          primitive.DateTime `json:"-" bson:"expiresAt"`
}

// UsageDocID builds the deterministic _id for a key's daily usage document.
func UsageDocID(keyID string, day time.Time) string {
	return keyID + ":" + day.UTC().Format("2006-01-02")
}

// RateDocID builds the deterministic _id for a key's per-minute burst counter.
func RateDocID(keyID string, at time.Time) string {
	return keyID + ":" + strconvItoa(at.UTC().Unix()/60)
}

// strconvItoa keeps this file free of a strconv import for one call.
func strconvItoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// PlayerIdentity maps a game identifier to an LPC user.
//
// Unique on (provider, identifier). That index is what makes the upsert
// idempotent, and it is also how two mods racing on the same player resolve:
// one insert wins, the other gets a duplicate-key error and re-reads the
// winner's row, so both end up with the same user.
type PlayerIdentity struct {
	ID                   primitive.ObjectID `json:"id" bson:"_id"`
	Provider             string             `json:"provider" bson:"provider"`
	Identifier           string             `json:"identifier" bson:"identifier"`
	UserID               string             `json:"userId" bson:"userId"`
	CreatedByKeyID       string             `json:"createdByKeyId,omitempty" bson:"createdByKeyId,omitempty"`
	CreatedByDeveloperID string             `json:"createdByDeveloperId,omitempty" bson:"createdByDeveloperId,omitempty"`
	Claimed              bool               `json:"claimed" bson:"claimed"`
	CreatedAt            primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt            primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// IsValidPlayerProvider reports whether a provider is one we map identities for.
func IsValidPlayerProvider(provider string) bool {
	switch provider {
	case PlayerProviderFiveM, PlayerProviderDiscord, PlayerProviderSteam:
		return true
	}
	return false
}

// NormalizePlayerIdentifier canonicalizes a game identifier so the same player
// is one row however the mod spells them.
//
// FiveM hands mods identifiers as "license:abc123", and different mods pass
// either form, so the prefix is stripped. Everything is lowercased and trimmed.
// This runs on both write and lookup, so it cannot drift between them.
func NormalizePlayerIdentifier(provider, identifier string) string {
	v := strings.ToLower(strings.TrimSpace(identifier))
	switch provider {
	case PlayerProviderFiveM:
		v = strings.TrimPrefix(v, "license2:")
		v = strings.TrimPrefix(v, "license:")
	case PlayerProviderDiscord:
		v = strings.TrimPrefix(v, "discord:")
	case PlayerProviderSteam:
		v = strings.TrimPrefix(v, "steam:")
	}
	return v
}

// DeveloperClaimCode is a one-time code shown to a player in-game so they can
// take ownership of the account a mod made for them.
//
// Only the hash is stored. The code appears on a screen other players may be
// able to see, so it is short-lived and single-use rather than long.
type DeveloperClaimCode struct {
	ID           primitive.ObjectID  `json:"id" bson:"_id"`
	CodeHash     string              `json:"-" bson:"codeHash"`
	UserID       string              `json:"userId" bson:"userId"`
	KeyID        string              `json:"keyId" bson:"keyId"`
	DeveloperID  string              `json:"developerId" bson:"developerId"`
	CommunityID  string              `json:"communityId,omitempty" bson:"communityId,omitempty"`
	Provider     string              `json:"provider" bson:"provider"`
	Identifier   string              `json:"identifier" bson:"identifier"`
	ExpiresAt    primitive.DateTime  `json:"expiresAt" bson:"expiresAt"`
	UsedAt       *primitive.DateTime `json:"usedAt,omitempty" bson:"usedAt,omitempty"`
	UsedByUserID string              `json:"usedByUserId,omitempty" bson:"usedByUserId,omitempty"`
	CreatedAt    primitive.DateTime  `json:"createdAt" bson:"createdAt"`
}

// RecordSource marks a record that a developer key created, so a misbehaving
// key's entire output can be found and removed together.
//
// Embedded on the records a key can create rather than kept in a side table:
// the whole point is to be able to find these rows from the record itself,
// including when the key and the developer are already gone.
type RecordSource struct {
	Type        string             `json:"type" bson:"type"`
	KeyID       string             `json:"keyId" bson:"keyId"`
	DeveloperID string             `json:"developerId" bson:"developerId"`
	Provider    string             `json:"provider,omitempty" bson:"provider,omitempty"`
	Identifier  string             `json:"identifier,omitempty" bson:"identifier,omitempty"`
	CreatedAt   primitive.DateTime `json:"createdAt" bson:"createdAt"`
}

// RecordSourceDeveloperAPI is the only Type we write today. It exists as a
// constant so a query for "everything a key made" has one spelling.
const RecordSourceDeveloperAPI = "developer_api"
