package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/linesmerrill/police-cad-api/config"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"go.uber.org/zap"

	"github.com/gorilla/mux"

	"github.com/linesmerrill/police-cad-api/mongodb/collections"
)

// CommunityHandler returns a community given a communityID
func (a *App) CommunityHandler(w http.ResponseWriter, r *http.Request) {
	commID := mux.Vars(r)["community_id"]

	zap.S().Debugf("community_id: %v", commID)

	collectionDba := collections.NewCommunityDatabase(a.DB)
	cID, err := primitive.ObjectIDFromHex(commID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
	}

	dbResp, err := collectionDba.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
	}

	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// CommunityByOwnerHandler creates a route that will return a community that contains the specified
// ownerID
func (a *App) CommunityByOwnerHandler(w http.ResponseWriter, r *http.Request) {
	commID := mux.Vars(r)["community_id"]
	ownerID := mux.Vars(r)["owner_id"]

	zap.S().Debugf("community_id: %v, owner_id: %v", commID, ownerID)

	collectionDba := collections.NewCommunityDatabase(a.DB)
	cID, err := primitive.ObjectIDFromHex(commID)
	if err != nil {
		zap.S().With(err).Error("failed to get objectID from Hex")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(fmt.Sprintf(`{"response": "failed to get objectID from Hex, %v"}`, err)))
		return
	}
	dbResp, err := collectionDba.FindOne(context.Background(), bson.M{"_id": cID, "community.ownerID": ownerID})
	if err != nil {
		zap.S().With(err).Error("failed to get community by ID and ownerID")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(fmt.Sprintf(`{"response": "failed to get community by ID and ownerID, %v"}`, err)))
		return
	}

	b, err := json.Marshal(dbResp)
	if err != nil {
		zap.S().With(err).Error("failed to marshal response")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(fmt.Sprintf(`{"error": "failed to marshal response, %v"}`, err)))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
