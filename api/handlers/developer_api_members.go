package handlers

import (
	"context"
	"errors"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/models"
)

// Membership from inside the game: players inviting friends, approving them,
// setting their department and rank, and removing them.
//
// The design problem here is that a key is not a person. Every one of these
// actions is one the website gates on *who is doing it*, so a key cannot simply
// be allowed to do them — it would be acting with authority nobody granted it.
//
// So every write names an **acting player** by their game identifier, and the
// permission check runs against that player exactly as the website would run it
// against a signed-in user. A key with members:write is permission to *ask on a
// player's behalf*, not permission to change the roster. The key can never
// grant more than the acting player holds.

// devMemberActor is the acting player on a membership write.
type devMemberActor struct {
	// ActingPlayer names the player taking the action, as provider:identifier.
	ActingPlayer string `json:"actingPlayer"`
}

type devAddMemberRequest struct {
	devMemberActor
	// Player is the friend being added, as provider:identifier. If we have
	// never seen them, their account is provisioned the same way
	// POST /dev/players would, so a mod does not need two calls.
	Player string `json:"player"`
	// Username is a display name for a player we have to create.
	Username string `json:"username"`
	// Status lets the mod add a friend as pending rather than approved, for a
	// community that wants to vet people. Defaults to approved, because a
	// player adding a friend from inside the game has already vetted them.
	Status string `json:"status"`
}

type devUpdateMemberRequest struct {
	devMemberActor
	DepartmentID string `json:"departmentId"`
	RankID       string `json:"rankId"`
	RoleID       string `json:"roleId"`
}

// resolvedPlayer is a player reference turned into an account.
type resolvedPlayer struct {
	UserID     string
	Provider   string
	Identifier string
	Username   string
	Claimed    bool
	Created    bool
}

// resolvePlayerRef turns "provider:identifier" into an account.
//
// `lpc:<userId>` is accepted too, and it is worth saying why that is not a hole:
// it buys the caller nothing. The user still has to pass whatever membership or
// permission check the route applies, and those are the real boundary. A forged
// id simply fails them.
//
// create decides whether an unknown identifier provisions a new account or is
// an error. Reads pass false: looking someone up should never create them.
func (d DeveloperAPI) resolvePlayerRef(ctx context.Context, dc *DevAPIContext, ref, username string, create bool) (*resolvedPlayer, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("a player reference is required, as provider:identifier")
	}

	parts := strings.SplitN(ref, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("player references look like fivem:<license> or discord:<id>")
	}
	provider := strings.ToLower(strings.TrimSpace(parts[0]))

	if provider == "lpc" {
		userID := strings.TrimSpace(parts[1])
		if objectIDOrZero(userID).IsZero() {
			return nil, fmt.Errorf("that is not a valid player reference")
		}
		var user models.User
		if err := d.UDB.FindOne(ctx, bson.M{"_id": objectIDOrZero(userID)}).Decode(&user); err != nil {
			return nil, fmt.Errorf("no such player")
		}
		return &resolvedPlayer{
			UserID: userID, Provider: "lpc", Identifier: userID,
			Username: user.Details.Username, Claimed: user.IsClaimed(),
		}, nil
	}

	if !models.IsValidPlayerProvider(provider) {
		return nil, fmt.Errorf("provider must be one of: fivem, discord, steam, lpc")
	}
	identifier := models.NormalizePlayerIdentifier(provider, parts[1])
	if identifier == "" {
		return nil, fmt.Errorf("a player identifier is required")
	}

	if existing, err := d.IdentityDB.FindOne(ctx, bson.M{"provider": provider, "identifier": identifier}); err == nil && existing != nil {
		resp := d.playerResponse(ctx, *existing, false)
		return &resolvedPlayer{
			UserID: resp.PlayerID, Provider: provider, Identifier: identifier,
			Username: resp.Username, Claimed: resp.Claimed,
		}, nil
	}

	if !create {
		return nil, fmt.Errorf("no player has that identifier")
	}

	// A friend we have never seen gets an account the same way a direct upsert
	// would make one: unclaimed, with no email and no password. Adding a friend
	// from in-game is the most common way a new player first appears, so
	// requiring the mod to call /dev/players first would be friction for no
	// safety gain — the account it creates is identical either way.
	player, created, err := d.resolveOrCreatePlayer(ctx, dc, provider, identifier, username)
	if err != nil {
		return nil, err
	}
	return &resolvedPlayer{
		UserID: player.PlayerID, Provider: provider, Identifier: identifier,
		Username: player.Username, Claimed: player.Claimed, Created: created,
	}, nil
}

