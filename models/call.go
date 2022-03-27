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
	ShortDescription        string        `json:"shortDescription" bson:"shortDescription"`
	Classifier              []interface{} `json:"classifier" bson:"classifier"`
	AssignedOfficers        []interface{} `json:"assignedOfficers" bson:"assignedOfficers"`
	AssignedFireEms         []interface{} `json:"assignedFireEms" bson:"assignedFireEms"`
	CallNotes               []interface{} `json:"callNotes" bson:"callNotes"`
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
