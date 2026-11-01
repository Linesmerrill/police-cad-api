package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// How many communities somebody may own is their website plan's business.
//
// A developer account is their ordinary LPC account with developer mode on, so
// there is one number, from one place, whether a community was made by hand on
// the website or by a key. The developer tier governs rate: requests per
// minute and per day, provisioning pace, and keys per developer.

// limitsCommunityDB counts and inserts communities, which the cap check needs
// and the shared fake does not do.
type limitsCommunityDB struct {
	databases.CommunityDatabase
	mu      sync.Mutex
	owned   map[string]int
	inserts int
}

func (f *limitsCommunityDB) CountDocuments(_ context.Context, filter interface{}, _ ...*options.CountOptions) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := filter.(bson.M)
	owner, _ := devFilterString(m["community.ownerID"])
	return int64(f.owned[owner]), nil
}

func (f *limitsCommunityDB) InsertOne(_ context.Context, community models.Community, _ ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inserts++
	f.owned[community.Details.OwnerID]++
	return fakeInsertResult{id: community.ID}, nil
}

func (f *limitsCommunityDB) FindOneIncludingPending(_ context.Context, _ interface{}) (*models.Community, error) {
	return nil, mongo.ErrNoDocuments
}

// limitsGrantDB records the grants a provisioned community produces. The shared
// grant fake does not insert, and the create path writes one per community.
type limitsGrantDB struct {
	databases.DeveloperGrantDatabase
	mu     sync.Mutex
	grants []models.DeveloperCommunityGrant
}

func (f *limitsGrantDB) InsertOne(_ context.Context, grant models.DeveloperCommunityGrant) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grants = append(f.grants, grant)
	return fakeInsertResult{id: grant.ID}, nil
}

func (f *limitsGrantDB) CountDocuments(_ context.Context, _ interface{}, _ ...*options.CountOptions) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.grants)), nil
}

func (f *limitsGrantDB) FindOne(_ context.Context, _ interface{}) (*models.DeveloperCommunityGrant, error) {
	return nil, mongo.ErrNoDocuments
}

// setKeyOwner points every key in a fake at a given LPC user, which is how the
// cap is found: from the owner's record, never from anything the key sent.
func setKeyOwner(byHash map[string]models.DeveloperAPIKey, userID string) map[string]models.DeveloperAPIKey {
	for hash, key := range byHash {
		key.UserID = userID
		byHash[hash] = key
	}
	return byHash
}

// limitsAccountDB resolves a developer account by userId, which is the lookup
// the account endpoint makes; the shared fake only answers by _id.
type limitsAccountDB struct {
	databases.DeveloperAccountDatabase
	account *models.DeveloperAccount
}

func (f *limitsAccountDB) FindOne(_ context.Context, filter interface{}) (*models.DeveloperAccount, error) {
	if f.account == nil {
		return nil, mongo.ErrNoDocuments
	}
	m, _ := filter.(bson.M)
	if want, ok := devFilterString(m["userId"]); ok && want != f.account.UserID {
		return nil, mongo.ErrNoDocuments
	}
	return f.account, nil
}

// limitsFixture is a key with communities:provision, whose owner is on a plan
// the test chooses.
type limitsFixture struct {
	*devFixture
	userDB *fakeUserDB
	commDB *limitsCommunityDB
	grants *limitsGrantDB
	owner  models.User
}

func newLimitsFixture(t *testing.T, plan string, alreadyOwned int) *limitsFixture {
	t.Helper()
	base := newDevFixture(t, models.ScopeCommunitiesProvision)

	owner := models.User{
		ID:      primitive.NewObjectID().Hex(),
		Details: models.UserDetails{Username: "dev-owner", Email: "dev@example.com"},
	}
	if plan != models.TierFree {
		owner.Details.Subscription.Plan = plan
		owner.Details.Subscription.Active = true
	}

	f := &limitsFixture{
		devFixture: base,
		userDB:     &fakeUserDB{users: map[string]models.User{owner.ID: owner}},
		commDB:     &limitsCommunityDB{owned: map[string]int{owner.ID: alreadyOwned}},
		grants:     &limitsGrantDB{},
		owner:      owner,
	}
	base.api.UDB = f.userDB
	base.api.CDB = f.commDB
	base.api.GrantDB = f.grants
	// The key belongs to this user, which is how the cap is found: from the
	// owner's record, never from anything the key sent.
	base.keyDB.byHash = setKeyOwner(base.keyDB.byHash, owner.ID)
	return f
}

