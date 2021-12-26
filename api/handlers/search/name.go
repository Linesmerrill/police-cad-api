package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/databases"
)

// Name ...
type Name struct {
	DB databases.CivilianDatabase
}

// NameSearchHandler ...
func (n Name) NameSearchHandler(w http.ResponseWriter, r *http.Request) {

	firstName := r.URL.Query().Get("first_name")
	lastName := r.URL.Query().Get("last_name")
	dob := r.URL.Query().Get("dob")
	communityID := r.URL.Query().Get("community_id")

	zap.S().Debugf("first_name: %v, last_name: %v, dob: %v, community_id: %v", firstName, lastName, dob, communityID)

	//TODO need to check if user is in community before searching just the name
	dbResp, err := n.DB.Find(context.TODO(), bson.M{
		"$and": []bson.M{
			bson.M{
				"$text": bson.M{
					"$search": fmt.Sprintf("%v %v", firstName, lastName),
				},
			},
			bson.M{
				"civilian.birthday": dob,
			},
			bson.M{"$or": []bson.M{
				bson.M{"civilian.activeCommunityID": ""},
				bson.M{"civilian.activeCommunityID": nil},
			}},
		},
	})
	if err != nil {
		config.ErrorStatus("failed to get name", http.StatusNotFound, w, err)
		return
	}
	// Because the frontend requires that the data elements inside models.User exist, if
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
