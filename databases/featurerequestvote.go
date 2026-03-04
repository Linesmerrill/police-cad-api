package databases

// go generate: mockery --name FeatureRequestVoteDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const featureRequestVoteCollectionName = "featureRequestVotes"

// FeatureRequestVoteDatabase contains the methods to use with the feature request vote database
type FeatureRequestVoteDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.FeatureRequestVote, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	InsertOne(ctx context.Context, vote models.FeatureRequestVote, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
	DeleteMany(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) (int64, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type featureRequestVoteDatabase struct {
	db DatabaseHelper
}

// NewFeatureRequestVoteDatabase initializes a new instance of feature request vote database with the provided db connection
func NewFeatureRequestVoteDatabase(db DatabaseHelper) FeatureRequestVoteDatabase {
	return &featureRequestVoteDatabase{
		db: db,
	}
}

func (f *featureRequestVoteDatabase) FindOne(ctx context.Context, filter interface{}) (*models.FeatureRequestVote, error) {
	vote := &models.FeatureRequestVote{}
	err := f.db.Collection(featureRequestVoteCollectionName).FindOne(ctx, filter).Decode(vote)
	if err != nil {
		return nil, err
	}
	return vote, nil
}

func (f *featureRequestVoteDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := f.db.Collection(featureRequestVoteCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}

func (f *featureRequestVoteDatabase) InsertOne(ctx context.Context, vote models.FeatureRequestVote, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := f.db.Collection(featureRequestVoteCollectionName).InsertOne(ctx, vote, opts...)
	return res, err
}

func (f *featureRequestVoteDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return f.db.Collection(featureRequestVoteCollectionName).DeleteOne(ctx, filter, opts...)
}

func (f *featureRequestVoteDatabase) DeleteMany(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) (int64, error) {
	return f.db.Collection(featureRequestVoteCollectionName).DeleteMany(ctx, filter, opts...)
}

func (f *featureRequestVoteDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return f.db.Collection(featureRequestVoteCollectionName).CountDocuments(ctx, filter, opts...)
}