// requireActingPlayerPermission resolves the acting player and checks they hold
// a community permission.
//
// This is the function that keeps a key honest. The website asks "does this
// signed-in user have manage members?"; we ask the same question about the
// player the mod says is acting, using the same helper
// (userHasCommunityPermission), so the answers cannot diverge.
func (d DeveloperAPI) requireActingPlayerPermission(ctx context.Context, dc *DevAPIContext, cc *CommunityContext, ref string, permissions ...string) (*resolvedPlayer, error) {
	actor, err := d.resolvePlayerRef(ctx, dc, ref, "", false)
	if err != nil {
		return nil, fmt.Errorf("actingPlayer: %w", err)
	}

	// The acting player must be an approved member before anything else. An
	// outsider cannot act in a community however the mod describes them.
	if !isApprovedMemberOfCommunity(ctx, d.UDB, actor.UserID, cc.Community.ID.Hex()) &&
		cc.Community.Details.OwnerID != actor.UserID {
		return nil, errActingPlayerNotAMember
	}
	if userBannedFromCommunity(&cc.Community, actor.UserID) {
		return nil, errActingPlayerBanned
	}
	if !userHasCommunityPermission(&cc.Community, actor.UserID, permissions...) {
		return nil, errActingPlayerLacksPermission
	}
	return actor, nil
}

var (
	errActingPlayerNotAMember      = fmt.Errorf("the acting player is not a member of that community")
	errActingPlayerBanned          = fmt.Errorf("the acting player is banned from that community")
	errActingPlayerLacksPermission = fmt.Errorf("the acting player does not have permission to do that")
)

// writeActingPlayerError turns an acting-player failure into a response that
// says which of the three things went wrong, because a mod author debugging
// this needs to know whether to fix their call or their player's role.
func (d DeveloperAPI) writeActingPlayerError(w http.ResponseWriter, r *http.Request, err error, permissions []string) {
	switch {
	case err == errActingPlayerNotAMember:
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "acting_player_not_a_member", err.Error(), nil)
	case err == errActingPlayerBanned:
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "acting_player_banned", err.Error(), nil)
	case err == errActingPlayerLacksPermission:
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "acting_player_lacks_permission",
			"The acting player does not have permission to do that. A key can never grant more than the player it acts for.",
			map[string]interface{}{"requiredPermission": permissions})
	default:
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_acting_player", err.Error(), nil)
	}
}

// DevListMembersHandler returns a community's roster.
//
// GET /api/v1/dev/communities/{communityId}/members — members:read
func (d DeveloperAPI) DevListMembersHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}
	_ = dc

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	limit, page, skip := api.ParseLimitPage(r, 50, 200)
	cur, err := d.UDB.Find(ctx, bson.M{
		"user.communities": bson.M{"$elemMatch": bson.M{
			"communityId": cc.Community.ID.Hex(),
			"status":      bson.M{"$in": []string{models.CommunityMemberStatusApproved, models.CommunityMemberStatusPending}},
		}},
	}, memberFindOptions(limit, skip))
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not load that roster.", nil)
		return
	}
	var users []models.User
	if err := cur.All(ctx, &users); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not load that roster.", nil)
		return
	}

	// Slim, and presentation-ish on purpose. A mod needs to know who is in the
	// community and what they can do in it; it has no business with their email
	// or their account state.
	members := make([]map[string]interface{}, 0, len(users))
	for _, u := range users {
		status := ""
		for _, m := range u.Details.Communities {
			if m.CommunityID == cc.Community.ID.Hex() {
				status = m.Status
			}
		}
		members = append(members, map[string]interface{}{
			"playerId":       u.ID,
			"username":       u.Details.Username,
			"status":         status,
			"claimed":        u.IsClaimed(),
			"gameIdentities": u.Details.GameIdentities,
			"isOwner":        cc.Community.Details.OwnerID == u.ID,
		})
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"members": members,
		"page":    page,
		"limit":   limit,
	})
}

