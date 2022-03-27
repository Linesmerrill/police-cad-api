package databases

// go generate: mockery --name CallDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
)

const callName = "calls"

// CallDatabase contains the methods to use with the call database
type CallDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.Call, error)
	Find(ctx context.Context, filter interface{}) ([]models.Call, error)
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

func (c *callDatabase) FindOne(ctx context.Context, filter interface{}) (*models.Call, error) {
	call := &models.Call{}
	err := c.db.Collection(callName).FindOne(ctx, filter).Decode(&call)
	if err != nil {
		return nil, err
	}
	return call, nil
}

func (c *callDatabase) Find(ctx context.Context, filter interface{}) ([]models.Call, error) {
	var calls []models.Call
	err := c.db.Collection(callName).Find(ctx, filter).Decode(&calls)
	if err != nil {
		return nil, err
	}
	return calls, nil
}
