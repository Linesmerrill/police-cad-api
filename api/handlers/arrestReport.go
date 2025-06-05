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

// ArrestReport exported for testing purposes
type ArrestReport struct {
	DB databases.ArrestReportDatabase
}

// PaginatedDataResponse holds the structure for paginated responses
type PaginatedDataResponse struct {
	Page       int         `json:"page"`
	TotalCount int64       `json:"totalCount"`
	Data       interface{} `json:"data"`
}

// GetArrestReportByIDHandler retrieves a Arrest report by its ID
func (a ArrestReport) GetArrestReportByIDHandler(w http.ResponseWriter, r *http.Request) {
	arrestReportID := mux.Vars(r)["arrestReport_id"]

	bID, err := primitive.ObjectIDFromHex(arrestReportID)
	if err != nil {
		config.ErrorStatus("invalid Arrest report ID", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": bID}
	arrestReport, err := a.DB.FindOne(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to find Arrest report", http.StatusNotFound, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(arrestReport)
}

// CreateArrestReportHandler creates a new ArrestReport
func (a ArrestReport) CreateArrestReportHandler(w http.ResponseWriter, r *http.Request) {
	var newArrestReport models.ArrestReport
	if err := json.NewDecoder(r.Body).Decode(&newArrestReport); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	newArrestReport.ID = primitive.NewObjectID()
	newArrestReport.Details.CreatedAt = primitive.NewDateTimeFromTime(time.Now())

	_, err := a.DB.InsertOne(context.TODO(), newArrestReport)
	if err != nil {
		config.ErrorStatus("failed to create new Arrest report", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Arrest report created successfully",
		"id":      newArrestReport.ID.Hex(),
	})
}

// UpdateArrestReportHandler updates the details of an existing Arrest report
func (a ArrestReport) UpdateArrestReportHandler(w http.ResponseWriter, r *http.Request) {
	arrestReportID := mux.Vars(r)["arrestReport_id"]

	var updatedDetails map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updatedDetails); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	bID, err := primitive.ObjectIDFromHex(arrestReportID)
	if err != nil {
		config.ErrorStatus("invalid Arrest report ID", http.StatusBadRequest, w, err)
		return
	}

	update := bson.M{}
	for key, value := range updatedDetails {
		update["arrestReport."+key] = value
	}

	// Set updatedAt to the current time
	update["arrestReport.updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	filter := bson.M{"_id": bID}
	err = a.DB.UpdateOne(context.Background(), filter, bson.M{"$set": update})
	if err != nil {
		config.ErrorStatus("failed to update Arrest report", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Arrest report updated successfully"}`))
}

// DeleteArrestReportHandler deletes an existing Arrest report
func (a ArrestReport) DeleteArrestReportHandler(w http.ResponseWriter, r *http.Request) {
	arrestReportID := mux.Vars(r)["arrest_report_id"]

	bID, err := primitive.ObjectIDFromHex(arrestReportID)
	if err != nil {
		config.ErrorStatus("invalid Arrest report ID", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": bID}
	err = a.DB.DeleteOne(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to delete Arrest report", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Arrest report deleted successfully"}`))
}

// GetArrestReportsByArresteeIDHandler retrieves all Arrest reports that contain the given arresteeID
func (a ArrestReport) GetArrestReportsByArresteeIDHandler(w http.ResponseWriter, r *http.Request) {
	arresteeID := mux.Vars(r)["arrestee_id"]

	// Parse pagination parameters
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 10 // Default limit
	}
	Page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || Page < 0 {
		Page = 0 // Default page
	}
	skip := int64(Page * Limit)
	limit64 := int64(Limit)

	// Create the filter
	filter := bson.M{
		"arrestReport.arrestee.id": arresteeID,
	}

	// Fetch total count
	totalCount, err := a.DB.CountDocuments(context.TODO(), filter)
	if err != nil {
		config.ErrorStatus("failed to get total count of arrest reports", http.StatusInternalServerError, w, err)
		return
	}

	// Fetch paginated data
	dbResp, err := a.DB.Find(context.TODO(), filter, &options.FindOptions{
		Limit: &limit64,
		Skip:  &skip,
	})
	if err != nil {
		config.ErrorStatus("failed to get arrest reports", http.StatusNotFound, w, err)
		return
	}

	// Create paginated response
	paginatedResponse := PaginatedDataResponse{
		Page:       Page,
		TotalCount: totalCount,
		Data:       dbResp,
	}

	// Encode and send the response
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(paginatedResponse)
}
