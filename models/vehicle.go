package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// Vehicle holds the structure for the vehicle collection in mongo
type Vehicle struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Details VehicleDetails     `json:"vehicle" bson:"vehicle"`
	Version int32              `json:"__v" bson:"__v"`
}

// VehicleDetails holds the structure for the inner user structure as
// defined in the vehicle collection in mongo
type VehicleDetails struct {
	Email             string             `json:"email" bson:"email"`
	Plate             string             `json:"plate" bson:"plate"`
	LicensePlateState string             `json:"licensePlateState" bson:"licensePlateState"`
	Vin               string             `json:"vin" bson:"vin"`
	Type              string             `json:"type" bson:"type"`
	Make              string             `json:"make" bson:"make"`
	Model             string             `json:"model" bson:"model"`
	Year              string             `json:"year" bson:"year"`
	Image             string             `json:"image" bson:"image"`
	Color             string             `json:"color" bson:"color"`
	LinkedCivilianID  string             `json:"linkedCivilianID" bson:"linkedCivilianID"`
	ValidRegistration string             `json:"validRegistration" bson:"validRegistration"` // TODO change to boolean
	ValidInsurance    string             `json:"validInsurance" bson:"validInsurance"`       // TODO change to boolean
	RegisteredOwner   string             `json:"registeredOwner" bson:"registeredOwner"`     // Deprecated, use linkedCivilianID
	RegisteredOwnerID string             `json:"registeredOwnerID" bson:"registeredOwnerID"` // Deprecated, use linkedCivilianID
	IsStolen          string             `json:"isStolen" bson:"isStolen"`
	IsExempt          string             `json:"isExempt" bson:"isExempt"`
	ActiveCommunityID string             `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID            string             `json:"userID" bson:"userID"`
	CreatedAt         primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt         primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}
