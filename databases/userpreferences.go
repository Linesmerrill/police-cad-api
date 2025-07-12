package databases

// go generate: mockery --name UserPreferencesDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const userPreferencesName = "userpreferences"

// UserPreferencesDatabase contains the methods to use with the user preferences database
type UserPreferencesDatabase interface {
	FindOne(ctx context.Context, filter interface{}) SingleResultHelper
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	InsertOne(ctx context.Context, userPreferences models.UserPreferences) (InsertOneResultHelper, error)
	Aggregate(ctx context.Context, pipeline interface{}) (*MongoCursor, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	UpdateMany(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
}

type userPreferencesDatabase struct {
	db DatabaseHelper
}

// NewUserPreferencesDatabase initializes a new instance of user preferences database with the provided db connection
func NewUserPreferencesDatabase(db DatabaseHelper) UserPreferencesDatabase {
	return &userPreferencesDatabase{
		db: db,
	}
}

func (up *userPreferencesDatabase) FindOne(ctx context.Context, filter interface{}) SingleResultHelper {
	return up.db.Collection(userPreferencesName).FindOne(ctx, filter)
}

func (up *userPreferencesDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	cursor, err := up.db.Collection(userPreferencesName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	return cursor, err
}

func (up *userPreferencesDatabase) InsertOne(ctx context.Context, userPreferences models.UserPreferences) (InsertOneResultHelper, error) {
	res, err := up.db.Collection(userPreferencesName).InsertOne(ctx, userPreferences)
	return res, err
}

func (up *userPreferencesDatabase) Aggregate(ctx context.Context, pipeline interface{}) (*MongoCursor, error) {
	cursor, err := up.db.Collection(userPreferencesName).Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	return cursor, err
}

func (up *userPreferencesDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	res, err := up.db.Collection(userPreferencesName).UpdateOne(ctx, filter, update, opts...)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (up *userPreferencesDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	count, err := up.db.Collection(userPreferencesName).CountDocuments(ctx, filter, opts...)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (up *userPreferencesDatabase) UpdateMany(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	res, err := up.db.Collection(userPreferencesName).UpdateMany(ctx, filter, update, opts...)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (up *userPreferencesDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return up.db.Collection(userPreferencesName).DeleteOne(ctx, filter, opts...)
} 