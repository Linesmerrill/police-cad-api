package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

// ContentCreator handler struct for content creator endpoints
type ContentCreator struct {
	AppDB  databases.ContentCreatorApplicationDatabase
	CCDB   databases.ContentCreatorDatabase
	EntDB  databases.ContentCreatorEntitlementDatabase
	SnapDB databases.ContentCreatorSnapshotDatabase
	UDB    databases.UserDatabase
	CDB    databases.CommunityDatabase
}

// getUserIDFromRequest extracts the user ID from the X-User-ID header (set by Express proxy)
// or from context (for future direct API access with auth middleware)
func getUserIDFromRequest(r *http.Request) (string, bool) {
	// First check X-User-ID header (set by Express server when proxying)
	if userID := r.Header.Get("X-User-ID"); userID != "" {
		return userID, true
	}
	// Fallback to context value (for direct API access with auth middleware)
	if userID := r.Context().Value("userID"); userID != nil {
		if str, ok := userID.(string); ok {
			return str, true
		}
	}
	return "", false
}

// --- Public Endpoints ---

// GetContentCreatorsHandler returns a list of active content creators (public)
// GET /api/v1/content-creators
func (cc ContentCreator) GetContentCreatorsHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Parse pagination params
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 50 {
		limit = 20
	}

	// Filter for active creators only
	filter := bson.M{
		"status": "active",
	}

	// Check for featured filter
	if r.URL.Query().Get("featured") == "true" {
		filter["featured"] = true
	}

	// Get total count
	totalCount, err := cc.CCDB.CountDocuments(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to count content creators", http.StatusInternalServerError, w, err)
		return
	}

	// Build find options
	skip := int64((page - 1) * limit)
	findOptions := options.Find().
		SetSkip(skip).
		SetLimit(int64(limit)).
		SetSort(bson.D{
			{Key: "featured", Value: -1},
			{Key: "joinedAt", Value: -1},
		})

	cursor, err := cc.CCDB.Find(ctx, filter, findOptions)
	if err != nil {
		config.ErrorStatus("failed to fetch content creators", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	var creators []models.ContentCreator
	if err := cursor.All(ctx, &creators); err != nil {
		config.ErrorStatus("failed to decode content creators", http.StatusInternalServerError, w, err)
		return
	}

	// Convert to public response format
	publicCreators := make([]models.ContentCreatorPublicResponse, len(creators))
	for i, c := range creators {
		publicCreators[i] = models.ContentCreatorPublicResponse{
			ID:              c.ID,
			DisplayName:     c.DisplayName,
			Slug:            c.Slug,
			ProfileImage:    c.ProfileImage,
			Bio:             c.Bio,
			PrimaryPlatform: c.PrimaryPlatform,
			Platforms:       c.Platforms,
			Featured:        c.Featured,
			JoinedAt:        c.JoinedAt,
		}
	}

	totalPages := int((totalCount + int64(limit) - 1) / int64(limit))

	response := models.ContentCreatorsListResponse{
		Success:  true,
		Creators: publicCreators,
		Pagination: models.ContentCreatorPagination{
			CurrentPage: page,
			TotalPages:  totalPages,
			TotalItems:  int(totalCount),
			HasNextPage: page < totalPages,
			HasPrevPage: page > 1,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// GetContentCreatorBySlugHandler returns a single content creator by slug (public)
// GET /api/v1/content-creators/{slug}
func (cc ContentCreator) GetContentCreatorBySlugHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	slug := mux.Vars(r)["slug"]
	if slug == "" {
		config.ErrorStatus("slug is required", http.StatusBadRequest, w, nil)
		return
	}

	filter := bson.M{
		"slug":   slug,
		"status": "active",
	}

	creator, err := cc.CCDB.FindOne(ctx, filter)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			config.ErrorStatus("content creator not found", http.StatusNotFound, w, err)
			return
		}
		config.ErrorStatus("failed to fetch content creator", http.StatusInternalServerError, w, err)
		return
	}

	publicCreator := models.ContentCreatorPublicResponse{
		ID:              creator.ID,
		DisplayName:     creator.DisplayName,
		Slug:            creator.Slug,
		ProfileImage:    creator.ProfileImage,
		Bio:             creator.Bio,
		PrimaryPlatform: creator.PrimaryPlatform,
		Platforms:       creator.Platforms,
		Featured:        creator.Featured,
		JoinedAt:        creator.JoinedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(publicCreator)
}

// --- Authenticated User Endpoints ---

// CreateApplicationHandler submits a new creator application
// POST /api/v1/content-creator-applications
func (cc ContentCreator) CreateApplicationHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Get user ID from header or context
	userIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}

	userObjID, err := primitive.ObjectIDFromHex(userIDStr)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	// Check if user already has a pending/approved application
	existingFilter := bson.M{
		"userId": userObjID,
		"status": bson.M{"$in": []string{"submitted", "under_review", "approved"}},
	}
	existingApp, _ := cc.AppDB.FindOne(ctx, existingFilter)
	if existingApp != nil {
		config.ErrorStatus("you already have an active application", http.StatusConflict, w, nil)
		return
	}

	// Check if user is already a creator
	creatorFilter := bson.M{
		"userId": userObjID,
		"status": bson.M{"$ne": "removed"},
	}
	existingCreator, _ := cc.CCDB.FindOne(ctx, creatorFilter)
	if existingCreator != nil {
		config.ErrorStatus("you are already a content creator", http.StatusConflict, w, nil)
		return
	}

	// Parse request body
	var req models.CreateContentCreatorApplicationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}

	// Validate minimum follower requirement
	maxFollowers := 0
	for _, p := range req.Platforms {
		if p.FollowerCount > maxFollowers {
			maxFollowers = p.FollowerCount
		}
	}
	if maxFollowers < 500 {
		config.ErrorStatus("minimum 500 followers required on at least one platform", http.StatusBadRequest, w, nil)
		return
	}

	// Validate description length
	if len(req.Description) < 50 {
		config.ErrorStatus("description must be at least 50 characters", http.StatusBadRequest, w, nil)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	application := models.ContentCreatorApplication{
		ID:              primitive.NewObjectID(),
		UserID:          userObjID,
		DisplayName:     req.DisplayName,
		PrimaryPlatform: req.PrimaryPlatform,
		Platforms:       req.Platforms,
		Description:     req.Description,
		Status:          "submitted",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	_, err = cc.AppDB.InsertOne(ctx, application)
	if err != nil {
		config.ErrorStatus("failed to create application", http.StatusInternalServerError, w, err)
		return
	}

	zap.S().Infow("content creator application submitted",
		"applicationId", application.ID.Hex(),
		"userId", userObjID.Hex(),
		"displayName", req.DisplayName,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"message":     "Application submitted successfully",
		"application": application,
	})
}

