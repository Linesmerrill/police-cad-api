package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	templates "github.com/linesmerrill/police-cad-api/templates/html"
)

// Sign in with Google and Apple.
//
// A developer account is an ordinary LPC account with developer mode switched
// on, so this is the account's sign-in, not a second one beside it. Email and
// password keep working; this adds two more ways in and a settings surface for
// managing which ways a given account has.
//
// Where the work happens, and why here. The token verification lives in the API
// rather than in the website because it is the part that must be right, and one
// implementation that mobile can also call later beats two that drift. Minting
// the session stays in the website, which is where sessions already live: this
// surface answers "which LPC account has this person proved they are", and the
// caller turns that into a login.
//
// These routes are reachable only through the website's own server-side proxy,
// which attaches the gateway secret. The browser never calls them, and the
// actor on the linking routes comes from the website's session rather than from
// anything the browser sends.

// SocialAuth serves the social sign-in surface.
type SocialAuth struct {
	UDB        databases.UserDatabase
	AuthDB     databases.AuthIdentityDatabase
	PVDB       databases.PendingVerificationDatabase
	IdentityDB databases.PlayerIdentityDatabase
	ALDB       databases.AuditLogDatabase

	Verifier *oidcVerifier

	// IssueToken mints the app's bearer session. A field so a test can assert
	// what the handler does with a token without standing up the token store;
	// nil means api.IssueBearerToken, which is what production uses.
	IssueToken func(r *http.Request, email, userID string) (string, error)
}

// issueToken mints a bearer session for an account.
func (s SocialAuth) issueToken(r *http.Request, email, userID string) (string, error) {
	if s.IssueToken != nil {
		return s.IssueToken(r, email, userID)
	}
	return api.IssueBearerToken(r, email, userID)
}

// NewSocialAuth builds the handler with the default verifier.
func NewSocialAuth(udb databases.UserDatabase, adb databases.AuthIdentityDatabase,
	pvdb databases.PendingVerificationDatabase, idb databases.PlayerIdentityDatabase,
	aldb databases.AuditLogDatabase) SocialAuth {
	return SocialAuth{
		UDB:        udb,
		AuthDB:     adb,
		PVDB:       pvdb,
		IdentityDB: idb,
		ALDB:       aldb,
		Verifier:   newOIDCVerifier(),
	}
}

// PurposeSocialLink is the pending-verification purpose for proving you own the
// address a provider reported, when you are not already signed in.
//
// Deliberately its own purpose rather than reusing the signup one, so a code
// issued for one thing cannot be redeemed for another.
const PurposeSocialLink = "social_link"

// Outcomes the verify endpoint can report.
const (
	socialOutcomeSignedIn     = "signed_in"
	socialOutcomeSignedUp     = "signed_up"
	socialOutcomeLinkRequired = "link_required"
)

// ---- request and response shapes ----
//
// Every field is a string or a bool. Nothing is decoded into interface{},
// bson.M or a map, so a JSON object where a string belongs fails to decode and
// the handler answers 400 before any validation runs.

type socialVerifyRequest struct {
	Provider string `json:"provider"`
	IDToken  string `json:"idToken"`
	Nonce    string `json:"nonce"`
}

type socialLinkRequest struct {
	Provider string `json:"provider"`
	IDToken  string `json:"idToken"`
	Nonce    string `json:"nonce"`
	UserID   string `json:"userId"`
}

type socialConfirmRequest struct {
	Provider string `json:"provider"`
	IDToken  string `json:"idToken"`
	Nonce    string `json:"nonce"`
	Code     string `json:"code"`
}

type socialVerifyResponse struct {
	Outcome      string `json:"outcome"`
	Provider     string `json:"provider"`
	UserID       string `json:"userId,omitempty"`
	MaskedEmail  string `json:"maskedEmail,omitempty"`
	DisplayName  string `json:"displayName,omitempty"`
	Message      string `json:"message,omitempty"`
	NeedsProfile bool   `json:"needsProfile,omitempty"`
}

