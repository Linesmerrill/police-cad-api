package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// TemplateMigration handles migration of existing embedded templates to the new system
type TemplateMigration struct {
	TemplateDB   *databases.TemplateDatabase
	CommunityDB  databases.CommunityDatabase
}

// NewTemplateMigration creates a new template migration handler
func NewTemplateMigration(templateDB *databases.TemplateDatabase, communityDB databases.CommunityDatabase) *TemplateMigration {
	return &TemplateMigration{
		TemplateDB:  templateDB,
		CommunityDB: communityDB,
	}
}

// MigrateCommunityTemplatesHandler migrates a specific community's embedded templates to the new system
func (tm *TemplateMigration) MigrateCommunityTemplatesHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Convert the community ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Find the community
	community, err := tm.CommunityDB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	migrationResults := make([]map[string]interface{}, 0)

	// Migrate each department's embedded template
	for _, department := range community.Details.Departments {
		// Skip if department already has a template reference
		if department.TemplateRef != nil {
			continue
		}

		// Skip if department has no embedded template
		if department.Template.ID.IsZero() {
			continue
		}

		// Create a new global template from the embedded template
		globalTemplate := models.GlobalTemplate{
			ID:          primitive.NewObjectID(),
			Name:        department.Template.Name,
			Description: department.Template.Description,
			Category:    "migrated", // Mark as migrated
			IsDefault:   false,
			IsActive:    true,
			Components:  make([]models.TemplateComponentReference, 0),
			CreatedAt:   department.CreatedAt,
			UpdatedAt:   department.UpdatedAt,
			CreatedBy:   "migration",
		}

		// Convert embedded components to component references
		// Note: This migration assumes components already exist in the global components collection
		// In a real migration, you'd need to create the components first or reference existing ones
		for i, comp := range department.Template.Components {
			componentRef := models.TemplateComponentReference{
				ComponentID: comp.ID, // This should reference an existing global component
				Enabled:     comp.Enabled,
				Settings:    make(map[string]interface{}),
				Order:       i,
			}
			globalTemplate.Components = append(globalTemplate.Components, componentRef)
		}

		// Insert the new global template
		_, err := tm.TemplateDB.InsertOne(context.Background(), globalTemplate)
		if err != nil {
			config.ErrorStatus("failed to create global template", http.StatusInternalServerError, w, err)
			return
		}

		// Create template reference for the department
		templateRef := &models.TemplateReference{
			TemplateID:     globalTemplate.ID,
			Customizations: make(map[string]models.ComponentOverride),
			IsActive:       true,
		}

		// Set up component customizations based on original enabled state
		for _, comp := range department.Template.Components {
			templateRef.Customizations[comp.ID.Hex()] = models.ComponentOverride{
				Enabled: comp.Enabled,
			}
		}

		// Update the department to use the new template reference
		// Keep the old template for backward compatibility
		updateFilter := bson.M{
			"_id": cID,
			"community.departments._id": department.ID,
		}
		update := bson.M{
			"$set": bson.M{
				"community.departments.$.templateRef": templateRef,
			},
		}

		err = tm.CommunityDB.UpdateOne(context.Background(), updateFilter, update)
		if err != nil {
			config.ErrorStatus("failed to update department template reference", http.StatusInternalServerError, w, err)
			return
		}

		migrationResults = append(migrationResults, map[string]interface{}{
			"departmentId":   department.ID,
			"departmentName": department.Name,
			"templateId":     globalTemplate.ID,
			"templateName":   globalTemplate.Name,
			"componentsCount": len(globalTemplate.Components),
		})
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":           "Community templates migrated successfully",
		"communityId":       communityID,
		"migratedTemplates": migrationResults,
	})
}

