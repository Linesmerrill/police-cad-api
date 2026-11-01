package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/models"
)

// Player provisioning: how a mod turns a game identifier into an LPC account.
//
// The whole design rests on one rule, and it is worth stating before any of the
// code: we never accept a raw LPC user id from a key holder as proof of
// anything, and we never accept an email from one either.
//
// A mod knows a player by their FiveM license or their Discord ID. It sends
// that, we map it, and the account we hand back cannot be logged into until the
// player themselves proves they own it. An email the mod supplies is an email
// the mod controls, and taking one would hand the developer a way into a
// player's account — so a provisioned account has no email at all. That is not
// a gap to close later; it is the mechanism.

// claimCodeTTL is how long a code shown in-game stays good.
//
// Short, because the code is displayed on a screen other players may be able
// to see or stream. Long enough that a player can alt-tab, find the website and
// type it without rushing.
const claimCodeTTL = 30 * time.Minute

// maxProvisionedUsernameLen keeps a mod-supplied display name sane.
const maxProvisionedUsernameLen = 32

type devUpsertPlayerRequest struct {
	Provider   string `json:"provider"`
	Identifier string `json:"identifier"`
	// Username is a display name only. It is never used to find or match an
	// account, so a mod sending someone else's name cannot reach their data.
	Username string `json:"username"`
	// Synthetic declares this identity as a simulated character rather than a
	// person. It is recorded on the identity row, and every record the
	// character owns inherits it, so an integration populating a city marks
	// each NPC once here and nothing it writes later shows up in a player's
	// name database.
	//
	// It is a claim the integration makes, not something we infer: the same
	// key provisions accounts for real players arriving from the game, so "a
	// key created it" says nothing about whether there is a person behind it.
	//
	// Claiming overrides it. If a person ever signs in and takes the account
	// over, they are a player from then on whatever the row was created as.
	Synthetic bool `json:"synthetic"`
}

type devPlayerResponse struct {
	PlayerRef  string `json:"playerRef"`
	PlayerID   string `json:"playerId"`
	Provider   string `json:"provider"`
	Identifier string `json:"identifier"`
	Username   string `json:"username"`
	Claimed    bool   `json:"claimed"`
	Created    bool   `json:"created"`
	Synthetic  bool   `json:"synthetic,omitempty"`
}

// DevUpsertPlayerHandler maps a game identifier to an LPC account, creating one
// if this is the first time we have seen the player.
//
// POST /api/v1/dev/players — players:provision
//
// Idempotent, and that is enforced by a unique index rather than by a check.
// Two mods starting at the same instant with the same player both try to
// insert; one wins, the other gets a duplicate-key error and re-reads the
// winner's row. Both end up with the same account, which is what a mod author
// expects and what a check-then-insert could not promise.
func (d DeveloperAPI) DevUpsertPlayerHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}

	var req devUpsertPlayerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}

	provider, err := devSafeProvider(req.Provider)
	if err != nil {
		devError(w, http.StatusBadRequest, "invalid_provider", err.Error(), nil)
		return
	}
	identifier, err := devSafeIdentifier(provider, req.Identifier)
	if err != nil {
		devError(w, http.StatusBadRequest, "invalid_identifier", err.Error(), nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	player, created, err := d.resolveOrCreatePlayer(ctx, dc, provider, identifier, req.Username, req.Synthetic)
	if err != nil {
		if err == errProvisionCapReached {
			d.RecordOutcome(r, http.StatusTooManyRequests)
			devError(w, http.StatusTooManyRequests, "provision_cap_reached",
				fmt.Sprintf("This key may create %d player accounts a day. It resets at midnight UTC.",
					dc.Limits.AccountsPerKeyDay),
				map[string]interface{}{"limit": dc.Limits.AccountsPerKeyDay, "scope": "accounts_per_day"})
			return
		}
		zap.S().Errorw("developer api: player upsert failed", "error", err)
		devError(w, http.StatusInternalServerError, "server_error", "Could not create that player.", nil)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	d.RecordOutcome(r, status)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"player": player})
}

