package models

import (
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// This file is the single source of truth for "what a current community should
// look like." Communities created before the permission/roles system are
// missing fields that newer communities get at creation (a Head Admin role,
// ten-codes, penal codes, an initialized members map, etc.), which leaves the
// owner locked out of their own community. CreateCommunityHandler, the
// admin schema-sync endpoint, the owner-facing community load path, and the
// batch backfill script all build the same canonical objects from here so a
// remediated community is indistinguishable from a freshly-created one.

// IsHeadAdminRole identifies the Head Admin role by its permission structure,
// not by name (owners can rename roles). The Head Admin role has an enabled
// "administrator" permission whose description is "Head Admin".
func IsHeadAdminRole(role Role) bool {
	for _, perm := range role.Permissions {
		if perm.Name == "administrator" && perm.Description == "Head Admin" && perm.Enabled {
			return true
		}
	}
	return false
}

// OwnerHasAdmin reports whether the owner is a member of any role that has an
// enabled "administrator" permission — i.e. the owner is NOT locked out. This
// is broader than IsHeadAdminRole on purpose: an owner who holds admin through
// any role does not need a Head Admin role stamped on them.
func OwnerHasAdmin(roles []Role, ownerID string) bool {
	if ownerID == "" {
		return false
	}
	for _, r := range roles {
		hasOwner := false
		for _, m := range r.Members {
			if m == ownerID {
				hasOwner = true
				break
			}
		}
		if !hasOwner {
			continue
		}
		for _, p := range r.Permissions {
			if p.Enabled && strings.EqualFold(p.Name, "administrator") {
				return true
			}
		}
	}
	return false
}

// BuildHeadAdminRole returns the canonical Head Admin role stamped onto every
// new community: only the first "administrator"/"Head Admin" permission is
// enabled; the rest are present-but-disabled so the owner can toggle them.
func BuildHeadAdminRole(ownerID string) Role {
	return Role{
		ID:      primitive.NewObjectID(),
		Name:    "Head Admin",
		Members: []string{ownerID},
		Permissions: []Permission{
			{ID: primitive.NewObjectID(), Name: "administrator", Description: "Head Admin", Enabled: true},
			{ID: primitive.NewObjectID(), Name: "manage community settings", Description: "Allows managing community settings", Enabled: false},
			{ID: primitive.NewObjectID(), Name: "manage community events", Description: "Allows managing community events", Enabled: false},
			{ID: primitive.NewObjectID(), Name: "manage departments", Description: "Allows managing departments", Enabled: false},
			{ID: primitive.NewObjectID(), Name: "manage roles", Description: "Allows managing roles", Enabled: false},
			{ID: primitive.NewObjectID(), Name: "manage members", Description: "Allows managing members", Enabled: false},
			{ID: primitive.NewObjectID(), Name: "manage bans", Description: "Allows managing bans", Enabled: false},
			{ID: primitive.NewObjectID(), Name: "administrator", Description: "Members with this permission will have every permission and will also bypass all community specific permissions or restrictions (for example, these members would get access to all settings and pages). This is a dangerous permission to grant.", Enabled: false},
		},
	}
}

// BuildCommunityBackfill computes a non-destructive, idempotent set of repairs
// that bring a legacy community up to the current schema. It returns a $set map
// (keyed by bson path) and a human-readable list of the actions taken. It only
// fills fields that are missing/empty — existing non-empty values are never
// overwritten — so re-running on an already-healthy community returns no
// actions and an empty set (the caller should skip the write in that case).
//
// updatedAt is stamped only when at least one repair is made, so a no-op sync
// does not bump the timestamp.
func BuildCommunityBackfill(c *Community) (bson.M, []string) {
	set := bson.M{}
	var actions []string

	owner := strings.TrimSpace(c.Details.OwnerID)

	// "Owner locked out" — the owner holds admin through no role — is the
	// defining signal of a pre-permissions legacy community, and the exact set
	// the batch backfill targets. We gate ALL repairs on it so that any healthy
	// community (whose owner already has admin) is never written on the hot read
	// path. There is no realistic way for a non-legacy community to be missing
	// these fields, so nothing is lost by scoping the repair this way.
	if owner == "" || OwnerHasAdmin(c.Details.Roles, owner) {
		return set, actions
	}

	// Head Admin: prefer attaching the owner to an existing Head Admin role;
	// otherwise stamp a fresh one. Setting the whole roles array keeps the write
	// atomic and avoids positional-path pitfalls.
	headAdminIdx := -1
	for i := range c.Details.Roles {
		if IsHeadAdminRole(c.Details.Roles[i]) {
			headAdminIdx = i
			break
		}
	}
	newRoles := append([]Role{}, c.Details.Roles...)
	if headAdminIdx >= 0 {
		newRoles[headAdminIdx].Members = append(append([]string{}, newRoles[headAdminIdx].Members...), owner)
		actions = append(actions, "add owner to Head Admin role")
	} else {
		newRoles = append(newRoles, BuildHeadAdminRole(owner))
		actions = append(actions, "add Head Admin role")
	}
	set["community.roles"] = newRoles

	// Seed the reference data every current community ships with at creation.
	// Guards match the create-time defaults: seed only when genuinely empty.
	if len(c.Details.TenCodes) == 0 {
		set["community.tenCodes"] = DefaultTenCodes()
		actions = append(actions, "seed tenCodes")
	}
	if c.Details.Fines.Currency == "" && len(c.Details.Fines.Categories) == 0 {
		set["community.fines"] = DefaultCommunityFines()
		actions = append(actions, "seed fines")
	}
	if len(c.Details.PenalCodes.Categories) == 0 {
		set["community.penalCodes"] = DefaultCommunityPenalCodes()
		actions = append(actions, "seed penalCodes")
	}
	if c.Details.InviteCodeIds == nil {
		set["community.inviteCodeIds"] = []string{}
		actions = append(actions, "init inviteCodeIds")
	}
	if c.Details.BanList == nil {
		set["community.banList"] = []string{}
		actions = append(actions, "init banList")
	}
	if c.Details.Departments == nil {
		set["community.departments"] = []Department{}
		actions = append(actions, "init departments")
	}
	if c.Details.Events == nil {
		set["community.events"] = []Event{}
		actions = append(actions, "init events")
	}
	if c.Details.Visibility == "" {
		set["community.visibility"] = "public"
		actions = append(actions, "default visibility=public")
	}

	// Members map + denormalized count: ensure the owner is a member, then make
	// membersCount agree with the map.
	_, hasOwnerMember := c.Details.Members[owner]
	if c.Details.Members == nil {
		set["community.members"] = map[string]MemberDetail{owner: {}}
		actions = append(actions, "init members{owner}")
	} else if !hasOwnerMember {
		set["community.members."+owner] = MemberDetail{}
		actions = append(actions, "add owner to members")
	}
	expected := len(c.Details.Members)
	if !hasOwnerMember {
		expected++
	}
	if expected == 0 {
		expected = 1
	}
	if c.Details.MembersCount != expected {
		set["community.membersCount"] = expected
		actions = append(actions, "fix membersCount")
	}

	if len(actions) > 0 {
		set["community.updatedAt"] = primitive.NewDateTimeFromTime(time.Now())
	}

	return set, actions
}
