package databases

// go generate: mockery --name CommunityDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const collectionName = "communities"

// CommunityDatabase contains the methods to use with the community database
type CommunityDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.Community, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	InsertOne(ctx context.Context, community models.Community, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
	Aggregate(ctx context.Context, pipeline mongo.Pipeline, opts ...*options.AggregateOptions) (*MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
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

func (c *communityDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := c.db.Collection(collectionName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}

func (c *communityDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(collectionName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *communityDatabase) InsertOne(ctx context.Context, community models.Community, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(collectionName).InsertOne(ctx, community, opts...)
	return res, err
}

func (c *communityDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(collectionName).DeleteOne(ctx, filter, opts...)

}

func (c *communityDatabase) Aggregate(ctx context.Context, pipeline mongo.Pipeline, opts ...*options.AggregateOptions) (*MongoCursor, error) {
	return c.db.Collection(collectionName).Aggregate(ctx, pipeline, opts...)
}

func (c *communityDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(collectionName).CountDocuments(ctx, filter, opts...)
}
