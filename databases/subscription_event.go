package databases

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const subscriptionEventCollectionName = "subscription_events"

// SubscriptionEventDatabase defines the interface for subscription_events operations.
type SubscriptionEventDatabase interface {
	InsertOne(ctx context.Context, event models.SubscriptionEvent, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) SingleResultHelper
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type subscriptionEventDatabase struct {
	db DatabaseHelper
}

// NewSubscriptionEventDatabase creates a new subscription_events database wrapper.
func NewSubscriptionEventDatabase(db DatabaseHelper) SubscriptionEventDatabase {
	return &subscriptionEventDatabase{db: db}
}

func (s *subscriptionEventDatabase) InsertOne(ctx context.Context, event models.SubscriptionEvent, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return s.db.Collection(subscriptionEventCollectionName).InsertOne(ctx, event, opts...)
}

func (s *subscriptionEventDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) SingleResultHelper {
	return s.db.Collection(subscriptionEventCollectionName).FindOne(ctx, filter, opts...)
}

func (s *subscriptionEventDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return s.db.Collection(subscriptionEventCollectionName).Find(ctx, filter, opts...)
}

func (s *subscriptionEventDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return s.db.Collection(subscriptionEventCollectionName).CountDocuments(ctx, filter, opts...)
}
