package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Bolo exported for testing purposes
type Bolo struct {
	DB databases.BoloDatabase
}

// PaginatedResponse holds the structure for paginated responses
type PaginatedResponse struct {
	Page       int           `json:"page"`
	TotalCount int64         `json:"totalCount"`
	Data       []models.Bolo `json:"data"`
}

// FetchDepartmentBolosHandler returns all BOLOs for a given community and department
func (b Bolo) FetchDepartmentBolosHandler(w http.ResponseWriter, r *http.Request) {
	communityID := r.URL.Query().Get("communityId")
	departmentID := r.URL.Query().Get("departmentId")
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		zap.S().Warnf("limit not set, using default of %v, err: %v", Limit|10, err)
		Limit = 10
	}
	limit64 := int64(Limit)
	Page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil {
		Page = 0
	}
	skip64 := int64(Page * Limit)

	// Fetch total count
	totalCount, err := b.DB.CountDocuments(context.TODO(), bson.M{
		"bolo.communityID":  communityID,
		"bolo.departmentID": departmentID,
	})
	if err != nil {
		config.ErrorStatus("failed to get total count of BOLOs", http.StatusInternalServerError, w, err)
		return
	}

	// Fetch paginated data
	dbResp, err := b.DB.Find(context.TODO(), bson.M{
		"bolo.communityID":  communityID,
		"bolo.departmentID": departmentID,
	}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
	if err != nil {
		config.ErrorStatus("failed to get BOLOs", http.StatusNotFound, w, err)
		return
	}

	if len(dbResp) == 0 {
		dbResp = []models.Bolo{}
	}

	// Create paginated response
	paginatedResponse := PaginatedResponse{
		Page:       Page,
		TotalCount: totalCount,
		Data:       dbResp,
	}

	respB, err := json.Marshal(paginatedResponse)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(respB)
}

// CreateBoloHandler creates a new BOLO
func (b Bolo) CreateBoloHandler(w http.ResponseWriter, r *http.Request) {
	var newBolo models.Bolo
	if err := json.NewDecoder(r.Body).Decode(&newBolo); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	newBolo.ID = primitive.NewObjectID()
	newBolo.Details.CreatedAt = primitive.NewDateTimeFromTime(time.Now())

	_, err := b.DB.InsertOne(context.TODO(), newBolo)
	if err != nil {
		config.ErrorStatus("failed to create new BOLO", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "BOLO created successfully",
		"id":      newBolo.ID.Hex(),
	})
}
