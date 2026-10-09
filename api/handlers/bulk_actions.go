package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// bulkActionMaxIDs caps how many items one bulk request may act on. It matches
// what an admin can select across a few pages of the management lists and keeps
// a single request comfortably inside the router timeout.
const bulkActionMaxIDs = 100

// bulkItemResult is the outcome for one id in a bulk request.
type bulkItemResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// bulkActionResponse is the body every bulk endpoint returns. A bulk request
// succeeds as a whole (200) even when some items fail; each item says how it went.
type bulkActionResponse struct {
	Results   []bulkItemResult `json:"results"`
	Succeeded int              `json:"succeeded"`
	Failed    int              `json:"failed"`
}

func (b *bulkActionResponse) ok(id string) {
	b.Results = append(b.Results, bulkItemResult{ID: id, OK: true})
	b.Succeeded++
}

func (b *bulkActionResponse) fail(id, reason string) {
	b.Results = append(b.Results, bulkItemResult{ID: id, OK: false, Error: reason})
	b.Failed++
}

// normalizeBulkIDs trims and de-duplicates the requested ids, keeping their
// order. Malformed ids are kept so they can be reported back as failed items.
// It errors when nothing was requested or when more than bulkActionMaxIDs
// distinct ids were.
func normalizeBulkIDs(raw []string) ([]string, error) {
	seen := make(map[string]bool, len(raw))
	ids := make([]string, 0, len(raw))
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, errors.New("select at least one item")
	}
	if len(ids) > bulkActionMaxIDs {
		return nil, fmt.Errorf("you can act on at most %d items at a time", bulkActionMaxIDs)
	}
	return ids, nil
}

// authorizeBulkCommunityAction resolves the caller and checks they may act on
// the community. Bulk actions are destructive, so unlike authorizeCommunityAction
// the query-string userId is believed only alongside the server-to-server secret
// (the same rule as resolveAdjustActor): the mobile app sends a bearer token,
// and the website calls these endpoints from its backend, which holds the secret
// and fills userId in from the logged-in session. A browser cannot forge it.
//
// On failure it writes the response and returns ok=false.
func authorizeBulkCommunityAction(w http.ResponseWriter, r *http.Request, community *models.Community, permissionNames ...string) (string, bool) {
	actorID := resolveAdjustActor(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return "", false
	}
	if !userHasCommunityPermission(community, actorID, permissionNames...) {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, fmt.Errorf("user %s lacks required permission", actorID))
		return "", false
	}
	return actorID, true
}

// loadBulkCommunity parses the communityId path variable and loads the
// community. On failure it writes the response and returns nil.
func loadBulkCommunity(w http.ResponseWriter, r *http.Request, find func(*http.Request, primitive.ObjectID) (*models.Community, error)) (*models.Community, primitive.ObjectID, string) {
	communityID := mux.Vars(r)["communityId"]
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid communityId", http.StatusBadRequest, w, err)
		return nil, cID, communityID
	}
	community, err := find(r, cID)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			config.InfoStatus("community not found", http.StatusNotFound, w, err)
		} else {
			config.ErrorStatus("failed to fetch community", http.StatusInternalServerError, w, err)
		}
		return nil, cID, communityID
	}
	// Hand back the canonical (lowercase) hex, which is how the id is stored on
	// users and civilians.
	return community, cID, cID.Hex()
}

