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
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Dispatch calls addressed by the integration's own id.
//
// The property everything here defends: a game server reconnects, replays a
// queue and retries a timeout, and none of that may put a second call on a
// dispatch board.

// ---- pure helpers ----

func TestNormalizeExternalRef(t *testing.T) {
	t.Run("requires an id, because idempotency depends on it", func(t *testing.T) {
		_, _, err := normalizeExternalRef("  ", "SixPD", "key")
		if err == nil {
			t.Fatal("expected an error with no external id")
		}
	})

	t.Run("source falls back to the key's name", func(t *testing.T) {
		_, source, err := normalizeExternalRef("incident-1", "", "SixPD production")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if source != "SixPD production" {
			t.Errorf("expected the key name as the source, got %q", source)
		}
	})

	t.Run("an explicit source wins, so one developer can keep two id spaces apart", func(t *testing.T) {
		_, source, _ := normalizeExternalRef("incident-1", "sixpd-dev", "SixPD production")
		if source != "sixpd-dev" {
			t.Errorf("got %q", source)
		}
	})

	t.Run("refuses a control character", func(t *testing.T) {
		if _, _, err := normalizeExternalRef("incident\x00one", "s", "k"); err == nil {
			t.Fatal("an id that cannot be logged or matched back must be refused")
		}
	})

	t.Run("refuses an unbounded id", func(t *testing.T) {
		if _, _, err := normalizeExternalRef(strings.Repeat("x", maxExternalIDLen+1), "s", "k"); err == nil {
			t.Fatal("expected a length limit")
		}
	})
}

func TestTruncateDispatchText(t *testing.T) {
	if got := truncateDispatchText("  hello  "); got != "hello" {
		t.Errorf("got %q", got)
	}
	if got := truncateDispatchText(strings.Repeat("x", maxDispatchTextLen*2)); len(got) > maxDispatchTextLen {
		t.Errorf("a %d-character description was not bounded", len(got))
	}
}

// Absent means "leave it alone", not "clear it". A status update should not
// have to resend the location and type set when the call was raised.
func TestApplyOptionalDispatchFields_OnlySetsWhatWasSent(t *testing.T) {
	set := bson.M{}
	applyOptionalDispatchFields(set, devCallWriteRequest{Priority: "high"})

	if _, ok := set["call.location"]; ok {
		t.Error("an absent location must not be written")
	}
	if _, ok := set["call.type"]; ok {
		t.Error("an absent type must not be written")
	}
	if set["call.priority"] != "high" {
		t.Errorf("the sent field should be written, got %v", set["call.priority"])
	}
}

// ---- fake call store ----

type fakeCallDB struct {
	databases.CallDatabase
	mu      sync.Mutex
	calls   []models.Call
	inserts int
	filters []interface{}
}

