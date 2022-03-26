package models

// Firearm holds the structure for the firearm collection in mongo
type Firearm struct {
	ID      string         `json:"_id" bson:"_id"`
	Details FirearmDetails `json:"firearm" bson:"firearm"`
	Version int32          `json:"__v" bson:"__v"`
}

// FirearmDetails holds the structure for the inner user structure as
// defined in the firearm collection in mongo
type FirearmDetails struct {
	SerialNumber      string      `json:"serialNumber" bson:"serialNumber"`
	WeaponType        string      `json:"weaponType" bson:"weaponType"`
	RegisteredOwner   string      `json:"registeredOwner" bson:"registeredOwner"`
	RegisteredOwnerID string      `json:"registeredOwnerID" bson:"registeredOwnerID"`
	IsStolen          string      `json:"isStolen" bson:"isStolen"`
	ActiveCommunityID string      `json:"activeCommunityID" bson:"activeCommunityID"`
	UserID            string      `json:"userID" bson:"userID"`
	CreatedAt         interface{} `json:"createdAt" bson:"createdAt"`
	UpdatedAt         interface{} `json:"updatedAt" bson:"updatedAt"`
}