// GetMyApplicationHandler returns the current user's application status
// GET /api/v1/content-creator-applications/me
func (cc ContentCreator) GetMyApplicationHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	userIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	userObjID, _ := primitive.ObjectIDFromHex(userIDStr)

	// Check for existing creator profile first
	creatorFilter := bson.M{
		"userId": userObjID,
		"status": bson.M{"$ne": "removed"},
	}
	creator, _ := cc.CCDB.FindOne(ctx, creatorFilter)

	if creator != nil {
		// User is an active creator, return their profile with entitlements
		entitlements := cc.getCreatorEntitlements(ctx, creator.ID)

		creatorResponse := models.ContentCreatorPrivateResponse{
			ID:              creator.ID,
			DisplayName:     creator.DisplayName,
			Slug:            creator.Slug,
			ProfileImage:    creator.ProfileImage,
			Bio:             creator.Bio,
			PrimaryPlatform: creator.PrimaryPlatform,
			Platforms:       creator.Platforms,
			Status:          creator.Status,
			Featured:        creator.Featured,
			WarnedAt:        creator.WarnedAt,
			WarningMessage:  creator.WarningMessage,
			JoinedAt:        creator.JoinedAt,
			Entitlements:    entitlements,
		}

		response := models.ContentCreatorMeResponse{
			Success: true,
			Creator: &creatorResponse,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Check for application
	appFilter := bson.M{"userId": userObjID}
	findOptions := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(1)
	cursor, err := cc.AppDB.Find(ctx, appFilter, findOptions)
	if err != nil {
		config.ErrorStatus("failed to fetch application", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	var applications []models.ContentCreatorApplication
	if err := cursor.All(ctx, &applications); err != nil {
		config.ErrorStatus("failed to decode application", http.StatusInternalServerError, w, err)
		return
	}

	if len(applications) == 0 {
		// No application found
		response := models.ContentCreatorMeResponse{
			Success: true,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
		return
	}

	app := applications[0]
	appResponse := models.ContentCreatorApplicationResponse{
		ID:              app.ID,
		DisplayName:     app.DisplayName,
		PrimaryPlatform: app.PrimaryPlatform,
		Platforms:       app.Platforms,
		Description:     app.Description,
		Status:          app.Status,
		Feedback:        app.Feedback,
		CreatedAt:       app.CreatedAt,
		ReviewedAt:      app.ReviewedAt,
	}

	response := models.ContentCreatorMeResponse{
		Success:     true,
		Application: &appResponse,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// WithdrawApplicationHandler allows a user to withdraw their pending application
// DELETE /api/v1/content-creator-applications/me
func (cc ContentCreator) WithdrawApplicationHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	userIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	userObjID, _ := primitive.ObjectIDFromHex(userIDStr)

	// Find the user's pending or under_review application
	filter := bson.M{
		"userId": userObjID,
		"status": bson.M{"$in": []string{"submitted", "under_review"}},
	}

	application, err := cc.AppDB.FindOne(ctx, filter)
	if err != nil || application == nil {
		config.ErrorStatus("no pending application found to withdraw", http.StatusNotFound, w, err)
		return
	}

	// Update application status to withdrawn
	now := primitive.NewDateTimeFromTime(time.Now())
	update := bson.M{
		"$set": bson.M{
			"status":      "withdrawn",
			"withdrawnAt": now,
			"updatedAt":   now,
		},
	}

	err = cc.AppDB.UpdateOne(ctx, bson.M{"_id": application.ID}, update)
	if err != nil {
		config.ErrorStatus("failed to withdraw application", http.StatusInternalServerError, w, err)
		return
	}

	zap.S().Infow("content creator application withdrawn",
		"applicationId", application.ID.Hex(),
		"userId", userObjID.Hex(),
		"displayName", application.DisplayName,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Application withdrawn successfully",
	})
}

// RequestRemovalHandler allows a creator to request voluntary removal
// POST /api/v1/content-creators/me/removal-request
func (cc ContentCreator) RequestRemovalHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	userIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	userObjID, _ := primitive.ObjectIDFromHex(userIDStr)

	// Find the creator
	filter := bson.M{
		"userId": userObjID,
		"status": bson.M{"$in": []string{"active", "warned"}},
	}

	creator, err := cc.CCDB.FindOne(ctx, filter)
	if err != nil {
		config.ErrorStatus("creator profile not found", http.StatusNotFound, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	// Update creator status
	update := bson.M{
		"$set": bson.M{
			"status":        "removed",
			"removalReason": "voluntary",
			"removedAt":     now,
			"updatedAt":     now,
		},
	}

	err = cc.CCDB.UpdateOne(ctx, bson.M{"_id": creator.ID}, update)
	if err != nil {
		config.ErrorStatus("failed to process removal", http.StatusInternalServerError, w, err)
		return
	}

	// Revoke all entitlements
	entitlementUpdate := bson.M{
		"$set": bson.M{
			"active":       false,
			"revokedAt":    now,
			"revokeReason": "creator_voluntary_removal",
			"updatedAt":    now,
		},
	}
	cc.EntDB.UpdateMany(ctx, bson.M{"contentCreatorId": creator.ID, "active": true}, entitlementUpdate)

	zap.S().Infow("content creator requested removal",
		"creatorId", creator.ID.Hex(),
		"userId", userObjID.Hex(),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Your creator profile has been removed",
	})
}

// UpdateMyProfileHandler allows a creator to update their profile
// PUT /api/v1/content-creators/me
func (cc ContentCreator) UpdateMyProfileHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	userIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	userObjID, _ := primitive.ObjectIDFromHex(userIDStr)

	// Find the creator
	filter := bson.M{
		"userId": userObjID,
		"status": bson.M{"$in": []string{"active", "warned"}},
	}

	creator, err := cc.CCDB.FindOne(ctx, filter)
	if err != nil {
		config.ErrorStatus("creator profile not found", http.StatusNotFound, w, err)
		return
	}

	var req models.UpdateContentCreatorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	// Build update with only provided fields
	updateFields := bson.M{
		"updatedAt": now,
	}

	if req.DisplayName != "" {
		updateFields["displayName"] = req.DisplayName
	}
	if req.Bio != "" {
		updateFields["bio"] = req.Bio
	}
	if req.ProfileImage != "" {
		updateFields["profileImage"] = req.ProfileImage
	}
	if req.Platforms != nil && len(req.Platforms) > 0 {
		updateFields["platforms"] = req.Platforms
	}

	update := bson.M{"$set": updateFields}

	err = cc.CCDB.UpdateOne(ctx, bson.M{"_id": creator.ID}, update)
	if err != nil {
		config.ErrorStatus("failed to update profile", http.StatusInternalServerError, w, err)
		return
	}

	zap.S().Infow("content creator updated profile",
		"creatorId", creator.ID.Hex(),
		"userId", userObjID.Hex(),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Profile updated successfully",
	})
}

// --- Admin Endpoints ---

// AdminGetApplicationsHandler returns all applications for admin review
// GET /api/v1/admin/content-creator-applications
func (cc ContentCreator) AdminGetApplicationsHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Parse filters
	status := r.URL.Query().Get("status")
	excludeStatus := r.URL.Query().Get("exclude_status")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	filter := bson.M{}
	if status != "" {
		filter["status"] = status
	} else if excludeStatus != "" {
		filter["status"] = bson.M{"$ne": excludeStatus}
	}

	totalCount, err := cc.AppDB.CountDocuments(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to count applications", http.StatusInternalServerError, w, err)
		return
	}

	skip := int64((page - 1) * limit)
	findOptions := options.Find().
		SetSkip(skip).
		SetLimit(int64(limit)).
		SetSort(bson.D{{Key: "createdAt", Value: -1}})

	cursor, err := cc.AppDB.Find(ctx, filter, findOptions)
	if err != nil {
		config.ErrorStatus("failed to fetch applications", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	var applications []models.ContentCreatorApplication
	if err := cursor.All(ctx, &applications); err != nil {
		config.ErrorStatus("failed to decode applications", http.StatusInternalServerError, w, err)
		return
	}

	totalPages := int((totalCount + int64(limit) - 1) / int64(limit))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"applications": applications,
		"pagination": models.ContentCreatorPagination{
			CurrentPage: page,
			TotalPages:  totalPages,
			TotalItems:  int(totalCount),
			HasNextPage: page < totalPages,
			HasPrevPage: page > 1,
		},
	})
}

// AdminGetApplicationHandler returns a single application by ID
// GET /api/v1/admin/content-creator-applications/{id}
func (cc ContentCreator) AdminGetApplicationHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	appID := mux.Vars(r)["id"]
	appObjID, err := primitive.ObjectIDFromHex(appID)
	if err != nil {
		config.ErrorStatus("invalid application ID", http.StatusBadRequest, w, err)
		return
	}

	application, err := cc.AppDB.FindOne(ctx, bson.M{"_id": appObjID})
	if err != nil {
		config.ErrorStatus("application not found", http.StatusNotFound, w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(application)
}

// AdminApproveApplicationHandler approves an application (requires 2 approvers)
// POST /api/v1/admin/content-creator-applications/{id}/approve
func (cc ContentCreator) AdminApproveApplicationHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	adminIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	adminObjID, _ := primitive.ObjectIDFromHex(adminIDStr)

	appID := mux.Vars(r)["id"]
	appObjID, err := primitive.ObjectIDFromHex(appID)
	if err != nil {
		config.ErrorStatus("invalid application ID", http.StatusBadRequest, w, err)
		return
	}

	// Find the application
	application, err := cc.AppDB.FindOne(ctx, bson.M{"_id": appObjID})
	if err != nil {
		config.ErrorStatus("application not found", http.StatusNotFound, w, err)
		return
	}

	if application.Status != "submitted" && application.Status != "under_review" {
		config.ErrorStatus("application already processed", http.StatusConflict, w, nil)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	// Check if this is first or second approval
	if application.FirstApprovalBy == nil {
		// First approval - set to under_review and record first approver
		appUpdate := bson.M{
			"$set": bson.M{
				"status":          "under_review",
				"firstApprovalBy": adminObjID,
				"firstApprovalAt": now,
				"updatedAt":       now,
			},
		}
		err = cc.AppDB.UpdateOne(ctx, bson.M{"_id": appObjID}, appUpdate)
		if err != nil {
			config.ErrorStatus("failed to update application", http.StatusInternalServerError, w, err)
			return
		}

		// Get admin username for response
		var adminUserDoc struct {
			Details struct {
				Username string `bson:"username"`
			} `bson:"user"`
		}
		adminName := "Unknown"
		if err := cc.UDB.FindOne(ctx, bson.M{"_id": adminObjID}).Decode(&adminUserDoc); err == nil {
			adminName = adminUserDoc.Details.Username
		}

		zap.S().Infow("content creator application first approval",
			"applicationId", appObjID.Hex(),
			"firstApprovalBy", adminObjID.Hex(),
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":       true,
			"message":       "First approval recorded. Awaiting second approval from another admin.",
			"needsSecond":   true,
			"firstApprover": adminName,
		})
		return
	}

	// Second approval - check it's a different admin
	if application.FirstApprovalBy.Hex() == adminObjID.Hex() {
		config.ErrorStatus("you already approved this application - a different admin must provide the second approval", http.StatusConflict, w, nil)
		return
	}

	// Update application status to approved
	appUpdate := bson.M{
		"$set": bson.M{
			"status":     "approved",
			"reviewedBy": adminObjID,
			"reviewedAt": now,
			"updatedAt":  now,
		},
	}
	err = cc.AppDB.UpdateOne(ctx, bson.M{"_id": appObjID}, appUpdate)
	if err != nil {
		config.ErrorStatus("failed to update application", http.StatusInternalServerError, w, err)
		return
	}

	// Create the creator profile
	slug := generateSlug(application.DisplayName)

	// Ensure slug is unique
	existingSlug, _ := cc.CCDB.FindOne(ctx, bson.M{"slug": slug})
	if existingSlug != nil {
		slug = slug + "-" + primitive.NewObjectID().Hex()[:6]
	}

	creator := models.ContentCreator{
		ID:              primitive.NewObjectID(),
		UserID:          &application.UserID,
		ApplicationID:   application.ID,
		DisplayName:     application.DisplayName,
		Slug:            slug,
		Bio:             application.Description,
		PrimaryPlatform: application.PrimaryPlatform,
		Platforms:       application.Platforms,
		Status:          "active",
		Featured:        false,
		JoinedAt:        now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	_, err = cc.CCDB.InsertOne(ctx, creator)
	if err != nil {
		config.ErrorStatus("failed to create creator profile", http.StatusInternalServerError, w, err)
		return
	}

	// Grant personal entitlement automatically
	personalEntitlement := models.ContentCreatorEntitlement{
		ID:               primitive.NewObjectID(),
		ContentCreatorID: creator.ID,
		TargetType:       "user",
		TargetID:         application.UserID,
		Plan:             "base",
		Source:           "content_creator_program",
		Active:           true,
		GrantedAt:        now,
		GrantedBy:        adminObjID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	cc.EntDB.InsertOne(ctx, personalEntitlement)

	zap.S().Infow("content creator application approved",
		"applicationId", appObjID.Hex(),
		"creatorId", creator.ID.Hex(),
		"firstApprovalBy", application.FirstApprovalBy.Hex(),
		"secondApprovalBy", adminObjID.Hex(),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Application approved",
		"creator": creator,
	})
}

// AdminRejectApplicationHandler rejects an application
// POST /api/v1/admin/content-creator-applications/{id}/reject
func (cc ContentCreator) AdminRejectApplicationHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	adminIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	adminObjID, _ := primitive.ObjectIDFromHex(adminIDStr)

	appID := mux.Vars(r)["id"]
	appObjID, err := primitive.ObjectIDFromHex(appID)
	if err != nil {
		config.ErrorStatus("invalid application ID", http.StatusBadRequest, w, err)
		return
	}

	var req models.ReviewApplicationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}

	// Rejection reason is required
	if strings.TrimSpace(req.RejectionReason) == "" {
		config.ErrorStatus("rejection reason is required", http.StatusBadRequest, w, nil)
		return
	}

	application, err := cc.AppDB.FindOne(ctx, bson.M{"_id": appObjID})
	if err != nil {
		config.ErrorStatus("application not found", http.StatusNotFound, w, err)
		return
	}

	if application.Status != "submitted" && application.Status != "under_review" {
		config.ErrorStatus("application already processed", http.StatusConflict, w, nil)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	update := bson.M{
		"$set": bson.M{
			"status":          "rejected",
			"rejectionReason": req.RejectionReason,
			"feedback":        req.Feedback,
			"adminNotes":      req.AdminNotes,
			"reviewedBy":      adminObjID,
			"reviewedAt":      now,
			"updatedAt":       now,
		},
	}

	err = cc.AppDB.UpdateOne(ctx, bson.M{"_id": appObjID}, update)
	if err != nil {
		config.ErrorStatus("failed to reject application", http.StatusInternalServerError, w, err)
		return
	}

	zap.S().Infow("content creator application rejected",
		"applicationId", appObjID.Hex(),
		"rejectedBy", adminObjID.Hex(),
		"reason", req.RejectionReason,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Application rejected",
	})
}

