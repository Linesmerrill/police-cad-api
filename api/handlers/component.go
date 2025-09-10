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

// Component handles component-related HTTP requests
type Component struct {
	DB *databases.ComponentDatabase
}

// NewComponent creates a new component handler
func NewComponent(db *databases.ComponentDatabase) *Component {
	return &Component{DB: db}
}

// CreateComponentHandler creates a new global component
func (c *Component) CreateComponentHandler(w http.ResponseWriter, r *http.Request) {
	var newComponent models.GlobalComponent

	// Parse the request body
	if err := json.NewDecoder(r.Body).Decode(&newComponent); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Generate a new _id for the component
	newComponent.ID = primitive.NewObjectID()
	now := primitive.NewDateTimeFromTime(time.Now())
	newComponent.CreatedAt = now
	newComponent.UpdatedAt = now

	// Insert the new component into the database
	_, err := c.DB.InsertOne(context.Background(), newComponent)
	if err != nil {
		config.ErrorStatus("failed to create component", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Component created successfully",
		"component": newComponent,
	})
}

// GetComponentsHandler retrieves all active components with optional filtering
func (c *Component) GetComponentsHandler(w http.ResponseWriter, r *http.Request) {
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

	skip := (page - 1) * limit
	opts := []*options.FindOptions{
		options.Find().SetSkip(int64(skip)).SetLimit(int64(limit)),
		options.Find().SetSort(bson.M{"category": 1, "name": 1}),
	}

	// Find components
	components, err := c.DB.FindMany(context.Background(), filter, opts...)
	if err != nil {
		config.ErrorStatus("failed to retrieve components", http.StatusInternalServerError, w, err)
		return
	}

	// Get total count for pagination
	totalCount, err := c.DB.Collection.CountDocuments(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to count components", http.StatusInternalServerError, w, err)
		return
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
	})
}

// GetComponentHandler retrieves a specific component by ID
func (c *Component) GetComponentHandler(w http.ResponseWriter, r *http.Request) {
	componentID := mux.Vars(r)["componentId"]

	// Convert the component ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(componentID)
	if err != nil {
		config.ErrorStatus("invalid component ID", http.StatusBadRequest, w, err)
		return
	}

	// Find the component by ID
	component, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("component not found", http.StatusNotFound, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(component)
}

// UpdateComponentHandler updates an existing component
func (c *Component) UpdateComponentHandler(w http.ResponseWriter, r *http.Request) {
	componentID := mux.Vars(r)["componentId"]

	// Convert the component ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(componentID)
	if err != nil {
		config.ErrorStatus("invalid component ID", http.StatusBadRequest, w, err)
		return
	}

	// Parse the request body
	var updateData models.GlobalComponent
	if err := json.NewDecoder(r.Body).Decode(&updateData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Update the updatedAt field
	updateData.UpdatedAt = primitive.NewDateTimeFromTime(time.Now())

	// Update the component in the database
	filter := bson.M{"_id": cID}
	update := bson.M{"$set": updateData}
	result, err := c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update component", http.StatusInternalServerError, w, err)
		return
	}

	if result.MatchedCount == 0 {
		config.ErrorStatus("component not found", http.StatusNotFound, w, nil)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Component updated successfully",
	})
}

// DeleteComponentHandler deletes a component
func (c *Component) DeleteComponentHandler(w http.ResponseWriter, r *http.Request) {
	componentID := mux.Vars(r)["componentId"]

	// Convert the component ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(componentID)
	if err != nil {
		config.ErrorStatus("invalid component ID", http.StatusBadRequest, w, err)
		return
	}

	// Check if component is being used by any templates
	// This would require a query to the templates collection
	// For now, we'll just delete it (you might want to add this check)

	// Delete the component
	filter := bson.M{"_id": cID}
	err = c.DB.DeleteOne(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to delete component", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Component deleted successfully",
	})
}

// GetComponentsByCategoryHandler retrieves components filtered by category
func (c *Component) GetComponentsByCategoryHandler(w http.ResponseWriter, r *http.Request) {
	category := mux.Vars(r)["category"]

	components, err := c.DB.GetComponentsByCategory(context.Background(), category)
	if err != nil {
		config.ErrorStatus("failed to retrieve components by category", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"components": components,
		"category":   category,
	})
}

// InitializeDefaultComponentsHandler creates default components if they don't exist
func (c *Component) InitializeDefaultComponentsHandler(w http.ResponseWriter, r *http.Request) {
	err := c.DB.CreateDefaultComponents(context.Background())
	if err != nil {
		config.ErrorStatus("failed to initialize default components", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Default components initialized successfully",
	})
}
