package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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
	dbResp, err := v.DB.Find(context.TODO(), bson.M{})
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

	zap.S().Debugf("user_id: '%v'", userID)
	zap.S().Debugf("active_community: '%v'", activeCommunityID)

	var dbResp []models.Vehicle

	// If the user is in a community then we want to search for vehicles that
	// are in that same community. This way each user can have different vehicles
	// across different communities.
	//
	// Likewise, if the user is not in a community, then we will display only the vehicles
	// that are not in a community
	var err error
	if activeCommunityID != "" && activeCommunityID != "null" && activeCommunityID != "undefined" {
		dbResp, err = v.DB.Find(context.TODO(), bson.M{
			"vehicle.userID":            userID,
			"vehicle.activeCommunityID": activeCommunityID,
		})
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
		})
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
