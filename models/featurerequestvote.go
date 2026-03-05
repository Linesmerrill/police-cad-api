package models

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// FeatureRequestVote holds the structure for the featureRequestVotes collection in mongo.
// Each document represents one upvote. Toggle: insert to upvote, delete to remove.
type FeatureRequestVote struct {
	ID               primitive.ObjectID `json:"_id" bson:"_id"`
	FeatureRequestID primitive.ObjectID `json:"featureRequestId" bson:"featureRequestId"`
	User             primitive.ObjectID `json:"user" bson:"user"`
	CreatedAt        primitive.DateTime `json:"createdAt" bson:"createdAt"`
}
