package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/linesmerrill/police-cad-api/models"
)

// What happens when the developer API is switched on but not yet configured.
//
// This is the state production was found in: DEVELOPER_API_ENABLED set,
// DEV_API_KEY_PEPPER not. The request surface already refused, correctly, but
// the management surface happily minted keys whose hashes were keyed with an
// empty secret. Those keys work against nothing today and will match nothing
// once the pepper is set, so the developer is left holding a secret that has
// never worked and never will.
//
// Minting is the step that has to refuse, which is what these cover.

// withPepper sets the hashing secret for one test.
func withPepper(t *testing.T, value string) {
	t.Helper()
	t.Setenv(devAPIEnabledEnv, "true")
	t.Setenv(devAPIKeyPepperEnv, value)
}

func TestCreateKey_RefusedWhenPepperMissing(t *testing.T) {
	f := newConsoleFixture(t)
	withPepper(t, "")

	req := f.request(http.MethodPost, "/api/v1/developer/keys",
		map[string]interface{}{
			"name":        "test1",
			"scopes":      []string{models.ScopeCommunitiesProvision},
			"communityId": "",
		}, nil)
	rr := httptest.NewRecorder()
	f.api.CreateDeveloperKeyHandler(rr, req)

	// The same status the request surface uses for the same condition, so a
	// developer who hits both sees one story.
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	// And it says whose problem it is. "Nothing is wrong with your account"
	// matters because every other refusal on this endpoint is about the
	// account.
	body := rr.Body.String()
	if !strings.Contains(body, "not switched on yet") {
		t.Errorf("the message should say the API is not switched on, got %s", body)
	}
	if strings.Contains(body, "plaintextKey") {
		t.Error("a refused create must not return a key")
	}
}

func TestCreateKey_WritesNothingWhenPepperMissing(t *testing.T) {
	// The real damage is a stored key, not the response: a key row written now
	// is a row whose hash can never match anything later.
	f := newConsoleFixture(t)
	withPepper(t, "")
	before := len(f.keyDB.keys)

	req := f.request(http.MethodPost, "/api/v1/developer/keys",
		map[string]interface{}{"name": "test1", "scopes": []string{models.ScopeCommunitiesProvision}}, nil)
	f.api.CreateDeveloperKeyHandler(httptest.NewRecorder(), req)

	if len(f.keyDB.keys) != before {
		t.Fatalf("expected no key to be written, went from %d to %d", before, len(f.keyDB.keys))
	}
	if len(f.grantDB.grants) != 0 {
		t.Errorf("expected no grant to be written, got %d", len(f.grantDB.grants))
	}
}

func TestRotateKey_RefusedWhenPepperMissing(t *testing.T) {
	// Rotation hands over a new secret, so it is the same hazard as creation.
	f := newConsoleFixture(t)
	withPepper(t, "")

	req := f.request(http.MethodPost, "/api/v1/developer/keys/"+f.keyID.Hex()+"/rotate", nil,
		map[string]string{"keyId": f.keyID.Hex()})
	rr := httptest.NewRecorder()
	f.api.RotateDeveloperKeyHandler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "plaintextKey") {
		t.Error("a refused rotation must not return a key")
	}
	// The existing key must be left alone: a rotation that refuses halfway
	// would revoke the working key and hand back nothing.
	if f.keyDB.keys[f.keyID.Hex()].Status != models.DeveloperKeyActive {
		t.Error("the existing key should still be active after a refused rotation")
	}
}

func TestCreateKey_WorksOncePepperIsSet(t *testing.T) {
	// The guard must be about the pepper and nothing else, or it becomes a
	// second way for key creation to fail.
	f := newConsoleFixture(t)
	withPepper(t, "a-real-pepper-value-for-tests")
	community := f.addCommunity("My Server", true)

	req := f.request(http.MethodPost, "/api/v1/developer/keys",
		map[string]interface{}{
			"name":        "test1",
			"scopes":      []string{models.ScopeCallsRead},
			"communityId": community.Hex(),
		}, nil)
	rr := httptest.NewRecorder()
	f.api.CreateDeveloperKeyHandler(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "plaintextKey") {
		t.Error("a successful create returns the secret exactly once")
	}
}

func TestAccountResponse_ReportsWhetherKeysCanBeIssued(t *testing.T) {
	// The console needs to know, so it can show the state rather than offering
	// a button whose result would be a dead key.
	for _, tc := range []struct {
		name   string
		pepper string
		want   bool
	}{
		{"configured", "a-real-pepper-value-for-tests", true},
		{"not configured", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConsoleFixture(t)
			withPepper(t, tc.pepper)

			req := f.request(http.MethodGet, "/api/v1/developer/account", nil, nil)
			rr := httptest.NewRecorder()
			f.api.GetDeveloperAccountHandler(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
			}
			var out struct {
				KeyIssuanceAvailable bool `json:"keyIssuanceAvailable"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if out.KeyIssuanceAvailable != tc.want {
				t.Errorf("keyIssuanceAvailable = %v, want %v", out.KeyIssuanceAvailable, tc.want)
			}
		})
	}
}

// ---- the header the docs promise ----

// TestDocumentedHeaderIsTheHeaderWeAccept pins the one header the middleware
// reads.
//
// The console told developers to send "X-Developer-Key", which this API has
// never accepted; every request built from that instruction got a 401 saying
// something else entirely. The copy is fixed, and this is here so the two
// cannot drift apart again: if the accepted header changes, this fails and the
// documentation has to change with it.
func TestDocumentedHeaderIsTheHeaderWeAccept(t *testing.T) {
	const documented = "Authorization"

	r := httptest.NewRequest(http.MethodGet, "/api/v1/dev/whoami", nil)
	r.Header.Set(documented, "Bearer "+models.DeveloperAPIKeyPrefix+"abcdef")
	if _, ok := bearerDeveloperKey(r); !ok {
		t.Fatalf("the middleware does not read the header the docs tell developers to send (%q)", documented)
	}

	// And the header the console used to name is not a second way in, so the
	// docs naming only one of them is honest.
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/dev/whoami", nil)
	r2.Header.Set("X-Developer-Key", models.DeveloperAPIKeyPrefix+"abcdef")
	if _, ok := bearerDeveloperKey(r2); ok {
		t.Error("X-Developer-Key is accepted after all; the documentation decision needs revisiting")
	}
}

// TestUnauthorizedMessageNamesTheRealHeader keeps the error a developer sees
// pointing at the scheme that works.
func TestUnauthorizedMessageNamesTheRealHeader(t *testing.T) {
	f := newDevFixture(t, models.ScopeCommunityRead)
	// Pepper is set by the fixture, so this reaches the auth check rather than
	// the configuration refusal.
	handler := f.api.RequireDeveloperAPIKey(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rr := httptest.NewRecorder()
	req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/v1/dev/whoami", nil), nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Authorization: Bearer") {
		t.Errorf("the 401 should name the header that works, got %s", body)
	}
	if strings.Contains(body, "X-Developer-Key") {
		t.Errorf("the 401 should not mention a header we do not accept, got %s", body)
	}
}
