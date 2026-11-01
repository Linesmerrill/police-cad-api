package handlers

import (
	"context"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/linesmerrill/police-cad-api/models"
)

// Verification of Google and Apple ID tokens.
//
// An ID token is a bearer assertion from someone else's server, so everything
// in it is a claim until checked. This file is the only place that reads one,
// and it answers exactly one question: which provider subject does this token
// prove, if any. Nothing downstream sees an unverified field.
//
// What is checked, and why each one matters on its own:
//
//   - the signature, against the provider's current published keys, so the
//     token is one they issued and not one the caller wrote;
//   - the issuer, so a token signed by one provider cannot be presented as
//     another's;
//   - the audience, so a token minted for a different application — including
//     somebody else's app that the same person also signed into — is not
//     accepted here;
//   - expiry, so an old token cannot be replayed;
//   - the nonce, against the value we generated for this one sign-in, so a
//     token captured elsewhere cannot be replayed into our flow.
//
// The nonce is required for both providers. Apple documents it as the
// protection against replay; Google leaves it optional, but we always send one,
// so a token arriving without it did not come from a flow we started.

const (
	googleOAuthClientIDEnv = "GOOGLE_OAUTH_CLIENT_ID"
	appleOAuthClientIDEnv  = "APPLE_OAUTH_CLIENT_ID"
)

const (
	googleJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"
	appleJWKSURL  = "https://appleid.apple.com/auth/keys"
)

// jwksTTL bounds how long a fetched signing key is reused.
//
// Providers rotate keys on their own schedule and publish the new one before
// retiring the old, so a short cache is safe and keeps us off their endpoint on
// every sign-in. A key the cache does not hold triggers one refresh, which is
// what makes rotation a non-event.
const jwksTTL = time.Hour

// oidcMaxTokenLen bounds an ID token before any parsing.
//
// A few kilobytes is generous for a real one. The point is to refuse an
// unbounded body outright rather than hand it to the parser.
const oidcMaxTokenLen = 8192

// oidcProvider is one issuer we accept tokens from.
type oidcProvider struct {
	Name      string
	Issuers   []string
	Audiences []string
	JWKSURL   string
}

// verifiedIdentity is what a valid token proves. Every field here came out of a
// token whose signature, issuer, audience, expiry and nonce all checked out.
type verifiedIdentity struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	PrivateRelay  bool
	Name          string
}

// oidcVerifier verifies ID tokens. HTTPClient and Now are fields rather than
// package calls so a test can serve its own keys and control the clock.
type oidcVerifier struct {
	HTTPClient *http.Client
	Now        func() time.Time

	// Providers overrides the configured set. Only tests set it.
	Providers map[string]oidcProvider

	mu    sync.Mutex
	cache map[string]cachedJWK
}

type cachedJWK struct {
	key       *rsa.PublicKey
	fetchedAt time.Time
}

func newOIDCVerifier() *oidcVerifier {
	return &oidcVerifier{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		Now:        time.Now,
		cache:      map[string]cachedJWK{},
	}
}

