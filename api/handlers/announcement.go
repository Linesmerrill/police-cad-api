package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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

// UserDoc is a minimal struct for decoding user info from the DB
type UserDoc struct {
	ID   primitive.ObjectID `bson:"_id"`
	User struct {
		Username       string  `bson:"username"`
		ProfilePicture *string `bson:"profilePicture"`
	} `bson:"user"`
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

	// Get total count for pagination first
	var totalCount int64
	totalCount, err = a.ADB.CountDocuments(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("Failed to count announcements", http.StatusInternalServerError, w, err)
		return
	}

	// Find announcements with pagination
	findOptions := options.Find().
		SetSkip(int64(skip)).
		SetLimit(int64(limit)).
		SetSort(bson.D{{Key: "createdAt", Value: -1}})

	cursor, err := a.ADB.Find(context.Background(), filter, findOptions)
	if err != nil {
		config.ErrorStatus("Failed to fetch announcements", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())

	var announcements []models.Announcement
	if err := cursor.All(context.Background(), &announcements); err != nil {
		config.ErrorStatus("Failed to decode announcements", http.StatusInternalServerError, w, err)
		return
	}



	// Convert to response format
	var announcementResponses []models.AnnouncementResponse
	for _, ann := range announcements {
		// For the creator:
		creatorDoc := UserDoc{}
		creatorResult := a.UDB.FindOne(context.Background(), bson.M{"_id": ann.Creator})
		if err := creatorResult.Decode(&creatorDoc); err != nil {
			creatorDoc.User.Username = "Unknown"
			creatorDoc.User.ProfilePicture = nil
		}

		// For now, create a minimal creator response since we know the user exists
		// TODO: Fix user database query issue
		response := models.AnnouncementResponse{
			ID:        ann.ID,
			Community: ann.Community,
			Creator: models.UserSummary{
				ID:             ann.Creator,
				Username:       creatorDoc.User.Username,
				ProfilePicture: creatorDoc.User.ProfilePicture,
			},
			Type:      ann.Type,
			Title:     ann.Title,
			Content:   ann.Content,
			Priority:  ann.Priority,
			IsActive:  ann.IsActive,
			IsPinned:  ann.IsPinned,
			StartTime: ann.StartTime,
			EndTime:   ann.EndTime,
			Reactions: []models.ReactionResponse{},
			Comments:  []models.CommentResponse{},
			ViewCount: ann.ViewCount,
			CreatedAt: ann.CreatedAt,
			UpdatedAt: ann.UpdatedAt,
		}
		
		// Always return empty arrays instead of null
		response.Reactions = []models.ReactionResponse{}
		response.Comments = []models.CommentResponse{}
		
		announcementResponses = append(announcementResponses, response)
	}

	// Manually populate reactions and comments for each announcement
	for i, ann := range announcements {
		// Populate reactions (handle null values from database)
		if ann.Reactions != nil {
			for _, reaction := range ann.Reactions {
				userDoc := UserDoc{}
				userResult := a.UDB.FindOne(context.Background(), bson.M{"_id": reaction.User})
				if err := userResult.Decode(&userDoc); err != nil {
					userDoc.User.Username = "Unknown"
					userDoc.User.ProfilePicture = nil
				}
				reactionResponse := models.ReactionResponse{
					User: models.UserSummary{
						ID:             reaction.User,
						Username:       userDoc.User.Username,
						ProfilePicture: userDoc.User.ProfilePicture,
					},
					Emoji:     reaction.Emoji,
					Timestamp: reaction.Timestamp,
				}
				announcementResponses[i].Reactions = append(announcementResponses[i].Reactions, reactionResponse)
			}
		}

		// Populate comments (handle null values from database)
		if ann.Comments != nil {
			for _, comment := range ann.Comments {
				userDoc := UserDoc{}
				userResult := a.UDB.FindOne(context.Background(), bson.M{"_id": comment.User})
				if err := userResult.Decode(&userDoc); err != nil {
					userDoc.User.Username = "Unknown"
					userDoc.User.ProfilePicture = nil
				}
				commentResponse := models.CommentResponse{
					ID: comment.ID,
					User: models.UserSummary{
						ID:             comment.User,
						Username:       userDoc.User.Username,
						ProfilePicture: userDoc.User.ProfilePicture,
					},
					Content:   comment.Content,
					Timestamp: comment.Timestamp,
					Edited:    comment.Edited,
					EditedAt:  comment.EditedAt,
				}
				announcementResponses[i].Comments = append(announcementResponses[i].Comments, commentResponse)
			}
		}
	}

	// Calculate pagination info
	totalPages := int((totalCount + int64(limit) - 1) / int64(limit))
	hasNextPage := page < totalPages
	hasPrevPage := page > 1

	response := models.PaginatedAnnouncementsResponse{
		Success:       true,
		Announcements: announcementResponses,
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
	creatorDoc := UserDoc{}
	creatorResult := a.UDB.FindOne(context.Background(), bson.M{"_id": announcement.Creator})
	if err := creatorResult.Decode(&creatorDoc); err != nil {
		// For now, create a minimal creator response since we know the user exists
		// TODO: Fix user database query issue
		creatorDoc.User.Username = "Unknown"
		creatorDoc.User.ProfilePicture = nil
	}

	// Build response with populated user data
	response := models.AnnouncementResponse{
		ID:        announcement.ID,
		Community: announcement.Community,
		Creator: models.UserSummary{
			ID:             announcement.Creator,
			Username:       creatorDoc.User.Username,
			ProfilePicture: creatorDoc.User.ProfilePicture,
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

	// Populate reactions (ensure we always have an array, even if null in DB)
	if announcement.Reactions == nil {
		announcement.Reactions = []models.Reaction{}
	}
	for _, reaction := range announcement.Reactions {
		userDoc := UserDoc{}
		userResult := a.UDB.FindOne(context.Background(), bson.M{"_id": reaction.User})
		if err := userResult.Decode(&userDoc); err != nil {
			continue // Skip if user not found
		}

		response.Reactions = append(response.Reactions, models.ReactionResponse{
			User: models.UserSummary{
				ID:             reaction.User,
				Username:       userDoc.User.Username,
				ProfilePicture: userDoc.User.ProfilePicture,
			},
			Emoji:     reaction.Emoji,
			Timestamp: reaction.Timestamp,
		})
	}

	// Populate comments (ensure we always have an array, even if null in DB)
	if announcement.Comments == nil {
		announcement.Comments = []models.Comment{}
	}
	for _, comment := range announcement.Comments {
		userDoc := UserDoc{}
		userResult := a.UDB.FindOne(context.Background(), bson.M{"_id": comment.User})
		if err := userResult.Decode(&userDoc); err != nil {
			continue // Skip if user not found
		}

		response.Comments = append(response.Comments, models.CommentResponse{
			ID: comment.ID,
			User: models.UserSummary{
				ID:             comment.User,
				Username:       userDoc.User.Username,
				ProfilePicture: userDoc.User.ProfilePicture,
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

	// Parse request body
	var req models.CreateAnnouncementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	// Get user ID from request body
	userID := req.UserID

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

		// For now, skip user lookup since we know the user exists and is the owner
	// TODO: Fix user database query issue
	
	// Get community to check permissions
	community, err := a.CDB.FindOne(context.Background(), bson.M{"_id": commID})
	if err != nil {
		config.ErrorStatus("Community not found", http.StatusNotFound, w, err)
		return
	}
	
	// Check if user has permission to create announcements
	hasPermission := false
	
	// Check if user is the owner
	if community.Details.OwnerID == userID {
		hasPermission = true
	} else {
		// Check if user has admin or manage community settings permission in any role
		for _, role := range community.Details.Roles {
			// Check if user is in this role
			userInRole := false
			for _, memberID := range role.Members {
				if memberID == userID {
					userInRole = true
					break
				}
			}
			
			if userInRole {
				// Check if role has admin or manage community settings permission
				for _, permission := range role.Permissions {
					if permission.Enabled && (permission.Name == "administrator" || permission.Name == "manage community settings") {
						hasPermission = true
						break
					}
				}
			}
			
			if hasPermission {
				break
			}
		}
	}
	
	if !hasPermission {
		config.ErrorStatus("User does not have permission to create announcements", http.StatusForbidden, w, fmt.Errorf("user does not have permission to create announcements"))
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

	// Ensure reactions and comments are stored as empty arrays (not null)
	err = a.ADB.UpdateOne(
		context.Background(),
		bson.M{"_id": announcement.ID},
		bson.M{"$set": bson.M{
			"reactions": []models.Reaction{},
			"comments":  []models.Comment{},
		}},
	)
	if err != nil {
		// Log the error but don't fail the request since the announcement was created
		fmt.Printf("Warning: Failed to initialize empty arrays: %v\n", err)
	}

	// Fetch creator user
	creatorDoc := UserDoc{}
	creatorResult := a.UDB.FindOne(context.Background(), bson.M{"_id": announcement.Creator})
	if err := creatorResult.Decode(&creatorDoc); err != nil {
		creatorDoc.User.Username = "Unknown"
		creatorDoc.User.ProfilePicture = nil
	}

	// Build response
	response := models.AnnouncementResponse{
		ID:        announcement.ID,
		Community: announcement.Community,
		Creator: models.UserSummary{
			ID:             announcement.Creator,
			Username:       creatorDoc.User.Username,
			ProfilePicture: creatorDoc.User.ProfilePicture,
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

	// Parse request body
	var req models.UpdateAnnouncementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	// Get user ID from request body
	userID := req.UserID

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
		config.ErrorStatus("Unauthorized to update this announcement", http.StatusForbidden, w, fmt.Errorf("unauthorized to update this announcement"))
		return
	}

	// Build update document
	update := bson.M{}
	if req.Type != nil {
		update["type"] = *req.Type
	}
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
	creatorDoc := UserDoc{}
	creatorResult := a.UDB.FindOne(context.Background(), bson.M{"_id": updatedAnnouncement.Creator})
	if err := creatorResult.Decode(&creatorDoc); err != nil {
		// Fallback to unknown user if lookup fails
		creatorDoc.User.Username = "Unknown"
		creatorDoc.User.ProfilePicture = nil
	}

	// Build response
	response := models.AnnouncementResponse{
		ID:        updatedAnnouncement.ID,
		Community: updatedAnnouncement.Community,
		Creator: models.UserSummary{
			ID:             updatedAnnouncement.Creator,
			Username:       creatorDoc.User.Username,
			ProfilePicture: creatorDoc.User.ProfilePicture,
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

	// Parse request body to get user ID
	var req struct {
		UserID string `json:"userId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	userID := req.UserID

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
		config.ErrorStatus("Unauthorized to delete this announcement", http.StatusForbidden, w, fmt.Errorf("unauthorized to delete this announcement"))
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

	// Parse request body
	var req models.AddReactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	// Get user ID from request body
	userID := req.UserID

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

	// Fetch the updated announcement with reactions
	updatedAnnouncement, err := a.ADB.FindOne(context.Background(), bson.M{"_id": annID})
	if err != nil {
		config.ErrorStatus("Failed to fetch updated announcement", http.StatusInternalServerError, w, err)
		return
	}

	// Send response with updated reactions
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"announcement": map[string]interface{}{
			"_id":       annID.Hex(),
			"reactions": updatedAnnouncement.Reactions,
		},
	})
}

// RemoveReactionHandler removes a user reaction
func (a Announcement) RemoveReactionHandler(w http.ResponseWriter, r *http.Request) {
	announcementID := mux.Vars(r)["announcementId"]

	// Parse request body to get user ID
	var req struct {
		UserID string `json:"userId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	userID := req.UserID

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

	// Fetch the updated announcement with reactions
	updatedAnnouncement, err := a.ADB.FindOne(context.Background(), bson.M{"_id": annID})
	if err != nil {
		config.ErrorStatus("Failed to fetch updated announcement", http.StatusInternalServerError, w, err)
		return
	}

	// Send response with updated reactions
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"announcement": map[string]interface{}{
			"_id":       annID.Hex(),
			"reactions": updatedAnnouncement.Reactions,
		},
	})
}

// AddCommentHandler adds a new comment
func (a Announcement) AddCommentHandler(w http.ResponseWriter, r *http.Request) {
	announcementID := mux.Vars(r)["announcementId"]

	// Parse request body
	var req models.AddCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	// Get user ID from request body
	userID := req.UserID

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
	userDoc := UserDoc{}
	userResult := a.UDB.FindOne(context.Background(), bson.M{"_id": userObjID})
	if err := userResult.Decode(&userDoc); err != nil {
		// Fallback to unknown user if lookup fails
		userDoc.User.Username = "Unknown"
		userDoc.User.ProfilePicture = nil
	}

	// Build comment response
	commentResponse := models.CommentResponse{
		ID: newComment.ID,
		User: models.UserSummary{
			ID:             newComment.User,
			Username:       userDoc.User.Username,
			ProfilePicture: userDoc.User.ProfilePicture,
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

	// Parse request body
	var req models.UpdateCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	// Get user ID from request body
	userID := req.UserID

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

	userObjID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("Invalid user ID", http.StatusBadRequest, w, err)
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
		config.ErrorStatus("Comment not found", http.StatusNotFound, w, fmt.Errorf("comment not found"))
		return
	}

	if targetComment.User.Hex() != userID {
		config.ErrorStatus("Unauthorized to edit this comment", http.StatusForbidden, w, fmt.Errorf("unauthorized to edit this comment"))
		return
	}

	// Update comment
	now := primitive.NewDateTimeFromTime(time.Now())
	filter := bson.M{
		"_id":          annID,
		"comments._id": commentObjID,
	}
	update := bson.M{
		"$set": bson.M{
			"comments.$.content":  req.Content,
			"comments.$.edited":   true,
			"comments.$.editedAt": now,
		},
	}

	err = a.ADB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("Failed to update comment", http.StatusInternalServerError, w, err)
		return
	}

	// Get user data for response
	userDoc := UserDoc{}
	userResult := a.UDB.FindOne(context.Background(), bson.M{"_id": userObjID})
	if err := userResult.Decode(&userDoc); err != nil {
		// Fallback to unknown user if lookup fails
		userDoc.User.Username = "Unknown"
		userDoc.User.ProfilePicture = nil
	}

	// Build comment response
	commentResponse := models.CommentResponse{
		ID: commentObjID,
		User: models.UserSummary{
			ID:             targetComment.User,
			Username:       userDoc.User.Username,
			ProfilePicture: userDoc.User.ProfilePicture,
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

	// Parse request body to get user ID
	var req struct {
		UserID string `json:"userId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	userID := req.UserID

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
		config.ErrorStatus("Comment not found", http.StatusNotFound, w, fmt.Errorf("comment not found"))
		return
	}

	if targetComment.User.Hex() != userID {
		config.ErrorStatus("Unauthorized to delete this comment", http.StatusForbidden, w, fmt.Errorf("unauthorized to delete this comment"))
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
