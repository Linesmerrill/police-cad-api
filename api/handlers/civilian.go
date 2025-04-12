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

// CiviliansByNameSearchHandler returns paginated list of civilians that match the given name
func (c Civilian) CiviliansByNameSearchHandler(w http.ResponseWriter, r *http.Request) {
	firstName := r.URL.Query().Get("first_name")
	lastName := r.URL.Query().Get("last_name")
	name := r.URL.Query().Get("name")
	activeCommunityID := r.URL.Query().Get("active_community_id") // optional
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		zap.S().Warnf(fmt.Sprintf("limit not set, using default of %v", Limit|10))
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)

	zap.S().Debugf("first_name: '%v', last_name: '%v'", firstName, lastName)
	zap.S().Debugf("active_community: '%v'", activeCommunityID)

	var dbResp []models.Civilian

	// If the user is in a community then we want to search for civilians that
	// are in that same community. This way each user can have different civilians
	// across different communities.
	//
	// Likewise, if the user is not in a community, then we will display only the civilians
	// that are not in a community
	err = nil
	var orConditions []bson.M

	if firstName != "" {
		orConditions = append(orConditions, bson.M{"civilian.firstName": bson.M{"$regex": firstName, "$options": "i"}})
	}
	if lastName != "" {
		orConditions = append(orConditions, bson.M{"civilian.lastName": bson.M{"$regex": lastName, "$options": "i"}})
	}
	if name != "" {
		orConditions = append(orConditions, bson.M{"civilian.name": bson.M{"$regex": name, "$options": "i"}})
	}

	filter := bson.M{}
	if len(orConditions) > 0 {
		filter["$or"] = orConditions
	}
	if activeCommunityID != "" {
		filter["civilian.activeCommunityID"] = activeCommunityID
	}

	dbResp, err = c.DB.Find(context.TODO(), filter, &options.FindOptions{Limit: &limit64, Skip: &skip64})
	if err != nil {
		config.ErrorStatus("failed to get civilian name search", http.StatusNotFound, w, err)
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

// CreateCivilianHandler creates a civilian
func (c Civilian) CreateCivilianHandler(w http.ResponseWriter, r *http.Request) {
	var civilian models.Civilian
	if err := json.NewDecoder(r.Body).Decode(&civilian.Details); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	civilian.ID = primitive.NewObjectID()
	civilian.Details.CreatedAt = primitive.NewDateTimeFromTime(time.Now())
	civilian.Details.UpdatedAt = civilian.Details.CreatedAt

	_, err := c.DB.InsertOne(context.Background(), civilian)
	if err != nil {
		config.ErrorStatus("failed to create civilian", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Civilian created successfully",
		"id":      civilian.ID.Hex(),
	})
}

// UpdateCivilianHandler updates a civilian's details
func (c Civilian) UpdateCivilianHandler(w http.ResponseWriter, r *http.Request) {
	civID := mux.Vars(r)["civilian_id"]

	cID, err := primitive.ObjectIDFromHex(civID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Decode the incoming changes
	var updatedFields map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updatedFields); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Prepare the update document
	update := bson.M{}
	for key, value := range updatedFields {
		update["civilian."+key] = value
	}

	// Add the updatedAt field to track the update time
	update["civilian.updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	// Update the civilian in the database
	err = c.DB.UpdateOne(context.Background(), bson.M{"_id": cID}, bson.M{"$set": update})
	if err != nil {
		config.ErrorStatus("failed to update civilian", http.StatusInternalServerError, w, err)
		return
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Civilian updated successfully",
	})
}

// DeleteCivilianHandler deletes a civilian by ID
func (c Civilian) DeleteCivilianHandler(w http.ResponseWriter, r *http.Request) {
	civID := mux.Vars(r)["civilian_id"]

	cID, err := primitive.ObjectIDFromHex(civID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	err = c.DB.DeleteOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to delete civilian", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Civilian deleted successfully",
	})
}

// AddCriminalHistoryHandler adds a new criminal history item to a civilian
func (c Civilian) AddCriminalHistoryHandler(w http.ResponseWriter, r *http.Request) {
	civilianID := mux.Vars(r)["civilian_id"]

	cID, err := primitive.ObjectIDFromHex(civilianID)
	if err != nil {
		config.ErrorStatus("invalid civilian ID", http.StatusBadRequest, w, err)
		return
	}

	var newHistory models.CriminalHistory
	if err := json.NewDecoder(r.Body).Decode(&newHistory); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Generate a new ObjectID for the criminal history item
	newHistory.ID = primitive.NewObjectID()
	newHistory.CreatedAt = primitive.NewDateTimeFromTime(time.Now())

	// Check if the criminalHistory field exists and initialize it if null
	filter := bson.M{"_id": cID}
	update := bson.M{
		"$set": bson.M{"civilian.criminalHistory": bson.A{}},
	}
	_ = c.DB.UpdateOne(context.Background(), filter, update) // Ignore errors here, it will error if null, which just means it an empty record

	// Push the new criminal history item
	update = bson.M{"$push": bson.M{"civilian.criminalHistory": newHistory}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to add criminal history", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Criminal history added successfully",
	})
}

// UpdateCriminalHistoryHandler updates a specific criminal history item
func (c Civilian) UpdateCriminalHistoryHandler(w http.ResponseWriter, r *http.Request) {
	civilianID := mux.Vars(r)["civilian_id"]
	citationID := mux.Vars(r)["citation_id"]

	cID, err := primitive.ObjectIDFromHex(civilianID)
	if err != nil {
		config.ErrorStatus("invalid civilian ID", http.StatusBadRequest, w, err)
		return
	}

	historyID, err := primitive.ObjectIDFromHex(citationID) // Convert citationID to ObjectID
	if err != nil {
		config.ErrorStatus("invalid criminal history ID", http.StatusBadRequest, w, err)
		return
	}

	var updatedFields map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updatedFields); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Add the updatedAt field to track the update time
	updatedFields["civilian.criminalHistory.$.updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	filter := bson.M{"_id": cID, "civilian.criminalHistory._id": historyID} // Match by the new ID field
	update := bson.M{"$set": updatedFields}                                 // Dynamically update only the provided fields

	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update criminal history", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Criminal history updated successfully",
	})
}
