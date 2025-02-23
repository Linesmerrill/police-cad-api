package databases

// go generate: mockery --name CommunityDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const collectionName = "communities"

// CommunityDatabase contains the methods to use with the community database
type CommunityDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.Community, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.Community, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	InsertOne(ctx context.Context, community models.Community, opts ...*options.InsertOneOptions) InsertOneResultHelper
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

func (c *communityDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.Community, error) {
	var communities []models.Community
	err := c.db.Collection(collectionName).Find(ctx, filter, opts...).Decode(&communities)
	if err != nil {
		return nil, err
	}
	return communities, nil
}

func (c *communityDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(collectionName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *communityDatabase) InsertOne(ctx context.Context, community models.Community, opts ...*options.InsertOneOptions) InsertOneResultHelper {
	res := c.db.Collection(collectionName).InsertOne(ctx, community, opts...)
	return res
}
