package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// CourtChatMessage holds the structure for the courtchat collection in mongo
type CourtChatMessage struct {
	ID        primitive.ObjectID `json:"_id" bson:"_id"`
	SessionID string             `json:"sessionID" bson:"sessionID"` // court session ID
	UserID    string             `json:"userID" bson:"userID"`
	UserName  string             `json:"userName" bson:"userName"`
	Message   string             `json:"message" bson:"message"`
	Role      string             `json:"role" bson:"role"` // "judge", "defendant", "spectator"
	CreatedAt primitive.DateTime `json:"createdAt" bson:"createdAt"`
}
