package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// Social sign-in for the mobile app.
//
// The website mints its own session from /auth/social/verify's answer, which is
// why that route returns an account id and no token. The app has no session of
// its own to mint: it authenticates with a bearer token from /auth/token, and
// that route wants a password over Basic auth. An account created by Google or
// Apple has no password — that is the point of it — so the app needs a route
// that ends in the same bearer token the password login produces.
//
// POST /api/v1/auth/social/token is that route. It answers with the same three
// outcomes as verify, decided by the same code, and adds a token to the two
// that are a sign-in. The third, link_required, deliberately carries no token:
// the caller has proved they own a Google or Apple account, not that they own
// the LPC account registered to the same address, and the link flow is what
// closes that gap.

// socialTokenResponse is verify's answer plus a session.
//
// `_id` rather than `userId`, and `token` beside it, because that is the shape
// /auth/token returns and the app stores the result of either through one path.
type socialTokenResponse struct {
	Outcome      string `json:"outcome"`
	Provider     string `json:"provider"`
	Token        string `json:"token,omitempty"`
	UserID       string `json:"_id,omitempty"`
	Email        string `json:"email,omitempty"`
	MaskedEmail  string `json:"maskedEmail,omitempty"`
	DisplayName  string `json:"displayName,omitempty"`
	NeedsProfile bool   `json:"needsProfile,omitempty"`
	Message      string `json:"message,omitempty"`
}

// socialResolution is what a verified provider identity turned out to mean.
type socialResolution struct {
	Outcome string
	// User is the account being signed in or just created. Nil for
	// link_required, where no account has been earned yet.
	User *models.User
	// Owner is the account that already holds the provider's verified address.
	// Set only for link_required, and never handed to the caller as an id.
	Owner *models.User
	// DisplayName is the provider's name claim, which Apple sends only on the
	// very first authorization.
	DisplayName string
}

// resolveIdentity decides what a verified identity means for our accounts.
//
// Shared by the website's verify route and the app's token route so the two
// surfaces cannot drift on the question that matters most here — which LPC
// account, if any, a provider subject is. On failure it returns the status and
// the player-facing reason for the caller to render.
func (s SocialAuth) resolveIdentity(ctx context.Context, identity *verifiedIdentity) (*socialResolution, int, error) {
	// Already linked: this is a sign-in.
	existing, err := s.findIdentity(ctx, identity.Provider, identity.Subject)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("could not complete that sign-in: %w", err)
	}
	if existing != nil {
		user, err := s.loadUser(ctx, existing.UserID)
		if err != nil {
			// The identity points at an account that is gone. Refusing is the
			// only safe answer; silently creating a new one would detach the
			// person from whatever is left of their old account.
			return nil, http.StatusNotFound, fmt.Errorf("that account is no longer available")
		}
		// Every way into an account refuses the same accounts: unclaimed,
		// deactivated, or serving a suspension. Before this was shared, a
		// suspended player the password form turned away could still sign in
		// with Google.
		if status, err := api.AccountSignInError(user, time.Now()); err != nil {
			return nil, status, err
		}
		s.touchIdentity(ctx, existing, identity)
		return &socialResolution{Outcome: socialOutcomeSignedIn, User: user}, 0, nil
	}

	// Not linked yet. Does the verified address belong to an account already?
	//
	// A relay address never does. Apple mints it per application, so it cannot
	// be the address another account was registered with, and treating a match
	// as meaningful would be matching on a value only we ever see.
	if identity.Email != "" && !identity.PrivateRelay {
		owner, err := s.findUserByEmail(ctx, identity.Email)
		if err != nil {
			return nil, http.StatusInternalServerError, fmt.Errorf("could not complete that sign-in: %w", err)
		}
		if owner != nil {
			return &socialResolution{Outcome: socialOutcomeLinkRequired, Owner: owner}, 0, nil
		}
	}

	// Nothing matches: a new account, owned by this provider subject.
	user, err := s.createAccount(ctx, identity)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("could not create your account: %w", err)
	}
	return &socialResolution{
		Outcome:     socialOutcomeSignedUp,
		User:        user,
		DisplayName: identity.Name,
	}, 0, nil
}

// SocialTokenHandler turns a provider ID token into a bearer session.
//
// POST /api/v1/auth/social/token
func (s SocialAuth) SocialTokenHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req socialVerifyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, socialMaxBodyBytes)).Decode(&req); err != nil {
		config.InfoStatus("we could not read that sign-in", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	identity, ok := s.verifyOrRefuse(ctx, w, req.Provider, req.IDToken, req.Nonce)
	if !ok {
		return
	}

	resolved, status, err := s.resolveIdentity(ctx, identity)
	if err != nil {
		writeSocialRefusal(w, status, err)
		return
	}

	if resolved.Outcome == socialOutcomeLinkRequired {
		// No token, and no account id either: neither has been earned yet.
		_ = json.NewEncoder(w).Encode(socialTokenResponse{
			Outcome:     socialOutcomeLinkRequired,
			Provider:    identity.Provider,
			MaskedEmail: models.MaskEmail(resolved.Owner.Details.Email),
			Message: fmt.Sprintf("You already have an LPC account for that address. Sign in, or we can email %s a code to connect your %s account.",
				models.MaskEmail(resolved.Owner.Details.Email), models.AuthProviderLabel(identity.Provider)),
		})
		return
	}

	token, err := s.issueToken(r, resolved.User.Details.Email, resolved.User.ID)
	if err != nil {
		// The account is fine and the sign-in was real; only the session could
		// not be stored. A 503 is what the app retries — a 401 would make it
		// throw away a saved login that is not at fault.
		zap.S().Errorw("social sign-in: could not issue a token",
			"provider", identity.Provider, "userID", resolved.User.ID, "error", err)
		w.Header().Set("Retry-After", "5")
		// The error is logged above and deliberately not passed on:
		// config.InfoStatus copies a non-database error into the response, and
		// why our token store is unhappy is not the caller's business.
		config.InfoStatus("sign-in is temporarily unavailable, please try again in a moment",
			http.StatusServiceUnavailable, w, nil)
		return
	}

	_ = json.NewEncoder(w).Encode(socialTokenResponse{
		Outcome:      resolved.Outcome,
		Provider:     identity.Provider,
		Token:        token,
		UserID:       resolved.User.ID,
		Email:        resolved.User.Details.Email,
		DisplayName:  resolved.DisplayName,
		NeedsProfile: resolved.Outcome == socialOutcomeSignedUp && resolved.DisplayName == "",
	})
}

// writeSocialRefusal renders a resolution failure.
//
// A 5xx is logged at error level and says nothing specific; everything else is
// an expected refusal the player is told about, so it goes out at info level
// with its own wording. config.ErrorStatus attaches a multi-KB stacktrace in
// production, which is for genuine faults only.
func writeSocialRefusal(w http.ResponseWriter, status int, err error) {
	if status >= http.StatusInternalServerError {
		config.ErrorStatus("could not complete that sign-in", status, w, err)
		return
	}
	config.InfoStatus(err.Error(), status, w, err)
}