// AdminGetCreatorsHandler returns all creators for admin
// GET /api/v1/admin/content-creators
func (cc ContentCreator) AdminGetCreatorsHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	status := r.URL.Query().Get("status")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	filter := bson.M{}
	if status != "" {
		filter["status"] = status
	}

	totalCount, err := cc.CCDB.CountDocuments(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to count creators", http.StatusInternalServerError, w, err)
		return
	}

	skip := int64((page - 1) * limit)
	findOptions := options.Find().
		SetSkip(skip).
		SetLimit(int64(limit)).
		SetSort(bson.D{{Key: "createdAt", Value: -1}})

	cursor, err := cc.CCDB.Find(ctx, filter, findOptions)
	if err != nil {
		config.ErrorStatus("failed to fetch creators", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	var creators []models.ContentCreator
	if err := cursor.All(ctx, &creators); err != nil {
		config.ErrorStatus("failed to decode creators", http.StatusInternalServerError, w, err)
		return
	}

	totalPages := int((totalCount + int64(limit) - 1) / int64(limit))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"creators": creators,
		"pagination": models.ContentCreatorPagination{
			CurrentPage: page,
			TotalPages:  totalPages,
			TotalItems:  int(totalCount),
			HasNextPage: page < totalPages,
			HasPrevPage: page > 1,
		},
	})
}

