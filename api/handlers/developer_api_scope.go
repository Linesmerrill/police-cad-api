package handlers

import (
	"context"
	"net/http"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/models"
)

// Authorization for the developer surface: what a key is allowed to do, and
// which communities it is allowed to do it in.
//
// These are two separate questions and they are checked separately, because
// they fail for different reasons and a developer needs to be told which.
// Holding calls:write does not get you into a community nobody granted you, and
// being granted a community does not let you write calls you have no scope for.

// RequireScope wraps a developer route with the scope it needs.
//
// Declared per route at registration (api.go) rather than derived from the
// path, so the scope a route needs is visible in the routing table. A route
// nobody gave a scope to does not quietly default to permitted; it will not
// compile without one.
func (d DeveloperAPI) RequireScope(scope string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dc, ok := DevContextFrom(r.Context())
		if !ok {
			// Only reachable if a route were registered outside the
			// authenticating middleware. Refuse rather than assume.
			devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
			return
		}
		if !dc.Key.HasScope(scope) {
			d.RecordOutcome(r, http.StatusForbidden)
			devError(w, http.StatusForbidden, "missing_scope",
				"This API key does not have the scope needed for that.",
				map[string]interface{}{"requiredScope": scope, "keyScopes": dc.Key.Scopes})
			return
		}
		next(w, r)
	})
}

// CommunityContext is a community a request has been proved to be allowed into.
type CommunityContext struct {
	Community models.Community
	Grant     models.DeveloperCommunityGrant
}

// ResolveCommunity reads the community from the request path and proves the key
// is allowed to act in it.
//
// Every developer handler that touches a community calls this. It is not done
// in middleware because the handler needs the loaded community anyway, and a
// second read would be wasted — but that means it is on each handler to call
// it, so the rule is: a handler that takes {communityId} starts with this line.
//
// The grant is re-read from the database every time. That is what makes a
// community owner's revocation take effect immediately: there is no cached
// decision to outlive it.
//
// On failure it has already written the response and recorded the outcome; the
// handler just returns.
func (d DeveloperAPI) ResolveCommunity(w http.ResponseWriter, r *http.Request) (*CommunityContext, bool) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return nil, false
	}

	// The canonical hex from the parsed id, not the caller's string. Checking
	// the input and then using the original would leave the raw value in the
	// query; using what the parse produced cannot.
	communityID, oid, err := devSafeObjectID(mux.Vars(r)["communityId"])
	if err != nil {
		devError(w, http.StatusBadRequest, "invalid_community", "That community id is not valid.", nil)
		return nil, false
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	grant, err := d.GrantDB.FindOne(ctx, bson.M{
		"keyId":       devEq(dc.Key.ID.Hex()),
		"communityId": devEq(communityID),
		"status":      devEq(models.DeveloperKeyActive),
	})
	if err != nil || grant == nil {
		// One answer for "no such community" and "not granted to you", so a key
		// cannot be used to discover which community ids exist.
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "community_not_granted",
			"This API key is not approved for that community.",
			map[string]interface{}{"communityId": communityID})
		return nil, false
	}

	community, err := d.CDB.FindOneIncludingPending(ctx, bson.M{"_id": devEq(oid)})
	if err != nil || community == nil {
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "community_not_granted",
			"This API key is not approved for that community.",
			map[string]interface{}{"communityId": communityID})
		return nil, false
	}

	// A community the owner has deleted is in a 30-day pending window. It still
	// exists, and staff can still review it, but it is going away and a mod
	// must not keep writing into it — those writes would be lost, and worse,
	// they would look to a player like their records were saved.
	if community.Details.PendingDeletionAt != nil {
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "community_pending_deletion",
			"That community has been deleted by its owner and is read-only.",
			map[string]interface{}{"communityId": communityID})
		return nil, false
	}

	return &CommunityContext{Community: *community, Grant: *grant}, true
}

// ActiveGrantCommunityIDs lists the communities a key may currently act in.
func (d DeveloperAPI) ActiveGrantCommunityIDs(ctx context.Context, keyID string) ([]string, error) {
	cur, err := d.GrantDB.Find(ctx, bson.M{
		"keyId":  devEq(keyID),
		"status": devEq(models.DeveloperKeyActive),
	})
	if err != nil {
		return nil, err
	}
	var grants []models.DeveloperCommunityGrant
	if err := cur.All(ctx, &grants); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(grants))
	for _, g := range grants {
		out = append(out, g.CommunityID)
	}
	return out, nil
}
