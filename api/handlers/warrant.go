package handlers

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
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

// Warrant exported for testing purposes
type Warrant struct {
	DB  databases.WarrantDatabase
	CDB databases.CommunityDatabase
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

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	opts := options.Find().
		SetLimit(limit64).
		SetSkip(skip64).
		SetSort(bson.M{"_id": -1})

	dbResp, err := v.DB.Find(ctx, bson.D{}, opts)
	if err != nil {
		config.ErrorStatus("failed to get warrants", http.StatusNotFound, w, err)
		return
	}

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
	warrantID := mux.Vars(r)["warrant_id"]

	zap.S().Debugf("warrant_id: %v", warrantID)

	wID, err := primitive.ObjectIDFromHex(warrantID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	dbResp, err := v.DB.FindOne(ctx, bson.M{"_id": wID})
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

// WarrantsByUserIDHandler returns all warrants that contain the given userID (accused civilian)
func (v Warrant) WarrantsByUserIDHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]
	status := r.URL.Query().Get("status")
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		zap.S().Warnf(fmt.Sprintf("limit not set, using default of %v", Limit|10))
	}
	limit64 := int64(Limit)
	Page = getPage(Page, r)
	skip64 := int64(Page * Limit)

	zap.S().Debugf("user_id: '%v'", userID)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{
		"warrant.accusedID": userID,
	}

	// Filter by status if provided
	if status != "" {
		filter["warrant.status"] = status
	}

	dbResp, err := v.DB.Find(ctx, filter, &options.FindOptions{
		Limit: &limit64,
		Skip:  &skip64,
		Sort:  bson.M{"_id": -1},
	})
	if err != nil {
		config.ErrorStatus("failed to get warrants", http.StatusNotFound, w, err)
		return
	}

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

