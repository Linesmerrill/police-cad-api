package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// PendingVerification holds the structure for the pending verification collection in MongoDB
type PendingVerification struct {
	ID        primitive.ObjectID `json:"_id" bson:"_id"`
	Email     string             `json:"email" bson:"email"`
	Code      string             `json:"code" bson:"code"`
	Attempts  int                `json:"attempts" bson:"attempts"`
	CreatedAt interface{}        `json:"createdAt" bson:"createdAt"`
}
