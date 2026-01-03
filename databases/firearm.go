package databases

// go generate: mockery --name FirearmDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const firearmName = "firearms"

// FirearmDatabase contains the methods to use with the firearm database
type FirearmDatabase interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.Firearm, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.Firearm, error)
	InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	DeleteOne(context.Context, interface{}, ...*options.DeleteOptions) error
	UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error
	CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error)
}

type firearmDatabase struct {
	db DatabaseHelper
}

// NewFirearmDatabase initializes a new instance of firearm database with the provided db connection
func NewFirearmDatabase(db DatabaseHelper) FirearmDatabase {
	return &firearmDatabase{
		db: db,
	}
}

func (c *firearmDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.Firearm, error) {
	firearm := &models.Firearm{}
	err := c.db.Collection(firearmName).FindOne(ctx, filter).Decode(&firearm)
	if err != nil {
		return nil, err
	}
	return firearm, nil
}

func (c *firearmDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.Firearm, error) {
	cur, err := c.db.Collection(firearmName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var firearms []models.Firearm
	err = cur.All(ctx, &firearms)
	if err != nil {
		return nil, err
	}
	return firearms, nil
}

func (c *firearmDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(firearmName).InsertOne(ctx, document, opts...)
	return res, err
}

func (c *firearmDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(firearmName).DeleteOne(ctx, filter, opts...)
}

func (c *firearmDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(firearmName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *firearmDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(firearmName).CountDocuments(ctx, filter, opts...)
}
