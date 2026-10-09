package handlers

import (
	"encoding/json"
	"net/http"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/models"
)

// A community must never be orphaned. Its owner cannot leave (the remove
// paths refuse them), and they must always hold administrator through some
// role, even when they no longer play: otherwise nobody may be left who can
// approve a join request, assign a role, or hand the community on.
//
// The role endpoints below are the ways an owner could lose admin without
// leaving. Each one computes the roles as they would be after the change and
// refuses it when the owner had admin before and would not after. A legacy
// community whose owner never had admin is not made worse by an unrelated
// edit, so those are let through; the load-path heal and the
// backfill_owner_admin script restore the owner's admin there.

const ownerMustKeepAdminMessage = "The community owner must keep Head Admin. Transfer ownership to someone else first."

// stripsOwnerAdmin reports whether replacing the community's roles with after
// would leave its owner without an enabled administrator permission they hold
// today.
func stripsOwnerAdmin(community *models.Community, after []models.Role) bool {
	if community == nil {
		return false
	}
	owner := community.Details.OwnerID
	if owner == "" {
		return false
	}
	return models.OwnerHasAdmin(community.Details.Roles, owner) && !models.OwnerHasAdmin(after, owner)
}

// writeOwnerMustKeepAdmin renders the 409 for a refused role change. The reason
// is carried both at the top level and in the standard `response` envelope so
// either client parser finds it.
func writeOwnerMustKeepAdmin(w http.ResponseWriter, communityID primitive.ObjectID) {
	zap.S().Infow("refused role change that would strip the owner's admin", "community_id", communityID.Hex())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":   "owner_must_keep_admin",
		"message": ownerMustKeepAdminMessage,
		"response": map[string]interface{}{
			"error":   "owner_must_keep_admin",
			"message": ownerMustKeepAdminMessage,
		},
	})
}

// rolesWith returns a copy of roles with fn applied to the role whose ID is
// roleID. Members and Permissions are copied so fn can't alias the stored
// community.
func rolesWith(roles []models.Role, roleID primitive.ObjectID, fn func(*models.Role)) []models.Role {
	out := make([]models.Role, len(roles))
	for i, r := range roles {
		r.Members = append([]string(nil), r.Members...)
		r.Permissions = append([]models.Permission(nil), r.Permissions...)
		if r.ID == roleID {
			fn(&r)
		}
		out[i] = r
	}
	return out
}

// rolesWithout returns roles minus the role whose ID is roleID.
func rolesWithout(roles []models.Role, roleID primitive.ObjectID) []models.Role {
	out := make([]models.Role, 0, len(roles))
	for _, r := range roles {
		if r.ID != roleID {
			out = append(out, r)
		}
	}
	return out
}
