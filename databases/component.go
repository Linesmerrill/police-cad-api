package databases

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/models"
)

// ComponentDatabase handles component-related database operations
type ComponentDatabase struct {
	Collection MongoCollectionHelper
}

// NewComponentDatabase creates a new component database instance
func NewComponentDatabase(db DatabaseHelper) *ComponentDatabase {
	return &ComponentDatabase{
		Collection: db.Collection("components"),
	}
}

// InsertOne inserts a new component into the database
func (c *ComponentDatabase) InsertOne(ctx context.Context, component models.GlobalComponent) (InsertOneResultHelper, error) {
	return c.Collection.InsertOne(ctx, component)
}

// FindOne finds a single component by ID
func (c *ComponentDatabase) FindOne(ctx context.Context, filter bson.M) (models.GlobalComponent, error) {
	var component models.GlobalComponent
	err := c.Collection.FindOne(ctx, filter).Decode(&component)
	return component, err
}

// FindMany finds multiple components based on filter
func (c *ComponentDatabase) FindMany(ctx context.Context, filter bson.M, opts ...*options.FindOptions) ([]models.GlobalComponent, error) {
	var components []models.GlobalComponent
	cursor, err := c.Collection.Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	err = cursor.All(ctx, &components)
	return components, err
}

// UpdateOne updates a single component
func (c *ComponentDatabase) UpdateOne(ctx context.Context, filter bson.M, update bson.M) (*mongo.UpdateResult, error) {
	return c.Collection.UpdateOne(ctx, filter, update)
}

// DeleteOne deletes a single component
func (c *ComponentDatabase) DeleteOne(ctx context.Context, filter bson.M) error {
	return c.Collection.DeleteOne(ctx, filter)
}

// GetActiveComponents returns all active components
func (c *ComponentDatabase) GetActiveComponents(ctx context.Context) ([]models.GlobalComponent, error) {
	filter := bson.M{"isActive": true}
	return c.FindMany(ctx, filter, options.Find().SetSort(bson.D{{"category", 1}, {"name", 1}}))
}

// GetComponentsByCategory returns components filtered by category
func (c *ComponentDatabase) GetComponentsByCategory(ctx context.Context, category string) ([]models.GlobalComponent, error) {
	filter := bson.M{"category": category, "isActive": true}
	return c.FindMany(ctx, filter, options.Find().SetSort(bson.M{"name": 1}))
}

// GetComponentsByIDs returns components by their IDs (for template resolution)
func (c *ComponentDatabase) GetComponentsByIDs(ctx context.Context, componentIDs []primitive.ObjectID) ([]models.GlobalComponent, error) {
	filter := bson.M{"_id": bson.M{"$in": componentIDs}, "isActive": true}
	return c.FindMany(ctx, filter, options.Find().SetSort(bson.M{"name": 1}))
}

// CreateDefaultComponents creates the default component set if they don't exist
func (c *ComponentDatabase) CreateDefaultComponents(ctx context.Context) error {
	// Check if default components already exist
	count, err := c.Collection.CountDocuments(ctx, bson.M{})
	if err != nil {
		return err
	}

	if count > 0 {
		return nil // Default components already exist
	}

	// Create all the components from your real data
	components := []models.GlobalComponent{
		// Communication Components
		{
			ID:          primitive.NewObjectID(),
			Name:        "10CodesInterface",
			DisplayName: "10-Codes Interface",
			Description: "10-codes interface for department communication",
			Category:    "communication",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "notepad",
			DisplayName: "Digital Notepad",
			Description: "Digital notepad for notes and reports",
			Category:    "productivity",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},

		// Search Components
		{
			ID:          primitive.NewObjectID(),
			Name:        "personSearch",
			DisplayName: "Person Search",
			Description: "Search for persons in the database",
			Category:    "search",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "vehicleSearch",
			DisplayName: "Vehicle Search",
			Description: "Search for vehicles in the database",
			Category:    "search",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "firearmSearch",
			DisplayName: "Firearm Search",
			Description: "Search for firearms in the database",
			Category:    "search",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "nameSearch",
			DisplayName: "Name Search",
			Description: "Search for persons by name",
			Category:    "search",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},

		// BOLO Components
		{
			ID:          primitive.NewObjectID(),
			Name:        "createBolos",
			DisplayName: "Create BOLOs",
			Description: "Create BOLO (Be On The Lookout) alerts",
			Category:    "alerts",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "viewBolosAndWarrants",
			DisplayName: "View BOLOs & Warrants",
			Description: "View BOLOs and warrants",
			Category:    "alerts",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},

		// Dispatch Components
		{
			ID:          primitive.NewObjectID(),
			Name:        "dispatchUnits",
			DisplayName: "Dispatch Units",
			Description: "Dispatch units to calls and incidents",
			Category:    "dispatch",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "createAndManageCalls",
			DisplayName: "Create & Manage Calls",
			Description: "Create and manage emergency calls",
			Category:    "dispatch",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "manage911Calls",
			DisplayName: "Manage 911 Calls",
			Description: "Manage 911 emergency calls",
			Category:    "dispatch",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},

		// Civilian Components
		{
			ID:          primitive.NewObjectID(),
			Name:        "createCivilians",
			DisplayName: "Create Civilians",
			Description: "Create civilian records",
			Category:    "records",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "createVehicles",
			DisplayName: "Create Vehicles",
			Description: "Create vehicle records",
			Category:    "records",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "createFirearms",
			DisplayName: "Create Firearms",
			Description: "Create firearm records",
			Category:    "records",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "call911",
			DisplayName: "Call 911",
			Description: "Call 911 emergency services",
			Category:    "emergency",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},

		// Medical Components
		{
			ID:          primitive.NewObjectID(),
			Name:        "medicalDatabase",
			DisplayName: "Medical Database",
			Description: "Medical database for medical information",
			Category:    "medical",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},

		// Judicial Components
		{
			ID:          primitive.NewObjectID(),
			Name:        "reviewWarrants",
			DisplayName: "Review Warrants",
			Description: "Review and approve or deny warrant requests submitted by officers",
			Category:    "judicial",
			Type:        "feature",
			Version:     "1.0.0",
			IsActive:    true,
			IsRequired:  false,
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt:   primitive.NewDateTimeFromTime(time.Now()),
			CreatedBy:   "system",
		},
	}

	// Insert all components
	for _, component := range components {
		_, err = c.Collection.InsertOne(ctx, component)
		if err != nil {
			return err
		}
	}

	return nil
}