// DevAddMemberHandler adds a friend to a community on an acting player's behalf.
//
// POST /api/v1/dev/communities/{communityId}/members — members:write
//
// If we have never seen the friend, their account is provisioned here, so
// "invite my mate" is one call from inside the game.
func (d DeveloperAPI) DevAddMemberHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var req devAddMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	permissions := []string{"manage members"}
	actor, err := d.requireActingPlayerPermission(ctx, dc, cc, req.ActingPlayer, permissions...)
	if err != nil {
		d.writeActingPlayerError(w, r, err, permissions)
		return
	}

	target, err := d.resolvePlayerRef(ctx, dc, req.Player, req.Username, true)
	if err != nil {
		if err == errProvisionCapReached {
			d.RecordOutcome(r, http.StatusTooManyRequests)
			devError(w, http.StatusTooManyRequests, "provision_cap_reached",
				fmt.Sprintf("This key may create %d player accounts a day. It resets at midnight UTC.",
					dc.Limits.AccountsPerKeyDay), nil)
			return
		}
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_player", err.Error(), nil)
		return
	}

	// A banned player is not re-invitable by a member. Lifting a ban is a
	// moderation decision and it belongs to the people who made it, on the
	// website, not to whoever happens to be holding a key.
	if userBannedFromCommunity(&cc.Community, target.UserID) {
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "player_banned",
			"That player is banned from this community. An admin has to lift the ban on the website first.",
			map[string]interface{}{"playerId": target.UserID})
		return
	}

	// Already a member: say so and do nothing. Idempotent rather than an error,
	// because a mod re-adding someone on every join is a normal thing to write.
	existingStatus, isMember := d.membershipStatus(ctx, target.UserID, cc.Community.ID.Hex())
	if isMember && existingStatus == models.CommunityMemberStatusApproved {
		d.RecordOutcome(r, http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"member":  d.memberSummary(target, models.CommunityMemberStatusApproved),
			"added":   false,
			"message": "That player is already a member.",
		})
		return
	}

	status := models.CommunityMemberStatusApproved
	if strings.EqualFold(strings.TrimSpace(req.Status), models.CommunityMemberStatusPending) {
		status = models.CommunityMemberStatusPending
	}

	if isMember {
		// A previously declined or pending row is flipped in place rather than
		// appended, so a player never ends up with two rows for one community.
		if _, err := d.UDB.UpdateOne(ctx,
			bson.M{"_id": objectIDOrZero(target.UserID), "user.communities.communityId": cc.Community.ID.Hex()},
			bson.M{"$set": bson.M{"user.communities.$.status": status}}); err != nil {
			devError(w, http.StatusInternalServerError, "server_error", "Could not add that player.", nil)
			return
		}
	} else {
		if _, err := d.UDB.UpdateOne(ctx,
			bson.M{"_id": objectIDOrZero(target.UserID), "user.communities": nil},
			bson.M{"$set": bson.M{"user.communities": bson.A{}}}); err != nil {
			zap.S().Debugw("developer api: communities array init", "error", err)
		}
		if _, err := d.UDB.UpdateOne(ctx,
			bson.M{"_id": objectIDOrZero(target.UserID)},
			bson.M{"$addToSet": bson.M{"user.communities": models.UserCommunity{
				ID:          primitive.NewObjectID().Hex(),
				CommunityID: cc.Community.ID.Hex(),
				Status:      status,
			}}}); err != nil {
			devError(w, http.StatusInternalServerError, "server_error", "Could not add that player.", nil)
			return
		}
	}

	if status == models.CommunityMemberStatusApproved {
		d.adjustMembersCount(ctx, cc.Community.ID, 1)
	}

	logAudit(d.ALDB, cc.Community.ID, "member.added_via_developer_key", "developer_api",
		actor.UserID, actor.Username, target.UserID, target.Username,
		map[string]interface{}{"keyId": dc.Key.ID.Hex(), "status": status, "playerCreated": target.Created})

	d.RecordOutcome(r, http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"member":        d.memberSummary(target, status),
		"added":         true,
		"playerCreated": target.Created,
		// A newly provisioned friend cannot log in yet, and the mod should tell
		// them so. Surfaced in the response rather than left for the developer
		// to remember.
		"needsClaim": !target.Claimed,
	})
}