func writeBulkResponse(w http.ResponseWriter, resp *bulkActionResponse) {
	if resp.Results == nil {
		resp.Results = []bulkItemResult{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// BulkRemoveCommunityMembersHandler removes (kicks) several members from a
// community in one request.
//
// POST /api/v1/community/{communityId}/members/bulk-remove
// Body: {"userIds": ["...", ...]}
//
// The caller needs "manage members" (the owner and administrators always pass).
// The owner and the caller themselves are never removed; they come back as
// failed items. Each removal runs the same writes as the single remove-community
// endpoint and is audited as member.kicked.
func (u User) BulkRemoveCommunityMembersHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserIDs []string `json:"userIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	ids, err := normalizeBulkIDs(body.UserIDs)
	if err != nil {
		config.ErrorStatus(err.Error(), http.StatusBadRequest, w, err)
		return
	}

	community, cID, communityID := loadBulkCommunity(w, r, func(r *http.Request, cID primitive.ObjectID) (*models.Community, error) {
		ctx, cancel := api.WithQueryTimeout(r.Context())
		defer cancel()
		return u.CDB.FindOne(ctx, bson.M{"_id": cID})
	})
	if community == nil {
		return
	}

	actorID, ok := authorizeBulkCommunityAction(w, r, community, "manage members")
	if !ok {
		return
	}
	actorName := resolveActorName(u.DB, actorID)

	resp := &bulkActionResponse{}
	for _, id := range ids {
		reason, removedName := u.bulkRemoveOneMember(r, id, actorID, communityID, cID, community)
		if reason != "" {
			resp.fail(id, reason)
			continue
		}
		logAudit(u.ALDB, cID, "member.kicked", "member", actorID, actorName, id, removedName, map[string]interface{}{"bulk": true})
		resp.ok(id)
	}

	writeBulkResponse(w, resp)
}

// bulkRemoveOneMember checks and removes one member. It returns a failure
// reason (empty on success) and the removed user's username for the audit log.
func (u User) bulkRemoveOneMember(r *http.Request, userID, actorID, communityID string, cID primitive.ObjectID, community *models.Community) (string, string) {
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return "invalid user id", ""
	}
	if userID == community.Details.OwnerID {
		return "the community owner can't be removed", ""
	}
	if userID == actorID {
		return "you can't remove yourself", ""
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	var user models.User
	if err := u.DB.FindOne(ctx, bson.M{"_id": uID}).Decode(&user); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "user not found", ""
		}
		zap.S().Warnw("bulk member remove: failed to load user", "userId", userID, "communityId", communityID, "error", err)
		return "failed to load user", ""
	}

	isMember := false
	for _, comm := range user.Details.Communities {
		if comm.CommunityID == communityID {
			isMember = true
			break
		}
	}
	if !isMember {
		return "not a member of this community", ""
	}

	if reason, err := u.removeUserFromCommunity(ctx, uID, cID, community, &user); err != nil {
		zap.S().Warnw("bulk member remove: failed", "userId", userID, "communityId", communityID, "error", err)
		return reason, ""
	}
	return "", user.Details.Username
}

// BulkDeleteCommunityCiviliansHandler deletes several of a community's
// civilians in one request.
//
// POST /api/v1/community/{communityId}/civilians/bulk-delete
// Body: {"civilianIds": ["...", ...]}
//
// The caller needs "manage members" (the owner and administrators always pass).
// Only civilians whose activeCommunityID is this community are deleted; any
// other id comes back as a failed item. Each delete runs the same cascade as the
// single delete endpoint and is audited as civilian.deleted.
func (c Civilian) BulkDeleteCommunityCiviliansHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CivilianIDs []string `json:"civilianIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	ids, err := normalizeBulkIDs(body.CivilianIDs)
	if err != nil {
		config.ErrorStatus(err.Error(), http.StatusBadRequest, w, err)
		return
	}

	community, cID, communityID := loadBulkCommunity(w, r, func(r *http.Request, cID primitive.ObjectID) (*models.Community, error) {
		ctx, cancel := api.WithQueryTimeout(r.Context())
		defer cancel()
		return c.CommDB.FindOne(ctx, bson.M{"_id": cID})
	})
	if community == nil {
		return
	}

	actorID, ok := authorizeBulkCommunityAction(w, r, community, "manage members")
	if !ok {
		return
	}
	actorName := ""
	if c.UDB != nil {
		actorName = resolveActorName(c.UDB, actorID)
	}

	resp := &bulkActionResponse{}
	for _, id := range ids {
		reason, civName := c.bulkDeleteOneCivilian(r, id, communityID)
		if reason != "" {
			resp.fail(id, reason)
			continue
		}
		if c.ALDB != nil {
			logAudit(c.ALDB, cID, "civilian.deleted", "civilian", actorID, actorName, id, civName, map[string]interface{}{"bulk": true})
		}
		resp.ok(id)
	}

	writeBulkResponse(w, resp)
}

// bulkDeleteOneCivilian checks and deletes one civilian. It returns a failure
// reason (empty on success) and the civilian's name for the audit log.
func (c Civilian) bulkDeleteOneCivilian(r *http.Request, civilianID, communityID string) (string, string) {
	oid, err := primitive.ObjectIDFromHex(civilianID)
	if err != nil {
		return "invalid civilian id", ""
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	civ, err := c.DB.FindOne(ctx, bson.M{"_id": oid})
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "civilian not found", ""
		}
		zap.S().Warnw("bulk civilian delete: failed to load civilian", "civilianId", civilianID, "communityId", communityID, "error", err)
		return "failed to load civilian", ""
	}
	if civ.Details.ActiveCommunityID != communityID {
		return "civilian is not in this community", ""
	}

	if err := c.deleteCivilian(ctx, oid); err != nil {
		zap.S().Warnw("bulk civilian delete: failed", "civilianId", civilianID, "communityId", communityID, "error", err)
		return "failed to delete civilian", ""
	}
	name := strings.TrimSpace(civ.Details.FirstName + " " + civ.Details.LastName)
	if name == "" {
		name = strings.TrimSpace(civ.Details.Name)
	}
	return "", name
}