// errProvisionCapReached means the key has created as many accounts today as
// its plan allows.
var errProvisionCapReached = fmt.Errorf("provisioning cap reached")

// resolveOrCreatePlayer is the idempotent core of player provisioning.
//
// Returns the player and whether this call created the account.
func (d DeveloperAPI) resolveOrCreatePlayer(ctx context.Context, dc *DevAPIContext, provider, identifier, username string, synthetic bool) (*devPlayerResponse, bool, error) {
	// Already known: hand back the same account, every time.
	if existing, err := d.IdentityDB.FindOne(ctx, bson.M{"provider": devEq(provider), "identifier": devEq(identifier)}); err == nil && existing != nil {
		return d.playerResponse(ctx, *existing, false), false, nil
	}

	// Creating an account is the expensive, hard-to-undo operation, so the cap
	// is checked here rather than in the middleware: a mod looking up players
	// it already knows should never be refused for it.
	if dc.Usage.AccountsCreated >= dc.Limits.AccountsPerKeyDay {
		return nil, false, errProvisionCapReached
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	claimedFalse := false

	// The shape of an unclaimed account, and every field here is deliberate.
	//
	// No email and no password hash: both login paths look a user up by email
	// and then bcrypt, so an account with neither cannot be signed into by any
	// route we have. Claimed is explicitly false so the auth path can refuse it
	// outright even if some future code path sets an email.
	details := models.UserDetails{
		Username:                 sanitizeProvisionedUsername(username, provider, identifier),
		Email:                    "",
		Password:                 "",
		DeveloperProvisioned:     true,
		DeveloperSynthetic:       synthetic,
		Claimed:                  &claimedFalse,
		ProvisionedByKeyID:       dc.Key.ID.Hex(),
		ProvisionedByDeveloperID: dc.Key.DeveloperID,
		ProvisionedAt:            &now,
		GameIdentities: []models.GameIdentity{{
			Provider:   provider,
			Identifier: identifier,
			AddedAt:    now,
		}},
		Communities: []models.UserCommunity{},
		CreatedAt:   time.Now(),
	}

	// The account goes in first, because Mongo assigns its _id and the identity
	// row has to carry it.
	//
	// That ordering decides the failure mode, so it is worth being explicit: if
	// this process dies before the identity row lands, what is left is an
	// unclaimed account with no identity and no records. Nothing can log into
	// it, nothing points at it, and the unclaimed-account cleanup removes it.
	// The reverse ordering is not available to us, and would strand an identity
	// pointing at an account that does not exist — which a mod would keep
	// resolving to forever.
	res, err := d.UDB.InsertOne(ctx, details)
	if err != nil {
		return nil, false, err
	}
	userOID, ok := res.Decode().(primitive.ObjectID)
	if !ok {
		return nil, false, fmt.Errorf("unexpected inserted id type %T", res.Decode())
	}

	identity := models.PlayerIdentity{
		ID:                   primitive.NewObjectID(),
		Provider:             provider,
		Identifier:           identifier,
		UserID:               userOID.Hex(),
		CreatedByKeyID:       dc.Key.ID.Hex(),
		CreatedByDeveloperID: dc.Key.DeveloperID,
		Synthetic:            synthetic,
		Claimed:              false,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if _, err := d.IdentityDB.InsertOne(ctx, identity); err != nil {
		// A duplicate key here is the race, not a failure: another request
		// created this player between our lookup and our insert. The unique
		// index on (provider, identifier) is what decides it, and we defer to
		// the winner so both callers end up with the same account.
		//
		// Our own just-created account is left behind rather than deleted. It
		// is unclaimed and record-less, so it is harmless and the cleanup
		// collects it; deleting it here would mean a delete racing whatever the
		// winner is already doing.
		if mongo.IsDuplicateKeyError(err) {
			zap.S().Infow("developer api: lost a player-upsert race, using the winner",
				"provider", provider, "orphanUserId", userOID.Hex())
			winner, findErr := d.IdentityDB.FindOne(ctx, bson.M{"provider": devEq(provider), "identifier": devEq(identifier)})
			if findErr != nil || winner == nil {
				return nil, false, findErr
			}
			return d.playerResponse(ctx, *winner, false), false, nil
		}
		return nil, false, err
	}

	d.countProvisioned(ctx, dc.Key, bson.M{"accountsCreated": 1})

	return &devPlayerResponse{
		PlayerRef:  provider + ":" + identifier,
		PlayerID:   userOID.Hex(),
		Provider:   provider,
		Identifier: identifier,
		Username:   details.Username,
		Claimed:    false,
		Created:    true,
		Synthetic:  synthetic,
	}, true, nil
}

// playerResponse fills a response from an identity row, reading the account for
// its current username and claim state.
func (d DeveloperAPI) playerResponse(ctx context.Context, identity models.PlayerIdentity, created bool) *devPlayerResponse {
	out := &devPlayerResponse{
		PlayerRef:  identity.Provider + ":" + identity.Identifier,
		PlayerID:   identity.UserID,
		Provider:   identity.Provider,
		Identifier: identity.Identifier,
		Claimed:    identity.Claimed,
		Created:    created,
		Synthetic:  identity.Synthetic,
	}
	var user models.User
	if err := d.UDB.FindOne(ctx, bson.M{"_id": devEq(objectIDOrZero(identity.UserID))}).Decode(&user); err == nil {
		out.Username = user.Details.Username
		out.Claimed = user.IsClaimed()
	}
	// A claimed account is a person, whatever the row was created as, so the
	// flag is dropped rather than reported against someone who signed in.
	if out.Claimed {
		out.Synthetic = false
	}
	return out
}

// sanitizeProvisionedUsername builds a display name for a provisioned account.
//
// A mod's display name is accepted but trimmed and capped; when it sends
// nothing we fall back to something recognisable rather than leaving the
// account nameless in a roster.
func sanitizeProvisionedUsername(username, provider, identifier string) string {
	name := strings.TrimSpace(username)
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	if len(name) > maxProvisionedUsernameLen {
		name = strings.TrimSpace(name[:maxProvisionedUsernameLen])
	}
	if name != "" {
		return name
	}
	suffix := identifier
	if len(suffix) > 6 {
		suffix = suffix[len(suffix)-6:]
	}
	return "player-" + suffix
}

// countProvisioned records a provisioning action against the key's daily
// counters.
func (d DeveloperAPI) countProvisioned(ctx context.Context, key models.DeveloperAPIKey, inc bson.M) {
	now := time.Now().UTC()
	set := bson.M{
		"keyId":       key.ID.Hex(),
		"developerId": key.DeveloperID,
		"day":         now.Format("2006-01-02"),
		"expiresAt":   primitive.NewDateTimeFromTime(now.Add(400 * 24 * time.Hour)),
	}
	if _, err := d.UsageDB.FindOneAndIncrement(ctx, models.UsageDocID(key.ID.Hex(), now), inc, set); err != nil {
		zap.S().Warnw("developer api: could not record provisioning", "keyId", key.ID.Hex(), "error", err)
	}
}

// DevGetPlayerHandler looks a player up by game identifier.
//
// GET /api/v1/dev/players/{provider}/{identifier} — players:provision
//
// Only players this developer created are visible. Without that restriction a
// key could ask whether an arbitrary Discord ID has an LPC account, which is
// nobody's business but the account holder's.
func (d DeveloperAPI) DevGetPlayerHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}

	provider, err := devSafeProvider(mux.Vars(r)["provider"])
	if err != nil {
		devError(w, http.StatusBadRequest, "invalid_provider", err.Error(), nil)
		return
	}
	identifier, err := devSafeIdentifier(provider, mux.Vars(r)["identifier"])
	if err != nil {
		devError(w, http.StatusBadRequest, "invalid_identifier", err.Error(), nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	identity, err := d.IdentityDB.FindOne(ctx, bson.M{
		"provider":             devEq(provider),
		"identifier":           devEq(identifier),
		"createdByDeveloperId": devEq(dc.Key.DeveloperID),
	})
	if err != nil || identity == nil {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "player_not_found",
			"No player of yours has that identifier. Upsert them first.", nil)
		return
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"player": d.playerResponse(ctx, *identity, false)})
}

