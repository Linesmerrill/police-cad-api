package databases

// go generate: mockery --name VehicleDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const vehicleName = "vehicles"

// VehicleDatabase contains the methods to use with the vehicle database
type VehicleDatabase interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.Vehicle, error)
	Find(context.Context, interface{}, ...*options.FindOptions) ([]models.Vehicle, error)
	InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	DeleteOne(context.Context, interface{}, ...*options.DeleteOptions) error
	UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error
	CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error)
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

func (c *vehicleDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.Vehicle, error) {
	vehicle := &models.Vehicle{}
	err := c.db.Collection(vehicleName).FindOne(ctx, filter, opts...).Decode(&vehicle)
	if err != nil {
		return nil, err
	}
	return vehicle, nil
}

func (c *vehicleDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.Vehicle, error) {
	var vehicles []models.Vehicle
	cur, err := c.db.Collection(vehicleName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx) // Ensure cursor is closed
	
	// Use All() with context instead of Decode() which uses context.Background()
	err = cur.All(ctx, &vehicles)
	if err != nil {
		return nil, err
	}
	return vehicles, nil
}

func (c *vehicleDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(vehicleName).InsertOne(ctx, document, opts...)
	return res, err
}

func (c *vehicleDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	err := c.db.Collection(vehicleName).DeleteOne(ctx, filter, opts...)
	if err != nil {
		return err
	}
	return nil
}

func (c *vehicleDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(vehicleName).UpdateOne(ctx, filter, update, opts...)
	if err != nil {
		return err
	}
	return nil
}

func (c *vehicleDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(vehicleName).CountDocuments(ctx, filter, opts...)
}