// create runs one POST /communities call.
func (f *limitsFixture) create(t *testing.T, name string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"name":"` + name + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dev/communities", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.plaintext)
	rr := httptest.NewRecorder()
	f.api.RequireDeveloperAPIKey(
		f.api.RequireScope(models.ScopeCommunitiesProvision, f.api.DevCreateCommunityHandler),
	).ServeHTTP(rr, req)
	return rr
}

func errorCode(rr *httptest.ResponseRecorder) string {
	var body map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if s, ok := body["error"].(string); ok {
		return s
	}
	return ""
}

// ---- the tier no longer has an opinion ----

func TestDeveloperLimits_DoNotCapTotalCommunities(t *testing.T) {
	// The tier describes rate, and nothing else. If a "communities in total"
	// allowance ever reappears here, it will contradict the website plan.
	for _, plan := range []string{models.TierFree, models.TierBase, models.TierPremium, models.TierPremiumPlus} {
		limits := models.LimitsForPlan(plan)
		if limits.RequestsPerMinute <= 0 || limits.RequestsPerDay <= 0 {
			t.Errorf("%s: expected request limits, got %+v", plan, limits)
		}
		if limits.CommunitiesPerKeyDay <= 0 {
			t.Errorf("%s: expected a provisioning pace, got %+v", plan, limits)
		}
		if limits.KeysPerDeveloper <= 0 {
			t.Errorf("%s: expected a key allowance, got %+v", plan, limits)
		}
	}

	// And the shape itself carries no total, so no handler can start reading one.
	raw, err := json.Marshal(models.LimitsForPlan(models.DeveloperPlanFree))
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if strings.Contains(string(raw), "communitiesPerKey\"") {
		t.Errorf("the tier must not publish a total-communities cap: %s", raw)
	}
}

func TestDevCreateCommunity_PastTheOldTierCapOnAPaidPlan(t *testing.T) {
	// Premium allows 10 communities. An earlier design also capped the total
	// per developer tier at 3, which would have refused the fourth even though
	// the customer had paid for ten. There is one cap now, and this is it.
	f := newLimitsFixture(t, models.TierPremium, 3)

	for i, name := range []string{"Fourth Community", "Fifth Community"} {
		rr := f.create(t, name)
		if rr.Code != http.StatusCreated {
			t.Fatalf("community %d: expected 201, got %d (%s): %s",
				i+4, rr.Code, errorCode(rr), rr.Body.String())
		}
	}
	if f.commDB.inserts != 2 {
		t.Errorf("expected two communities created, got %d", f.commDB.inserts)
	}
}

func TestDevCreateCommunity_StillStopsAtTheWebsitePlanCap(t *testing.T) {
	// Free allows 1, and they already have it. Dropping the tier cap did not
	// drop the real one.
	f := newLimitsFixture(t, models.TierFree, 1)

	rr := f.create(t, "One Too Many")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
	if code := errorCode(rr); code != "owner_community_limit_reached" {
		t.Errorf("error code: got %q want owner_community_limit_reached", code)
	}
	if f.commDB.inserts != 0 {
		t.Error("nothing should have been created")
	}
	// The message has to point at the right lever, because the two numbers are
	// easy to confuse and only one of them is the developer's to change here.
	if !strings.Contains(rr.Body.String(), "plan") {
		t.Errorf("the refusal should name the plan: %s", rr.Body.String())
	}
}

func TestDevCreateCommunity_UnlimitedPlanIsNotCapped(t *testing.T) {
	f := newLimitsFixture(t, models.TierPremiumPlus, 40)

	if rr := f.create(t, "Forty First"); rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 on an unlimited plan, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDevCreateCommunity_DailyPaceStillApplies(t *testing.T) {
	// Pace and total are different limits, and this is the one the tier owns.
	//
	// Base deliberately: it allows 5 communities in total at 3 a day, so the
	// pace is what refuses the fourth. On Free the total of 1 would bite first
	// and the test would pass for the wrong reason.
	f := newLimitsFixture(t, models.TierBase, 0)

	for i := 0; i < 3; i++ {
		if rr := f.create(t, "Community"); rr.Code != http.StatusCreated {
			t.Fatalf("community %d: expected 201, got %d: %s", i+1, rr.Code, rr.Body.String())
		}
	}
	rr := f.create(t, "Fourth Today")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on the fourth today, got %d: %s", rr.Code, rr.Body.String())
	}
	if code := errorCode(rr); code != "provision_cap_reached" {
		t.Errorf("error code: got %q want provision_cap_reached", code)
	}
}

