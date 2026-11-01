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
	for _, plan := range []string{models.DeveloperPlanFree, models.DeveloperPlanPro} {
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
	// Premium allows 10. The old Free developer tier allowed 3, which would
	// have refused the fourth even though the customer had paid for ten.
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
	// Pace is the tier's job and is unaffected: Free is two a day, however many
	// the website plan allows in total.
	//
	// Premium deliberately, not Premium Plus: Premium Plus includes Developer
	// API Pro, which raises the pace to ten a day as well as the request rate.
	// Premium raises the community total to 10 without touching the tier, which
	// is exactly the separation under test.
	f := newLimitsFixture(t, models.TierPremium, 0)

	for i := 0; i < 2; i++ {
		if rr := f.create(t, "Community"); rr.Code != http.StatusCreated {
			t.Fatalf("community %d: expected 201, got %d: %s", i+1, rr.Code, rr.Body.String())
		}
	}
	rr := f.create(t, "Third Today")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on the third today, got %d: %s", rr.Code, rr.Body.String())
	}
	if code := errorCode(rr); code != "provision_cap_reached" {
		t.Errorf("error code: got %q want provision_cap_reached", code)
	}
}

// ---- what the developer page is told ----

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

// The page renders the tier table from this response. If it ever stops matching
// what the middleware enforces, developers write code against a number we do
// not honour, so the table and the enforcement come from one function.
func TestDeveloperAccount_PublishesTheRealTierLimits(t *testing.T) {
	f := newLimitsFixture(t, models.TierFree, 0)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/developer/account?userId="+f.owner.ID, nil)
	rr := httptest.NewRecorder()
	f.api.GetDeveloperAccountHandler(rr, req)

	var out developerAccountResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(out.Tiers) != 2 {
		t.Fatalf("expected Free and Pro, got %+v", out.Tiers)
	}
	for _, tier := range out.Tiers {
		enforced := models.LimitsForPlan(tier.Plan)
		if tier.Limits != enforced {
			t.Errorf("%s: published %+v but the middleware enforces %+v",
				tier.Plan, tier.Limits, enforced)
		}
	}
	// Cheapest first, so the page can render in order without sorting.
	if out.Tiers[0].Plan != models.DeveloperPlanFree {
		t.Errorf("expected Free first, got %q", out.Tiers[0].Plan)
	}
}

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

// ---- who is entitled to Pro, and why ----

// Premium Plus includes Developer API Pro. It is the same person and the same
// account, so a second subscription for throughput they already pay for would
// be indefensible.
func TestDeveloperEntitlement_PremiumPlusGrantsPro(t *testing.T) {
	account := &models.DeveloperAccount{Plan: models.DeveloperPlanFree}

	got := models.DeveloperPlanFor(account, models.TierPremiumPlus)
	if got.Plan != models.DeveloperPlanPro {
		t.Fatalf("expected Pro, got %q", got.Plan)
	}
	if got.Source != models.DeveloperEntitlementWebsite {
		t.Errorf("source: got %q want %q", got.Source, models.DeveloperEntitlementWebsite)
	}
	if got.IncludedWith != models.TierPremiumPlus {
		t.Errorf("includedWith: got %q", got.IncludedWith)
	}
	if got.StandaloneActive || got.Redundant {
		t.Error("they are not paying twice, so nothing should say they are")
	}
	// And the limits that follow are the real Pro ones.
	if models.LimitsForPlan(got.Plan) != models.LimitsForPlan(models.DeveloperPlanPro) {
		t.Error("a Premium Plus developer must get the actual Pro limits")
	}
}

func TestDeveloperEntitlement_LapsedPremiumPlusFallsBack(t *testing.T) {
	// Falls back rather than locking out: Free is a working tier, so a lapse
	// costs rate and nothing else.
	account := &models.DeveloperAccount{Plan: models.DeveloperPlanFree}

	got := models.DeveloperPlanFor(account, models.TierPremium)
	if got.Plan != models.DeveloperPlanFree {
		t.Fatalf("expected a fall back to Free, got %q", got.Plan)
	}
	if got.Source != models.DeveloperEntitlementNone {
		t.Errorf("source: got %q", got.Source)
	}
}

func TestDeveloperEntitlement_LapsedPremiumPlusKeepsAStandalonePro(t *testing.T) {
	// They bought Pro as well. Losing Premium Plus must not take away the
	// subscription they are still paying for.
	account := &models.DeveloperAccount{Plan: models.DeveloperPlanPro, PlanActive: true}

	got := models.DeveloperPlanFor(account, models.TierPremium)
	if got.Plan != models.DeveloperPlanPro {
		t.Fatalf("expected Pro from the standalone subscription, got %q", got.Plan)
	}
	if got.Source != models.DeveloperEntitlementStandalone {
		t.Errorf("source: got %q want %q", got.Source, models.DeveloperEntitlementStandalone)
	}
	if got.Redundant {
		t.Error("with no Premium Plus the standalone is not redundant")
	}
}

func TestDeveloperEntitlement_StandaloneProWorksOnItsOwn(t *testing.T) {
	account := &models.DeveloperAccount{Plan: models.DeveloperPlanPro, PlanActive: true}

	got := models.DeveloperPlanFor(account, models.TierFree)
	if got.Plan != models.DeveloperPlanPro || got.Source != models.DeveloperEntitlementStandalone {
		t.Fatalf("expected standalone Pro, got %+v", got)
	}
	if !got.StandaloneActive {
		t.Error("their own subscription is active and should say so")
	}
}

