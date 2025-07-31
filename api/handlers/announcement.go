package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Announcement struct for handling announcement operations
type Announcement struct {
	ADB databases.AnnouncementDatabase
	UDB databases.UserDatabase
	CDB databases.CommunityDatabase
}

// GetAnnouncementsHandler returns paginated announcements for a community
func (a Announcement) GetAnnouncementsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	
	// Parse query parameters
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 10
	}
	
	announcementType := r.URL.Query().Get("type")
	
	// Convert community ID to ObjectID
	commID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("Invalid community ID", http.StatusBadRequest, w, err)
		return
	}
	
	// Build filter
	filter := bson.M{
		"community": commID,
		"isActive":  true,
	}
	
	if announcementType != "" {
		filter["type"] = announcementType
	}
	
	// Calculate skip value for pagination
	skip := (page - 1) * limit
	
	// Get announcements with populated user data
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: filter}},
		{{Key: "$lookup", Value: bson.M{
			"from":         "users",
			"localField":   "creator",
			"foreignField": "_id",
			"as":           "creatorData",
		}}},
		{{Key: "$unwind", Value: "$creatorData"}},
		{{Key: "$addFields", Value: bson.M{
			"creator": bson.M{
				"_id":             "$creatorData._id",
				"username":        "$creatorData.user.username",
				"profilePicture":  "$creatorData.user.profilePicture",
			},
		}}},
		{{Key: "$skip", Value: int64(skip)}},
		{{Key: "$limit", Value: int64(limit)}},
	}
	
	// Execute aggregation
	cursor, err := a.ADB.Aggregate(context.Background(), pipeline)
	if err != nil {
		config.ErrorStatus("Failed to fetch announcements", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())
	
	// Decode results
	var announcements []models.AnnouncementResponse
	if err := cursor.All(context.Background(), &announcements); err != nil {
		config.ErrorStatus("Failed to decode announcements", http.StatusInternalServerError, w, err)
		return
	}
	
	// Get total count for pagination
	totalCount, err := a.ADB.CountDocuments(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("Failed to count announcements", http.StatusInternalServerError, w, err)
		return
	}
	
	// Calculate pagination info
	totalPages := int((totalCount + int64(limit) - 1) / int64(limit))
	hasNextPage := page < totalPages
	hasPrevPage := page > 1
	
	response := models.PaginatedAnnouncementsResponse{
		Success:       true,
		Announcements: announcements,
		Pagination: models.PaginationInfo{
			CurrentPage:        page,
			TotalPages:         totalPages,
			TotalAnnouncements: int(totalCount),
			HasNextPage:        hasNextPage,
			HasPrevPage:        hasPrevPage,
		},
	}
	
	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// GetAnnouncementHandler returns a single announcement and increments view count
func (a Announcement) GetAnnouncementHandler(w http.ResponseWriter, r *http.Request) {
	announcementID := mux.Vars(r)["announcementId"]
	
	// Convert announcement ID to ObjectID
	annID, err := primitive.ObjectIDFromHex(announcementID)
	if err != nil {
		config.ErrorStatus("Invalid announcement ID", http.StatusBadRequest, w, err)
		return
	}
	
	// Increment view count and get announcement
	filter := bson.M{"_id": annID}
	update := bson.M{"$inc": bson.M{"viewCount": 1}}
	
	announcement, err := a.ADB.FindOneAndUpdate(
		context.Background(),
		filter,
		update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)
	if err != nil {
		config.ErrorStatus("Announcement not found", http.StatusNotFound, w, err)
		return
	}
	
	// Get creator user data
	creatorResult := a.UDB.FindOne(context.Background(), bson.M{"_id": announcement.Creator.Hex()})
	var creator models.User
	if err := creatorResult.Decode(&creator); err != nil {
		config.ErrorStatus("Failed to fetch creator data", http.StatusInternalServerError, w, err)
		return
	}
	
	// Build response with populated user data
	response := models.AnnouncementResponse{
		ID:        announcement.ID,
		Community: announcement.Community,
		Creator: models.UserSummary{
			ID:             announcement.Creator,
			Username:       creator.Details.Username,
			ProfilePicture: &creator.Details.ProfilePicture,
		},
		Type:      announcement.Type,
		Title:     announcement.Title,
		Content:   announcement.Content,
		Priority:  announcement.Priority,
		IsActive:  announcement.IsActive,
		IsPinned:  announcement.IsPinned,
		StartTime: announcement.StartTime,
		EndTime:   announcement.EndTime,
		ViewCount: announcement.ViewCount,
		CreatedAt: announcement.CreatedAt,
		UpdatedAt: announcement.UpdatedAt,
	}
	
	// Populate reactions
	for _, reaction := range announcement.Reactions {
		userResult := a.UDB.FindOne(context.Background(), bson.M{"_id": reaction.User.Hex()})
		var user models.User
		if err := userResult.Decode(&user); err != nil {
			continue // Skip if user not found
		}
		
		response.Reactions = append(response.Reactions, models.ReactionResponse{
			User: models.UserSummary{
				ID:             reaction.User,
				Username:       user.Details.Username,
				ProfilePicture: &user.Details.ProfilePicture,
			},
			Emoji:     reaction.Emoji,
			Timestamp: reaction.Timestamp,
		})
	}
	
	// Populate comments
	for _, comment := range announcement.Comments {
		userResult := a.UDB.FindOne(context.Background(), bson.M{"_id": comment.User.Hex()})
		var user models.User
		if err := userResult.Decode(&user); err != nil {
			continue // Skip if user not found
		}
		
		response.Comments = append(response.Comments, models.CommentResponse{
			ID: comment.ID,
			User: models.UserSummary{
				ID:             comment.User,
				Username:       user.Details.Username,
				ProfilePicture: &user.Details.ProfilePicture,
			},
			Content:   comment.Content,
			Timestamp: comment.Timestamp,
			Edited:    comment.Edited,
			EditedAt:  comment.EditedAt,
		})
	}
	
	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"announcement": response,
	})
}

