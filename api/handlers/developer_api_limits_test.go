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
	f := newLimitsFixture(t, models.TierPremiumPlus, 0)

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
