package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
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
	
	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	
	// Empty filter with limit/skip - add sort by _id for better performance
	opts := options.Find().
		SetLimit(limit64).
		SetSkip(skip64).
		SetSort(bson.M{"_id": -1}) // Sort by _id descending (most recent first) for better index usage
	
	dbResp, err := f.DB.Find(ctx, bson.D{}, opts)
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

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	var dbResp []models.Firearm

	// If the user is in a community then we want to search for firearms that
	// are in that same community. This way each user can have different firearms
	// across different communities.
	//
	// Likewise, if the user is not in a community, then we will display only the firearms
	// that are not in a community
	err = nil
	if activeCommunityID != "" && activeCommunityID != "null" && activeCommunityID != "undefined" {
		dbResp, err = f.DB.Find(ctx, bson.M{
			"firearm.userID":            userID,
			"firearm.activeCommunityID": activeCommunityID,
		}, &options.FindOptions{Limit: &limit64, Skip: &skip64})
		if err != nil {
			config.ErrorStatus("failed to get firearms with active community id", http.StatusNotFound, w, err)
			return
		}
	} else {
		dbResp, err = f.DB.Find(ctx, bson.M{
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

// FirearmsByUserIDHandlerV2 returns paginated firearms with totalCount metadata
func (f Firearm) FirearmsByUserIDHandlerV2(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]
	activeCommunityID := r.URL.Query().Get("active_community_id")
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		zap.S().Warnf(fmt.Sprintf("limit not set, using default of %v", Limit|10))
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	var filter bson.M
	if activeCommunityID != "" && activeCommunityID != "null" && activeCommunityID != "undefined" {
		filter = bson.M{
			"firearm.userID":            userID,
			"firearm.activeCommunityID": activeCommunityID,
		}
	} else {
		filter = bson.M{
			"firearm.userID": userID,
			"$or": []bson.M{
				{"firearm.activeCommunityID": nil},
				{"firearm.activeCommunityID": ""},
			},
		}
	}

	type findResult struct {
		firearms []models.Firearm
		err      error
	}
	type countResult struct {
		count int64
		err   error
	}

	findChan := make(chan findResult, 1)
	countChan := make(chan countResult, 1)

	go func() {
		firearms, err := f.DB.Find(ctx, filter, &options.FindOptions{Limit: &limit64, Skip: &skip64})
		findChan <- findResult{firearms: firearms, err: err}
	}()

	go func() {
		count, err := f.DB.CountDocuments(ctx, filter)
		countChan <- countResult{count: count, err: err}
	}()

	findRes := <-findChan
	countRes := <-countChan

	if findRes.err != nil {
		config.ErrorStatus("failed to get firearms", http.StatusNotFound, w, findRes.err)
		return
	}

	dbResp := findRes.firearms
	var totalCount int64
	if countRes.err != nil {
		totalCount = int64(len(dbResp))
	} else {
		totalCount = countRes.count
	}

	if len(dbResp) == 0 {
		dbResp = []models.Firearm{}
	}

	totalPages := int(math.Ceil(float64(totalCount) / float64(Limit)))

	response := map[string]interface{}{
		"data":       dbResp,
		"page":       Page,
		"limit":      Limit,
		"totalCount": totalCount,
		"totalPages": totalPages,
	}

	b, err := json.Marshal(response)
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

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Build filter once (reused for both queries)
	filter := bson.M{
		"$or": []bson.M{
			{"firearm.registeredOwnerID": registeredOwnerID}, // Deprecated, use linkedCivilianID
			{"firearm.linkedCivilianID": registeredOwnerID},
		},
	}

	// Execute queries in parallel for better performance
	type findResult struct {
		firearms []models.Firearm
		err      error
	}
	type countResult struct {
		total int64
		err   error
	}

	findChan := make(chan findResult, 1)
	countChan := make(chan countResult, 1)

	// Query to fetch firearms (async)
	go func() {
		dbResp, err := f.DB.Find(ctx, filter, &options.FindOptions{Limit: &limit64, Skip: &skip64})
		findChan <- findResult{firearms: dbResp, err: err}
	}()

	// Count total firearms for pagination (async)
	go func() {
		total, err := f.DB.CountDocuments(ctx, filter)
		countChan <- countResult{total: total, err: err}
	}()

	// Wait for both queries to complete
	findRes := <-findChan
	countRes := <-countChan

	if findRes.err != nil {
		config.ErrorStatus("failed to get firearms with empty registered owner id", http.StatusNotFound, w, findRes.err)
		return
	}

	if countRes.err != nil {
		config.ErrorStatus("failed to count firearms", http.StatusInternalServerError, w, countRes.err)
		return
	}

	dbResp := findRes.firearms
	total := countRes.total

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

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Delete the firearm from the database
	err = f.DB.DeleteOne(ctx, bson.M{"_id": fID})
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
	weaponType := r.URL.Query().Get("weaponType")
	communityID := r.URL.Query().Get("communityId")
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 10 // Default limit
	}
	limit64 := int64(Limit)
	Page := getPage(Page, r)
	skip64 := int64(Page * Limit)

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	var dbResp []models.Firearm

	// Build the query - use prefix regex for better performance than full regex
	// TODO: Consider adding text index on firearm.name and firearm.serialNumber for better performance
	query := bson.M{}
	var orConditions []bson.M
	
	if name != "" {
		orConditions = append(orConditions, bson.M{"firearm.name": bson.M{"$regex": "^" + regexp.QuoteMeta(name), "$options": "i"}}) // Prefix match is faster
	}
	if serialNumber != "" {
		orConditions = append(orConditions, bson.M{"firearm.serialNumber": bson.M{"$regex": "^" + regexp.QuoteMeta(serialNumber), "$options": "i"}}) // Prefix match is faster
	}
	if weaponType != "" {
		orConditions = append(orConditions, bson.M{"firearm.weaponType": bson.M{"$regex": "^" + regexp.QuoteMeta(weaponType), "$options": "i"}}) // Prefix match is faster
	}
	
	if len(orConditions) > 0 {
		query["$or"] = orConditions
	}
	if communityID != "" {
		query["firearm.activeCommunityID"] = communityID
	}

	// Fetch firearms
	opts := options.Find().
		SetLimit(limit64).
		SetSkip(skip64).
		SetSort(bson.M{"_id": -1}) // Sort by _id for better index usage
	
	dbResp, err = f.DB.Find(ctx, query, opts)
	if err != nil {
		config.ErrorStatus("failed to search firearms", http.StatusNotFound, w, err)
		return
	}

	// Count total firearms for pagination (skip if query is empty to avoid slow count)
	var total int64
	if len(query) > 0 {
		total, err = f.DB.CountDocuments(ctx, query)
	} else {
		// Empty query - estimate from results
		total = limit64 * int64(Page + 1)
	}
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
