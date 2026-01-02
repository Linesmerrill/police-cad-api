package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
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

	// Get the total count of matching documents
	totalCount, err := s.CommDB.CountDocuments(ctx, communityFilter)
	if err != nil {
		config.ErrorStatus("failed to count communities", http.StatusInternalServerError, w, err)
		return
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
