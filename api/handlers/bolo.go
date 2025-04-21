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

// GetBoloByIDHandler retrieves a BOLO by its ID
func (b Bolo) GetBoloByIDHandler(w http.ResponseWriter, r *http.Request) {
	boloID := mux.Vars(r)["bolo_id"]

	bID, err := primitive.ObjectIDFromHex(boloID)
	if err != nil {
		config.ErrorStatus("invalid BOLO ID", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": bID}
	bolo, err := b.DB.FindOne(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to find BOLO", http.StatusNotFound, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(bolo)
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

	// Create the base filter
	filter := bson.M{
		"$or": []bson.M{
			{
				"bolo.communityID": communityID,
				"bolo.scope":       "community",
			},
			{
				"bolo.communityID":  communityID,
				"bolo.departmentID": departmentID,
				"bolo.scope":        "department",
			},
		},
	}

	// Add optional key-value filter
	for key, values := range r.URL.Query() {
		if key != "communityId" && key != "departmentId" && key != "limit" && key != "page" {
			if boolValue, err := strconv.ParseBool(values[0]); err == nil {
				filter["bolo."+key] = boolValue
			} else {
				filter["bolo."+key] = values[0]
			}
		}
	}

	// Fetch total count
	totalCount, err := b.DB.CountDocuments(context.TODO(), filter)
	if err != nil {
		config.ErrorStatus("failed to get total count of BOLOs", http.StatusInternalServerError, w, err)
		return
	}

	// Fetch paginated data
	dbResp, err := b.DB.Find(context.TODO(), filter, &options.FindOptions{Limit: &limit64, Skip: &skip64})
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

// UpdateBoloHandler updates the details of an existing BOLO
func (b Bolo) UpdateBoloHandler(w http.ResponseWriter, r *http.Request) {
	boloID := mux.Vars(r)["bolo_id"]

	var updatedDetails map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updatedDetails); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	bID, err := primitive.ObjectIDFromHex(boloID)
	if err != nil {
		config.ErrorStatus("invalid BOLO ID", http.StatusBadRequest, w, err)
		return
	}

	update := bson.M{}
	for key, value := range updatedDetails {
		update["bolo."+key] = value
	}

	// Set updatedAt to the current time
	update["bolo.updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	filter := bson.M{"_id": bID}
	err = b.DB.UpdateOne(context.Background(), filter, bson.M{"$set": update})
	if err != nil {
		config.ErrorStatus("failed to update BOLO", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "BOLO updated successfully"}`))
}

// DeleteBoloHandler deletes an existing BOLO
func (b Bolo) DeleteBoloHandler(w http.ResponseWriter, r *http.Request) {
	boloID := mux.Vars(r)["bolo_id"]

	bID, err := primitive.ObjectIDFromHex(boloID)
	if err != nil {
		config.ErrorStatus("invalid BOLO ID", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": bID}
	err = b.DB.DeleteOne(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to delete BOLO", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "BOLO deleted successfully"}`))
}