func (v *oidcVerifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *oidcVerifier) httpClient() *http.Client {
	if v.HTTPClient != nil {
		return v.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// splitClientIDs reads a comma-separated audience list.
//
// One provider can have several client ids for one product — a web client, an
// iOS client, an Android client — and a token is minted for whichever one the
// person signed in through. All of ours are equally valid here; anyone else's
// is not.
func splitClientIDs(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// provider returns the configuration for a provider, or an error saying it is
// not configured.
//
// Audiences come from the environment and are never defaulted. A verifier with
// no audience would accept any token the provider ever signed, so an unset
// client id has to be a refusal rather than a permissive fallback.
func (v *oidcVerifier) provider(name string) (oidcProvider, error) {
	if v.Providers != nil {
		p, ok := v.Providers[name]
		if !ok {
			return oidcProvider{}, fmt.Errorf("provider is not configured")
		}
		if len(p.Audiences) == 0 {
			return oidcProvider{}, fmt.Errorf("provider is not configured")
		}
		return p, nil
	}

	switch name {
	case models.AuthProviderGoogle:
		aud := splitClientIDs(os.Getenv(googleOAuthClientIDEnv))
		if len(aud) == 0 {
			return oidcProvider{}, fmt.Errorf("provider is not configured")
		}
		return oidcProvider{
			Name: models.AuthProviderGoogle,
			// Google has issued tokens under both spellings for years and
			// treats them as equivalent. Both are accepted; nothing else is.
			Issuers:   []string{"https://accounts.google.com", "accounts.google.com"},
			Audiences: aud,
			JWKSURL:   googleJWKSURL,
		}, nil
	case models.AuthProviderApple:
		aud := splitClientIDs(os.Getenv(appleOAuthClientIDEnv))
		if len(aud) == 0 {
			return oidcProvider{}, fmt.Errorf("provider is not configured")
		}
		return oidcProvider{
			Name:      models.AuthProviderApple,
			Issuers:   []string{"https://appleid.apple.com"},
			Audiences: aud,
			JWKSURL:   appleJWKSURL,
		}, nil
	}
	return oidcProvider{}, fmt.Errorf("provider is not configured")
}

// SocialSignInConfigured reports whether at least one provider is usable.
//
// Used to decide whether to offer the buttons at all, so a deployment without
// client ids shows a password form rather than a button that cannot work.
func (v *oidcVerifier) SocialSignInConfigured() (google bool, apple bool) {
	_, gerr := v.provider(models.AuthProviderGoogle)
	_, aerr := v.provider(models.AuthProviderApple)
	return gerr == nil, aerr == nil
}

// jwksResponse is the subset of a JWKS document we use.
type jwksResponse struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		Use string `json:"use"`
		Alg string `json:"alg"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

// fetchJWKS replaces every cached key for a provider.
//
// Whole-document replacement rather than a per-kid fetch, because that is the
// shape providers publish and it keeps a rotated-out key from lingering.
func (v *oidcVerifier) fetchJWKS(ctx context.Context, p oidcProvider) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.JWKSURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("fetching signing keys: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("signing keys returned %d", resp.StatusCode)
	}

	var doc jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("parsing signing keys: %w", err)
	}

	now := v.now()
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cache == nil {
		v.cache = map[string]cachedJWK{}
	}
	added := 0
	for _, k := range doc.Keys {
		// Only RSA signing keys. Both providers sign ID tokens with RS256; a
		// document entry for anything else is not ours to use.
		if k.Kty != "RSA" || k.Kid == "" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		key, err := jwkToRSA(k.N, k.E)
		if err != nil {
			continue
		}
		v.cache[p.Name+"|"+k.Kid] = cachedJWK{key: key, fetchedAt: now}
		added++
	}
	if added == 0 {
		return errors.New("signing keys contained no usable key")
	}
	return nil
}

func jwkToRSA(nB64, eB64 string) (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(nB64, "="))
	if err != nil || len(nb) == 0 {
		return nil, errors.New("bad modulus")
	}
	eb, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(eB64, "="))
	if err != nil || len(eb) == 0 {
		return nil, errors.New("bad exponent")
	}
	e := new(big.Int).SetBytes(eb)
	if !e.IsInt64() || e.Int64() <= 0 {
		return nil, errors.New("bad exponent")
	}
	key := &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: int(e.Int64())}
	// A short modulus is not a key we should be verifying anything with.
	if key.N.BitLen() < 2048 {
		return nil, errors.New("signing key is too small")
	}
	return key, nil
}

// signingKey returns the key for a kid, refreshing once if it is unknown or stale.
//
// The single refresh is what makes key rotation invisible: the first token
// signed with a new key misses the cache, one fetch brings the whole current
// document in, and the token verifies. It also means an unknown kid costs at
// most one request rather than one per attempt, because the refresh repopulates
// the cache either way.
func (v *oidcVerifier) signingKey(ctx context.Context, p oidcProvider, kid string) (*rsa.PublicKey, error) {
	if kid == "" {
		return nil, errors.New("token has no key id")
	}
	cacheKey := p.Name + "|" + kid

	v.mu.Lock()
	cached, ok := v.cache[cacheKey]
	v.mu.Unlock()
	if ok && v.now().Sub(cached.fetchedAt) <= jwksTTL {
		return cached.key, nil
	}

	if err := v.fetchJWKS(ctx, p); err != nil {
		// A stale key still beats failing every sign-in while the provider's
		// key endpoint is unreachable. It is the same key material; only our
		// copy's age is in question.
		if ok {
			return cached.key, nil
		}
		return nil, err
	}

	v.mu.Lock()
	cached, ok = v.cache[cacheKey]
	v.mu.Unlock()
	if !ok {
		return nil, errors.New("token was signed with an unknown key")
	}
	return cached.key, nil
}

// Verify checks an ID token and returns what it proves.
//
// expectedNonce is the value we generated when starting this sign-in. It is
// required: a token with no nonce, or a different one, did not come from a flow
// we started.
//
// Every error returned here is deliberately uninformative to the caller about
// which check failed. The handler turns it into one generic refusal, so a
// caller cannot use the response to learn which part of a forged token to fix.
func (v *oidcVerifier) Verify(ctx context.Context, provider, rawToken, expectedNonce string) (*verifiedIdentity, error) {
	providerName, err := safeAuthProvider(provider)
	if err != nil {
		return nil, err
	}
	p, err := v.provider(providerName)
	if err != nil {
		return nil, err
	}

	token := strings.TrimSpace(rawToken)
	if token == "" {
		return nil, errors.New("no token")
	}
	if len(token) > oidcMaxTokenLen {
		return nil, errors.New("token is too large")
	}
	nonce := strings.TrimSpace(expectedNonce)
	if nonce == "" {
		return nil, errors.New("no nonce")
	}

	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		// RS256 only. Without this a token could name "none" or a symmetric
		// algorithm and be "verified" against the public key as a secret.
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithTimeFunc(v.now),
		jwt.WithExpirationRequired(),
	)
	if _, err := parser.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		return v.signingKey(ctx, p, kid)
	}); err != nil {
		return nil, fmt.Errorf("token did not verify: %w", err)
	}

	iss, _ := claims["iss"].(string)
	if !containsString(p.Issuers, iss) {
		return nil, errors.New("token issuer is not accepted")
	}

	if !audienceMatches(claims["aud"], p.Audiences) {
		return nil, errors.New("token was not issued for this application")
	}

	gotNonce, _ := claims["nonce"].(string)
	if subtle.ConstantTimeCompare([]byte(gotNonce), []byte(nonce)) != 1 {
		return nil, errors.New("token nonce does not match this sign-in")
	}

	subject, err := safeAuthSubject(claims["sub"])
	if err != nil {
		return nil, err
	}

	identity := &verifiedIdentity{
		Provider:      providerName,
		Subject:       subject,
		EmailVerified: claimTruthy(claims["email_verified"]),
		PrivateRelay:  claimTruthy(claims["is_private_email"]),
		Name:          safeAuthDisplayName(claims["name"]),
	}
	if email, ok := claims["email"].(string); ok {
		identity.Email = normalizeAuthEmail(email)
	}
	// An address the provider will not vouch for is not usable for the one
	// thing we would use it for, which is matching an existing account. Keep
	// the subject, drop the address.
	if !identity.EmailVerified {
		identity.Email = ""
	}
	return identity, nil
}

// audienceMatches checks the aud claim against our client ids.
//
// `aud` is a string in practice and a string-or-array by specification, so both
// shapes are handled. Anything else is not an audience we can read, and an
// unreadable audience is a failed check, never a passed one.
func audienceMatches(raw interface{}, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	switch aud := raw.(type) {
	case string:
		return containsString(allowed, aud)
	case []interface{}:
		for _, entry := range aud {
			if s, ok := entry.(string); ok && containsString(allowed, s) {
				return true
			}
		}
	case []string:
		for _, s := range aud {
			if containsString(allowed, s) {
				return true
			}
		}
	}
	return false
}

// claimTruthy reads a boolean claim that may arrive as a bool or a string.
//
// Google has sent `email_verified` as the string "true", and Apple sends
// `is_private_email` as a string in some flows. Reading only the bool would
// silently treat a verified address as unverified.
func claimTruthy(raw interface{}) bool {
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}