func TestDeveloperEntitlement_FlagsPayingTwice(t *testing.T) {
	// Premium Plus already covers it. Worth saying out loud rather than quietly
	// taking the money.
	account := &models.DeveloperAccount{Plan: models.DeveloperPlanPro, PlanActive: true}

	got := models.DeveloperPlanFor(account, models.TierPremiumPlus)
	if !got.Redundant {
		t.Fatal("a standalone subscription under Premium Plus is redundant and must be flagged")
	}
	if got.Source != models.DeveloperEntitlementWebsite {
		t.Errorf("the website plan is what grants it, got source %q", got.Source)
	}
	if !got.StandaloneActive {
		t.Error("the standalone subscription is still live and still cancellable")
	}
}

func TestDeveloperEntitlement_UnknownPlanFallsBackToFree(t *testing.T) {
	account := &models.DeveloperAccount{Plan: models.DeveloperPlanFree}

	// A value from a webhook payload we cannot read must never grant anything.
	for _, plan := range []string{"", "  ", "platinum", "premium_plus_plus", "PREMIUM-PLUS", "$ne"} {
		got := models.DeveloperPlanFor(account, plan)
		if got.Plan != models.DeveloperPlanFree {
			t.Errorf("plan %q granted %q; unknown plans must fall back to Free", plan, got.Plan)
		}
	}
	// And the one spelling that does count still counts, whatever the case.
	if !models.WebsitePlanIncludesDeveloperPro("  Premium_Plus  ") {
		t.Error("the real plan should resolve regardless of case and padding")
	}
}

func TestDeveloperEntitlement_InactiveStandaloneDoesNotGrantPro(t *testing.T) {
	account := &models.DeveloperAccount{Plan: models.DeveloperPlanPro, PlanActive: false}

	got := models.DeveloperPlanFor(account, models.TierFree)
	if got.Plan != models.DeveloperPlanFree {
		t.Fatalf("a lapsed standalone must bill as Free, got %q", got.Plan)
	}
	if got.StandaloneActive {
		t.Error("an inactive subscription is not active")
	}
}

func TestDeveloperEntitlement_NilAccountIsFree(t *testing.T) {
	if got := models.DeveloperPlanFor(nil, models.TierFree); got.Plan != models.DeveloperPlanFree {
		t.Errorf("no account means Free, got %q", got.Plan)
	}
	// Premium Plus still grants Pro before a developer account exists, which is
	// what the page shows someone deciding whether to turn one on.
	if got := models.DeveloperPlanFor(nil, models.TierPremiumPlus); got.Plan != models.DeveloperPlanPro {
		t.Errorf("Premium Plus should grant Pro even with no developer account yet, got %q", got.Plan)
	}
}
func TestDeveloperPricing_UsesTheApprovedPrices(t *testing.T) {
	t.Setenv("DEV_API_PRO_PRICE_MONTHLY", "")
	t.Setenv("DEV_API_PRO_PRICE_YEARLY", "")
	t.Setenv("PREMIUM_PLUS_PRICE_MONTHLY", "")
	t.Setenv("PREMIUM_PLUS_PRICE_YEARLY", "")

	pricing := developerPricingConfig()
	if pricing.ProMonthly != "4.99" || pricing.ProYearly != "49.00" {
		t.Errorf("expected the approved Pro prices, got %+v", pricing)
	}
	if pricing.IncludedWithMonthly != "9.99" || pricing.IncludedWithYearly != "99.00" {
		t.Errorf("expected the Premium Plus prices, got %+v", pricing)
	}
	if pricing.IncludedWithPlan != models.TierPremiumPlus {
		t.Errorf("includedWithPlan: got %q", pricing.IncludedWithPlan)
	}
	if pricing.IncludedWithURL == "" {
		t.Error("the page needs somewhere to send people to buy Premium Plus")
	}
	if !pricing.PriceSet {
		t.Error("the prices are decided, so the page should show them")
	}
}

func TestDeveloperPricing_OverridableWithoutADeploy(t *testing.T) {
	t.Setenv("DEV_API_PRO_PRICE_MONTHLY", "5.99")
	t.Setenv("DEV_API_PRO_PRICE_YEARLY", "59.00")

	pricing := developerPricingConfig()
	if pricing.ProMonthly != "5.99" || pricing.ProYearly != "59.00" {
		t.Errorf("config should win over the default, got %+v", pricing)
	}
}

// Premium Plus raises the provisioning pace too, because it grants the whole
// Pro tier and pace is part of it. Worth pinning: it is a side effect of the
// entitlement that is easy to not notice.
func TestDevCreateCommunity_PremiumPlusGetsTheProPace(t *testing.T) {
	f := newLimitsFixture(t, models.TierPremiumPlus, 0)

	// Three in a day, which the Free pace of two would have refused.
	for i := 0; i < 3; i++ {
		if rr := f.create(t, "Community"); rr.Code != http.StatusCreated {
			t.Fatalf("community %d: expected 201 on the Pro pace, got %d: %s",
				i+1, rr.Code, rr.Body.String())
		}
	}
}
