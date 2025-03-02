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
	err = curr.Decode(&warrants)
	if err != nil {
		return nil, err
	}
	return warrants, nil
}
