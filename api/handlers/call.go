package handlers

import (
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

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Call exported for testing purposes
type Call struct {
	DB databases.CallDatabase
}

// CallHandler returns all calls
func (c Call) CallHandler(w http.ResponseWriter, r *http.Request) {
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
	
	dbResp, err := c.DB.Find(ctx, bson.D{}, opts)
	if err != nil {
		config.ErrorStatus("failed to get calls", http.StatusNotFound, w, err)
		return
	}
	// Because the frontend requires that the data elements inside models.Calls exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Call{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// CallByIDHandler returns a call by ID
func (c Call) CallByIDHandler(w http.ResponseWriter, r *http.Request) {
	civID := mux.Vars(r)["call_id"]

	zap.S().Debugf("call_id: %v", civID)

	cID, err := primitive.ObjectIDFromHex(civID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for database query
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	dbResp, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get call by ID", http.StatusNotFound, w, err)
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

// CallsByCommunityIDHandler returns all calls that contain the given communityID
// Deprecated: Use CallsByCommunityIDHandlerV2 instead which supports proper pagination with totalCount
func (c Call) CallsByCommunityIDHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["community_id"]
	status := r.URL.Query().Get("status")
	zap.S().Debugf("community_id: '%v'", communityID)
	zap.S().Debugf("status: '%v'", status)

	// Validate communityID is provided
	if communityID == "" || communityID == "null" || communityID == "undefined" {
		config.ErrorStatus("community_id is required", http.StatusBadRequest, w, fmt.Errorf("community_id is required"))
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Build filter - always require communityID to prevent full collection scans
	filter := bson.M{
		"call.communityID": communityID,
	}
	if status != "" {
		statusB, err := strconv.ParseBool(status)
		if err != nil {
			config.ErrorStatus("invalid status value", http.StatusBadRequest, w, err)
			return
		}
		filter["call.status"] = statusB
	}

	// Add pagination/limit to prevent loading all calls at once
	limitParam := r.URL.Query().Get("limit")
	limit := int64(100) // Default limit
	if limitParam != "" {
		if l, err := strconv.ParseInt(limitParam, 10, 64); err == nil && l > 0 && l <= 1000 {
			limit = l
		}
	}

	// Sort by most recent first (by _id which is ObjectID and includes timestamp)
	findOptions := options.Find().
		SetLimit(limit).
		SetSort(bson.M{"_id": -1}) // Most recent first (ObjectID includes timestamp)

	dbResp, err := c.DB.Find(ctx, filter, findOptions)
	if err != nil {
		config.ErrorStatus("failed to get calls with community id", http.StatusNotFound, w, err)
		return
	}

	// Because the frontend requires that the data elements inside models.Calls exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Call{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// CallsByCommunityIDHandlerV2 returns all calls that contain the given communityID with pagination support
// Returns: { data: []Call, totalCount: int, page: int, limit: int }
func (c Call) CallsByCommunityIDHandlerV2(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["community_id"]
	status := r.URL.Query().Get("status")
	zap.S().Debugf("community_id: '%v'", communityID)
	zap.S().Debugf("status: '%v'", status)

	// Validate communityID is provided
	if communityID == "" || communityID == "null" || communityID == "undefined" {
		config.ErrorStatus("community_id is required", http.StatusBadRequest, w, fmt.Errorf("community_id is required"))
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Build filter - always require communityID to prevent full collection scans
	filter := bson.M{
		"call.communityID": communityID,
	}
	if status != "" {
		statusB, err := strconv.ParseBool(status)
		if err != nil {
			config.ErrorStatus("invalid status value", http.StatusBadRequest, w, err)
			return
		}
		filter["call.status"] = statusB
	}

	// Add pagination/limit to prevent loading all calls at once
	limitParam := r.URL.Query().Get("limit")
	limit := int64(100) // Default limit
	if limitParam != "" {
		if l, err := strconv.ParseInt(limitParam, 10, 64); err == nil && l > 0 && l <= 1000 {
			limit = l
		}
	}

	// Page parameter (1-based)
	pageParam := r.URL.Query().Get("page")
	page := int64(1) // Default to first page
	if pageParam != "" {
		if p, err := strconv.ParseInt(pageParam, 10, 64); err == nil && p > 0 {
			page = p
		}
	}
	skip := (page - 1) * limit

	// Get total count for pagination
	totalCount, err := c.DB.CountDocuments(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to count calls", http.StatusInternalServerError, w, err)
		return
	}

	// Sort by most recent first (by _id which is ObjectID and includes timestamp)
	findOptions := options.Find().
		SetLimit(limit).
		SetSkip(skip).
		SetSort(bson.M{"_id": -1}) // Most recent first (ObjectID includes timestamp)

	dbResp, err := c.DB.Find(ctx, filter, findOptions)
	if err != nil {
		config.ErrorStatus("failed to get calls with community id", http.StatusNotFound, w, err)
		return
	}

	// Because the frontend requires that the data elements inside models.Calls exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.Call{}
	}

	// Return paginated response with totalCount
	response := map[string]interface{}{
		"data":       dbResp,
		"totalCount": totalCount,
		"page":       page,
		"limit":      limit,
	}
	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// CreateCallHandler creates a new call
func (c Call) CreateCallHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody models.CallDetails
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	requestBody.CreatedAt = primitive.NewDateTimeFromTime(time.Now())
	requestBody.UpdatedAt = primitive.NewDateTimeFromTime(time.Now())

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	newCall := bson.M{
		"_id":  primitive.NewObjectID(),
		"call": requestBody,
		"__v":  0,
	}

	_, err := c.DB.InsertOne(ctx, newCall)
	if err != nil {
		config.ErrorStatus("failed to create call", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Call created successfully",
		"call":    newCall,
	})
}

// UpdateCallByIDHandler updates a call by ID
func (c Call) UpdateCallByIDHandler(w http.ResponseWriter, r *http.Request) {
	callID := mux.Vars(r)["call_id"]

	var requestBody map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(callID)
	if err != nil {
		config.ErrorStatus("invalid call ID", http.StatusBadRequest, w, err)
		return
	}

	// Add the updatedAt field to the requestBody
	requestBody["updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	// Prefix all keys in requestBody with "call."
	updateFields := bson.M{}
	for key, value := range requestBody {
		updateFields["call."+key] = value
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	update := bson.M{
		"$set": updateFields,
	}

	filter := bson.M{"_id": cID}
	_, err = c.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to update call", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Call updated successfully",
	})
}

// DeleteCallByIDHandler deletes a call by ID
func (c Call) DeleteCallByIDHandler(w http.ResponseWriter, r *http.Request) {
	callID := mux.Vars(r)["call_id"]

	cID, err := primitive.ObjectIDFromHex(callID)
	if err != nil {
		config.ErrorStatus("invalid call ID", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"_id": cID}
	err = c.DB.DeleteOne(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to delete call", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Call deleted successfully",
	})
}

// AddCallNoteHandler adds a new note to a call
func (c Call) AddCallNoteHandler(w http.ResponseWriter, r *http.Request) {
	callID := mux.Vars(r)["call_id"]

	var newNote models.CallNotes
	if err := json.NewDecoder(r.Body).Decode(&newNote); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(callID)
	if err != nil {
		config.ErrorStatus("invalid call ID", http.StatusBadRequest, w, err)
		return
	}

	newNote.ID = primitive.NewObjectID().Hex()
	newNote.CreatedAt = primitive.NewDateTimeFromTime(time.Now())
	newNote.UpdatedAt = primitive.NewDateTimeFromTime(time.Now())

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Check if call.callNotes is null and initialize it if necessary
	filter := bson.M{"_id": cID}

	callDoc, err := c.DB.FindOne(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to find call", http.StatusInternalServerError, w, err)
		return
	}

	if callDoc.Details.CallNotes == nil {
		update := bson.M{
			"$set": bson.M{
				"call.callNotes": []models.CallNotes{},
			},
		}
		_, err = c.DB.UpdateOne(ctx, filter, update)
		if err != nil {
			config.ErrorStatus("failed to initialize call notes", http.StatusInternalServerError, w, err)
			return
		}
	}

	// Add the new note to call.callNotes
	update := bson.M{
		"$push": bson.M{
			"call.callNotes": newNote,
		},
	}

	_, err = c.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to add call note", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Call note added successfully",
	})
}

// EditCallNoteByIDHandler edits a note of a call by note ID
func (c Call) EditCallNoteByIDHandler(w http.ResponseWriter, r *http.Request) {
	callID := mux.Vars(r)["call_id"]
	noteID := mux.Vars(r)["note_id"]

	var requestBody map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(callID)
	if err != nil {
		config.ErrorStatus("invalid call ID", http.StatusBadRequest, w, err)
		return
	}

	// Add the updatedAt field to the requestBody
	requestBody["call.callNotes.$.updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Find the call by ID and update the specific note by note ID
	filter := bson.M{"_id": cID, "call.callNotes._id": noteID}
	update := bson.M{
		"$set": bson.M{
			"call.callNotes.$.note":      requestBody["note"],
			"call.callNotes.$.updatedAt": requestBody["call.callNotes.$.updatedAt"],
		},
	}

	_, err = c.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to update call note", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Call note updated successfully",
	})
}

// DeleteCallNoteByIDHandler deletes a note of a call by note ID
func (c Call) DeleteCallNoteByIDHandler(w http.ResponseWriter, r *http.Request) {
	callID := mux.Vars(r)["call_id"]
	noteID := mux.Vars(r)["note_id"]

	cID, err := primitive.ObjectIDFromHex(callID)
	if err != nil {
		config.ErrorStatus("invalid call ID", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"_id": cID}
	update := bson.M{
		"$pull": bson.M{
			"call.callNotes": bson.M{"_id": noteID},
		},
	}

	_, err = c.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to delete call note", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Call note deleted successfully",
	})
}
