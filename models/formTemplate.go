package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// FormTemplate is the top-level metadata for a community-scoped form
// definition. The actual section/field schema lives in FormTemplateVersion
// rows; FormTemplate.CurrentVersion points at the latest.
//
// Defaults (e.g. the built-in Incident Report) are NOT stored as rows in
// this collection — they are virtual, defined in code under
// api/handlers/formdefaults. A community can hide a default by inserting a
// row with IsHidden=true and DefaultSlug=<slug>.
type FormTemplate struct {
	ID      primitive.ObjectID  `json:"_id" bson:"_id"`
	Details FormTemplateDetails `json:"formTemplate" bson:"formTemplate"`
	Version int32               `json:"__v" bson:"__v"`
}

// FormTemplateDetails holds the inner template metadata.
type FormTemplateDetails struct {
	CommunityID string `json:"communityID" bson:"communityID"`

	Name        string `json:"name" bson:"name"`
	Slug        string `json:"slug" bson:"slug"` // unique within a community
	Description string `json:"description" bson:"description"`
	Icon        string `json:"icon" bson:"icon"`

	CurrentVersion int32 `json:"currentVersion" bson:"currentVersion"`

	// Default-template hide override. When IsHidden=true and DefaultSlug
	// is set, this row exists solely to suppress the named default for
	// this community.
	IsHidden    bool   `json:"isHidden" bson:"isHidden"`
	DefaultSlug string `json:"defaultSlug,omitempty" bson:"defaultSlug,omitempty"`
	IsArchived  bool   `json:"isArchived" bson:"isArchived"`

	NumberFormat    string   `json:"numberFormat" bson:"numberFormat"` // tokens: {YYYY}, {NNNNNN}, custom prefix
	VisibleToRoles  []string `json:"visibleToRoles" bson:"visibleToRoles"`
	EditableByRoles []string `json:"editableByRoles" bson:"editableByRoles"`

	// RankGates holds per-department rank-based gating, ANDed with the
	// role-based gating above. Each gate scopes a visible/editable rank
	// rule to one department's ranks. An empty slice means "no rank gate"
	// (open to everyone who passes the role gate). A non-empty slice acts
	// as an allow-list: only members of a listed department whose rank
	// satisfies that department's rule may see/edit the form.
	RankGates []DepartmentRankGate `json:"rankGates,omitempty" bson:"rankGates,omitempty"`

	// Legacy single-department rank gate. Superseded by RankGates and no
	// longer written by the API, but retained so pre-migration rows still
	// decode. Readers normalize these into RankGates via
	// FormTemplateDetails.EffectiveRankGates.
	DepartmentID       string    `json:"departmentId,omitempty" bson:"departmentId,omitempty"`
	VisibleToRankRule  *RankRule `json:"visibleToRankRule,omitempty" bson:"visibleToRankRule,omitempty"`
	EditableByRankRule *RankRule `json:"editableByRankRule,omitempty" bson:"editableByRankRule,omitempty"`

	LinkableEntities []string `json:"linkableEntities" bson:"linkableEntities"` // civilian, vehicle, firearm, call, citation, arrestReport, warrant, bolo

	CreatedAt primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
	CreatedBy string             `json:"createdBy" bson:"createdBy"`
}

// RankRule expresses a rank-based gating rule used by FormTemplate's
// visibleToRankRule / editableByRankRule. Mode is one of:
//
//   - "exact":     match only the listed anchor ranks
//   - "atOrAbove": match ranks at or above the highest anchor (lowest displayOrder)
//   - "atOrBelow": match ranks at or below the lowest anchor (highest displayOrder)
//
// AnchorRankIDs are rank ObjectID hex strings inside the form's department.
// A nil/empty rule (or an unknown mode) is treated as "no rank gate".
type RankRule struct {
	Mode          string   `json:"mode" bson:"mode"`
	AnchorRankIDs []string `json:"anchorRankIDs" bson:"anchorRankIDs"`
}

// IsEmpty reports whether the rule imposes no gate (nil, blank mode, or no
// anchor ranks) and should be treated as "no rank restriction".
func (r *RankRule) IsEmpty() bool {
	return r == nil || r.Mode == "" || len(r.AnchorRankIDs) == 0
}

// DepartmentRankGate scopes visible/editable rank rules to a single
// department. A form template carries a list of these so a single form can
// be rank-gated differently across the multiple departments it applies to.
// AnchorRankIDs inside each rule reference ranks within DepartmentID.
type DepartmentRankGate struct {
	DepartmentID       string    `json:"departmentId" bson:"departmentId"`
	VisibleToRankRule  *RankRule `json:"visibleToRankRule,omitempty" bson:"visibleToRankRule,omitempty"`
	EditableByRankRule *RankRule `json:"editableByRankRule,omitempty" bson:"editableByRankRule,omitempty"`
}

// EffectiveRankGates returns the template's per-department rank gates,
// normalizing legacy single-department rows on the fly. When RankGates is
// populated it is returned as-is; otherwise, if the legacy DepartmentID +
// rule fields are set, a single equivalent gate is synthesized so callers
// only ever reason about the per-department shape. Returns nil when the
// template has no rank gating at all.
func (d FormTemplateDetails) EffectiveRankGates() []DepartmentRankGate {
	if len(d.RankGates) > 0 {
		return d.RankGates
	}
	if d.DepartmentID != "" && (!d.VisibleToRankRule.IsEmpty() || !d.EditableByRankRule.IsEmpty()) {
		return []DepartmentRankGate{{
			DepartmentID:       d.DepartmentID,
			VisibleToRankRule:  d.VisibleToRankRule,
			EditableByRankRule: d.EditableByRankRule,
		}}
	}
	return nil
}

// FormTemplateView is the merged representation returned to clients —
// either a stored FormTemplateDetails row or a virtual default. The
// IsDefault flag tells the UI whether this row is built-in (and therefore
// can only be hidden, not edited or deleted).
type FormTemplateView struct {
	ID              string               `json:"_id"`
	CommunityID     string               `json:"communityID"`
	Name            string               `json:"name"`
	Slug            string               `json:"slug"`
	Description     string               `json:"description"`
	Icon            string               `json:"icon"`
	CurrentVersion  int32                `json:"currentVersion"`
	NumberFormat    string               `json:"numberFormat"`
	VisibleToRoles  []string             `json:"visibleToRoles"`
	EditableByRoles []string             `json:"editableByRoles"`
	RankGates       []DepartmentRankGate `json:"rankGates,omitempty"`
	// CanEdit is computed per requesting user when a userId is supplied to
	// the list/get endpoints. Nil when not evaluated (e.g. the admin
	// builder fetch), in which case clients should not gate editing on it.
	CanEdit *bool `json:"canEdit,omitempty"`
	// Legacy single-department rank fields, mirrored from RankGates[0] for
	// backward compatibility with older clients during rollout. Prefer
	// RankGates.
	DepartmentID       string             `json:"departmentId,omitempty"`
	VisibleToRankRule  *RankRule          `json:"visibleToRankRule,omitempty"`
	EditableByRankRule *RankRule          `json:"editableByRankRule,omitempty"`
	LinkableEntities   []string           `json:"linkableEntities"`
	IsDefault          bool               `json:"isDefault"`
	IsHidden           bool               `json:"isHidden"`
	IsArchived         bool               `json:"isArchived"`
	Sections           []FormSection      `json:"sections"`
	CreatedAt          primitive.DateTime `json:"createdAt,omitempty"`
	UpdatedAt          primitive.DateTime `json:"updatedAt,omitempty"`
}
