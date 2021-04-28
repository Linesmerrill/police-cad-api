package collections

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"

	"go.mongodb.org/mongo-driver/mongo"
)

const collectionName = "communities"

// CommunityDatabase contains the methods to use with the community database
type CommunityDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.Community, error)
}

type communityDatabase struct {
	db *mongo.Database
}

// NewCommunityDatabase initializes a new instance of community database with the provided db connection
func NewCommunityDatabase(db *mongo.Database) CommunityDatabase {
	return &communityDatabase{
		db: db,
	}
}

func (c *communityDatabase) FindOne(ctx context.Context, filter interface{}) (*models.Community, error) {
	community := &models.Community{}
	err := c.db.Collection(collectionName).FindOne(ctx, filter).Decode(&community)
	if err != nil {
		return nil, err
	}
	return community, nil
}