// CreateAnnouncementHandler creates a new announcement
func (a Announcement) CreateAnnouncementHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	
	// Get user ID from context (set by middleware)
	userID := r.Context().Value("user_id").(string)
	
	// Parse request body
	var req models.CreateAnnouncementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}
	
	// Convert community ID to ObjectID
	commID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("Invalid community ID", http.StatusBadRequest, w, err)
		return
	}
	
	// Convert user ID to ObjectID
	creatorID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("Invalid user ID", http.StatusBadRequest, w, err)
		return
	}
	
	// Check if user is a member of the community
	community, err := a.CDB.FindOne(context.Background(), bson.M{"_id": commID})
	if err != nil {
		config.ErrorStatus("Community not found", http.StatusNotFound, w, err)
		return
	}
	
	// Check if user is a member
	if _, exists := community.Details.Members[userID]; !exists {
		config.ErrorStatus("User is not a member of this community", http.StatusForbidden, w, nil)
		return
	}
	
	// Create new announcement
	now := primitive.NewDateTimeFromTime(time.Now())
	announcement := models.Announcement{
		ID:        primitive.NewObjectID(),
		Community: commID,
		Creator:   creatorID,
		Type:      req.Type,
		Title:     req.Title,
		Content:   req.Content,
		Priority:  req.Priority,
		IsActive:  true,
		IsPinned:  req.IsPinned,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Reactions: []models.Reaction{},
		Comments:  []models.Comment{},
		ViewCount: 0,
		CreatedAt: now,
		UpdatedAt: now,
	}
	
	// Insert announcement
	_, err = a.ADB.InsertOne(context.Background(), announcement)
	if err != nil {
		config.ErrorStatus("Failed to create announcement", http.StatusInternalServerError, w, err)
		return
	}
	
	// Get creator user data for response
	creatorResult := a.UDB.FindOne(context.Background(), bson.M{"_id": userID})
	var creator models.User
	if err := creatorResult.Decode(&creator); err != nil {
		config.ErrorStatus("Failed to fetch creator data", http.StatusInternalServerError, w, err)
		return
	}
	
	// Build response
	response := models.AnnouncementResponse{
		ID:        announcement.ID,
		Community: announcement.Community,
		Creator: models.UserSummary{
			ID:             announcement.Creator,
			Username:       creator.Details.Username,
			ProfilePicture: &creator.Details.ProfilePicture,
		},
		Type:      announcement.Type,
		Title:     announcement.Title,
		Content:   announcement.Content,
		Priority:  announcement.Priority,
		IsActive:  announcement.IsActive,
		IsPinned:  announcement.IsPinned,
		StartTime: announcement.StartTime,
		EndTime:   announcement.EndTime,
		Reactions: []models.ReactionResponse{},
		Comments:  []models.CommentResponse{},
		ViewCount: announcement.ViewCount,
		CreatedAt: announcement.CreatedAt,
		UpdatedAt: announcement.UpdatedAt,
	}
	
	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"announcement": response,
	})
}

