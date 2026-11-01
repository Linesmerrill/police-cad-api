package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Management endpoints for the Developer API: what the website calls when a
// developer turns on their account, makes a key, or looks at their usage, and
// what a community owner calls to see and control the keys on their community.
//
// These live on the ordinary authenticated surface, not on /api/v1/dev. A
// developer key cannot reach them, which is the point: a key can never mint
// another key, widen its own scopes, or approve itself into a community.

// developerTermsVersion is the Developer Terms a developer accepted. Bumping it
// is how we would require re-acceptance after a material change.
const developerTermsVersion = "2026-10-31"

// maxDeveloperKeyNameLen keeps a key's label to something a dashboard can show.
const maxDeveloperKeyNameLen = 60

// devActorID is the LPC user making a management request.
//
// Same resolution as the rest of the API, which means it trusts ?userId= when
// no bearer actor is present. That is a known, systemic weakness
// (see memory: the website has no API token), and the reason it is acceptable
// here is that these routes are only reachable through the website's own
// server-side proxy, which sends the gateway secret and takes the user id from
// the session rather than from the browser. The browser never calls these
// directly.
func devActorID(r *http.Request) string {
	return resolveActorFromRequest(r)
}

// ---- developer account ----

type developerAccountResponse struct {
	Enabled  bool                     `json:"enabled"`
	Account  *models.DeveloperAccount `json:"account,omitempty"`
	Limits   models.DeveloperLimits   `json:"limits"`
	Scopes   []models.DeveloperScope  `json:"availableScopes"`
	KeyCount int                      `json:"keyCount"`
	// UpgradeAvailable is false until the Stripe product exists. The website
	// hides the upgrade path rather than offering a button that cannot work.
	UpgradeAvailable bool   `json:"upgradeAvailable"`
	TermsVersion     string `json:"termsVersion"`
}

// GetDeveloperAccountHandler returns the signed-in user's developer account, or
// that they do not have one yet.
func (d DeveloperAPI) GetDeveloperAccountHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	resp := developerAccountResponse{
		Limits:           models.LimitsForPlan(models.DeveloperPlanFree),
		Scopes:           models.DeveloperScopes(),
		UpgradeAvailable: developerPaidTierAvailable(),
		TermsVersion:     developerTermsVersion,
	}

	account, err := d.AccountDB.FindOne(ctx, bson.M{"userId": devEq(actorID)})
	if err != nil || account == nil {
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	count, _ := d.KeyDB.CountDocuments(ctx, bson.M{
		"developerId": account.ID.Hex(),
		"status":      bson.M{"$ne": models.DeveloperKeyRevoked},
	})

	resp.Enabled = true
	resp.Account = account
	resp.Limits = models.LimitsForPlan(account.EffectivePlan())
	resp.KeyCount = int(count)
	_ = json.NewEncoder(w).Encode(resp)
}

// developerPaidTierAvailable reports whether there is a Stripe price to send
// someone to. Until Merrill creates the product, every developer is on Free and
// the upgrade path stays hidden.
func developerPaidTierAvailable() bool {
	return strings.TrimSpace(os.Getenv("STRIPE_DEV_API_PRO_MONTHLY_PRICE_ID")) != ""
}

type enableDeveloperAccountRequest struct {
	AppName     string `json:"appName"`
	AppURL      string `json:"appUrl"`
	AcceptTerms bool   `json:"acceptTerms"`
}

