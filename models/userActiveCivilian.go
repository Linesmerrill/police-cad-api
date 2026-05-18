package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// UserActiveCivilian is the user's persisted "active civilian" choice for a
// given community. The wallet's civilian-swap and the Discord bot's
// /set-active-civilian both write to this collection so the surfaces stay in
// sync — pick a civilian once, every surface honors it until you swap again.
//
// Upsert key: (userId, communityId). One active civilian per user per community.
type UserActiveCivilian struct {
	ID          primitive.ObjectID `json:"_id" bson:"_id,omitempty"`
	UserID      string             `json:"userId" bson:"userId"`
	CommunityID string             `json:"communityId" bson:"communityId"`
	CivilianID  string             `json:"civilianId" bson:"civilianId"`
	UpdatedAt   primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}