type socialMethod struct {
	Kind      string `json:"kind"`
	Provider  string `json:"provider,omitempty"`
	Label     string `json:"label"`
	Email     string `json:"email,omitempty"`
	Removable bool   `json:"removable"`
}

type socialMethodsResponse struct {
	Methods   []socialMethod `json:"methods"`
	Google    bool           `json:"googleAvailable"`
	Apple     bool           `json:"appleAvailable"`
	CanUnlink bool           `json:"canUnlink"`
}

// ---- verify: sign in, or sign up, or ask to link ----

// SocialVerifyHandler turns a provider ID token into an LPC account.
//
// POST /api/v1/auth/social/verify
//
// Three outcomes, and the difference between them is the whole design:
//
//   - the subject is already attached to an account, so this is a sign-in;
//   - the subject is new and the provider's verified address matches an
//     existing account, so this is a linking request that needs proof and is
//     never completed here;
//   - the subject is new and nothing matches, so this is a sign-up.
//
// The matching step uses the address only to *recognise* a possible owner. The
// thing we store and sign in by is always the subject.
func (s SocialAuth) SocialVerifyHandler(w http.ResponseWriter, r *http.Request) {
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

	// What this identity means for our accounts — a sign-in, a sign-up, or a
	// link that has to be proved — is decided in one place and shared with the
	// app's /auth/social/token (social_auth_session.go).
	resolved, status, err := s.resolveIdentity(ctx, identity)
	if err != nil {
		writeSocialRefusal(w, status, err)
		return
	}

	switch resolved.Outcome {
	case socialOutcomeLinkRequired:
		// Proof required. The account id is deliberately absent: the caller
		// has not yet shown they own this account, and handing it over here
		// would be telling them something they have not earned.
		_ = json.NewEncoder(w).Encode(socialVerifyResponse{
			Outcome:     socialOutcomeLinkRequired,
			Provider:    identity.Provider,
			MaskedEmail: models.MaskEmail(resolved.Owner.Details.Email),
			Message: fmt.Sprintf("You already have an LPC account for that address. Sign in, or we can email %s a code to connect your %s account.",
				models.MaskEmail(resolved.Owner.Details.Email), models.AuthProviderLabel(identity.Provider)),
		})
	case socialOutcomeSignedUp:
		_ = json.NewEncoder(w).Encode(socialVerifyResponse{
			Outcome:     socialOutcomeSignedUp,
			Provider:    identity.Provider,
			UserID:      resolved.User.ID,
			DisplayName: resolved.DisplayName,
			// Apple sends a name only on the very first authorization, and
			// nothing guarantees we were the ones who got it. The caller uses
			// this to decide whether to ask for a username rather than to
			// invent one.
			NeedsProfile: resolved.DisplayName == "",
		})
	default:
		_ = json.NewEncoder(w).Encode(socialVerifyResponse{
			Outcome:  socialOutcomeSignedIn,
			Provider: identity.Provider,
			UserID:   resolved.User.ID,
		})
	}
}

// socialMaxBodyBytes bounds a request body. An ID token is the only large field
// and the verifier caps that separately; this stops an unbounded read.
const socialMaxBodyBytes = 32 * 1024

// ---- linking ----

