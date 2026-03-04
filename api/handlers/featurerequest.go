package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// FeatureRequestHandler struct for handling feature request operations
type FeatureRequestHandler struct {
	DB      databases.FeatureRequestDatabase
	VDB     databases.FeatureRequestVoteDatabase
	UDB     databases.UserDatabase
	AdminDB databases.AdminDatabase
}

// ListFeatureRequestsHandler returns paginated feature requests with filtering, sorting, and search
func (h FeatureRequestHandler) ListFeatureRequestsHandler(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	sortBy := r.URL.Query().Get("sort")
	status := r.URL.Query().Get("status")
	query := r.URL.Query().Get("q")
	userID := r.URL.Query().Get("userId")

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Build filter
	filter := bson.M{}
	if status != "" {
		filter["status"] = status
	}
	if query != "" {
		escaped := regexp.QuoteMeta(query)
		filter["$or"] = []bson.M{
			{"title": bson.M{"$regex": escaped, "$options": "i"}},
			{"description": bson.M{"$regex": escaped, "$options": "i"}},
		}
	}

	// Build sort
	var sort bson.D
	switch sortBy {
	case "top":
		sort = bson.D{{Key: "upvoteCount", Value: -1}, {Key: "createdAt", Value: -1}}
	case "trending":
		sort = bson.D{{Key: "upvoteCount", Value: -1}, {Key: "createdAt", Value: -1}}
	case "newest":
		sort = bson.D{{Key: "createdAt", Value: -1}}
	default:
		sort = bson.D{{Key: "createdAt", Value: -1}}
	}

	skip := (page - 1) * limit

	// Run Find + Count in parallel
	type countResult struct {
		count int64
		err   error
	}
	type findResult struct {
		requests []models.FeatureRequest
		err      error
	}

	countCh := make(chan countResult, 1)
	findCh := make(chan findResult, 1)

	go func() {
		count, err := h.DB.CountDocuments(ctx, filter)
		countCh <- countResult{count, err}
	}()

	go func() {
		findOptions := options.Find().
			SetSkip(int64(skip)).
			SetLimit(int64(limit)).
			SetSort(sort)

		cursor, err := h.DB.Find(ctx, filter, findOptions)
		if err != nil {
			findCh <- findResult{nil, err}
			return
		}
		defer cursor.Close(ctx)

		var requests []models.FeatureRequest
		if err := cursor.All(ctx, &requests); err != nil {
			findCh <- findResult{nil, err}
			return
		}
		findCh <- findResult{requests, nil}
	}()

	countRes := <-countCh
	findRes := <-findCh

	if countRes.err != nil {
		config.ErrorStatus("Failed to count feature requests", http.StatusInternalServerError, w, countRes.err)
		return
	}
	if findRes.err != nil {
		config.ErrorStatus("Failed to fetch feature requests", http.StatusInternalServerError, w, findRes.err)
		return
	}

	requests := findRes.requests
	if requests == nil {
		requests = []models.FeatureRequest{}
	}

	// Batch-fetch user data for all authors
	userIDs := make(map[primitive.ObjectID]bool)
	for _, req := range requests {
		userIDs[req.Author] = true
	}
	userMap := h.batchFetchUsers(ctx, userIDs)
	adminRoles := h.getAdminRolesForUsers(ctx, userMap)

	// If userId provided, batch-check votes for all feature requests
	voteMap := make(map[primitive.ObjectID]bool)
	if userID != "" {
		userObjID, err := primitive.ObjectIDFromHex(userID)
		if err == nil {
			frIDs := make([]primitive.ObjectID, len(requests))
			for i, req := range requests {
				frIDs[i] = req.ID
			}
			if len(frIDs) > 0 {
				voteFilter := bson.M{
					"featureRequestId": bson.M{"$in": frIDs},
					"user":             userObjID,
				}
				voteCursor, err := h.VDB.Find(ctx, voteFilter)
				if err == nil {
					var votes []models.FeatureRequestVote
					if err := voteCursor.All(ctx, &votes); err == nil {
						for _, v := range votes {
							voteMap[v.FeatureRequestID] = true
						}
					}
					voteCursor.Close(ctx)
				}
			}
		}
	}

	// Build response
	responseData := make([]models.FeatureRequestResponse, len(requests))
	for i, req := range requests {
		authorDoc, exists := userMap[req.Author]
		authorSummary := models.UserSummary{ID: req.Author}
		if exists {
			authorSummary.Username = authorDoc.User.Username
			authorSummary.ProfilePicture = authorDoc.User.ProfilePicture
		} else {
			authorSummary.Username = "Unknown"
		}
		if role, ok := adminRoles[req.Author]; ok {
			authorSummary.AdminRole = &role
		}

		imageURLs := req.ImageURLs
		if imageURLs == nil {
			imageURLs = []string{}
		}

		responseData[i] = models.FeatureRequestResponse{
			ID:           req.ID,
			Title:        req.Title,
			Description:  req.Description,
			Author:       authorSummary,
			Status:       req.Status,
			ImageURLs:    imageURLs,
			UpvoteCount:  req.UpvoteCount,
			CommentCount: req.CommentCount,
			HasVoted:     voteMap[req.ID],
			Comments:     []models.FeatureCommentResponse{},
			CreatedAt:    req.CreatedAt,
			UpdatedAt:    req.UpdatedAt,
		}
	}

	response := models.FeatureRequestListResponse{
		Data:       responseData,
		TotalCount: countRes.count,
		Page:       page,
		Limit:      limit,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// CreateFeatureRequestHandler creates a new feature request
func (h FeatureRequestHandler) CreateFeatureRequestHandler(w http.ResponseWriter, r *http.Request) {
	var req models.CreateFeatureRequestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	if req.Title == "" || req.Description == "" {
		config.ErrorStatus("Title and description are required", http.StatusBadRequest, w, fmt.Errorf("title and description are required"))
		return
	}

	// Get authenticated user ID from query param (passed by Express proxy)
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		config.ErrorStatus("User ID is required", http.StatusBadRequest, w, fmt.Errorf("userId query param is required"))
		return
	}

	userObjID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("Invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	imageURLs := req.ImageURLs
	if imageURLs == nil {
		imageURLs = []string{}
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	featureRequest := models.FeatureRequest{
		ID:           primitive.NewObjectID(),
		Title:        req.Title,
		Description:  req.Description,
		Author:       userObjID,
		Status:       "open",
		ImageURLs:    imageURLs,
		UpvoteCount:  1,
		CommentCount: 0,
		Comments:     []models.FeatureComment{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	_, err = h.DB.InsertOne(ctx, featureRequest)
	if err != nil {
		config.ErrorStatus("Failed to create feature request", http.StatusInternalServerError, w, err)
		return
	}

	// Auto-upvote: author automatically votes on their own request (Reddit-style)
	autoVote := models.FeatureRequestVote{
		ID:               primitive.NewObjectID(),
		FeatureRequestID: featureRequest.ID,
		User:             userObjID,
		CreatedAt:        now,
	}
	h.VDB.InsertOne(ctx, autoVote) // best-effort, don't fail the create if this errors

	// Get user data for response
	authorSummary := h.getUserSummary(ctx, userObjID)

	response := models.FeatureRequestResponse{
		ID:           featureRequest.ID,
		Title:        featureRequest.Title,
		Description:  featureRequest.Description,
		Author:       authorSummary,
		Status:       featureRequest.Status,
		ImageURLs:    imageURLs,
		UpvoteCount:  1,
		CommentCount: 0,
		HasVoted:     true,
		Comments:     []models.FeatureCommentResponse{},
		CreatedAt:    featureRequest.CreatedAt,
		UpdatedAt:    featureRequest.UpdatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

// GetFeatureRequestHandler returns a single feature request with comments
func (h FeatureRequestHandler) GetFeatureRequestHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	userID := r.URL.Query().Get("userId")

	frID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		config.ErrorStatus("Invalid feature request ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	fr, err := h.DB.FindOne(ctx, bson.M{"_id": frID})
	if err != nil {
		config.ErrorStatus("Feature request not found", http.StatusNotFound, w, err)
		return
	}

	// Batch-fetch users for author + comment authors
	userIDs := make(map[primitive.ObjectID]bool)
	userIDs[fr.Author] = true
	if fr.Comments != nil {
		for _, c := range fr.Comments {
			userIDs[c.User] = true
		}
	}
	userMap := h.batchFetchUsers(ctx, userIDs)
	adminRoles := h.getAdminRolesForUsers(ctx, userMap)

	// Check if current user has voted
	hasVoted := false
	if userID != "" {
		userObjID, err := primitive.ObjectIDFromHex(userID)
		if err == nil {
			_, err := h.VDB.FindOne(ctx, bson.M{
				"featureRequestId": frID,
				"user":             userObjID,
			})
			hasVoted = err == nil
		}
	}

	// Build author summary
	authorDoc, exists := userMap[fr.Author]
	authorSummary := models.UserSummary{ID: fr.Author}
	if exists {
		authorSummary.Username = authorDoc.User.Username
		authorSummary.ProfilePicture = authorDoc.User.ProfilePicture
	} else {
		authorSummary.Username = "Unknown"
	}
	if role, ok := adminRoles[fr.Author]; ok {
		authorSummary.AdminRole = &role
	}

	// Build comments
	comments := make([]models.FeatureCommentResponse, 0)
	if fr.Comments != nil {
		for _, c := range fr.Comments {
			commentUserDoc, exists := userMap[c.User]
			commentUserSummary := models.UserSummary{ID: c.User}
			if exists {
				commentUserSummary.Username = commentUserDoc.User.Username
				commentUserSummary.ProfilePicture = commentUserDoc.User.ProfilePicture
			} else {
				commentUserSummary.Username = "Unknown"
			}
			if role, ok := adminRoles[c.User]; ok {
				commentUserSummary.AdminRole = &role
			}

			commentImageURLs := c.ImageURLs
			if commentImageURLs == nil {
				commentImageURLs = []string{}
			}

			comments = append(comments, models.FeatureCommentResponse{
				ID:        c.ID,
				User:      commentUserSummary,
				Content:   c.Content,
				ImageURLs: commentImageURLs,
				Edited:    c.Edited,
				EditedAt:  c.EditedAt,
				CreatedAt: c.CreatedAt,
			})
		}
	}

	imageURLs := fr.ImageURLs
	if imageURLs == nil {
		imageURLs = []string{}
	}

	response := models.FeatureRequestResponse{
		ID:           fr.ID,
		Title:        fr.Title,
		Description:  fr.Description,
		Author:       authorSummary,
		Status:       fr.Status,
		ImageURLs:    imageURLs,
		UpvoteCount:  fr.UpvoteCount,
		CommentCount: fr.CommentCount,
		HasVoted:     hasVoted,
		Comments:     comments,
		CreatedAt:    fr.CreatedAt,
		UpdatedAt:    fr.UpdatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// UpdateFeatureRequestHandler updates a feature request (author only)
func (h FeatureRequestHandler) UpdateFeatureRequestHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	userID := r.URL.Query().Get("userId")

	if userID == "" {
		config.ErrorStatus("User ID is required", http.StatusBadRequest, w, fmt.Errorf("userId query param is required"))
		return
	}

	frID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		config.ErrorStatus("Invalid feature request ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Get existing feature request
	fr, err := h.DB.FindOne(ctx, bson.M{"_id": frID})
	if err != nil {
		config.ErrorStatus("Feature request not found", http.StatusNotFound, w, err)
		return
	}

	// Check ownership
	if fr.Author.Hex() != userID {
		config.ErrorStatus("Unauthorized to update this feature request", http.StatusForbidden, w, fmt.Errorf("only the author can update"))
		return
	}

	var req models.UpdateFeatureRequestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	update := bson.M{}
	if req.Title != nil {
		update["title"] = *req.Title
	}
	if req.Description != nil {
		update["description"] = *req.Description
	}
	if req.ImageURLs != nil {
		update["imageUrls"] = req.ImageURLs
	}
	update["updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	err = h.DB.UpdateOne(ctx, bson.M{"_id": frID}, bson.M{"$set": update})
	if err != nil {
		config.ErrorStatus("Failed to update feature request", http.StatusInternalServerError, w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Feature request updated successfully",
	})
}

// DeleteFeatureRequestHandler deletes a feature request (author or admin)
func (h FeatureRequestHandler) DeleteFeatureRequestHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	userID := r.URL.Query().Get("userId")

	if userID == "" {
		config.ErrorStatus("User ID is required", http.StatusBadRequest, w, fmt.Errorf("userId query param is required"))
		return
	}

	frID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		config.ErrorStatus("Invalid feature request ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	fr, err := h.DB.FindOne(ctx, bson.M{"_id": frID})
	if err != nil {
		config.ErrorStatus("Feature request not found", http.StatusNotFound, w, err)
		return
	}

	// Check ownership or admin
	if fr.Author.Hex() != userID {
		isAdmin := h.checkIsAdmin(ctx, userID)
		if !isAdmin {
			config.ErrorStatus("Unauthorized to delete this feature request", http.StatusForbidden, w, fmt.Errorf("only the author or admin can delete"))
			return
		}
	}

	// Delete the feature request
	err = h.DB.DeleteOne(ctx, bson.M{"_id": frID})
	if err != nil {
		config.ErrorStatus("Failed to delete feature request", http.StatusInternalServerError, w, err)
		return
	}

	// Also delete all associated votes
	h.VDB.DeleteOne(ctx, bson.M{"featureRequestId": frID})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Feature request deleted successfully",
	})
}

// ToggleVoteHandler toggles an upvote on a feature request
func (h FeatureRequestHandler) ToggleVoteHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	userID := r.URL.Query().Get("userId")

	if userID == "" {
		config.ErrorStatus("User ID is required", http.StatusBadRequest, w, fmt.Errorf("userId query param is required"))
		return
	}

	frID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		config.ErrorStatus("Invalid feature request ID", http.StatusBadRequest, w, err)
		return
	}

	userObjID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("Invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Check if feature request exists
	_, err = h.DB.FindOne(ctx, bson.M{"_id": frID})
	if err != nil {
		config.ErrorStatus("Feature request not found", http.StatusNotFound, w, err)
		return
	}

	// Check if user already voted
	voteFilter := bson.M{
		"featureRequestId": frID,
		"user":             userObjID,
	}

	_, err = h.VDB.FindOne(ctx, voteFilter)
	hasVoted := err == nil

	if hasVoted {
		// Remove vote (toggle off)
		err = h.VDB.DeleteOne(ctx, voteFilter)
		if err != nil {
			config.ErrorStatus("Failed to remove vote", http.StatusInternalServerError, w, err)
			return
		}
		// Decrement upvote count
		err = h.DB.UpdateOne(ctx, bson.M{"_id": frID}, bson.M{"$inc": bson.M{"upvoteCount": -1}})
		if err != nil {
			config.ErrorStatus("Failed to update vote count", http.StatusInternalServerError, w, err)
			return
		}
	} else {
		// Add vote
		vote := models.FeatureRequestVote{
			ID:               primitive.NewObjectID(),
			FeatureRequestID: frID,
			User:             userObjID,
			CreatedAt:        primitive.NewDateTimeFromTime(time.Now()),
		}
		_, err = h.VDB.InsertOne(ctx, vote)
		if err != nil {
			config.ErrorStatus("Failed to add vote", http.StatusInternalServerError, w, err)
			return
		}
		// Increment upvote count
		err = h.DB.UpdateOne(ctx, bson.M{"_id": frID}, bson.M{"$inc": bson.M{"upvoteCount": 1}})
		if err != nil {
			config.ErrorStatus("Failed to update vote count", http.StatusInternalServerError, w, err)
			return
		}
	}

	// Get updated feature request for response
	updatedFR, err := h.DB.FindOne(ctx, bson.M{"_id": frID})
	if err != nil {
		config.ErrorStatus("Failed to fetch updated feature request", http.StatusInternalServerError, w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"hasVoted":    !hasVoted,
		"upvoteCount": updatedFR.UpvoteCount,
	})
}

// AddCommentHandler adds a comment to a feature request
func (h FeatureRequestHandler) AddCommentHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	userID := r.URL.Query().Get("userId")

	if userID == "" {
		config.ErrorStatus("User ID is required", http.StatusBadRequest, w, fmt.Errorf("userId query param is required"))
		return
	}

	frID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		config.ErrorStatus("Invalid feature request ID", http.StatusBadRequest, w, err)
		return
	}

	userObjID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("Invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	var req models.AddFeatureCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	if req.Content == "" {
		config.ErrorStatus("Comment content is required", http.StatusBadRequest, w, fmt.Errorf("content is required"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	imageURLs := req.ImageURLs
	if imageURLs == nil {
		imageURLs = []string{}
	}

	newComment := models.FeatureComment{
		ID:        primitive.NewObjectID(),
		User:      userObjID,
		Content:   req.Content,
		ImageURLs: imageURLs,
		Edited:    false,
		EditedAt:  nil,
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
	}

	// Push comment and increment comment count atomically
	update := bson.M{
		"$push": bson.M{"comments": newComment},
		"$inc":  bson.M{"commentCount": 1},
		"$set":  bson.M{"updatedAt": primitive.NewDateTimeFromTime(time.Now())},
	}

	err = h.DB.UpdateOne(ctx, bson.M{"_id": frID}, update)
	if err != nil {
		config.ErrorStatus("Failed to add comment", http.StatusInternalServerError, w, err)
		return
	}

	// Get user data for response (including admin role)
	singleUserIDs := map[primitive.ObjectID]bool{userObjID: true}
	singleUserMap := h.batchFetchUsers(ctx, singleUserIDs)
	userSummary := models.UserSummary{ID: userObjID}
	if doc, ok := singleUserMap[userObjID]; ok {
		userSummary.Username = doc.User.Username
		userSummary.ProfilePicture = doc.User.ProfilePicture
	} else {
		userSummary.Username = "Unknown"
	}
	if roles := h.getAdminRolesForUsers(ctx, singleUserMap); len(roles) > 0 {
		if role, ok := roles[userObjID]; ok {
			userSummary.AdminRole = &role
		}
	}

	commentResponse := models.FeatureCommentResponse{
		ID:        newComment.ID,
		User:      userSummary,
		Content:   newComment.Content,
		ImageURLs: imageURLs,
		Edited:    false,
		CreatedAt: newComment.CreatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"comment": commentResponse,
	})
}

// UpdateCommentHandler updates a comment on a feature request (author only)
func (h FeatureRequestHandler) UpdateCommentHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	commentID := mux.Vars(r)["commentId"]
	userID := r.URL.Query().Get("userId")

	if userID == "" {
		config.ErrorStatus("User ID is required", http.StatusBadRequest, w, fmt.Errorf("userId query param is required"))
		return
	}

	frID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		config.ErrorStatus("Invalid feature request ID", http.StatusBadRequest, w, err)
		return
	}

	commentObjID, err := primitive.ObjectIDFromHex(commentID)
	if err != nil {
		config.ErrorStatus("Invalid comment ID", http.StatusBadRequest, w, err)
		return
	}

	userObjID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("Invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	var req models.UpdateFeatureCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	if req.Content == "" {
		config.ErrorStatus("Comment content is required", http.StatusBadRequest, w, fmt.Errorf("content is required"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Verify the comment belongs to this user
	fr, err := h.DB.FindOne(ctx, bson.M{"_id": frID})
	if err != nil {
		config.ErrorStatus("Feature request not found", http.StatusNotFound, w, err)
		return
	}

	commentFound := false
	for _, c := range fr.Comments {
		if c.ID == commentObjID {
			if c.User != userObjID {
				config.ErrorStatus("Unauthorized to update this comment", http.StatusForbidden, w, fmt.Errorf("only the comment author can update"))
				return
			}
			commentFound = true
			break
		}
	}

	if !commentFound {
		config.ErrorStatus("Comment not found", http.StatusNotFound, w, fmt.Errorf("comment not found"))
		return
	}

	// Update the comment using array filters
	now := primitive.NewDateTimeFromTime(time.Now())
	filter := bson.M{"_id": frID}
	update := bson.M{
		"$set": bson.M{
			"comments.$[elem].content":  req.Content,
			"comments.$[elem].edited":   true,
			"comments.$[elem].editedAt": now,
		},
	}
	arrayFilters := options.Update().SetArrayFilters(options.ArrayFilters{
		Filters: []interface{}{bson.M{"elem._id": commentObjID}},
	})

	err = h.DB.UpdateOne(ctx, filter, update, arrayFilters)
	if err != nil {
		config.ErrorStatus("Failed to update comment", http.StatusInternalServerError, w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Comment updated successfully",
	})
}

// DeleteCommentHandler deletes a comment from a feature request (author or admin)
func (h FeatureRequestHandler) DeleteCommentHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	commentID := mux.Vars(r)["commentId"]
	userID := r.URL.Query().Get("userId")

	if userID == "" {
		config.ErrorStatus("User ID is required", http.StatusBadRequest, w, fmt.Errorf("userId query param is required"))
		return
	}

	frID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		config.ErrorStatus("Invalid feature request ID", http.StatusBadRequest, w, err)
		return
	}

	commentObjID, err := primitive.ObjectIDFromHex(commentID)
	if err != nil {
		config.ErrorStatus("Invalid comment ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Verify ownership or admin
	fr, err := h.DB.FindOne(ctx, bson.M{"_id": frID})
	if err != nil {
		config.ErrorStatus("Feature request not found", http.StatusNotFound, w, err)
		return
	}

	commentFound := false
	for _, c := range fr.Comments {
		if c.ID == commentObjID {
			if c.User.Hex() != userID {
				isAdmin := h.checkIsAdmin(ctx, userID)
				if !isAdmin {
					config.ErrorStatus("Unauthorized to delete this comment", http.StatusForbidden, w, fmt.Errorf("only the comment author or admin can delete"))
					return
				}
			}
			commentFound = true
			break
		}
	}

	if !commentFound {
		config.ErrorStatus("Comment not found", http.StatusNotFound, w, fmt.Errorf("comment not found"))
		return
	}

	// Pull the comment and decrement comment count
	update := bson.M{
		"$pull": bson.M{"comments": bson.M{"_id": commentObjID}},
		"$inc":  bson.M{"commentCount": -1},
	}

	err = h.DB.UpdateOne(ctx, bson.M{"_id": frID}, update)
	if err != nil {
		config.ErrorStatus("Failed to delete comment", http.StatusInternalServerError, w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Comment deleted successfully",
	})
}

// UpdateStatusHandler updates the status of a feature request (admin only)
func (h FeatureRequestHandler) UpdateStatusHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	userID := r.URL.Query().Get("userId")

	if userID == "" {
		config.ErrorStatus("User ID is required", http.StatusBadRequest, w, fmt.Errorf("userId query param is required"))
		return
	}

	frID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		config.ErrorStatus("Invalid feature request ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Check if user is admin
	isAdmin := h.checkIsAdmin(ctx, userID)
	if !isAdmin {
		config.ErrorStatus("Only admins can update feature request status", http.StatusForbidden, w, fmt.Errorf("admin access required"))
		return
	}

	var req models.UpdateFeatureRequestStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	validStatuses := map[string]bool{
		"open": true, "under_review": true, "planned": true,
		"in_progress": true, "released": true, "declined": true,
	}
	if !validStatuses[req.Status] {
		config.ErrorStatus("Invalid status", http.StatusBadRequest, w, fmt.Errorf("invalid status: %s", req.Status))
		return
	}

	update := bson.M{
		"$set": bson.M{
			"status":    req.Status,
			"updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	err = h.DB.UpdateOne(ctx, bson.M{"_id": frID}, update)
	if err != nil {
		config.ErrorStatus("Failed to update status", http.StatusInternalServerError, w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Status updated successfully",
		"status":  req.Status,
	})
}

// batchFetchUsers fetches multiple users by ID in a single query
func (h FeatureRequestHandler) batchFetchUsers(ctx context.Context, userIDs map[primitive.ObjectID]bool) map[primitive.ObjectID]UserDoc {
	userMap := make(map[primitive.ObjectID]UserDoc)
	if len(userIDs) == 0 {
		return userMap
	}

	userIDList := make([]primitive.ObjectID, 0, len(userIDs))
	for id := range userIDs {
		userIDList = append(userIDList, id)
	}

	userFilter := bson.M{"_id": bson.M{"$in": userIDList}}
	userCursor, err := h.UDB.Find(ctx, userFilter)
	if err != nil {
		return userMap
	}
	defer userCursor.Close(ctx)

	var users []UserDoc
	if err := userCursor.All(ctx, &users); err == nil {
		for _, user := range users {
			userMap[user.ID] = user
		}
	}

	return userMap
}

// getAdminRolesForUsers looks up admin roles for users based on their emails.
// Returns a map of userID → admin role ("owner" or "admin").
func (h FeatureRequestHandler) getAdminRolesForUsers(ctx context.Context, userMap map[primitive.ObjectID]UserDoc) map[primitive.ObjectID]string {
	adminRoles := make(map[primitive.ObjectID]string)
	if h.AdminDB == nil || len(userMap) == 0 {
		return adminRoles
	}

	// Build email → userID mapping
	emailToUserID := make(map[string]primitive.ObjectID)
	emails := make([]string, 0)
	for id, doc := range userMap {
		if doc.User.Email != "" {
			lower := strings.ToLower(doc.User.Email)
			emailToUserID[lower] = id
			emails = append(emails, doc.User.Email)
		}
	}

	if len(emails) == 0 {
		return adminRoles
	}

	cursor, err := h.AdminDB.Find(ctx, bson.M{"email": bson.M{"$in": emails}})
	if err != nil {
		return adminRoles
	}
	defer cursor.Close(ctx)

	var admins []models.AdminUser
	if err := cursor.All(ctx, &admins); err == nil {
		for _, admin := range admins {
			if userID, ok := emailToUserID[strings.ToLower(admin.Email)]; ok {
				adminRoles[userID] = admin.Role
			}
		}
	}

	return adminRoles
}

// getUserSummary gets a user summary for a single user
func (h FeatureRequestHandler) getUserSummary(ctx context.Context, userID primitive.ObjectID) models.UserSummary {
	userDoc := UserDoc{}
	result := h.UDB.FindOne(ctx, bson.M{"_id": userID})
	if err := result.Decode(&userDoc); err != nil {
		return models.UserSummary{
			ID:       userID,
			Username: "Unknown",
		}
	}
	return models.UserSummary{
		ID:             userDoc.ID,
		Username:       userDoc.User.Username,
		ProfilePicture: userDoc.User.ProfilePicture,
	}
}

// checkIsAdmin checks if a user is a site admin
func (h FeatureRequestHandler) checkIsAdmin(ctx context.Context, userID string) bool {
	if h.AdminDB == nil {
		return false
	}
	// Check admin database for this user
	admin, err := h.AdminDB.FindOne(ctx, bson.M{"email": userID})
	if err == nil && admin != nil {
		return true
	}
	// Also try by ID
	userObjID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return false
	}
	admin, err = h.AdminDB.FindOne(ctx, bson.M{"_id": userObjID})
	return err == nil && admin != nil
}
