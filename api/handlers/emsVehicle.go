package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// EMSVehicle represents the EMS vehicle handler
type EMSVehicle struct {
	DB databases.EMSVehicleDatabase
}

// GetEMSVehiclesHandler handles GET requests for EMS vehicles
func (h EMSVehicle) GetEMSVehiclesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get query parameters
	activeCommunityID := r.URL.Query().Get("active_community_id")
	limitStr := r.URL.Query().Get("limit")
	pageStr := r.URL.Query().Get("page")

	// Validate required parameters
	if activeCommunityID == "" {
		http.Error(w, "active_community_id is required", http.StatusBadRequest)
		return
	}

	// Set default values for optional parameters
	limit := int64(20)
	page := int64(0)

	// Parse limit parameter
	if limitStr != "" {
		if parsedLimit, err := strconv.ParseInt(limitStr, 10, 64); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	// Parse page parameter
	if pageStr != "" {
		if parsedPage, err := strconv.ParseInt(pageStr, 10, 64); err == nil && parsedPage >= 0 {
			page = parsedPage
		}
	}

	// Get EMS vehicles from database
	ctx := context.Background()
	response, err := h.DB.GetEMSVehiclesByCommunityID(ctx, activeCommunityID, limit, page)
	if err != nil {
		zap.S().With(err).Error("failed to get EMS vehicles")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return response
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		zap.S().With(err).Error("failed to encode EMS vehicles response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// GetEMSVehicleByIDHandler handles GET requests for a single EMS vehicle
func (h EMSVehicle) GetEMSVehicleByIDHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get ID from URL parameters
	vars := mux.Vars(r)
	id := vars["id"]

	if id == "" {
		http.Error(w, "EMS vehicle ID is required", http.StatusBadRequest)
		return
	}

	// Get EMS vehicle from database
	ctx := context.Background()
	vehicle, err := h.DB.GetEMSVehicleByID(ctx, id)
	if err != nil {
		zap.S().With(err).Error("failed to get EMS vehicle by ID")
		http.Error(w, "EMS vehicle not found", http.StatusNotFound)
		return
	}

	// Return response
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(vehicle); err != nil {
		zap.S().With(err).Error("failed to encode EMS vehicle response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// CreateEMSVehicleHandler handles POST requests to create a new EMS vehicle
func (h EMSVehicle) CreateEMSVehicleHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse request body
	var vehicle models.EMSVehicle
	if err := json.Unmarshal(body, &vehicle); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if vehicle.Vehicle.Plate == "" {
		http.Error(w, "plate is required", http.StatusBadRequest)
		return
	}

	if vehicle.Vehicle.Model == "" {
		http.Error(w, "model is required", http.StatusBadRequest)
		return
	}

	if vehicle.Vehicle.EngineNumber == "" {
		http.Error(w, "engineNumber is required", http.StatusBadRequest)
		return
	}

	if vehicle.Vehicle.ActiveCommunityID == "" {
		http.Error(w, "activeCommunityID is required", http.StatusBadRequest)
		return
	}

	// Validate plate length
	if len(vehicle.Vehicle.Plate) > 8 {
		http.Error(w, "plate cannot exceed 8 characters", http.StatusBadRequest)
		return
	}

	// Validate model value
	isValidModel := false
	for _, validModel := range models.ValidVehicleModels {
		if vehicle.Vehicle.Model == validModel {
			isValidModel = true
			break
		}
	}
	if !isValidModel {
		http.Error(w, "model must be one of the valid options: "+strings.Join(models.ValidVehicleModels, ", "), http.StatusBadRequest)
		return
	}

	// Validate engine number length
	if len(vehicle.Vehicle.EngineNumber) > 10 {
		http.Error(w, "engineNumber cannot exceed 10 characters", http.StatusBadRequest)
		return
	}

	// Set default registered owner if not provided
	if vehicle.Vehicle.RegisteredOwner == "" {
		vehicle.Vehicle.RegisteredOwner = "N/A"
	}

	// Create EMS vehicle in database
	ctx := context.Background()
	err = h.DB.CreateEMSVehicle(ctx, &vehicle)
	if err != nil {
		zap.S().With(err).Error("failed to create EMS vehicle")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return created EMS vehicle
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(vehicle); err != nil {
		zap.S().With(err).Error("failed to encode created EMS vehicle response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// UpdateEMSVehicleHandler handles PUT requests to update an existing EMS vehicle
func (h EMSVehicle) UpdateEMSVehicleHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get ID from URL parameters
	vars := mux.Vars(r)
	id := vars["id"]

	if id == "" {
		http.Error(w, "EMS vehicle ID is required", http.StatusBadRequest)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse request body
	var vehicle models.EMSVehicle
	if err := json.Unmarshal(body, &vehicle); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if vehicle.Vehicle.Plate == "" {
		http.Error(w, "plate is required", http.StatusBadRequest)
		return
	}

	if vehicle.Vehicle.Model == "" {
		http.Error(w, "model is required", http.StatusBadRequest)
		return
	}

	if vehicle.Vehicle.EngineNumber == "" {
		http.Error(w, "engineNumber is required", http.StatusBadRequest)
		return
	}

	if vehicle.Vehicle.ActiveCommunityID == "" {
		http.Error(w, "activeCommunityID is required", http.StatusBadRequest)
		return
	}

	// Validate plate length
	if len(vehicle.Vehicle.Plate) > 8 {
		http.Error(w, "plate cannot exceed 8 characters", http.StatusBadRequest)
		return
	}

	// Validate model value
	isValidModel := false
	for _, validModel := range models.ValidVehicleModels {
		if vehicle.Vehicle.Model == validModel {
			isValidModel = true
			break
		}
	}
	if !isValidModel {
		http.Error(w, "model must be one of the valid options: "+strings.Join(models.ValidVehicleModels, ", "), http.StatusBadRequest)
		return
	}

	// Validate engine number length
	if len(vehicle.Vehicle.EngineNumber) > 10 {
		http.Error(w, "engineNumber cannot exceed 10 characters", http.StatusBadRequest)
		return
	}

	// Set default registered owner if not provided
	if vehicle.Vehicle.RegisteredOwner == "" {
		vehicle.Vehicle.RegisteredOwner = "N/A"
	}

	// Update EMS vehicle in database
	ctx := context.Background()
	err = h.DB.UpdateEMSVehicle(ctx, id, &vehicle)
	if err != nil {
		zap.S().With(err).Error("failed to update EMS vehicle")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	response := map[string]string{"message": "EMS vehicle updated successfully"}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		zap.S().With(err).Error("failed to encode update response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// DeleteEMSVehicleHandler handles DELETE requests to delete an EMS vehicle
func (h EMSVehicle) DeleteEMSVehicleHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get ID from URL parameters
	vars := mux.Vars(r)
	id := vars["id"]

	if id == "" {
		http.Error(w, "EMS vehicle ID is required", http.StatusBadRequest)
		return
	}

	// Delete EMS vehicle from database
	ctx := context.Background()
	err := h.DB.DeleteEMSVehicle(ctx, id)
	if err != nil {
		zap.S().With(err).Error("failed to delete EMS vehicle")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	response := map[string]string{"message": "EMS vehicle deleted successfully"}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		zap.S().With(err).Error("failed to encode delete response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
} 