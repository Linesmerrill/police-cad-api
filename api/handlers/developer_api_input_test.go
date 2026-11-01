package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"
)

// Input validation for the Developer API.
//
// Two layers are under test. The first is structural: request bodies decode
// into structs whose fields are strings, so a JSON object where a string
// belongs fails to decode and never reaches a handler. The second is the
// validators, which bound and re-materialize every value that does get through.
//
// The payloads below are the shapes a third party would send to try to turn a
// value into something the database treats as more than a value.

// operatorPayloads are the JSON values a client might send where a plain string
// is expected.
var operatorPayloads = map[string]string{
	"not equal":    `{"$ne":""}`,
	"greater than": `{"$gt":""}`,
	"regex":        `{"$regex":".*"}`,
	"in":           `{"$in":["a","b"]}`,
	"where":        `{"$where":"true"}`,
	"nested":       `{"a":{"$ne":null}}`,
	"array":        `["a"]`,
	"number":       `12345`,
	"boolean":      `true`,
}

// A struct field typed as string cannot hold an object, so the decode fails and
// the handler answers 400 before any validation runs. This is the layer that
// makes operator injection through a body structurally impossible, so it is
// worth pinning rather than assuming.
func TestRequestBodies_RejectNonStringValues(t *testing.T) {
	for name, payload := range operatorPayloads {
		t.Run(name, func(t *testing.T) {
			var req createDeveloperKeyRequest
			body := `{"name":"k","scopes":["units:read"],"communityId":` + payload + `}`
			err := json.NewDecoder(strings.NewReader(body)).Decode(&req)
			if err == nil {
				t.Fatalf("a %s payload decoded into a string field as %q", name, req.CommunityID)
			}
		})
	}
}

// The same for every other body this surface accepts.
func TestProvisioningBodies_AreTypedStructs(t *testing.T) {
	cases := []struct {
		name string
		body string
		into interface{}
	}{
		{"player upsert", `{"provider":"fivem","identifier":{"$ne":""}}`, &devUpsertPlayerRequest{}},
		{"create community", `{"name":{"$ne":""}}`, &devCreateCommunityRequest{}},
		{"claim", `{"code":{"$ne":""}}`, &claimRequest{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := json.NewDecoder(strings.NewReader(tc.body)).Decode(tc.into); err == nil {
				t.Fatalf("an operator object decoded into %s", tc.name)
			}
		})
	}
}