// AdminUpdateCreatorHandler updates a creator profile
// PATCH /api/v1/admin/content-creators/{id}
func (cc ContentCreator) AdminUpdateCreatorHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	creatorID := mux.Vars(r)["id"]
	creatorObjID, err := primitive.ObjectIDFromHex(creatorID)
	if err != nil {
		config.ErrorStatus("invalid creator ID", http.StatusBadRequest, w, err)
		return
	}

	var req models.UpdateContentCreatorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}

	updateFields := bson.M{
		"updatedAt": primitive.NewDateTimeFromTime(time.Now()),
	}

	if req.DisplayName != "" {
		updateFields["displayName"] = req.DisplayName
	}
	if req.Bio != "" {
		updateFields["bio"] = req.Bio
	}
	if req.ProfileImage != "" {
		updateFields["profileImage"] = req.ProfileImage
	}
	if req.Platforms != nil {
		updateFields["platforms"] = req.Platforms
	}
	if req.Featured != nil {
		updateFields["featured"] = *req.Featured
	}

	update := bson.M{"$set": updateFields}

	err = cc.CCDB.UpdateOne(ctx, bson.M{"_id": creatorObjID}, update)
	if err != nil {
		config.ErrorStatus("failed to update creator", http.StatusInternalServerError, w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Creator updated",
	})
}

