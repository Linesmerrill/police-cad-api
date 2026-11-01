package handlers

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/linesmerrill/police-cad-api/models"
)

// A provider's own signing key, served from a JWKS endpoint we control, so
// these tests exercise the real parse-and-verify path rather than a stub of it.
// Every token below is genuinely signed; the ones that must be refused are
// refused on their merits.

const (
	testIssuer   = "https://test-provider.example"
	testAudience = "lpc-test-client-id.apps.example"
	testKID      = "test-key-1"
	testNonce    = "n0nce-value-abcdef0123456789"
)

type oidcFixture struct {
	key      *rsa.PrivateKey
	other    *rsa.PrivateKey
	verifier *oidcVerifier
	now      time.Time
	jwks     *httptest.Server
	fetches  int
}

func newOIDCFixture(t *testing.T) *oidcFixture {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a signing key: %v", err)
	}
	// A second, unrelated key. Tokens signed with this one are correctly formed
	// and verify against nothing we publish, which is exactly the forgery case.
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating the second key: %v", err)
	}

	f := &oidcFixture{key: key, other: other, now: time.Now()}

	f.jwks = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.fetches++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": []map[string]string{{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": testKID,
				"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
			}},
		})
	}))
	t.Cleanup(f.jwks.Close)

	f.verifier = &oidcVerifier{
		HTTPClient: f.jwks.Client(),
		Now:        func() time.Time { return f.now },
		cache:      map[string]cachedJWK{},
		Providers: map[string]oidcProvider{
			models.AuthProviderGoogle: {
				Name:      models.AuthProviderGoogle,
				Issuers:   []string{testIssuer},
				Audiences: []string{testAudience},
				JWKSURL:   f.jwks.URL,
			},
			models.AuthProviderApple: {
				Name:      models.AuthProviderApple,
				Issuers:   []string{testIssuer},
				Audiences: []string{testAudience},
				JWKSURL:   f.jwks.URL,
			},
		},
	}
	return f
}

// mint signs a token with the published key unless told otherwise.
func (f *oidcFixture) mint(t *testing.T, claims jwt.MapClaims, opts ...func(*mintOpts)) string {
	t.Helper()
	o := mintOpts{key: f.key, kid: testKID, method: jwt.SigningMethodRS256}
	for _, apply := range opts {
		apply(&o)
	}
	if _, ok := claims["iss"]; !ok {
		claims["iss"] = testIssuer
	}
	if _, ok := claims["aud"]; !ok {
		claims["aud"] = testAudience
	}
	if _, ok := claims["sub"]; !ok {
		claims["sub"] = "1234567890"
	}
	if _, ok := claims["nonce"]; !ok {
		claims["nonce"] = testNonce
	}
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = f.now.Add(10 * time.Minute).Unix()
	}
	if _, ok := claims["iat"]; !ok {
		claims["iat"] = f.now.Add(-time.Minute).Unix()
	}
	token := jwt.NewWithClaims(o.method, claims)
	token.Header["kid"] = o.kid
	signed, err := token.SignedString(o.key)
	if err != nil {
		t.Fatalf("signing the test token: %v", err)
	}
	return signed
}

type mintOpts struct {
	key    interface{}
	kid    string
	method jwt.SigningMethod
}

func signedWithOtherKey(f *oidcFixture) func(*mintOpts) {
	return func(o *mintOpts) { o.key = f.other }
}

func withKID(kid string) func(*mintOpts) {
	return func(o *mintOpts) { o.kid = kid }
}

// ---- the checks that make a token trustworthy ----