// SocialLinkHandler attaches a provider to the account already signed in.
//
// POST /api/v1/auth/social/link
//
// This is the "already signed in" proof from the linking rule: the website took
// the user id from its own session, so the person is demonstrably in the
// account they are adding a provider to. No email round trip is needed.
func (s SocialAuth) SocialLinkHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req socialLinkRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, socialMaxBodyBytes)).Decode(&req); err != nil {
		config.InfoStatus("we could not read that request", http.StatusBadRequest, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	if strings.TrimSpace(req.UserID) != "" {
		actorID = req.UserID
	}
	userID, _, err := devSafeObjectID(actorID)
	if err != nil {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	user, err := s.loadUser(ctx, userID)
	if err != nil {
		config.InfoStatus("sign in and try again", http.StatusUnauthorized, w, err)
		return
	}
	if !user.CanAuthenticate() {
		config.InfoStatus("that account cannot be signed into", http.StatusForbidden, w,
			fmt.Errorf("account not claimed"))
		return
	}

	identity, ok := s.verifyOrRefuse(ctx, w, req.Provider, req.IDToken, req.Nonce)
	if !ok {
		return
	}

	s.attachAndRespond(ctx, w, userID, identity, "linked")
}

// SocialLinkRequestCodeHandler emails a code to the address a provider vouched
// for, so somebody who is not signed in can still prove they own the account.
//
// POST /api/v1/auth/social/link/request-code
//
// The code goes to the address from the *token*, never to an address the caller
// typed. That is what makes it proof: we send it to an inbox the provider says
// belongs to this subject, and only someone reading that inbox can finish.
func (s SocialAuth) SocialLinkRequestCodeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req socialVerifyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, socialMaxBodyBytes)).Decode(&req); err != nil {
		config.InfoStatus("we could not read that request", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	identity, ok := s.verifyOrRefuse(ctx, w, req.Provider, req.IDToken, req.Nonce)
	if !ok {
		return
	}
	if identity.Email == "" || identity.PrivateRelay {
		config.InfoStatus("sign in with your password to connect that account", http.StatusBadRequest, w,
			fmt.Errorf("no usable verified email"))
		return
	}

	owner, err := s.findUserByEmail(ctx, identity.Email)
	if err != nil {
		config.ErrorStatus("could not send that code", http.StatusInternalServerError, w, err)
		return
	}
	if owner == nil {
		// Nothing to link. Answering the same way either way would be tidier,
		// but the caller has already proved this address at the provider, so
		// there is nothing here they could learn that they do not know.
		config.InfoStatus("there is no LPC account for that address", http.StatusNotFound, w,
			fmt.Errorf("no account for verified email"))
		return
	}

	_, ownerOID, err := devSafeObjectID(owner.ID)
	if err != nil {
		config.ErrorStatus("could not send that code", http.StatusInternalServerError, w, err)
		return
	}
	if err := checkSensitiveCodeRateLimit(ctx, s.PVDB, ownerOID, PurposeSocialLink); err != nil {
		config.InfoStatus("you have asked for too many codes, try again later", http.StatusTooManyRequests, w, err)
		return
	}
	code, err := generateNumericCode()
	if err != nil {
		config.ErrorStatus("could not send that code", http.StatusInternalServerError, w, err)
		return
	}
	if err := upsertSensitiveCode(ctx, s.PVDB, ownerOID, PurposeSocialLink, owner.Details.Email, identity.Email, code); err != nil {
		config.ErrorStatus("could not send that code", http.StatusInternalServerError, w, err)
		return
	}
	label := models.AuthProviderLabel(identity.Provider)
	sendSensitiveEmailAsync(sensitiveEmail{
		To:      identity.Email,
		Subject: "Lines Police CAD: Connect Your " + label + " Account",
		PlainText: "Your code to connect " + label + " to your Lines Police CAD account is: " + code +
			". This code expires in 15 minutes. If you did not request this, ignore this email.",
		HTML: templates.RenderCode(code),
	})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"sent":        true,
		"maskedEmail": models.MaskEmail(identity.Email),
		"message":     "We emailed you a code. Enter it to finish connecting your account.",
	})
}

