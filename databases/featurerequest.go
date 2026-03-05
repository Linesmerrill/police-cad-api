package databases

// go generate: mockery --name FeatureRequestDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const featureRequestCollectionName = "featureRequests"

// FeatureRequestDatabase contains the methods to use with the feature request database
type FeatureRequestDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.FeatureRequest, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	InsertOne(ctx context.Context, featureRequest models.FeatureRequest, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	Aggregate(ctx context.Context, pipeline mongo.Pipeline, opts ...*options.AggregateOptions) (*MongoCursor, error)
}

type featureRequestDatabase struct {
	db DatabaseHelper
}

// NewFeatureRequestDatabase initializes a new instance of feature request database with the provided db connection
func NewFeatureRequestDatabase(db DatabaseHelper) FeatureRequestDatabase {
	return &featureRequestDatabase{
		db: db,
	}
}

func (f *featureRequestDatabase) FindOne(ctx context.Context, filter interface{}) (*models.FeatureRequest, error) {
	featureRequest := &models.FeatureRequest{}
	err := f.db.Collection(featureRequestCollectionName).FindOne(ctx, filter).Decode(featureRequest)
	if err != nil {
		return nil, err
	}
	return featureRequest, nil
}

func (f *featureRequestDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := f.db.Collection(featureRequestCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}

func (f *featureRequestDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := f.db.Collection(featureRequestCollectionName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (f *featureRequestDatabase) InsertOne(ctx context.Context, featureRequest models.FeatureRequest, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := f.db.Collection(featureRequestCollectionName).InsertOne(ctx, featureRequest, opts...)
	return res, err
}

func (f *featureRequestDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return f.db.Collection(featureRequestCollectionName).DeleteOne(ctx, filter, opts...)
}

func (f *featureRequestDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return f.db.Collection(featureRequestCollectionName).CountDocuments(ctx, filter, opts...)
}

func (f *featureRequestDatabase) Aggregate(ctx context.Context, pipeline mongo.Pipeline, opts ...*options.AggregateOptions) (*MongoCursor, error) {
	return f.db.Collection(featureRequestCollectionName).Aggregate(ctx, pipeline, opts...)
}
