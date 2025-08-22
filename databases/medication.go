package databases

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"
)

// MedicationDatabase defines the interface for medication database operations
type MedicationDatabase interface {
	GetMedicationsByCivilianID(ctx context.Context, civilianID, activeCommunityID string, limit, page int64) (*models.MedicationResponse, error)
	GetMedicationByID(ctx context.Context, id string) (*models.Medication, error)
	CreateMedication(ctx context.Context, medication *models.Medication) error
	UpdateMedication(ctx context.Context, id string, medication *models.Medication) error
	DeleteMedication(ctx context.Context, id string) error
}

// medicationDatabase implements MedicationDatabase
type medicationDatabase struct {
	collection MongoCollectionHelper
}

// NewMedicationDatabase creates a new medication database instance
func NewMedicationDatabase(dbHelper DatabaseHelper) MedicationDatabase {
	return &medicationDatabase{
		collection: dbHelper.Collection("medications"),
	}
}

// GetMedicationsByCivilianID retrieves medications for a specific civilian with pagination
func (m *medicationDatabase) GetMedicationsByCivilianID(ctx context.Context, civilianID, activeCommunityID string, limit, page int64) (*models.MedicationResponse, error) {
	// Build filter
	filter := bson.M{
		"medication.civilianID":        civilianID,
		"medication.activeCommunityID": activeCommunityID,
	}

	// Calculate skip value for pagination
	skip := page * limit

	// Set up aggregation pipeline
	pipeline := []bson.M{
		{"$match": filter},
		{"$project": bson.M{
			"_id":                1,
			"startDate":          "$medication.startDate",
			"name":               "$medication.name",
			"dosage":             "$medication.dosage",
			"frequency":          "$medication.frequency",
			"civilianID":         "$medication.civilianID",
			"activeCommunityID":  "$medication.activeCommunityID",
			"userID":             "$medication.userID",
			"firstName":          "$medication.firstName",
			"lastName":           "$medication.lastName",
			"dateOfBirth":        "$medication.dateOfBirth",
			"createdAt":          "$medication.createdAt",
			"updatedAt":          "$medication.updatedAt",
		}},
		{"$sort": bson.M{"createdAt": -1}}, // Sort by creation date, newest first
		{"$skip": skip},
		{"$limit": limit},
	}

	// Execute aggregation
	cursor, err := m.collection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	// Decode results
	var medications []models.MedicationWithDetails
	if err := cursor.All(ctx, &medications); err != nil {
		return nil, err
	}

	// Get total count for pagination
	totalCount, err := m.collection.CountDocuments(ctx, filter)
	if err != nil {
		return nil, err
	}

	// Calculate pagination info
	totalPages := (totalCount + limit - 1) / limit

	response := &models.MedicationResponse{
		Medications: medications,
		Pagination: models.Pagination{
			CurrentPage:  page,
			TotalPages:   totalPages,
			TotalRecords: totalCount,
			Limit:        limit,
		},
	}

	return response, nil
}

// GetMedicationByID retrieves a single medication by ID
func (m *medicationDatabase) GetMedicationByID(ctx context.Context, id string) (*models.Medication, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, err
	}

	filter := bson.M{"_id": objectID}
	
	var medication models.Medication
	err = m.collection.FindOne(ctx, filter).Decode(&medication)
	if err != nil {
		return nil, err
	}

	return &medication, nil
}

// CreateMedication creates a new medication
func (m *medicationDatabase) CreateMedication(ctx context.Context, medication *models.Medication) error {
	// Set creation and update timestamps
	now := primitive.NewDateTimeFromTime(time.Now())
	medication.Medication.CreatedAt = now
	medication.Medication.UpdatedAt = now
	
	// Generate new ObjectID if not provided
	if medication.ID.IsZero() {
		medication.ID = primitive.NewObjectID()
	}

	_, err := m.collection.InsertOne(ctx, medication)
	return err
}

// UpdateMedication updates an existing medication
func (m *medicationDatabase) UpdateMedication(ctx context.Context, id string, medication *models.Medication) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return err
	}

	filter := bson.M{"_id": objectID}
	
	// Update the timestamp
	medication.Medication.UpdatedAt = primitive.NewDateTimeFromTime(time.Now())
	
	// Create update document
	update := bson.M{
		"$set": bson.M{
			"medication": medication.Medication,
		},
	}

	_, err = m.collection.UpdateOne(ctx, filter, update)
	return err
}

// DeleteMedication deletes a medication by ID
func (m *medicationDatabase) DeleteMedication(ctx context.Context, id string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return err
	}

	filter := bson.M{"_id": objectID}
	
	return m.collection.DeleteOne(ctx, filter)
}
