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

// Civilian exported for testing purposes
type Civilian struct {
	DB databases.CivilianDatabase
}

// CivilianHandler returns all civilians
func (c Civilian) CivilianHandler(w http.ResponseWriter, r *http.Request) {
	dbResp, err := c.DB.Find(context.TODO(), bson.M{})
	if err != nil {
		config.ErrorStatus("failed to get civilians", http.StatusNotFound, w, err)
		return
	}
	// Because the frontend requires that the data elements inside models.Civilians exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Civilian{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// CivilianByIDHandler returns a civilian by ID
func (c Civilian) CivilianByIDHandler(w http.ResponseWriter, r *http.Request) {
	civID := mux.Vars(r)["civilian_id"]

	zap.S().Debugf("civilian_id: %v", civID)

	cID, err := primitive.ObjectIDFromHex(civID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	dbResp, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get civilian by ID", http.StatusNotFound, w, err)
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

// CiviliansByUserIDHandler returns all civilians that contain the given userID
func (c Civilian) CiviliansByUserIDHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]
	activeCommunityID := r.URL.Query().Get("active_community_id")

	zap.S().Debugf("user_id: '%v'", userID)
	zap.S().Debugf("active_community: '%v'", activeCommunityID)

	var dbResp []models.Civilian

	// If the user is in a community then we want to search for civilians that
	// are in that same community. This way each user can have different civilians
	// across different communities.
	//
	// Likewise, if the user is not in a community, then we will display only the civilians
	// that are not in a community
	var err error
	if activeCommunityID != "" && activeCommunityID != "null" && activeCommunityID != "undefined" {
		dbResp, err = c.DB.Find(context.TODO(), bson.M{
			"civilian.userID":            userID,
			"civilian.activeCommunityID": activeCommunityID,
		})
		if err != nil {
			config.ErrorStatus("failed to get civilians with active community id", http.StatusNotFound, w, err)
			return
		}
	} else {
		dbResp, err = c.DB.Find(context.TODO(), bson.M{
			"civilian.userID": userID,
			"$or": []bson.M{
				{"civilian.activeCommunityID": nil},
				{"civilian.activeCommunityID": ""},
			},
		})
		if err != nil {
			config.ErrorStatus("failed to get civilians with empty active community id", http.StatusNotFound, w, err)
			return
		}
	}

	// Because the frontend requires that the data elements inside models.Civilians exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Civilian{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
