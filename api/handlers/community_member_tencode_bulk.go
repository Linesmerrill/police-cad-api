package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// maxBulkTenCodeUnits caps how many units one bulk status change may touch.
// The dispatch dashboards load at most 100 units per page.
const maxBulkTenCodeUnits = 100

// mergeMemberTenCode applies a ten-code change to a member's entry the way the
// dispatch dashboards expect: a field left empty in the request keeps the
// member's current value. Shared by the single and bulk handlers so the two can
// never drift on what a status change writes.
func mergeMemberTenCode(existing, requested models.MemberDetail) models.MemberDetail {
	return models.MemberDetail{
		DepartmentID:         getStringOrDefault(requested.DepartmentID, existing.DepartmentID),
		TenCodeID:            getStringOrDefault(requested.TenCodeID, existing.TenCodeID),
		IsOnline:             existing.IsOnline,
		ActiveDepartmentID:   getStringOrDefault(requested.ActiveDepartmentID, existing.ActiveDepartmentID),
		ActiveDepartmentName: getStringOrDefault(requested.ActiveDepartmentName, existing.ActiveDepartmentName),
		DepartmentCallSigns:  existing.DepartmentCallSigns,
	}
}

// findCommunityTenCode returns the community's configured ten-code with the
// given id, if any.
func findCommunityTenCode(community *models.Community, tenCodeID string) (models.TenCodes, bool) {
	for _, tc := range community.Details.TenCodes {
		if tc.ID.Hex() == tenCodeID {
			return tc, true
		}
	}
	return models.TenCodes{}, false
}

// notifyUnitStatusChanged broadcasts a member's new ten-code so dispatch
// dashboards update without polling. The code and description are looked up
// from the community's configured ten-codes so subscribers don't need a second
// round-trip.
func (c Community) notifyUnitStatusChanged(community *models.Community, communityID, userID string, member models.MemberDetail) {
	tc, _ := findCommunityTenCode(community, member.TenCodeID)
	go c.notifyNodeServerPanic("dispatch_unit_status_changed", map[string]interface{}{
		"communityId":        communityID,
		"userId":             userID,
		"tenCodeId":          member.TenCodeID,
		"tenCode":            tc.Code,
		"tenCodeDescription": tc.Description,
		"activeDepartmentId": member.ActiveDepartmentID,
	})
}

// isDispatchDepartment reports whether a department uses the Dispatch template.
// The dashboards pick the dispatch layout from the template name, compared
// without regard to case.
func isDispatchDepartment(d models.Department) bool {
	return strings.EqualFold(strings.TrimSpace(d.Template.Name), "dispatch")
}

// isApprovedCommunityMember reports whether the user is an approved member of
// the community, read from the user's own communities list (the community's
// members map is not membership).
func (c Community) isApprovedCommunityMember(ctx context.Context, community *models.Community, userID string) bool {
	if community.Details.OwnerID == userID {
		return true
	}
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return false
	}
	count, err := c.UDB.CountDocuments(ctx, bson.M{
		"_id": uID,
		"user.communities": bson.M{"$elemMatch": bson.M{
			"communityId": community.ID.Hex(),
			"status":      "approved",
		}},
	})
	return err == nil && count > 0
}

// canDispatchUnits reports whether the actor may set other units' statuses in
// bulk: the community owner, anyone with "administrator", or anyone who can use
// a Dispatch department. An approval-required Dispatch department admits only
// its approved members; an open one admits every community member, the same
// rule that decides who can open that department's dashboard.
func (c Community) canDispatchUnits(ctx context.Context, community *models.Community, actorID string) bool {
	if userHasCommunityPermission(community, actorID) {
		return true
	}
	hasOpenDispatch := false
	for _, d := range community.Details.Departments {
		if !isDispatchDepartment(d) {
			continue
		}
		if !d.ApprovalRequired {
			hasOpenDispatch = true
			continue
		}
		for _, m := range d.Members {
			if m.UserID == actorID && isApprovedDepartmentMember(m) {
				return true
			}
		}
	}
	return hasOpenDispatch && c.isApprovedCommunityMember(ctx, community, actorID)
}

// approvedCommunityMemberIDs returns which of the given users are approved
// members of the community, in one query.
func (c Community) approvedCommunityMemberIDs(ctx context.Context, community *models.Community, ids []primitive.ObjectID) (map[string]bool, error) {
	members := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return members, nil
	}
	cursor, err := c.UDB.Find(ctx, bson.M{
		"_id": bson.M{"$in": ids},
		"user.communities": bson.M{"$elemMatch": bson.M{
			"communityId": community.ID.Hex(),
			"status":      "approved",
		}},
	}, options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, err
	}
	var found []struct {
		ID primitive.ObjectID `bson:"_id"`
	}
	if err := cursor.All(ctx, &found); err != nil {
		return nil, err
	}
	for _, f := range found {
		members[f.ID.Hex()] = true
	}
	// The owner is always a member, whatever their communities list says.
	for _, id := range ids {
		if id.Hex() == community.Details.OwnerID {
			members[id.Hex()] = true
		}
	}
	return members, nil
}