// UpdateAnnouncementHandler updates an existing announcement
func (a Announcement) UpdateAnnouncementHandler(w http.ResponseWriter, r *http.Request) {
	announcementID := mux.Vars(r)["announcementId"]
	
	// Get user ID from context
	userID := r.Context().Value("user_id").(string)
	
	// Parse request body
	var req models.UpdateAnnouncementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}
	
	// Convert announcement ID to ObjectID
	annID, err := primitive.ObjectIDFromHex(announcementID)
	if err != nil {
		config.ErrorStatus("Invalid announcement ID", http.StatusBadRequest, w, err)
		return
	}
	
	// Get existing announcement
	announcement, err := a.ADB.FindOne(context.Background(), bson.M{"_id": annID})
	if err != nil {
		config.ErrorStatus("Announcement not found", http.StatusNotFound, w, err)
		return
	}
	
	// Check if user is the creator or has admin permissions
	if announcement.Creator.Hex() != userID {
		// TODO: Add admin permission check here
		config.ErrorStatus("Unauthorized to update this announcement", http.StatusForbidden, w, nil)
		return
	}
	
	// Build update document
	update := bson.M{}
	if req.Title != nil {
		update["title"] = *req.Title
	}
	if req.Content != nil {
		update["content"] = *req.Content
	}
	if req.Priority != nil {
		update["priority"] = *req.Priority
	}
	if req.IsActive != nil {
		update["isActive"] = *req.IsActive
	}
	if req.IsPinned != nil {
		update["isPinned"] = *req.IsPinned
	}
	if req.StartTime != nil {
		update["startTime"] = *req.StartTime
	}
	if req.EndTime != nil {
		update["endTime"] = *req.EndTime
	}
	
	update["updatedAt"] = primitive.NewDateTimeFromTime(time.Now())
	
	// Update announcement
	err = a.ADB.UpdateOne(context.Background(), bson.M{"_id": annID}, bson.M{"$set": update})
	if err != nil {
		config.ErrorStatus("Failed to update announcement", http.StatusInternalServerError, w, err)
		return
	}
	
	// Get updated announcement
	updatedAnnouncement, err := a.ADB.FindOne(context.Background(), bson.M{"_id": annID})
	if err != nil {
		config.ErrorStatus("Failed to fetch updated announcement", http.StatusInternalServerError, w, err)
		return
	}
	
	// Get creator user data
	creatorResult := a.UDB.FindOne(context.Background(), bson.M{"_id": updatedAnnouncement.Creator.Hex()})
	var creator models.User
	if err := creatorResult.Decode(&creator); err != nil {
		config.ErrorStatus("Failed to fetch creator data", http.StatusInternalServerError, w, err)
		return
	}
	
	// Build response
	response := models.AnnouncementResponse{
		ID:        updatedAnnouncement.ID,
		Community: updatedAnnouncement.Community,
		Creator: models.UserSummary{
			ID:             updatedAnnouncement.Creator,
			Username:       creator.Details.Username,
			ProfilePicture: &creator.Details.ProfilePicture,
		},
		Type:      updatedAnnouncement.Type,
		Title:     updatedAnnouncement.Title,
		Content:   updatedAnnouncement.Content,
		Priority:  updatedAnnouncement.Priority,
		IsActive:  updatedAnnouncement.IsActive,
		IsPinned:  updatedAnnouncement.IsPinned,
		StartTime: updatedAnnouncement.StartTime,
		EndTime:   updatedAnnouncement.EndTime,
		ViewCount: updatedAnnouncement.ViewCount,
		CreatedAt: updatedAnnouncement.CreatedAt,
		UpdatedAt: updatedAnnouncement.UpdatedAt,
	}
	
	// Populate reactions and comments (similar to GetAnnouncementHandler)
	// ... (implementation similar to GetAnnouncementHandler)
	
	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"announcement": response,
	})
}

