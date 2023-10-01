package models

// Warrant holds the structure for the warrant collection in mongo
type Warrant struct {
	ID      string         `json:"_id" bson:"_id"`
	Details WarrantDetails `json:"warrant" bson:"warrant"`
	Version int32          `json:"__v" bson:"__v"`
}

// WarrantDetails holds the structure for the inner user structure as
// defined in the warrant collection in mongo
type WarrantDetails struct {
	Status             bool        `json:"status" bson:"status"`
	AccusedID          string      `json:"accusedID" bson:"accusedID"`
	AccusedFirstName   string      `json:"accusedFirstName" bson:"accusedFirstName"`
	AccusedLastName    string      `json:"accusedLastName" bson:"accusedLastName"`
	Reasons            []string    `json:"reasons" bson:"reasons"`
	ReportingOfficerID string      `json:"reportingOfficerID" bson:"reportingOfficerID"`
	ClearingOfficerID  string      `json:"clearingOfficerID" bson:"clearingOfficerID"`
	CreatedAt          interface{} `json:"createdAt" bson:"createdAt"`
	UpdatedAt          interface{} `json:"updatedAt" bson:"updatedAt"`
}
