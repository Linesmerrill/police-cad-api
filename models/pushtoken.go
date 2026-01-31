package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// PushToken holds the structure for the pushtokens collection in mongo
type PushToken struct {
	ID        primitive.ObjectID `json:"_id" bson:"_id,omitempty"`
	UserID    string             `json:"userId" bson:"userId"`
	Token     string             `json:"token" bson:"token"`       // Expo push token (e.g., "ExponentPushToken[xxx]")
	Platform  string             `json:"platform" bson:"platform"` // "ios" or "android"
	CreatedAt primitive.DateTime `json:"createdAt" bson:"createdAt"`
	UpdatedAt primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
}