// ---- what the developer page is told ----

// A higher plan raises the provisioning pace as well as the request rate: the
// tier is the whole allowance, not just its rate limits.
func TestDevCreateCommunity_AHigherPlanGetsAHigherPace(t *testing.T) {
	f := newLimitsFixture(t, models.TierPremiumPlus, 0)

	// Four in a day, which the Free pace of two and the Base pace of three
	// would both have refused.
	for i := 0; i < 4; i++ {
		if rr := f.create(t, "Community"); rr.Code != http.StatusCreated {
			t.Fatalf("community %d: expected 201 on the Premium Plus pace, got %d: %s",
				i+1, rr.Code, rr.Body.String())
		}
	}
}

func TestDeveloperAccount_ReportsTheWebsitePlanAllowance(t *testing.T) {
	f := newLimitsFixture(t, models.TierPremium, 2)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/account?userId="+f.owner.ID, nil)
	rr := httptest.NewRecorder()
	f.api.GetDeveloperAccountHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var out developerAccountResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if out.Communities.Plan != models.TierPremium {
		t.Errorf("plan: got %q want %q", out.Communities.Plan, models.TierPremium)
	}
	if out.Communities.Owned != 2 {
		t.Errorf("owned: got %d want 2", out.Communities.Owned)
	}
	if out.Communities.Cap != 10 {
		t.Errorf("cap: got %d want 10", out.Communities.Cap)
	}
	if out.Communities.Unlimited {
		t.Error("premium is capped, not unlimited")
	}
	// The page says where the number comes from, so it does not have to guess.
	if out.Communities.Source != "website_plan" {
		t.Errorf("source: got %q", out.Communities.Source)
	}
}

func TestDeveloperAccount_MarksAnUnlimitedPlan(t *testing.T) {
	f := newLimitsFixture(t, models.TierPremiumPlus, 37)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/account?userId="+f.owner.ID, nil)
	rr := httptest.NewRecorder()
	f.api.GetDeveloperAccountHandler(rr, req)

	var out developerAccountResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if !out.Communities.Unlimited {
		t.Error("premium plus has no cap and must say so rather than showing a sentinel")
	}
	if out.Communities.Owned != 37 {
		t.Errorf("owned: got %d want 37", out.Communities.Owned)
	}
}

