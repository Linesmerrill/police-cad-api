package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// License holds the structure for the license collection in mongo
type License struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Details LicenseDetails     `json:"license" bson:"license"`
	Version int32              `json:"__v" bson:"__v"`
}

// LicenseDetails holds the structure for the inner user structure as
// defined in the license collection in mongo.
//
// Two writers have populated this collection with two different key sets — see
// license_normalize.go. The legacy keys are declared here so they survive the
// decode; without them Go silently drops the only copy of the type, notes and
// owner for three quarters of the collection.
type LicenseDetails struct {
	Type           string      `json:"type" bson:"type"`
	Status         string      `json:"status" bson:"status"`
	ExpirationDate string      `json:"expirationDate" bson:"expirationDate"`
	Notes          string      `json:"notes" bson:"notes"`
	CivilianID     string      `json:"civilianID" bson:"civilianID"`
	CreatedAt      interface{} `json:"createdAt" bson:"createdAt"`
	UpdatedAt      interface{} `json:"updatedAt" bson:"updatedAt"`

	// Legacy keys, written by the website's Mongoose model
	// (police-cad/app/models/license.go). Type, Notes and CivilianID above are
	// the canonical homes for the first three; the rest have no canonical twin
	// and are passed through as-is.
	LicenseType       string `json:"licenseType,omitempty" bson:"licenseType,omitempty"`
	AdditionalNotes   string `json:"additionalNotes,omitempty" bson:"additionalNotes,omitempty"`
	OwnerID           string `json:"ownerID,omitempty" bson:"ownerID,omitempty"`
	OwnerName         string `json:"ownerName,omitempty" bson:"ownerName,omitempty"`
	ActiveCommunityID string `json:"activeCommunityID,omitempty" bson:"activeCommunityID,omitempty"`
	UserID            string `json:"userID,omitempty" bson:"userID,omitempty"`
}
