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
	CommunityID  string `json:"communityID" bson:"communityID"`
	DepartmentID string `json:"departmentId,omitempty" bson:"departmentId,omitempty"` // optional scope; empty = community-wide

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

	NumberFormat      string   `json:"numberFormat" bson:"numberFormat"` // tokens: {YYYY}, {NNNNNN}, custom prefix
	VisibleToRoles    []string `json:"visibleToRoles" bson:"visibleToRoles"`
	EditableByRoles   []string `json:"editableByRoles" bson:"editableByRoles"`
	// Rank-based gating, ANDed with the role-based gating above. When a
	// rule is nil/empty, that scope has no rank gate.
	VisibleToRankRule  *RankRule `json:"visibleToRankRule,omitempty" bson:"visibleToRankRule,omitempty"`
	EditableByRankRule *RankRule `json:"editableByRankRule,omitempty" bson:"editableByRankRule,omitempty"`
	LinkableEntities  []string `json:"linkableEntities" bson:"linkableEntities"` // civilian, vehicle, firearm, call, citation, arrestReport, warrant, bolo

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

// FormTemplateView is the merged representation returned to clients —
// either a stored FormTemplateDetails row or a virtual default. The
// IsDefault flag tells the UI whether this row is built-in (and therefore
// can only be hidden, not edited or deleted).
type FormTemplateView struct {
	ID               string             `json:"_id"`
	CommunityID      string             `json:"communityID"`
	DepartmentID     string             `json:"departmentId,omitempty"`
	Name             string             `json:"name"`
	Slug             string             `json:"slug"`
	Description      string             `json:"description"`
	Icon             string             `json:"icon"`
	CurrentVersion   int32              `json:"currentVersion"`
	NumberFormat       string             `json:"numberFormat"`
	VisibleToRoles     []string           `json:"visibleToRoles"`
	EditableByRoles    []string           `json:"editableByRoles"`
	VisibleToRankRule  *RankRule          `json:"visibleToRankRule,omitempty"`
	EditableByRankRule *RankRule          `json:"editableByRankRule,omitempty"`
	LinkableEntities   []string           `json:"linkableEntities"`
	IsDefault        bool               `json:"isDefault"`
	IsHidden         bool               `json:"isHidden"`
	IsArchived       bool               `json:"isArchived"`
	Sections         []FormSection      `json:"sections"`
	CreatedAt        primitive.DateTime `json:"createdAt,omitempty"`
	UpdatedAt        primitive.DateTime `json:"updatedAt,omitempty"`
}
