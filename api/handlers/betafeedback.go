package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// BetaFeedback holds the collection handle for beta-opt-out feedback docs.
type BetaFeedback struct {
	DB databases.BetaFeedbackDatabase
}

// validReasons mirrors the enum on the model struct tag so we can reject
// unknown values at the edge without pulling in a full validator.
var validReasons = map[string]bool{
	"too_buggy":        true,
	"look_and_feel":    true,
	"preferred_old":    true,
	"missing_features": true,
	"performance":      true,
	"other":            true,
}

var validFlags = map[string]bool{
	"betaCommandDashboard": true,
	"betaCommandDispatch":  true,
	"betaCivDashboard":     true,
}

// CreateBetaFeedbackHandler stores a new feedback entry. Open endpoint —
// rate-limiting + deduping can be layered at the proxy if needed.
func (b BetaFeedback) CreateBetaFeedbackHandler(w http.ResponseWriter, r *http.Request) {
	var req models.CreateBetaFeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("failed to decode request", http.StatusBadRequest, w, err)
		return
	}
	if req.UserID == "" || req.Flag == "" || req.Reason == "" {
		config.ErrorStatus("userId, flag, and reason are required", http.StatusBadRequest, w, nil)
		return
	}
	if !validFlags[req.Flag] {
		config.ErrorStatus("unknown flag", http.StatusBadRequest, w, nil)
		return
	}
	if !validReasons[req.Reason] {
		config.ErrorStatus("unknown reason", http.StatusBadRequest, w, nil)
		return
	}
	// Cap free-form feedback defensively in case a proxy strips struct tags.
	if len(req.Feedback) > 2000 {
		req.Feedback = req.Feedback[:2000]
	}
	if len(req.Context) > 200 {
		req.Context = req.Context[:200]
	}

	doc := models.BetaFeedback{
		ID:        primitive.NewObjectID(),
		UserID:    req.UserID,
		Flag:      req.Flag,
		Reason:    req.Reason,
		Feedback:  req.Feedback,
		Context:   req.Context,
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
	}
	if _, err := b.DB.InsertOne(context.Background(), doc); err != nil {
		config.ErrorStatus("failed to save beta feedback", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

// ListBetaFeedbackHandler returns feedback entries for the admin console.
// Supports `flag`, `status` (open|resolved|all), `limit`, and `page`
// query params. Sorted newest first. Soft-deleted entries are always
// excluded.
func (b BetaFeedback) ListBetaFeedbackHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// Always exclude soft-deleted entries from the admin list.
	filter := bson.M{
		"$or": []bson.M{
			{"deletedAt": bson.M{"$exists": false}},
			{"deletedAt": nil},
		},
	}
	if flag := q.Get("flag"); flag != "" {
		if !validFlags[flag] {
			config.ErrorStatus("unknown flag", http.StatusBadRequest, w, nil)
			return
		}
		filter["flag"] = flag
	}

	// Status: open (default) hides resolved entries; resolved shows
	// only resolved; all shows both. Counts are always computed against
	// the same status window so the distribution chips reflect the list.
	status := q.Get("status")
	if status == "" {
		status = "open"
	}
	switch status {
	case "open":
		filter["$and"] = []bson.M{{"$or": []bson.M{
			{"resolvedAt": bson.M{"$exists": false}},
			{"resolvedAt": nil},
		}}}
	case "resolved":
		filter["resolvedAt"] = bson.M{"$ne": nil}
	case "all":
		// no extra filter
	default:
		config.ErrorStatus("unknown status", http.StatusBadRequest, w, nil)
		return
	}

	limit := int64(50)
	if s := q.Get("limit"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	page := int64(1)
	if s := q.Get("page"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
			page = n
		}
	}
	skip := (page - 1) * limit

	ctx := context.Background()
	total, err := b.DB.CountDocuments(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to count beta feedback", http.StatusInternalServerError, w, err)
		return
	}

	// Summary counts by reason so the admin UI can show a distribution
	// without paginating through every entry.
	pipe := []bson.M{
		{"$match": filter},
		{"$group": bson.M{"_id": "$reason", "count": bson.M{"$sum": 1}}},
	}
	cur, err := b.DB.Aggregate(ctx, pipe)
	if err != nil {
		config.ErrorStatus("failed to aggregate beta feedback", http.StatusInternalServerError, w, err)
		return
	}
	// Custom MongoCursor wrapper: Decode() calls All() under the hood, so we
	// decode into a slice in one shot rather than iterating with Next/Decode.
	var rows []struct {
		ID    string `bson:"_id"`
		Count int64  `bson:"count"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		_ = cur.Close(ctx)
		config.ErrorStatus("failed to decode beta feedback aggregation", http.StatusInternalServerError, w, err)
		return
	}
	_ = cur.Close(ctx)
	reasonCounts := map[string]int64{}
	for _, row := range rows {
		reasonCounts[row.ID] = row.Count
	}

	// Sibling counts so the UI can show "Open (N) · Resolved (M)"
	// without firing a second request. Only entries with a non-empty
	// free-form `feedback` are counted — those are the ones the admin
	// can actually triage. The reason-distribution chips above still
	// reflect the full opt-out volume.
	statusBaseFilter := bson.M{
		"$or": []bson.M{
			{"deletedAt": bson.M{"$exists": false}},
			{"deletedAt": nil},
		},
		"feedback": bson.M{"$exists": true, "$ne": ""},
	}
	if flag, ok := filter["flag"]; ok {
		statusBaseFilter["flag"] = flag
	}
	openFilter := bson.M{}
	for k, v := range statusBaseFilter {
		openFilter[k] = v
	}
	openFilter["$and"] = []bson.M{{"$or": []bson.M{
		{"resolvedAt": bson.M{"$exists": false}},
		{"resolvedAt": nil},
	}}}
	openCount, err := b.DB.CountDocuments(ctx, openFilter)
	if err != nil {
		config.ErrorStatus("failed to count open beta feedback", http.StatusInternalServerError, w, err)
		return
	}
	resolvedFilter := bson.M{}
	for k, v := range statusBaseFilter {
		resolvedFilter[k] = v
	}
	resolvedFilter["resolvedAt"] = bson.M{"$ne": nil}
	resolvedCount, err := b.DB.CountDocuments(ctx, resolvedFilter)
	if err != nil {
		config.ErrorStatus("failed to count resolved beta feedback", http.StatusInternalServerError, w, err)
		return
	}
	// Total commentable entries in the current status window — used for
	// the "X comments" label on the panel. Excludes empty-feedback rows.
	commentFilter := bson.M{}
	for k, v := range filter {
		commentFilter[k] = v
	}
	commentFilter["feedback"] = bson.M{"$exists": true, "$ne": ""}
	commentCount, err := b.DB.CountDocuments(ctx, commentFilter)
	if err != nil {
		config.ErrorStatus("failed to count beta feedback comments", http.StatusInternalServerError, w, err)
		return
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: -1}}).
		SetSkip(skip).
		SetLimit(limit)
	cursor, err := b.DB.Find(ctx, filter, findOpts)
	if err != nil {
		config.ErrorStatus("failed to fetch beta feedback", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	entries := []models.BetaFeedback{}
	if err := cursor.All(ctx, &entries); err != nil {
		config.ErrorStatus("failed to decode beta feedback", http.StatusInternalServerError, w, err)
		return
	}

	resp := map[string]interface{}{
		"data":          entries,
		"totalCount":    total,
		"commentCount":  commentCount,
		"page":          page,
		"limit":         limit,
		"reasonCounts":  reasonCounts,
		"openCount":     openCount,
		"resolvedCount": resolvedCount,
		"status":        status,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// resolveBody is the PATCH body for ResolveBetaFeedbackHandler. Setting
// resolved=false reopens an entry; resolved=true marks it completed.
type resolveBody struct {
	Resolved   bool   `json:"resolved"`
	ResolvedBy string `json:"resolvedBy"`
}

// ResolveBetaFeedbackHandler toggles the resolved state of an entry.
// PATCH /api/v1/admin/beta-feedback/{id}
func (b BetaFeedback) ResolveBetaFeedbackHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		config.ErrorStatus("invalid id", http.StatusBadRequest, w, err)
		return
	}
	var req resolveBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("failed to decode request", http.StatusBadRequest, w, err)
		return
	}
	if len(req.ResolvedBy) > 200 {
		req.ResolvedBy = req.ResolvedBy[:200]
	}

	var update bson.M
	if req.Resolved {
		now := primitive.NewDateTimeFromTime(time.Now())
		update = bson.M{"$set": bson.M{
			"resolvedAt": now,
			"resolvedBy": req.ResolvedBy,
		}}
	} else {
		update = bson.M{"$unset": bson.M{
			"resolvedAt": "",
			"resolvedBy": "",
		}}
	}
	if err := b.DB.UpdateOne(context.Background(), bson.M{"_id": objID}, update); err != nil {
		config.ErrorStatus("failed to update beta feedback", http.StatusInternalServerError, w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "resolved": req.Resolved})
}

// DeleteBetaFeedbackHandler soft-deletes (default) or restores an entry.
// DELETE /api/v1/admin/beta-feedback/{id}              -> soft-delete
// DELETE /api/v1/admin/beta-feedback/{id}?undo=true    -> restore
func (b BetaFeedback) DeleteBetaFeedbackHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		config.ErrorStatus("invalid id", http.StatusBadRequest, w, err)
		return
	}

	var update bson.M
	if r.URL.Query().Get("undo") == "true" {
		update = bson.M{"$unset": bson.M{"deletedAt": ""}}
	} else {
		now := primitive.NewDateTimeFromTime(time.Now())
		update = bson.M{"$set": bson.M{"deletedAt": now}}
	}
	if err := b.DB.UpdateOne(context.Background(), bson.M{"_id": objID}, update); err != nil {
		config.ErrorStatus("failed to delete beta feedback", http.StatusInternalServerError, w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
