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

// EMSPersona represents the EMS persona handler
type EMSPersona struct {
	DB databases.EMSPersonaDatabase
}

// GetEMSPersonasHandler handles GET requests for EMS personas
func (h EMSPersona) GetEMSPersonasHandler(w http.ResponseWriter, r *http.Request) {
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

	// Get EMS personas from database
	ctx := context.Background()
	response, err := h.DB.GetEMSPersonasByCommunityID(ctx, activeCommunityID, limit, page)
	if err != nil {
		zap.S().With(err).Error("failed to get EMS personas")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return response
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		zap.S().With(err).Error("failed to encode EMS personas response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// GetEMSPersonaByIDHandler handles GET requests for a single EMS persona
func (h EMSPersona) GetEMSPersonaByIDHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get ID from URL parameters
	vars := mux.Vars(r)
	id := vars["id"]

	if id == "" {
		http.Error(w, "EMS persona ID is required", http.StatusBadRequest)
		return
	}

	// Get EMS persona from database
	ctx := context.Background()
	persona, err := h.DB.GetEMSPersonaByID(ctx, id)
	if err != nil {
		zap.S().With(err).Error("failed to get EMS persona by ID")
		http.Error(w, "EMS persona not found", http.StatusNotFound)
		return
	}

	// Return response
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(persona); err != nil {
		zap.S().With(err).Error("failed to encode EMS persona response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// CreateEMSPersonaHandler handles POST requests to create a new EMS persona
func (h EMSPersona) CreateEMSPersonaHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse request body
	var persona models.EMSPersona
	if err := json.Unmarshal(body, &persona); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if persona.Persona.FirstName == "" {
		http.Error(w, "firstName is required", http.StatusBadRequest)
		return
	}

	if persona.Persona.LastName == "" {
		http.Error(w, "lastName is required", http.StatusBadRequest)
		return
	}

	if persona.Persona.Department == "" {
		http.Error(w, "department is required", http.StatusBadRequest)
		return
	}

	if persona.Persona.AssignmentArea == "" {
		http.Error(w, "assignmentArea is required", http.StatusBadRequest)
		return
	}

	if persona.Persona.ActiveCommunityID == "" {
		http.Error(w, "activeCommunityID is required", http.StatusBadRequest)
		return
	}

	// Validate department value
	if persona.Persona.Department != "Fire" && persona.Persona.Department != "EMS" {
		http.Error(w, "department must be either 'Fire' or 'EMS'", http.StatusBadRequest)
		return
	}

	// Validate assignment area length
	if len(persona.Persona.AssignmentArea) > 50 {
		http.Error(w, "assignmentArea cannot exceed 50 characters", http.StatusBadRequest)
		return
	}

	// Validate station number
	if persona.Persona.Station > 99999 {
		http.Error(w, "station cannot exceed 5 digits", http.StatusBadRequest)
		return
	}

	// Validate call sign length
	if persona.Persona.CallSign != "" && len(persona.Persona.CallSign) > 10 {
		http.Error(w, "callSign cannot exceed 10 characters", http.StatusBadRequest)
		return
	}

	// Create EMS persona in database
	ctx := context.Background()
	err = h.DB.CreateEMSPersona(ctx, &persona)
	if err != nil {
		zap.S().With(err).Error("failed to create EMS persona")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return created EMS persona
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(persona); err != nil {
		zap.S().With(err).Error("failed to encode created EMS persona response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// UpdateEMSPersonaHandler handles PUT requests to update an existing EMS persona
func (h EMSPersona) UpdateEMSPersonaHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get ID from URL parameters
	vars := mux.Vars(r)
	id := vars["id"]

	if id == "" {
		http.Error(w, "EMS persona ID is required", http.StatusBadRequest)
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
	var persona models.EMSPersona
	if err := json.Unmarshal(body, &persona); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if persona.Persona.FirstName == "" {
		http.Error(w, "firstName is required", http.StatusBadRequest)
		return
	}

	if persona.Persona.LastName == "" {
		http.Error(w, "lastName is required", http.StatusBadRequest)
		return
	}

	if persona.Persona.Department == "" {
		http.Error(w, "department is required", http.StatusBadRequest)
		return
	}

	if persona.Persona.AssignmentArea == "" {
		http.Error(w, "assignmentArea is required", http.StatusBadRequest)
		return
	}

	if persona.Persona.ActiveCommunityID == "" {
		http.Error(w, "activeCommunityID is required", http.StatusBadRequest)
		return
	}

	// Validate department value
	if persona.Persona.Department != "Fire" && persona.Persona.Department != "EMS" {
		http.Error(w, "department must be either 'Fire' or 'EMS'", http.StatusBadRequest)
		return
	}

	// Validate assignment area length
	if len(persona.Persona.AssignmentArea) > 50 {
		http.Error(w, "assignmentArea cannot exceed 50 characters", http.StatusBadRequest)
		return
	}

	// Validate station number
	if persona.Persona.Station > 99999 {
		http.Error(w, "station cannot exceed 5 digits", http.StatusBadRequest)
		return
	}

	// Validate call sign length
	if persona.Persona.CallSign != "" && len(persona.Persona.CallSign) > 10 {
		http.Error(w, "callSign cannot exceed 10 characters", http.StatusBadRequest)
		return
	}

	// Update EMS persona in database
	ctx := context.Background()
	err = h.DB.UpdateEMSPersona(ctx, id, &persona)
	if err != nil {
		zap.S().With(err).Error("failed to update EMS persona")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	response := map[string]string{"message": "EMS persona updated successfully"}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		zap.S().With(err).Error("failed to encode update response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// DeleteEMSPersonaHandler handles DELETE requests to delete an EMS persona
func (h EMSPersona) DeleteEMSPersonaHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get ID from URL parameters
	vars := mux.Vars(r)
	id := vars["id"]

	if id == "" {
		http.Error(w, "EMS persona ID is required", http.StatusBadRequest)
		return
	}

	// Delete EMS persona from database
	ctx := context.Background()
	err := h.DB.DeleteEMSPersona(ctx, id)
	if err != nil {
		zap.S().With(err).Error("failed to delete EMS persona")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	response := map[string]string{"message": "EMS persona deleted successfully"}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		zap.S().With(err).Error("failed to encode delete response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}
