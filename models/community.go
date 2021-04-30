package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Community holds the structure for the community collection in mongo
type Community struct {
	ID             string         `json:"_id" bson:"_id"`
	CommunityInner CommunityInner `json:"community" bson:"community"`
	Version        int32          `json:"__v" bson:"__v"`
}

type CommunityInner struct {
	Name            string                 `json:"name"`
	OwnerID         string                 `json:"ownerID"`
	Code            string                 `json:"code"`
	ActivePanics    map[string]interface{} `json:"activePanics"`
	ActiveSignal100 bool                   `json:"activeSignal100"`
	CreatedAt       primitive.DateTime     `json:"createdAt"`
	UpdatedAt       primitive.DateTime     `json:"updatedAt"`
}
