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
	var firearms []models.Firearm
	cur, err := c.db.Collection(firearmName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	err = cur.Decode(&firearms)
	if err != nil {
		return nil, err
	}
	return firearms, nil
}

func (c *firearmDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(firearmName).InsertOne(ctx, document, opts...)
	return res, err
}