func TestDeveloperAccount_AllowanceSurvivesAMissingUser(t *testing.T) {
	// The allowance is a nicety on a page; it must not take the page down.
	f := newLimitsFixture(t, models.TierFree, 0)
	missing := primitive.NewObjectID().Hex()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/account?userId="+missing, nil)
	rr := httptest.NewRecorder()
	f.api.GetDeveloperAccountHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---- terms re-acceptance ----

func TestDeveloperAccount_AsksForReacceptanceOnStaleTerms(t *testing.T) {
	f := newLimitsFixture(t, models.TierFree, 0)

	for name, accepted := range map[string]string{
		"an older version":  "2026-10-31",
		"no version at all": "",
	} {
		t.Run(name, func(t *testing.T) {
			f.api.AccountDB = &limitsAccountDB{account: &models.DeveloperAccount{
				ID:           primitive.NewObjectID(),
				UserID:       f.owner.ID,
				Status:       models.DeveloperAccountActive,
				TermsVersion: accepted,
			}}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/account?userId="+f.owner.ID, nil)
			rr := httptest.NewRecorder()
			f.api.GetDeveloperAccountHandler(rr, req)

			var out developerAccountResponse
			_ = json.Unmarshal(rr.Body.Bytes(), &out)
			if !out.TermsReacceptRequired {
				t.Errorf("%s should require re-acceptance", name)
			}
			if out.TermsVersion != developerTermsVersion {
				t.Errorf("the current version should be published, got %q", out.TermsVersion)
			}
		})
	}
}

func TestDeveloperAccount_DoesNotNagOnCurrentTerms(t *testing.T) {
	f := newLimitsFixture(t, models.TierFree, 0)
	f.api.AccountDB = &limitsAccountDB{account: &models.DeveloperAccount{
		ID:           primitive.NewObjectID(),
		UserID:       f.owner.ID,
		Status:       models.DeveloperAccountActive,
		TermsVersion: developerTermsVersion,
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/account?userId="+f.owner.ID, nil)
	rr := httptest.NewRecorder()
	f.api.GetDeveloperAccountHandler(rr, req)

	var out developerAccountResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.TermsReacceptRequired {
		t.Error("a developer on the current terms must not be prompted")
	}
}

// ---- the published comparison table ----

func TestDeveloperAccount_PublishesTheWebsitePlanCaps(t *testing.T) {
	f := newLimitsFixture(t, models.TierFree, 0)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/account?userId="+f.owner.ID, nil)
	rr := httptest.NewRecorder()
	f.api.GetDeveloperAccountHandler(rr, req)

	var out developerAccountResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if len(out.CommunityPlans) != 4 {
		t.Fatalf("expected four website plans, got %+v", out.CommunityPlans)
	}
	for _, plan := range out.CommunityPlans {
		enforced := models.CommunityCap(plan.Plan)
		if plan.Cap != enforced {
			t.Errorf("%s: published cap %d but %d is enforced", plan.Plan, plan.Cap, enforced)
		}
		if plan.Unlimited != (enforced >= models.CommunityCapUnlimited) {
			t.Errorf("%s: unlimited flag disagrees with the cap", plan.Plan)
		}
	}
	// The sentinel must never be rendered as a number, so the flag has to be
	// the thing the page keys on.
	last := out.CommunityPlans[len(out.CommunityPlans)-1]
	if !last.Unlimited {
		t.Errorf("expected the top plan to be unlimited, got %+v", last)
	}
}

// ---- which website plan grants which tier ----

// The website subscription is the only input. There is no developer
// subscription to buy, so this table is the whole of the policy.
func TestDeveloperEntitlement_EachWebsitePlanMapsToItsLimits(t *testing.T) {
	for _, tc := range []struct {
		plan                 string
		requestsPerMinute    int
		requestsPerDay       int
		keysPerDeveloper     int
		accountsPerKeyDay    int
		communitiesPerKeyDay int
	}{
		{models.TierFree, 60, 10000, 2, 25, 2},
		{models.TierBase, 120, 25000, 3, 100, 3},
		{models.TierPremium, 300, 100000, 5, 250, 5},
		{models.TierPremiumPlus, 600, 250000, 10, 500, 10},
	} {
		t.Run(tc.plan, func(t *testing.T) {
			user := &models.User{}
			if tc.plan != models.TierFree {
				user.Details.Subscription.Plan = tc.plan
				user.Details.Subscription.Active = true
			}

			got := models.DeveloperPlanFor(user)
			if got.Plan != tc.plan {
				t.Fatalf("plan: got %q want %q", got.Plan, tc.plan)
			}
			if got.Source != models.DeveloperEntitlementWebsite {
				t.Errorf("source: got %q", got.Source)
			}
			if got.Lapsed {
				t.Error("an active subscription is not lapsed")
			}

			limits := models.LimitsForPlan(got.Plan)
			if limits.RequestsPerMinute != tc.requestsPerMinute {
				t.Errorf("requests/min: got %d want %d", limits.RequestsPerMinute, tc.requestsPerMinute)
			}
			if limits.RequestsPerDay != tc.requestsPerDay {
				t.Errorf("requests/day: got %d want %d", limits.RequestsPerDay, tc.requestsPerDay)
			}
			if limits.KeysPerDeveloper != tc.keysPerDeveloper {
				t.Errorf("keys: got %d want %d", limits.KeysPerDeveloper, tc.keysPerDeveloper)
			}
			if limits.AccountsPerKeyDay != tc.accountsPerKeyDay {
				t.Errorf("accounts/day: got %d want %d", limits.AccountsPerKeyDay, tc.accountsPerKeyDay)
			}
			if limits.CommunitiesPerKeyDay != tc.communitiesPerKeyDay {
				t.Errorf("communities/day: got %d want %d", limits.CommunitiesPerKeyDay, tc.communitiesPerKeyDay)
			}
		})
	}
}

func TestDeveloperEntitlement_LapseFallsBackToFree(t *testing.T) {
	// Free is a working tier, so a lapse costs rate and nothing else. It must
	// never take a developer's integration away mid-session.
	for _, plan := range []string{models.TierBase, models.TierPremium, models.TierPremiumPlus} {
		t.Run(plan, func(t *testing.T) {
			user := &models.User{}
			user.Details.Subscription.Plan = plan
			user.Details.Subscription.Active = false

			got := models.DeveloperPlanFor(user)
			if got.Plan != models.DeveloperPlanFree {
				t.Fatalf("a lapsed %s must bill as Free, got %q", plan, got.Plan)
			}
			// The page needs to say "your subscription lapsed" rather than
			// silently showing Free and leaving someone to work out why.
			if !got.Lapsed {
				t.Error("a lapsed paid plan should be marked lapsed")
			}
			if got.WebsitePlan != plan {
				t.Errorf("the plan they had should still be reported, got %q", got.WebsitePlan)
			}
		})
	}
}

func TestDeveloperEntitlement_UnknownPlanFallsBackToFree(t *testing.T) {
	// Falling back rather than up is the only safe direction for a value that
	// arrives from a webhook payload.
	for _, plan := range []string{"", "  ", "platinum", "pro", "premium_plus_plus", "$ne"} {
		user := &models.User{}
		user.Details.Subscription.Plan = plan
		user.Details.Subscription.Active = true

		got := models.DeveloperPlanFor(user)
		if got.Plan != models.DeveloperPlanFree {
			t.Errorf("plan %q granted %q; unknown plans must fall back to Free", plan, got.Plan)
		}
		if got.Lapsed {
			t.Errorf("plan %q is unknown, not lapsed", plan)
		}
	}
}

func TestDeveloperEntitlement_NilUserIsFree(t *testing.T) {
	// The middleware passes nil when the owner read fails. That decides
	// throughput, not access, so it must resolve rather than error.
	if got := models.DeveloperPlanFor(nil); got.Plan != models.DeveloperPlanFree {
		t.Errorf("no user means Free, got %q", got.Plan)
	}
}

func TestDeveloperEntitlement_PlanCaseAndPaddingResolve(t *testing.T) {
	user := &models.User{}
	user.Details.Subscription.Plan = "  Premium_Plus  "
	user.Details.Subscription.Active = true

	if got := models.DeveloperPlanFor(user); got.Plan != models.TierPremiumPlus {
		t.Errorf("a padded, mixed-case plan should still resolve, got %q", got.Plan)
	}
}

// ---- the published comparison ----

func TestDeveloperTiers_CoverEveryWebsitePlan(t *testing.T) {
	tiers := models.DeveloperTiers()
	if len(tiers) != 4 {
		t.Fatalf("expected one tier per website plan, got %+v", tiers)
	}
	want := []string{models.TierFree, models.TierBase, models.TierPremium, models.TierPremiumPlus}
	for i, tier := range tiers {
		if tier.Plan != want[i] {
			t.Errorf("index %d: got %q want %q (cheapest first)", i, tier.Plan, want[i])
		}
		if tier.Limits != models.LimitsForPlan(tier.Plan) {
			t.Errorf("%s: published %+v but %+v is enforced",
				tier.Plan, tier.Limits, models.LimitsForPlan(tier.Plan))
		}
	}
}

// The page shows prices beside the limits. They come from the same list
// /pricing renders, because price cohorts mean a hardcoded figure is not merely
// stale — it can be wrong for everyone.
func TestDeveloperAccount_PublishesTheSameTiersAsPricing(t *testing.T) {
	f := newLimitsFixture(t, models.TierPremium, 0)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/account?userId="+f.owner.ID, nil)
	rr := httptest.NewRecorder()
	f.api.GetDeveloperAccountHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var out developerAccountResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	canonical := SubscriptionTiers()
	if len(out.WebsiteTiers) != len(canonical) {
		t.Fatalf("expected the canonical tier list, got %d of %d", len(out.WebsiteTiers), len(canonical))
	}
	for i := range canonical {
		if out.WebsiteTiers[i].Key != canonical[i].Key ||
			out.WebsiteTiers[i].MonthlyPrice != canonical[i].MonthlyPrice ||
			out.WebsiteTiers[i].AnnualPrice != canonical[i].AnnualPrice {
			t.Errorf("tier %d drifted from /pricing: %+v vs %+v",
				i, out.WebsiteTiers[i], canonical[i])
		}
	}
	if out.UpgradeURL == "" {
		t.Error("the page needs somewhere to send someone to upgrade")
	}
	// And this developer's own tier follows their plan.
	if out.Entitlement.Plan != models.TierPremium {
		t.Errorf("entitlement: got %q want premium", out.Entitlement.Plan)
	}
	if out.Limits != models.LimitsForPlan(models.TierPremium) {
		t.Error("the limits shown must be the ones enforced for their plan")
	}
}

// A price cohort change has to move the developer page too, not just /pricing.
func TestDeveloperAccount_FollowsThePriceDrop(t *testing.T) {
	t.Setenv("KICKBACK_PRICE_DROP_LIVE", "true")
	f := newLimitsFixture(t, models.TierFree, 0)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/account?userId="+f.owner.ID, nil)
	rr := httptest.NewRecorder()
	f.api.GetDeveloperAccountHandler(rr, req)

	var out developerAccountResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	var plus *SubscriptionTier
	for i := range out.WebsiteTiers {
		if out.WebsiteTiers[i].Key == models.TierPremiumPlus {
			plus = &out.WebsiteTiers[i]
		}
	}
	if plus == nil {
		t.Fatal("expected a Premium Plus tier")
	}
	if !plus.OnSale || plus.MonthlyPrice != 9.99 {
		t.Errorf("the developer page must follow the live cohort price, got %+v", *plus)
	}
}

// The upgrade link has to point at the page that sells the subscription API
// limits actually follow. It shipped pointing at /community-pricing, which
// sells community promotions — a different product — so developers clicking
// "raise your limits" were sent to buy something that would not raise them.
func TestDeveloperAccount_UpgradeLinkPointsAtUserPlans(t *testing.T) {
	f := newLimitsFixture(t, models.TierFree, 0)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/account?userId="+f.owner.ID, nil)
	rr := httptest.NewRecorder()
	f.api.GetDeveloperAccountHandler(rr, req)

	var out developerAccountResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if out.UpgradeURL != "/pricing" {
		t.Errorf("upgrade url: got %q want /pricing", out.UpgradeURL)
	}
	if strings.Contains(out.UpgradeURL, "community-pricing") {
		t.Error("community-pricing sells promotions, not the plan that sets API limits")
	}
}

func TestDeveloperAccount_UpgradeLinkIsOverridable(t *testing.T) {
	// So the destination can move without a deploy.
	t.Setenv("DEVELOPER_UPGRADE_URL", "https://example.com/plans")
	f := newLimitsFixture(t, models.TierFree, 0)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/account?userId="+f.owner.ID, nil)
	rr := httptest.NewRecorder()
	f.api.GetDeveloperAccountHandler(rr, req)

	var out developerAccountResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.UpgradeURL != "https://example.com/plans" {
		t.Errorf("config should win, got %q", out.UpgradeURL)
	}
}

// ---- the community picker ----

// The key form used to ask for a raw community id. The picker needs names, and
// it needs to know which communities this person could approve a key into
// themselves, so it can say so before they create a key that silently waits.
func TestListDeveloperCommunities_SortsGrantableFirstAndNamesThem(t *testing.T) {
	f := newLimitsFixture(t, models.TierPremium, 0)

	owned := primitive.NewObjectID()
	adminOf := primitive.NewObjectID()
	memberOf := primitive.NewObjectID()
	other := primitive.NewObjectID()

	user := f.owner
	user.Details.Communities = []models.UserCommunity{
		{CommunityID: memberOf.Hex(), Status: "active"},
		{CommunityID: owned.Hex(), Status: "active"},
		{CommunityID: adminOf.Hex(), Status: "active"},
	}
	f.userDB.users[user.ID] = user

	build := func(id primitive.ObjectID, name, ownerID string, adminMembers []string) models.Community {
		c := models.Community{ID: id}
		c.Details.Name = name
		c.Details.OwnerID = ownerID
		if len(adminMembers) > 0 {
			c.Details.Roles = []models.Role{{
				Name:        "Staff",
				Members:     adminMembers,
				Permissions: []models.Permission{{Name: "administrator", Enabled: true}},
			}}
		}
		return c
	}
	f.api.CDB = &pickerCommunityDB{communities: []models.Community{
		build(memberOf, "Zebra RP", other.Hex(), nil),
		build(owned, "My Server", user.ID, nil),
		build(adminOf, "Alpha County", other.Hex(), []string{user.ID}),
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/communities?userId="+user.ID, nil)
	rr := httptest.NewRecorder()
	f.api.ListDeveloperCommunitiesHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var out struct {
		Data       []developerCommunityOption `json:"data"`
		TotalCount int                        `json:"totalCount"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if out.TotalCount != 3 || len(out.Data) != 3 {
		t.Fatalf("expected three communities, got %+v", out.Data)
	}
	// Grantable first, then by name: the common case at the top, and a stable
	// order between loads.
	if out.Data[0].Name != "Alpha County" || !out.Data[0].CanGrant {
		t.Errorf("expected the admin community first, got %+v", out.Data[0])
	}
	if out.Data[1].Name != "My Server" || !out.Data[1].CanGrant {
		t.Errorf("expected the owned community second, got %+v", out.Data[1])
	}
	// Membership alone does not let you approve a key.
	if out.Data[2].Name != "Zebra RP" || out.Data[2].CanGrant {
		t.Errorf("a plain member must not be marked grantable, got %+v", out.Data[2])
	}
}

func TestListDeveloperCommunities_EmptyWhenInNone(t *testing.T) {
	f := newLimitsFixture(t, models.TierFree, 0)
	f.api.CDB = &pickerCommunityDB{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/communities?userId="+f.owner.ID, nil)
	rr := httptest.NewRecorder()
	f.api.ListDeveloperCommunitiesHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// An empty list, never null: the form renders an empty state from it, and a
	// null would make that a crash rather than a message.
	if !strings.Contains(rr.Body.String(), `"data":[]`) {
		t.Errorf("expected an empty array, got %s", rr.Body.String())
	}
}

func TestListDeveloperCommunities_RequiresAnActor(t *testing.T) {
	f := newLimitsFixture(t, models.TierFree, 0)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/communities", nil)
	rr := httptest.NewRecorder()
	f.api.ListDeveloperCommunitiesHandler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestListDeveloperCommunities_SkipsUnreadableMemberships(t *testing.T) {
	// A membership row with a junk id must not take the whole picker down.
	f := newLimitsFixture(t, models.TierFree, 0)
	user := f.owner
	good := primitive.NewObjectID()
	user.Details.Communities = []models.UserCommunity{
		{CommunityID: "not-an-id"},
		{CommunityID: ""},
		{CommunityID: good.Hex()},
	}
	f.userDB.users[user.ID] = user

	c := models.Community{ID: good}
	c.Details.Name = "Still Here"
	c.Details.OwnerID = user.ID
	f.api.CDB = &pickerCommunityDB{communities: []models.Community{c}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/communities?userId="+user.ID, nil)
	rr := httptest.NewRecorder()
	f.api.ListDeveloperCommunitiesHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Still Here") {
		t.Errorf("the readable community should survive, got %s", rr.Body.String())
	}
}

// pickerCommunityDB answers the picker's one query: find by a set of ids.
type pickerCommunityDB struct {
	databases.CommunityDatabase
	communities []models.Community
}

func (f *pickerCommunityDB) Find(_ context.Context, _ interface{}, _ ...*options.FindOptions) (databases.MongoCursor, error) {
	docs := make([]interface{}, 0, len(f.communities))
	for _, c := range f.communities {
		docs = append(docs, c)
	}
	return databases.NewMongoCursorFromDocuments(docs)
}

func (f *pickerCommunityDB) CountDocuments(_ context.Context, _ interface{}, _ ...*options.CountOptions) (int64, error) {
	return 0, nil
}

func (f *pickerCommunityDB) FindOneIncludingPending(_ context.Context, _ interface{}) (*models.Community, error) {
	return nil, mongo.ErrNoDocuments
}
