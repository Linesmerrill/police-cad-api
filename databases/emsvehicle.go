package databases

// go generate: mockery --name EmsVehicleDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const emsVehicleName = "emsvehicles"

// EmsVehicleDatabase contains the methods to use with the emsVehicle database
type EmsVehicleDatabase interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.EmsVehicle, error)
	Find(context.Context, interface{}, ...*options.FindOptions) ([]models.EmsVehicle, error)
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

func (c *emsVehicleDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.EmsVehicle, error) {
	emsVehicle := &models.EmsVehicle{}
	err := c.db.Collection(emsVehicleName).FindOne(ctx, filter, opts...).Decode(&emsVehicle)
	if err != nil {
		return nil, err
	}
	return emsVehicle, nil
}

func (c *emsVehicleDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.EmsVehicle, error) {
	var emsVehicles []models.EmsVehicle
	err := c.db.Collection(emsVehicleName).Find(ctx, filter, opts...).Decode(&emsVehicles)
	if err != nil {
		return nil, err
	}
	return emsVehicles, nil
}
