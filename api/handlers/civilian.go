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
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"math"

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
			zap.S().Warnf(fmt.Sprintf("warning parsing page number: %v", err))
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
	// Extract civilian ID from URL parameters
	civilianID := mux.Vars(r)["civilian_id"]
	cID, err := primitive.ObjectIDFromHex(civilianID)
	if err != nil {
		config.ErrorStatus("invalid civilian ID", http.StatusBadRequest, w, err)
		return
	}

	// Decode the request body into a new CriminalHistory struct
	var newHistory models.CriminalHistory
	if err := json.NewDecoder(r.Body).Decode(&newHistory); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Validate the new criminal history entry
	// if newHistory.Charge == "" || newHistory.Date == "" {
	// 	config.ErrorStatus("charge and date are required fields", http.StatusBadRequest, w, nil)
	// 	return
	// }

	// Generate a new ObjectID and set the creation time
	newHistory.ID = primitive.NewObjectID()
	newHistory.CreatedAt = primitive.NewDateTimeFromTime(time.Now())

	// Update the civilian document using an aggregation pipeline
	filter := bson.M{"_id": cID}
	update := bson.A{
		bson.M{
			"$set": bson.M{
				"civilian.criminalHistory": bson.M{
					"$ifNull": bson.A{"$civilian.criminalHistory", bson.A{}},
				},
				"civilian.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
			},
		},
		bson.M{
			"$set": bson.M{
				"civilian.criminalHistory": bson.M{
					"$concatArrays": bson.A{
						"$civilian.criminalHistory",
						bson.A{newHistory},
					},
				},
			},
		},
	}

	// Perform the update
	result := c.DB.FindOneAndUpdate(
		context.Background(),
		filter,
		update,
	)
	if result.Err() != nil {
		if result.Err() == mongo.ErrNoDocuments {
			config.ErrorStatus("civilian not found", http.StatusNotFound, w, result.Err())
		} else {
			config.ErrorStatus("failed to add criminal history", http.StatusInternalServerError, w, result.Err())
		}
		return
	}

	// Respond with success
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":         "Criminal history added successfully",
		"criminalHistory": newHistory,
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

// DeleteCriminalHistoryHandler deletes a specific criminal history item
func (c Civilian) DeleteCriminalHistoryHandler(w http.ResponseWriter, r *http.Request) {
	// Extract `civilian_id` and `citation_id` from the URL
	civilianID := mux.Vars(r)["civilian_id"]
	citationID := mux.Vars(r)["citation_id"]

	// Convert IDs to `primitive.ObjectID`
	cID, err := primitive.ObjectIDFromHex(civilianID)
	if err != nil {
		config.ErrorStatus("invalid civilian ID", http.StatusBadRequest, w, err)
		return
	}

	citID, err := primitive.ObjectIDFromHex(citationID)
	if err != nil {
		config.ErrorStatus("invalid citation ID", http.StatusBadRequest, w, err)
		return
	}

	// Define the filter and update for removing the citation
	filter := bson.M{"_id": cID}
	update := bson.M{"$pull": bson.M{"civilian.criminalHistory": bson.M{"_id": citID}}}

	// Perform the update operation
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to delete criminal history", http.StatusInternalServerError, w, err)
		return
	}

	// Respond with success
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Criminal history deleted successfully"}`))
}

