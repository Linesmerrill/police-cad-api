package databases

// go generate: mockery --name EmsDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const emsName = "ems"

// EmsDatabase contains the methods to use with the ems database
type EmsDatabase interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.Ems, error)
	Find(context.Context, interface{}, ...*options.FindOptions) ([]models.Ems, error)
}

type emsDatabase struct {
	db DatabaseHelper
}

// NewEmsDatabase initializes a new instance of user database with the provided db connection
func NewEmsDatabase(db DatabaseHelper) EmsDatabase {
	return &emsDatabase{
		db: db,
	}
}

func (c *emsDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.Ems, error) {
	ems := &models.Ems{}
	err := c.db.Collection(emsName).FindOne(ctx, filter, opts...).Decode(&ems)
	if err != nil {
		return nil, err
	}
	return ems, nil
}

func (c *emsDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.Ems, error) {
	var ems []models.Ems
	err := c.db.Collection(emsName).Find(ctx, filter, opts...).Decode(&ems)
	if err != nil {
		return nil, err
	}
	return ems, nil
}
