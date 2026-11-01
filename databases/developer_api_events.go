package databases

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/models"
)

// Collections behind the activity feed, the event stream and webhooks.
const (
	developerActivityCollectionName        = "developer_api_activity"
	developerEventCollectionName           = "developer_api_events"
	developerWebhookCollectionName         = "developer_webhooks"
	developerWebhookDeliveryCollectionName = "developer_webhook_deliveries"
)

// DeveloperActivityDatabase holds one row per request a key made.
//
// Write-heavy and append-only: every authenticated developer request produces a
// row. InsertOne is the only write, there is no update path, and the TTL index
// is what bounds the collection — nothing deletes from here by hand.
type DeveloperActivityDatabase interface {
	InsertOne(ctx context.Context, activity models.DeveloperAPIActivity) (InsertOneResultHelper, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.DeveloperAPIActivity, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type developerActivityDatabase struct{ db DatabaseHelper }

// NewDeveloperActivityDatabase creates an activity feed wrapper.
func NewDeveloperActivityDatabase(db DatabaseHelper) DeveloperActivityDatabase {
	return &developerActivityDatabase{db: db}
}

func (d *developerActivityDatabase) InsertOne(ctx context.Context, activity models.DeveloperAPIActivity) (InsertOneResultHelper, error) {
	return d.db.Collection(developerActivityCollectionName).InsertOne(ctx, activity)
}

func (d *developerActivityDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.DeveloperAPIActivity, error) {
	cur, err := d.db.Collection(developerActivityCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var rows []models.DeveloperAPIActivity
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (d *developerActivityDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return d.db.Collection(developerActivityCollectionName).CountDocuments(ctx, filter, opts...)
}

// DeveloperEventDatabase holds the community change stream.
//
// Read by the cursor endpoint and by the webhook worker, with the same filter
// shape in both: "this community, _id greater than this one, in _id order".
type DeveloperEventDatabase interface {
	InsertOne(ctx context.Context, event models.DeveloperAPIEvent) (InsertOneResultHelper, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.DeveloperAPIEvent, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type developerEventDatabase struct{ db DatabaseHelper }

// NewDeveloperEventDatabase creates an event stream wrapper.
func NewDeveloperEventDatabase(db DatabaseHelper) DeveloperEventDatabase {
	return &developerEventDatabase{db: db}
}

func (d *developerEventDatabase) InsertOne(ctx context.Context, event models.DeveloperAPIEvent) (InsertOneResultHelper, error) {
	return d.db.Collection(developerEventCollectionName).InsertOne(ctx, event)
}

func (d *developerEventDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.DeveloperAPIEvent, error) {
	cur, err := d.db.Collection(developerEventCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var rows []models.DeveloperAPIEvent
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (d *developerEventDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return d.db.Collection(developerEventCollectionName).CountDocuments(ctx, filter, opts...)
}

// DeveloperWebhookDatabase holds the endpoints events are delivered to.
type DeveloperWebhookDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.DeveloperWebhook, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.DeveloperWebhook, error)
	InsertOne(ctx context.Context, webhook models.DeveloperWebhook) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type developerWebhookDatabase struct{ db DatabaseHelper }

// NewDeveloperWebhookDatabase creates a webhook endpoint wrapper.
func NewDeveloperWebhookDatabase(db DatabaseHelper) DeveloperWebhookDatabase {
	return &developerWebhookDatabase{db: db}
}

func (d *developerWebhookDatabase) FindOne(ctx context.Context, filter interface{}) (*models.DeveloperWebhook, error) {
	webhook := &models.DeveloperWebhook{}
	if err := d.db.Collection(developerWebhookCollectionName).FindOne(ctx, filter).Decode(webhook); err != nil {
		return nil, err
	}
	return webhook, nil
}

func (d *developerWebhookDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.DeveloperWebhook, error) {
	cur, err := d.db.Collection(developerWebhookCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var rows []models.DeveloperWebhook
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (d *developerWebhookDatabase) InsertOne(ctx context.Context, webhook models.DeveloperWebhook) (InsertOneResultHelper, error) {
	return d.db.Collection(developerWebhookCollectionName).InsertOne(ctx, webhook)
}

func (d *developerWebhookDatabase) UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return d.db.Collection(developerWebhookCollectionName).UpdateOne(ctx, filter, update, opts...)
}

func (d *developerWebhookDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return d.db.Collection(developerWebhookCollectionName).DeleteOne(ctx, filter, opts...)
}

func (d *developerWebhookDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return d.db.Collection(developerWebhookCollectionName).CountDocuments(ctx, filter, opts...)
}

// DeveloperWebhookDeliveryDatabase holds the attempt log.
type DeveloperWebhookDeliveryDatabase interface {
	InsertOne(ctx context.Context, delivery models.DeveloperWebhookDelivery) (InsertOneResultHelper, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.DeveloperWebhookDelivery, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type developerWebhookDeliveryDatabase struct{ db DatabaseHelper }

// NewDeveloperWebhookDeliveryDatabase creates a delivery log wrapper.
func NewDeveloperWebhookDeliveryDatabase(db DatabaseHelper) DeveloperWebhookDeliveryDatabase {
	return &developerWebhookDeliveryDatabase{db: db}
}

func (d *developerWebhookDeliveryDatabase) InsertOne(ctx context.Context, delivery models.DeveloperWebhookDelivery) (InsertOneResultHelper, error) {
	return d.db.Collection(developerWebhookDeliveryCollectionName).InsertOne(ctx, delivery)
}

func (d *developerWebhookDeliveryDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.DeveloperWebhookDelivery, error) {
	cur, err := d.db.Collection(developerWebhookDeliveryCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var rows []models.DeveloperWebhookDelivery
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (d *developerWebhookDeliveryDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return d.db.Collection(developerWebhookDeliveryCollectionName).CountDocuments(ctx, filter, opts...)
}