// SocialLinkConfirmHandler redeems the emailed code and links the provider.
//
// POST /api/v1/auth/social/link/confirm
//
// The token is verified again rather than carried over from the request that
// sent the code. A code on its own proves the inbox; the token proves the
// provider subject; linking needs both, and re-verifying is cheaper than
// holding verified state between two requests.
func (s SocialAuth) SocialLinkConfirmHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req socialConfirmRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, socialMaxBodyBytes)).Decode(&req); err != nil {
		config.InfoStatus("we could not read that request", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	identity, ok := s.verifyOrRefuse(ctx, w, req.Provider, req.IDToken, req.Nonce)
	if !ok {
		return
	}
	if identity.Email == "" || identity.PrivateRelay {
		config.InfoStatus("sign in with your password to connect that account", http.StatusBadRequest, w,
			fmt.Errorf("no usable verified email"))
		return
	}

	owner, err := s.findUserByEmail(ctx, identity.Email)
	if err != nil || owner == nil {
		config.InfoStatus("that code is not valid", http.StatusBadRequest, w, fmt.Errorf("no account for verified email"))
		return
	}

	_, ownerOID, err := devSafeObjectID(owner.ID)
	if err != nil {
		config.InfoStatus("that code is not valid", http.StatusBadRequest, w, err)
		return
	}
	pending, err := s.PVDB.FindOne(ctx, bson.M{
		"userID":  devEq(ownerOID),
		"purpose": devEq(PurposeSocialLink),
	})
	if err != nil || pending == nil {
		config.InfoStatus("that code is not valid", http.StatusBadRequest, w, fmt.Errorf("no pending code"))
		return
	}
	if expires := timeFromBSON(pending.ExpiresAt); !expires.IsZero() && time.Now().After(expires) {
		_ = s.PVDB.DeleteOne(ctx, bson.M{"_id": devEq(pending.ID)})
		config.InfoStatus("that code has expired, ask for a new one", http.StatusBadRequest, w,
			fmt.Errorf("code expired"))
		return
	}
	// Attempt ceiling, so the six digits cannot simply be enumerated.
	if pending.Attempts >= sensitiveMaxRetries {
		_ = s.PVDB.DeleteOne(ctx, bson.M{"_id": devEq(pending.ID)})
		config.InfoStatus("too many attempts, ask for a new code", http.StatusBadRequest, w,
			fmt.Errorf("too many attempts"))
		return
	}
	if !codesEqualConstantTime(pending.Code, strings.TrimSpace(req.Code)) {
		_ = s.PVDB.UpdateOne(ctx, bson.M{"_id": devEq(pending.ID)}, bson.M{"$inc": bson.M{"attempts": 1}})
		config.InfoStatus("that code is not valid", http.StatusBadRequest, w, fmt.Errorf("code mismatch"))
		return
	}

	// Single use. The code is cleared before the link so a replay cannot ride
	// in behind a slow write.
	if err := s.PVDB.DeleteOne(ctx, bson.M{"_id": devEq(pending.ID)}); err != nil {
		zap.S().Warnw("social link: could not clear the used code", "error", err)
	}

	s.attachAndRespond(ctx, w, owner.ID, identity, "linked_by_email_code")
}

// SocialUnlinkHandler removes a provider from an account.
//
// DELETE /api/v1/auth/social/link/{provider}
//
// Refused when it would leave no way back in. That is the whole of the guard:
// an account with a password keeps one after unlinking everything, and an
// account that only ever signed in with Google must keep Google until it has
// something else.
func (s SocialAuth) SocialUnlinkHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	provider, err := safeAuthProvider(mux.Vars(r)["provider"])
	if err != nil {
		config.InfoStatus("that is not a provider we support", http.StatusBadRequest, w, err)
		return
	}
	userID, _, err := devSafeObjectID(resolveActorFromRequest(r))
	if err != nil {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	user, err := s.loadUser(ctx, userID)
	if err != nil {
		config.InfoStatus("sign in and try again", http.StatusUnauthorized, w, err)
		return
	}

	methods, err := s.signInMethods(ctx, user)
	if err != nil {
		config.ErrorStatus("could not check your sign-in methods", http.StatusInternalServerError, w, err)
		return
	}
	held := false
	for _, m := range methods {
		if m.Kind == "provider" && m.Provider == provider {
			held = true
		}
	}
	if !held {
		config.InfoStatus(fmt.Sprintf("your account is not connected to %s", models.AuthProviderLabel(provider)),
			http.StatusNotFound, w, fmt.Errorf("provider not linked"))
		return
	}
	if len(methods) <= 1 {
		config.InfoStatus(fmt.Sprintf("%s is the only way into your account. Set a password first, then you can disconnect it.",
			models.AuthProviderLabel(provider)), http.StatusConflict, w, fmt.Errorf("last sign-in method"))
		return
	}

	if err := s.AuthDB.DeleteOne(ctx, bson.M{
		"provider": devEq(provider),
		"userId":   devEq(userID),
	}); err != nil {
		config.ErrorStatus("could not disconnect that account", http.StatusInternalServerError, w, err)
		return
	}

	logAudit(s.ALDB, primitive.NilObjectID, "social_signin_unlinked", "account",
		userID, user.Details.Username, userID, user.Details.Username,
		map[string]interface{}{"provider": provider})

	remaining, err := s.signInMethods(ctx, user)
	if err != nil {
		remaining = nil
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"unlinked": true,
		"provider": provider,
		"methods":  remaining,
		"message":  fmt.Sprintf("%s is no longer connected to your account.", models.AuthProviderLabel(provider)),
	})
}

