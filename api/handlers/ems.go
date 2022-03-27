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

// Ems exported for testing purposes
type Ems struct {
	DB databases.EmsDatabase
}

// EmsHandler returns all ems
func (e Ems) EmsHandler(w http.ResponseWriter, r *http.Request) {
	dbResp, err := e.DB.Find(context.TODO(), bson.M{})
	if err != nil {
		config.ErrorStatus("failed to get ems", http.StatusNotFound, w, err)
		return
	}
	// Because the frontend requires that the data elements inside models.Ems exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Ems{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// EmsByIDHandler returns a ems by ID
func (e Ems) EmsByIDHandler(w http.ResponseWriter, r *http.Request) {
	emsID := mux.Vars(r)["ems_id"]

	zap.S().Debugf("ems_id: %v", emsID)

	cID, err := primitive.ObjectIDFromHex(emsID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	dbResp, err := e.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get ems by ID", http.StatusNotFound, w, err)
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

// EmsByUserIDHandler returns all ems that contain the given userID
func (e Ems) EmsByUserIDHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]
	activeCommunityID := r.URL.Query().Get("active_community_id")

	zap.S().Debugf("user_id: '%v'", userID)
	zap.S().Debugf("active_community: '%v'", activeCommunityID)

	var dbResp []models.Ems

	// If the user is in a community then we want to search for ems that
	// are in that same community. This way each user can have different ems
	// across different communities.
	//
	// Likewise, if the user is not in a community, then we will display only the ems
	// that are not in a community
	var err error
	if activeCommunityID != "" && activeCommunityID != "null" && activeCommunityID != "undefined" {
		dbResp, err = e.DB.Find(context.TODO(), bson.M{
			"ems.userID":            userID,
			"ems.activeCommunityID": activeCommunityID,
		})
		if err != nil {
			config.ErrorStatus("failed to get ems with active community id", http.StatusNotFound, w, err)
			return
		}
	} else {
		dbResp, err = e.DB.Find(context.TODO(), bson.M{
			"ems.userID": userID,
			"$or": []bson.M{
				{"ems.activeCommunityID": nil},
				{"ems.activeCommunityID": ""},
			},
		})
		if err != nil {
			config.ErrorStatus("failed to get ems with empty active community id", http.StatusNotFound, w, err)
			return
		}
	}

	// Because the frontend requires that the data elements inside models.Emss exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Ems{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
