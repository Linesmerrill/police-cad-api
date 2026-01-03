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

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// License exported for testing purposes
type License struct {
	DB databases.LicenseDatabase
}

// LicenseByIDHandler returns a license by ID
func (l License) LicenseByIDHandler(w http.ResponseWriter, r *http.Request) {
	licID := mux.Vars(r)["license_id"]

	zap.S().Debugf("license_id: %v", licID)

	lID, err := primitive.ObjectIDFromHex(licID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	dbResp, err := l.DB.FindOne(ctx, bson.M{"_id": lID})
	if err != nil {
		config.ErrorStatus("failed to get license by ID", http.StatusNotFound, w, err)
		return
	}

	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// LicensesByCivilianIDHandler returns all licenses that contain the given civilianID
func (l License) LicensesByCivilianIDHandler(w http.ResponseWriter, r *http.Request) {
	civID := mux.Vars(r)["civilian_id"]
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 10 // Default limit
	}
	Page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || Page < 1 {
		Page = 1 // Default page
	}
	skip := int64((Page - 1) * Limit)
	limit64 := int64(Limit)

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Build filter once (reused for both queries)
	filter := bson.M{"license.civilianID": civID}

	// Execute queries in parallel for better performance
	type findResult struct {
		licenses []models.License
		err      error
	}
	type countResult struct {
		total int64
		err   error
	}

	findChan := make(chan findResult, 1)
	countChan := make(chan countResult, 1)

	// Fetch paginated data (async)
	go func() {
		dbResp, err := l.DB.Find(ctx, filter, &options.FindOptions{Limit: &limit64, Skip: &skip})
		findChan <- findResult{licenses: dbResp, err: err}
	}()

	// Fetch total count (async)
	go func() {
		total, err := l.DB.CountDocuments(ctx, filter)
		countChan <- countResult{total: total, err: err}
	}()

	// Wait for both queries to complete
	findRes := <-findChan
	countRes := <-countChan

	if findRes.err != nil {
		config.ErrorStatus("failed to get licenses by civilian id", http.StatusNotFound, w, findRes.err)
		return
	}

	if countRes.err != nil {
		config.ErrorStatus("failed to get total count of licenses", http.StatusInternalServerError, w, countRes.err)
		return
	}

	dbResp := findRes.licenses
	totalCount := countRes.total

	// Ensure the response is not nil
	if len(dbResp) == 0 {
		dbResp = []models.License{}
	}

	// Create paginated response
	paginatedResponse := PaginatedDataResponse{
		Page:       Page,
		TotalCount: totalCount,
		Data:       dbResp,
	}

	// Send the response
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(paginatedResponse)
}

// CreateLicenseHandler creates a new license
func (l License) CreateLicenseHandler(w http.ResponseWriter, r *http.Request) {
	// Create a new license object with generated ID and timestamps
	licenseID := primitive.NewObjectID()
	newLicense := models.License{
		ID: licenseID,
		Details: models.LicenseDetails{
			CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
			UpdatedAt: primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	// Decode the request body into the LicenseDetails field
	if err := json.NewDecoder(r.Body).Decode(&newLicense.Details); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Insert the new license into the database
	_, err := l.DB.InsertOne(context.Background(), newLicense)
	if err != nil {
		config.ErrorStatus("failed to create license", http.StatusInternalServerError, w, err)
		return
	}

	// Respond with the ID of the newly created license
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "License created successfully",
		"id":      licenseID.Hex(),
	})
}

// UpdateLicenseByIDHandler updates a license by ID
func (l License) UpdateLicenseByIDHandler(w http.ResponseWriter, r *http.Request) {
	licID := mux.Vars(r)["license_id"]

	zap.S().Debugf("license_id: %v", licID)

	lID, err := primitive.ObjectIDFromHex(licID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	var updatedFields map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updatedFields); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	update := bson.M{"$set": bson.M{}}
	for key, value := range updatedFields {
		update["$set"].(bson.M)["license."+key] = value
	}

	filter := bson.M{"_id": lID}

	err = l.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update license by ID", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "license updated successfully"}`))
}

// DeleteLicenseByIDHandler deletes a license by ID
func (l License) DeleteLicenseByIDHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the license ID from the URL parameters
	licID := mux.Vars(r)["license_id"]

	// Convert the license ID to an ObjectID
	lID, err := primitive.ObjectIDFromHex(licID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Delete the license from the database
	err = l.DB.DeleteOne(context.Background(), bson.M{"_id": lID})
	if err != nil {
		config.ErrorStatus("failed to delete license", http.StatusInternalServerError, w, err)
		return
	}

	// Respond with a success message
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "License deleted successfully",
	})
}
