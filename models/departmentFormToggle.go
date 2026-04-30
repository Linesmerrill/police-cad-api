package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// DepartmentFormToggle records an explicit enable/disable decision for a
// form template at the department level.
//
// Default behavior when no row exists: every department implicitly has
// every available template (community + defaults) enabled. Rows are only
// stored when an admin overrides the implicit default — typically to
// disable a template for a particular department.
type DepartmentFormToggle struct {
	ID      primitive.ObjectID          `json:"_id" bson:"_id"`
	Details DepartmentFormToggleDetails `json:"departmentFormToggle" bson:"departmentFormToggle"`
	Version int32                       `json:"__v" bson:"__v"`
}

// DepartmentFormToggleDetails holds the inner toggle state.
type DepartmentFormToggleDetails struct {
	CommunityID      string             `json:"communityID" bson:"communityID"`
	DepartmentID     string             `json:"departmentId" bson:"departmentId"`
	FormTemplateSlug string             `json:"formTemplateSlug" bson:"formTemplateSlug"`
	IsEnabled        bool               `json:"isEnabled" bson:"isEnabled"`
	UpdatedAt        primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
	UpdatedBy        string             `json:"updatedBy" bson:"updatedBy"`
}