// SocialMethodsHandler lists how an account can be signed into.
//
// GET /api/v1/auth/social/methods
//
// Drives the settings UI: which providers are connected, which are available to
// connect, and whether anything may be removed at all.
func (s SocialAuth) SocialMethodsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	userID, _, err := devSafeObjectID(resolveActorFromRequest(r))
	if err != nil {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	user, err := s.loadUser(ctx, userID)
	if err != nil {
		config.InfoStatus("sign in and try again", http.StatusUnauthorized, w, err)
		return
	}
	methods, err := s.signInMethods(ctx, user)
	if err != nil {
		config.ErrorStatus("could not read your sign-in methods", http.StatusInternalServerError, w, err)
		return
	}
	google, apple := s.verifier().SocialSignInConfigured()
	_ = json.NewEncoder(w).Encode(socialMethodsResponse{
		Methods:   methods,
		Google:    google,
		Apple:     apple,
		CanUnlink: len(methods) > 1,
	})
}

// ---- shared helpers ----

func (s SocialAuth) verifier() *oidcVerifier {
	if s.Verifier != nil {
		return s.Verifier
	}
	return newOIDCVerifier()
}

// verifyOrRefuse validates the token and writes one generic refusal on failure.
//
// Every verification failure answers the same way. Which check failed is useful
// to us in the logs and useful to nobody else, so the response does not say.
func (s SocialAuth) verifyOrRefuse(ctx context.Context, w http.ResponseWriter, provider, token, nonce string) (*verifiedIdentity, bool) {
	providerName, err := safeAuthProvider(provider)
	if err != nil {
		config.InfoStatus("that is not a provider we support", http.StatusBadRequest, w, err)
		return nil, false
	}
	checkedNonce, err := safeAuthNonce(nonce)
	if err != nil {
		config.InfoStatus("that sign-in could not be verified", http.StatusUnauthorized, w, err)
		return nil, false
	}
	identity, err := s.verifier().Verify(ctx, providerName, token, checkedNonce)
	if err != nil {
		zap.S().Infow("social sign-in refused", "provider", providerName, "reason", err)
		config.InfoStatus("that sign-in could not be verified", http.StatusUnauthorized, w, err)
		return nil, false
	}
	return identity, true
}

func (s SocialAuth) findIdentity(ctx context.Context, provider, subject string) (*models.AuthIdentity, error) {
	identity, err := s.AuthDB.FindOne(ctx, bson.M{
		"provider": devEq(provider),
		"subject":  devEq(subject),
	})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return identity, nil
}

func (s SocialAuth) loadUser(ctx context.Context, userID string) (*models.User, error) {
	_, oid, err := devSafeObjectID(userID)
	if err != nil {
		return nil, err
	}
	user := &models.User{}
	if err := s.UDB.FindOne(ctx, bson.M{"_id": devEq(oid)}).Decode(user); err != nil {
		return nil, err
	}
	return user, nil
}

