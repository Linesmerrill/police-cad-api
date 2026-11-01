package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// Firearm holds the structure for the firearm collection in mongo
type Firearm struct {
	ID      primitive.ObjectID `json:"_id" bson:"_id"`
	Details FirearmDetails     `json:"firearm" bson:"firearm"`
	Version int32              `json:"__v" bson:"__v"`
}

// FirearmDetails holds the structure for the inner user structure as
// defined in the firearm collection in mongo
type FirearmDetails struct {
	SerialNumber      string             `json:"serialNumber" bson:"serialNumber"`
	Name              string             `json:"name" bson:"name"`
	WeaponType        string             `json:"weaponType" bson:"weaponType"`
	Caliber           string             `json:"caliber" bson:"caliber"`
	LinkedCivilianID  string             `json:"linkedCivilianID" bson:"linkedCivilianID"`
	RegisteredOwner   string             `json:"registeredOwner" bson:"registeredOwner"`     // Deprecated, use linkedCivilianID
	RegisteredOwnerID string             `json:"registeredOwnerID" bson:"registeredOwnerID"` // Deprecated, use linkedCivilianID
	IsStolen          string             `json:"isStolen" bson:"isStolen"`
	Image             string             `json:"image" bson:"image"`
	ActiveCommunityID string             `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID            string             `json:"userID" bson:"userID"`
	CreatedAt         primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt         primitive.DateTime `json:"updatedAt" bson:"updatedAt"`

	// Source marks a record a Developer API key created on a player's behalf,
	// so a misbehaving key's entire output can be found and removed together.
	// Absent on every record a person made themselves.
	Source *RecordSource `json:"source,omitempty" bson:"source,omitempty"`
}