// DevApproveMemberHandler approves a pending join request.
//
// POST /api/v1/dev/communities/{communityId}/members/{playerRef}/approve — members:write
func (d DeveloperAPI) DevApproveMemberHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var req devMemberActor
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	permissions := []string{"manage members"}
	actor, err := d.requireActingPlayerPermission(ctx, dc, cc, req.ActingPlayer, permissions...)
	if err != nil {
		d.writeActingPlayerError(w, r, err, permissions)
		return
	}

	target, err := d.resolvePlayerRef(ctx, dc, mux.Vars(r)["playerRef"], "", false)
	if err != nil {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "player_not_found", err.Error(), nil)
		return
	}

	status, isMember := d.membershipStatus(ctx, target.UserID, cc.Community.ID.Hex())
	if !isMember {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "no_request",
			"That player has not asked to join this community.", nil)
		return
	}
	if status == models.CommunityMemberStatusApproved {
		d.RecordOutcome(r, http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"member": d.memberSummary(target, status), "approved": false,
			"message": "That player is already approved.",
		})
		return
	}
	if status == models.CommunityMemberStatusBanned {
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "player_banned",
			"That player is banned. An admin has to lift the ban on the website first.", nil)
		return
	}

	if _, err := d.UDB.UpdateOne(ctx,
		bson.M{"_id": objectIDOrZero(target.UserID), "user.communities.communityId": cc.Community.ID.Hex()},
		bson.M{"$set": bson.M{"user.communities.$.status": models.CommunityMemberStatusApproved}}); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not approve that player.", nil)
		return
	}
	d.adjustMembersCount(ctx, cc.Community.ID, 1)

	logAudit(d.ALDB, cc.Community.ID, "member.approved_via_developer_key", "developer_api",
		actor.UserID, actor.Username, target.UserID, target.Username,
		map[string]interface{}{"keyId": dc.Key.ID.Hex()})

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"member": d.memberSummary(target, models.CommunityMemberStatusApproved), "approved": true,
	})
}

// DevUpdateMemberHandler sets a member's department and rank.
//
// PATCH /api/v1/dev/communities/{communityId}/members/{playerRef} — members:write
//
// Deliberately narrower than the website's own editor: department and rank
// only. Roles carry permissions, and a key handing out permissions — including
// permissions the acting player does not have — is a privilege-escalation path
// that is not worth opening for a convenience. A mod that needs to promote
// someone into a role can send them to the website.
func (d DeveloperAPI) DevUpdateMemberHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var req devUpdateMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}
	if strings.TrimSpace(req.RoleID) != "" {
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "roles_not_assignable",
			"A key cannot assign roles, because roles carry permissions. "+
				"Set a department and rank here, and change roles on the website.", nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	permissions := []string{"manage members"}
	actor, err := d.requireActingPlayerPermission(ctx, dc, cc, req.ActingPlayer, permissions...)
	if err != nil {
		d.writeActingPlayerError(w, r, err, permissions)
		return
	}

	target, err := d.resolvePlayerRef(ctx, dc, mux.Vars(r)["playerRef"], "", false)
	if err != nil {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "player_not_found", err.Error(), nil)
		return
	}
	if status, isMember := d.membershipStatus(ctx, target.UserID, cc.Community.ID.Hex()); !isMember || status != models.CommunityMemberStatusApproved {
		d.RecordOutcome(r, http.StatusConflict)
		devError(w, http.StatusConflict, "not_a_member",
			"That player is not an approved member of this community yet.", nil)
		return
	}

	departmentID := strings.TrimSpace(req.DepartmentID)
	if departmentID == "" {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_department", "departmentId is required.", nil)
		return
	}
	dept := findCommunityDepartment(&cc.Community, departmentID)
	if dept == nil {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "department_not_found",
			"This community has no department with that id.", nil)
		return
	}
	// A rank has to exist in the community, or a mod could write a dangling id
	// that every surface then has to tolerate.
	rankID := strings.TrimSpace(req.RankID)
	if rankID != "" && !communityHasRank(&cc.Community, rankID) {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "rank_not_found", "This community has no rank with that id.", nil)
		return
	}

	entry := models.MemberStatus{
		UserID: target.UserID,
		Status: models.CommunityMemberStatusApproved,
		RankID: rankID,
	}

	// Replace the player's row in that department if they already have one,
	// otherwise append. Two rows for one person in one department would make
	// their rank ambiguous.
	// Replace the player's row in that department if they already have one,
	// otherwise append. Two rows for one person in one department would make
	// their rank ambiguous.
	//
	// FindOneAndUpdate rather than UpdateOne because this has to know whether
	// it matched: UpdateOne on this store returns only an error, so an update
	// that found nobody would be indistinguishable from one that worked, and
	// the append below would never run.
	_, err = d.CDB.FindOneAndUpdate(ctx, bson.M{
		"_id":                                  cc.Community.ID,
		"community.departments.id":             objectIDOrZero(departmentID),
		"community.departments.members.userID": target.UserID,
	}, bson.M{"$set": bson.M{
		"community.departments.$[dept].members.$[member].rankId": rankID,
		"community.departments.$[dept].members.$[member].status": models.CommunityMemberStatusApproved,
	}}, departmentMemberArrayFilters(departmentID, target.UserID))

	if err != nil {
		if !errors.Is(err, mongo.ErrNoDocuments) {
			devError(w, http.StatusInternalServerError, "server_error", "Could not update that member.", nil)
			return
		}
		// Nobody matched, so this is their first time in the department.
		if addErr := d.CDB.UpdateOne(ctx,
			bson.M{"_id": cc.Community.ID, "community.departments.id": objectIDOrZero(departmentID)},
			bson.M{"$addToSet": bson.M{"community.departments.$.members": entry}}); addErr != nil {
			devError(w, http.StatusInternalServerError, "server_error", "Could not update that member.", nil)
			return
		}
	}

	logAudit(d.ALDB, cc.Community.ID, "member.department_set_via_developer_key", "developer_api",
		actor.UserID, actor.Username, target.UserID, target.Username,
		map[string]interface{}{"keyId": dc.Key.ID.Hex(), "departmentId": departmentID, "rankId": rankID})

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"member":       d.memberSummary(target, models.CommunityMemberStatusApproved),
		"departmentId": departmentID,
		"rankId":       rankID,
	})
}