// DeleteAnnouncementHandler deletes an announcement
func (a Announcement) DeleteAnnouncementHandler(w http.ResponseWriter, r *http.Request) {
	announcementID := mux.Vars(r)["announcementId"]
	
	// Get user ID from context
	userID := r.Context().Value("user_id").(string)
	
	// Convert announcement ID to ObjectID
	annID, err := primitive.ObjectIDFromHex(announcementID)
	if err != nil {
		config.ErrorStatus("Invalid announcement ID", http.StatusBadRequest, w, err)
		return
	}
	
	// Get existing announcement
	announcement, err := a.ADB.FindOne(context.Background(), bson.M{"_id": annID})
	if err != nil {
		config.ErrorStatus("Announcement not found", http.StatusNotFound, w, err)
		return
	}
	
	// Check if user is the creator or has admin permissions
	if announcement.Creator.Hex() != userID {
		// TODO: Add admin permission check here
		config.ErrorStatus("Unauthorized to delete this announcement", http.StatusForbidden, w, nil)
		return
	}
	
	// Delete announcement
	err = a.ADB.DeleteOne(context.Background(), bson.M{"_id": annID})
	if err != nil {
		config.ErrorStatus("Failed to delete announcement", http.StatusInternalServerError, w, err)
		return
	}
	
	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Announcement deleted successfully",
	})
}

// AddReactionHandler adds or updates a user reaction
func (a Announcement) AddReactionHandler(w http.ResponseWriter, r *http.Request) {
	announcementID := mux.Vars(r)["announcementId"]
	
	// Get user ID from context
	userID := r.Context().Value("user_id").(string)
	
	// Parse request body
	var req models.AddReactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}
	
	// Convert IDs to ObjectID
	annID, err := primitive.ObjectIDFromHex(announcementID)
	if err != nil {
		config.ErrorStatus("Invalid announcement ID", http.StatusBadRequest, w, err)
		return
	}
	
	userObjID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("Invalid user ID", http.StatusBadRequest, w, err)
		return
	}
	
	// Remove existing reaction from this user
	removeFilter := bson.M{"_id": annID}
	removeUpdate := bson.M{"$pull": bson.M{"reactions": bson.M{"user": userObjID}}}
	
	err = a.ADB.UpdateOne(context.Background(), removeFilter, removeUpdate)
	if err != nil {
		config.ErrorStatus("Failed to update reactions", http.StatusInternalServerError, w, err)
		return
	}
	
	// Add new reaction
	newReaction := models.Reaction{
		User:      userObjID,
		Emoji:     req.Emoji,
		Timestamp: primitive.NewDateTimeFromTime(time.Now()),
	}
	
	addFilter := bson.M{"_id": annID}
	addUpdate := bson.M{"$push": bson.M{"reactions": newReaction}}
	
	err = a.ADB.UpdateOne(context.Background(), addFilter, addUpdate)
	if err != nil {
		config.ErrorStatus("Failed to add reaction", http.StatusInternalServerError, w, err)
		return
	}
	
	// Get updated announcement with reactions
	announcement, err := a.ADB.FindOne(context.Background(), bson.M{"_id": annID})
	if err != nil {
		config.ErrorStatus("Failed to fetch updated announcement", http.StatusInternalServerError, w, err)
		return
	}
	
	// Build reactions response
	var reactions []models.ReactionResponse
	for _, reaction := range announcement.Reactions {
		userResult := a.UDB.FindOne(context.Background(), bson.M{"_id": reaction.User.Hex()})
		var user models.User
		if err := userResult.Decode(&user); err != nil {
			continue
		}
		
		reactions = append(reactions, models.ReactionResponse{
			User: models.UserSummary{
				ID:             reaction.User,
				Username:       user.Details.Username,
				ProfilePicture: &user.Details.ProfilePicture,
			},
			Emoji:     reaction.Emoji,
			Timestamp: reaction.Timestamp,
		})
	}
	
	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"announcement": map[string]interface{}{
			"_id":       announcement.ID,
			"reactions": reactions,
		},
	})
}