// EnableDeveloperAccountHandler turns on a developer account for the signed-in
// user.
//
// Accepting the Developer Terms is required and recorded with its version. A
// developer can create accounts for players who have never seen our Terms, so
// the person taking that power on needs to have agreed to the rules about it.
func (d DeveloperAPI) EnableDeveloperAccountHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	var req enableDeveloperAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if !req.AcceptTerms {
		config.InfoStatus("developer terms must be accepted", http.StatusBadRequest, w,
			fmt.Errorf("acceptTerms was not set"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	if existing, err := d.AccountDB.FindOne(ctx, bson.M{"userId": devEq(actorID)}); err == nil && existing != nil {
		// Already a developer. Re-enabling is how someone comes back after
		// disabling, so this reactivates rather than refusing.
		now := primitive.NewDateTimeFromTime(time.Now())
		set := bson.M{
			"status":          models.DeveloperAccountActive,
			"termsAcceptedAt": now,
			"termsVersion":    developerTermsVersion,
			"updatedAt":       now,
		}
		// A staff suspension is not something a developer can lift by clicking
		// enable again.
		if existing.Status == models.DeveloperAccountSuspended {
			config.InfoStatus("developer account suspended", http.StatusForbidden, w,
				fmt.Errorf("account is suspended"))
			return
		}
		if req.AppName != "" {
			set["appName"] = strings.TrimSpace(req.AppName)
		}
		if req.AppURL != "" {
			set["appUrl"] = strings.TrimSpace(req.AppURL)
		}
		if _, err := d.AccountDB.UpdateOne(ctx, bson.M{"_id": existing.ID}, bson.M{"$set": set}); err != nil {
			config.ErrorStatus("failed to update developer account", http.StatusInternalServerError, w, err)
			return
		}
		updated, _ := d.AccountDB.FindOne(ctx, bson.M{"_id": existing.ID})
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"account": updated})
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	account := models.DeveloperAccount{
		ID:              primitive.NewObjectID(),
		UserID:          actorID,
		Plan:            models.DeveloperPlanFree,
		PlanActive:      false,
		AppName:         strings.TrimSpace(req.AppName),
		AppURL:          strings.TrimSpace(req.AppURL),
		TermsAcceptedAt: &now,
		TermsVersion:    developerTermsVersion,
		Status:          models.DeveloperAccountActive,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if _, err := d.AccountDB.InsertOne(ctx, account); err != nil {
		config.ErrorStatus("failed to create developer account", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"account": account})
}

// DisableDeveloperAccountHandler turns a developer account off and revokes every
// key it holds.
//
// Revoking the keys is the point of the button. Leaving a key alive on a
// disabled account would mean a developer who thought they had switched off
// still had working credentials in the wild.
func (d DeveloperAPI) DisableDeveloperAccountHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	account, err := d.AccountDB.FindOne(ctx, bson.M{"userId": devEq(actorID)})
	if err != nil || account == nil {
		config.InfoStatus("no developer account", http.StatusNotFound, w, fmt.Errorf("not found"))
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	revoked := d.revokeAllKeysForDeveloper(ctx, account.ID.Hex(), actorID, "developer account disabled")
	if _, err := d.AccountDB.UpdateOne(ctx, bson.M{"_id": account.ID}, bson.M{"$set": bson.M{
		"status":    models.DeveloperAccountSuspended,
		"updatedAt": now,
	}}); err != nil {
		config.ErrorStatus("failed to disable developer account", http.StatusInternalServerError, w, err)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"disabled":    true,
		"keysRevoked": revoked,
	})
}

// revokeAllKeysForDeveloper revokes every active key a developer holds, and the
// community grants behind them, returning how many keys it stopped.
func (d DeveloperAPI) revokeAllKeysForDeveloper(ctx context.Context, developerID, actorID, reason string) int64 {
	now := primitive.NewDateTimeFromTime(time.Now())
	return d.revokeKeysAndGrants(ctx, bson.M{"developerId": developerID}, developerID, actorID, reason, now)
}

// revokeKeysAndGrants revokes the keys matching a filter and every grant those
// keys hold.
//
// Keys first, then grants. If the process dies between the two, what is left is
// a revoked key holding grants that no longer matter, because the key itself
// stops authenticating. The other order would leave a working key with no
// community access, which looks to a developer like a mysterious outage.
func (d DeveloperAPI) revokeKeysAndGrants(ctx context.Context, keyFilter bson.M, developerID, actorID, reason string, now primitive.DateTime) int64 {
	cur, err := d.KeyDB.Find(ctx, keyFilter)
	if err != nil {
		zap.S().Warnw("developer api: could not list keys to revoke", "error", err)
		return 0
	}
	var keys []models.DeveloperAPIKey
	if err := cur.All(ctx, &keys); err != nil {
		zap.S().Warnw("developer api: could not decode keys to revoke", "error", err)
		return 0
	}

	var revoked int64
	for _, k := range keys {
		if k.Status == models.DeveloperKeyRevoked {
			continue
		}
		if _, err := d.KeyDB.UpdateOne(ctx, bson.M{"_id": k.ID}, bson.M{"$set": bson.M{
			"status":       models.DeveloperKeyRevoked,
			"revokedBy":    actorID,
			"revokedAt":    now,
			"revokeReason": reason,
			"updatedAt":    now,
		}}); err != nil {
			zap.S().Warnw("developer api: could not revoke key", "keyId", k.ID.Hex(), "error", err)
			continue
		}
		revoked++

		if _, err := d.GrantDB.UpdateMany(ctx,
			bson.M{"keyId": k.ID.Hex(), "status": bson.M{"$ne": models.DeveloperKeyRevoked}},
			bson.M{"$set": bson.M{
				"status":       models.DeveloperKeyRevoked,
				"revokedBy":    actorID,
				"revokedAt":    now,
				"revokeReason": reason,
			}}); err != nil {
			zap.S().Warnw("developer api: could not revoke grants", "keyId", k.ID.Hex(), "error", err)
		}
	}
	return revoked
}

// ---- keys ----

type createDeveloperKeyRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	// CommunityID binds the key to a community at creation. Optional: a key
	// that will create its own communities does not need one.
	CommunityID string `json:"communityId"`
}

