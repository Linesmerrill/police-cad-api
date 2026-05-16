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
	"go.mongodb.org/mongo-driver/mongo"
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
	excludeStatuses := r.URL.Query()["excludeStatus"]
	query := r.URL.Query().Get("q")
	userID := r.URL.Query().Get("userId")
	authorID := r.URL.Query().Get("authorId")

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Build filter — exclude merged tickets by default; callers may additionally
	// exclude released/declined from default browsing so the main list stays
	// focused on in-flight work.
	filter := bson.M{}
	if status != "" {
		filter["status"] = status
	} else {
		excluded := []string{"merged"}
		for _, s := range excludeStatuses {
			if s != "" {
				excluded = append(excluded, s)
			}
		}
		filter["status"] = bson.M{"$nin": excluded}
	}
	if query != "" {
		escaped := regexp.QuoteMeta(query)
		filter["$or"] = []bson.M{
			{"title": bson.M{"$regex": escaped, "$options": "i"}},
			{"description": bson.M{"$regex": escaped, "$options": "i"}},
		}
	}
	if authorID != "" {
		if authorObjID, err := primitive.ObjectIDFromHex(authorID); err == nil {
			filter["author"] = authorObjID
		}
	}

	skip := (page - 1) * limit

	// Run Find/Aggregate + Count in parallel
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
		if sortBy == "trending" {
			// HN-style trending: score = (votes + comments*0.5) / (ageHours + 2)^1.5
			pipeline := mongo.Pipeline{
				{{Key: "$match", Value: filter}},
				{{Key: "$addFields", Value: bson.D{
					{Key: "trendingScore", Value: bson.D{
						{Key: "$divide", Value: bson.A{
							bson.D{{Key: "$add", Value: bson.A{
								"$upvoteCount",
								bson.D{{Key: "$multiply", Value: bson.A{"$commentCount", 0.5}}},
							}}},
							bson.D{{Key: "$pow", Value: bson.A{
								bson.D{{Key: "$add", Value: bson.A{
									bson.D{{Key: "$divide", Value: bson.A{
										bson.D{{Key: "$subtract", Value: bson.A{"$$NOW", "$createdAt"}}},
										3600000.0, // ms to hours
									}}},
									2.0, // offset so brand-new posts don't divide by zero
								}}},
								1.5, // gravity — higher = faster decay
							}}},
						}},
					}},
				}}},
				{{Key: "$sort", Value: bson.D{{Key: "trendingScore", Value: -1}}}},
				{{Key: "$skip", Value: int64(skip)}},
				{{Key: "$limit", Value: int64(limit)}},
			}

			cursor, err := h.DB.Aggregate(ctx, pipeline)
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
		} else {
			// top / newest / default
			var sort bson.D
			switch sortBy {
			case "top":
				sort = bson.D{{Key: "upvoteCount", Value: -1}, {Key: "createdAt", Value: -1}}
			case "newest":
				sort = bson.D{{Key: "createdAt", Value: -1}}
			default:
				sort = bson.D{{Key: "createdAt", Value: -1}}
			}

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
		}
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

	// Phase 2 (parallel): batch-fetch users AND batch-check votes. They don't
	// depend on each other.
	userIDs := make(map[primitive.ObjectID]bool)
	for _, req := range requests {
		userIDs[req.Author] = true
	}

	userMapCh := make(chan map[primitive.ObjectID]UserDoc, 1)
	go func() { userMapCh <- h.batchFetchUsers(ctx, userIDs) }()

	voteMapCh := make(chan map[primitive.ObjectID]bool, 1)
	go func() {
		voteMap := make(map[primitive.ObjectID]bool)
		if userID == "" || len(requests) == 0 {
			voteMapCh <- voteMap
			return
		}
		userObjID, err := primitive.ObjectIDFromHex(userID)
		if err != nil {
			voteMapCh <- voteMap
			return
		}
		frIDs := make([]primitive.ObjectID, len(requests))
		for i, req := range requests {
			frIDs[i] = req.ID
		}
		voteCursor, err := h.VDB.Find(ctx, bson.M{
			"featureRequestId": bson.M{"$in": frIDs},
			"user":             userObjID,
		})
		if err != nil {
			voteMapCh <- voteMap
			return
		}
		defer voteCursor.Close(ctx)
		var votes []models.FeatureRequestVote
		if err := voteCursor.All(ctx, &votes); err == nil {
			for _, v := range votes {
				voteMap[v.FeatureRequestID] = true
			}
		}
		voteMapCh <- voteMap
	}()

	userMap := <-userMapCh
	voteMap := <-voteMapCh

	// Phase 3: admin roles depend on userMap (needs emails).
	adminRoles := h.getAdminRolesForUsers(ctx, userMap)

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

	// Phase 1: kick off the feature-request fetch and the vote check in
	// parallel. The vote check only needs frID + userID, so it doesn't have to
	// wait for the FR doc.
	type frResult struct {
		fr  *models.FeatureRequest
		err error
	}
	frCh := make(chan frResult, 1)
	go func() {
		fr, err := h.DB.FindOne(ctx, bson.M{"_id": frID})
		frCh <- frResult{fr, err}
	}()

	hasVotedCh := make(chan bool, 1)
	go func() {
		if userID == "" {
			hasVotedCh <- false
			return
		}
		userObjID, err := primitive.ObjectIDFromHex(userID)
		if err != nil {
			hasVotedCh <- false
			return
		}
		_, err = h.VDB.FindOne(ctx, bson.M{
			"featureRequestId": frID,
			"user":             userObjID,
		})
		hasVotedCh <- err == nil
	}()

	frRes := <-frCh
	if frRes.err != nil {
		config.ErrorStatus("Feature request not found", http.StatusNotFound, w, frRes.err)
		return
	}
	fr := frRes.fr

	// Phase 2: with the FR doc in hand, fan out the dependent queries:
	//   - batchFetchUsers (author + comment authors)
	//   - mergedInto lookup (if any)
	//   - mergedFrom lookup (if any)
	userIDs := make(map[primitive.ObjectID]bool)
	userIDs[fr.Author] = true
	if fr.Comments != nil {
		for _, c := range fr.Comments {
			userIDs[c.User] = true
		}
	}

	type userMapResult struct{ m map[primitive.ObjectID]UserDoc }
	userMapCh := make(chan userMapResult, 1)
	go func() { userMapCh <- userMapResult{h.batchFetchUsers(ctx, userIDs)} }()

	type mergedIntoResult struct {
		summary *models.MergedRequestSummary
	}
	mergedIntoCh := make(chan mergedIntoResult, 1)
	go func() {
		if fr.MergedInto == nil {
			mergedIntoCh <- mergedIntoResult{nil}
			return
		}
		mi, err := h.DB.FindOne(ctx, bson.M{"_id": *fr.MergedInto})
		if err != nil {
			mergedIntoCh <- mergedIntoResult{nil}
			return
		}
		mergedIntoCh <- mergedIntoResult{&models.MergedRequestSummary{
			ID:    mi.ID,
			Title: mi.Title,
		}}
	}()

	type mergedFromResult struct {
		summaries []models.MergedRequestSummary
	}
	mergedFromCh := make(chan mergedFromResult, 1)
	go func() {
		if len(fr.MergedFrom) == 0 {
			mergedFromCh <- mergedFromResult{nil}
			return
		}
		summaries := make([]models.MergedRequestSummary, 0, len(fr.MergedFrom))
		cursor, err := h.DB.Find(ctx, bson.M{"_id": bson.M{"$in": fr.MergedFrom}})
		if err != nil {
			mergedFromCh <- mergedFromResult{summaries}
			return
		}
		defer cursor.Close(ctx)
		var mergedFRs []models.FeatureRequest
		if err := cursor.All(ctx, &mergedFRs); err == nil {
			for _, mfr := range mergedFRs {
				summaries = append(summaries, models.MergedRequestSummary{
					ID:    mfr.ID,
					Title: mfr.Title,
				})
			}
		}
		mergedFromCh <- mergedFromResult{summaries}
	}()

	userMap := (<-userMapCh).m
	mergedIntoSummary := (<-mergedIntoCh).summary
	mergedFromSummaries := (<-mergedFromCh).summaries
	hasVoted := <-hasVotedCh

	// Phase 3: admin-role lookup depends on userMap (we need user emails).
	adminRoles := h.getAdminRolesForUsers(ctx, userMap)

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
		MergedInto:   mergedIntoSummary,
		MergedFrom:   mergedFromSummaries,
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
	fr, err := h.DB.FindOne(ctx, bson.M{"_id": frID})
	if err != nil {
		config.ErrorStatus("Feature request not found", http.StatusNotFound, w, err)
		return
	}

	// Reject votes on closed tickets (merged, released, declined)
	if fr.Status == "merged" || fr.Status == "released" || fr.Status == "declined" {
		config.ErrorStatus("Cannot vote on a closed feature request", http.StatusBadRequest, w, fmt.Errorf("feature request status is %s", fr.Status))
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

	// Reject comments on merged tickets
	frCheck, err := h.DB.FindOne(ctx, bson.M{"_id": frID})
	if err != nil {
		config.ErrorStatus("Feature request not found", http.StatusNotFound, w, err)
		return
	}
	if frCheck.Status == "merged" || frCheck.Status == "released" || frCheck.Status == "declined" {
		config.ErrorStatus("Cannot comment on a closed feature request", http.StatusBadRequest, w, fmt.Errorf("feature request status is %s", frCheck.Status))
		return
	}

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
		"open": true, "planned": true, "beta_testing": true,
		"released": true, "declined": true,
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

// MergeHandler merges a source feature request into a target (admin only)
func (h FeatureRequestHandler) MergeHandler(w http.ResponseWriter, r *http.Request) {
	targetID := mux.Vars(r)["targetId"]
	userID := r.URL.Query().Get("userId")

	if userID == "" {
		config.ErrorStatus("User ID is required", http.StatusBadRequest, w, fmt.Errorf("userId query param is required"))
		return
	}

	targetObjID, err := primitive.ObjectIDFromHex(targetID)
	if err != nil {
		config.ErrorStatus("Invalid target ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Admin only
	if !h.checkIsAdmin(ctx, userID) {
		config.ErrorStatus("Only admins can merge feature requests", http.StatusForbidden, w, fmt.Errorf("admin access required"))
		return
	}

	var req models.MergeFeatureRequestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	sourceObjID, err := primitive.ObjectIDFromHex(req.SourceID)
	if err != nil {
		config.ErrorStatus("Invalid source ID", http.StatusBadRequest, w, err)
		return
	}

	if sourceObjID == targetObjID {
		config.ErrorStatus("Cannot merge a request into itself", http.StatusBadRequest, w, fmt.Errorf("source and target are the same"))
		return
	}

	// Fetch both tickets
	source, err := h.DB.FindOne(ctx, bson.M{"_id": sourceObjID})
	if err != nil {
		config.ErrorStatus("Source feature request not found", http.StatusNotFound, w, err)
		return
	}
	target, err := h.DB.FindOne(ctx, bson.M{"_id": targetObjID})
	if err != nil {
		config.ErrorStatus("Target feature request not found", http.StatusNotFound, w, err)
		return
	}

	// Validate neither is already merged
	if source.MergedInto != nil {
		config.ErrorStatus("Source is already merged into another request", http.StatusBadRequest, w, fmt.Errorf("source already merged"))
		return
	}
	if target.MergedInto != nil {
		config.ErrorStatus("Target is already merged into another request", http.StatusBadRequest, w, fmt.Errorf("target already merged"))
		return
	}

	// Transfer unique votes from source to target
	sourceVoteCursor, err := h.VDB.Find(ctx, bson.M{"featureRequestId": sourceObjID})
	newVoteCount := 0
	if err == nil {
		var sourceVotes []models.FeatureRequestVote
		if err := sourceVoteCursor.All(ctx, &sourceVotes); err == nil {
			for _, sv := range sourceVotes {
				// Check if user already voted on target
				_, err := h.VDB.FindOne(ctx, bson.M{
					"featureRequestId": targetObjID,
					"user":             sv.User,
				})
				if err != nil {
					// User hasn't voted on target — create vote
					newVote := models.FeatureRequestVote{
						ID:               primitive.NewObjectID(),
						FeatureRequestID: targetObjID,
						User:             sv.User,
						CreatedAt:        sv.CreatedAt,
					}
					h.VDB.InsertOne(ctx, newVote)
					newVoteCount++
				}
			}
		}
		sourceVoteCursor.Close(ctx)
	}

	// Update source: status = "merged", mergedInto = targetID
	now := primitive.NewDateTimeFromTime(time.Now())
	err = h.DB.UpdateOne(ctx, bson.M{"_id": sourceObjID}, bson.M{
		"$set": bson.M{
			"status":     "merged",
			"mergedInto": targetObjID,
			"updatedAt":  now,
		},
	})
	if err != nil {
		config.ErrorStatus("Failed to update source feature request", http.StatusInternalServerError, w, err)
		return
	}

	// Update target: push sourceID to mergedFrom, increment upvoteCount
	err = h.DB.UpdateOne(ctx, bson.M{"_id": targetObjID}, bson.M{
		"$push": bson.M{"mergedFrom": sourceObjID},
		"$inc":  bson.M{"upvoteCount": newVoteCount},
		"$set":  bson.M{"updatedAt": now},
	})
	if err != nil {
		config.ErrorStatus("Failed to update target feature request", http.StatusInternalServerError, w, err)
		return
	}

	// Get updated target for response
	updatedTarget, _ := h.DB.FindOne(ctx, bson.M{"_id": targetObjID})
	targetUpvoteCount := target.UpvoteCount + newVoteCount
	if updatedTarget != nil {
		targetUpvoteCount = updatedTarget.UpvoteCount
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"message":           "Feature request merged successfully",
		"sourceId":          sourceObjID.Hex(),
		"targetId":          targetObjID.Hex(),
		"targetUpvoteCount": targetUpvoteCount,
		"sourceTitle":       source.Title,
		"targetTitle":       target.Title,
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

// getAdminRolesForUsers resolves which users in userMap have elevated admin
// access. Resolution order per admin:
//  1. If the admin has a linked LPC account (LinkedUserID), the admin's role
//     applies to that linked user only — not the admin's own email match.
//  2. If the admin has no linked LPC account, the admin's role applies to any
//     user whose email matches the admin's email (legacy behavior).
//
// This means once an admin links an LPC account, creating a new LPC account
// with the admin's email no longer confers elevated access.
func (h FeatureRequestHandler) getAdminRolesForUsers(ctx context.Context, userMap map[primitive.ObjectID]UserDoc) map[primitive.ObjectID]string {
	adminRoles := make(map[primitive.ObjectID]string)
	if h.AdminDB == nil || len(userMap) == 0 {
		return adminRoles
	}

	emailToUserID := make(map[string]primitive.ObjectID)
	userIDs := make(map[primitive.ObjectID]bool)
	emails := make([]string, 0)
	for id, doc := range userMap {
		userIDs[id] = true
		if doc.User.Email != "" {
			lower := strings.ToLower(doc.User.Email)
			emailToUserID[lower] = id
			emails = append(emails, doc.User.Email)
		}
	}

	// Fetch any admins whose email or linkedUserId matches a user in scope.
	or := []bson.M{}
	if len(emails) > 0 {
		or = append(or, bson.M{"email": bson.M{"$in": emails}})
	}
	if len(userIDs) > 0 {
		ids := make([]primitive.ObjectID, 0, len(userIDs))
		for id := range userIDs {
			ids = append(ids, id)
		}
		or = append(or, bson.M{"linkedUserId": bson.M{"$in": ids}})
	}
	if len(or) == 0 {
		return adminRoles
	}

	cursor, err := h.AdminDB.Find(ctx, bson.M{"$or": or})
	if err != nil {
		return adminRoles
	}
	defer cursor.Close(ctx)

	var admins []models.AdminUser
	if err := cursor.All(ctx, &admins); err != nil {
		return adminRoles
	}

	for _, admin := range admins {
		if admin.LinkedUserID != nil {
			// Linked admins attribute their role only to the linked user.
			if userIDs[*admin.LinkedUserID] {
				adminRoles[*admin.LinkedUserID] = admin.Role
			}
			continue
		}
		// Unlinked admins fall back to email matching.
		if userID, ok := emailToUserID[strings.ToLower(admin.Email)]; ok {
			// Don't override a role already attributed via a link.
			if _, exists := adminRoles[userID]; !exists {
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

// checkIsAdmin checks if a user is a site admin. Mirrors the resolution rules
// in getAdminRolesForUsers: a Linked LPC Account inherits its admin's role,
// and unlinked admins fall back to email matching. An admin's own email no
// longer confers privileges on a different LPC account once the admin has
// linked elsewhere.
//
// userID may be either an LPC user's hex ObjectID (e.g. delete handler) or
// the user's email (e.g. status / merge handlers).
func (h FeatureRequestHandler) checkIsAdmin(ctx context.Context, userID string) bool {
	if h.AdminDB == nil || userID == "" {
		return false
	}

	unlinked := bson.M{"$or": []bson.M{
		{"linkedUserId": bson.M{"$exists": false}},
		{"linkedUserId": nil},
	}}

	// Path 1: userID is a hex ObjectID — a user._id.
	if userObjID, err := primitive.ObjectIDFromHex(userID); err == nil {
		if admin, err := h.AdminDB.FindOne(ctx, bson.M{"linkedUserId": userObjID}); err == nil && admin != nil {
			return true
		}
		// Fallback: resolve the user's email, then look up an unlinked admin
		// by that email.
		if h.UDB != nil {
			var userDoc UserDoc
			if err := h.UDB.FindOne(ctx, bson.M{"_id": userObjID}).Decode(&userDoc); err == nil && userDoc.User.Email != "" {
				filter := bson.M{
					"email": bson.M{"$regex": "^" + regexp.QuoteMeta(userDoc.User.Email) + "$", "$options": "i"},
					"$and":  []bson.M{unlinked},
				}
				if admin, err := h.AdminDB.FindOne(ctx, filter); err == nil && admin != nil {
					return true
				}
			}
		}
		return false
	}

	// Path 2: userID is an email.
	email := userID
	if h.UDB != nil {
		var userDoc UserDoc
		if err := h.UDB.FindOne(ctx, bson.M{
			"user.email": bson.M{"$regex": "^" + regexp.QuoteMeta(email) + "$", "$options": "i"},
		}).Decode(&userDoc); err == nil {
			if admin, err := h.AdminDB.FindOne(ctx, bson.M{"linkedUserId": userDoc.ID}); err == nil && admin != nil {
				return true
			}
		}
	}
	filter := bson.M{
		"email": bson.M{"$regex": "^" + regexp.QuoteMeta(email) + "$", "$options": "i"},
		"$and":  []bson.M{unlinked},
	}
	if admin, err := h.AdminDB.FindOne(ctx, filter); err == nil && admin != nil {
		return true
	}
	return false
}
