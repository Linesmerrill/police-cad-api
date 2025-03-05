package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Report handles report-related requests
type Report struct {
	RDB databases.ReportDatabase
}

// CreateReportHandler creates a new report
func (re Report) CreateReportHandler(w http.ResponseWriter, r *http.Request) {
	var report models.Report

	// Parse the request body to get the report details
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Generate a new _id for the report
	report.ID = primitive.NewObjectID()
	// Set the createdAt field to the current time
	report.CreatedAt = primitive.NewDateTimeFromTime(time.Now())

	// Insert the new report into the database
	_ = re.RDB.InsertOne(context.Background(), report)

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(`{"message": "Report created successfully"}`))
}
