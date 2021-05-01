package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/linesmerrill/police-cad-api/databases"

	"github.com/linesmerrill/police-cad-api/config"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"go.uber.org/zap"

	"github.com/gorilla/mux"
)

type Community struct {
	DB databases.CommunityDatabase
}

// CommunityHandler returns a community given a communityID
func (c Community) CommunityHandler(w http.ResponseWriter, r *http.Request) {
	commID := mux.Vars(r)["community_id"]

	zap.S().Debugf("community_id: %v", commID)

	cID, err := primitive.ObjectIDFromHex(commID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	dbResp, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
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

// CommunityByOwnerHandler creates a route that will return a community that contains the specified
// ownerID
func (c Community) CommunityByOwnerHandler(w http.ResponseWriter, r *http.Request) {
	commID := mux.Vars(r)["community_id"]
	ownerID := mux.Vars(r)["owner_id"]

	zap.S().Debugf("community_id: %v, owner_id: %v", commID, ownerID)

	cID, err := primitive.ObjectIDFromHex(commID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	dbResp, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID, "community.ownerID": ownerID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID and ownerID", http.StatusNotFound, w, err)
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
