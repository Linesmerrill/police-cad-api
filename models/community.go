package models

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Community holds the structure for the community collection in mongo
type Community struct {
	ID              string
	Name            string `json:"name"`
	OwnerID         string `json:"ownerID"`
	Code            string `json:"code"`
	ActivePanics    map[string]interface{}
	ActiveSignal100 bool
	CreatedAt       primitive.DateTime `json:"createdAt"`
	UpdatedAt       time.Time
}

func (c *Community) GetCommunity(ctx context.Context, db *mongo.Database) (bson.M, error) {
	communityCollection := db.Collection("communities")
	var resp bson.M
	cID, err := primitive.ObjectIDFromHex(c.ID)
	if err != nil {
		return nil, err
	}
	err = communityCollection.FindOne(ctx, bson.M{"_id": cID}).Decode(&resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