func TestOIDCVerify_AcceptsAValidToken(t *testing.T) {
	f := newOIDCFixture(t)

	identity, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle,
		f.mint(t, jwt.MapClaims{
			"sub":            "109876543210987654321",
			"email":          "Dev@Example.com",
			"email_verified": true,
			"name":           "Dev Example",
		}), testNonce)
	if err != nil {
		t.Fatalf("a valid token must verify, got: %v", err)
	}
	if identity.Subject != "109876543210987654321" {
		t.Errorf("subject: got %q", identity.Subject)
	}
	// The address is normalized on the way out, so a later comparison against a
	// stored address cannot miss on capitalisation alone.
	if identity.Email != "dev@example.com" {
		t.Errorf("email should be normalized, got %q", identity.Email)
	}
	if !identity.EmailVerified {
		t.Error("email_verified: true must be carried through")
	}
	if identity.Name != "Dev Example" {
		t.Errorf("name: got %q", identity.Name)
	}
	if identity.Provider != models.AuthProviderGoogle {
		t.Errorf("provider: got %q", identity.Provider)
	}
}

func TestOIDCVerify_RefusesAnExpiredToken(t *testing.T) {
	f := newOIDCFixture(t)

	token := f.mint(t, jwt.MapClaims{"exp": f.now.Add(-time.Minute).Unix()})
	if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle, token, testNonce); err == nil {
		t.Fatal("an expired token must not verify")
	}

	// And the same token one minute earlier was fine, so it is expiry being
	// caught rather than something else about the token.
	f.now = f.now.Add(-2 * time.Minute)
	if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle, token, testNonce); err != nil {
		t.Fatalf("the same token before its expiry should verify, got: %v", err)
	}
}

func TestOIDCVerify_RefusesATokenWithNoExpiry(t *testing.T) {
	f := newOIDCFixture(t)

	// A token that never expires is a token that can be replayed forever.
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": testIssuer, "aud": testAudience, "sub": "1", "nonce": testNonce,
	})
	token.Header["kid"] = testKID
	signed, err := token.SignedString(f.key)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle, signed, testNonce); err == nil {
		t.Fatal("a token with no exp must not verify")
	}
}

func TestOIDCVerify_RefusesTheWrongAudience(t *testing.T) {
	f := newOIDCFixture(t)

	// A real, correctly signed, unexpired token — minted for somebody else's
	// application. The same person may well have signed into that app.
	for name, aud := range map[string]interface{}{
		"another app":                   "someone-elses-client-id.apps.example",
		"array without ours":            []interface{}{"a.example", "b.example"},
		"empty":                         "",
		"missing":                       nil,
		"object where a string belongs": map[string]interface{}{"$ne": ""},
	} {
		t.Run(name, func(t *testing.T) {
			claims := jwt.MapClaims{}
			if aud != nil {
				claims["aud"] = aud
			} else {
				claims["aud"] = ""
			}
			if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle,
				f.mint(t, claims), testNonce); err == nil {
				t.Fatal("a token for another audience must not verify")
			}
		})
	}
}

func TestOIDCVerify_AcceptsOurAudienceInsideAnArray(t *testing.T) {
	f := newOIDCFixture(t)

	// aud is a string in practice and string-or-array by specification.
	identity, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle,
		f.mint(t, jwt.MapClaims{"aud": []interface{}{"other.example", testAudience}}), testNonce)
	if err != nil {
		t.Fatalf("our audience inside an array must verify, got: %v", err)
	}
	if identity.Subject == "" {
		t.Error("expected a subject")
	}
}

func TestOIDCVerify_RefusesABadSignature(t *testing.T) {
	f := newOIDCFixture(t)

	t.Run("signed with a key we do not publish", func(t *testing.T) {
		token := f.mint(t, jwt.MapClaims{}, signedWithOtherKey(f))
		if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle, token, testNonce); err == nil {
			t.Fatal("a token signed with an unpublished key must not verify")
		}
	})

	t.Run("claims edited after signing", func(t *testing.T) {
		token := f.mint(t, jwt.MapClaims{"sub": "111"})
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			t.Fatalf("expected three segments, got %d", len(parts))
		}
		tampered := base64.RawURLEncoding.EncodeToString([]byte(
			`{"iss":"` + testIssuer + `","aud":"` + testAudience + `","sub":"999","nonce":"` + testNonce + `","exp":99999999999}`))
		forged := parts[0] + "." + tampered + "." + parts[2]
		if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle, forged, testNonce); err == nil {
			t.Fatal("a token whose claims were edited after signing must not verify")
		}
	})

	t.Run("unknown key id", func(t *testing.T) {
		token := f.mint(t, jwt.MapClaims{}, withKID("not-a-key-we-have"))
		if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle, token, testNonce); err == nil {
			t.Fatal("a token naming an unknown key must not verify")
		}
	})

	t.Run("no key id at all", func(t *testing.T) {
		token := f.mint(t, jwt.MapClaims{}, withKID(""))
		if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle, token, testNonce); err == nil {
			t.Fatal("a token with no kid must not verify")
		}
	})
}

