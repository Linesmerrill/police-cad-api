package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"
)

// Tests for the app's sign-in route, POST /auth/social/token.
//
// What separates it from the website's verify route is the session it hands
// back, so most of these assert on the token: that it is there when the person
// signed in, that it is not there when they have only proved a provider
// account, and that an account the password form would refuse does not get one
// either.

// tokenFixture is a social fixture whose session minting is recorded rather
// than stored, so these tests assert the handler's decisions without standing
// up go-guardian and Mongo.
type tokenFixture struct {
	*socialFixture
	minted   []string // the account ids a token was issued for, in order
	mintFail error
}

func newTokenFixture(t *testing.T) *tokenFixture {
	t.Helper()
	f := &tokenFixture{socialFixture: newSocialFixture(t)}
	f.api.IssueToken = func(_ *http.Request, _, userID string) (string, error) {
		if f.mintFail != nil {
			return "", f.mintFail
		}
		f.minted = append(f.minted, userID)
		return "session-for-" + userID, nil
	}
	return f
}

func (f *tokenFixture) signIn(t *testing.T, provider string, claims jwt.MapClaims) *httptest.ResponseRecorder {
	t.Helper()
	return f.post(t, f.api.SocialTokenHandler, "/auth/social/token",
		f.verifyBody(t, provider, claims), "")
}

func decodeSocialToken(t *testing.T, rr *httptest.ResponseRecorder) socialTokenResponse {
	t.Helper()
	var out socialTokenResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding the response: %v (%s)", err, rr.Body.String())
	}
	return out
}

// linkIdentity attaches a provider subject to an account.
func (f *tokenFixture) linkIdentity(provider, subject, userID string) {
	f.authDB.rows = append(f.authDB.rows, models.AuthIdentity{
		ID: primitive.NewObjectID(), Provider: provider,
		Subject: subject, UserID: userID,
	})
}

func TestSocialToken_SignsInAndMintsASession(t *testing.T) {
	f := newTokenFixture(t)
	user := f.passwordUser("dev@example.com")
	f.linkIdentity(models.AuthProviderGoogle, "sub-known", user.ID)

	rr := f.signIn(t, models.AuthProviderGoogle, jwt.MapClaims{"sub": "sub-known"})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeSocialToken(t, rr)
	if out.Outcome != socialOutcomeSignedIn {
		t.Errorf("outcome: got %q want %q", out.Outcome, socialOutcomeSignedIn)
	}
	if out.UserID != user.ID {
		t.Errorf("signed in as the wrong account: %q", out.UserID)
	}
	if out.Token != "session-for-"+user.ID {
		t.Errorf("token: got %q", out.Token)
	}
	if out.Email != "dev@example.com" {
		t.Errorf("email: got %q", out.Email)
	}
	if f.userDB.inserts != 0 {
		t.Error("signing in must not create an account")
	}
}

