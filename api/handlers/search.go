package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"context"

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
	userOptions := options.Find().SetLimit(limit).SetSkip(skip)
	cursor, err := s.UserDB.Find(context.Background(), userFilter, userOptions)
	if err != nil {
		config.ErrorStatus("failed to search users", http.StatusInternalServerError, w, err)
		return
	}

	defer cursor.Close(context.Background())

	var users []models.User
	if err = cursor.All(context.Background(), &users); err != nil {
		config.ErrorStatus("failed to decode users", http.StatusInternalServerError, w, err)
		return
	}

	if users == nil {
		users = []models.User{}
	}

	// Search for communities with visibility set to "public"
	communityFilter := bson.M{
		"$and": []bson.M{
			{"community.name": bson.M{"$regex": query, "$options": "i"}},
			{"community.visibility": "public"},
		},
	}
	communityOptions := options.Find().SetLimit(limit).SetSkip(skip)
	communityCursor, err := s.CommDB.Find(context.Background(), communityFilter, communityOptions)
	if err != nil {
		config.ErrorStatus("failed to search communities", http.StatusInternalServerError, w, err)
		return
	}
	defer communityCursor.Close(context.Background())

	var communities []models.Community
	if err = communityCursor.All(context.Background(), &communities); err != nil {
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
