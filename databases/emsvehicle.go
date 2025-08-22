package databases

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"
)

// EMSVehicleDatabase defines the interface for EMS vehicle database operations
type EMSVehicleDatabase interface {
	GetEMSVehiclesByCommunityID(ctx context.Context, activeCommunityID string, limit, page int64) (*models.EMSVehicleResponse, error)
	GetEMSVehicleByID(ctx context.Context, id string) (*models.EMSVehicle, error)
	CreateEMSVehicle(ctx context.Context, vehicle *models.EMSVehicle) error
	UpdateEMSVehicle(ctx context.Context, id string, vehicle *models.EMSVehicle) error
	DeleteEMSVehicle(ctx context.Context, id string) error
}

// emsVehicleDatabase implements EMSVehicleDatabase
type emsVehicleDatabase struct {
	collection MongoCollectionHelper
}

// NewEMSVehicleDatabase creates a new EMS vehicle database instance
func NewEMSVehicleDatabase(dbHelper DatabaseHelper) EMSVehicleDatabase {
	return &emsVehicleDatabase{
		collection: dbHelper.Collection("emsvehicles"),
	}
}

// GetEMSVehiclesByCommunityID retrieves EMS vehicles for a specific community with pagination
func (e *emsVehicleDatabase) GetEMSVehiclesByCommunityID(ctx context.Context, activeCommunityID string, limit, page int64) (*models.EMSVehicleResponse, error) {
	// Build filter
	filter := bson.M{
		"vehicle.activeCommunityID": activeCommunityID,
	}

	// Calculate skip value for pagination
	skip := page * limit

	// Set up aggregation pipeline
	pipeline := []bson.M{
		{"$match": filter},
		{"$project": bson.M{
			"_id":                1,
			"plate":              "$vehicle.plate",
			"model":              "$vehicle.model",
			"engineNumber":       "$vehicle.engineNumber",
			"color":              "$vehicle.color",
			"registeredOwner":    "$vehicle.registeredOwner",
			"activeCommunityID":  "$vehicle.activeCommunityID",
			"userID":             "$vehicle.userID",
			"createdAt":          "$vehicle.createdAt",
			"updatedAt":          "$vehicle.updatedAt",
		}},
		{"$sort": bson.M{"createdAt": -1}}, // Sort by creation date, newest first
		{"$skip": skip},
		{"$limit": limit},
	}

	// Execute aggregation
	cursor, err := e.collection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	// Decode results
	var vehicles []models.EMSVehicleWithDetails
	if err := cursor.All(ctx, &vehicles); err != nil {
		return nil, err
	}

	// Get total count for pagination
	totalCount, err := e.collection.CountDocuments(ctx, filter)
	if err != nil {
		return nil, err
	}

	// Calculate pagination info
	totalPages := (totalCount + limit - 1) / limit

	response := &models.EMSVehicleResponse{
		Vehicles: vehicles,
		Pagination: models.Pagination{
			CurrentPage:  page,
			TotalPages:   totalPages,
			TotalRecords: totalCount,
			Limit:        limit,
		},
	}

	return response, nil
}

// GetEMSVehicleByID retrieves a single EMS vehicle by ID
func (e *emsVehicleDatabase) GetEMSVehicleByID(ctx context.Context, id string) (*models.EMSVehicle, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, err
	}

	filter := bson.M{"_id": objectID}
	
	var vehicle models.EMSVehicle
	err = e.collection.FindOne(ctx, filter).Decode(&vehicle)
	if err != nil {
		return nil, err
	}

	return &vehicle, nil
}

// CreateEMSVehicle creates a new EMS vehicle
func (e *emsVehicleDatabase) CreateEMSVehicle(ctx context.Context, vehicle *models.EMSVehicle) error {
	// Set creation and update timestamps
	now := primitive.NewDateTimeFromTime(time.Now())
	vehicle.Vehicle.CreatedAt = now
	vehicle.Vehicle.UpdatedAt = now
	
	// Generate new ObjectID if not provided
	if vehicle.ID.IsZero() {
		vehicle.ID = primitive.NewObjectID()
	}

	_, err := e.collection.InsertOne(ctx, vehicle)
	return err
}

// UpdateEMSVehicle updates an existing EMS vehicle
func (e *emsVehicleDatabase) UpdateEMSVehicle(ctx context.Context, id string, vehicle *models.EMSVehicle) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return err
	}

	filter := bson.M{"_id": objectID}
	
	// Update the timestamp
	vehicle.Vehicle.UpdatedAt = primitive.NewDateTimeFromTime(time.Now())
	
	// Create update document
	update := bson.M{
		"$set": bson.M{
			"vehicle": vehicle.Vehicle,
		},
	}

	_, err = e.collection.UpdateOne(ctx, filter, update)
	return err
}

// DeleteEMSVehicle deletes an EMS vehicle by ID
func (e *emsVehicleDatabase) DeleteEMSVehicle(ctx context.Context, id string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return err
	}

	filter := bson.M{"_id": objectID}
	
	return e.collection.DeleteOne(ctx, filter)
}
