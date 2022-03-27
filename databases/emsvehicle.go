package databases

//go generate: mockery --name EmsVehicleDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
)

const emsVehicleName = "emsvehicles"

// EmsVehicleDatabase contains the methods to use with the emsVehicle database
type EmsVehicleDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.EmsVehicle, error)
	Find(ctx context.Context, filter interface{}) ([]models.EmsVehicle, error)
}

type emsVehicleDatabase struct {
	db DatabaseHelper
}

// NewEmsVehicleDatabase initializes a new instance of user database with the provided db connection
func NewEmsVehicleDatabase(db DatabaseHelper) EmsVehicleDatabase {
	return &emsVehicleDatabase{
		db: db,
	}
}

func (c *emsVehicleDatabase) FindOne(ctx context.Context, filter interface{}) (*models.EmsVehicle, error) {
	emsVehicle := &models.EmsVehicle{}
	err := c.db.Collection(emsVehicleName).FindOne(ctx, filter).Decode(&emsVehicle)
	if err != nil {
		return nil, err
	}
	return emsVehicle, nil
}

func (c *emsVehicleDatabase) Find(ctx context.Context, filter interface{}) ([]models.EmsVehicle, error) {
	var emsVehicles []models.EmsVehicle
	err := c.db.Collection(emsVehicleName).Find(ctx, filter).Decode(&emsVehicles)
	if err != nil {
		return nil, err
	}
	return emsVehicles, nil
}
