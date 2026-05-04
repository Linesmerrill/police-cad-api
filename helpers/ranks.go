package helpers

import "github.com/linesmerrill/police-cad-api/models"

// RankRuleMode is the comparison mode for a rank-based gating rule.
const (
	RankRuleModeExact      = "exact"
	RankRuleModeAtOrAbove  = "atOrAbove"
	RankRuleModeAtOrBelow  = "atOrBelow"
)

// FindDepartment returns a pointer to the department with the given ID
// in the community, or nil when not found. Empty departmentID returns nil.
func FindDepartment(community *models.Community, departmentID string) *models.Department {
	if community == nil || departmentID == "" {
		return nil
	}
	for i := range community.Details.Departments {
		if community.Details.Departments[i].ID.Hex() == departmentID {
			return &community.Details.Departments[i]
		}
	}
	return nil
}

// FindRank returns a pointer to the rank with the given ID inside the
// department, or nil when not found.
func FindRank(dept *models.Department, rankID string) *models.Rank {
	if dept == nil || rankID == "" {
		return nil
	}
	for i := range dept.Ranks {
		if dept.Ranks[i].ID.Hex() == rankID {
			return &dept.Ranks[i]
		}
	}
	return nil
}

// UserRankInDepartment returns the assigned Rank for a member in the
// department, or nil when the user is not a member or has no rank assigned.
func UserRankInDepartment(community *models.Community, departmentID, userID string) *models.Rank {
	dept := FindDepartment(community, departmentID)
	if dept == nil || userID == "" {
		return nil
	}
	for _, m := range dept.Members {
		if m.UserID == userID {
			if m.RankID == "" {
				return nil
			}
			return FindRank(dept, m.RankID)
		}
	}
	return nil
}

// ResolveRankRule returns the set of rank IDs that satisfy the rule against
// the department's ranks. Lower DisplayOrder = higher rank.
//
//   - exact:      returns the anchor IDs as-is (filtered to those that exist)
//   - atOrAbove:  returns every rank with displayOrder <= min(anchor.displayOrder)
//   - atOrBelow:  returns every rank with displayOrder >= max(anchor.displayOrder)
//
// When the rule is invalid (unknown mode, no anchors, no department) the
// result is an empty slice — meaning "nobody satisfies" — so callers can
// safely union with role-based checks without leaking access.
func ResolveRankRule(community *models.Community, departmentID string, mode string, anchorRankIDs []string) []string {
	dept := FindDepartment(community, departmentID)
	if dept == nil || len(anchorRankIDs) == 0 {
		return nil
	}

	anchors := make([]models.Rank, 0, len(anchorRankIDs))
	for _, id := range anchorRankIDs {
		if r := FindRank(dept, id); r != nil {
			anchors = append(anchors, *r)
		}
	}
	if len(anchors) == 0 {
		return nil
	}

	switch mode {
	case RankRuleModeExact:
		out := make([]string, 0, len(anchors))
		for _, r := range anchors {
			out = append(out, r.ID.Hex())
		}
		return out

	case RankRuleModeAtOrAbove:
		minOrder := anchors[0].DisplayOrder
		for _, r := range anchors[1:] {
			if r.DisplayOrder < minOrder {
				minOrder = r.DisplayOrder
			}
		}
		out := make([]string, 0, len(dept.Ranks))
		for _, r := range dept.Ranks {
			if r.DisplayOrder <= minOrder {
				out = append(out, r.ID.Hex())
			}
		}
		return out

	case RankRuleModeAtOrBelow:
		maxOrder := anchors[0].DisplayOrder
		for _, r := range anchors[1:] {
			if r.DisplayOrder > maxOrder {
				maxOrder = r.DisplayOrder
			}
		}
		out := make([]string, 0, len(dept.Ranks))
		for _, r := range dept.Ranks {
			if r.DisplayOrder >= maxOrder {
				out = append(out, r.ID.Hex())
			}
		}
		return out
	}
	return nil
}

// ResolveRankRuleNames is like ResolveRankRule but returns rank Names
// (useful for the editor's live preview pill row).
func ResolveRankRuleNames(community *models.Community, departmentID string, mode string, anchorRankIDs []string) []string {
	ids := ResolveRankRule(community, departmentID, mode, anchorRankIDs)
	if len(ids) == 0 {
		return nil
	}
	dept := FindDepartment(community, departmentID)
	if dept == nil {
		return nil
	}
	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}
	out := make([]string, 0, len(ids))
	for _, r := range dept.Ranks {
		if _, ok := idSet[r.ID.Hex()]; ok {
			out = append(out, r.Name)
		}
	}
	return out
}

// RankRuleMatches reports whether the user's rank in the department
// satisfies the given rule. A nil/empty rule is treated as "no rank gate"
// and always matches (true).
func RankRuleMatches(community *models.Community, departmentID, userID string, mode string, anchorRankIDs []string) bool {
	if mode == "" || len(anchorRankIDs) == 0 {
		return true
	}
	resolved := ResolveRankRule(community, departmentID, mode, anchorRankIDs)
	if len(resolved) == 0 {
		return false
	}
	rank := UserRankInDepartment(community, departmentID, userID)
	if rank == nil {
		return false
	}
	target := rank.ID.Hex()
	for _, id := range resolved {
		if id == target {
			return true
		}
	}
	return false
}
