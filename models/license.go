package models

// License holds the structure for the license collection in mongo
type License struct {
	ID      string         `json:"_id" bson:"_id"`
	Details LicenseDetails `json:"license" bson:"license"`
	Version int32          `json:"__v" bson:"__v"`
}

// LicenseDetails holds the structure for the inner user structure as
// defined in the license collection in mongo
type LicenseDetails struct {
	LicenseType       string      `json:"licenseType" bson:"licenseType"`
	Status            string      `json:"status" bson:"status"`
	ExpirationDate    string      `json:"expirationDate" bson:"expirationDate"`
	AdditionalNotes   string      `json:"additionalNotes" bson:"additionalNotes"`
	OwnerID           string      `json:"ownerID" bson:"ownerID"`
	OwnerName         string      `json:"ownerName" bson:"ownerName"`
	ActiveCommunityID string      `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID            string      `json:"userID" bson:"userID"`
	CreatedAt         interface{} `json:"createdAt" bson:"createdAt"`
	UpdatedAt         interface{} `json:"updatedAt" bson:"updatedAt"`
}