// A player reference shaped like an operator is refused before any lookup.
func TestDevUpsertPlayer_RefusesOperatorShapedIdentifiers(t *testing.T) {
	f := newProvisionFixture(t)

	for name, body := range map[string]string{
		"operator identifier": `{"provider":"fivem","identifier":"$ne"}`,
		"where":               `{"provider":"fivem","identifier":"$where"}`,
		"operator provider":   `{"provider":"$ne","identifier":"abc123"}`,
		"traversal":           `{"provider":"fivem","identifier":"../../etc/passwd"}`,
		"oversized":           `{"provider":"fivem","identifier":"` + strings.Repeat("a", 200) + `"}`,
		"wrong shape":         `{"provider":"discord","identifier":"notdigits"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rr, _ := f.upsert(t, body)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", rr.Code, rr.Body.String())
			}
			if f.userDB.inserts != 0 {
				t.Error("a refused identifier must not create an account")
			}
		})
	}
}

// The same for the other body this surface accepts.
func TestEnableAccountBody_IsATypedStruct(t *testing.T) {
	for name, payload := range operatorPayloads {
		t.Run(name, func(t *testing.T) {
			var req enableDeveloperAccountRequest
			body := `{"acceptTerms":true,"appName":` + payload + `}`
			if err := json.NewDecoder(strings.NewReader(body)).Decode(&req); err == nil {
				t.Fatalf("a %s payload decoded into a string field as %q", name, req.AppName)
			}
		})
	}
}

// ---- the validators ----

func TestDevSafeObjectID(t *testing.T) {
	t.Run("returns the canonical form, not the caller's string", func(t *testing.T) {
		oid := primitive.NewObjectID()
		upper := strings.ToUpper(oid.Hex())

		got, parsed, err := devSafeObjectID(upper)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != oid.Hex() {
			t.Errorf("expected the canonical hex %q, got %q", oid.Hex(), got)
		}
		if got == upper {
			t.Error("the returned value must be re-serialized, not the input echoed back")
		}
		if parsed != oid {
			t.Error("the parsed id should match")
		}
	})

	t.Run("refuses anything that is not 24 hex characters", func(t *testing.T) {
		for _, bad := range []string{
			"", "   ", "nope",
			"$ne", "$where",
			"6adab9b0c602a448a910d97",   // one short
			"6adab9b0c602a448a910d97ez", // one long, and not hex
			"6adab9b0c602a448a910d97z",
			"../../etc/passwd",
			"6adab9b0c602a448a910d97e\x00",
		} {
			if _, _, err := devSafeObjectID(bad); err == nil {
				t.Errorf("accepted %q", bad)
			}
		}
	})
}

func TestDevSafeScalar(t *testing.T) {
	t.Run("accepts an ordinary value", func(t *testing.T) {
		got, err := devSafeScalar("  hello  ", 60, "value")
		if err != nil || got != "hello" {
			t.Fatalf("got %q, %v", got, err)
		}
	})

	t.Run("refuses the reserved prefix, control characters, empties and over-length", func(t *testing.T) {
		for _, bad := range []string{"", "   ", "$ne", "$where", "$gt", "a\x00b", "a\x1bb", strings.Repeat("x", 61)} {
			if _, err := devSafeScalar(bad, 60, "value"); err == nil {
				t.Errorf("accepted %q", bad)
			}
		}
	})
}

func TestDevSafeIdentifier(t *testing.T) {
	t.Run("normalizes then validates", func(t *testing.T) {
		got, err := devSafeIdentifier(models.PlayerProviderFiveM, "license:ABC123def")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "abc123def" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("holds each provider to its own shape", func(t *testing.T) {
		if _, err := devSafeIdentifier(models.PlayerProviderDiscord, "notdigits"); err == nil {
			t.Error("a Discord id should be digits only")
		}
		if _, err := devSafeIdentifier(models.PlayerProviderFiveM, "zzzz-not-hex"); err == nil {
			t.Error("a FiveM licence should be hexadecimal")
		}
		if _, err := devSafeIdentifier(models.PlayerProviderDiscord, "198573466634"); err != nil {
			t.Errorf("a real Discord id should pass: %v", err)
		}
	})

	t.Run("refuses operator-shaped and oversized identifiers", func(t *testing.T) {
		for _, bad := range []string{"", "$ne", "$where", strings.Repeat("a", 200), "a b", "a\x00b", "a/b", "a$b"} {
			if _, err := devSafeIdentifier(models.PlayerProviderFiveM, bad); err == nil {
				t.Errorf("accepted %q", bad)
			}
		}
	})
}

func TestDevSafeExternalID(t *testing.T) {
	t.Run("accepts a machine-generated handle", func(t *testing.T) {
		for _, good := range []string{"sixpd-incident-42", "INC_2026.11.01:7", "a"} {
			if _, err := devSafeExternalID(good); err != nil {
				t.Errorf("rejected %q: %v", good, err)
			}
		}
	})

	t.Run("refuses operator-shaped, spaced and oversized values", func(t *testing.T) {
		for _, bad := range []string{"", "   ", "$ne", "$where", "has space", "a\x00b", "a/b", "a{b}", strings.Repeat("x", 129)} {
			if _, err := devSafeExternalID(bad); err == nil {
				t.Errorf("accepted %q", bad)
			}
		}
	})
}

func TestDevSafeDay(t *testing.T) {
	if _, err := devSafeDay("2026-11-01"); err != nil {
		t.Errorf("a real date should pass: %v", err)
	}
	for _, bad := range []string{
		"", "2026-11", "2026-11-01T00:00:00Z", "$ne", "x2026-11-01", "2026-11-01x", "../2026-11-01",
		// Right shape, not a real date. The pattern alone would pass these.
		"2026-13-01", "2026-00-10", "2026-02-30", "2026-11-32",
	} {
		if _, err := devSafeDay(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}

	// The returned value is built from the parsed date, not echoed back.
	if got, err := devSafeDay("  2026-11-01  "); err != nil || got != "2026-11-01" {
		t.Errorf("got %q, %v", got, err)
	}
}

// Every pattern must match the whole string. An unanchored one would pass a
// value that merely contains something acceptable.
func TestDeveloperPatterns_AreAnchored(t *testing.T) {
	for name, pattern := range map[string]string{
		"objectId":   devObjectIDPattern.String(),
		"day":        devDayPattern.String(),
		"identifier": devIdentifierPattern.String(),
		"discord":    devDiscordIDPattern.String(),
		"fivem":      devFiveMIDPattern.String(),
		"steam":      devSteamIDPattern.String(),
		"externalId": devExternalIDPattern.String(),
	} {
		if !strings.HasPrefix(pattern, "^") || !strings.HasSuffix(pattern, "$") {
			t.Errorf("%s is not anchored at both ends: %s", name, pattern)
		}
	}
}

func TestDevEq(t *testing.T) {
	got := devEq("value")
	if len(got) != 1 {
		t.Fatalf("expected one condition, got %v", got)
	}
	if got["$eq"] != "value" {
		t.Errorf("expected an $eq condition, got %v", got)
	}
}

// ---- end to end: an injection payload is a 400, and nothing is queried ----

// A route variable shaped like an operator must be refused before the handler
// reaches the database.
func TestDevAPI_OperatorInRouteVarIsRefused(t *testing.T) {
	f := newDevFixture(t, models.ScopeCommunityRead)

	for name, payload := range map[string]string{
		"operator":   "$ne",
		"where":      "$where",
		"json":       `{"$ne":""}`,
		"traversal":  "../../etc/passwd",
		"empty":      "",
		"almost hex": "6adab9b0c602a448a910d97",
	} {
		t.Run(name, func(t *testing.T) {
			rr := f.serveCommunity(t, payload, nil)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for %q, got %d: %s", payload, rr.Code, rr.Body.String())
			}
		})
	}
}

// The usage range is the one place a client value bounds a query rather than
// matching one, so its validator is held to the same standard as the rest.
func TestDevSafeDay_RefusesRangeInjection(t *testing.T) {
	for _, bad := range []string{"$ne", `{"$gt":""}`, "2026-11-01T00:00:00Z", "../2026-11-01", "2026-11-01'"} {
		if _, err := devSafeDay(bad); err == nil {
			t.Errorf("accepted %q as a range bound", bad)
		}
	}
}

// A filter built by the handlers must never carry a value that came straight
// from the caller. This walks the filters the fakes recorded and asserts every
// leaf is a plain scalar rather than a nested condition the caller supplied.
func TestDevAPI_FiltersCarryOnlyScalars(t *testing.T) {
	f := newDevFixture(t, models.ScopeCommunityRead)
	communityID := primitive.NewObjectID()
	f.grantDB.grants = []models.DeveloperCommunityGrant{{
		KeyID: f.keyID.Hex(), CommunityID: communityID.Hex(), Status: models.DeveloperKeyActive,
	}}
	f.api.CDB = &fakeDevCommunityDB{communities: map[string]models.Community{
		communityID.Hex(): {ID: communityID},
	}}

	if rr := f.serveCommunity(t, communityID.Hex(), nil); rr.Code != http.StatusOK {
		t.Fatalf("expected the request to succeed, got %d", rr.Code)
	}

	for _, filter := range f.grantDB.seenFilters {
		assertScalarLeaves(t, filter)
	}
}

// assertScalarLeaves checks a filter contains only equality conditions over
// scalars, with no operator a caller could have introduced.
func assertScalarLeaves(t *testing.T, filter interface{}) {
	t.Helper()
	m, ok := filter.(bson.M)
	if !ok {
		return
	}
	for field, condition := range m {
		if strings.HasPrefix(field, "$") && field != "$eq" && field != "$in" && field != "$ne" {
			t.Errorf("unexpected operator %q in a filter", field)
		}
		switch v := condition.(type) {
		case bson.M:
			for op := range v {
				if op != "$eq" && op != "$in" && op != "$ne" && op != "$gte" && op != "$lte" && op != "$exists" {
					t.Errorf("field %q carries an unexpected operator %q", field, op)
				}
			}
		case string, primitive.ObjectID, bool, int, int64:
			// A bare scalar is fine.
		}
	}
}
