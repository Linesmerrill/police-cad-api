package collections

//go generate: mockery --name CommunityDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
)

const collectionName = "communities"

// CommunityDatabase contains the methods to use with the community database
type CommunityDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.Community, error)
}

type communityDatabase struct {
	db DatabaseHelper
}

// NewCommunityDatabase initializes a new instance of community database with the provided db connection
func NewCommunityDatabase(db DatabaseHelper) CommunityDatabase {
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
