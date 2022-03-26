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

// Firearm exported for testing purposes
type Firearm struct {
	DB databases.FirearmDatabase
}

// FirearmHandler returns all firearms
func (v Firearm) FirearmHandler(w http.ResponseWriter, r *http.Request) {
	dbResp, err := v.DB.Find(context.TODO(), bson.M{})
	if err != nil {
		config.ErrorStatus("failed to get firearms", http.StatusNotFound, w, err)
		return
	}
	// Because the frontend requires that the data elements inside models.Firearms exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Firearm{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// FirearmByIDHandler returns a firearm by ID
func (v Firearm) FirearmByIDHandler(w http.ResponseWriter, r *http.Request) {
	civID := mux.Vars(r)["firearm_id"]

	zap.S().Debugf("firearm_id: %v", civID)

	cID, err := primitive.ObjectIDFromHex(civID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	dbResp, err := v.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get firearm by ID", http.StatusNotFound, w, err)
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

// FirearmsByUserIDHandler returns all firearms that contain the given userID
func (v Firearm) FirearmsByUserIDHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]
	activeCommunityID := r.URL.Query().Get("active_community_id")

	zap.S().Debugf("user_id: '%v'", userID)
	zap.S().Debugf("active_community: '%v'", activeCommunityID)

	var dbResp []models.Firearm

	// If the user is in a community then we want to search for firearms that
	// are in that same community. This way each user can have different firearms
	// across different communities.
	//
	// Likewise, if the user is not in a community, then we will display only the firearms
	// that are not in a community
	var err error
	if activeCommunityID != "" {
		dbResp, err = v.DB.Find(context.TODO(), bson.M{
			"firearm.userID":            userID,
			"firearm.activeCommunityID": activeCommunityID,
		})
		if err != nil {
			config.ErrorStatus("failed to get firearms with active community id", http.StatusNotFound, w, err)
			return
		}
	} else {
		dbResp, err = v.DB.Find(context.TODO(), bson.M{
			"firearm.userID": userID,
			"$or": []bson.M{
				{"firearm.activeCommunityID": nil},
				{"firearm.activeCommunityID": ""},
			},
		})
		if err != nil {
			config.ErrorStatus("failed to get firearms with empty active community id", http.StatusNotFound, w, err)
			return
		}
	}

	// Because the frontend requires that the data elements inside models.Firearms exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Firearm{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