// DevRemoveMemberHandler removes a member, or lets a player leave.
//
// DELETE /api/v1/dev/communities/{communityId}/members/{playerRef} — members:write
//
// Leaving is the same call with the acting player as the target, and it needs no
// permission: anyone may leave a community. Removing somebody else needs
// manage members.
func (d DeveloperAPI) DevRemoveMemberHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var req devMemberActor
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.ActingPlayer) == "" {
		req.ActingPlayer = r.URL.Query().Get("actingPlayer")
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	target, err := d.resolvePlayerRef(ctx, dc, mux.Vars(r)["playerRef"], "", false)
	if err != nil {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "player_not_found", err.Error(), nil)
		return
	}

	// Leaving needs no permission; removing someone else does.
	leaving := false
	if actorRef := strings.TrimSpace(req.ActingPlayer); actorRef != "" {
		if actor, aErr := d.resolvePlayerRef(ctx, dc, actorRef, "", false); aErr == nil && actor.UserID == target.UserID {
			leaving = true
		}
	}

	var actor *resolvedPlayer
	if leaving {
		actor = target
	} else {
		permissions := []string{"manage members"}
		actor, err = d.requireActingPlayerPermission(ctx, dc, cc, req.ActingPlayer, permissions...)
		if err != nil {
			d.writeActingPlayerError(w, r, err, permissions)
			return
		}
	}

	// The owner cannot be removed and cannot leave. A community with no owner
	// has nobody who can administer it, and ownership transfer is a deliberate
	// act that belongs on the website where it can be confirmed.
	if cc.Community.Details.OwnerID == target.UserID {
		d.RecordOutcome(r, http.StatusConflict)
		devError(w, http.StatusConflict, "owner_cannot_be_removed",
			"The community owner cannot be removed or leave. Transfer ownership on the website first.", nil)
		return
	}

	status, isMember := d.membershipStatus(ctx, target.UserID, cc.Community.ID.Hex())
	if !isMember {
		d.RecordOutcome(r, http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"removed": false, "message": "That player is not a member of this community.",
		})
		return
	}

	if _, err := d.UDB.UpdateOne(ctx, bson.M{"_id": objectIDOrZero(target.UserID)},
		bson.M{"$pull": bson.M{"user.communities": bson.M{"communityId": cc.Community.ID.Hex()}}}); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not remove that player.", nil)
		return
	}
	// Also drop them from every department, or they keep showing up on rosters.
	if err := d.CDB.UpdateOne(ctx, bson.M{"_id": cc.Community.ID},
		bson.M{"$pull": bson.M{"community.departments.$[].members": bson.M{"userID": target.UserID}}}); err != nil {
		zap.S().Warnw("developer api: could not clear department membership on removal",
			"communityId", cc.Community.ID.Hex(), "error", err)
	}
	if status == models.CommunityMemberStatusApproved {
		d.adjustMembersCount(ctx, cc.Community.ID, -1)
	}

	action := "member.removed_via_developer_key"
	if leaving {
		action = "member.left_via_developer_key"
	}
	logAudit(d.ALDB, cc.Community.ID, action, "developer_api",
		actor.UserID, actor.Username, target.UserID, target.Username,
		map[string]interface{}{"keyId": dc.Key.ID.Hex()})

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"removed": true, "left": leaving})
}

