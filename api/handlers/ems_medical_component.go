package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// EMSMedicalComponent handles EMS medical database component operations
type EMSMedicalComponent struct {
	ComponentDB *databases.ComponentDatabase
	TemplateDB  *databases.TemplateDatabase
	CommunityDB databases.CommunityDatabase
}

// NewEMSMedicalComponent creates a new EMS medical component handler
func NewEMSMedicalComponent(componentDB *databases.ComponentDatabase, templateDB *databases.TemplateDatabase, communityDB databases.CommunityDatabase) *EMSMedicalComponent {
	return &EMSMedicalComponent{
		ComponentDB: componentDB,
		TemplateDB:  templateDB,
		CommunityDB: communityDB,
	}
}

// GetEMSMedicalComponentHandler returns the medicalDatabase component for EMS templates
func (e *EMSMedicalComponent) GetEMSMedicalComponentHandler(w http.ResponseWriter, r *http.Request) {
	// Find the medicalDatabase component
	component, err := e.ComponentDB.FindOne(context.Background(), bson.M{"name": "medicalDatabase"})
	if err != nil {
		config.ErrorStatus("medical database component not found", http.StatusNotFound, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"component": component,
		"message":   "Medical database component retrieved successfully",
	})
}

// GetEMSComponentsWithPaginationHandler returns all components with pagination, highlighting medicalDatabase
func (e *EMSMedicalComponent) GetEMSComponentsWithPaginationHandler(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	pageStr := r.URL.Query().Get("page")
	limitStr := r.URL.Query().Get("limit")
	category := r.URL.Query().Get("category")
	includeInactive := r.URL.Query().Get("includeInactive") == "true"

	// Set up pagination
	page := 1
	limit := 50
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	// Set up filter
	filter := bson.M{}
	if category != "" {
		filter["category"] = category
	}
	if !includeInactive {
		filter["isActive"] = true
	}

	skip := (page - 1) * limit
	opts := []*options.FindOptions{
		options.Find().SetSkip(int64(skip)).SetLimit(int64(limit)),
		options.Find().SetSort(bson.D{{"category", 1}, {"name", 1}}),
	}

	// Find components
	components, err := e.ComponentDB.FindMany(context.Background(), filter, opts...)
	if err != nil {
		config.ErrorStatus("failed to retrieve components", http.StatusInternalServerError, w, err)
		return
	}

	// Get total count for pagination
	totalCount, err := e.ComponentDB.Collection.CountDocuments(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to count components", http.StatusInternalServerError, w, err)
		return
	}

	// Mark medicalDatabase component as special
	for i, comp := range components {
		if comp.Name == "medicalDatabase" {
			// Add a special flag for frontend highlighting
			components[i].Metadata = map[string]interface{}{
				"isNewComponent": true,
				"isEMSSpecific":  true,
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"components": components,
		"pagination": map[string]interface{}{
			"page":       page,
			"limit":      limit,
			"total":      totalCount,
			"totalPages": (totalCount + int64(limit) - 1) / int64(limit),
		},
		"message": "Components retrieved successfully with pagination",
	})
}

// GetEMSTemplateWithMedicalComponentHandler returns the EMS template with the medicalDatabase component resolved
func (e *EMSMedicalComponent) GetEMSTemplateWithMedicalComponentHandler(w http.ResponseWriter, r *http.Request) {
	// Find the EMS template
	template, err := e.TemplateDB.FindOne(context.Background(), bson.M{"name": "EMS", "isDefault": true})
	if err != nil {
		config.ErrorStatus("EMS template not found", http.StatusNotFound, w, err)
		return
	}

	// Get all components referenced by this template
	var componentIDs []primitive.ObjectID
	for _, compRef := range template.Components {
		componentIDs = append(componentIDs, compRef.ComponentID)
	}

	// Fetch the actual components
	components, err := e.ComponentDB.GetComponentsByIDs(context.Background(), componentIDs)
	if err != nil {
		config.ErrorStatus("failed to fetch template components", http.StatusInternalServerError, w, err)
		return
	}

	// Create a map of component ID to component for easy lookup
	componentMap := make(map[primitive.ObjectID]models.GlobalComponent)
	for _, comp := range components {
		componentMap[comp.ID] = comp
	}

	// Build the response with resolved components
	type ResolvedComponent struct {
		Component models.GlobalComponent `json:"component"`
		Enabled   bool                   `json:"enabled"`
		Settings  map[string]interface{} `json:"settings"`
		Order     int                    `json:"order"`
	}

	var resolvedComponents []ResolvedComponent
	for _, compRef := range template.Components {
		if comp, exists := componentMap[compRef.ComponentID]; exists {
			resolvedComponents = append(resolvedComponents, ResolvedComponent{
				Component: comp,
				Enabled:   compRef.Enabled,
				Settings:  compRef.Settings,
				Order:     compRef.Order,
			})
		}
	}

	// Count enabled and disabled components
	enabledCount := 0
	disabledCount := 0
	for _, comp := range resolvedComponents {
		if comp.Enabled {
			enabledCount++
		} else {
			disabledCount++
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"template": map[string]interface{}{
			"id":          template.ID,
			"name":        template.Name,
			"description": template.Description,
			"category":    template.Category,
			"isDefault":   template.IsDefault,
			"isActive":    template.IsActive,
			"components":  resolvedComponents,
			"createdAt":   template.CreatedAt,
			"updatedAt":   template.UpdatedAt,
			"createdBy":   template.CreatedBy,
		},
		"componentStats": map[string]interface{}{
			"total":    len(resolvedComponents),
			"enabled":  enabledCount,
			"disabled": disabledCount,
		},
		"message": "EMS template with resolved components retrieved successfully",
	})
}

// UpdateEMSMedicalComponentHandler enables/disables the medicalDatabase component for a specific department
func (e *EMSMedicalComponent) UpdateEMSMedicalComponentHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	// Convert IDs to ObjectIDs
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	dID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		config.ErrorStatus("invalid department ID", http.StatusBadRequest, w, err)
		return
	}

	// Parse request body
	var requestData struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Find the medicalDatabase component ID
	medicalComponent, err := e.ComponentDB.FindOne(context.Background(), bson.M{"name": "medicalDatabase"})
	if err != nil {
		config.ErrorStatus("medical database component not found", http.StatusNotFound, w, err)
		return
	}

	// Find the community
	community, err := e.CommunityDB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	// Find the department
	var targetDepartment *models.Department
	for i, dept := range community.Details.Departments {
		if dept.ID == dID {
			targetDepartment = &community.Details.Departments[i]
			break
		}
	}

	if targetDepartment == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, err)
		return
	}

	// Update the department's template reference for the medicalDatabase component
	if targetDepartment.TemplateRef == nil {
		// If no template reference exists, create one
		targetDepartment.TemplateRef = &models.TemplateReference{
			TemplateID:     primitive.NilObjectID, // Will be set when template is assigned
			Customizations: make(map[string]models.ComponentOverride),
			IsActive:       true,
		}
	}

	// Update the medicalDatabase component customization
	targetDepartment.TemplateRef.Customizations[medicalComponent.ID.Hex()] = models.ComponentOverride{
		Enabled: requestData.Enabled,
	}

	// Update the department in the database
	filter := bson.M{"_id": cID, "community.departments._id": dID}
	update := bson.M{
		"$set": bson.M{
			"community.departments.$.templateRef": targetDepartment.TemplateRef,
		},
	}

	err = e.CommunityDB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update department medical component", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Medical database component updated successfully",
		"enabled": requestData.Enabled,
	})
}

