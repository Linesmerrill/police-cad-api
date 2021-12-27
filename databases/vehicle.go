package databases

//go generate: mockery --name VehicleDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
)

const vehicleName = "vehicles"

// VehicleDatabase contains the methods to use with the vehicle database
type VehicleDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.Vehicle, error)
	Find(ctx context.Context, filter interface{}) ([]models.Vehicle, error)
}

type vehicleDatabase struct {
	db DatabaseHelper
}

// NewVehicleDatabase initializes a new instance of user database with the provided db connection
func NewVehicleDatabase(db DatabaseHelper) VehicleDatabase {
	return &vehicleDatabase{
		db: db,
	}
}

func (c *vehicleDatabase) FindOne(ctx context.Context, filter interface{}) (*models.Vehicle, error) {
	vehicle := &models.Vehicle{}
	err := c.db.Collection(vehicleName).FindOne(ctx, filter).Decode(&vehicle)
	if err != nil {
		return nil, err
	}
	return vehicle, nil
}

func (c *vehicleDatabase) Find(ctx context.Context, filter interface{}) ([]models.Vehicle, error) {
	var vehicles []models.Vehicle
	err := c.db.Collection(vehicleName).Find(ctx, filter).Decode(&vehicles)
	if err != nil {
		return nil, err
	}
	return vehicles, nil
}