// CreateDeveloperKeyHandler issues a key and returns the plaintext exactly once.
//
// What makes this safe to hand back in a response body is that it is the only
// time it exists anywhere outside the caller: we store a SHA-256 of it and
// nothing else, so neither we nor a database dump can produce it again.
func (d DeveloperAPI) CreateDeveloperKeyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	var req createDeveloperKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		config.InfoStatus("a key name is required", http.StatusBadRequest, w, fmt.Errorf("empty name"))
		return
	}
	if len(name) > maxDeveloperKeyNameLen {
		name = name[:maxDeveloperKeyNameLen]
	}

	scopes, err := normalizeDeveloperScopes(req.Scopes)
	if err != nil {
		config.InfoStatus(err.Error(), http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	account, err := d.AccountDB.FindOne(ctx, bson.M{"userId": devEq(actorID)})
	if err != nil || account == nil {
		config.InfoStatus("enable a developer account first", http.StatusForbidden, w, fmt.Errorf("no developer account"))
		return
	}
	if account.Status != models.DeveloperAccountActive {
		config.InfoStatus("developer account is not active", http.StatusForbidden, w, fmt.Errorf("inactive developer"))
		return
	}

	limits := models.LimitsForPlan(account.EffectivePlan())
	liveKeys, err := d.KeyDB.CountDocuments(ctx, bson.M{
		"developerId": account.ID.Hex(),
		"status":      bson.M{"$ne": models.DeveloperKeyRevoked},
	})
	if err != nil {
		config.ErrorStatus("failed to count keys", http.StatusInternalServerError, w, err)
		return
	}
	if int(liveKeys) >= limits.KeysPerDeveloper {
		config.InfoStatus(
			fmt.Sprintf("your plan allows %d keys; revoke one or upgrade", limits.KeysPerDeveloper),
			http.StatusConflict, w, fmt.Errorf("key limit reached"))
		return
	}

	// A key that cannot create communities and was not bound to one would be
	// inert, so say so at creation rather than letting the developer find out
	// when every call 403s.
	wantsProvisioning := false
	for _, s := range scopes {
		if s == models.ScopeCommunitiesProvision {
			wantsProvisioning = true
		}
	}
	if req.CommunityID == "" && !wantsProvisioning {
		config.InfoStatus(
			"pick a community for this key, or give it the communities:provision scope",
			http.StatusBadRequest, w, fmt.Errorf("no community and no provisioning scope"))
		return
	}

	// Binding to an existing community needs authority over that community.
	// This is the check that stops a member of someone else's community quietly
	// giving a key write access to it: anyone may *request*, but only an owner,
	// an administrator, or a member with manage-community rights makes it live.
	grantStatus := ""
	boundCommunityID := ""
	var community *models.Community
	if req.CommunityID != "" {
		canonical, oid, idErr := devSafeObjectID(req.CommunityID)
		if idErr != nil {
			config.InfoStatus("invalid community id", http.StatusBadRequest, w, idErr)
			return
		}
		boundCommunityID = canonical
		community, err = d.CDB.FindOne(ctx, bson.M{"_id": devEq(oid)})
		if err != nil || community == nil {
			config.InfoStatus("community not found", http.StatusNotFound, w, fmt.Errorf("no community"))
			return
		}
		if userHasCommunityPermission(community, actorID, "manage community settings") {
			grantStatus = models.DeveloperKeyActive
		} else {
			if !isApprovedMemberOfCommunity(ctx, d.UDB, actorID, boundCommunityID) {
				config.InfoStatus("you are not a member of that community", http.StatusForbidden, w,
					fmt.Errorf("not a member"))
				return
			}
			grantStatus = models.DeveloperKeyPending
		}
	}

	plaintext, hash, displayPrefix, lastFour, err := generateDeveloperKey()
	if err != nil {
		config.ErrorStatus("failed to generate key", http.StatusInternalServerError, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	key := models.DeveloperAPIKey{
		ID:            primitive.NewObjectID(),
		DeveloperID:   account.ID.Hex(),
		UserID:        actorID,
		Name:          name,
		KeyHash:       hash,
		DisplayPrefix: displayPrefix,
		LastFour:      lastFour,
		Scopes:        scopes,
		Status:        models.DeveloperKeyActive,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if _, err := d.KeyDB.InsertOne(ctx, key); err != nil {
		config.ErrorStatus("failed to create key", http.StatusInternalServerError, w, err)
		return
	}

	var grant *models.DeveloperCommunityGrant
	if req.CommunityID != "" {
		g := models.DeveloperCommunityGrant{
			ID:          primitive.NewObjectID(),
			KeyID:       key.ID.Hex(),
			DeveloperID: account.ID.Hex(),
			CommunityID: boundCommunityID,
			Source:      models.DeveloperGrantOwnerApproved,
			Status:      grantStatus,
			RequestedAt: now,
		}
		if grantStatus == models.DeveloperKeyActive {
			g.ApprovedBy = actorID
			g.ApprovedAt = &now
		}
		if _, err := d.GrantDB.InsertOne(ctx, g); err != nil {
			config.ErrorStatus("failed to bind key to community", http.StatusInternalServerError, w, err)
			return
		}
		grant = &g

		logAudit(d.ALDB, objectIDOrZero(boundCommunityID), "developer_key.created", "developer_api",
			actorID, resolveActorName(d.UDB, actorID), key.ID.Hex(), name,
			map[string]interface{}{"scopes": scopes, "grantStatus": grantStatus})
	}

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"key": key,
		// The only time this value is ever returned.
		"plaintextKey": plaintext,
		"warning":      "Copy this key now. It is not stored and cannot be shown again.",
		"grant":        grant,
	})
}

// normalizeDeveloperScopes validates and de-duplicates a requested scope list.
//
// An unknown scope is refused rather than dropped: silently ignoring one would
// hand back a key that does less than the developer asked for, and they would
// find out from a 403 in production.
func normalizeDeveloperScopes(requested []string) ([]string, error) {
	if len(requested) == 0 {
		return nil, fmt.Errorf("pick at least one scope")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(requested))
	for _, s := range requested {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !models.IsValidDeveloperScope(s) {
			return nil, fmt.Errorf("unknown scope: %s", s)
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("pick at least one scope")
	}
	sort.Strings(out)
	return out, nil
}

// isApprovedMemberOfCommunity checks membership the canonical way: the user
// document, not the community's members map.
//
// The members map is only populated once someone sets a ten-code, so it is not
// a membership source — a real member who has never gone on duty is absent
// from it.
func isApprovedMemberOfCommunity(ctx context.Context, udb databases.UserDatabase, userID, communityID string) bool {
	oid := objectIDOrZero(userID)
	if oid.IsZero() {
		return false
	}
	count, err := udb.CountDocuments(ctx, bson.M{
		"_id": oid,
		"user.communities": bson.M{"$elemMatch": bson.M{
			"communityId": communityID,
			"status":      models.CommunityMemberStatusApproved,
		}},
	})
	if err != nil {
		return false
	}
	return count > 0
}

// ListDeveloperKeysHandler returns the signed-in developer's keys with their
// communities.
func (d DeveloperAPI) ListDeveloperKeysHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	account, err := d.AccountDB.FindOne(ctx, bson.M{"userId": devEq(actorID)})
	if err != nil || account == nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"keys": []interface{}{}})
		return
	}

	cur, err := d.KeyDB.Find(ctx, bson.M{"developerId": account.ID.Hex()},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}))
	if err != nil {
		config.ErrorStatus("failed to load keys", http.StatusInternalServerError, w, err)
		return
	}
	var keys []models.DeveloperAPIKey
	if err := cur.All(ctx, &keys); err != nil {
		config.ErrorStatus("failed to decode keys", http.StatusInternalServerError, w, err)
		return
	}

	out := make([]map[string]interface{}, 0, len(keys))
	for _, k := range keys {
		grants := d.grantsForKey(ctx, k.ID.Hex())
		out = append(out, map[string]interface{}{"key": k, "communities": grants})
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"keys":   out,
		"limits": models.LimitsForPlan(account.EffectivePlan()),
	})
}

