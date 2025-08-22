package databases

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"
)

// MedicalReportDatabase defines the interface for medical report database operations
type MedicalReportDatabase interface {
	GetMedicalReportsByCivilianID(ctx context.Context, civilianID, activeCommunityID string, limit, page int64) (*models.MedicalReportResponse, error)
	GetMedicalReportByID(ctx context.Context, id string) (*models.MedicalReport, error)
	CreateMedicalReport(ctx context.Context, medicalReport *models.MedicalReport) error
	UpdateMedicalReport(ctx context.Context, id string, medicalReport *models.MedicalReport) error
	DeleteMedicalReport(ctx context.Context, id string) error
}

// medicalReportDatabase implements MedicalReportDatabase
type medicalReportDatabase struct {
	collection MongoCollectionHelper
}

// NewMedicalReportDatabase creates a new medical report database instance
func NewMedicalReportDatabase(dbHelper DatabaseHelper) MedicalReportDatabase {
	return &medicalReportDatabase{
		collection: dbHelper.Collection("medicalreports"),
	}
}

// GetMedicalReportsByCivilianID retrieves medical reports for a specific civilian with pagination
func (m *medicalReportDatabase) GetMedicalReportsByCivilianID(ctx context.Context, civilianID, activeCommunityID string, limit, page int64) (*models.MedicalReportResponse, error) {
	// Build filter
	filter := bson.M{
		"report.civilianID":        civilianID,
		"report.activeCommunityID": activeCommunityID,
	}

	// Calculate skip value for pagination
	skip := page * limit

	// Set up aggregation pipeline to join with EMS data
	pipeline := []bson.M{
		{"$match": filter},
		{"$lookup": bson.M{
			"from":         "ems",
			"localField":   "report.reportingEmsID",
			"foreignField": "_id",
			"as":           "reportingEms",
		}},
		{"$unwind": bson.M{
			"path":                       "$reportingEms",
			"preserveNullAndEmptyArrays": true,
		}},
		{"$project": bson.M{
			"_id":                1,
			"civilianID":         "$report.civilianID",
			"reportingEmsID":     "$report.reportingEmsID",
			"reportDate":         "$report.date",
			"reportTime":         bson.M{"$substr": []interface{}{"$report.date", 11, 5}}, // Extract time portion
			"hospitalized":       bson.M{"$cond": []interface{}{"$report.hospitalized", "yes", "no"}},
			"deceased":           "$report.deceased",
			"details":            "$report.details",
			"activeCommunityID":  "$report.activeCommunityID",
			"createdAt":          "$report.createdAt",
			"updatedAt":          "$report.updatedAt",
			"reportingEms": bson.M{
				"name":       bson.M{"$concat": []string{"$reportingEms.ems.firstName", " ", "$reportingEms.ems.lastName"}},
				"department": "$reportingEms.ems.department",
			},
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
	var medicalReports []models.MedicalReportWithEms
	if err := cursor.All(ctx, &medicalReports); err != nil {
		return nil, err
	}

	// Get total count for pagination
	totalCount, err := m.collection.CountDocuments(ctx, filter)
	if err != nil {
		return nil, err
	}

	// Calculate pagination info
	totalPages := (totalCount + limit - 1) / limit

	response := &models.MedicalReportResponse{
		MedicalReports: medicalReports,
		Pagination: models.Pagination{
			CurrentPage:  page,
			TotalPages:   totalPages,
			TotalRecords: totalCount,
			Limit:        limit,
		},
	}

	return response, nil
}

// GetMedicalReportByID retrieves a single medical report by ID
func (m *medicalReportDatabase) GetMedicalReportByID(ctx context.Context, id string) (*models.MedicalReport, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, err
	}

	filter := bson.M{"_id": objectID}
	
	var medicalReport models.MedicalReport
	err = m.collection.FindOne(ctx, filter).Decode(&medicalReport)
	if err != nil {
		return nil, err
	}

	return &medicalReport, nil
}

// CreateMedicalReport creates a new medical report
func (m *medicalReportDatabase) CreateMedicalReport(ctx context.Context, medicalReport *models.MedicalReport) error {
	// Set creation and update timestamps
	now := primitive.NewDateTimeFromTime(time.Now())
	medicalReport.Report.CreatedAt = now
	medicalReport.Report.UpdatedAt = now
	
	// Generate new ObjectID if not provided
	if medicalReport.ID.IsZero() {
		medicalReport.ID = primitive.NewObjectID()
	}

	_, err := m.collection.InsertOne(ctx, medicalReport)
	return err
}

// UpdateMedicalReport updates an existing medical report
func (m *medicalReportDatabase) UpdateMedicalReport(ctx context.Context, id string, medicalReport *models.MedicalReport) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return err
	}

	filter := bson.M{"_id": objectID}
	
	// Update the timestamp
	medicalReport.Report.UpdatedAt = primitive.NewDateTimeFromTime(time.Now())
	
	// Create update document
	update := bson.M{
		"$set": bson.M{
			"report": medicalReport.Report,
		},
	}

	_, err = m.collection.UpdateOne(ctx, filter, update)
	return err
}

// DeleteMedicalReport deletes a medical report by ID
func (m *medicalReportDatabase) DeleteMedicalReport(ctx context.Context, id string) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return err
	}

	filter := bson.M{"_id": objectID}
	
	return m.collection.DeleteOne(ctx, filter)
}
