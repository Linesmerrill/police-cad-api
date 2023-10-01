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

// Warrant exported for testing purposes
type Warrant struct {
	DB databases.WarrantDatabase
}

// WarrantList paginated response with a list of items and next page id
type WarrantList struct {
	Items      []*models.Warrant `json:"items"`
	NextPageID int               `json:"next_page_id,omitempty" example:"10"`
}

// WarrantHandler returns all warrants
func (v Warrant) WarrantHandler(w http.ResponseWriter, r *http.Request) {
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		zap.S().Warnf(fmt.Sprintf("limit not set, using default of %v, err: %v", Limit|10, err))
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)
	dbResp, err := v.DB.Find(context.TODO(), bson.D{}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
	if err != nil {
		config.ErrorStatus("failed to get warrants", http.StatusNotFound, w, err)
		return
	}

	// Because the frontend requires that the data elements inside models.Warrants exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Warrant{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// WarrantByIDHandler returns a warrant by ID
func (v Warrant) WarrantByIDHandler(w http.ResponseWriter, r *http.Request) {
	civID := mux.Vars(r)["warrant_id"]

	zap.S().Debugf("warrant_id: %v", civID)

	cID, err := primitive.ObjectIDFromHex(civID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	dbResp, err := v.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get warrant by ID", http.StatusNotFound, w, err)
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

// WarrantsByUserIDHandler returns all warrants that contain the given userID
func (v Warrant) WarrantsByUserIDHandler(w http.ResponseWriter, r *http.Request) {
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

	var dbResp []models.Warrant

	// If the user is in a community then we want to search for warrants that
	// are in that same community. This way each user can have different warrants
	// across different communities.
	//
	// Likewise, if the user is not in a community, then we will display only the warrants
	// that are not in a community
	err = nil
	dbResp, err = v.DB.Find(context.TODO(), bson.M{
		"warrant.accusedID": userID,
	}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
	if err != nil {
		config.ErrorStatus("failed to get warrants", http.StatusNotFound, w, err)
		return
	}

	// Because the frontend requires that the data elements inside models.Warrants exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Warrant{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