// RemoveReactionHandler removes a user reaction
func (a Announcement) RemoveReactionHandler(w http.ResponseWriter, r *http.Request) {
	announcementID := mux.Vars(r)["announcementId"]
	
	// Get user ID from context
	userID := r.Context().Value("user_id").(string)
	
	// Convert IDs to ObjectID
	annID, err := primitive.ObjectIDFromHex(announcementID)
	if err != nil {
		config.ErrorStatus("Invalid announcement ID", http.StatusBadRequest, w, err)
		return
	}
	
	userObjID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("Invalid user ID", http.StatusBadRequest, w, err)
		return
	}
	
	// Remove reaction
	filter := bson.M{"_id": annID}
	update := bson.M{"$pull": bson.M{"reactions": bson.M{"user": userObjID}}}
	
	err = a.ADB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("Failed to remove reaction", http.StatusInternalServerError, w, err)
		return
	}
	
	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Reaction removed successfully",
	})
}

// AddCommentHandler adds a new comment
func (a Announcement) AddCommentHandler(w http.ResponseWriter, r *http.Request) {
	announcementID := mux.Vars(r)["announcementId"]
	
	// Get user ID from context
	userID := r.Context().Value("user_id").(string)
	
	// Parse request body
	var req models.AddCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}
	
	// Convert IDs to ObjectID
	annID, err := primitive.ObjectIDFromHex(announcementID)
	if err != nil {
		config.ErrorStatus("Invalid announcement ID", http.StatusBadRequest, w, err)
		return
	}
	
	userObjID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("Invalid user ID", http.StatusBadRequest, w, err)
		return
	}
	
	// Create new comment
	newComment := models.Comment{
		ID:        primitive.NewObjectID(),
		User:      userObjID,
		Content:   req.Content,
		Timestamp: primitive.NewDateTimeFromTime(time.Now()),
		Edited:    false,
		EditedAt:  nil,
	}
	
	// Add comment to announcement
	filter := bson.M{"_id": annID}
	update := bson.M{"$push": bson.M{"comments": newComment}}
	
	err = a.ADB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("Failed to add comment", http.StatusInternalServerError, w, err)
		return
	}
	
	// Get user data for response
	userResult := a.UDB.FindOne(context.Background(), bson.M{"_id": userID})
	var user models.User
	if err := userResult.Decode(&user); err != nil {
		config.ErrorStatus("Failed to fetch user data", http.StatusInternalServerError, w, err)
		return
	}
	
	// Build comment response
	commentResponse := models.CommentResponse{
		ID: newComment.ID,
		User: models.UserSummary{
			ID:             newComment.User,
			Username:       user.Details.Username,
			ProfilePicture: &user.Details.ProfilePicture,
		},
		Content:   newComment.Content,
		Timestamp: newComment.Timestamp,
		Edited:    newComment.Edited,
		EditedAt:  newComment.EditedAt,
	}
	
	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"comment": commentResponse,
	})
}

