package models

// Vehicle holds the structure for the vehicle collection in mongo
type Vehicle struct {
	ID      string         `json:"_id" bson:"_id"`
	Details VehicleDetails `json:"vehicle" bson:"vehicle"`
	Version int32          `json:"__v" bson:"__v"`
}

// VehicleDetails holds the structure for the inner user structure as
// defined in the vehicle collection in mongo
type VehicleDetails struct {
	Email             string      `json:"email" bson:"email"`
	Plate             string      `json:"plate" bson:"plate"`
	Vin               string      `json:"vin" bson:"vin"`
	Model             string      `json:"model" bson:"model"`
	Color             string      `json:"color" bson:"color"`
	ValidRegistration string      `json:"validRegistration" bson:"validRegistration"`
	ValidInsurance    string      `json:"validInsurance" bson:"validInsurance"`
	RegisteredOwner   string      `json:"registeredOwner" bson:"registeredOwner"`
	RegisteredOwnerID string      `json:"registeredOwnerID" bson:"registeredOwnerID"`
	IsStolen          string      `json:"isStolen" bson:"isStolen"`
	ActiveCommunityID string      `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID            string      `json:"userID" bson:"userID"`
	CreatedAt         interface{} `json:"createdAt" bson:"createdAt"`
	UpdatedAt         interface{} `json:"updatedAt" bson:"updatedAt"`
}