// findUserByEmail looks an account up by address, ignoring case.
//
// Addresses are stored as the user typed them and there is no unique index, so
// an exact match would miss an account registered with different capitalisation
// — and missing it is the bad outcome here, because it is what would create a
// duplicate account instead of asking to link. The term is escaped and anchored
// before it becomes a pattern.
func (s SocialAuth) findUserByEmail(ctx context.Context, email string) (*models.User, error) {
	checked, err := safeAuthEmail(email)
	if err != nil {
		return nil, nil
	}
	user := &models.User{}
	err = s.UDB.FindOne(ctx, bson.M{
		"user.email": bson.M{"$regex": "^" + regexEscapeDev(checked) + "$", "$options": "i"},
	}).Decode(user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return user, nil
}

// createAccount makes a new LPC account owned by a provider subject.
//
// No password is set, which is the point: the account's way in is the provider.
// Claimed is left unset, which reads as claimed — this is a real person's own
// account from the moment it exists, unlike the shells a mod provisions, and it
// can therefore act as the claimant in the in-game claim flow.
func (s SocialAuth) createAccount(ctx context.Context, identity *verifiedIdentity) (*models.User, error) {
	now := time.Now()
	verified := true
	details := models.UserDetails{
		Username:    socialUsername(identity),
		Email:       identity.Email,
		Password:    "",
		Communities: []models.UserCommunity{},
		CreatedAt:   now,
	}
	// Only claim the address as verified when the provider vouched for it. A
	// relay address is still deliverable and still theirs, so it counts.
	if identity.Email != "" {
		details.EmailVerified = &verified
	}

	res, err := s.UDB.InsertOne(ctx, details)
	if err != nil {
		return nil, err
	}
	userOID, ok := res.Decode().(primitive.ObjectID)
	if !ok {
		return nil, fmt.Errorf("unexpected inserted id type %T", res.Decode())
	}

	if err := s.insertIdentity(ctx, userOID.Hex(), identity); err != nil {
		// The unique index decided that another request got here first with the
		// same subject. Defer to the winner rather than keeping two accounts
		// for one person.
		if mongo.IsDuplicateKeyError(err) {
			winner, findErr := s.findIdentity(ctx, identity.Provider, identity.Subject)
			if findErr == nil && winner != nil {
				zap.S().Infow("social sign-up lost a race, using the winner",
					"provider", identity.Provider, "orphanUserId", userOID.Hex())
				return s.loadUser(ctx, winner.UserID)
			}
		}
		return nil, err
	}

	user := &models.User{ID: userOID.Hex(), Details: details}
	logAudit(s.ALDB, primitive.NilObjectID, "social_signin_signed_up", "account",
		userOID.Hex(), details.Username, userOID.Hex(), details.Username,
		map[string]interface{}{"provider": identity.Provider})
	return user, nil
}

func (s SocialAuth) insertIdentity(ctx context.Context, userID string, identity *verifiedIdentity) error {
	now := primitive.NewDateTimeFromTime(time.Now())
	_, err := s.AuthDB.InsertOne(ctx, models.AuthIdentity{
		ID:            primitive.NewObjectID(),
		Provider:      identity.Provider,
		Subject:       identity.Subject,
		UserID:        userID,
		Email:         identity.Email,
		EmailVerified: identity.EmailVerified,
		PrivateRelay:  identity.PrivateRelay,
		DisplayName:   identity.Name,
		CreatedAt:     now,
		UpdatedAt:     now,
		LastSignInAt:  &now,
	})
	return err
}

// attachAndRespond links a verified subject to an account and answers.
//
// Shared by both linking paths, because once ownership is proved — by session
// or by emailed code — what happens next is identical.
func (s SocialAuth) attachAndRespond(ctx context.Context, w http.ResponseWriter,
	userID string, identity *verifiedIdentity, auditAction string) {

	existing, err := s.findIdentity(ctx, identity.Provider, identity.Subject)
	if err != nil {
		config.ErrorStatus("could not connect that account", http.StatusInternalServerError, w, err)
		return
	}
	if existing != nil {
		if existing.UserID == userID {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"linked":   true,
				"provider": identity.Provider,
				"userId":   userID,
				"message":  fmt.Sprintf("%s is already connected to this account.", models.AuthProviderLabel(identity.Provider)),
			})
			return
		}
		// One provider account, one LPC account. Moving it would silently take
		// a sign-in method away from whoever holds it now.
		config.InfoStatus(fmt.Sprintf("that %s account is already connected to a different LPC account",
			models.AuthProviderLabel(identity.Provider)), http.StatusConflict, w,
			fmt.Errorf("subject already linked"))
		return
	}

	if err := s.insertIdentity(ctx, userID, identity); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			config.InfoStatus(fmt.Sprintf("that %s account is already connected to a different LPC account",
				models.AuthProviderLabel(identity.Provider)), http.StatusConflict, w, err)
			return
		}
		config.ErrorStatus("could not connect that account", http.StatusInternalServerError, w, err)
		return
	}

	logAudit(s.ALDB, primitive.NilObjectID, "social_signin_"+auditAction, "account",
		userID, "", userID, "",
		map[string]interface{}{"provider": identity.Provider})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"linked":   true,
		"provider": identity.Provider,
		"userId":   userID,
		"message":  fmt.Sprintf("%s is now connected to your account.", models.AuthProviderLabel(identity.Provider)),
	})
}

