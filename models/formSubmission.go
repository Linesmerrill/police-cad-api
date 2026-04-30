package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// FormSubmission is a filled instance of a FormTemplate. Data is stored as
// a free-form map keyed by field IDs from the snapshotted template version.
type FormSubmission struct {
	ID      primitive.ObjectID    `json:"_id" bson:"_id"`
	Details FormSubmissionDetails `json:"formSubmission" bson:"formSubmission"`
	Version int32                 `json:"__v" bson:"__v"`
}

// FormSubmissionDetails holds the inner submission record.
type FormSubmissionDetails struct {
	CommunityID  string `json:"communityID" bson:"communityID"`
	DepartmentID string `json:"departmentId" bson:"departmentId"`

	FormTemplateID      string `json:"formTemplateID,omitempty" bson:"formTemplateID,omitempty"` // empty for default-template submissions
	FormTemplateSlug    string `json:"formTemplateSlug" bson:"formTemplateSlug"`
	FormTemplateVersion int32  `json:"formTemplateVersion" bson:"formTemplateVersion"`

	ReportNumber string `json:"reportNumber" bson:"reportNumber"`

	// Data is keyed by field.id from the snapshotted template version.
	// Repeatable sections store an array of row objects under the section.id key.
	Data map[string]interface{} `json:"data" bson:"data"`

	Links []FormSubmissionLink `json:"links" bson:"links"`

	SignedBy FormSubmissionSignature      `json:"signedBy" bson:"signedBy"`
	Status   string                       `json:"status" bson:"status"` // "draft", "submitted"
	History  []FormSubmissionHistoryEntry `json:"history,omitempty" bson:"history,omitempty"`

	CreatedAt primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// FormSubmissionHistoryEntry is one append-only entry in the report's
// audit trail. Action is one of: "submitted", "reopened", "resubmitted".
type FormSubmissionHistoryEntry struct {
	Action   string             `json:"action" bson:"action"`
	UserID   string             `json:"userID" bson:"userID"`
	Username string             `json:"username" bson:"username"`
	At       primitive.DateTime `json:"at" bson:"at"`
}

// FormSubmissionLink is one cross-link to another entity. ChildID is set
// when the linked entity is embedded in a parent doc — e.g. citations live
// inside a civilian's criminalHistory array, so Type="citation" sets
// ID=<civilianID> and ChildID=<criminalHistoryID>.
type FormSubmissionLink struct {
	Type    string `json:"type" bson:"type"`
	ID      string `json:"id" bson:"id"`
	ChildID string `json:"childId,omitempty" bson:"childId,omitempty"`
	Label   string `json:"label,omitempty" bson:"label,omitempty"`
}

// FormSubmissionSignature is server-stamped from the auth context on submit.
type FormSubmissionSignature struct {
	UserID   string             `json:"userID" bson:"userID"`
	Username string             `json:"username" bson:"username"`
	SignedAt primitive.DateTime `json:"signedAt" bson:"signedAt"`
}
