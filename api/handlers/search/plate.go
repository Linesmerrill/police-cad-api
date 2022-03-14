package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"go.mongodb.org/mongo-driver/bson"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

type Plate struct {
	DB databases.VehicleDatabase
}

// Plate Search Handler ...
func (p Plate) PlateSearchHandler(w http.ResponseWriter, r *http.Request) {
	plateNumber := r.URL.Query().Get("plate")
	communityID := r.URL.Query().Get("community_id")

	zap.S().Debugf("plate: %v, community_id: %v", plateNumber, communityID)

	var dbResp []models.Vehicle
	var err error
	if communityID != "" {
		dbResp, err = p.DB.Find(context.TODO(), bson.M{
			"$and": []bson.M{
				bson.M{
					"$text": bson.M{
						"$search": fmt.Sprintf("%v", plateNumber),
					},
				},
				bson.M{
					"vehicle.activeCommunityID": communityID,
				},
			},
		})
	} else {
		dbResp, err = p.DB.Find(context.TODO(), bson.M{
			"$and": []bson.M{
				bson.M{
					"$text": bson.M{
						"$search": fmt.Sprintf("%v", plateNumber),
					},
				},
				bson.M{"$or": []bson.M{
					bson.M{"civilian.activeCommunityID": ""},
					bson.M{"civilian.activeCommunityID": nil},
				}},
			},
		})
	}

	if err != nil {
		config.ErrorStatus("failed to get vehicle", http.StatusNotFound, w, err)
		return
	}

	// Because the frontend requires that the data elements inside models.Vehicle exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Vehicle{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
