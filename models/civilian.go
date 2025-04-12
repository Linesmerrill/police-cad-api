package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// Civilian holds the structure for the civilian collection in mongo
type Civilian struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Details CivilianDetails    `json:"civilian" bson:"civilian"`
	Version int32              `json:"__v" bson:"__v"`
}

// CivilianDetails holds the structure for the inner user structure as
// defined in the civilian collection in mongo
type CivilianDetails struct {
	Email                string             `json:"email" bson:"email"` // Deprecated, use the userID field
	FirstName            string             `json:"firstName" bson:"firstName"`
	LastName             string             `json:"lastName" bson:"lastName"`
	Name                 string             `json:"name" bson:"name"`
	LicenseStatus        string             `json:"licenseStatus" bson:"licenseStatus"`
	TicketCount          string             `json:"ticketCount" bson:"ticketCount"` // TODO may need to change the database definition
	Birthday             string             `json:"birthday" bson:"birthday"`
	Warrants             []interface{}      `json:"warrants" bson:"warrants"` // TODO replace with a concrete type
	CriminalHistory      []CriminalHistory  `json:"criminalHistory" bson:"criminalHistory"`
	Gender               string             `json:"gender" bson:"gender"`
	Address              string             `json:"address" bson:"address"`
	Race                 string             `json:"race" bson:"race"`
	HairColor            string             `json:"hairColor" bson:"hairColor"`
	Weight               string             `json:"weight" bson:"weight"` // TODO may need to change the database definition
	WeightClassification string             `json:"weightClassification" bson:"weightClassification"`
	Height               string             `json:"height" bson:"height"` // TODO may need to change the database definition
	HeightClassification string             `json:"heightClassification" bson:"heightClassification"`
	EyeColor             string             `json:"eyeColor" bson:"eyeColor"`
	OrganDonor           bool               `json:"organDonor" bson:"organDonor"`
	Veteran              bool               `json:"veteran" bson:"veteran"`
	Image                string             `json:"image" bson:"image"`
	Occupation           string             `json:"occupation" bson:"occupation"`
	FirearmLicense       string             `json:"firearmLicense" bson:"firearmLicense"`
	ActiveCommunityID    string             `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID               string             `json:"userID" bson:"userID"`
	CreatedAt            primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt            primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}

// CriminalHistory holds the structure for the criminal history
type CriminalHistory struct {
	ID         primitive.ObjectID `json:"_id" bson:"_id"`
	OfficerID  string             `json:"officerID" bson:"officerID"`
	Type       string             `json:"type" bson:"type"`
	FineType   string             `json:"fineType" bson:"fineType"`
	FineAmount string             `json:"fineAmount" bson:"fineAmount"`
	Redacted   bool               `json:"redacted" bson:"redacted"` // Rather than deleting records, we can mark them as redacted
	Notes      string             `json:"notes" bson:"notes"`
	CreatedAt  primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt  primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}
