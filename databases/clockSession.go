package databases

// go generate: mockery --name ClockSessionDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const clockSessionName = "clock_sessions"

// ClockSessionDatabase contains the methods to use with the clock_sessions collection
type ClockSessionDatabase interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.ClockSession, error)
	Find(context.Context, interface{}, ...*options.FindOptions) ([]models.ClockSession, error)
	InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error
	FindOneAndUpdate(context.Context, interface{}, interface{}, ...*options.FindOneAndUpdateOptions) *mongo.SingleResult
	CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error)
}

type clockSessionDatabase struct {
	db DatabaseHelper
}

// NewClockSessionDatabase initializes the clock_sessions database accessor.
func NewClockSessionDatabase(db DatabaseHelper) ClockSessionDatabase {
	return &clockSessionDatabase{db: db}
}

func (c *clockSessionDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.ClockSession, error) {
	out := &models.ClockSession{}
	if err := c.db.Collection(clockSessionName).FindOne(ctx, filter, opts...).Decode(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *clockSessionDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.ClockSession, error) {
	var sessions []models.ClockSession
	cur, err := c.db.Collection(clockSessionName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	if err := cur.All(ctx, &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (c *clockSessionDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return c.db.Collection(clockSessionName).InsertOne(ctx, document, opts...)
}

func (c *clockSessionDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(clockSessionName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *clockSessionDatabase) FindOneAndUpdate(ctx context.Context, filter interface{}, update interface{}, opts ...*options.FindOneAndUpdateOptions) *mongo.SingleResult {
	return c.db.Collection(clockSessionName).FindOneAndUpdate(ctx, filter, update, opts...)
}

func (c *clockSessionDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(clockSessionName).CountDocuments(ctx, filter, opts...)
}