// CreateWarrantHandler creates a new warrant request
func (v Warrant) CreateWarrantHandler(w http.ResponseWriter, r *http.Request) {
	var warrant models.Warrant
	if err := json.NewDecoder(r.Body).Decode(&warrant.Details); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	warrant.ID = primitive.NewObjectID()
	now := primitive.NewDateTimeFromTime(time.Now())
	warrant.Details.CreatedAt = now
	warrant.Details.UpdatedAt = now

	// Determine initial status based on community warrant approval mode
	approvalMode := "auto-approve" // default
	approvalRate := 0.7           // default 70% for random mode
	if warrant.Details.ActiveCommunityID != "" && v.CDB != nil {
		cID, err := primitive.ObjectIDFromHex(warrant.Details.ActiveCommunityID)
		if err == nil {
			ctx, cancel := api.WithQueryTimeout(r.Context())
			defer cancel()
			community, err := v.CDB.FindOne(ctx, bson.M{"_id": cID})
			if err == nil && community.Details.WarrantApprovalMode != "" {
				approvalMode = community.Details.WarrantApprovalMode
				if community.Details.WarrantRandomApprovalRate > 0 {
					approvalRate = float64(community.Details.WarrantRandomApprovalRate) / 100.0
				}
			}
		}
	}

	switch approvalMode {
	case "require-judge":
		warrant.Details.Status = "pending"
	case "random":
		warrant.Details.ReviewedAt = now
		randomJudgeNames := []string{"Smith", "Carter", "Patel", "Nguyen", "Brown", "Lopez", "Williams", "Johnson", "Davis", "Martinez"}
		warrant.Details.JudgeName = "Judge " + randomJudgeNames[rand.Intn(len(randomJudgeNames))]
		if rand.Float64() < approvalRate {
			warrant.Details.Status = "approved"
			warrant.Details.JudgeNotes = "Warrant approved based on submitted probable cause."
		} else {
			warrant.Details.Status = "denied"
			warrant.Details.JudgeNotes = "Insufficient probable cause to issue warrant."
		}
	default: // "auto-approve"
		warrant.Details.Status = "approved"
		warrant.Details.JudgeName = "Auto-Approved"
		warrant.Details.ReviewedAt = now
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	_, err := v.DB.InsertOne(ctx, warrant)
	if err != nil {
		config.ErrorStatus("failed to create warrant", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Warrant created successfully",
		"id":      warrant.ID.Hex(),
		"status":  warrant.Details.Status,
		"warrant": warrant,
	})
}

// UpdateWarrantHandler updates a warrant's details
func (v Warrant) UpdateWarrantHandler(w http.ResponseWriter, r *http.Request) {
	warrantID := mux.Vars(r)["warrant_id"]

	wID, err := primitive.ObjectIDFromHex(warrantID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	existingWarrant, err := v.DB.FindOne(ctx, bson.M{"_id": wID})
	if err != nil {
		config.ErrorStatus("failed to find warrant", http.StatusNotFound, w, err)
		return
	}

	// Convert existing details to a map for merging
	existingDetailsMap := make(map[string]interface{})
	data, _ := json.Marshal(existingWarrant.Details)
	json.Unmarshal(data, &existingDetailsMap)

	var updateData map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updateData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	for key, value := range updateData {
		existingDetailsMap[key] = value
	}
	existingDetailsMap["updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	updatedDetails := models.WarrantDetails{}
	data, _ = json.Marshal(existingDetailsMap)
	json.Unmarshal(data, &updatedDetails)

	err = v.DB.UpdateOne(ctx, bson.M{"_id": wID}, bson.M{"$set": bson.M{"warrant": updatedDetails}})
	if err != nil {
		config.ErrorStatus("failed to update warrant", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Warrant updated successfully",
	})
}

// DeleteWarrantHandler deletes a warrant by its ID
func (v Warrant) DeleteWarrantHandler(w http.ResponseWriter, r *http.Request) {
	warrantID := mux.Vars(r)["warrant_id"]

	wID, err := primitive.ObjectIDFromHex(warrantID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	err = v.DB.DeleteOne(ctx, bson.M{"_id": wID})
	if err != nil {
		config.ErrorStatus("failed to delete warrant", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Warrant deleted successfully",
	})
}

// ReviewWarrantHandler allows a judge to approve or deny a pending warrant
func (v Warrant) ReviewWarrantHandler(w http.ResponseWriter, r *http.Request) {
	warrantID := mux.Vars(r)["warrant_id"]

	wID, err := primitive.ObjectIDFromHex(warrantID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	var reviewData struct {
		Approved   bool   `json:"approved"`
		JudgeID    string `json:"judgeID"`
		JudgeName  string `json:"judgeName"`
		JudgeNotes string `json:"judgeNotes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reviewData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Verify warrant exists and is pending
	existingWarrant, err := v.DB.FindOne(ctx, bson.M{"_id": wID})
	if err != nil {
		config.ErrorStatus("failed to find warrant", http.StatusNotFound, w, err)
		return
	}

	if existingWarrant.Details.Status != "pending" {
		config.ErrorStatus("warrant is not in pending status", http.StatusBadRequest, w, fmt.Errorf("warrant status is '%s', expected 'pending'", existingWarrant.Details.Status))
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	status := "denied"
	if reviewData.Approved {
		status = "approved"
	}

	update := bson.M{
		"$set": bson.M{
			"warrant.status":     status,
			"warrant.judgeID":    reviewData.JudgeID,
			"warrant.judgeName":  reviewData.JudgeName,
			"warrant.judgeNotes": reviewData.JudgeNotes,
			"warrant.reviewedAt": now,
			"warrant.updatedAt":  now,
		},
	}

	err = v.DB.UpdateOne(ctx, bson.M{"_id": wID}, update)
	if err != nil {
		config.ErrorStatus("failed to review warrant", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Warrant reviewed successfully",
		"status":  status,
	})
}

// ExecuteWarrantHandler marks an approved warrant as executed
func (v Warrant) ExecuteWarrantHandler(w http.ResponseWriter, r *http.Request) {
	warrantID := mux.Vars(r)["warrant_id"]

	wID, err := primitive.ObjectIDFromHex(warrantID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	var execData struct {
		ExecutingOfficerID   string `json:"executingOfficerID"`
		ExecutingOfficerName string `json:"executingOfficerName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&execData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Verify warrant exists and is approved
	existingWarrant, err := v.DB.FindOne(ctx, bson.M{"_id": wID})
	if err != nil {
		config.ErrorStatus("failed to find warrant", http.StatusNotFound, w, err)
		return
	}

	if existingWarrant.Details.Status != "approved" {
		config.ErrorStatus("warrant is not in approved status", http.StatusBadRequest, w, fmt.Errorf("warrant status is '%s', expected 'approved'", existingWarrant.Details.Status))
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	update := bson.M{
		"$set": bson.M{
			"warrant.status":               "executed",
			"warrant.executingOfficerID":   execData.ExecutingOfficerID,
			"warrant.executingOfficerName": execData.ExecutingOfficerName,
			"warrant.executedAt":           now,
			"warrant.updatedAt":            now,
		},
	}

	err = v.DB.UpdateOne(ctx, bson.M{"_id": wID}, update)
	if err != nil {
		config.ErrorStatus("failed to execute warrant", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Warrant executed successfully",
	})
}

// WarrantsSearchHandler searches for warrants based on name, type, or status
func (v Warrant) WarrantsSearchHandler(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	warrantType := r.URL.Query().Get("warrantType")
	status := r.URL.Query().Get("status")
	communityID := r.URL.Query().Get("communityId")
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 10
	}
	limit64 := int64(Limit)
	Page := getPage(Page, r)
	skip64 := int64(Page * Limit)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	query := bson.M{}

	if name != "" {
		query["$or"] = []bson.M{
			{"warrant.accusedFirstName": bson.M{"$regex": "^" + name, "$options": "i"}},
			{"warrant.accusedLastName": bson.M{"$regex": "^" + name, "$options": "i"}},
		}
	}
	if warrantType != "" {
		query["warrant.warrantType"] = warrantType
	}
	if status != "" {
		query["warrant.status"] = status
	}
	if communityID != "" {
		query["warrant.activeCommunityID"] = communityID
	}

	opts := options.Find().
		SetLimit(limit64).
		SetSkip(skip64).
		SetSort(bson.M{"_id": -1})

	type findResult struct {
		warrants []models.Warrant
		err      error
	}
	type countResult struct {
		count int64
		err   error
	}

	findChan := make(chan findResult, 1)
	countChan := make(chan countResult, 1)

	go func() {
		warrants, err := v.DB.Find(ctx, query, opts)
		findChan <- findResult{warrants: warrants, err: err}
	}()

	go func() {
		count, err := v.DB.CountDocuments(ctx, query)
		countChan <- countResult{count: count, err: err}
	}()

	findRes := <-findChan
	countRes := <-countChan

	if findRes.err != nil {
		config.ErrorStatus("failed to search warrants", http.StatusNotFound, w, findRes.err)
		return
	}

	dbResp := findRes.warrants
	var totalCount int64
	if countRes.err != nil {
		totalCount = int64(len(dbResp))
	} else {
		totalCount = countRes.count
	}

	if len(dbResp) == 0 {
		dbResp = []models.Warrant{}
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

// PendingWarrantsByCommunityHandler returns pending warrants for a community (judge queue)
func (v Warrant) PendingWarrantsByCommunityHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["community_id"]
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 20
	}
	limit64 := int64(Limit)
	Page := getPage(Page, r)
	skip64 := int64(Page * Limit)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{
		"warrant.activeCommunityID": communityID,
		"warrant.status":            "pending",
	}

	type findResult struct {
		warrants []models.Warrant
		err      error
	}
	type countResult struct {
		count int64
		err   error
	}

	findChan := make(chan findResult, 1)
	countChan := make(chan countResult, 1)

	go func() {
		warrants, err := v.DB.Find(ctx, filter, &options.FindOptions{
			Limit: &limit64,
			Skip:  &skip64,
			Sort:  bson.M{"_id": -1},
		})
		findChan <- findResult{warrants: warrants, err: err}
	}()

	go func() {
		count, err := v.DB.CountDocuments(ctx, filter)
		countChan <- countResult{count: count, err: err}
	}()

	findRes := <-findChan
	countRes := <-countChan

	if findRes.err != nil {
		config.ErrorStatus("failed to get pending warrants", http.StatusNotFound, w, findRes.err)
		return
	}

	dbResp := findRes.warrants
	var totalCount int64
	if countRes.err != nil {
		totalCount = int64(len(dbResp))
	} else {
		totalCount = countRes.count
	}

	if len(dbResp) == 0 {
		dbResp = []models.Warrant{}
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

// WarrantsByCommunityHandlerV2 returns paginated warrants for a community with metadata
func (v Warrant) WarrantsByCommunityHandlerV2(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["community_id"]
	status := r.URL.Query().Get("status")
	warrantType := r.URL.Query().Get("warrantType")
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 10
	}
	limit64 := int64(Limit)
	Page := getPage(Page, r)
	skip64 := int64(Page * Limit)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{
		"warrant.activeCommunityID": communityID,
	}
	if status != "" {
		filter["warrant.status"] = status
	}
	if warrantType != "" {
		filter["warrant.warrantType"] = warrantType
	}

	type findResult struct {
		warrants []models.Warrant
		err      error
	}
	type countResult struct {
		count int64
		err   error
	}

	findChan := make(chan findResult, 1)
	countChan := make(chan countResult, 1)

	go func() {
		warrants, err := v.DB.Find(ctx, filter, &options.FindOptions{
			Limit: &limit64,
			Skip:  &skip64,
			Sort:  bson.M{"_id": -1},
		})
		findChan <- findResult{warrants: warrants, err: err}
	}()

	go func() {
		count, err := v.DB.CountDocuments(ctx, filter)
		countChan <- countResult{count: count, err: err}
	}()

	findRes := <-findChan
	countRes := <-countChan

	if findRes.err != nil {
		config.ErrorStatus("failed to get warrants", http.StatusNotFound, w, findRes.err)
		return
	}

	dbResp := findRes.warrants
	var totalCount int64
	if countRes.err != nil {
		totalCount = int64(len(dbResp))
	} else {
		totalCount = countRes.count
	}

	if len(dbResp) == 0 {
		dbResp = []models.Warrant{}
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
