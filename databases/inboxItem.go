package databases

// go generate: mockery --name InboxItemDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const inboxItemName = "inbox_items"

// InboxItemDatabase contains the methods to use with the inbox_items collection
type InboxItemDatabase interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.InboxItem, error)
	Find(context.Context, interface{}, ...*options.FindOptions) ([]models.InboxItem, error)
	InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error
	UpdateMany(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error
	FindOneAndUpdate(context.Context, interface{}, interface{}, ...*options.FindOneAndUpdateOptions) *mongo.SingleResult
	CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error)
}

type inboxItemDatabase struct {
	db DatabaseHelper
}

// NewInboxItemDatabase initializes the inbox_items database accessor.
func NewInboxItemDatabase(db DatabaseHelper) InboxItemDatabase {
	return &inboxItemDatabase{db: db}
}

func (i *inboxItemDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.InboxItem, error) {
	out := &models.InboxItem{}
	if err := i.db.Collection(inboxItemName).FindOne(ctx, filter, opts...).Decode(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (i *inboxItemDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.InboxItem, error) {
	var items []models.InboxItem
	cur, err := i.db.Collection(inboxItemName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	if err := cur.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (i *inboxItemDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return i.db.Collection(inboxItemName).InsertOne(ctx, document, opts...)
}

func (i *inboxItemDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := i.db.Collection(inboxItemName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (i *inboxItemDatabase) UpdateMany(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := i.db.Collection(inboxItemName).UpdateMany(ctx, filter, update, opts...)
	return err
}

func (i *inboxItemDatabase) FindOneAndUpdate(ctx context.Context, filter interface{}, update interface{}, opts ...*options.FindOneAndUpdateOptions) *mongo.SingleResult {
	return i.db.Collection(inboxItemName).FindOneAndUpdate(ctx, filter, update, opts...)
}

func (i *inboxItemDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return i.db.Collection(inboxItemName).CountDocuments(ctx, filter, opts...)
}
