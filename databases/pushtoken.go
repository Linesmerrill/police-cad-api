package databases

// go generate: mockery --name PushTokenDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const pushTokenCollectionName = "pushtokens"

// PushTokenDatabase contains the methods to use with the push token database
type PushTokenDatabase interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.PushToken, error)
	Find(context.Context, interface{}, ...*options.FindOptions) ([]models.PushToken, error)
	InsertOne(context.Context, models.PushToken) (InsertOneResultHelper, error)
	UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	DeleteOne(context.Context, interface{}, ...*options.DeleteOptions) error
	DeleteMany(context.Context, interface{}, ...*options.DeleteOptions) (int64, error)
}

type pushTokenDatabase struct {
	db DatabaseHelper
}

// NewPushTokenDatabase initializes a new instance of push token database with the provided db connection
func NewPushTokenDatabase(db DatabaseHelper) PushTokenDatabase {
	return &pushTokenDatabase{
		db: db,
	}
}

func (pt *pushTokenDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.PushToken, error) {
	token := &models.PushToken{}
	err := pt.db.Collection(pushTokenCollectionName).FindOne(ctx, filter, opts...).Decode(token)
	if err != nil {
		return nil, err
	}
	return token, nil
}

func (pt *pushTokenDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.PushToken, error) {
	var tokens []models.PushToken
	cur, err := pt.db.Collection(pushTokenCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	err = cur.Decode(&tokens)
	if err != nil {
		return nil, err
	}
	return tokens, nil
}

func (pt *pushTokenDatabase) InsertOne(ctx context.Context, token models.PushToken) (InsertOneResultHelper, error) {
	res, err := pt.db.Collection(pushTokenCollectionName).InsertOne(ctx, token)
	return res, err
}

func (pt *pushTokenDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return pt.db.Collection(pushTokenCollectionName).UpdateOne(ctx, filter, update, opts...)
}

func (pt *pushTokenDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return pt.db.Collection(pushTokenCollectionName).DeleteOne(ctx, filter, opts...)
}

func (pt *pushTokenDatabase) DeleteMany(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) (int64, error) {
	return pt.db.Collection(pushTokenCollectionName).DeleteMany(ctx, filter, opts...)
}
