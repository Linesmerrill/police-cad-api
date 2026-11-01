package models

import (
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
)

// Call holds the structure for the call collection in mongo
type Call struct {
	ID      string      `json:"_id" bson:"_id"`
	Details CallDetails `json:"call" bson:"call"`
	Version int32       `json:"__v" bson:"__v"`
}

// CallDetails holds the structure for the inner user structure as
// defined in the call collection in mongo
type CallDetails struct {
	Title                   string        `json:"title" bson:"title"`
	Details                 string        `json:"details" bson:"details"`
	ShortDescription        string        `json:"shortDescription" bson:"shortDescription"` // Deprecated, use Details
	Classifier              []interface{} `json:"classifier" bson:"classifier"`
	Departments             []string      `json:"departments" bson:"departments"`
	AssignedOfficers        []interface{} `json:"assignedOfficers" bson:"assignedOfficers"` // Deprecated, use AssignedTo
	AssignedFireEms         []interface{} `json:"assignedFireEms" bson:"assignedFireEms"`   // Deprecated, use AssignedTo
	AssignedTo              []string      `json:"assignedTo" bson:"assignedTo"`
	CallNotes               []CallNotes   `json:"callNotes" bson:"callNotes"`
	CommunityID             string        `json:"communityID" bson:"communityID"`
	CreatedByUsername       string        `json:"createdByUsername" bson:"createdByUsername"`
	CreatedByID             string        `json:"createdByID" bson:"createdByID"`
	ClearingOfficerUsername string        `json:"clearingOfficerUsername" bson:"clearingOfficerUsername"`
	ClearingOfficerID       string        `json:"clearingOfficerID" bson:"clearingOfficerID"`
	Status                  bool          `json:"status" bson:"status"`
	CreatedAt               interface{}   `json:"createdAt" bson:"createdAt"`
	CreatedAtReadable       string        `json:"createdAtReadable" bson:"createdAtReadable"`
	UpdatedAt               interface{}   `json:"updatedAt" bson:"updatedAt"`

	// Dispatch fields, added for the Developer API so a game server can keep
	// its own incident state in step with ours. All omitempty: a call made on
	// the website carries none of them and must not grow empty keys.
	//
	// Location finally has a home here. It used to be folded into the
	// description text because the model had nowhere to put it, which worked
	// but meant nothing could filter or display it as a location.
	Location string `json:"location,omitempty" bson:"location,omitempty"`
	// Type and Priority are free strings rather than enums. Communities run
	// wildly different call taxonomies, and an enum we invent would be wrong
	// for most of them within a week.
	Type     string `json:"type,omitempty" bson:"type,omitempty"`
	Priority string `json:"priority,omitempty" bson:"priority,omitempty"`
	// Disposition is how the call ended, recorded when it closes.
	Disposition string `json:"disposition,omitempty" bson:"disposition,omitempty"`

	// Source identifies the integration that owns this call, and ExternalID is
	// that integration's own id for it.
	//
	// Together they are a unique key, which is what makes a create idempotent:
	// a game server that reconnects and replays its queue gets the same call
	// back rather than a duplicate. This matters more than it looks — the whole
	// point of an external id is that the caller already lost track of ours.
	Source     string `json:"source,omitempty" bson:"source,omitempty"`
	ExternalID string `json:"externalId,omitempty" bson:"externalId,omitempty"`
	// CreatedByKeyID is the developer key that raised it, for the per-key
	// audit trail and for cleaning up after a misbehaving integration.
	CreatedByKeyID string `json:"createdByKeyId,omitempty" bson:"createdByKeyId,omitempty"`
}

// CallNotes holds the structure for the notes associated with a call.
// Legacy documents may store callNotes as plain strings instead of objects.
// The custom UnmarshalBSONValue handles both formats.
type CallNotes struct {
	ID        string      `json:"_id" bson:"_id"`
	Note      string      `json:"note" bson:"note"`
	CreatedBy string      `json:"createdBy" bson:"createdBy"`
	CreatedAt interface{} `json:"createdAt" bson:"createdAt"`
	UpdatedBy string      `json:"updatedBy" bson:"updatedBy"`
	UpdatedAt interface{} `json:"updatedAt" bson:"updatedAt"`
}

// UnmarshalBSONValue handles legacy callNotes that are plain strings
// by converting them into a CallNotes struct with the string as the Note field.
func (cn *CallNotes) UnmarshalBSONValue(t bsontype.Type, data []byte) error {
	rv := bson.RawValue{Type: t, Value: data}

	if t == bsontype.String {
		s, ok := rv.StringValueOK()
		if ok {
			cn.Note = s
			return nil
		}
	}

	// For normal object documents, decode into an alias to avoid infinite recursion
	type callNotesAlias CallNotes
	var alias callNotesAlias
	if err := rv.Unmarshal(&alias); err != nil {
		return err
	}
	*cn = CallNotes(alias)
	return nil
}