// grantsForKey loads a key's community grants, newest first, for display.
func (d DeveloperAPI) grantsForKey(ctx context.Context, keyID string) []map[string]interface{} {
	cur, err := d.GrantDB.Find(ctx, bson.M{"keyId": keyID})
	if err != nil {
		return []map[string]interface{}{}
	}
	var grants []models.DeveloperCommunityGrant
	if err := cur.All(ctx, &grants); err != nil {
		return []map[string]interface{}{}
	}
	out := make([]map[string]interface{}, 0, len(grants))
	for _, g := range grants {
		name := ""
		if c, err := d.CDB.FindOneIncludingPending(ctx, bson.M{"_id": objectIDOrZero(g.CommunityID)}); err == nil && c != nil {
			name = c.Details.Name
		}
		out = append(out, map[string]interface{}{"grant": g, "communityName": name})
	}
	return out
}

// RevokeDeveloperKeyHandler revokes one of the signed-in developer's keys.
//
// Revoked, never deleted: the usage history and the audit trail point at the
// key id, and a key that vanished would take the record of what it did with it.
func (d DeveloperAPI) RevokeDeveloperKeyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	keyID, oid, err := devSafeObjectID(mux.Vars(r)["keyId"])
	if err != nil {
		config.InfoStatus("invalid key id", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Scoped by userId as well as _id, so a developer can only revoke their
	// own keys however the id was obtained.
	key, err := d.KeyDB.FindOne(ctx, bson.M{"_id": devEq(oid), "userId": devEq(actorID)})
	if err != nil || key == nil {
		config.InfoStatus("key not found", http.StatusNotFound, w, fmt.Errorf("not found"))
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	d.revokeKeysAndGrants(ctx, bson.M{"_id": devEq(oid)}, key.DeveloperID, actorID, "revoked by developer", now)

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"revoked": true, "keyId": keyID})
}