// DevIssueClaimCodeHandler mints a one-time code the mod shows in-game so the
// player can take ownership of their account.
//
// POST /api/v1/dev/players/{provider}/{identifier}/claim-code — players:provision
//
// Only for accounts this developer provisioned, and only while they are
// unclaimed. Once a player owns their account, a developer has no business
// minting anything that could be redeemed against it.
func (d DeveloperAPI) DevIssueClaimCodeHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}

	provider, err := devSafeProvider(mux.Vars(r)["provider"])
	if err != nil {
		devError(w, http.StatusBadRequest, "invalid_provider", err.Error(), nil)
		return
	}
	identifier, err := devSafeIdentifier(provider, mux.Vars(r)["identifier"])
	if err != nil {
		devError(w, http.StatusBadRequest, "invalid_identifier", err.Error(), nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	identity, err := d.IdentityDB.FindOne(ctx, bson.M{
		"provider":             devEq(provider),
		"identifier":           devEq(identifier),
		"createdByDeveloperId": devEq(dc.Key.DeveloperID),
	})
	if err != nil || identity == nil {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "player_not_found",
			"No player of yours has that identifier. Upsert them first.", nil)
		return
	}

	var user models.User
	if err := d.UDB.FindOne(ctx, bson.M{"_id": devEq(objectIDOrZero(identity.UserID))}).Decode(&user); err != nil {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "player_not_found", "That player's account is missing.", nil)
		return
	}
	if user.IsClaimed() {
		d.RecordOutcome(r, http.StatusConflict)
		devError(w, http.StatusConflict, "already_claimed",
			"That player already owns their LPC account. They can sign in normally.", nil)
		return
	}

	code, hash, err := generateClaimCode()
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not create a claim code.", nil)
		return
	}

	now := time.Now()
	// Outstanding codes for this player are dropped first, so the code the mod
	// is about to put on screen is the only one that works. Otherwise a code
	// from an earlier session, possibly still visible in someone's stream VOD,
	// would stay live.
	if _, err := d.ClaimCodeDB.DeleteMany(ctx, bson.M{"userId": devEq(identity.UserID), "usedAt": nil}); err != nil {
		zap.S().Warnw("developer api: could not clear old claim codes", "error", err)
	}

	record := models.DeveloperClaimCode{
		ID:          primitive.NewObjectID(),
		CodeHash:    hash,
		UserID:      identity.UserID,
		KeyID:       dc.Key.ID.Hex(),
		DeveloperID: dc.Key.DeveloperID,
		Provider:    provider,
		Identifier:  identifier,
		ExpiresAt:   primitive.NewDateTimeFromTime(now.Add(claimCodeTTL)),
		CreatedAt:   primitive.NewDateTimeFromTime(now),
	}
	if _, err := d.ClaimCodeDB.InsertOne(ctx, record); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not create a claim code.", nil)
		return
	}

	d.RecordOutcome(r, http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		// The only time the code exists outside the caller. Only its hash is
		// stored, so it cannot be looked up or re-sent later.
		"code":      code,
		"expiresAt": record.ExpiresAt.Time().UTC().Format(time.RFC3339),
		"url":       claimURL(),
		"instructions": "Show this code to the player. They open the URL, sign in or sign up, " +
			"and enter the code to take ownership of their characters.",
	})
}

// claimURL is where a player goes to redeem a code.
func claimURL() string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_WEB_BASE_URL")), "/")
	if base == "" {
		base = "https://www.linespolice-cad.com"
	}
	return base + "/developer/claim"
}
