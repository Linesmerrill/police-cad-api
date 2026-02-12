package handlers

import (
	"encoding/json"
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

// MostWanted exported for testing purposes
type MostWanted struct {
	DB    databases.MostWantedDatabase
	CivDB databases.CivilianDatabase
}

// FetchMostWantedHandler returns a paginated list of most wanted entries for a community
func (mw MostWanted) FetchMostWantedHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 0 {
		page = 0
	}

	status := r.URL.Query().Get("status")
	if status == "" {
		status = "active"
	}

	skip := int64(page * limit)
	limit64 := int64(limit)

	filter := bson.M{
		"mostWanted.communityID": communityID,
		"mostWanted.status":      status,
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Run Find and CountDocuments in parallel
	type findResult struct {
		data []models.MostWantedEntry
		err  error
	}
	type countResult struct {
		count int64
		err   error
	}

	findCh := make(chan findResult, 1)
	countCh := make(chan countResult, 1)

	go func() {
		opts := options.Find().
			SetLimit(limit64).
			SetSkip(skip).
			SetSort(bson.D{{"mostWanted.listOrder", 1}})
		data, err := mw.DB.Find(ctx, filter, opts)
		findCh <- findResult{data: data, err: err}
	}()

	go func() {
		count, err := mw.DB.CountDocuments(ctx, filter)
		countCh <- countResult{count: count, err: err}
	}()

	fr := <-findCh
	cr := <-countCh

	if fr.err != nil {
		config.ErrorStatus("failed to fetch most wanted entries", http.StatusInternalServerError, w, fr.err)
		return
	}
	if cr.err != nil {
		config.ErrorStatus("failed to count most wanted entries", http.StatusInternalServerError, w, cr.err)
		return
	}

	data := fr.data
	if data == nil {
		data = []models.MostWantedEntry{}
	}

	totalCount := cr.count
	totalPages := int64(0)
	if limit > 0 {
		totalPages = (totalCount + int64(limit) - 1) / int64(limit)
	}

	response := map[string]interface{}{
		"data": data,
		"pagination": map[string]interface{}{
			"currentPage": page,
			"limit":       limit,
			"totalCount":  totalCount,
			"totalPages":  totalPages,
			"hasNext":     int64(page+1) < totalPages,
			"hasPrev":     page > 0,
		},
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// GetMostWantedByIDHandler retrieves a single most wanted entry by its ID
func (mw MostWanted) GetMostWantedByIDHandler(w http.ResponseWriter, r *http.Request) {
	entryID := mux.Vars(r)["entry_id"]

	eID, err := primitive.ObjectIDFromHex(entryID)
	if err != nil {
		config.ErrorStatus("invalid entry ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	entry, err := mw.DB.FindOne(ctx, bson.M{"_id": eID})
	if err != nil {
		config.ErrorStatus("failed to find most wanted entry", http.StatusNotFound, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(entry)
}

// createMostWantedRequest holds the expected request body for creating a most wanted entry
type createMostWantedRequest struct {
	CommunityID   string            `json:"communityID"`
	CivilianID    string            `json:"civilianID"`
	Charges       []string          `json:"charges"`
	Description   string            `json:"description"`
	AddedByUserID string            `json:"addedByUserID"`
	CustomFields  map[string]string `json:"customFields"`
	Stars         int               `json:"stars"`
}

// CreateMostWantedHandler adds a civilian to the most wanted list
func (mw MostWanted) CreateMostWantedHandler(w http.ResponseWriter, r *http.Request) {
	var req createMostWantedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if req.CommunityID == "" || req.CivilianID == "" {
		config.ErrorStatus("communityID and civilianID are required", http.StatusBadRequest, w, nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Check uniqueness: one active entry per civilian per community
	uniqueFilter := bson.M{
		"mostWanted.communityID": req.CommunityID,
		"mostWanted.civilianID":  req.CivilianID,
		"mostWanted.status":      "active",
	}
	existing, _ := mw.DB.FindOne(ctx, uniqueFilter)
	if existing != nil {
		config.ErrorStatus("this civilian is already on the most wanted list", http.StatusConflict, w, nil)
		return
	}

	// Fetch civilian data for snapshot
	civID, err := primitive.ObjectIDFromHex(req.CivilianID)
	if err != nil {
		config.ErrorStatus("invalid civilian ID", http.StatusBadRequest, w, err)
		return
	}

	civ, err := mw.CivDB.FindOne(ctx, bson.M{"_id": civID})
	if err != nil {
		config.ErrorStatus("failed to find civilian", http.StatusNotFound, w, err)
		return
	}

	snapshot := buildCivilianSnapshot(civ)

	// Determine next list order
	countFilter := bson.M{
		"mostWanted.communityID": req.CommunityID,
		"mostWanted.status":      "active",
	}
	count, err := mw.DB.CountDocuments(ctx, countFilter)
	if err != nil {
		zap.S().Warnf("failed to count existing entries for list order: %v", err)
		count = 0
	}

	stars := req.Stars
	if stars < 1 || stars > 5 {
		stars = 5
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	entry := models.MostWantedEntry{
		ID: primitive.NewObjectID(),
		Details: models.MostWantedEntryDetails{
			CommunityID:      req.CommunityID,
			CivilianID:       req.CivilianID,
			ListOrder:        int(count) + 1,
			Stars:            stars,
			Charges:          req.Charges,
			Description:      req.Description,
			Status:           "active",
			AddedByUserID:    req.AddedByUserID,
			CustomFields:     req.CustomFields,
			CivilianSnapshot: snapshot,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}

	if entry.Details.Charges == nil {
		entry.Details.Charges = []string{}
	}
	if entry.Details.CustomFields == nil {
		entry.Details.CustomFields = map[string]string{}
	}

	_, err = mw.DB.InsertOne(ctx, entry)
	if err != nil {
		config.ErrorStatus("failed to create most wanted entry", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "most wanted entry created successfully",
		"id":      entry.ID.Hex(),
		"data":    entry,
	})
}

// UpdateMostWantedHandler updates an existing most wanted entry
func (mw MostWanted) UpdateMostWantedHandler(w http.ResponseWriter, r *http.Request) {
	entryID := mux.Vars(r)["entry_id"]

	eID, err := primitive.ObjectIDFromHex(entryID)
	if err != nil {
		config.ErrorStatus("invalid entry ID", http.StatusBadRequest, w, err)
		return
	}

	var updatedFields map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updatedFields); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	update := bson.M{}
	for key, value := range updatedFields {
		update["mostWanted."+key] = value
	}
	update["mostWanted.updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	// If civilianID is provided, refresh the snapshot
	if civIDStr, ok := updatedFields["civilianID"].(string); ok && civIDStr != "" {
		civID, err := primitive.ObjectIDFromHex(civIDStr)
		if err == nil {
			civ, err := mw.CivDB.FindOne(ctx, bson.M{"_id": civID})
			if err == nil {
				update["mostWanted.civilianSnapshot"] = buildCivilianSnapshot(civ)
			}
		}
	} else {
		// Refresh snapshot from existing civilianID on the entry
		existing, err := mw.DB.FindOne(ctx, bson.M{"_id": eID})
		if err == nil && existing.Details.CivilianID != "" {
			civID, err := primitive.ObjectIDFromHex(existing.Details.CivilianID)
			if err == nil {
				civ, err := mw.CivDB.FindOne(ctx, bson.M{"_id": civID})
				if err == nil {
					update["mostWanted.civilianSnapshot"] = buildCivilianSnapshot(civ)
				}
			}
		}
	}

	filter := bson.M{"_id": eID}
	err = mw.DB.UpdateOne(ctx, filter, bson.M{"$set": update})
	if err != nil {
		config.ErrorStatus("failed to update most wanted entry", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "most wanted entry updated successfully"})
}

// DeleteMostWantedHandler deletes a most wanted entry
func (mw MostWanted) DeleteMostWantedHandler(w http.ResponseWriter, r *http.Request) {
	entryID := mux.Vars(r)["entry_id"]

	eID, err := primitive.ObjectIDFromHex(entryID)
	if err != nil {
		config.ErrorStatus("invalid entry ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"_id": eID}
	err = mw.DB.DeleteOne(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to delete most wanted entry", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "most wanted entry deleted successfully"})
}

// reorderRequest holds the expected request body for reordering
type reorderRequest struct {
	CommunityID string          `json:"communityID"`
	Order       []reorderItem   `json:"order"`
}

type reorderItem struct {
	EntryID   string `json:"entryId"`
	ListOrder int    `json:"listOrder"`
}

// ReorderMostWantedHandler reorders the most wanted list
func (mw MostWanted) ReorderMostWantedHandler(w http.ResponseWriter, r *http.Request) {
	var req reorderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if req.CommunityID == "" || len(req.Order) == 0 {
		config.ErrorStatus("communityID and order are required", http.StatusBadRequest, w, nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	for _, item := range req.Order {
		eID, err := primitive.ObjectIDFromHex(item.EntryID)
		if err != nil {
			continue
		}
		filter := bson.M{"_id": eID, "mostWanted.communityID": req.CommunityID}
		update := bson.M{"$set": bson.M{
			"mostWanted.listOrder":  item.ListOrder,
			"mostWanted.updatedAt":  primitive.NewDateTimeFromTime(time.Now()),
		}}
		if err := mw.DB.UpdateOne(ctx, filter, update); err != nil {
			zap.S().Warnf("failed to reorder entry %s: %v", item.EntryID, err)
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "most wanted list reordered successfully"})
}

// buildCivilianSnapshot creates a map of civilian fields for denormalized storage
func buildCivilianSnapshot(civ *models.Civilian) map[string]interface{} {
	return map[string]interface{}{
		"name":                 civ.Details.Name,
		"firstName":            civ.Details.FirstName,
		"lastName":             civ.Details.LastName,
		"image":                civ.Details.Image,
		"birthday":             civ.Details.Birthday,
		"gender":               civ.Details.Gender,
		"race":                 civ.Details.Race,
		"hairColor":            civ.Details.HairColor,
		"eyeColor":             civ.Details.EyeColor,
		"height":               civ.Details.Height,
		"heightClassification": civ.Details.HeightClassification,
		"weight":               civ.Details.Weight,
		"weightClassification": civ.Details.WeightClassification,
		"address":              civ.Details.Address,
		"occupation":           civ.Details.Occupation,
		"onProbation":          civ.Details.OnProbation,
		"onParole":             civ.Details.OnParole,
		"deceased":             civ.Details.Deceased,
	}
}
