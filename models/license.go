package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// License holds the structure for the license collection in mongo
type License struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Details LicenseDetails     `json:"license" bson:"license"`
	Version int32              `json:"__v" bson:"__v"`
}

// LicenseDetails holds the structure for the inner user structure as
// defined in the license collection in mongo
type LicenseDetails struct {
	Type           string      `json:"type" bson:"type"`
	Status         string      `json:"status" bson:"status"`
	ExpirationDate string      `json:"expirationDate" bson:"expirationDate"`
	Notes          string      `json:"notes" bson:"notes"`
	CivilianID     string      `json:"civilianID" bson:"civilianID"`
	CreatedAt      interface{} `json:"createdAt" bson:"createdAt"`
	UpdatedAt      interface{} `json:"updatedAt" bson:"updatedAt"`
}
