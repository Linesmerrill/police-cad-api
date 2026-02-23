package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Template handles template-related HTTP requests
type Template struct {
	DB           *databases.TemplateDatabase
	ComponentDB  *databases.ComponentDatabase
}

// NewTemplate creates a new template handler
func NewTemplate(db *databases.TemplateDatabase, componentDB *databases.ComponentDatabase) *Template {
	return &Template{DB: db, ComponentDB: componentDB}
}

// CreateTemplateHandler creates a new global template
func (t *Template) CreateTemplateHandler(w http.ResponseWriter, r *http.Request) {
	var newTemplate models.GlobalTemplate

	// Parse the request body
	if err := json.NewDecoder(r.Body).Decode(&newTemplate); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Generate a new _id for the template
	newTemplate.ID = primitive.NewObjectID()
	now := primitive.NewDateTimeFromTime(time.Now())
	newTemplate.CreatedAt = now
	newTemplate.UpdatedAt = now

	// Note: Components are now references to global components, not embedded objects
	// Component IDs should be provided in the request body

	// Insert the new template into the database
	_, err := t.DB.InsertOne(context.Background(), newTemplate)
	if err != nil {
		config.ErrorStatus("failed to create template", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":  "Template created successfully",
		"template": newTemplate,
	})
}

// GetTemplatesHandler retrieves all active templates with optional filtering
func (t *Template) GetTemplatesHandler(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	category := r.URL.Query().Get("category")
	includeInactive := r.URL.Query().Get("includeInactive") == "true"
	pageStr := r.URL.Query().Get("page")
	limitStr := r.URL.Query().Get("limit")

	// Set up filter
	filter := bson.M{}
	if category != "" {
		filter["category"] = category
	}
	if !includeInactive {
		filter["isActive"] = true
	}

	// Set up pagination
	page := 1
	limit := 20
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

	skip := (page - 1) * limit
	opts := []*options.FindOptions{
		options.Find().SetSkip(int64(skip)).SetLimit(int64(limit)),
		options.Find().SetSort(bson.M{"name": 1}),
	}

	// Find templates
	templates, err := t.DB.FindMany(context.Background(), filter, opts...)
	if err != nil {
		config.ErrorStatus("failed to retrieve templates", http.StatusInternalServerError, w, err)
		return
	}

	// Get total count for pagination
	totalCount, err := t.DB.Collection.CountDocuments(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to count templates", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"templates":   templates,
		"pagination": map[string]interface{}{
			"page":       page,
			"limit":      limit,
			"total":      totalCount,
			"totalPages": (totalCount + int64(limit) - 1) / int64(limit),
		},
	})
}

// GetTemplateHandler retrieves a specific template by ID
func (t *Template) GetTemplateHandler(w http.ResponseWriter, r *http.Request) {
	templateID := mux.Vars(r)["templateId"]

	// Convert the template ID to a primitive.ObjectID
	tID, err := primitive.ObjectIDFromHex(templateID)
	if err != nil {
		config.ErrorStatus("invalid template ID", http.StatusBadRequest, w, err)
		return
	}

	// Find the template by ID
	template, err := t.DB.FindOne(context.Background(), bson.M{"_id": tID})
	if err != nil {
		config.ErrorStatus("template not found", http.StatusNotFound, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(template)
}

// UpdateTemplateHandler updates an existing template
func (t *Template) UpdateTemplateHandler(w http.ResponseWriter, r *http.Request) {
	templateID := mux.Vars(r)["templateId"]

	// Convert the template ID to a primitive.ObjectID
	tID, err := primitive.ObjectIDFromHex(templateID)
	if err != nil {
		config.ErrorStatus("invalid template ID", http.StatusBadRequest, w, err)
		return
	}

	// Parse the request body
	var updateData models.GlobalTemplate
	if err := json.NewDecoder(r.Body).Decode(&updateData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Update the updatedAt field
	updateData.UpdatedAt = primitive.NewDateTimeFromTime(time.Now())

	// Note: Components are now references to global components, not embedded objects
	// Component updates are handled separately

	// Update the template in the database
	filter := bson.M{"_id": tID}
	update := bson.M{"$set": updateData}
	result, err := t.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update template", http.StatusInternalServerError, w, err)
		return
	}

	if result.MatchedCount == 0 {
		config.ErrorStatus("template not found", http.StatusNotFound, w, nil)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Template updated successfully",
	})
}

// DeleteTemplateHandler deletes a template
func (t *Template) DeleteTemplateHandler(w http.ResponseWriter, r *http.Request) {
	templateID := mux.Vars(r)["templateId"]

	// Convert the template ID to a primitive.ObjectID
	tID, err := primitive.ObjectIDFromHex(templateID)
	if err != nil {
		config.ErrorStatus("invalid template ID", http.StatusBadRequest, w, err)
		return
	}

	// Check if template is a default template (prevent deletion)
	template, err := t.DB.FindOne(context.Background(), bson.M{"_id": tID})
	if err != nil {
		config.ErrorStatus("template not found", http.StatusNotFound, w, err)
		return
	}

	if template.IsDefault {
		config.ErrorStatus("cannot delete default templates", http.StatusBadRequest, w, nil)
		return
	}

	// Delete the template
	filter := bson.M{"_id": tID}
	err = t.DB.DeleteOne(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to delete template", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Template deleted successfully",
	})
}

