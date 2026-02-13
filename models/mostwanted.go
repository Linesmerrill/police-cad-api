package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// MostWantedEntry holds the structure for the most_wanted_entries collection in MongoDB
type MostWantedEntry struct {
	ID      primitive.ObjectID     `json:"_id" bson:"_id"`
	Details MostWantedEntryDetails `json:"mostWanted" bson:"mostWanted"`
	Version int32                  `json:"__v" bson:"__v"`
}

// MostWantedEntryDetails holds the structure for the inner most wanted entry details
type MostWantedEntryDetails struct {
	CommunityID      string                 `json:"communityID" bson:"communityID"`
	CivilianID       string                 `json:"civilianID" bson:"civilianID"`
	ListOrder        int                    `json:"listOrder" bson:"listOrder"`
	Stars            int                    `json:"stars" bson:"stars"`
	Charges          []string               `json:"charges" bson:"charges"`
	Description      string                 `json:"description" bson:"description"`
	Status           string                 `json:"status" bson:"status"` // "active", "captured", "removed"
	AddedByUserID    string                 `json:"addedByUserID" bson:"addedByUserID"`
	CustomFields     map[string]string      `json:"customFields" bson:"customFields"`
	CivilianSnapshot map[string]interface{} `json:"civilianSnapshot" bson:"civilianSnapshot"`
	CreatedAt        primitive.DateTime     `json:"createdAt" bson:"createdAt"`
	UpdatedAt        primitive.DateTime     `json:"updatedAt" bson:"updatedAt"`
}