// touchIdentity records a sign-in and refreshes what the provider told us.
//
// Fields are only ever filled in, never blanked. Apple stops sending the name
// after the first authorization, so writing whatever arrived would erase a name
// we already had on the second sign-in.
func (s SocialAuth) touchIdentity(ctx context.Context, stored *models.AuthIdentity, fresh *verifiedIdentity) {
	now := primitive.NewDateTimeFromTime(time.Now())
	set := bson.M{"lastSignInAt": now, "updatedAt": now}
	if fresh.Email != "" && fresh.Email != stored.Email {
		set["email"] = fresh.Email
		set["emailVerified"] = fresh.EmailVerified
		set["privateRelay"] = fresh.PrivateRelay
	}
	if fresh.Name != "" && stored.DisplayName == "" {
		set["displayName"] = fresh.Name
	}
	if _, err := s.AuthDB.UpdateOne(ctx, bson.M{
		"provider": devEq(stored.Provider),
		"subject":  devEq(stored.Subject),
	}, bson.M{"$set": set}); err != nil {
		// A sign-in that worked should not fail because a timestamp did not
		// land.
		zap.S().Warnw("social sign-in: could not record the sign-in", "error", err)
	}
}

// signInMethods lists every way into an account.
//
// The password counts as one, and each linked provider counts as one. This is
// the list the last-method guard measures and the settings screen renders, and
// having one function produce both means the screen cannot disagree with the
// rule that governs it.
func (s SocialAuth) signInMethods(ctx context.Context, user *models.User) ([]socialMethod, error) {
	var methods []socialMethod
	if strings.TrimSpace(user.Details.Password) != "" {
		methods = append(methods, socialMethod{
			Kind: "password", Label: "Email and password", Email: user.Details.Email,
		})
	}

	cursor, err := s.AuthDB.Find(ctx, bson.M{"userId": devEq(user.ID)})
	if err != nil {
		return nil, err
	}
	var linked []models.AuthIdentity
	if cursor != nil {
		if err := cursor.All(ctx, &linked); err != nil {
			return nil, err
		}
	}
	for _, identity := range linked {
		if !models.IsValidAuthProvider(identity.Provider) {
			continue
		}
		email := identity.Email
		if identity.PrivateRelay {
			// Showing a relay address as the person's own address is wrong and
			// confusing; it is ours, not theirs.
			email = ""
		}
		methods = append(methods, socialMethod{
			Kind:     "provider",
			Provider: identity.Provider,
			Label:    models.AuthProviderLabel(identity.Provider),
			Email:    email,
		})
	}

	// Removable is computed last, because it depends on the total.
	for i := range methods {
		methods[i].Removable = len(methods) > 1 && methods[i].Kind == "provider"
	}
	return methods, nil
}

// socialUsername picks a starting username for a new account.
//
// Deliberately not derived from the email local part: an Apple relay address
// has a meaningless one, and a real address would put part of somebody's email
// on a public profile. The caller is told when there was no name to use, and
// asks.
func socialUsername(identity *verifiedIdentity) string {
	if name := strings.TrimSpace(identity.Name); name != "" {
		return name
	}
	suffix := identity.Subject
	if len(suffix) > 6 {
		suffix = suffix[len(suffix)-6:]
	}
	return "player" + suffix
}
