package databases

// go generate: mockery --name ArchivedCommunityDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const archivedCommunityName = "archivedcommunities"

// ArchivedCommunityDatabase contains the methods to use with the archivedCommunity database
type ArchivedCommunityDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.ArchivedCommunity, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	InsertOne(ctx context.Context, archivedCommunity models.Community, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
	Aggregate(ctx context.Context, pipeline mongo.Pipeline, opts ...*options.AggregateOptions) (*MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type archivedCommunityDatabase struct {
	db DatabaseHelper
}

// NewArchivedCommunityDatabase initializes a new instance of archivedCommunity database with the provided db connection
func NewArchivedCommunityDatabase(db DatabaseHelper) ArchivedCommunityDatabase {
	return &archivedCommunityDatabase{
		db: db,
	}
}

func (c *archivedCommunityDatabase) FindOne(ctx context.Context, filter interface{}) (*models.ArchivedCommunity, error) {
	archivedCommunity := &models.ArchivedCommunity{}
	err := c.db.Collection(archivedCommunityName).FindOne(ctx, filter).Decode(&archivedCommunity)
	if err != nil {
		return nil, err
	}
	return archivedCommunity, nil
}

func (c *archivedCommunityDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := c.db.Collection(archivedCommunityName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}

func (c *archivedCommunityDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(archivedCommunityName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *archivedCommunityDatabase) InsertOne(ctx context.Context, archivedCommunity models.Community, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(archivedCommunityName).InsertOne(ctx, archivedCommunity, opts...)
	return res, err
}

func (c *archivedCommunityDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(archivedCommunityName).DeleteOne(ctx, filter, opts...)

}

func (c *archivedCommunityDatabase) Aggregate(ctx context.Context, pipeline mongo.Pipeline, opts ...*options.AggregateOptions) (*MongoCursor, error) {
	return c.db.Collection(archivedCommunityName).Aggregate(ctx, pipeline, opts...)
}

func (c *archivedCommunityDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(archivedCommunityName).CountDocuments(ctx, filter, opts...)
}