// GetDepartmentMedicalComponentStatusHandler returns the current status of the medicalDatabase component for a department
func (e *EMSMedicalComponent) GetDepartmentMedicalComponentStatusHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	// Convert IDs to ObjectIDs
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	dID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		config.ErrorStatus("invalid department ID", http.StatusBadRequest, w, err)
		return
	}

	// Find the medicalDatabase component
	medicalComponent, err := e.ComponentDB.FindOne(context.Background(), bson.M{"name": "medicalDatabase"})
	if err != nil {
		config.ErrorStatus("medical database component not found", http.StatusNotFound, w, err)
		return
	}

	// Find the community and department
	community, err := e.CommunityDB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	var targetDepartment *models.Department
	for _, dept := range community.Details.Departments {
		if dept.ID == dID {
			targetDepartment = &dept
			break
		}
	}

	if targetDepartment == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, err)
		return
	}

	// Check if the medicalDatabase component is enabled
	enabled := false
	if targetDepartment.TemplateRef != nil {
		if override, exists := targetDepartment.TemplateRef.Customizations[medicalComponent.ID.Hex()]; exists {
			enabled = override.Enabled
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"component": map[string]interface{}{
			"id":          medicalComponent.ID,
			"name":        medicalComponent.Name,
			"displayName": medicalComponent.DisplayName,
			"description": medicalComponent.Description,
			"category":    medicalComponent.Category,
		},
		"enabled": enabled,
		"message": "Medical database component status retrieved successfully",
	})
}
