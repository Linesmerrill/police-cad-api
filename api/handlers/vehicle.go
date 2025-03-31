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
	dbResp, err := v.DB.Find(context.TODO(), bson.D{}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
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

	dbResp, err := v.DB.FindOne(context.Background(), bson.M{"_id": cID})
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

	var dbResp []models.Vehicle

	// If the user is in a community then we want to search for vehicles that
	// are in that same community. This way each user can have different vehicles
	// across different communities.
	//
	// Likewise, if the user is not in a community, then we will display only the vehicles
	// that are not in a community
	err = nil
	if activeCommunityID != "" && activeCommunityID != "null" && activeCommunityID != "undefined" {
		dbResp, err = v.DB.Find(context.TODO(), bson.M{
			"vehicle.userID":            userID,
			"vehicle.activeCommunityID": activeCommunityID,
		}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
		if err != nil {
			config.ErrorStatus("failed to get vehicles with active community id", http.StatusNotFound, w, err)
			return
		}
	} else {
		dbResp, err = v.DB.Find(context.TODO(), bson.M{
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
	if err != nil {
		zap.S().Warnf(fmt.Sprintf("limit not set, using default of %v", Limit|10))
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)

	zap.S().Debugf("registered_owner_id: '%v'", registeredOwnerID)

	var dbResp []models.Vehicle

	err = nil
	dbResp, err = v.DB.Find(context.TODO(), bson.M{
		"vehicle.registeredOwnerID": registeredOwnerID,
	}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
	if err != nil {
		config.ErrorStatus("failed to get vehicles by registered owner id", http.StatusNotFound, w, err)
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

// VehiclesByPlateSearchHandler returns paginated list of vehicles that match the give plate
func (v Vehicle) VehiclesByPlateSearchHandler(w http.ResponseWriter, r *http.Request) {
	plate := r.URL.Query().Get("plate")
	activeCommunityID := r.URL.Query().Get("active_community_id") // optional
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		zap.S().Warnf(fmt.Sprintf("limit not set, using default of %v", Limit|10))
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)

	zap.S().Debugf("plate: '%v'", plate)
	zap.S().Debugf("active_community: '%v'", activeCommunityID)

	var dbResp []models.Vehicle

	// If the user is in a community then we want to search for vehicles that
	// are in that same community. This way each user can have different vehicles
	// across different communities.
	//
	// Likewise, if the user is not in a community, then we will display only the vehicles
	// that are not in a community
	err = nil
	if activeCommunityID != "" && activeCommunityID != "null" && activeCommunityID != "undefined" {
		dbResp, err = v.DB.Find(context.TODO(), bson.M{
			"$text": bson.M{
				"$search": fmt.Sprintf("%s", plate),
			},
			"vehicle.activeCommunityID": activeCommunityID,
		}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
		if err != nil {
			config.ErrorStatus("failed to get vehicle plate search with active community id", http.StatusNotFound, w, err)
			return
		}
	} else {
		dbResp, err = v.DB.Find(context.TODO(), bson.M{
			"vehicle.plate": plate,
			"$or": []bson.M{
				{"vehicle.activeCommunityID": nil},
				{"vehicle.activeCommunityID": ""},
			},
		}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
		if err != nil {
			config.ErrorStatus("failed to get vehicle plate search with empty active community id", http.StatusNotFound, w, err)
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

	_, err := v.DB.InsertOne(context.Background(), vehicle)
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

	err = v.DB.DeleteOne(context.Background(), bson.M{"_id": vID})
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

	var vehicle models.Vehicle
	if err := json.NewDecoder(r.Body).Decode(&vehicle.Details); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	vehicle.Details.UpdatedAt = primitive.NewDateTimeFromTime(time.Now())

	err = v.DB.UpdateOne(context.Background(), bson.M{"_id": vID}, bson.M{"$set": bson.M{"vehicle": vehicle.Details}})
	if err != nil {
		config.ErrorStatus("failed to update vehicle", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Vehicle updated successfully",
	})
}