// AdminWarnCreatorHandler issues a warning to a creator
// POST /api/v1/admin/content-creators/{id}/warn
func (cc ContentCreator) AdminWarnCreatorHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	creatorID := mux.Vars(r)["id"]
	creatorObjID, err := primitive.ObjectIDFromHex(creatorID)
	if err != nil {
		config.ErrorStatus("invalid creator ID", http.StatusBadRequest, w, err)
		return
	}

	var req models.WarnCreatorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	update := bson.M{
		"$set": bson.M{
			"status":         "warned",
			"warnedAt":       now,
			"warningReason":  req.Reason,
			"warningMessage": req.Message,
			"updatedAt":      now,
		},
	}

	err = cc.CCDB.UpdateOne(ctx, bson.M{"_id": creatorObjID}, update)
	if err != nil {
		config.ErrorStatus("failed to warn creator", http.StatusInternalServerError, w, err)
		return
	}

	zap.S().Infow("content creator warned",
		"creatorId", creatorObjID.Hex(),
		"reason", req.Reason,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Warning issued",
	})
}

// AdminRemoveCreatorHandler removes a creator from the program
// POST /api/v1/admin/content-creators/{id}/remove
func (cc ContentCreator) AdminRemoveCreatorHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	adminIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	adminObjID, _ := primitive.ObjectIDFromHex(adminIDStr)

	creatorID := mux.Vars(r)["id"]
	creatorObjID, err := primitive.ObjectIDFromHex(creatorID)
	if err != nil {
		config.ErrorStatus("invalid creator ID", http.StatusBadRequest, w, err)
		return
	}

	var req models.RemoveCreatorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	// Update creator status
	update := bson.M{
		"$set": bson.M{
			"status":        "removed",
			"removalReason": req.Reason,
			"removedAt":     now,
			"updatedAt":     now,
		},
	}

	err = cc.CCDB.UpdateOne(ctx, bson.M{"_id": creatorObjID}, update)
	if err != nil {
		config.ErrorStatus("failed to remove creator", http.StatusInternalServerError, w, err)
		return
	}

	// Revoke all entitlements
	entitlementUpdate := bson.M{
		"$set": bson.M{
			"active":       false,
			"revokedAt":    now,
			"revokedBy":    adminObjID,
			"revokeReason": req.Reason,
			"updatedAt":    now,
		},
	}
	cc.EntDB.UpdateMany(ctx, bson.M{"contentCreatorId": creatorObjID, "active": true}, entitlementUpdate)

	zap.S().Infow("content creator removed",
		"creatorId", creatorObjID.Hex(),
		"removedBy", adminObjID.Hex(),
		"reason", req.Reason,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Creator removed",
	})
}