func TestOIDCVerify_RefusesAnAlgorithmSwap(t *testing.T) {
	f := newOIDCFixture(t)

	// "none" is the classic: strip the signature and declare there isn't one.
	token := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"iss": testIssuer, "aud": testAudience, "sub": "1",
		"nonce": testNonce, "exp": f.now.Add(time.Hour).Unix(),
	})
	token.Header["kid"] = testKID
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle, signed, testNonce); err == nil {
		t.Fatal("an unsigned token must not verify")
	}
}

func TestOIDCVerify_RefusesANonceMismatch(t *testing.T) {
	f := newOIDCFixture(t)

	t.Run("a different nonce", func(t *testing.T) {
		// A real token from a real sign-in — just not the sign-in we started.
		token := f.mint(t, jwt.MapClaims{"nonce": "some-other-flows-nonce-000001"})
		if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle, token, testNonce); err == nil {
			t.Fatal("a token from another flow must not verify")
		}
	})

	t.Run("no nonce in the token", func(t *testing.T) {
		claims := jwt.MapClaims{"iss": testIssuer, "aud": testAudience, "sub": "1",
			"exp": f.now.Add(time.Hour).Unix()}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = testKID
		signed, err := token.SignedString(f.key)
		if err != nil {
			t.Fatalf("signing: %v", err)
		}
		if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle, signed, testNonce); err == nil {
			t.Fatal("a token with no nonce must not verify")
		}
	})

	t.Run("no nonce expected", func(t *testing.T) {
		// Verification without an expected nonce is refused outright rather
		// than skipping the check.
		if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle, f.mint(t, jwt.MapClaims{}), ""); err == nil {
			t.Fatal("verifying with no expected nonce must not succeed")
		}
	})
}

func TestOIDCVerify_RefusesTheWrongIssuer(t *testing.T) {
	f := newOIDCFixture(t)

	for name, iss := range map[string]string{
		"another issuer": "https://attacker.example",
		"empty":          "",
		"near miss":      testIssuer + ".attacker.example",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle,
				f.mint(t, jwt.MapClaims{"iss": iss}), testNonce); err == nil {
				t.Fatal("a token from another issuer must not verify")
			}
		})
	}
}

func TestOIDCVerify_RefusesAnUnconfiguredProvider(t *testing.T) {
	f := newOIDCFixture(t)
	// With no client id there is no audience to check against, and a verifier
	// with no audience would accept every token the provider ever signed.
	f.verifier.Providers = map[string]oidcProvider{
		models.AuthProviderGoogle: {
			Name: models.AuthProviderGoogle, Issuers: []string{testIssuer},
			Audiences: nil, JWKSURL: f.jwks.URL,
		},
	}
	if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle,
		f.mint(t, jwt.MapClaims{}), testNonce); err == nil {
		t.Fatal("an unconfigured provider must refuse, not accept everything")
	}
}

// ---- claim shapes a provider actually sends ----

func TestOIDCVerify_HandlesAppleRepeatSignIn(t *testing.T) {
	f := newOIDCFixture(t)

	// Apple sends a name only on the very first authorization, and a relay
	// address for "Hide My Email". This is the shape of every sign-in after the
	// first, and it has to work.
	identity, err := f.verifier.Verify(context.Background(), models.AuthProviderApple,
		f.mint(t, jwt.MapClaims{
			"sub":              "001234.abcdef0123456789abcdef.0123",
			"email":            "xyz987@privaterelay.appleid.com",
			"email_verified":   "true",
			"is_private_email": "true",
		}), testNonce)
	if err != nil {
		t.Fatalf("a returning Apple user must verify, got: %v", err)
	}
	if identity.Name != "" {
		t.Errorf("no name is the normal case here, got %q", identity.Name)
	}
	if !identity.PrivateRelay {
		t.Error("is_private_email as a string must be read as true")
	}
	if !identity.EmailVerified {
		t.Error("email_verified as a string must be read as true")
	}
	if identity.Subject != "001234.abcdef0123456789abcdef.0123" {
		t.Errorf("Apple's dotted subject must survive intact, got %q", identity.Subject)
	}
}

