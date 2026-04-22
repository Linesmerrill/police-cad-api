package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

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
// Supports `flag`, `limit`, and `page` query params. Sorted newest first.
func (b BetaFeedback) ListBetaFeedbackHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := bson.M{}
	if flag := q.Get("flag"); flag != "" {
		if !validFlags[flag] {
			config.ErrorStatus("unknown flag", http.StatusBadRequest, w, nil)
			return
		}
		filter["flag"] = flag
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
	reasonCounts := map[string]int64{}
	for cur.Next(ctx) {
		var row struct {
			ID    string `bson:"_id"`
			Count int64  `bson:"count"`
		}
		if err := cur.Decode(&row); err == nil {
			reasonCounts[row.ID] = row.Count
		}
	}
	_ = cur.Close(ctx)

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
	for cursor.Next(ctx) {
		var doc models.BetaFeedback
		if err := cursor.Decode(&doc); err == nil {
			entries = append(entries, doc)
		}
	}

	resp := map[string]interface{}{
		"data":         entries,
		"totalCount":   total,
		"page":         page,
		"limit":        limit,
		"reasonCounts": reasonCounts,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