func TestSocialToken_SignsUpAndMintsASession(t *testing.T) {
	f := newTokenFixture(t)

	rr := f.signIn(t, models.AuthProviderApple, jwt.MapClaims{
		"sub": "sub-new", "email": "newcomer@example.com",
		"email_verified": true, "name": "New Comer",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeSocialToken(t, rr)
	if out.Outcome != socialOutcomeSignedUp {
		t.Fatalf("outcome: got %q want %q", out.Outcome, socialOutcomeSignedUp)
	}
	if f.userDB.inserts != 1 {
		t.Fatalf("expected one account to be created, got %d", f.userDB.inserts)
	}
	if out.Token == "" || out.UserID == "" {
		t.Fatalf("a new account must still arrive signed in: %+v", out)
	}
	if len(f.minted) != 1 || f.minted[0] != out.UserID {
		t.Errorf("the session was minted for %v, not for the new account %q", f.minted, out.UserID)
	}
	// The provider sent a name, so there is nothing to ask the player for.
	if out.NeedsProfile {
		t.Error("needsProfile should be false when the provider sent a name")
	}
}

func TestSocialToken_SignUpWithoutANameAsksForAProfile(t *testing.T) {
	f := newTokenFixture(t)

	// Apple sends the name only on the very first authorization, and nothing
	// guarantees we were the ones who got it.
	rr := f.signIn(t, models.AuthProviderApple, jwt.MapClaims{
		"sub": "sub-nameless", "email": "quiet@example.com", "email_verified": true,
	})
	out := decodeSocialToken(t, rr)
	if out.Outcome != socialOutcomeSignedUp || !out.NeedsProfile {
		t.Fatalf("a nameless sign-up must ask for a profile: %+v", out)
	}
	if out.Token == "" {
		t.Error("it must still be signed in")
	}
}

func TestSocialToken_LinkRequiredCarriesNoSession(t *testing.T) {
	f := newTokenFixture(t)
	f.passwordUser("owner@example.com")

	rr := f.signIn(t, models.AuthProviderGoogle, jwt.MapClaims{
		"sub": "sub-unlinked", "email": "owner@example.com", "email_verified": true,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeSocialToken(t, rr)
	if out.Outcome != socialOutcomeLinkRequired {
		t.Fatalf("outcome: got %q want %q", out.Outcome, socialOutcomeLinkRequired)
	}
	// The whole point of this outcome: the caller has proved a Google account,
	// not the LPC account registered to the same address.
	if out.Token != "" {
		t.Error("link_required must not hand out a session")
	}
	if out.UserID != "" {
		t.Error("link_required must not name the account")
	}
	if out.MaskedEmail == "" || strings.Contains(out.MaskedEmail, "owner@example.com") {
		t.Errorf("the address must be masked, got %q", out.MaskedEmail)
	}
	if len(f.minted) != 0 {
		t.Errorf("no session should have been minted, got %v", f.minted)
	}
	if f.userDB.inserts != 0 {
		t.Error("link_required must not create a second account")
	}
}

func TestSocialToken_RefusesASuspendedAccount(t *testing.T) {
	f := newTokenFixture(t)
	user := f.passwordUser("suspended@example.com")
	until := primitive.NewDateTimeFromTime(time.Now().Add(72 * time.Hour))
	user.Details.Suspension = &models.Suspension{Until: &until}
	f.userDB.add(user)
	f.linkIdentity(models.AuthProviderGoogle, "sub-suspended", user.ID)

	rr := f.signIn(t, models.AuthProviderGoogle, jwt.MapClaims{"sub": "sub-suspended"})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	// The player is told when they get back in. A suspended player who is not
	// told concludes they have been hacked.
	if !strings.Contains(rr.Body.String(), "suspended") {
		t.Errorf("the refusal should say it is a suspension: %s", rr.Body.String())
	}
	if len(f.minted) != 0 {
		t.Error("a suspended account must not get a session")
	}
}

func TestSocialToken_RefusesADeactivatedAccount(t *testing.T) {
	f := newTokenFixture(t)
	user := f.passwordUser("gone@example.com")
	user.Details.IsDeactivated = true
	f.userDB.add(user)
	f.linkIdentity(models.AuthProviderApple, "sub-deactivated", user.ID)

	rr := f.signIn(t, models.AuthProviderApple, jwt.MapClaims{"sub": "sub-deactivated"})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "deactivated") {
		t.Errorf("the refusal should say it is deactivated: %s", rr.Body.String())
	}
	if len(f.minted) != 0 {
		t.Error("a deactivated account must not get a session")
	}
}

func TestSocialToken_RefusesATokenMintedForAnotherApplication(t *testing.T) {
	f := newTokenFixture(t)
	user := f.passwordUser("dev@example.com")
	f.linkIdentity(models.AuthProviderGoogle, "sub-known", user.ID)

	// Correctly signed by the provider, for somebody else's client id. This is
	// the check that stops another app's token signing in here, and it is the
	// one a native client is most likely to trip, because a native token's
	// audience is whichever of our client ids the sign-in went through.
	body := fmt.Sprintf(`{"provider":%q,"idToken":%q,"nonce":%q}`,
		models.AuthProviderGoogle,
		f.token(t, jwt.MapClaims{"sub": "sub-known", "aud": "someone-elses-client-id"}),
		testNonce)
	rr := f.post(t, f.api.SocialTokenHandler, "/auth/social/token", body, "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(f.minted) != 0 {
		t.Error("a token for another application must not get a session")
	}
}

func TestSocialToken_RefusesAMissingNonce(t *testing.T) {
	f := newTokenFixture(t)
	user := f.passwordUser("dev@example.com")
	f.linkIdentity(models.AuthProviderGoogle, "sub-known", user.ID)

	// A token with no nonce of ours did not come from a sign-in we started,
	// whoever minted it.
	body := fmt.Sprintf(`{"provider":%q,"idToken":%q,"nonce":""}`,
		models.AuthProviderGoogle, f.token(t, jwt.MapClaims{"sub": "sub-known"}))
	rr := f.post(t, f.api.SocialTokenHandler, "/auth/social/token", body, "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(f.minted) != 0 {
		t.Error("a sign-in with no nonce must not get a session")
	}
}

func TestSocialToken_RefusesANonceFromAnotherSignIn(t *testing.T) {
	f := newTokenFixture(t)
	user := f.passwordUser("dev@example.com")
	f.linkIdentity(models.AuthProviderGoogle, "sub-known", user.ID)

	body := fmt.Sprintf(`{"provider":%q,"idToken":%q,"nonce":%q}`,
		models.AuthProviderGoogle,
		f.token(t, jwt.MapClaims{"sub": "sub-known", "nonce": "a-different-sign-ins-nonce"}),
		testNonce)
	rr := f.post(t, f.api.SocialTokenHandler, "/auth/social/token", body, "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSocialToken_SaysTryAgainWhenTheSessionCannotBeStored(t *testing.T) {
	f := newTokenFixture(t)
	user := f.passwordUser("dev@example.com")
	f.linkIdentity(models.AuthProviderGoogle, "sub-known", user.ID)
	f.mintFail = fmt.Errorf("the token store is unreachable")

	rr := f.signIn(t, models.AuthProviderGoogle, jwt.MapClaims{"sub": "sub-known"})
	// 503 and not 401: the sign-in was real and the account is fine. A 401
	// would make the app throw away a saved login that is not at fault.
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("a retryable refusal should say when to retry")
	}
	if strings.Contains(rr.Body.String(), "unreachable") {
		t.Error("the internal reason must not reach the caller")
	}
}

func TestSocialToken_RefusesAnUnsupportedProvider(t *testing.T) {
	f := newTokenFixture(t)

	body := fmt.Sprintf(`{"provider":"facebook","idToken":%q,"nonce":%q}`,
		f.token(t, jwt.MapClaims{"sub": "sub-x"}), testNonce)
	rr := f.post(t, f.api.SocialTokenHandler, "/auth/social/token", body, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ---- the gate is shared with the website's own route ----

func TestSocialVerify_RefusesASuspendedAccount(t *testing.T) {
	f := newSocialFixture(t)
	user := f.passwordUser("suspended@example.com")
	until := primitive.NewDateTimeFromTime(time.Now().Add(72 * time.Hour))
	user.Details.Suspension = &models.Suspension{Until: &until}
	f.userDB.add(user)
	f.authDB.rows = append(f.authDB.rows, models.AuthIdentity{
		ID: primitive.NewObjectID(), Provider: models.AuthProviderGoogle,
		Subject: "sub-suspended", UserID: user.ID,
	})

	// Before the account gate was shared, a suspended player the password form
	// turned away could still sign in with Google on the website.
	rr := f.post(t, f.api.SocialVerifyHandler, "/auth/social/verify",
		f.verifyBody(t, models.AuthProviderGoogle, jwt.MapClaims{"sub": "sub-suspended"}), "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "suspended") {
		t.Errorf("the refusal should say it is a suspension: %s", rr.Body.String())
	}
}

func TestSocialVerify_RefusesADeactivatedAccount(t *testing.T) {
	f := newSocialFixture(t)
	user := f.passwordUser("gone@example.com")
	user.Details.IsDeactivated = true
	f.userDB.add(user)
	f.authDB.rows = append(f.authDB.rows, models.AuthIdentity{
		ID: primitive.NewObjectID(), Provider: models.AuthProviderApple,
		Subject: "sub-deactivated", UserID: user.ID,
	})

	rr := f.post(t, f.api.SocialVerifyHandler, "/auth/social/verify",
		f.verifyBody(t, models.AuthProviderApple, jwt.MapClaims{"sub": "sub-deactivated"}), "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSocialToken_SaysNothingAboutWhichCheckFailed(t *testing.T) {
	f := newTokenFixture(t)

	// A forged token gets one generic refusal. Naming the failing check — a
	// bad signature, a wrong audience, a stale nonce — tells whoever sent it
	// which part to fix next.
	body := fmt.Sprintf(`{"provider":%q,"idToken":"not-even-a-jwt","nonce":%q}`,
		models.AuthProviderGoogle, testNonce)
	rr := f.post(t, f.api.SocialTokenHandler, "/auth/social/token", body, "")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	for _, leak := range []string{"malformed", "segments", "signature", "audience", "nonce", "issuer"} {
		if strings.Contains(strings.ToLower(rr.Body.String()), leak) {
			t.Errorf("the refusal names the failing check (%q): %s", leak, rr.Body.String())
		}
	}
}
