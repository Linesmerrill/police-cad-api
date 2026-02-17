package databases

// go generate: mockery --name WarrantDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const warrantName = "warrants"

// WarrantDatabase contains the methods to use with the warrant database
type WarrantDatabase interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.Warrant, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.Warrant, error)
	InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	DeleteOne(context.Context, interface{}, ...*options.DeleteOptions) error
	UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error
	CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error)
}

type warrantDatabase struct {
	db DatabaseHelper
}

// NewWarrantDatabase initializes a new instance of warrant database with the provided db connection
func NewWarrantDatabase(db DatabaseHelper) WarrantDatabase {
	return &warrantDatabase{
		db: db,
	}
}

func (c *warrantDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.Warrant, error) {
	warrant := &models.Warrant{}
	err := c.db.Collection(warrantName).FindOne(ctx, filter).Decode(&warrant)
	if err != nil {
		return nil, err
	}
	return warrant, nil
}

func (c *warrantDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.Warrant, error) {
	var warrants []models.Warrant
	curr, err := c.db.Collection(warrantName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer curr.Close(ctx)
	err = curr.All(ctx, &warrants)
	if err != nil {
		return nil, err
	}
	return warrants, nil
}

func (c *warrantDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(warrantName).InsertOne(ctx, document, opts...)
	return res, err
}

func (c *warrantDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(warrantName).DeleteOne(ctx, filter, opts...)
}

func (c *warrantDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(warrantName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *warrantDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(warrantName).CountDocuments(ctx, filter, opts...)
}