// GetDefaultTemplatesHandler retrieves all default templates
func (t *Template) GetDefaultTemplatesHandler(w http.ResponseWriter, r *http.Request) {
	templates, err := t.DB.GetDefaultTemplates(context.Background())
	if err != nil {
		config.ErrorStatus("failed to retrieve default templates", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"templates": templates,
	})
}

// GetDefaultTemplatesResolvedHandler retrieves all default templates with
// component references resolved to their names. Returns a map of template
// category → [{name, enabled}] so the frontend can know the canonical
// component list for each department type without hardcoding it.
func (t *Template) GetDefaultTemplatesResolvedHandler(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	templates, err := t.DB.GetDefaultTemplates(ctx)
	if err != nil {
		config.ErrorStatus("failed to retrieve default templates", http.StatusInternalServerError, w, err)
		return
	}

	// Collect all unique component IDs across all templates
	idSet := make(map[primitive.ObjectID]struct{})
	for _, tpl := range templates {
		for _, ref := range tpl.Components {
			idSet[ref.ComponentID] = struct{}{}
		}
	}

	ids := make([]primitive.ObjectID, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}

	// Resolve component IDs → names
	compNameMap := make(map[string]string) // hex ID → name
	if len(ids) > 0 && t.ComponentDB != nil {
		components, err := t.ComponentDB.GetComponentsByIDs(ctx, ids)
		if err != nil {
			config.ErrorStatus("failed to resolve component names", http.StatusInternalServerError, w, err)
			return
		}
		for _, c := range components {
			compNameMap[c.ID.Hex()] = c.Name
		}
	}

	// Build resolved map: category → [{name, enabled}]
	type resolvedComponent struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}

	result := make(map[string][]resolvedComponent)
	for _, tpl := range templates {
		category := tpl.Category
		if category == "" {
			category = tpl.Name
		}
		var comps []resolvedComponent
		for _, ref := range tpl.Components {
			name := compNameMap[ref.ComponentID.Hex()]
			if name == "" {
				continue // skip unresolved references
			}
			comps = append(comps, resolvedComponent{
				Name:    name,
				Enabled: ref.Enabled,
			})
		}
		result[category] = comps
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"templates": result,
	})
}

// GetTemplatesByCategoryHandler retrieves templates filtered by category
func (t *Template) GetTemplatesByCategoryHandler(w http.ResponseWriter, r *http.Request) {
	category := mux.Vars(r)["category"]

	templates, err := t.DB.GetTemplatesByCategory(context.Background(), category)
	if err != nil {
		config.ErrorStatus("failed to retrieve templates by category", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"templates": templates,
		"category":  category,
	})
}

// InitializeDefaultTemplatesHandler creates default templates if they don't exist
func (t *Template) InitializeDefaultTemplatesHandler(w http.ResponseWriter, r *http.Request) {
	err := t.DB.CreateDefaultTemplates(context.Background(), t.ComponentDB)
	if err != nil {
		config.ErrorStatus("failed to initialize default templates", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Default templates initialized successfully",
	})
}
