package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/models"
)

// Community provisioning: a key creating a community for its developer.
//
// The community is owned by the developer's own LPC account and counts against
// their plan exactly like one they made by hand on the website. A key is a
// convenience for a developer, not a way around the limits that apply to them.

// maxProvisionedCommunityNameLen matches what the website accepts.
const maxProvisionedCommunityNameLen = 60

type devCreateCommunityRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Visibility  string   `json:"visibility"`
}

// DevCreateCommunityHandler creates a community owned by the calling key's
// developer, and grants the key access to it.
//
// POST /api/v1/dev/communities — communities:provision
//
// Three limits apply, and they are separate on purpose:
//   - the developer's own community cap from their LPC plan, because this is
//     their community;
//   - a per-key daily creation cap, so a mod looping by mistake cannot make
//     a hundred communities before anyone notices;
//   - a per-key total cap, so one key cannot accumulate an unbounded estate.
func (d DeveloperAPI) DevCreateCommunityHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}

	var req devCreateCommunityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		devError(w, http.StatusBadRequest, "invalid_name", "name is required.", nil)
		return
	}
	if len(name) > maxProvisionedCommunityNameLen {
		name = strings.TrimSpace(name[:maxProvisionedCommunityNameLen])
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Daily cap first: it is the cheapest check and the one a runaway loop
	// trips.
	if dc.Usage.CommunitiesCreated >= dc.Limits.CommunitiesPerKeyDay {
		d.RecordOutcome(r, http.StatusTooManyRequests)
		devError(w, http.StatusTooManyRequests, "provision_cap_reached",
			fmt.Sprintf("This key may create %d communities a day. It resets at midnight UTC.",
				dc.Limits.CommunitiesPerKeyDay),
			map[string]interface{}{"limit": dc.Limits.CommunitiesPerKeyDay, "scope": "communities_per_day"})
		return
	}

	// How many communities the developer may own at all is their website plan's
	// business and nothing else's. There is deliberately no per-key total here:
	// a key is a tool its owner uses, so capping it separately would have meant
	// telling a Premium customer they cannot make their sixth community because
	// a developer tier said three.
	//
	// Read from the owner's user record, never from anything the key sent.
	ownerID := dc.Key.UserID
	ownerOID := objectIDOrZero(ownerID)
	if ownerOID.IsZero() {
		devError(w, http.StatusInternalServerError, "server_error", "Could not resolve the key's owner.", nil)
		return
	}
	var owner models.User
	if err := d.UDB.FindOne(ctx, bson.M{"_id": devEq(ownerOID)}).Decode(&owner); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not resolve the key's owner.", nil)
		return
	}
	plan := models.PlanFromUser(&owner)
	cap := models.CommunityCap(plan)
	active, _ := d.CDB.CountDocuments(ctx, bson.M{"community.ownerID": devEq(ownerID)})
	if int(active) >= cap {
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "owner_community_limit_reached",
			fmt.Sprintf("The developer's %s plan allows %d communities and they have %d. "+
				"A community made with a key counts the same as one made on the website.", plan, cap, active),
			map[string]interface{}{"plan": plan, "cap": cap, "active": active})
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	community := models.Community{ID: primitive.NewObjectID()}
	community.Details.Name = name
	community.Details.OwnerID = ownerID
	community.Details.Description = strings.TrimSpace(req.Description)
	community.Details.Tags = req.Tags
	community.Details.Visibility = normalizeProvisionedVisibility(req.Visibility)
	community.Details.CreatedAt = now
	community.Details.UpdatedAt = now
	community.Details.MembersCount = 1

	// The same defaults the website's own create path writes, so a community a
	// mod made is not a second-class one: it has ten-codes, penal codes and a
	// Head Admin role, and it behaves identically everywhere else in the
	// product.
	community.Details.InviteCodeIds = []string{}
	community.Details.BanList = []string{}
	community.Details.Departments = []models.Department{}
	community.Details.TenCodes = models.DefaultTenCodes()
	community.Details.Fines = models.DefaultCommunityFines()
	community.Details.PenalCodes = models.DefaultCommunityPenalCodes()
	community.Details.Events = []models.Event{}
	community.Details.Roles = append(community.Details.Roles, models.BuildHeadAdminRole(ownerID))

	// Which key made it, so a misbehaving key's communities can be found.
	community.Details.Source = &models.RecordSource{
		Type:        models.RecordSourceDeveloperAPI,
		KeyID:       dc.Key.ID.Hex(),
		DeveloperID: dc.Key.DeveloperID,
		CreatedAt:   now,
	}

	if _, err := d.CDB.InsertOne(ctx, community); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not create that community.", nil)
		return
	}

	// The owner's own membership, the same shape the website writes.
	_, _ = d.UDB.UpdateOne(ctx, bson.M{"_id": devEq(ownerOID), "user.communities": nil},
		bson.M{"$set": bson.M{"user.communities": bson.A{}}})
	_, _ = d.UDB.UpdateOne(ctx, bson.M{"_id": devEq(ownerOID)}, bson.M{"$addToSet": bson.M{
		"user.communities": models.UserCommunity{
			ID:          primitive.NewObjectID().Hex(),
			CommunityID: community.ID.Hex(),
			Status:      models.CommunityMemberStatusApproved,
		},
	}})

	// Grant the key access to what it just made. Marked created_by_key, which
	// is what distinguishes it from a community an owner let this key into.
	grant := models.DeveloperCommunityGrant{
		ID:          primitive.NewObjectID(),
		KeyID:       dc.Key.ID.Hex(),
		DeveloperID: dc.Key.DeveloperID,
		CommunityID: community.ID.Hex(),
		Source:      models.DeveloperGrantCreatedByKey,
		Status:      models.DeveloperKeyActive,
		RequestedAt: now,
		ApprovedBy:  ownerID,
		ApprovedAt:  &now,
	}
	if _, err := d.GrantDB.InsertOne(ctx, grant); err != nil {
		// The community exists and is the developer's; only the key's access to
		// it is missing. Say so plainly rather than pretending the call failed,
		// so a mod author does not retry and make a second community.
		devError(w, http.StatusInternalServerError, "grant_failed",
			"The community was created but this key could not be granted access to it. "+
				"Approve the key for it from the community's settings.",
			map[string]interface{}{"communityId": community.ID.Hex()})
		return
	}

	d.countProvisioned(ctx, dc.Key, bson.M{"communitiesCreated": 1})
	logAudit(d.ALDB, community.ID, "community.created_by_developer_key", "developer_api",
		ownerID, resolveActorName(d.UDB, ownerID), dc.Key.ID.Hex(), name,
		map[string]interface{}{"keyId": dc.Key.ID.Hex(), "developerId": dc.Key.DeveloperID})

	d.RecordOutcome(r, http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"community": map[string]interface{}{
			"id":         community.ID.Hex(),
			"name":       community.Details.Name,
			"ownerId":    ownerID,
			"visibility": community.Details.Visibility,
			"createdAt":  now.Time().UTC().Format(time.RFC3339),
		},
	})
}

// normalizeProvisionedVisibility defaults a community to private.
//
// A mod creating a community has not chosen to advertise it, and a community
// that appears in the public listing before its owner meant it to is harder to
// undo than one that does not.
func normalizeProvisionedVisibility(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), "public") {
		return "public"
	}
	return "private"
}
