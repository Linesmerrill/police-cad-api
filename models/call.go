package models

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

// CallNotes holds the structure for the notes associated with a call
type CallNotes struct {
	ID        string      `json:"_id" bson:"_id"`
	Note      string      `json:"note" bson:"note"`
	CreatedBy string      `json:"createdBy" bson:"createdBy"`
	CreatedAt interface{} `json:"createdAt" bson:"createdAt"`
	UpdatedBy string      `json:"updatedBy" bson:"updatedBy"`
	UpdatedAt interface{} `json:"updatedAt" bson:"updatedAt"`
}