// AdminGetAnalyticsHandler returns analytics for the creator program
// GET /api/v1/admin/content-creators/analytics
func (cc ContentCreator) AdminGetAnalyticsHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Count creators by status
	totalCreators, _ := cc.CCDB.CountDocuments(ctx, bson.M{})
	activeCreators, _ := cc.CCDB.CountDocuments(ctx, bson.M{"status": "active"})
	warnedCreators, _ := cc.CCDB.CountDocuments(ctx, bson.M{"status": "warned"})
	pendingRemoval, _ := cc.CCDB.CountDocuments(ctx, bson.M{"status": "pending_removal"})

	// Count applications by status
	totalApplications, _ := cc.AppDB.CountDocuments(ctx, bson.M{})
	pendingApplications, _ := cc.AppDB.CountDocuments(ctx, bson.M{"status": bson.M{"$in": []string{"submitted", "under_review"}}})
	approvedApplications, _ := cc.AppDB.CountDocuments(ctx, bson.M{"status": "approved"})
	rejectedApplications, _ := cc.AppDB.CountDocuments(ctx, bson.M{"status": "rejected"})

	// Count active entitlements for value calculation
	activeEntitlements, _ := cc.EntDB.CountDocuments(ctx, bson.M{"active": true})

	// Base plan values: $3/month, $36/year
	monthlyValue := float64(activeEntitlements) * 3.0
	yearlyValue := float64(activeEntitlements) * 36.0

	analytics := models.ContentCreatorAnalytics{
		TotalCreators:        int(totalCreators),
		ActiveCreators:       int(activeCreators),
		WarnedCreators:       int(warnedCreators),
		PendingRemoval:       int(pendingRemoval),
		TotalApplications:    int(totalApplications),
		PendingApplications:  int(pendingApplications),
		ApprovedApplications: int(approvedApplications),
		RejectedApplications: int(rejectedApplications),
		TotalMonthlyValue:    monthlyValue,
		TotalYearlyValue:     yearlyValue,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(analytics)
}