type bulkMemberTenCodeRequest struct {
	UserIDs              []string `json:"userIds"`
	DepartmentID         string   `json:"departmentId"`
	TenCodeID            string   `json:"tenCodeId"`
	ActiveDepartmentID   string   `json:"activeDepartmentId"`
	ActiveDepartmentName string   `json:"activeDepartmentName"`
}

type bulkItemResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// BulkSetMemberTenCodeHandler sets one ten-code on many units at once, for a
// dispatcher clearing a shift or moving several units to the same status.
// PUT /api/v1/community/{communityId}/members/tenCode/bulk
//
// Each unit's entry changes exactly as the single endpoint would change it, and
// each changed unit gets its own dispatch_unit_status_changed broadcast, so
// other viewers see the same events they see for single changes.
func (c Community) BulkSetMemberTenCodeHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	var req bulkMemberTenCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	req.TenCodeID = strings.TrimSpace(req.TenCodeID)
	if req.TenCodeID == "" {
		config.ErrorStatus("tenCodeId is required", http.StatusBadRequest, w, fmt.Errorf("missing tenCodeId"))
		return
	}

	// Dedupe, keeping the caller's order so results line up with the request.
	seen := make(map[string]bool, len(req.UserIDs))
	userIDs := make([]string, 0, len(req.UserIDs))
	for _, id := range req.UserIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		userIDs = append(userIDs, id)
	}
	if len(userIDs) == 0 {
		config.ErrorStatus("userIds is required", http.StatusBadRequest, w, fmt.Errorf("no userIds"))
		return
	}
	if len(userIDs) > maxBulkTenCodeUnits {
		config.ErrorStatus(fmt.Sprintf("at most %d units can be updated at once", maxBulkTenCodeUnits),
			http.StatusBadRequest, w, fmt.Errorf("%d userIds", len(userIDs)))
		return
	}

	// The browser never holds the gateway secret, so a forged ?userId= cannot
	// pass as a dispatcher here; the website calls this from its server.
	actorID := resolveAdjustActor(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil || community == nil {
		config.InfoStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	if !c.canDispatchUnits(ctx, community, actorID) {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w,
			fmt.Errorf("user %s cannot set unit statuses in community %s", actorID, communityID))
		return
	}

	if _, ok := findCommunityTenCode(community, req.TenCodeID); !ok {
		config.ErrorStatus("unknown ten-code", http.StatusBadRequest, w, fmt.Errorf("ten-code %s not in community", req.TenCodeID))
		return
	}

	results := make([]bulkItemResult, len(userIDs))
	var validIDs []primitive.ObjectID
	for i, id := range userIDs {
		results[i].ID = id
		oid, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			results[i].Error = "invalid user id"
			continue
		}
		validIDs = append(validIDs, oid)
	}

	members, err := c.approvedCommunityMemberIDs(ctx, community, validIDs)
	if err != nil {
		config.ErrorStatus("failed to look up community members", http.StatusInternalServerError, w, err)
		return
	}

	requested := models.MemberDetail{
		DepartmentID:         req.DepartmentID,
		TenCodeID:            req.TenCodeID,
		ActiveDepartmentID:   req.ActiveDepartmentID,
		ActiveDepartmentName: req.ActiveDepartmentName,
	}
	set := bson.M{}
	updated := make(map[string]models.MemberDetail)
	for i, id := range userIDs {
		if results[i].Error != "" {
			continue
		}
		if !members[id] {
			results[i].Error = "not a member of this community"
			continue
		}
		merged := mergeMemberTenCode(community.Details.Members[id], requested)
		prefix := "community.members." + id + "."
		set[prefix+"departmentID"] = merged.DepartmentID
		set[prefix+"tenCodeID"] = merged.TenCodeID
		set[prefix+"activeDepartmentId"] = merged.ActiveDepartmentID
		set[prefix+"activeDepartmentName"] = merged.ActiveDepartmentName
		updated[id] = merged
	}

	if len(updated) > 0 {
		// A dotted $set cannot reach into a members map stored as null, so give
		// legacy communities an empty map first. A no-op for everyone else.
		if community.Details.Members == nil {
			if err := c.DB.UpdateOne(ctx, bson.M{"_id": cID, "community.members": nil},
				bson.M{"$set": bson.M{"community.members": bson.M{}}}); err != nil {
				config.ErrorStatus("failed to update unit statuses", http.StatusInternalServerError, w, err)
				return
			}
		}
		// Only the listed members' status fields are written, so concurrent
		// changes to other members are never overwritten.
		if err := c.DB.UpdateOne(ctx, bson.M{"_id": cID}, bson.M{"$set": set}); err != nil {
			config.ErrorStatus("failed to update unit statuses", http.StatusInternalServerError, w, err)
			return
		}
	}

	succeeded := 0
	for i := range results {
		if results[i].Error != "" {
			continue
		}
		results[i].OK = true
		succeeded++
		c.notifyUnitStatusChanged(community, communityID, results[i].ID, updated[results[i].ID])
	}

	zap.S().Infow("bulk unit status change",
		"community_id", communityID, "actor_id", actorID, "ten_code_id", req.TenCodeID,
		"requested", len(userIDs), "succeeded", succeeded)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"results":   results,
		"succeeded": succeeded,
		"failed":    len(results) - succeeded,
	})
}
