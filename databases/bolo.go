package databases

// go generate: mockery --name BoloDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const boloName = "bolos"

// BoloDatabase contains the methods to use with the bolo database
type BoloDatabase interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.Bolo, error)
	Find(context.Context, interface{}, ...*options.FindOptions) ([]models.Bolo, error)
	CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error)
	InsertOne(ctx context.Context, bolo models.Bolo, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
}

type boloDatabase struct {
	db DatabaseHelper
}

// NewBoloDatabase initializes a new instance of user database with the provided db connection
func NewBoloDatabase(db DatabaseHelper) BoloDatabase {
	return &boloDatabase{
		db: db,
	}
}

func (c *boloDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.Bolo, error) {
	bolo := &models.Bolo{}
	err := c.db.Collection(boloName).FindOne(ctx, filter, opts...).Decode(&bolo)
	if err != nil {
		return nil, err
	}
	return bolo, nil
}

func (c *boloDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.Bolo, error) {
	var bolos []models.Bolo
	cr, err := c.db.Collection(boloName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	err = cr.Decode(&bolos)
	if err != nil {
		return nil, err
	}
	return bolos, nil
}

func (c *boloDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	count, err := c.db.Collection(boloName).CountDocuments(ctx, filter, opts...)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (c *boloDatabase) InsertOne(ctx context.Context, bolo models.Bolo, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(boloName).InsertOne(ctx, bolo, opts...)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (c *boloDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(boloName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *boloDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	err := c.db.Collection(boloName).DeleteOne(ctx, filter, opts...)
	if err != nil {
		return err
	}
	return nil
}
