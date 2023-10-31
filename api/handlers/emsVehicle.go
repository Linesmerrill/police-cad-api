package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// EmsVehicle exported for testing purposes
type EmsVehicle struct {
	DB databases.EmsVehicleDatabase
}

// EmsVehicleHandler returns all emsVehicles
func (v EmsVehicle) EmsVehicleHandler(w http.ResponseWriter, r *http.Request) {
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		zap.S().Warnf(fmt.Sprintf("limit not set, using default of %v, err: %v", Limit|10, err))
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)
	dbResp, err := v.DB.Find(context.TODO(), bson.D{}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
	if err != nil {
		config.ErrorStatus("failed to get emsVehicles", http.StatusNotFound, w, err)
		return
	}
	// Because the frontend requires that the data elements inside models.EmsVehicles exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.EmsVehicle{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// EmsVehicleByIDHandler returns a emsVehicle by ID
func (v EmsVehicle) EmsVehicleByIDHandler(w http.ResponseWriter, r *http.Request) {
	emsVehicleID := mux.Vars(r)["ems_vehicle_id"]

	zap.S().Debugf("ems_vehicle_id: %v", emsVehicleID)

	evID, err := primitive.ObjectIDFromHex(emsVehicleID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	dbResp, err := v.DB.FindOne(context.Background(), bson.M{"_id": evID})
	if err != nil {
		config.ErrorStatus("failed to get emsVehicle by ID", http.StatusNotFound, w, err)
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

// EmsVehiclesByUserIDHandler returns all emsVehicles that contain the given userID
func (v EmsVehicle) EmsVehiclesByUserIDHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]
	activeCommunityID := r.URL.Query().Get("active_community_id")

	zap.S().Debugf("user_id: '%v'", userID)
	zap.S().Debugf("active_community: '%v'", activeCommunityID)

	var dbResp []models.EmsVehicle

	// If the user is in a community then we want to search for emsVehicles that
	// are in that same community. This way each user can have different emsVehicles
	// across different communities.
	//
	// Likewise, if the user is not in a community, then we will display only the emsVehicles
	// that are not in a community
	var err error
	if activeCommunityID != "" && activeCommunityID != "null" && activeCommunityID != "undefined" {
		dbResp, err = v.DB.Find(context.TODO(), bson.M{
			"emsVehicle.userID":            userID,
			"emsVehicle.activeCommunityID": activeCommunityID,
		})
		if err != nil {
			config.ErrorStatus("failed to get emsVehicles with active community id", http.StatusNotFound, w, err)
			return
		}
	} else {
		dbResp, err = v.DB.Find(context.TODO(), bson.M{
			"emsVehicle.userID": userID,
			"$or": []bson.M{
				{"emsVehicle.activeCommunityID": nil},
				{"emsVehicle.activeCommunityID": ""},
			},
		})
		if err != nil {
			config.ErrorStatus("failed to get emsVehicles with empty active community id", http.StatusNotFound, w, err)
			return
		}
	}

	// Because the frontend requires that the data elements inside models.EmsVehicles exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.EmsVehicle{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
