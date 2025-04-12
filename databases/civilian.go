package databases

// go generate: mockery --name CivilianDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const civilianName = "civilians"

// CivilianDatabase contains the methods to use with the civilian database
type CivilianDatabase interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.Civilian, error)
	Find(context.Context, interface{}, ...*options.FindOptions) ([]models.Civilian, error)
	InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error
	DeleteOne(context.Context, interface{}, ...*options.DeleteOptions) error
	FindOneAndUpdate(context.Context, interface{}, interface{}, ...*options.FindOneAndUpdateOptions) *mongo.SingleResult
}

type civilianDatabase struct {
	db DatabaseHelper
}

// NewCivilianDatabase initializes a new instance of user database with the provided db connection
func NewCivilianDatabase(db DatabaseHelper) CivilianDatabase {
	return &civilianDatabase{
		db: db,
	}
}

func (c *civilianDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.Civilian, error) {
	civilian := &models.Civilian{}
	err := c.db.Collection(civilianName).FindOne(ctx, filter, opts...).Decode(&civilian)
	if err != nil {
		return nil, err
	}
	return civilian, nil
}

func (c *civilianDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.Civilian, error) {
	var civilians []models.Civilian
	cur, err := c.db.Collection(civilianName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	err = cur.Decode(&civilians)
	if err != nil {
		return nil, err
	}
	return civilians, nil
}

func (c *civilianDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(civilianName).InsertOne(ctx, document, opts...)
	return res, err
}

func (c *civilianDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(civilianName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *civilianDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(civilianName).DeleteOne(ctx, filter, opts...)
}

func (c *civilianDatabase) FindOneAndUpdate(ctx context.Context, filter interface{}, update interface{}, opts ...*options.FindOneAndUpdateOptions) *mongo.SingleResult {
	res := c.db.Collection(civilianName).FindOneAndUpdate(ctx, filter, update, opts...)
	return res
}