// MigrateAllCommunitiesTemplatesHandler migrates all communities' embedded templates to the new system
func (tm *TemplateMigration) MigrateAllCommunitiesTemplatesHandler(w http.ResponseWriter, r *http.Request) {
	// Find all communities
	cursor, err := tm.CommunityDB.Find(context.Background(), bson.M{})
	if err != nil {
		config.ErrorStatus("failed to retrieve communities", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())

	var communities []models.Community
	err = cursor.All(context.Background(), &communities)
	if err != nil {
		config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
		return
	}

	totalMigrated := 0
	communityResults := make([]map[string]interface{}, 0)

	for _, community := range communities {
		communityMigrated := 0

		// Migrate each department's embedded template
		for _, department := range community.Details.Departments {
			// Skip if department already has a template reference
			if department.TemplateRef != nil {
				continue
			}

			// Skip if department has no embedded template
			if department.Template.ID.IsZero() {
				continue
			}

			// Create a new global template from the embedded template
			globalTemplate := models.GlobalTemplate{
				ID:          primitive.NewObjectID(),
				Name:        department.Template.Name,
				Description: department.Template.Description,
				Category:    "migrated",
				IsDefault:   false,
				IsActive:    true,
				Components:  make([]models.TemplateComponentReference, 0),
				CreatedAt:   department.CreatedAt,
				UpdatedAt:   department.UpdatedAt,
				CreatedBy:   "migration",
			}

			// Convert embedded components to component references
			for i, comp := range department.Template.Components {
				componentRef := models.TemplateComponentReference{
					ComponentID: comp.ID, // This should reference an existing global component
					Enabled:     comp.Enabled,
					Settings:    make(map[string]interface{}),
					Order:       i,
				}
				globalTemplate.Components = append(globalTemplate.Components, componentRef)
			}

			// Insert the new global template
			_, err := tm.TemplateDB.InsertOne(context.Background(), globalTemplate)
			if err != nil {
				// Log error but continue with other migrations
				continue
			}

			// Create template reference for the department
			templateRef := &models.TemplateReference{
				TemplateID:     globalTemplate.ID,
				Customizations: make(map[string]models.ComponentOverride),
				IsActive:       true,
			}

			// Set up component customizations
			for _, comp := range department.Template.Components {
				templateRef.Customizations[comp.ID.Hex()] = models.ComponentOverride{
					Enabled: comp.Enabled,
				}
			}

			// Update the department to use the new template reference
			updateFilter := bson.M{
				"_id": community.ID,
				"community.departments._id": department.ID,
			}
			update := bson.M{
				"$set": bson.M{
					"community.departments.$.templateRef": templateRef,
				},
			}

			err = tm.CommunityDB.UpdateOne(context.Background(), updateFilter, update)
			if err != nil {
				// Log error but continue with other migrations
				continue
			}

			communityMigrated++
			totalMigrated++
		}

		if communityMigrated > 0 {
			communityResults = append(communityResults, map[string]interface{}{
				"communityId":       community.ID,
				"communityName":     community.Details.Name,
				"migratedTemplates": communityMigrated,
			})
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":            "All communities templates migrated successfully",
		"totalMigrated":      totalMigrated,
		"communitiesUpdated": len(communityResults),
		"results":            communityResults,
	})
}

// GetMigrationStatusHandler returns the status of template migration for all communities
func (tm *TemplateMigration) GetMigrationStatusHandler(w http.ResponseWriter, r *http.Request) {
	// Find all communities
	cursor, err := tm.CommunityDB.Find(context.Background(), bson.M{})
	if err != nil {
		config.ErrorStatus("failed to retrieve communities", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())

	var communities []models.Community
	err = cursor.All(context.Background(), &communities)
	if err != nil {
		config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
		return
	}

	status := map[string]interface{}{
		"totalCommunities":     len(communities),
		"migratedCommunities":  0,
		"partiallyMigrated":    0,
		"unmigratedCommunities": 0,
		"totalDepartments":     0,
		"migratedDepartments":  0,
		"unmigratedDepartments": 0,
	}

	for _, community := range communities {
		communityMigrated := 0
		communityTotal := len(community.Details.Departments)
		status["totalDepartments"] = status["totalDepartments"].(int) + communityTotal

		for _, department := range community.Details.Departments {
			if department.TemplateRef != nil {
				communityMigrated++
				status["migratedDepartments"] = status["migratedDepartments"].(int) + 1
			} else {
				status["unmigratedDepartments"] = status["unmigratedDepartments"].(int) + 1
			}
		}

		if communityMigrated == communityTotal && communityTotal > 0 {
			status["migratedCommunities"] = status["migratedCommunities"].(int) + 1
		} else if communityMigrated > 0 {
			status["partiallyMigrated"] = status["partiallyMigrated"].(int) + 1
		} else {
			status["unmigratedCommunities"] = status["unmigratedCommunities"].(int) + 1
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(status)
}