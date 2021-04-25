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

// GetCommunity finds the corresponding community with the _id.
// Since these _id's are randomly generated non-sequential numbers, I think
// it is safe to expose this route.
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

// GetCommunityByOwner finds a single community by _id that belongs to the ownerID.
// ideally this is to provide, only the owner of a community, the power to see the community
// details.
//
// Potential loophole here: if provided a matching communityID and ownerID,
// then any user with a valid token could invoke this route and get back the details.
// We should probably fix this at some point :famous-last-words:
//
// Potential fix to loophole: Require that we receive the logged in userID submitting the
// request. Then we can validate that the ownerID matches the userID.
func (c *Community) GetCommunityByOwner(ctx context.Context, db *mongo.Database) (bson.M, error) {
	communityCollection := db.Collection("communities")
	var resp bson.M
	cID, err := primitive.ObjectIDFromHex(c.ID)
	if err != nil {
		return nil, err
	}
	err = communityCollection.FindOne(ctx, bson.M{"_id": cID, "community.ownerID": c.OwnerID}).Decode(&resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