func (f *fakeCallDB) Find(_ context.Context, filter interface{}, _ ...*options.FindOptions) ([]models.Call, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filters = append(f.filters, filter)
	m, _ := filter.(bson.M)

	out := []models.Call{}
	for _, c := range f.calls {
		if v, ok := devFilterString(m["call.communityID"]); ok && c.Details.CommunityID != v {
			continue
		}
		if v, ok := devFilterString(m["call.source"]); ok && c.Details.Source != v {
			continue
		}
		if v, ok := devFilterString(m["call.externalId"]); ok && c.Details.ExternalID != v {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeCallDB) InsertOne(_ context.Context, doc interface{}, _ ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := doc.(models.Call); ok {
		f.calls = append(f.calls, c)
		f.inserts++
	}
	return fakeInsertResult{id: primitive.NewObjectID()}, nil
}

func (f *fakeCallDB) UpdateOne(_ context.Context, filter, update interface{}, _ ...*options.UpdateOptions) (*models.Call, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fm, _ := filter.(bson.M)
	um, _ := update.(bson.M)
	set, _ := um["$set"].(bson.M)

	for i := range f.calls {
		if wanted, ok := devFilterString(fm["_id"]); !ok || f.calls[i].ID != wanted {
			continue
		}
		if v, ok := set["call.title"].(string); ok {
			f.calls[i].Details.Title = v
		}
		if v, ok := set["call.details"].(string); ok {
			f.calls[i].Details.Details = v
		}
		if v, ok := set["call.location"].(string); ok {
			f.calls[i].Details.Location = v
		}
		if v, ok := set["call.priority"].(string); ok {
			f.calls[i].Details.Priority = v
		}
		if v, ok := set["call.disposition"].(string); ok {
			f.calls[i].Details.Disposition = v
		}
		if v, ok := set["call.assignedTo"].([]string); ok {
			f.calls[i].Details.AssignedTo = v
		}
		if v, ok := set["call.status"].(bool); ok {
			f.calls[i].Details.Status = v
		}
		found := f.calls[i]
		return &found, nil
	}
	return nil, nil
}

// dispatchFixture is a member fixture with a call store and calls scopes.
type dispatchFixture struct {
	*memberFixture
	callDB *fakeCallDB
}

func newDispatchFixture(t *testing.T) *dispatchFixture {
	t.Helper()
	base := newMemberFixture(t)
	base.keyDB.byHash = upgradeKeyScopes(base.keyDB.byHash,
		models.ScopeCallsRead, models.ScopeCallsWrite, models.ScopePlayersProvision)

	callDB := &fakeCallDB{}
	base.api.CallDB = callDB
	return &dispatchFixture{memberFixture: base, callDB: callDB}
}

func (f *dispatchFixture) upsert(t *testing.T, body string) (*httptest.ResponseRecorder, devCallView, bool) {
	t.Helper()
	rr := f.run(t, http.MethodPost, "/dispatch/calls", body, nil,
		f.api.RequireScope(models.ScopeCallsWrite, f.api.DevUpsertCallHandler))

	var out struct {
		Call    devCallView `json:"call"`
		Created bool        `json:"created"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr, out.Call, out.Created
}

// ---- idempotency, which is the whole point ----

// The same external id twice is one call, updated. This is what makes a
// reconnect and queue replay safe.
func TestDevUpsertCall_SameExternalIdDoesNotDuplicate(t *testing.T) {
	f := newDispatchFixture(t)

	rr1, call1, created1 := f.upsert(t, `{"externalId":"sixpd-incident-42","source":"SixPD","title":"Traffic stop","location":"Route 68","type":"traffic","priority":"routine"}`)
	if rr1.Code != http.StatusCreated || !created1 {
		t.Fatalf("expected 201 created, got %d created=%v: %s", rr1.Code, created1, rr1.Body.String())
	}

	rr2, call2, created2 := f.upsert(t, `{"externalId":"sixpd-incident-42","source":"SixPD","title":"Traffic stop - updated","priority":"urgent"}`)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 on the repeat, got %d: %s", rr2.Code, rr2.Body.String())
	}
	if created2 {
		t.Error("a repeat must not report creating anything")
	}
	if call1.ID != call2.ID {
		t.Errorf("the same external id must resolve to one call: %s vs %s", call1.ID, call2.ID)
	}
	if f.callDB.inserts != 1 {
		t.Errorf("expected exactly one call to be written, got %d", f.callDB.inserts)
	}
	if call2.Title != "Traffic stop - updated" || call2.Priority != "urgent" {
		t.Errorf("the repeat should have updated the call, got %+v", call2)
	}
	// Absent fields are left alone rather than cleared.
	if call2.Location != "Route 68" {
		t.Errorf("an absent location must not clear the stored one, got %q", call2.Location)
	}
	if call2.Type != "traffic" {
		t.Errorf("an absent type must not clear the stored one, got %q", call2.Type)
	}
}

// Two integrations, or one integration's dev and production instances, can use
// the same incident numbers without colliding.
func TestDevUpsertCall_SourceScopesTheIdSpace(t *testing.T) {
	f := newDispatchFixture(t)

	_, first, _ := f.upsert(t, `{"externalId":"incident-1","source":"sixpd-prod","title":"One"}`)
	_, second, created := f.upsert(t, `{"externalId":"incident-1","source":"sixpd-dev","title":"Two"}`)

	if !created {
		t.Fatal("a different source must be a different call")
	}
	if first.ID == second.ID {
		t.Error("the same id under two sources must not collide")
	}
	if f.callDB.inserts != 2 {
		t.Errorf("expected two calls, got %d", f.callDB.inserts)
	}
}

func TestDevUpsertCall_RequiresExternalIdAndTitle(t *testing.T) {
	f := newDispatchFixture(t)

	rr, _, _ := f.upsert(t, `{"title":"No id"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 with no external id, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "idempotent") {
		t.Errorf("the error should say why the id is required: %s", rr.Body.String())
	}

	rr2, _, _ := f.upsert(t, `{"externalId":"x","title":"  "}`)
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 with no title, got %d", rr2.Code)
	}
}

// ---- assignment ----

// An assignment list is a statement about who is on a call. Silently dropping a
// name would show dispatch a roster that is quietly wrong.
func TestDevUpsertCall_AssignmentIsAllOrNothing(t *testing.T) {
	f := newDispatchFixture(t)

	rr, _, _ := f.upsert(t, `{"externalId":"i1","title":"Call","assignedUnits":["fivem:6d656d626572","fivem:6f75747369646572"]}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when one assigned unit is not a member, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "player_not_a_member") {
		t.Errorf("the reason should be specific: %s", rr.Body.String())
	}
	if f.callDB.inserts != 0 {
		t.Error("nothing should have been written when the assignment was refused")
	}
}

func TestDevUpsertCall_AssignsMembers(t *testing.T) {
	f := newDispatchFixture(t)

	rr, call, _ := f.upsert(t, `{"externalId":"i1","title":"Call","assignedUnits":["fivem:6d656d626572","fivem:61646d696e","fivem:6d656d626572"]}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(call.AssignedUnits) != 2 {
		t.Errorf("expected two distinct units, got %v", call.AssignedUnits)
	}
}

// ---- reconciliation after an outage ----

// A game server that restarted knows its own incident ids and nothing else.
func TestDevGetCallByExternalID(t *testing.T) {
	f := newDispatchFixture(t)
	f.upsert(t, `{"externalId":"sixpd-7","source":"SixPD","title":"Shots fired","priority":"urgent"}`)

	rr := f.run(t, http.MethodGet, "/dispatch/calls/sixpd-7?source=SixPD", "",
		map[string]string{"externalId": "sixpd-7"},
		f.api.RequireScope(models.ScopeCallsRead, f.api.DevGetCallByExternalIDHandler))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var out struct{ Call devCallView }
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Call.ExternalID != "sixpd-7" || out.Call.Status != "open" {
		t.Errorf("unexpected call %+v", out.Call)
	}
	// Both ids come back: theirs to match on, ours for a deep link.
	if out.Call.ID == "" || out.Call.Link == "" {
		t.Errorf("expected our id and a deep link, got %+v", out.Call)
	}
}

func TestDevGetCallByExternalID_UnknownIsA404(t *testing.T) {
	f := newDispatchFixture(t)

	rr := f.run(t, http.MethodGet, "/dispatch/calls/nope", "",
		map[string]string{"externalId": "nope"},
		f.api.RequireScope(models.ScopeCallsRead, f.api.DevGetCallByExternalIDHandler))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// ---- closing ----

func TestDevCloseCallByExternalID(t *testing.T) {
	f := newDispatchFixture(t)
	f.upsert(t, `{"externalId":"sixpd-9","source":"SixPD","title":"Pursuit"}`)

	rr := f.run(t, http.MethodPost, "/dispatch/calls/sixpd-9/close",
		`{"source":"SixPD","disposition":"Arrest made"}`,
		map[string]string{"externalId": "sixpd-9"},
		f.api.RequireScope(models.ScopeCallsWrite, f.api.DevCloseCallByExternalIDHandler))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var out struct {
		Call   devCallView `json:"call"`
		Closed bool        `json:"closed"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if !out.Closed || out.Call.Status != "closed" {
		t.Errorf("expected a closed call, got %+v", out)
	}
	if out.Call.Disposition != "Arrest made" {
		t.Errorf("the disposition should be recorded, got %q", out.Call.Disposition)
	}
}

// A retry after a timeout must not become a failure the caller has to handle.
func TestDevCloseCallByExternalID_ClosingTwiceIsNotAnError(t *testing.T) {
	f := newDispatchFixture(t)
	f.upsert(t, `{"externalId":"sixpd-9","source":"SixPD","title":"Pursuit"}`)

	closeOnce := func() *httptest.ResponseRecorder {
		return f.run(t, http.MethodPost, "/dispatch/calls/sixpd-9/close", `{"source":"SixPD"}`,
			map[string]string{"externalId": "sixpd-9"},
			f.api.RequireScope(models.ScopeCallsWrite, f.api.DevCloseCallByExternalIDHandler))
	}

	if rr := closeOnce(); rr.Code != http.StatusOK {
		t.Fatalf("first close: expected 200, got %d", rr.Code)
	}

	rr := closeOnce()
	if rr.Code != http.StatusOK {
		t.Fatalf("a retry must not fail, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Closed bool `json:"closed"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Closed {
		t.Error("the second close should report that nothing changed")
	}
}

// ---- scoping ----

// A key granted one community must not reach a call in another, even with a
// valid external id.
func TestDevDispatch_IsScopedToTheCommunityInThePath(t *testing.T) {
	f := newDispatchFixture(t)
	f.upsert(t, `{"externalId":"shared-id","source":"SixPD","title":"Ours"}`)

	// A call with the same external id in a community this key cannot reach.
	f.callDB.mu.Lock()
	f.callDB.calls = append(f.callDB.calls, models.Call{
		ID: primitive.NewObjectID().Hex(),
		Details: models.CallDetails{
			Title: "Somebody else's", CommunityID: primitive.NewObjectID().Hex(),
			Source: "SixPD", ExternalID: "shared-id", Status: true,
		},
	})
	f.callDB.mu.Unlock()

	rr := f.run(t, http.MethodGet, "/dispatch/calls/shared-id?source=SixPD", "",
		map[string]string{"externalId": "shared-id"},
		f.api.RequireScope(models.ScopeCallsRead, f.api.DevGetCallByExternalIDHandler))

	var out struct{ Call devCallView }
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Call.Title != "Ours" {
		t.Errorf("resolved the wrong community's call: %+v", out.Call)
	}

	// And the query itself carries the community, rather than filtering after.
	f.callDB.mu.Lock()
	defer f.callDB.mu.Unlock()
	for _, filter := range f.callDB.filters {
		m, _ := filter.(bson.M)
		if _, ok := devFilterString(m["call.communityID"]); !ok {
			t.Errorf("every dispatch lookup must be scoped by community, got %v", m)
		}
	}
}

// A key without calls:write cannot raise a call, however well-formed.
func TestDevDispatch_NeedsTheWriteScope(t *testing.T) {
	f := newDispatchFixture(t)
	f.keyDB.byHash = upgradeKeyScopes(f.keyDB.byHash, models.ScopeCallsRead)

	rr, _, _ := f.upsert(t, `{"externalId":"i1","title":"Call"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without calls:write, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), models.ScopeCallsWrite) {
		t.Errorf("the error should name the scope: %s", rr.Body.String())
	}
}
