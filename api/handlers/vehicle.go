package handlers

import (
	"context"
	"encoding/json"
	"fmt"
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

// Vehicle exported for testing purposes
type Vehicle struct {
	DB databases.VehicleDatabase
}

// VehicleHandler returns all vehicles
func (v Vehicle) VehicleHandler(w http.ResponseWriter, r *http.Request) {
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		zap.S().Warnf(fmt.Sprintf("limit not set, using default of %v, err: %v", Limit|10, err))
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	dbResp, err := v.DB.Find(ctx, bson.D{}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
	if err != nil {
		config.ErrorStatus("failed to get vehicles", http.StatusNotFound, w, err)
		return
	}
	// Because the frontend requires that the data elements inside models.Vehicles exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Vehicle{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// VehicleByIDHandler returns a vehicle by ID
func (v Vehicle) VehicleByIDHandler(w http.ResponseWriter, r *http.Request) {
	civID := mux.Vars(r)["vehicle_id"]

	zap.S().Debugf("vehicle_id: %v", civID)

	cID, err := primitive.ObjectIDFromHex(civID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	dbResp, err := v.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get vehicle by ID", http.StatusNotFound, w, err)
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

// VehiclesByUserIDHandler returns all vehicles that contain the given userID
func (v Vehicle) VehiclesByUserIDHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]
	activeCommunityID := r.URL.Query().Get("active_community_id")
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		zap.S().Warnf(fmt.Sprintf("limit not set, using default of %v", Limit|10))
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)

	zap.S().Debugf("user_id: '%v'", userID)
	zap.S().Debugf("active_community: '%v'", activeCommunityID)

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	var dbResp []models.Vehicle

	// If the user is in a community then we want to search for vehicles that
	// are in that same community. This way each user can have different vehicles
	// across different communities.
	//
	// Likewise, if the user is not in a community, then we will display only the vehicles
	// that are not in a community
	err = nil
	if activeCommunityID != "" && activeCommunityID != "null" && activeCommunityID != "undefined" {
		dbResp, err = v.DB.Find(ctx, bson.M{
			"vehicle.userID":            userID,
			"vehicle.activeCommunityID": activeCommunityID,
		}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
		if err != nil {
			config.ErrorStatus("failed to get vehicles with active community id", http.StatusNotFound, w, err)
			return
		}
	} else {
		dbResp, err = v.DB.Find(ctx, bson.M{
			"vehicle.userID": userID,
			"$or": []bson.M{
				{"vehicle.activeCommunityID": nil},
				{"vehicle.activeCommunityID": ""},
			},
		}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
		if err != nil {
			config.ErrorStatus("failed to get vehicles with empty active community id", http.StatusNotFound, w, err)
			return
		}
	}

	// Because the frontend requires that the data elements inside models.Vehicles exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Vehicle{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// VehiclesByRegisteredOwnerIDHandler returns all vehicles that contain the given registeredOwnerID
func (v Vehicle) VehiclesByRegisteredOwnerIDHandler(w http.ResponseWriter, r *http.Request) {
	registeredOwnerID := mux.Vars(r)["registered_owner_id"]
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 10 // Default limit
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)

	zap.S().Debugf("registered_owner_id: '%v'", registeredOwnerID)

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Build filter once (reused for both queries)
	filter := bson.M{
		"$or": []bson.M{
			{"vehicle.registeredOwnerID": registeredOwnerID}, // Deprecated, use linkedCivilianID
			{"vehicle.linkedCivilianID": registeredOwnerID},
		},
	}

	// Execute queries in parallel for better performance
	type findResult struct {
		vehicles []models.Vehicle
		err      error
	}
	type countResult struct {
		total int64
		err   error
	}

	findChan := make(chan findResult, 1)
	countChan := make(chan countResult, 1)

	// Query to fetch vehicles (async)
	go func() {
		dbResp, err := v.DB.Find(ctx, filter, &options.FindOptions{Limit: &limit64, Skip: &skip64})
		findChan <- findResult{vehicles: dbResp, err: err}
	}()

	// Count total vehicles for pagination (async)
	go func() {
		total, err := v.DB.CountDocuments(ctx, filter)
		countChan <- countResult{total: total, err: err}
	}()

	// Wait for both queries to complete
	findRes := <-findChan
	countRes := <-countChan

	if findRes.err != nil {
		config.ErrorStatus("failed to get vehicles by registered owner id", http.StatusNotFound, w, findRes.err)
		return
	}

	if countRes.err != nil {
		config.ErrorStatus("failed to count vehicles", http.StatusInternalServerError, w, countRes.err)
		return
	}

	dbResp := findRes.vehicles
	total := countRes.total

	// Ensure the response is always an array
	if len(dbResp) == 0 {
		dbResp = []models.Vehicle{}
	}

	// Build the response
	response := map[string]interface{}{
		"limit":    Limit,
		"vehicles": dbResp,
		"page":     Page,
		"total":    total,
	}

	// Marshal and send the response
	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// VehicleSearchHandler returns vehicles based on search
func (v Vehicle) VehicleSearchHandler(w http.ResponseWriter, r *http.Request) {
	plate := r.URL.Query().Get("plate")
	vin := r.URL.Query().Get("vin")
	vehMake := r.URL.Query().Get("make")
	model := r.URL.Query().Get("model")
	activeCommunityID := r.URL.Query().Get("active_community_id") // optional
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 10 // Default limit
	}
	limit64 := int64(Limit)
	Page := getPage(Page, r)
	skip64 := int64(Page * Limit)

	zap.S().Debugf("plate: '%v'", plate)
	zap.S().Debugf("vin: '%v'", vin)
	zap.S().Debugf("make: '%v'", vehMake)
	zap.S().Debugf("model: '%v'", model)
	zap.S().Debugf("active_community: '%v'", activeCommunityID)

	// Build the query
	query := bson.M{
		"$or": []bson.M{
			{"vehicle.plate": bson.M{"$regex": plate, "$options": "i"}},
			{"vehicle.vin": bson.M{"$regex": vin, "$options": "i"}},
			{"vehicle.make": bson.M{"$regex": vehMake, "$options": "i"}},
			{"vehicle.model": bson.M{"$regex": model, "$options": "i"}},
		},
	}
	if activeCommunityID != "" {
		query["vehicle.activeCommunityID"] = activeCommunityID
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Execute queries in parallel for better performance
	type findResult struct {
		vehicles []models.Vehicle
		err      error
	}
	type countResult struct {
		total int64
		err   error
	}

	findChan := make(chan findResult, 1)
	countChan := make(chan countResult, 1)

	// Fetch vehicles (async)
	go func() {
		dbResp, err := v.DB.Find(ctx, query, &options.FindOptions{Limit: &limit64, Skip: &skip64})
		findChan <- findResult{vehicles: dbResp, err: err}
	}()

	// Count total vehicles for pagination (async)
	go func() {
		total, err := v.DB.CountDocuments(ctx, query)
		countChan <- countResult{total: total, err: err}
	}()

	// Wait for both queries to complete
	findRes := <-findChan
	countRes := <-countChan

	if findRes.err != nil {
		config.ErrorStatus("failed to search vehicles", http.StatusNotFound, w, findRes.err)
		return
	}

	if countRes.err != nil {
		config.ErrorStatus("failed to count vehicles", http.StatusInternalServerError, w, countRes.err)
		return
	}

	dbResp := findRes.vehicles
	total := countRes.total

	// Ensure the response is always an array
	if len(dbResp) == 0 {
		dbResp = []models.Vehicle{}
	}

	// Build the response
	response := map[string]interface{}{
		"limit":    Limit,
		"vehicles": dbResp,
		"page":     Page,
		"total":    total,
	}

	// Marshal and send the response
	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// CreateVehicleHandler creates a vehicle
func (v Vehicle) CreateVehicleHandler(w http.ResponseWriter, r *http.Request) {
	var vehicle models.Vehicle
	if err := json.NewDecoder(r.Body).Decode(&vehicle.Details); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	vehicle.ID = primitive.NewObjectID()
	vehicle.Details.CreatedAt = primitive.NewDateTimeFromTime(time.Now())
	vehicle.Details.UpdatedAt = vehicle.Details.CreatedAt

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	_, err := v.DB.InsertOne(ctx, vehicle)
	if err != nil {
		config.ErrorStatus("failed to create vehicle", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Vehicle created successfully",
		"id":      vehicle.ID.Hex(),
	})
}

// DeleteVehicleHandler deletes a vehicle by ID
func (v Vehicle) DeleteVehicleHandler(w http.ResponseWriter, r *http.Request) {
	vehicleID := mux.Vars(r)["vehicle_id"]

	vID, err := primitive.ObjectIDFromHex(vehicleID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	err = v.DB.DeleteOne(ctx, bson.M{"_id": vID})
	if err != nil {
		config.ErrorStatus("failed to delete vehicle", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Vehicle deleted successfully",
	})
}

// UpdateVehicleHandler updates a vehicle's details
func (v Vehicle) UpdateVehicleHandler(w http.ResponseWriter, r *http.Request) {
	vehicleID := mux.Vars(r)["vehicle_id"]

	vID, err := primitive.ObjectIDFromHex(vehicleID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Retrieve the existing vehicle data
	// var existingVehicle models.Vehicle
	existingVehicle, err := v.DB.FindOne(ctx, bson.M{"_id": vID})
	if err != nil {
		config.ErrorStatus("failed to find vehicle", http.StatusNotFound, w, err)
		return
	}

	// Convert existing vehicle details to a map
	existingDetailsMap := make(map[string]interface{})
	data, _ := json.Marshal(existingVehicle.Details)
	json.Unmarshal(data, &existingDetailsMap)

	// Decode the request body into a map
	var updateData map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updateData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Merge the update data with the existing vehicle data
	for key, value := range updateData {
		existingDetailsMap[key] = value
	}
	existingDetailsMap["updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	// Convert the map back to VehicleDetails
	updatedDetails := models.VehicleDetails{}
	data, _ = json.Marshal(existingDetailsMap)
	json.Unmarshal(data, &updatedDetails)

	// Update the vehicle in the database
	err = v.DB.UpdateOne(ctx, bson.M{"_id": vID}, bson.M{"$set": bson.M{"vehicle": updatedDetails}})
	if err != nil {
		config.ErrorStatus("failed to update vehicle", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Vehicle updated successfully",
	})
}