// AdminGrantEntitlementHandler grants an entitlement to a creator
// POST /api/v1/admin/content-creators/{id}/entitlements
func (cc ContentCreator) AdminGrantEntitlementHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	adminIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	adminObjID, _ := primitive.ObjectIDFromHex(adminIDStr)

	creatorID := mux.Vars(r)["id"]
	creatorObjID, err := primitive.ObjectIDFromHex(creatorID)
	if err != nil {
		config.ErrorStatus("invalid creator ID", http.StatusBadRequest, w, err)
		return
	}

	var req models.GrantEntitlementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}

	targetObjID, err := primitive.ObjectIDFromHex(req.TargetID)
	if err != nil {
		config.ErrorStatus("invalid target ID", http.StatusBadRequest, w, err)
		return
	}

	// Check if entitlement already exists
	existingFilter := bson.M{
		"contentCreatorId": creatorObjID,
		"targetType":       req.TargetType,
		"active":           true,
	}
	existing, _ := cc.EntDB.FindOne(ctx, existingFilter)
	if existing != nil && req.TargetType == "community" {
		config.ErrorStatus("creator already has an active community entitlement", http.StatusConflict, w, nil)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	entitlement := models.ContentCreatorEntitlement{
		ID:               primitive.NewObjectID(),
		ContentCreatorID: creatorObjID,
		TargetType:       req.TargetType,
		TargetID:         targetObjID,
		Plan:             req.Plan,
		Source:           "content_creator_program",
		Active:           true,
		GrantedAt:        now,
		GrantedBy:        adminObjID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	_, err = cc.EntDB.InsertOne(ctx, entitlement)
	if err != nil {
		config.ErrorStatus("failed to grant entitlement", http.StatusInternalServerError, w, err)
		return
	}

	zap.S().Infow("entitlement granted",
		"entitlementId", entitlement.ID.Hex(),
		"creatorId", creatorObjID.Hex(),
		"targetType", req.TargetType,
		"targetId", req.TargetID,
		"grantedBy", adminObjID.Hex(),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"message":     "Entitlement granted",
		"entitlement": entitlement,
	})
}

