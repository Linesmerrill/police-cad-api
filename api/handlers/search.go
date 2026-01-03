package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

// Search struct mostly used for mocking tests
type Search struct {
	UserDB databases.UserDatabase
	CommDB databases.CommunityDatabase
}

// PaginatedCommunityResponse holds the structure for paginated responses
type PaginatedCommunityResponse struct {
	Page       int                `json:"page"`
	TotalCount int64              `json:"totalCount"`
	Data       []models.Community `json:"data"`
}

// SearchHandler returns a list of users and communities that match the query
func (s Search) SearchHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		config.ErrorStatus("query param q is required", http.StatusBadRequest, w, fmt.Errorf("q == %s", query))
		return
	}

	currentUserID := r.URL.Query().Get("userId")
	if currentUserID == "" {
		config.ErrorStatus("query param userId is required", http.StatusBadRequest, w, fmt.Errorf("userId == %s", currentUserID))
		return
	}

	currentUserObjectID, err := primitive.ObjectIDFromHex(currentUserID)
	if err != nil {
		config.ErrorStatus("invalid userId", http.StatusBadRequest, w, err)
		return
	}

	limitParam := r.URL.Query().Get("limit")
	pageParam := r.URL.Query().Get("page")

	limit := int64(10) // default limit
	page := int64(1)   // default page

	if limitParam != "" {
		l, err := strconv.ParseInt(limitParam, 10, 64)
		if err == nil {
			limit = l
		}
	}

	if pageParam != "" {
		p, err := strconv.ParseInt(pageParam, 10, 64)
		if err == nil {
			page = p
		}
	}

	skip := (page - 1) * limit

	results := map[string]interface{}{}

	// Search for users
	userFilter := bson.M{
		"$and": []bson.M{
			{"$or": []bson.M{
				{"user.name": bson.M{"$regex": query, "$options": "i"}},
				{"user.username": bson.M{"$regex": query, "$options": "i"}},
			}},
			{"_id": bson.M{"$ne": currentUserObjectID}},
		},
	}
	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	userOptions := options.Find().SetLimit(limit).SetSkip(skip)
	cursor, err := s.UserDB.Find(ctx, userFilter, userOptions)
	if err != nil {
		config.ErrorStatus("failed to search users", http.StatusInternalServerError, w, err)
		return
	}

	defer cursor.Close(ctx)

	var users []models.User
	if err = cursor.All(ctx, &users); err != nil {
		config.ErrorStatus("failed to decode users", http.StatusInternalServerError, w, err)
		return
	}

	if users == nil {
		users = []models.User{}
	}

	// TODO: Remove after frontend release v1.0.5
	// Search for communities with visibility set to "public"
	communityFilter := bson.M{
		"$and": []bson.M{
			{"community.name": bson.M{"$regex": query, "$options": "i"}},
			{"community.visibility": "public"},
		},
	}
	communityOptions := options.Find().SetLimit(limit).SetSkip(skip)
	communityCursor, err := s.CommDB.Find(ctx, communityFilter, communityOptions)
	if err != nil {
		config.ErrorStatus("failed to search communities", http.StatusInternalServerError, w, err)
		return
	}
	defer communityCursor.Close(ctx)

	var communities []models.Community
	if err = communityCursor.All(ctx, &communities); err != nil {
		config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
		return
	}

	results["users"] = users
	results["communities"] = communities

	responseBody, err := json.Marshal(results)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(responseBody)
}

// SearchCommunityHandler returns a list of communities that match the query
func (s Search) SearchCommunityHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		config.ErrorStatus("query param q is required", http.StatusBadRequest, w, fmt.Errorf("q == %s", query))
		return
	}

	limitParam := r.URL.Query().Get("limit")
	pageParam := r.URL.Query().Get("page")

	limit := int64(10) // default limit
	page := int64(1)   // default page

	if limitParam != "" {
		l, err := strconv.ParseInt(limitParam, 10, 64)
		if err == nil {
			limit = l
		}
	}

	if pageParam != "" {
		p, err := strconv.ParseInt(pageParam, 10, 64)
		if err == nil {
			page = p
		}
	}

	skip := (page - 1) * limit

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// For very short queries (1-2 chars), regex causes full collection scans
	// For queries 3+ chars, use $text search with text index for much better performance
	// Always filter by visibility to use the visibility index
	queryLen := len(strings.TrimSpace(query))
	var communityFilter bson.M
	var communityOptions *options.FindOptions
	
	if queryLen >= 3 {
		// Use $text search for longer queries - much faster with text index
		communityFilter = bson.M{
			"$and": []bson.M{
				{"$text": bson.M{"$search": query}}, // Uses community_name_text_idx
				{"community.visibility": "public"},  // Uses community_visibility_idx
			},
		}
		// Sort by text score for relevance, then by name
		communityOptions = options.Find().
			SetLimit(limit).
			SetSkip(skip).
			SetSort(bson.M{"score": bson.M{"$meta": "textScore"}, "community.name": 1})
	} else {
		// For short queries, use regex but optimize by filtering visibility first
		// The visibility index will help reduce the scan set
		communityFilter = bson.M{
			"$and": []bson.M{
				{"community.name": bson.M{"$regex": "^" + query, "$options": "i"}}, // Prefix match is slightly faster than full regex
				{"community.visibility": "public"},
			},
		}
		communityOptions = options.Find().SetLimit(limit).SetSkip(skip)
	}
	
	communityCursor, err := s.CommDB.Find(ctx, communityFilter, communityOptions)
	if err != nil {
		// If text search fails (e.g., index doesn't exist), fallback to regex
		if queryLen >= 3 {
			zap.S().Warnw("text search failed, falling back to regex", "query", query, "error", err)
			communityFilter = bson.M{
				"$and": []bson.M{
					{"community.name": bson.M{"$regex": query, "$options": "i"}},
					{"community.visibility": "public"},
				},
			}
			communityOptions = options.Find().SetLimit(limit).SetSkip(skip)
			communityCursor, err = s.CommDB.Find(ctx, communityFilter, communityOptions)
			if err != nil {
				config.ErrorStatus("failed to search communities", http.StatusInternalServerError, w, err)
				return
			}
		} else {
			config.ErrorStatus("failed to search communities", http.StatusInternalServerError, w, err)
			return
		}
	}
	defer communityCursor.Close(ctx)

	var communities []models.Community
	if err = communityCursor.All(ctx, &communities); err != nil {
		config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
		return
	}

	// For very short queries (1-2 chars), skip expensive CountDocuments
	// Return results with estimated count based on current page results
	var totalCount int64
	if queryLen <= 2 {
		// Skip count for very short queries - it's too expensive
		// Estimate: if we got a full page, there might be more; otherwise this is likely all
		if len(communities) == int(limit) {
			totalCount = int64(len(communities)) + 1 // Indicate there might be more
		} else {
			totalCount = int64(len(communities))
		}
		zap.S().Debugw("skipped count for short query", "query", query, "estimatedCount", totalCount)
	} else {
		// OPTIMIZATION: Skip CountDocuments for longer queries too - it's expensive
		// The $text search is already fast, but CountDocuments still requires scanning
		// Estimate based on results: if we got a full page, there's likely more
		if len(communities) == int(limit) {
			totalCount = int64(len(communities)) + 1 // Indicate there might be more
		} else {
			totalCount = int64(len(communities))
		}
		zap.S().Debugw("skipped count for query", "query", query, "estimatedCount", totalCount)
	}

	// Prepare the paginated response
	response := PaginatedCommunityResponse{
		Page:       int(page),
		TotalCount: totalCount,
		Data:       communities,
	}

	responseBody, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(responseBody)
}
