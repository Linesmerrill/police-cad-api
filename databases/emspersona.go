package databases

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"
)

// EMSPersonaDatabase defines the interface for EMS persona database operations
type EMSPersonaDatabase interface {
	GetEMSPersonasByCommunityID(ctx context.Context, activeCommunityID string, limit, page int64) (*models.EMSPersonaResponse, error)
	GetEMSPersonaByID(ctx context.Context, id string) (*models.EMSPersona, error)
	CreateEMSPersona(ctx context.Context, persona *models.EMSPersona) error
	UpdateEMSPersona(ctx context.Context, id string, persona *models.EMSPersona) error
	DeleteEMSPersona(ctx context.Context, id string) error
}

// emsPersonaDatabase implements EMSPersonaDatabase
type emsPersonaDatabase struct {
	collection MongoCollectionHelper
}

// NewEMSPersonaDatabase creates a new EMS persona database instance
func NewEMSPersonaDatabase(dbHelper DatabaseHelper) EMSPersonaDatabase {
	return &emsPersonaDatabase{
		collection: dbHelper.Collection("ems"),
	}
}

// GetEMSPersonasByCommunityID retrieves EMS personas for a specific community with pagination
func (e *emsPersonaDatabase) GetEMSPersonasByCommunityID(ctx context.Context, activeCommunityID string, limit, page int64) (*models.EMSPersonaResponse, error) {
	// Build filter
	filter := bson.M{
		"persona.activeCommunityID": activeCommunityID,
	}

	// Calculate skip value for pagination
	skip := page * limit

	// Set up aggregation pipeline
	pipeline := []bson.M{
		{"$match": filter},
		{"$project": bson.M{
			"_id":                1,
			"firstName":          "$persona.firstName",
			"lastName":           "$persona.lastName",
			"department":         "$persona.department",
			"assignmentArea":     "$persona.assignmentArea",
			"station":            "$persona.station",
			"callSign":           "$persona.callSign",
			"activeCommunityID":  "$persona.activeCommunityID",
			"userID":             "$persona.userID",
			"createdAt":          "$persona.createdAt",
			"updatedAt":          "$persona.updatedAt",
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
	var personas []models.EMSPersonaWithDetails
	if err := cursor.All(ctx, &personas); err != nil {
		return nil, err
	}

	// Get total count for pagination
	totalCount, err := e.collection.CountDocuments(ctx, filter)
	if err != nil {
		return nil, err
	}

	// Calculate pagination info
	totalPages := (totalCount + limit - 1) / limit

	response := &models.EMSPersonaResponse{
		Personas: personas,
		Pagination: models.Pagination{
			CurrentPage:  page,
			TotalPages:   totalPages,
			TotalRecords: totalCount,
			Limit:        limit,
		},
	}

	return response, nil
}

// GetEMSPersonaByID retrieves a single EMS persona by ID
func (e *emsPersonaDatabase) GetEMSPersonaByID(ctx context.Context, id string) (*models.EMSPersona, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, err
	}

	filter := bson.M{"_id": objectID}
	
	var persona models.EMSPersona
	err = e.collection.FindOne(ctx, filter).Decode(&persona)
	if err != nil {
		return nil, err
	}

	return &persona, nil
}

// CreateEMSPersona creates a new EMS persona
func (e *emsPersonaDatabase) CreateEMSPersona(ctx context.Context, persona *models.EMSPersona) error {
	// Set creation and update timestamps
	now := primitive.NewDateTimeFromTime(time.Now())
	persona.Persona.CreatedAt = now
	persona.Persona.UpdatedAt = now
	
	// Generate new ObjectID if not provided
	if persona.ID.IsZero() {
		persona.ID = primitive.NewObjectID()
	}

	_, err := e.collection.InsertOne(ctx, persona)
	return err
}

// UpdateEMSPersona updates an existing EMS persona
func (e *emsPersonaDatabase) UpdateEMSPersona(ctx context.Context, id string, persona *models.EMSPersona) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return err
	}

	filter := bson.M{"_id": objectID}
	
	// Update the timestamp
	persona.Persona.UpdatedAt = primitive.NewDateTimeFromTime(time.Now())
	
	// Create update document
	update := bson.M{
		"$set": bson.M{
			"persona": persona.Persona,
		},
	}

	_, err = e.collection.UpdateOne(ctx, filter, update)
	return err
}

// DeleteEMSPersona deletes an EMS persona by ID
func (e *emsPersonaDatabase) DeleteEMSPersona(ctx context.Context, id string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return err
	}

	filter := bson.M{"_id": objectID}
	
	return e.collection.DeleteOne(ctx, filter)
}