// --- Helper Functions ---

func (cc ContentCreator) getCreatorEntitlements(ctx context.Context, creatorID primitive.ObjectID) models.EntitlementsSummary {
	summary := models.EntitlementsSummary{
		PersonalPlan: false,
		CommunityPlan: models.CommunityPlanSummary{
			Active: false,
		},
	}

	cursor, err := cc.EntDB.Find(ctx, bson.M{
		"contentCreatorId": creatorID,
		"active":           true,
	})
	if err != nil {
		return summary
	}
	defer cursor.Close(ctx)

	var entitlements []models.ContentCreatorEntitlement
	if err := cursor.All(ctx, &entitlements); err != nil {
		return summary
	}

	for _, ent := range entitlements {
		if ent.TargetType == "user" {
			summary.PersonalPlan = true
		} else if ent.TargetType == "community" {
			summary.CommunityPlan.Active = true
			summary.CommunityPlan.CommunityID = ent.TargetID.Hex()
			// TODO: Fetch community name from CDB if needed
		}
	}

	return summary
}

// generateSlug creates a URL-friendly slug from a display name
func generateSlug(displayName string) string {
	// Convert to lowercase
	slug := strings.ToLower(displayName)

	// Replace spaces with hyphens
	slug = strings.ReplaceAll(slug, " ", "-")

	// Remove special characters
	reg := regexp.MustCompile("[^a-z0-9-]")
	slug = reg.ReplaceAllString(slug, "")

	// Remove multiple consecutive hyphens
	reg = regexp.MustCompile("-+")
	slug = reg.ReplaceAllString(slug, "-")

	// Trim hyphens from start and end
	slug = strings.Trim(slug, "-")

	return slug
}
