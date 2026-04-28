package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// FormTemplateVersion is an immutable snapshot of a template's section/field
// schema. A new row is appended every time a template is saved.
type FormTemplateVersion struct {
	ID      primitive.ObjectID         `json:"_id" bson:"_id"`
	Details FormTemplateVersionDetails `json:"formTemplateVersion" bson:"formTemplateVersion"`
	Version int32                      `json:"__v" bson:"__v"`
}

// FormTemplateVersionDetails holds the inner snapshot.
type FormTemplateVersionDetails struct {
	FormTemplateID string `json:"formTemplateID" bson:"formTemplateID"` // ObjectID hex; empty for default-template snapshots that aren't stored
	CommunityID    string `json:"communityID" bson:"communityID"`
	Slug           string `json:"slug" bson:"slug"`
	Version        int32  `json:"version" bson:"version"`

	Sections []FormSection `json:"sections" bson:"sections"`

	PublishedAt primitive.DateTime `json:"publishedAt" bson:"publishedAt"`
	PublishedBy string             `json:"publishedBy" bson:"publishedBy"`
}

// FormSection is one section in a template (e.g. "Incident Info"). When
// Repeatable is true, the section produces an array of row objects in the
// submission data instead of a single object.
//
// BindEntity, when set on a repeatable section, anchors each row to a real
// entity (civilian | vehicle | firearm). The renderer adds a per-row "Link
// <entity>" control that fetches the entity and auto-fills any field whose
// PopulateFrom mapping has Source="bound". The picked entity ID is also
// added to the submission's Links array so future "all reports involving X"
// queries can use a single index lookup.
type FormSection struct {
	ID         string      `json:"id" bson:"id"`
	Title      string      `json:"title" bson:"title"`
	Repeatable bool        `json:"repeatable" bson:"repeatable"`
	BindEntity string      `json:"bindEntity,omitempty" bson:"bindEntity,omitempty"` // "" | civilian | vehicle | firearm
	MinRows    int         `json:"minRows,omitempty" bson:"minRows,omitempty"`
	MaxRows    int         `json:"maxRows,omitempty" bson:"maxRows,omitempty"`
	Fields     []FormField `json:"fields" bson:"fields"`
}

// FormField is a single input within a section.
//
// Type drives the renderer on web/mobile. PopulateFrom drives auto-fill
// when the submission is created with sources= context (e.g. "from this
// call, pull callType into this field").
type FormField struct {
	ID          string   `json:"id" bson:"id"`
	Type        string   `json:"type" bson:"type"` // text, textarea, number, date, time, datetime, select, multiSelect, checkbox, radio, penalCodePicker, userPicker, departmentPicker
	Label       string   `json:"label" bson:"label"`
	Placeholder string   `json:"placeholder,omitempty" bson:"placeholder,omitempty"`
	HelpText    string   `json:"helpText,omitempty" bson:"helpText,omitempty"`
	Required    bool     `json:"required,omitempty" bson:"required,omitempty"`
	Options     []string `json:"options,omitempty" bson:"options,omitempty"` // for select/radio/multiSelect
	DefaultExpr string   `json:"defaultExpr,omitempty" bson:"defaultExpr,omitempty"` // "now", "today", "auth.username", "auth.badgeNumber", "auth.departmentName"

	PopulateFrom []FormFieldPopulate `json:"populateFrom,omitempty" bson:"populateFrom,omitempty"`
}

// FormFieldPopulate maps a field to a path on a source entity. First match
// wins when multiple sources are provided.
//
// Source="bound" is special: it resolves against the entity linked to the
// field's row (only valid inside a repeatable section whose BindEntity is
// set). The renderer applies bound paths client-side after the user picks
// an entity for that row, and never sends "bound" sources to the
// server-side prefill resolver.
type FormFieldPopulate struct {
	Source string `json:"source" bson:"source"` // call, citation, arrestReport, warrant, bolo, civilian, vehicle, firearm, bound
	Path   string `json:"path" bson:"path"`     // dotted JSON path on the source entity (or special tokens like "criminalHistory.fines[].fineType")
}
