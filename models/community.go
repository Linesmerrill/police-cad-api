package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Community holds the structure for the community collection in mongo
type Community struct {
	ID             string         `bson:"_id"`
	CommunityInner CommunityInner `bson:"community"`
	Version        int32          `bson:"__v"`
}

type CommunityInner struct {
	Name            string `json:"name"`
	OwnerID         string `json:"ownerID"`
	Code            string `json:"code"`
	ActivePanics    map[string]interface{}
	ActiveSignal100 bool
	CreatedAt       primitive.DateTime `json:"createdAt"`
	UpdatedAt       time.Time
}

type CommunityResp struct {
	ID              string          `json:"_id"`
	CommunityInResp CommunityInResp `json:"community"`
}

type CommunityInResp struct {
	Name string `json:"name"`
}
