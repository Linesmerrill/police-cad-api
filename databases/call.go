package databases

// go generate: mockery --name CallDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const callName = "calls"

// CallDatabase contains the methods to use with the call database
type CallDatabase interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.Call, error)
	Find(context.Context, interface{}, ...*options.FindOptions) ([]models.Call, error)
}

type callDatabase struct {
	db DatabaseHelper
}

// NewCallDatabase initializes a new instance of user database with the provided db connection
func NewCallDatabase(db DatabaseHelper) CallDatabase {
	return &callDatabase{
		db: db,
	}
}

func (c *callDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.Call, error) {
	call := &models.Call{}
	err := c.db.Collection(callName).FindOne(ctx, filter, opts...).Decode(&call)
	if err != nil {
		return nil, err
	}
	return call, nil
}

func (c *callDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.Call, error) {
	var calls []models.Call
	cr, err := c.db.Collection(callName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	err = cr.Decode(&calls)
	if err != nil {
		return nil, err
	}
	return calls, nil
}
