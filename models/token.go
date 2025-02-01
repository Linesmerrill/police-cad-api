package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Token holds the structure for the token collection in mongo
type Token struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	Token     string             `bson:"token"`
	Email     string             `bson:"email"`
	CreatedAt time.Time          `bson:"createdAt"`
}
