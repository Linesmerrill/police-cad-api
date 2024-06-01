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

// Spotlight exported for testing purposes
type Spotlight struct {
	DB databases.SpotlightDatabase
}

// SpotlightHandler returns all spotlights
func (e Spotlight) SpotlightHandler(w http.ResponseWriter, r *http.Request) {
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		zap.S().Warnf(fmt.Sprintf("limit not set, using default of %v, err: %v", Limit|10, err))
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)
	dbResp, err := e.DB.Find(context.TODO(), bson.D{}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
	if err != nil {
		config.ErrorStatus("failed to get spotlight", http.StatusNotFound, w, err)
		return
	}
	// Because the frontend requires that the data elements inside models.Spotlight exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Spotlight{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// SpotlightByIDHandler returns a spotlight by ID
func (e Spotlight) SpotlightByIDHandler(w http.ResponseWriter, r *http.Request) {
	spotlightID := mux.Vars(r)["spotlight_id"]

	zap.S().Debugf("spotlight_id: %v", spotlightID)

	cID, err := primitive.ObjectIDFromHex(spotlightID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	dbResp, err := e.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get spotlight by ID", http.StatusNotFound, w, err)
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