// CivilianApprovalHandler handles civilian approval workflow actions
func (c Civilian) CivilianApprovalHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		CivilianID  string `json:"civilianId"`
		CommunityID string `json:"communityId"`
		UserID      string `json:"userId"`
		Action      string `json:"action"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Validate required fields
	if requestBody.CivilianID == "" || requestBody.CommunityID == "" || requestBody.UserID == "" || requestBody.Action == "" {
		config.ErrorStatus("missing required fields", http.StatusBadRequest, w, fmt.Errorf("civilianId, communityId, userId, and action are required"))
		return
	}

	// Convert civilian ID to ObjectID
	civID, err := primitive.ObjectIDFromHex(requestBody.CivilianID)
	if err != nil {
		config.ErrorStatus("invalid civilian ID", http.StatusBadRequest, w, err)
		return
	}

	// Handle different actions
	switch requestBody.Action {
	case "send_for_approval":
		// Update civilian status to "requested_review" for admin review
		filter := bson.M{"_id": civID}
		update := bson.M{
			"$set": bson.M{
				"civilian.approvalStatus": "requested_review",
				"civilian.updatedAt":      primitive.NewDateTimeFromTime(time.Now()),
			},
		}

		err = c.DB.UpdateOne(context.Background(), filter, update)
		if err != nil {
			config.ErrorStatus("failed to update civilian approval status", http.StatusInternalServerError, w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Civilian sent for approval successfully",
			"status":  "requested_review",
		})

	default:
		config.ErrorStatus("invalid action", http.StatusBadRequest, w, fmt.Errorf("action must be 'send_for_approval'"))
	}
}

// AdminCivilianApprovalHandler handles admin actions on civilian approvals
func (c Civilian) AdminCivilianApprovalHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		CivilianID  string `json:"civilianId"`
		CommunityID string `json:"communityId"`
		AdminID     string `json:"adminId"`
		Action      string `json:"action"`
		Notes       string `json:"notes,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Validate required fields
	if requestBody.CivilianID == "" || requestBody.CommunityID == "" || requestBody.AdminID == "" || requestBody.Action == "" {
		config.ErrorStatus("missing required fields", http.StatusBadRequest, w, fmt.Errorf("civilianId, communityId, adminId, and action are required"))
		return
	}

	// Convert civilian ID to ObjectID
	civID, err := primitive.ObjectIDFromHex(requestBody.CivilianID)
	if err != nil {
		config.ErrorStatus("invalid civilian ID", http.StatusBadRequest, w, err)
		return
	}

	// Validate action
	validActions := map[string]string{
		"approve": "approved",
		"deny":    "rejected",
		"require_edits": "requires_edits",
	}

	newStatus, isValidAction := validActions[requestBody.Action]
	if !isValidAction {
		config.ErrorStatus("invalid action", http.StatusBadRequest, w, fmt.Errorf("action must be one of: approve, deny, require_edits"))
		return
	}

	// Update civilian approval status
	filter := bson.M{"_id": civID}
	update := bson.M{
		"$set": bson.M{
			"civilian.approvalStatus": newStatus,
			"civilian.updatedAt":      primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	// Add notes if provided
	if requestBody.Notes != "" {
		update["$set"].(bson.M)["civilian.approvalNotes"] = requestBody.Notes
	}

	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update civilian approval status", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": fmt.Sprintf("Civilian %s successfully", requestBody.Action),
		"status":  newStatus,
		"notes":   requestBody.Notes,
	})
}

// PendingApprovalsHandler returns all civilians with pending approval status for a community
func (c Civilian) PendingApprovalsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := r.URL.Query().Get("communityId")
	if communityID == "" {
		config.ErrorStatus("communityId query parameter is required", http.StatusBadRequest, w, fmt.Errorf("communityId is required"))
		return
	}

	// Get pagination parameters
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 10 // default limit
	}
	limit64 := int64(limit)

	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 0 {
		page = 0 // default page
	}
	skip64 := int64(page * limit)

	// Build filter for pending approvals
	filter := bson.M{
		"civilian.activeCommunityID": communityID,
		"$or": []bson.M{
			{"civilian.approvalStatus": "pending"},
			{"civilian.approvalStatus": "requested_review"},
		},
	}

	zap.S().Debugf("MongoDB filter: %+v", filter)

	// Set up pagination options
	options := &options.FindOptions{
		Limit: &limit64,
		Skip:  &skip64,
		Sort:  bson.D{{Key: "civilian.createdAt", Value: -1}}, // Sort by creation date, newest first
	}

	// Find civilians with pending approval status
	dbResp, err := c.DB.Find(context.TODO(), filter, options)
	if err != nil {
		config.ErrorStatus("failed to get pending approvals", http.StatusInternalServerError, w, err)
		return
	}

	// Get total count for pagination metadata
	totalCount, err := c.DB.CountDocuments(context.TODO(), filter)
	if err != nil {
		config.ErrorStatus("failed to get total count", http.StatusInternalServerError, w, err)
		return
	}

	// Calculate pagination info
	totalPages := int(math.Ceil(float64(totalCount) / float64(limit)))
	hasNext := page < totalPages-1
	hasPrev := page > 0

	// Build response with pagination metadata
	response := map[string]interface{}{
		"data": dbResp,
		"pagination": map[string]interface{}{
			"currentPage": page,
			"limit":       limit,
			"totalCount":  totalCount,
			"totalPages":  totalPages,
			"hasNext":     hasNext,
			"hasPrev":     hasPrev,
		},
	}

	// Return empty data array if no results
	if len(dbResp) == 0 {
		response["data"] = []models.Civilian{}
	}

	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