// UpdateCommentHandler updates an existing comment
func (a Announcement) UpdateCommentHandler(w http.ResponseWriter, r *http.Request) {
	announcementID := mux.Vars(r)["announcementId"]
	commentID := mux.Vars(r)["commentId"]
	
	// Get user ID from context
	userID := r.Context().Value("user_id").(string)
	
	// Parse request body
	var req models.UpdateCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}
	
	// Convert IDs to ObjectID
	annID, err := primitive.ObjectIDFromHex(announcementID)
	if err != nil {
		config.ErrorStatus("Invalid announcement ID", http.StatusBadRequest, w, err)
		return
	}
	
	commentObjID, err := primitive.ObjectIDFromHex(commentID)
	if err != nil {
		config.ErrorStatus("Invalid comment ID", http.StatusBadRequest, w, err)
		return
	}
	
	// Get announcement to check comment ownership
	announcement, err := a.ADB.FindOne(context.Background(), bson.M{"_id": annID})
	if err != nil {
		config.ErrorStatus("Announcement not found", http.StatusNotFound, w, err)
		return
	}
	
	// Find the comment and check ownership
	var targetComment *models.Comment
	for _, comment := range announcement.Comments {
		if comment.ID == commentObjID {
			targetComment = &comment
			break
		}
	}
	
	if targetComment == nil {
		config.ErrorStatus("Comment not found", http.StatusNotFound, w, nil)
		return
	}
	
	if targetComment.User.Hex() != userID {
		config.ErrorStatus("Unauthorized to edit this comment", http.StatusForbidden, w, nil)
		return
	}
	
	// Update comment
	now := primitive.NewDateTimeFromTime(time.Now())
	filter := bson.M{
		"_id":           annID,
		"comments._id":  commentObjID,
	}
	update := bson.M{
		"$set": bson.M{
			"comments.$.content":   req.Content,
			"comments.$.edited":    true,
			"comments.$.editedAt":  now,
		},
	}
	
	err = a.ADB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("Failed to update comment", http.StatusInternalServerError, w, err)
		return
	}
	
	// Get user data for response
	userResult := a.UDB.FindOne(context.Background(), bson.M{"_id": userID})
	var user models.User
	if err := userResult.Decode(&user); err != nil {
		config.ErrorStatus("Failed to fetch user data", http.StatusInternalServerError, w, err)
		return
	}
	
	// Build comment response
	commentResponse := models.CommentResponse{
		ID: commentObjID,
		User: models.UserSummary{
			ID:             targetComment.User,
			Username:       user.Details.Username,
			ProfilePicture: &user.Details.ProfilePicture,
		},
		Content:   req.Content,
		Timestamp: targetComment.Timestamp,
		Edited:    true,
		EditedAt:  &now,
	}
	
	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"comment": commentResponse,
	})
}

// DeleteCommentHandler deletes a comment
func (a Announcement) DeleteCommentHandler(w http.ResponseWriter, r *http.Request) {
	announcementID := mux.Vars(r)["announcementId"]
	commentID := mux.Vars(r)["commentId"]
	
	// Get user ID from context
	userID := r.Context().Value("user_id").(string)
	
	// Convert IDs to ObjectID
	annID, err := primitive.ObjectIDFromHex(announcementID)
	if err != nil {
		config.ErrorStatus("Invalid announcement ID", http.StatusBadRequest, w, err)
		return
	}
	
	commentObjID, err := primitive.ObjectIDFromHex(commentID)
	if err != nil {
		config.ErrorStatus("Invalid comment ID", http.StatusBadRequest, w, err)
		return
	}
	
	// Get announcement to check comment ownership
	announcement, err := a.ADB.FindOne(context.Background(), bson.M{"_id": annID})
	if err != nil {
		config.ErrorStatus("Announcement not found", http.StatusNotFound, w, err)
		return
	}
	
	// Find the comment and check ownership
	var targetComment *models.Comment
	for _, comment := range announcement.Comments {
		if comment.ID == commentObjID {
			targetComment = &comment
			break
		}
	}
	
	if targetComment == nil {
		config.ErrorStatus("Comment not found", http.StatusNotFound, w, nil)
		return
	}
	
	if targetComment.User.Hex() != userID {
		config.ErrorStatus("Unauthorized to delete this comment", http.StatusForbidden, w, nil)
		return
	}
	
	// Remove comment
	filter := bson.M{"_id": annID}
	update := bson.M{"$pull": bson.M{"comments": bson.M{"_id": commentObjID}}}
	
	err = a.ADB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("Failed to delete comment", http.StatusInternalServerError, w, err)
		return
	}
	
	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Comment deleted successfully",
	})
} 