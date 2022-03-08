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

type User struct {
	DB databases.UserDatabase
}

/*
This funciton is passed on into the search handlers via the api
handler, so there is the ability to verify a user is in a given
community, as I saw your TODO comment in there.
*/
func (u User) VerifyInCommunity() func(string, string) bool {
	return func(userID, communityID string) bool {
		cID, err := primitive.ObjectIDFromHex(userID)
		if err != nil {
			return false
		}

		dbResp, err := u.DB.FindOne(context.Background(), bson.M{"_id": cID})
		if err != nil {
			return false
		}

		if dbResp.Details.ActiveCommunity != communityID {
			return false
		}
		return true
	}
}

// UserHandler returns a user given a userID
func (u User) UserHandler(w http.ResponseWriter, r *http.Request) {
	commID := mux.Vars(r)["user_id"]

	zap.S().Debugf("user_id: %v", commID)

	cID, err := primitive.ObjectIDFromHex(commID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	dbResp, err := u.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
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

// UsersFindAllHandler runs a mongo find{} query to find all
func (u User) UsersFindAllHandler(w http.ResponseWriter, r *http.Request) {
	commID := mux.Vars(r)["active_community_id"]

	zap.S().Debugf("active_community_id: %v", commID)

	dbResp, err := u.DB.Find(context.Background(), bson.M{"user.activeCommunity": commID})
	if err != nil {
		config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
		return
	}
	// Because the frontend requires that the data elements inside models.User exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.User{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
