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
