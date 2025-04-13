package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Firearm exported for testing purposes
type Firearm struct {
	DB databases.FirearmDatabase
}

// FirearmList paginated response with a list of items and next page id
type FirearmList struct {
	Items      []*models.Firearm `json:"items"`
	NextPageID int               `json:"next_page_id,omitempty" example:"10"`
}

// FirearmHandler returns all firearms
func (f Firearm) FirearmHandler(w http.ResponseWriter, r *http.Request) {
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		zap.S().Warnf(fmt.Sprintf("limit not set, using default of %v, err: %v", Limit|10, err))
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)
	dbResp, err := f.DB.Find(context.TODO(), bson.D{}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
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
func (f Firearm) FirearmByIDHandler(w http.ResponseWriter, r *http.Request) {
	civID := mux.Vars(r)["firearm_id"]

	zap.S().Debugf("firearm_id: %v", civID)

	cID, err := primitive.ObjectIDFromHex(civID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	dbResp, err := f.DB.FindOne(context.Background(), bson.M{"_id": cID})
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
func (f Firearm) FirearmsByUserIDHandler(w http.ResponseWriter, r *http.Request) {
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

	var dbResp []models.Firearm

	// If the user is in a community then we want to search for firearms that
	// are in that same community. This way each user can have different firearms
	// across different communities.
	//
	// Likewise, if the user is not in a community, then we will display only the firearms
	// that are not in a community
	err = nil
	if activeCommunityID != "" && activeCommunityID != "null" && activeCommunityID != "undefined" {
		dbResp, err = f.DB.Find(context.TODO(), bson.M{
			"firearm.userID":            userID,
			"firearm.activeCommunityID": activeCommunityID,
		}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
		if err != nil {
			config.ErrorStatus("failed to get firearms with active community id", http.StatusNotFound, w, err)
			return
		}
	} else {
		dbResp, err = f.DB.Find(context.TODO(), bson.M{
			"firearm.userID": userID,
			"$or": []bson.M{
				{"firearm.activeCommunityID": nil},
				{"firearm.activeCommunityID": ""},
			},
		}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
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

// FirearmsByRegisteredOwnerIDHandler returns all firearms that contain the given registeredOwnerID
func (f Firearm) FirearmsByRegisteredOwnerIDHandler(w http.ResponseWriter, r *http.Request) {
	registeredOwnerID := mux.Vars(r)["registered_owner_id"]
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 10 // Default limit
	}
	limit64 := int64(Limit)
	Page := getPage(Page, r)
	skip64 := int64(Page * Limit)

	zap.S().Debugf("registered_owner_id: '%v'", registeredOwnerID)

	var dbResp []models.Firearm

	// Query to fetch firearms
	err = nil
	dbResp, err = f.DB.Find(context.TODO(), bson.M{
		"$or": []bson.M{
			{"firearm.registeredOwnerID": registeredOwnerID}, // Deprecated, use linkedCivilianID
			{"firearm.linkedCivilianID": registeredOwnerID},
		},
	}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
	if err != nil {
		config.ErrorStatus("failed to get firearms with empty registered owner id", http.StatusNotFound, w, err)
		return
	}

	// Count total firearms for pagination
	total, err := f.DB.CountDocuments(context.TODO(), bson.M{
		"$or": []bson.M{
			{"firearm.registeredOwnerID": registeredOwnerID},
			{"firearm.linkedCivilianID": registeredOwnerID},
		},
	})
	if err != nil {
		config.ErrorStatus("failed to count firearms", http.StatusInternalServerError, w, err)
		return
	}

	// Ensure the response is always an array
	if len(dbResp) == 0 {
		dbResp = []models.Firearm{}
	}

	// Build the response
	response := map[string]interface{}{
		"limit":    Limit,
		"firearms": dbResp,
		"page":     Page,
		"total":    total,
	}

	// Marshal and send the response
	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// CreateFirearmHandler creates a new firearm
func (f Firearm) CreateFirearmHandler(w http.ResponseWriter, r *http.Request) {
	var firearm models.Firearm
	if err := json.NewDecoder(r.Body).Decode(&firearm.Details); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	firearm.ID = primitive.NewObjectID()
	firearm.Details.CreatedAt = primitive.NewDateTimeFromTime(time.Now())
	firearm.Details.UpdatedAt = firearm.Details.CreatedAt

	_, err := f.DB.InsertOne(context.Background(), firearm)
	if err != nil {
		config.ErrorStatus("failed to create firearm", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Firearm created successfully",
		"id":      firearm.ID.Hex(),
	})
}

// UpdateFirearmHandler updates a firearm's details
func (f Firearm) UpdateFirearmHandler(w http.ResponseWriter, r *http.Request) {
	firearmID := mux.Vars(r)["firearm_id"]

	fID, err := primitive.ObjectIDFromHex(firearmID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Retrieve the existing firearm data
	existingFirearm, err := f.DB.FindOne(context.Background(), bson.M{"_id": fID})
	if err != nil {
		config.ErrorStatus("failed to find firearm", http.StatusNotFound, w, err)
		return
	}

	// Convert existing firearm details to a map
	existingDetailsMap := make(map[string]interface{})
	data, _ := json.Marshal(existingFirearm.Details)
	json.Unmarshal(data, &existingDetailsMap)

	// Decode the request body into a map
	var updateData map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updateData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Merge the update data with the existing firearm data
	for key, value := range updateData {
		existingDetailsMap[key] = value
	}
	existingDetailsMap["updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	// Convert the map back to FirearmDetails
	updatedDetails := models.FirearmDetails{}
	data, _ = json.Marshal(existingDetailsMap)
	json.Unmarshal(data, &updatedDetails)

	// Update the firearm in the database
	err = f.DB.UpdateOne(context.Background(), bson.M{"_id": fID}, bson.M{"$set": bson.M{"firearm": updatedDetails}})
	if err != nil {
		config.ErrorStatus("failed to update firearm", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Firearm updated successfully",
	})
}

// DeleteFirearmHandler deletes a firearm by its ID
func (f Firearm) DeleteFirearmHandler(w http.ResponseWriter, r *http.Request) {
	firearmID := mux.Vars(r)["firearm_id"]

	fID, err := primitive.ObjectIDFromHex(firearmID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Delete the firearm from the database
	err = f.DB.DeleteOne(context.Background(), bson.M{"_id": fID})
	if err != nil {
		config.ErrorStatus("failed to delete firearm", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Firearm deleted successfully",
	})
}

// FirearmsSearchHandler searches for firearms based on name or serial number
func (f Firearm) FirearmsSearchHandler(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	serialNumber := r.URL.Query().Get("serialNumber")
	communityID := r.URL.Query().Get("communityId")
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 10 // Default limit
	}
	limit64 := int64(Limit)
	Page := getPage(Page, r)
	skip64 := int64(Page * Limit)

	var dbResp []models.Firearm

	// Build the query
	query := bson.M{
		"$or": []bson.M{
			{"firearm.name": bson.M{"$regex": name, "$options": "i"}},
			{"firearm.serialNumber": bson.M{"$regex": serialNumber, "$options": "i"}},
		},
	}
	if communityID != "" {
		query["firearm.activeCommunityID"] = communityID
	}

	// Fetch firearms
	dbResp, err = f.DB.Find(context.TODO(), query, &options.FindOptions{Limit: &limit64, Skip: &skip64})
	if err != nil {
		config.ErrorStatus("failed to search firearms", http.StatusNotFound, w, err)
		return
	}

	// Count total firearms for pagination
	total, err := f.DB.CountDocuments(context.TODO(), query)
	if err != nil {
		config.ErrorStatus("failed to count firearms", http.StatusInternalServerError, w, err)
		return
	}

	// Ensure the response is always an array
	if len(dbResp) == 0 {
		dbResp = []models.Firearm{}
	}

	// Build the response
	response := map[string]interface{}{
		"limit":    Limit,
		"firearms": dbResp,
		"page":     Page,
		"total":    total,
	}

	// Marshal and send the response
	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