// RotateDeveloperKeyHandler issues a replacement key with the same scopes and
// communities, and revokes the old one.
//
// Two documents rather than a new secret on the same row, so the usage history
// of the old key stays attached to the old key. The new key records what it
// replaced.
func (d DeveloperAPI) RotateDeveloperKeyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	_, oid, err := devSafeObjectID(mux.Vars(r)["keyId"])
	if err != nil {
		config.InfoStatus("invalid key id", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	old, err := d.KeyDB.FindOne(ctx, bson.M{"_id": devEq(oid), "userId": devEq(actorID)})
	if err != nil || old == nil {
		config.InfoStatus("key not found", http.StatusNotFound, w, fmt.Errorf("not found"))
		return
	}
	if old.Status == models.DeveloperKeyRevoked {
		config.InfoStatus("that key is already revoked", http.StatusConflict, w, fmt.Errorf("revoked"))
		return
	}

	plaintext, hash, displayPrefix, lastFour, err := generateDeveloperKey()
	if err != nil {
		config.ErrorStatus("failed to generate key", http.StatusInternalServerError, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	newKey := models.DeveloperAPIKey{
		ID:               primitive.NewObjectID(),
		DeveloperID:      old.DeveloperID,
		UserID:           old.UserID,
		Name:             old.Name,
		KeyHash:          hash,
		DisplayPrefix:    displayPrefix,
		LastFour:         lastFour,
		Scopes:           old.Scopes,
		Status:           models.DeveloperKeyActive,
		RotatedFromKeyID: old.ID.Hex(),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if _, err := d.KeyDB.InsertOne(ctx, newKey); err != nil {
		config.ErrorStatus("failed to create key", http.StatusInternalServerError, w, err)
		return
	}

	// Carry the communities across before the old key stops working, so a
	// rotation is not an outage for the integration.
	cur, err := d.GrantDB.Find(ctx, bson.M{"keyId": old.ID.Hex(), "status": models.DeveloperKeyActive})
	if err == nil {
		var grants []models.DeveloperCommunityGrant
		if cur.All(ctx, &grants) == nil {
			for _, g := range grants {
				copied := g
				copied.ID = primitive.NewObjectID()
				copied.KeyID = newKey.ID.Hex()
				if _, err := d.GrantDB.InsertOne(ctx, copied); err != nil {
					zap.S().Warnw("developer api: could not copy grant on rotate", "error", err)
				}
			}
		}
	}

	d.revokeKeysAndGrants(ctx, bson.M{"_id": old.ID}, old.DeveloperID, actorID, "rotated", now)

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"key":           newKey,
		"plaintextKey":  plaintext,
		"warning":       "Copy this key now. It is not stored and cannot be shown again.",
		"replacedKeyId": old.ID.Hex(),
	})
}

// GetDeveloperKeyUsageHandler returns a key's daily usage for the dashboard.
func (d DeveloperAPI) GetDeveloperKeyUsageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	keyID, oid, err := devSafeObjectID(mux.Vars(r)["keyId"])
	if err != nil {
		config.InfoStatus("invalid key id", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	key, err := d.KeyDB.FindOne(ctx, bson.M{"_id": devEq(oid), "userId": devEq(actorID)})
	if err != nil || key == nil {
		config.InfoStatus("key not found", http.StatusNotFound, w, fmt.Errorf("not found"))
		return
	}

	// Default to the last 30 days, which is what the dashboard charts.
	days := 30
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	to := time.Now().UTC().Format("2006-01-02")
	// Both bounds are validated to YYYY-MM-DD and the validated value is what
	// goes into the query, so a range bound is always a date-shaped scalar.
	if v := strings.TrimSpace(r.URL.Query().Get("from")); v != "" {
		day, dErr := devSafeDay(v)
		if dErr != nil {
			config.InfoStatus("invalid from date", http.StatusBadRequest, w, dErr)
			return
		}
		from = day
	}
	if v := strings.TrimSpace(r.URL.Query().Get("to")); v != "" {
		day, dErr := devSafeDay(v)
		if dErr != nil {
			config.InfoStatus("invalid to date", http.StatusBadRequest, w, dErr)
			return
		}
		to = day
	}

	cur, err := d.UsageDB.Find(ctx, bson.M{
		"keyId": devEq(keyID),
		"day":   bson.M{"$gte": from, "$lte": to},
	}, options.Find().SetSort(bson.D{{Key: "day", Value: 1}}))
	if err != nil {
		config.ErrorStatus("failed to load usage", http.StatusInternalServerError, w, err)
		return
	}
	var rows []models.DeveloperAPIUsage
	if err := cur.All(ctx, &rows); err != nil {
		config.ErrorStatus("failed to decode usage", http.StatusInternalServerError, w, err)
		return
	}

	account, _ := d.AccountDB.FindOne(ctx, bson.M{"_id": objectIDOrZero(key.DeveloperID)})
	plan := models.DeveloperPlanFree
	if account != nil {
		plan = account.EffectivePlan()
	}
	limits := models.LimitsForPlan(plan)

	today := models.DeveloperAPIUsage{}
	monthRequests := 0
	thisMonth := time.Now().UTC().Format("2006-01")
	todayKey := time.Now().UTC().Format("2006-01-02")
	for _, row := range rows {
		if row.Day == todayKey {
			today = row
		}
		if strings.HasPrefix(row.Day, thisMonth) {
			monthRequests += row.Requests
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"keyId":             keyID,
		"plan":              plan,
		"limits":            limits,
		"days":              rows,
		"requestsToday":     today.Requests,
		"requestsThisMonth": monthRequests,
		"errorsToday": map[string]int{
			"rateLimited":  today.Rejected429,
			"unauthorized": today.Rejected401,
			"forbidden":    today.Rejected403,
		},
		"dailyLimitResetsAt": time.Now().UTC().Add(time.Duration(secondsToNextUTCDay()) * time.Second).Format(time.RFC3339),
	})
}

// ---- the community owner's side ----

// ListCommunityDeveloperKeysHandler returns every key with a grant on a
// community, for its owner and admins.
//
// The community side gets its own view rather than being told to trust the
// developer's list: these are keys with write access to their data, and they
// should not have to ask anyone to find out what they are.
func (d DeveloperAPI) ListCommunityDeveloperKeysHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	communityID, oid, err := devSafeObjectID(mux.Vars(r)["communityId"])
	if err != nil {
		config.InfoStatus("invalid community id", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := d.CDB.FindOne(ctx, bson.M{"_id": devEq(oid)})
	if err != nil || community == nil {
		config.InfoStatus("community not found", http.StatusNotFound, w, fmt.Errorf("not found"))
		return
	}
	if !authorizeCommunityAction(w, r, community, "manage community settings") {
		return
	}

	cur, err := d.GrantDB.Find(ctx, bson.M{"communityId": devEq(communityID)})
	if err != nil {
		config.ErrorStatus("failed to load keys", http.StatusInternalServerError, w, err)
		return
	}
	var grants []models.DeveloperCommunityGrant
	if err := cur.All(ctx, &grants); err != nil {
		config.ErrorStatus("failed to decode keys", http.StatusInternalServerError, w, err)
		return
	}

	out := make([]map[string]interface{}, 0, len(grants))
	for _, g := range grants {
		entry := map[string]interface{}{"grant": g}
		if key, err := d.KeyDB.FindOne(ctx, bson.M{"_id": objectIDOrZero(g.KeyID)}); err == nil && key != nil {
			entry["key"] = key
			entry["developerName"] = resolveActorName(d.UDB, key.UserID)
		}
		out = append(out, entry)
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"keys": out})
}

// ApproveCommunityDeveloperKeyHandler lets an owner or admin approve a key that
// asked for access to their community.
func (d DeveloperAPI) ApproveCommunityDeveloperKeyHandler(w http.ResponseWriter, r *http.Request) {
	d.setCommunityGrantStatus(w, r, models.DeveloperKeyActive)
}

// RevokeCommunityDeveloperKeyHandler lets an owner or admin withdraw a key's
// access to their community.
//
// It revokes the grant, not the key: the developer may hold grants on other
// communities that have nothing to do with this decision.
func (d DeveloperAPI) RevokeCommunityDeveloperKeyHandler(w http.ResponseWriter, r *http.Request) {
	d.setCommunityGrantStatus(w, r, models.DeveloperKeyRevoked)
}

func (d DeveloperAPI) setCommunityGrantStatus(w http.ResponseWriter, r *http.Request, status string) {
	w.Header().Set("Content-Type", "application/json")

	communityID, cOID, err := devSafeObjectID(mux.Vars(r)["communityId"])
	if err != nil {
		config.InfoStatus("invalid community id", http.StatusBadRequest, w, err)
		return
	}
	keyID, _, err := devSafeObjectID(mux.Vars(r)["keyId"])
	if err != nil {
		config.InfoStatus("invalid key id", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := d.CDB.FindOne(ctx, bson.M{"_id": devEq(cOID)})
	if err != nil || community == nil {
		config.InfoStatus("community not found", http.StatusNotFound, w, fmt.Errorf("not found"))
		return
	}
	if !authorizeCommunityAction(w, r, community, "manage community settings") {
		return
	}

	actorID := devActorID(r)
	now := primitive.NewDateTimeFromTime(time.Now())
	set := bson.M{"status": status}
	action := "developer_key.grant_revoked"
	if status == models.DeveloperKeyActive {
		set["approvedBy"] = actorID
		set["approvedAt"] = now
		action = "developer_key.grant_approved"
	} else {
		set["revokedBy"] = actorID
		set["revokedAt"] = now
		set["revokeReason"] = "revoked by community"
	}

	res, err := d.GrantDB.UpdateOne(ctx,
		bson.M{"keyId": devEq(keyID), "communityId": devEq(communityID)},
		bson.M{"$set": set})
	if err != nil {
		config.ErrorStatus("failed to update key access", http.StatusInternalServerError, w, err)
		return
	}
	if res != nil && res.MatchedCount == 0 {
		config.InfoStatus("that key has no access request for this community", http.StatusNotFound, w,
			fmt.Errorf("no grant"))
		return
	}

	logAudit(d.ALDB, cOID, action, "developer_api", actorID, resolveActorName(d.UDB, actorID),
		keyID, "", map[string]interface{}{"status": status})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"keyId": keyID, "status": status})
}
