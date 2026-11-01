package handlers

import (
	"context"
	"encoding/json"
	"net/http"
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

// Tests for the records a mod writes and the lookups it runs.
//
// The two properties worth defending: every query is scoped to the community in
// the path, and a key does not get to ignore the settings the community's owner
// chose.

// A search term goes into a Mongo regex, so an unescaped one is both a
// correctness and a disclosure problem: a plate of ".*" would return every
// vehicle in the community.
func TestRegexEscapeDev(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"plain":           {"ABC123", "ABC123"},
		"wildcard":        {".*", `\.\*`},
		"anchors":         {"^abc$", `\^abc\$`},
		"groups":          {"(((", `\(\(\(`},
		"character class": {"[a-z]", `\[a-z\]`},
		"alternation":     {"a|b", `a\|b`},
		"backslash":       {`a\b`, `a\\b`},
	} {
		t.Run(name, func(t *testing.T) {
			if got := regexEscapeDev(tc.in); got != tc.want {
				t.Errorf("regexEscapeDev(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A character a mod creates has to be findable by the website's own search,
// which matches on a normalized name.
func TestNormalizeDevSearchName(t *testing.T) {
	for in, want := range map[string]string{
		"John Smith":      "john smith",
		"  JOHN   SMITH ": "john smith",
		"John":            "john",
	} {
		if got := normalizeDevSearchName(in); got != want {
			t.Errorf("normalizeDevSearchName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A location comes straight from a game script and ends up on a dispatch board.
func TestTruncateDevLocation(t *testing.T) {
	long := strings.Repeat("x", maxDevLocationLen*2)
	if got := truncateDevLocation(long); len(got) > maxDevLocationLen {
		t.Errorf("a %d-character location was not truncated", len(got))
	}
	if got := truncateDevLocation("Grapeseed"); got != "Grapeseed" {
		t.Errorf("a short location should pass through, got %q", got)
	}
}

// ---- fakes ----

type fakeCivilianDB struct {
	databases.CivilianDatabase
	mu         sync.Mutex
	civilians  []models.Civilian
	count      int64
	inserted   []models.Civilian
	lastFilter interface{}
}

func (f *fakeCivilianDB) CountDocuments(_ context.Context, filter interface{}, _ ...*options.CountOptions) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastFilter = filter
	return f.count, nil
}

func (f *fakeCivilianDB) InsertOne(_ context.Context, doc interface{}, _ ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := doc.(models.Civilian); ok {
		f.inserted = append(f.inserted, c)
	}
	return fakeInsertResult{id: primitive.NewObjectID()}, nil
}

func (f *fakeCivilianDB) Find(_ context.Context, filter interface{}, _ ...*options.FindOptions) ([]models.Civilian, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastFilter = filter
	return f.civilians, nil
}

func (f *fakeCivilianDB) FindOne(_ context.Context, filter interface{}, _ ...*options.FindOneOptions) (*models.Civilian, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastFilter = filter
	m, _ := filter.(bson.M)
	for _, c := range f.civilians {
		if oid, ok := m["_id"].(primitive.ObjectID); ok && c.ID != oid {
			continue
		}
		if u, ok := m["civilian.userID"].(string); ok && c.Details.UserID != u {
			continue
		}
		if cm, ok := m["civilian.activeCommunityID"].(string); ok && c.Details.ActiveCommunityID != cm {
			continue
		}
		found := c
		return &found, nil
	}
	return nil, mongo.ErrNoDocuments
}

// recordFixture is a member fixture with record stores attached.
type recordFixture struct {
	*memberFixture
	civDB *fakeCivilianDB
}

func newRecordFixture(t *testing.T) *recordFixture {
	t.Helper()
	base := newMemberFixture(t)
	base.keyDB.byHash = upgradeKeyScopes(base.keyDB.byHash,
		models.ScopeCiviliansRead, models.ScopeCiviliansWrite,
		models.ScopeUnitsRead, models.ScopeUnitsWrite,
		models.ScopeCommunityRead, models.ScopePlayersProvision)

	civDB := &fakeCivilianDB{}
	base.api.CivDB = civDB
	return &recordFixture{memberFixture: base, civDB: civDB}
}

// ---- civilians ----

// A key does not get to ignore the community's own civilian cap.
func TestDevCreateCivilian_RespectsTheCommunityCap(t *testing.T) {
	f := newRecordFixture(t)

	c := f.communityDB.communities[f.community.ID.Hex()]
	c.Details.CivilianCreationLimitsEnabled = true
	c.Details.CivilianCreationLimit = 2
	f.communityDB.communities[f.community.ID.Hex()] = c
	f.civDB.count = 2 // the player already has their two

	rr := f.run(t, http.MethodPost, "/civilians",
		`{"player":"fivem:member","firstName":"John","lastName":"Smith"}`, nil,
		f.api.RequireScope(models.ScopeCiviliansWrite, f.api.DevCreateCivilianHandler))

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 at the community's cap, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "civilian_limit_reached") {
		t.Errorf("the reason should be specific: %s", rr.Body.String())
	}
	if len(f.civDB.inserted) != 0 {
		t.Error("nothing should have been written at the cap")
	}
}

// A community that vets its characters vets a mod's too.
func TestDevCreateCivilian_RespectsTheApprovalSystem(t *testing.T) {
	f := newRecordFixture(t)

	c := f.communityDB.communities[f.community.ID.Hex()]
	c.Details.CivilianApprovalSystemEnabled = true
	f.communityDB.communities[f.community.ID.Hex()] = c

	rr := f.run(t, http.MethodPost, "/civilians",
		`{"player":"fivem:member","firstName":"John","lastName":"Smith"}`, nil,
		f.api.RequireScope(models.ScopeCiviliansWrite, f.api.DevCreateCivilianHandler))

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		PendingApproval bool `json:"pendingApproval"`
		Civilian        struct {
			ApprovalStatus string `json:"approvalStatus"`
		} `json:"civilian"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if !out.PendingApproval || out.Civilian.ApprovalStatus != "pending" {
		t.Errorf("a character should arrive pending where the community vets them: %s", rr.Body.String())
	}
	if len(f.civDB.inserted) == 1 && f.civDB.inserted[0].Details.ApprovalStatus != "pending" {
		t.Error("the stored character should be pending too")
	}
}

// A character is stamped with the key that made it, which is the handle for
// cleaning up after a misbehaving key.
func TestDevCreateCivilian_StampsTheKey(t *testing.T) {
	f := newRecordFixture(t)

	rr := f.run(t, http.MethodPost, "/civilians",
		`{"player":"fivem:member","firstName":"John","lastName":"Smith"}`, nil,
		f.api.RequireScope(models.ScopeCiviliansWrite, f.api.DevCreateCivilianHandler))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	if len(f.civDB.inserted) != 1 {
		t.Fatalf("expected one character, got %d", len(f.civDB.inserted))
	}
	src := f.civDB.inserted[0].Details.Source
	if src == nil {
		t.Fatal("a mod-made character must carry its source")
	}
	if src.Type != models.RecordSourceDeveloperAPI || src.KeyID != f.keyID.Hex() {
		t.Errorf("unexpected source %+v", src)
	}
	if src.Provider != models.PlayerProviderFiveM {
		t.Errorf("the source should record how the player was named, got %q", src.Provider)
	}
	// And it must land in the right community, from the path rather than the body.
	if f.civDB.inserted[0].Details.ActiveCommunityID != f.community.ID.Hex() {
		t.Error("the character must belong to the community in the path")
	}
}

// Someone who is not a member of the community cannot have records made for
// them, however the mod names them.
func TestDevCreateCivilian_PlayerMustBeAMember(t *testing.T) {
	f := newRecordFixture(t)

	rr := f.run(t, http.MethodPost, "/civilians",
		`{"player":"fivem:outsider","firstName":"John","lastName":"Smith"}`, nil,
		f.api.RequireScope(models.ScopeCiviliansWrite, f.api.DevCreateCivilianHandler))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "player_not_a_member") {
		t.Errorf("the reason should be specific: %s", rr.Body.String())
	}
}

func TestDevCreateCivilian_RequiresAName(t *testing.T) {
	f := newRecordFixture(t)

	rr := f.run(t, http.MethodPost, "/civilians",
		`{"player":"fivem:member","firstName":"  ","lastName":""}`, nil,
		f.api.RequireScope(models.ScopeCiviliansWrite, f.api.DevCreateCivilianHandler))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// Every search is scoped to the community in the path by the query itself, not
// by filtering results afterwards, so there is no path by which another
// community's record could be returned.
func TestDevSearchCivilians_IsScopedByQuery(t *testing.T) {
	f := newRecordFixture(t)

	rr := f.run(t, http.MethodGet, "/civilians?name=Smith", "", nil,
		f.api.RequireScope(models.ScopeCiviliansRead, f.api.DevSearchCiviliansHandler))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	filter, ok := f.civDB.lastFilter.(bson.M)
	if !ok {
		t.Fatalf("expected a bson filter, got %T", f.civDB.lastFilter)
	}
	if filter["civilian.activeCommunityID"] != f.community.ID.Hex() {
		t.Errorf("the search must be scoped to the community in the path, got %v", filter)
	}
}

func TestDevSearchCivilians_RequiresAName(t *testing.T) {
	f := newRecordFixture(t)

	rr := f.run(t, http.MethodGet, "/civilians", "", nil,
		f.api.RequireScope(models.ScopeCiviliansRead, f.api.DevSearchCiviliansHandler))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 with no name, got %d", rr.Code)
	}
}

// ---- unit status ----

// The bulk endpoint is what a FiveM server calls on a tick, and a game server
// cannot usefully retry a whole tick, so one bad entry must not discard the
// good ones.
func TestDevBulkSetUnitStatus_IsPartial(t *testing.T) {
	f := newRecordFixture(t)
	tenCodeID := f.seedTenCode()

	body := `{"units":[
		{"player":"fivem:member","tenCodeId":"` + tenCodeID + `","location":"Grapeseed"},
		{"player":"fivem:nobody","tenCodeId":"` + tenCodeID + `"},
		{"player":"fivem:outsider","tenCodeId":"` + tenCodeID + `"},
		{"player":"fivem:admin","tenCodeId":"` + primitive.NewObjectID().Hex() + `"},
		{"player":"fivem:admin"}
	]}`

	rr := f.run(t, http.MethodPut, "/units/status/bulk", body, nil,
		f.api.RequireScope(models.ScopeUnitsWrite, f.api.DevBulkSetUnitStatusHandler))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var out struct {
		Applied int `json:"applied"`
		Failed  int `json:"failed"`
		Units   []struct {
			Player string `json:"player"`
			OK     bool   `json:"ok"`
			Error  string `json:"error"`
		} `json:"units"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)

	if out.Applied != 1 {
		t.Errorf("expected the one good entry to land, got %d applied: %s", out.Applied, rr.Body.String())
	}
	if out.Failed != 4 {
		t.Errorf("expected four failures, got %d", out.Failed)
	}

	wantErrors := map[string]string{
		"fivem:member":   "",
		"fivem:nobody":   "invalid_player",
		"fivem:outsider": "player_not_a_member",
		"fivem:admin":    "", // two entries, checked below
	}
	for _, u := range out.Units {
		if want, known := wantErrors[u.Player]; known && want != "" && u.Error != want {
			t.Errorf("%s: expected error %q, got %q", u.Player, want, u.Error)
		}
	}
	// Each entry reports its own outcome rather than one shared verdict.
	if len(out.Units) != 5 {
		t.Errorf("expected a result per entry, got %d", len(out.Units))
	}
}

// A batch is bounded, so one request cannot become an unbounded write.
func TestDevBulkSetUnitStatus_CapsTheBatch(t *testing.T) {
	f := newRecordFixture(t)

	var entries []string
	for i := 0; i <= maxBulkUnitStatus; i++ {
		entries = append(entries, `{"player":"fivem:member"}`)
	}
	body := `{"units":[` + strings.Join(entries, ",") + `]}`

	rr := f.run(t, http.MethodPut, "/units/status/bulk", body, nil,
		f.api.RequireScope(models.ScopeUnitsWrite, f.api.DevBulkSetUnitStatusHandler))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 over the batch cap, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "too_many_units") {
		t.Errorf("the reason should be specific: %s", rr.Body.String())
	}
}

func TestDevBulkSetUnitStatus_RejectsAnEmptyBatch(t *testing.T) {
	f := newRecordFixture(t)

	rr := f.run(t, http.MethodPut, "/units/status/bulk", `{"units":[]}`, nil,
		f.api.RequireScope(models.ScopeUnitsWrite, f.api.DevBulkSetUnitStatusHandler))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// A single-unit call is not a batch of one: a failure is the whole request and
// should not come back as a 200 with the error buried in a list.
func TestDevSetUnitStatus_SingleFailureIsNotA200(t *testing.T) {
	f := newRecordFixture(t)

	rr := f.run(t, http.MethodPut, "/units/fivem:outsider/status",
		`{"tenCodeId":"`+f.seedTenCode()+`"}`,
		map[string]string{"playerRef": "fivem:outsider"},
		f.api.RequireScope(models.ScopeUnitsWrite, f.api.DevSetUnitStatusHandler))

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for a one-unit failure, got %d: %s", rr.Code, rr.Body.String())
	}
}

// An arbitrary ten-code id would put a status on the dispatch board that no
// client can render and nobody can clear.
func TestDevSetUnitStatus_TenCodeMustBelongToTheCommunity(t *testing.T) {
	f := newRecordFixture(t)

	rr := f.run(t, http.MethodPut, "/units/fivem:member/status",
		`{"tenCodeId":"`+primitive.NewObjectID().Hex()+`"}`,
		map[string]string{"playerRef": "fivem:member"},
		f.api.RequireScope(models.ScopeUnitsWrite, f.api.DevSetUnitStatusHandler))

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "ten_code_not_found") {
		t.Errorf("the reason should be specific: %s", rr.Body.String())
	}
}

// seedTenCode adds a ten-code to the fixture's community and returns its id.
func (f *recordFixture) seedTenCode() string {
	id := primitive.NewObjectID()
	c := f.communityDB.communities[f.community.ID.Hex()]
	c.Details.TenCodes = append(c.Details.TenCodes, models.TenCodes{
		ID: id, Code: "10-8", Description: "In service", Category: models.TenCodeCategoryAvailable,
	})
	f.communityDB.communities[f.community.ID.Hex()] = c
	return id.Hex()
}

// ---- reference data ----

// A mod needs these ids, and the alternative is hardcoding them per community
// and breaking the moment an owner edits a code.
func TestDevListTenCodesAndDepartments(t *testing.T) {
	f := newRecordFixture(t)
	f.seedTenCode()

	t.Run("ten-codes", func(t *testing.T) {
		rr := f.run(t, http.MethodGet, "/ten-codes", "", nil,
			f.api.RequireScope(models.ScopeCommunityRead, f.api.DevListTenCodesHandler))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "10-8") {
			t.Errorf("expected the community's code, got %s", rr.Body.String())
		}
	})

	t.Run("departments carry their ranks", func(t *testing.T) {
		rr := f.run(t, http.MethodGet, "/departments", "", nil,
			f.api.RequireScope(models.ScopeCommunityRead, f.api.DevListDepartmentsHandler))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "BCSO") || !strings.Contains(body, "Deputy") {
			t.Errorf("expected the department and its rank, got %s", body)
		}
	})
}
