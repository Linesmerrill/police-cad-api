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

var (
	// Page denotes the starting Page for pagination results
	Page = 0
)

// Civilian exported for testing purposes
type Civilian struct {
	DB databases.CivilianDatabase
}

// CivilianHandler returns all civilians
func (c Civilian) CivilianHandler(w http.ResponseWriter, r *http.Request) {
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		zap.S().Warnf(fmt.Sprintf("limit not set, using default of %v, err: %v", Limit|10, err))
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)
	dbResp, err := c.DB.Find(context.TODO(), bson.D{}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
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
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		zap.S().Warnf(fmt.Sprintf("limit not set, using default of %v", Limit|10))
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)

	zap.S().Debugf("user_id: '%v'", userID)
	zap.S().Debugf("active_community: '%v'", activeCommunityID)

	var dbResp []models.Civilian

	// If the user is in a community then we want to search for civilians that
	// are in that same community. This way each user can have different civilians
	// across different communities.
	//
	// Likewise, if the user is not in a community, then we will display only the civilians
	// that are not in a community
	err = nil
	if activeCommunityID != "" && activeCommunityID != "null" && activeCommunityID != "undefined" {
		dbResp, err = c.DB.Find(context.TODO(), bson.M{
			"civilian.userID":            userID,
			"civilian.activeCommunityID": activeCommunityID,
		}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
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
		}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
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

// CiviliansByNameSearchHandler returns paginated list of civilians that match the give name
func (c Civilian) CiviliansByNameSearchHandler(w http.ResponseWriter, r *http.Request) {
	firstName := r.URL.Query().Get("first_name")
	lastName := r.URL.Query().Get("last_name")
	dateOfBirth := r.URL.Query().Get("date_of_birth")             // optional
	activeCommunityID := r.URL.Query().Get("active_community_id") // optional
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		zap.S().Warnf(fmt.Sprintf("limit not set, using default of %v", Limit|10))
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)

	zap.S().Debugf("first_name: '%v', last_name: '%v', date_of_birth: '%v'", firstName, lastName, dateOfBirth)
	zap.S().Debugf("active_community: '%v'", activeCommunityID)

	var dbResp []models.Civilian

	// If the user is in a community then we want to search for civilians that
	// are in that same community. This way each user can have different civilians
	// across different communities.
	//
	// Likewise, if the user is not in a community, then we will display only the civilians
	// that are not in a community
	err = nil
	if activeCommunityID != "" && activeCommunityID != "null" && activeCommunityID != "undefined" {
		dbResp, err = c.DB.Find(context.TODO(), bson.M{
			"$text": bson.M{
				"$search": fmt.Sprintf("%s %s", firstName, lastName),
			},
			"civilian.activeCommunityID": activeCommunityID,
		}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
		if err != nil {
			config.ErrorStatus("failed to get civilian name search with active community id", http.StatusNotFound, w, err)
			return
		}
	} else {
		dbResp, err = c.DB.Find(context.TODO(), bson.M{
			"civilian.firstName": firstName,
			"civilian.lastName":  lastName,
			"civilian.birthday":  dateOfBirth,
			"$or": []bson.M{
				{"civilian.activeCommunityID": nil},
				{"civilian.activeCommunityID": ""},
			},
		}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
		if err != nil {
			config.ErrorStatus("failed to get civilian name search with empty active community id", http.StatusNotFound, w, err)
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

func getPage(Page int, r *http.Request) int {
	if r.URL.Query().Get("page") == "" {
		zap.S().Warnf("page not set, using default of %v", Page)
	} else {
		var err error
		Page, err = strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil {
			zap.S().Errorf(fmt.Sprintf("error parsing page number: %v", err))
		}
		if Page < 0 {
			zap.S().Warnf(fmt.Sprintf("cannot process page number less than 1. Got: %v", Page))
			return 0
		}
	}
	return Page
}
