package models

// Civilian holds the structure for the civilian collection in mongo
type Civilian struct {
	ID      string          `json:"_id" bson:"_id"`
	Details CivilianDetails `json:"civilian" bson:"civilian"`
	Version int32           `json:"__v" bson:"__v"`
}

// CivilianDetails holds the structure for the inner user structure as
// defined in the civilian collection in mongo
type CivilianDetails struct {
	Email                string        `json:"email" bson:"email"`
	FirstName            string        `json:"firstName" bson:"firstName"`
	LastName             string        `json:"lastName" bson:"lastName"`
	LicenseStatus        string        `json:"licenseStatus" bson:"licenseStatus"`
	TicketCount          string        `json:"ticketCount" bson:"ticketCount"` // TODO may need to change the database definition
	Birthday             string        `json:"birthday" bson:"birthday"`
	Warrants             []interface{} `json:"warrants" bson:"warrants"` // TODO replace with a concrete type
	Gender               string        `json:"Gender" bson:"gender"`
	Address              string        `json:"address" bson:"address"`
	Race                 string        `json:"race" bson:"race"`
	HairColor            string        `json:"hairColor" bson:"hairColor"`
	Weight               string        `json:"weight" bson:"weight"` // TODO may need to change the database definition
	WeightClassification string        `json:"weightClassification" bson:"weightClassification"`
	Height               string        `json:"height" bson:"height"` // TODO may need to change the database definition
	HeightClassification string        `json:"heightClassification" bson:"heightClassification"`
	EyeColor             string        `json:"eyeColor" bson:"eyeColor"`
	OrganDonor           bool          `json:"organDonor" bson:"organDonor"`
	Veteran              bool          `json:"veteran" bson:"veteran"`
	Image                string        `json:"image" bson:"image"`
	Occupation           string        `json:"occupation" bson:"occupation"`
	FirearmLicense       string        `json:"firearmLicense" bson:"firearmLicense"`
	ActiveCommunityID    string        `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID               string        `json:"userID" bson:"userID"`
	CreatedAt            interface{}   `json:"createdAt" bson:"createdAt"`
	UpdatedAt            interface{}   `json:"updatedAt" bson:"updatedAt"`
}