// ---- small shared pieces ----

// membershipStatus reads a player's status in a community from the user
// document, which is the canonical source. The community's members map is not:
// it is only populated once someone sets a ten-code, so a real member who has
// never gone on duty is missing from it.
func (d DeveloperAPI) membershipStatus(ctx context.Context, userID, communityID string) (string, bool) {
	var user models.User
	if err := d.UDB.FindOne(ctx, bson.M{"_id": objectIDOrZero(userID)}).Decode(&user); err != nil {
		return "", false
	}
	for _, m := range user.Details.Communities {
		if m.CommunityID == communityID {
			return m.Status, true
		}
	}
	return "", false
}

// memberSummary is the slim member shape the developer surface returns.
func (d DeveloperAPI) memberSummary(p *resolvedPlayer, status string) map[string]interface{} {
	return map[string]interface{}{
		"playerId":   p.UserID,
		"playerRef":  p.Provider + ":" + p.Identifier,
		"username":   p.Username,
		"status":     status,
		"claimed":    p.Claimed,
	}
}

// adjustMembersCount keeps the denormalized count honest.
//
// $inc rather than a recount, so two mods adding players at once cannot both
// read the same number and write the same total.
func (d DeveloperAPI) adjustMembersCount(ctx context.Context, communityID primitive.ObjectID, delta int) {
	if err := d.CDB.UpdateOne(ctx, bson.M{"_id": communityID},
		bson.M{"$inc": bson.M{"community.membersCount": delta}}); err != nil {
		zap.S().Warnw("developer api: could not adjust membersCount",
			"communityId", communityID.Hex(), "delta", delta, "error", err)
	}
}

// findCommunityDepartment looks a department up by hex id.
func findCommunityDepartment(community *models.Community, departmentID string) *models.Department {
	oid := objectIDOrZero(departmentID)
	if oid.IsZero() {
		return nil
	}
	for i := range community.Details.Departments {
		if community.Details.Departments[i].ID == oid {
			return &community.Details.Departments[i]
		}
	}
	return nil
}

// communityHasRank reports whether a rank id exists anywhere in the community.
func communityHasRank(community *models.Community, rankID string) bool {
	for _, dept := range community.Details.Departments {
		for _, rank := range dept.Ranks {
			if rank.ID.Hex() == rankID {
				return true
			}
		}
	}
	return false
}

// departmentMemberArrayFilters targets one member inside one department, so an
// update touches that person's row and nobody else's.
func departmentMemberArrayFilters(departmentID, userID string) *options.FindOneAndUpdateOptions {
	return options.FindOneAndUpdate().SetArrayFilters(options.ArrayFilters{Filters: []interface{}{
		bson.M{"dept.id": objectIDOrZero(departmentID)},
		bson.M{"member.userID": userID},
	}})
}

// memberFindOptions pages a roster query.
//
// The sort is not decoration: skip/limit without one returns an undefined
// order, so page two can repeat or omit rows from page one.
func memberFindOptions(limit, skip int64) *options.FindOptions {
	return options.Find().SetLimit(limit).SetSkip(skip).SetSort(bson.D{{Key: "_id", Value: 1}})
}
