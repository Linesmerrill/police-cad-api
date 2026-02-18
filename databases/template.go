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

// TemplateDatabase handles template-related database operations
type TemplateDatabase struct {
	Collection MongoCollectionHelper
}

// NewTemplateDatabase creates a new template database instance
func NewTemplateDatabase(db DatabaseHelper) *TemplateDatabase {
	return &TemplateDatabase{
		Collection: db.Collection("templates"),
	}
}

// InsertOne inserts a new template into the database
func (t *TemplateDatabase) InsertOne(ctx context.Context, template models.GlobalTemplate) (InsertOneResultHelper, error) {
	return t.Collection.InsertOne(ctx, template)
}

// FindOne finds a single template by ID
func (t *TemplateDatabase) FindOne(ctx context.Context, filter bson.M) (models.GlobalTemplate, error) {
	var template models.GlobalTemplate
	err := t.Collection.FindOne(ctx, filter).Decode(&template)
	return template, err
}

// FindMany finds multiple templates based on filter
func (t *TemplateDatabase) FindMany(ctx context.Context, filter bson.M, opts ...*options.FindOptions) ([]models.GlobalTemplate, error) {
	var templates []models.GlobalTemplate
	cursor, err := t.Collection.Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	err = cursor.All(ctx, &templates)
	return templates, err
}

// UpdateOne updates a single template
func (t *TemplateDatabase) UpdateOne(ctx context.Context, filter bson.M, update bson.M) (*mongo.UpdateResult, error) {
	return t.Collection.UpdateOne(ctx, filter, update)
}

// DeleteOne deletes a single template
func (t *TemplateDatabase) DeleteOne(ctx context.Context, filter bson.M) error {
	return t.Collection.DeleteOne(ctx, filter)
}

// GetDefaultTemplates returns all default templates
func (t *TemplateDatabase) GetDefaultTemplates(ctx context.Context) ([]models.GlobalTemplate, error) {
	filter := bson.M{"isDefault": true, "isActive": true}
	return t.FindMany(ctx, filter)
}

// GetTemplatesByCategory returns templates filtered by category
func (t *TemplateDatabase) GetTemplatesByCategory(ctx context.Context, category string) ([]models.GlobalTemplate, error) {
	filter := bson.M{"category": category, "isActive": true}
	return t.FindMany(ctx, filter)
}

// GetActiveTemplates returns all active templates
func (t *TemplateDatabase) GetActiveTemplates(ctx context.Context) ([]models.GlobalTemplate, error) {
	filter := bson.M{"isActive": true}
	return t.FindMany(ctx, filter)
}