func TestOIDCVerify_DropsAnUnverifiedEmail(t *testing.T) {
	f := newOIDCFixture(t)

	// An address the provider will not vouch for cannot be used for the one
	// thing we would use it for, so it is discarded rather than carried.
	identity, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle,
		f.mint(t, jwt.MapClaims{"email": "unverified@example.com", "email_verified": false}), testNonce)
	if err != nil {
		t.Fatalf("the token itself is fine, got: %v", err)
	}
	if identity.Email != "" {
		t.Errorf("an unverified address must not be carried, got %q", identity.Email)
	}
}

func TestOIDCVerify_RefusesOperatorShapedClaims(t *testing.T) {
	f := newOIDCFixture(t)

	// A signed token still carries caller-controlled strings, and `sub` is the
	// one that reaches a filter.
	for name, sub := range map[string]interface{}{
		"operator":  "$ne",
		"where":     "$where",
		"object":    map[string]interface{}{"$ne": ""},
		"array":     []interface{}{"1"},
		"number":    12345,
		"empty":     "",
		"oversized": strings.Repeat("a", 300),
		"spaced":    "has space",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle,
				f.mint(t, jwt.MapClaims{"sub": sub}), testNonce); err == nil {
				t.Fatalf("subject %v must be refused", sub)
			}
		})
	}
}

func TestOIDCVerify_RefusesAnOversizedToken(t *testing.T) {
	f := newOIDCFixture(t)
	if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle,
		strings.Repeat("a", oidcMaxTokenLen+1), testNonce); err == nil {
		t.Fatal("an oversized token must be refused before parsing")
	}
}

// ---- key handling ----

func TestOIDCVerify_CachesKeysAndRefreshesOnRotation(t *testing.T) {
	f := newOIDCFixture(t)

	for i := 0; i < 3; i++ {
		if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle,
			f.mint(t, jwt.MapClaims{}), testNonce); err != nil {
			t.Fatalf("verify %d: %v", i, err)
		}
	}
	if f.fetches != 1 {
		t.Errorf("three sign-ins should cost one key fetch, got %d", f.fetches)
	}

	// Past the TTL the key is refetched rather than trusted indefinitely.
	f.now = f.now.Add(jwksTTL + time.Minute)
	if _, err := f.verifier.Verify(context.Background(), models.AuthProviderGoogle,
		f.mint(t, jwt.MapClaims{}), testNonce); err != nil {
		t.Fatalf("after the TTL: %v", err)
	}
	if f.fetches != 2 {
		t.Errorf("expected a refetch after the TTL, got %d fetches", f.fetches)
	}
}

func TestJWKToRSA_RefusesAWeakKey(t *testing.T) {
	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	_, err = jwkToRSA(
		base64.RawURLEncoding.EncodeToString(small.PublicKey.N.Bytes()),
		base64.RawURLEncoding.EncodeToString(big.NewInt(int64(small.PublicKey.E)).Bytes()))
	if err == nil {
		t.Fatal("a 1024-bit key must not be accepted as a signing key")
	}
}

func TestSplitClientIDs(t *testing.T) {
	// One product can have a web, iOS and Android client id, and a token is
	// minted for whichever one was used.
	got := splitClientIDs(" web.example , ios.example ,, android.example ")
	want := []string{"web.example", "ios.example", "android.example"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %q want %q", i, got[i], want[i])
		}
	}
	if len(splitClientIDs("  ,  ")) != 0 {
		t.Error("a blank setting must yield no audiences, so the provider reads as unconfigured")
	}
}
