package databases

// go generate: mockery --name LicenseDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const licenseName = "licenses"

// LicenseDatabase contains the methods to use with the license database
type LicenseDatabase interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.License, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.License, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	InsertOne(ctx context.Context, license models.License, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
}

type licenseDatabase struct {
	db DatabaseHelper
}

// NewLicenseDatabase initializes a new instance of license database with the provided db connection
func NewLicenseDatabase(db DatabaseHelper) LicenseDatabase {
	return &licenseDatabase{
		db: db,
	}
}

func (c *licenseDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.License, error) {
	license := &models.License{}
	err := c.db.Collection(licenseName).FindOne(ctx, filter).Decode(&license)
	if err != nil {
		return nil, err
	}
	return license, nil
}

func (c *licenseDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.License, error) {
	var licenses []models.License
	cur, err := c.db.Collection(licenseName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	err = cur.Decode(&licenses)
	if err != nil {
		return nil, err
	}
	return licenses, nil
}

func (c *licenseDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	count, err := c.db.Collection(licenseName).CountDocuments(ctx, filter, opts...)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (c *licenseDatabase) InsertOne(ctx context.Context, license models.License, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return c.db.Collection(licenseName).InsertOne(ctx, license, opts...)
}

func (c *licenseDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(licenseName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *licenseDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(licenseName).DeleteOne(ctx, filter, opts...)
}
