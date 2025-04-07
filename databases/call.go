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
	InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) (*models.Call, error)
	DeleteOne(context.Context, interface{}, ...*options.DeleteOptions) error
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

func (c *callDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(callName).InsertOne(ctx, document, opts...)
	return res, err
}

func (c *callDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*models.Call, error) {
	_, err := c.db.Collection(callName).UpdateOne(ctx, filter, update, opts...)
	if err != nil {
		return nil, err
	}
	call := &models.Call{}
	err = c.db.Collection(callName).FindOne(ctx, filter).Decode(&call)
	if err != nil {
		return nil, err
	}
	return call, nil
}

func (c *callDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(callName).DeleteOne(ctx, filter, opts...)

}
