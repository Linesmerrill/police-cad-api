package databases

//go generate: mockery --name FirearmDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
)

const firearmName = "firearms"

// FirearmDatabase contains the methods to use with the firearm database
type FirearmDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.Firearm, error)
	Find(ctx context.Context, filter interface{}) ([]models.Firearm, error)
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

func (c *firearmDatabase) FindOne(ctx context.Context, filter interface{}) (*models.Firearm, error) {
	firearm := &models.Firearm{}
	err := c.db.Collection(firearmName).FindOne(ctx, filter).Decode(&firearm)
	if err != nil {
		return nil, err
	}
	return firearm, nil
}

func (c *firearmDatabase) Find(ctx context.Context, filter interface{}) ([]models.Firearm, error) {
	var firearms []models.Firearm
	err := c.db.Collection(firearmName).Find(ctx, filter).Decode(&firearms)
	if err != nil {
		return nil, err
	}
	return firearms, nil
}