// CreateDefaultTemplates creates the default template set if they don't exist
func (t *TemplateDatabase) CreateDefaultTemplates(ctx context.Context, componentDB *ComponentDatabase) error {
	// Check if default templates already exist
	count, err := t.Collection.CountDocuments(ctx, bson.M{"isDefault": true})
	if err != nil {
		return err
	}

	if count > 0 {
		return nil // Default templates already exist
	}

	// First, ensure components exist
	err = componentDB.CreateDefaultComponents(ctx)
	if err != nil {
		return err
	}

	// Get all active components to reference them
	components, err := componentDB.GetActiveComponents(ctx)
	if err != nil {
		return err
	}

	// Create a map of component names to IDs for easy lookup
	componentMap := make(map[string]primitive.ObjectID)
	for _, comp := range components {
		componentMap[comp.Name] = comp.ID
	}

	// Helper function to create component references
	createComponentRefs := func(componentNames []string, enabled bool) []models.TemplateComponentReference {
		var refs []models.TemplateComponentReference
		for i, name := range componentNames {
			if componentID, exists := componentMap[name]; exists {
				refs = append(refs, models.TemplateComponentReference{
					ComponentID: componentID,
					Enabled:     enabled,
					Settings:    make(map[string]interface{}),
					Order:       i,
				})
			}
		}
		return refs
	}

	// Create default police template
	policeTemplate := models.GlobalTemplate{
		ID:          primitive.NewObjectID(),
		Name:        "Police",
		Description: "Default template for police departments",
		Category:    "police",
		IsDefault:   true,
		IsActive:    true,
		Components: createComponentRefs([]string{
			"10CodesInterface",
			"personSearch",
			"vehicleSearch",
			"firearmSearch",
			"createBolos",
			"viewBolosAndWarrants",
			"notepad",
		}, true),
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
		UpdatedAt: primitive.NewDateTimeFromTime(time.Now()),
		CreatedBy: "system",
	}

	// Create default EMS template
	emsTemplate := models.GlobalTemplate{
		ID:          primitive.NewObjectID(),
		Name:        "EMS",
		Description: "Default template for EMS departments",
		Category:    "ems",
		IsDefault:   true,
		IsActive:    true,
		Components: createComponentRefs([]string{
			"10CodesInterface",
			"medicalDatabase",
			"vehicleSearch",
			"createBolos",
			"viewBolosAndWarrants",
			"notepad",
		}, true),
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
		UpdatedAt: primitive.NewDateTimeFromTime(time.Now()),
		CreatedBy: "system",
	}

	// Create default Fire template
	fireTemplate := models.GlobalTemplate{
		ID:          primitive.NewObjectID(),
		Name:        "Fire",
		Description: "Default template for fire departments",
		Category:    "fire",
		IsDefault:   true,
		IsActive:    true,
		Components: createComponentRefs([]string{
			"10CodesInterface",
			"medicalDatabase",
			"vehicleSearch",
			"createBolos",
			"viewBolosAndWarrants",
			"notepad",
		}, true),
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
		UpdatedAt: primitive.NewDateTimeFromTime(time.Now()),
		CreatedBy: "system",
	}

	// Create default Dispatch template
	dispatchTemplate := models.GlobalTemplate{
		ID:          primitive.NewObjectID(),
		Name:        "Dispatch",
		Description: "Default template for dispatch departments",
		Category:    "dispatch",
		IsDefault:   true,
		IsActive:    true,
		Components: createComponentRefs([]string{
			"dispatchUnits",
			"createAndManageCalls",
			"createBolos",
			"manage911Calls",
			"nameSearch",
			"vehicleSearch",
			"firearmSearch",
		}, true),
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
		UpdatedAt: primitive.NewDateTimeFromTime(time.Now()),
		CreatedBy: "system",
	}

	// Create default Civilian template
	civilianTemplate := models.GlobalTemplate{
		ID:          primitive.NewObjectID(),
		Name:        "Civilian",
		Description: "Default template for civilian departments",
		Category:    "civilian",
		IsDefault:   true,
		IsActive:    true,
		Components: createComponentRefs([]string{
			"createCivilians",
			"createVehicles",
			"createFirearms",
			"call911",
			"notepad",
		}, false), // Most civilian components are disabled by default
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
		UpdatedAt: primitive.NewDateTimeFromTime(time.Now()),
		CreatedBy: "system",
	}

	// Override call911 and notepad to be enabled for civilian template
	for i := range civilianTemplate.Components {
		compID := civilianTemplate.Components[i].ComponentID
		if compID == componentMap["call911"] || compID == componentMap["notepad"] {
			civilianTemplate.Components[i].Enabled = true
		}
	}

	// Create default Judicial template
	judicialTemplate := models.GlobalTemplate{
		ID:          primitive.NewObjectID(),
		Name:        "Judicial",
		Description: "Default template for judicial departments (judges, magistrates)",
		Category:    "judicial",
		IsDefault:   true,
		IsActive:    true,
		Components: createComponentRefs([]string{
			"reviewWarrants",
			"10CodesInterface",
			"notepad",
		}, true),
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
		UpdatedAt: primitive.NewDateTimeFromTime(time.Now()),
		CreatedBy: "system",
	}

	// Insert all default templates
	_, err = t.Collection.InsertOne(ctx, policeTemplate)
	if err != nil {
		return err
	}
	_, err = t.Collection.InsertOne(ctx, emsTemplate)
	if err != nil {
		return err
	}
	_, err = t.Collection.InsertOne(ctx, fireTemplate)
	if err != nil {
		return err
	}
	_, err = t.Collection.InsertOne(ctx, dispatchTemplate)
	if err != nil {
		return err
	}
	_, err = t.Collection.InsertOne(ctx, civilianTemplate)
	if err != nil {
		return err
	}
	_, err = t.Collection.InsertOne(ctx, judicialTemplate)
	return err
}
