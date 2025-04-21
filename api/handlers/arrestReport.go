package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// ArrestReport exported for testing purposes
type ArrestReport struct {
	DB databases.ArrestReportDatabase
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
	arrestReportID := mux.Vars(r)["arrestReport_id"]

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
