package helpers

import "github.com/linesmerrill/police-cad-api/models"

// IsCommunityAdmin reports whether the user is the community owner or holds a
// role carrying the enabled "administrator" permission. Admins bypass form
// rank gating entirely. Mirrors the ownership/administrator check used across
// the community handlers.
func IsCommunityAdmin(community *models.Community, userID string) bool {
	if community == nil || userID == "" {
		return false
	}
	if community.Details.OwnerID == userID {
		return true
	}
	for _, role := range community.Details.Roles {
		inRole := false
		for _, member := range role.Members {
			if member == userID {
				inRole = true
				break
			}
		}
		if !inRole {
			continue
		}
		for _, perm := range role.Permissions {
			if perm.Enabled && perm.Name == "administrator" {
				return true
			}
		}
	}
	return false
}

// UserIsDepartmentMember reports whether the user appears in the department's
// member list.
func UserIsDepartmentMember(community *models.Community, departmentID, userID string) bool {
	dept := FindDepartment(community, departmentID)
	if dept == nil || userID == "" {
		return false
	}
	for _, m := range dept.Members {
		if m.UserID == userID {
			return true
		}
	}
	return false
}

// UserCanSeeForm reports whether the user may see a form with the given
// per-department rank gates.
//
// Semantics: an empty gate list means no restriction (visible to everyone
// who passes the role gate). A non-empty list acts as an allow-list — the
// user must be a member of at least one gated department and, where that
// department carries a visible rule, satisfy it. A gate with an empty
// visible rule grants access to every member of that department. Community
// admins (owner / "administrator") always pass.
func UserCanSeeForm(community *models.Community, userID string, gates []models.DepartmentRankGate) bool {
	if len(gates) == 0 {
		return true
	}
	if IsCommunityAdmin(community, userID) {
		return true
	}
	for _, gate := range gates {
		if !UserIsDepartmentMember(community, gate.DepartmentID, userID) {
			continue
		}
		if gate.VisibleToRankRule.IsEmpty() {
			return true
		}
		if RankRuleMatches(community, gate.DepartmentID, userID, gate.VisibleToRankRule.Mode, gate.VisibleToRankRule.AnchorRankIDs) {
			return true
		}
	}
	return false
}

// UserCanEditForm reports whether the user may edit/submit a form with the
// given per-department rank gates. Same allow-list model as UserCanSeeForm
// but evaluated against each gate's editable rule. A gate with an empty
// editable rule lets any member of that department edit. Community admins
// always pass; an empty gate list means no restriction.
func UserCanEditForm(community *models.Community, userID string, gates []models.DepartmentRankGate) bool {
	if len(gates) == 0 {
		return true
	}
	if IsCommunityAdmin(community, userID) {
		return true
	}
	for _, gate := range gates {
		if !UserIsDepartmentMember(community, gate.DepartmentID, userID) {
			continue
		}
		if gate.EditableByRankRule.IsEmpty() {
			return true
		}
		if RankRuleMatches(community, gate.DepartmentID, userID, gate.EditableByRankRule.Mode, gate.EditableByRankRule.AnchorRankIDs) {
			return true
		}
	}
	return false
}
