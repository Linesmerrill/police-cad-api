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
	err := c.db.Collection(licenseName).Find(ctx, filter, opts...).Decode(&licenses)
	if err != nil {
		return nil, err
	}
	return licenses, nil
}
