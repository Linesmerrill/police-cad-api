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

// MedicalReport represents the medical report handler
type MedicalReport struct {
	DB databases.MedicalReportDatabase
}

// GetMedicalReportsHandler handles GET requests for medical reports
func (h MedicalReport) GetMedicalReportsHandler(w http.ResponseWriter, r *http.Request) {
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

	// Get medical reports from database
	ctx := context.Background()
	response, err := h.DB.GetMedicalReportsByCivilianID(ctx, civilianID, activeCommunityID, limit, page)
	if err != nil {
		zap.S().With(err).Error("failed to get medical reports")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return response
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		zap.S().With(err).Error("failed to encode medical reports response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// GetMedicalReportByIDHandler handles GET requests for a single medical report
func (h MedicalReport) GetMedicalReportByIDHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get ID from URL parameters
	vars := mux.Vars(r)
	id := vars["id"]

	if id == "" {
		http.Error(w, "medical report ID is required", http.StatusBadRequest)
		return
	}

	// Get medical report from database
	ctx := context.Background()
	medicalReport, err := h.DB.GetMedicalReportByID(ctx, id)
	if err != nil {
		zap.S().With(err).Error("failed to get medical report by ID")
		http.Error(w, "Medical report not found", http.StatusNotFound)
		return
	}

	// Return response
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(medicalReport); err != nil {
		zap.S().With(err).Error("failed to encode medical report response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// CreateMedicalReportHandler handles POST requests to create a new medical report
func (h MedicalReport) CreateMedicalReportHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse request body
	var medicalReport models.MedicalReport
	if err := json.Unmarshal(body, &medicalReport); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if medicalReport.Report.CivilianID == "" {
		http.Error(w, "civilianID is required", http.StatusBadRequest)
		return
	}

	if medicalReport.Report.ActiveCommunityID == "" {
		http.Error(w, "activeCommunityID is required", http.StatusBadRequest)
		return
	}

	// Create medical report in database
	ctx := context.Background()
	err = h.DB.CreateMedicalReport(ctx, &medicalReport)
	if err != nil {
		zap.S().With(err).Error("failed to create medical report")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return created medical report
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(medicalReport); err != nil {
		zap.S().With(err).Error("failed to encode created medical report response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// UpdateMedicalReportHandler handles PUT requests to update an existing medical report
func (h MedicalReport) UpdateMedicalReportHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get ID from URL parameters
	vars := mux.Vars(r)
	id := vars["id"]

	if id == "" {
		http.Error(w, "medical report ID is required", http.StatusBadRequest)
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
	var medicalReport models.MedicalReport
	if err := json.Unmarshal(body, &medicalReport); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if medicalReport.Report.CivilianID == "" {
		http.Error(w, "civilianID is required", http.StatusBadRequest)
		return
	}

	if medicalReport.Report.ActiveCommunityID == "" {
		http.Error(w, "activeCommunityID is required", http.StatusBadRequest)
		return
	}

	// Update medical report in database
	ctx := context.Background()
	err = h.DB.UpdateMedicalReport(ctx, id, &medicalReport)
	if err != nil {
		zap.S().With(err).Error("failed to update medical report")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	response := map[string]string{"message": "Medical report updated successfully"}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		zap.S().With(err).Error("failed to encode update response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// DeleteMedicalReportHandler handles DELETE requests to delete a medical report
func (h MedicalReport) DeleteMedicalReportHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get ID from URL parameters
	vars := mux.Vars(r)
	id := vars["id"]

	if id == "" {
		http.Error(w, "medical report ID is required", http.StatusBadRequest)
		return
	}

	// Delete medical report from database
	ctx := context.Background()
	err := h.DB.DeleteMedicalReport(ctx, id)
	if err != nil {
		zap.S().With(err).Error("failed to delete medical report")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	response := map[string]string{"message": "Medical report deleted successfully"}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		zap.S().With(err).Error("failed to encode delete response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}
