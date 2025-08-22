package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Medication represents the medication handler
type Medication struct {
	DB databases.MedicationDatabase
}

// GetMedicationsHandler handles GET requests for medications
func (h Medication) GetMedicationsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get query parameters
	civilianID := r.URL.Query().Get("civilian_id")
	activeCommunityID := r.URL.Query().Get("active_community_id")
	limitStr := r.URL.Query().Get("limit")
	pageStr := r.URL.Query().Get("page")

	// Validate required parameters
	if civilianID == "" {
		http.Error(w, "civilian_id is required", http.StatusBadRequest)
		return
	}

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

	// Get medications from database
	ctx := context.Background()
	response, err := h.DB.GetMedicationsByCivilianID(ctx, civilianID, activeCommunityID, limit, page)
	if err != nil {
		zap.S().With(err).Error("failed to get medications")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return response
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		zap.S().With(err).Error("failed to encode medications response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// GetMedicationByIDHandler handles GET requests for a single medication
func (h Medication) GetMedicationByIDHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get ID from URL parameters
	vars := mux.Vars(r)
	id := vars["id"]

	if id == "" {
		http.Error(w, "medication ID is required", http.StatusBadRequest)
		return
	}

	// Get medication from database
	ctx := context.Background()
	medication, err := h.DB.GetMedicationByID(ctx, id)
	if err != nil {
		zap.S().With(err).Error("failed to get medication by ID")
		http.Error(w, "Medication not found", http.StatusNotFound)
		return
	}

	// Return response
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(medication); err != nil {
		zap.S().With(err).Error("failed to encode medication response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// CreateMedicationHandler handles POST requests to create a new medication
func (h Medication) CreateMedicationHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse request body
	var medication models.Medication
	if err := json.Unmarshal(body, &medication); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if medication.Medication.CivilianID == "" {
		http.Error(w, "civilianID is required", http.StatusBadRequest)
		return
	}

	if medication.Medication.ActiveCommunityID == "" {
		http.Error(w, "activeCommunityID is required", http.StatusBadRequest)
		return
	}

	if medication.Medication.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Create medication in database
	ctx := context.Background()
	err = h.DB.CreateMedication(ctx, &medication)
	if err != nil {
		zap.S().With(err).Error("failed to create medication")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return created medication
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(medication); err != nil {
		zap.S().With(err).Error("failed to encode created medication response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// UpdateMedicationHandler handles PUT requests to update an existing medication
func (h Medication) UpdateMedicationHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get ID from URL parameters
	vars := mux.Vars(r)
	id := vars["id"]

	if id == "" {
		http.Error(w, "medication ID is required", http.StatusBadRequest)
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
	var medication models.Medication
	if err := json.Unmarshal(body, &medication); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if medication.Medication.CivilianID == "" {
		http.Error(w, "civilianID is required", http.StatusBadRequest)
		return
	}

	if medication.Medication.ActiveCommunityID == "" {
		http.Error(w, "activeCommunityID is required", http.StatusBadRequest)
		return
	}

	if medication.Medication.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Update medication in database
	ctx := context.Background()
	err = h.DB.UpdateMedication(ctx, id, &medication)
	if err != nil {
		zap.S().With(err).Error("failed to update medication")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	response := map[string]string{"message": "Medication updated successfully"}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		zap.S().With(err).Error("failed to encode update response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// DeleteMedicationHandler handles DELETE requests to delete a medication
func (h Medication) DeleteMedicationHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get ID from URL parameters
	vars := mux.Vars(r)
	id := vars["id"]

	if id == "" {
		http.Error(w, "medication ID is required", http.StatusBadRequest)
		return
	}

	// Delete medication from database
	ctx := context.Background()
	err := h.DB.DeleteMedication(ctx, id)
	if err != nil {
		zap.S().With(err).Error("failed to delete medication")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	response := map[string]string{"message": "Medication deleted successfully"}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		zap.S().With(err).Error("failed to encode delete response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}
