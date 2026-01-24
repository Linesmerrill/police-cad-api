package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	templates "github.com/linesmerrill/police-cad-api/templates/html"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

// ContentCreator handler struct for content creator endpoints
type ContentCreator struct {
	AppDB   databases.ContentCreatorApplicationDatabase
	CCDB    databases.ContentCreatorDatabase
	EntDB   databases.ContentCreatorEntitlementDatabase
	SnapDB  databases.ContentCreatorSnapshotDatabase
	UDB     databases.UserDatabase
	CDB     databases.CommunityDatabase
	AdminDB databases.AdminDatabase
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

// --- Email Helper Functions ---

// sendContentCreatorEmail sends an email using SendGrid
func sendContentCreatorEmail(toEmail, toName, subject, htmlContent, plainText string) error {
	from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
	to := mail.NewEmail(toName, toEmail)
	message := mail.NewSingleEmail(from, subject, to, plainText, htmlContent)
	client := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))
	response, err := client.Send(message)
	if err != nil {
		zap.S().Errorw("failed to send email", "error", err, "to", toEmail)
		return err
	}
	if response.StatusCode >= 400 {
		zap.S().Errorw("sendgrid returned error status", "status", response.StatusCode, "body", response.Body, "to", toEmail)
		return fmt.Errorf("sendgrid error: status %d", response.StatusCode)
	}
	zap.S().Infow("email sent successfully", "to", toEmail, "subject", subject)
	return nil
}

// sendApplicationSubmittedEmail sends confirmation email to the applicant
func (cc ContentCreator) sendApplicationSubmittedEmail(ctx context.Context, userID primitive.ObjectID, displayName string) {
	// Get user email
	var user struct {
		Details struct {
			Email    string `bson:"email"`
			Username string `bson:"username"`
		} `bson:"user"`
	}
	err := cc.UDB.FindOne(ctx, bson.M{"_id": userID}).Decode(&user)
	if err != nil {
		zap.S().Errorw("failed to get user email for application confirmation", "error", err, "userId", userID.Hex())
		return
	}
	if user.Details.Email == "" {
		zap.S().Warnw("user has no email, skipping application confirmation", "userId", userID.Hex())
		return
	}

	subject := "Application Received - Lines Police CAD Creator Program"
	htmlContent := templates.RenderApplicationSubmittedEmail(displayName)
	plainText := fmt.Sprintf("Hi %s, Thank you for applying to the Lines Police CAD Content Creator Program! Our team will review your application within 5-7 business days. You can check your status at https://www.linespolice-cad.com/content-creators/me", displayName)

	go func() {
		if err := sendContentCreatorEmail(user.Details.Email, displayName, subject, htmlContent, plainText); err != nil {
			zap.S().Errorw("failed to send application submitted email", "error", err, "userId", userID.Hex())
		}
	}()
}

// sendAdminNewApplicationEmail sends notification to all active admins
func (cc ContentCreator) sendAdminNewApplicationEmail(ctx context.Context, applicantUsername, displayName, primaryPlatform string, totalFollowers int) {
	// Get all active admins
	cursor, err := cc.AdminDB.Find(ctx, bson.M{"active": true})
	if err != nil {
		zap.S().Errorw("failed to get admins for new application notification", "error", err)
		return
	}
	defer cursor.Close(ctx)

	var admins []models.AdminUser
	if err := cursor.All(ctx, &admins); err != nil {
		zap.S().Errorw("failed to decode admins", "error", err)
		return
	}

	subject := "New Creator Application Submitted - Lines Police CAD"
	followersStr := fmt.Sprintf("%d", totalFollowers)
	if totalFollowers >= 1000000 {
		followersStr = fmt.Sprintf("%.1fM", float64(totalFollowers)/1000000)
	} else if totalFollowers >= 1000 {
		followersStr = fmt.Sprintf("%.1fK", float64(totalFollowers)/1000)
	}

	htmlContent := templates.RenderAdminNewApplicationEmail(applicantUsername, displayName, primaryPlatform, followersStr)
	plainText := fmt.Sprintf("New Creator Application: %s (%s) - Platform: %s, Followers: %s. Please review within 5-7 business days at https://www.linespolice-cad.com/lpc-admin", applicantUsername, displayName, primaryPlatform, followersStr)

	go func() {
		for _, admin := range admins {
			if admin.Email != "" {
				if err := sendContentCreatorEmail(admin.Email, "Admin", subject, htmlContent, plainText); err != nil {
					zap.S().Errorw("failed to send admin notification email", "error", err, "adminEmail", admin.Email)
				}
			}
		}
	}()
}

// sendApplicationDecisionEmail sends approval/rejection email to the applicant
func (cc ContentCreator) sendApplicationDecisionEmail(ctx context.Context, userID primitive.ObjectID, displayName, status, rejectionReason, feedback string) {
	// Get user email
	var user struct {
		Details struct {
			Email    string `bson:"email"`
			Username string `bson:"username"`
		} `bson:"user"`
	}
	err := cc.UDB.FindOne(ctx, bson.M{"_id": userID}).Decode(&user)
	if err != nil {
		zap.S().Errorw("failed to get user email for decision notification", "error", err, "userId", userID.Hex())
		return
	}
	if user.Details.Email == "" {
		zap.S().Warnw("user has no email, skipping decision notification", "userId", userID.Hex())
		return
	}

	var subject, htmlContent, plainText string
	if status == "approved" {
		subject = "Welcome to the Creator Program! - Lines Police CAD"
		htmlContent = templates.RenderApplicationApprovedEmail(displayName)
		plainText = fmt.Sprintf("Congratulations %s! Your application to the Lines Police CAD Content Creator Program has been approved! Visit https://www.linespolice-cad.com/content-creators/me to view your benefits.", displayName)
	} else {
		subject = "Application Update - Lines Police CAD Creator Program"
		htmlContent = templates.RenderApplicationRejectedEmail(displayName, rejectionReason, feedback)
		plainText = fmt.Sprintf("Hi %s, Your application to the Lines Police CAD Content Creator Program was not approved. Reason: %s. You can apply again in the future. Visit https://www.linespolice-cad.com/content-creators/me for more details.", displayName, rejectionReason)
	}

	go func() {
		if err := sendContentCreatorEmail(user.Details.Email, displayName, subject, htmlContent, plainText); err != nil {
			zap.S().Errorw("failed to send decision email", "error", err, "userId", userID.Hex(), "status", status)
		}
	}()
}

// sendCreatorRemovedEmail sends removal notification email to a creator removed by admin
func (cc ContentCreator) sendCreatorRemovedEmail(ctx context.Context, userID primitive.ObjectID, displayName, reason string) {
	// Get user email
	var user struct {
		Details struct {
			Email    string `bson:"email"`
			Username string `bson:"username"`
		} `bson:"user"`
	}
	err := cc.UDB.FindOne(ctx, bson.M{"_id": userID}).Decode(&user)
	if err != nil {
		zap.S().Errorw("failed to get user email for removal notification", "error", err, "userId", userID.Hex())
		return
	}
	if user.Details.Email == "" {
		zap.S().Warnw("user has no email, skipping removal notification", "userId", userID.Hex())
		return
	}

	subject := "Creator Program Removal Notice - Lines Police CAD"
	htmlContent := templates.RenderCreatorRemovedEmail(displayName, reason)
	plainText := fmt.Sprintf("Hi %s, Your membership in the Lines Police CAD Content Creator Program has been terminated. Reason: %s. All benefits have been revoked. If you believe this was in error or wish to rejoin, you may submit a new application at https://www.linespolice-cad.com/content-creators/apply", displayName, reason)

	go func() {
		if err := sendContentCreatorEmail(user.Details.Email, displayName, subject, htmlContent, plainText); err != nil {
			zap.S().Errorw("failed to send removal email", "error", err, "userId", userID.Hex())
		}
	}()
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
			ThemeColor:      c.ThemeColor,
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
		ThemeColor:      creator.ThemeColor,
		PrimaryPlatform: creator.PrimaryPlatform,
		Platforms:       creator.Platforms,
		Featured:        creator.Featured,
		JoinedAt:        creator.JoinedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"creator": publicCreator,
	})
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

	// Validate bio length
	if len(req.Bio) < 20 {
		config.ErrorStatus("bio must be at least 20 characters", http.StatusBadRequest, w, nil)
		return
	}
	if len(req.Bio) > 500 {
		config.ErrorStatus("bio must be at most 500 characters", http.StatusBadRequest, w, nil)
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
		Bio:             req.Bio,
		Status:          "submitted",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	_, err = cc.AppDB.InsertOne(ctx, application)
	if err != nil {
		config.ErrorStatus("failed to create application", http.StatusInternalServerError, w, err)
		return
	}

	// Get user details for email notifications
	var user struct {
		Details struct {
			Username string `bson:"username"`
		} `bson:"user"`
	}
	cc.UDB.FindOne(ctx, bson.M{"_id": userObjID}).Decode(&user)

	// Calculate total followers for admin notification
	totalFollowers := 0
	for _, p := range req.Platforms {
		totalFollowers += p.FollowerCount
	}

	// Send confirmation email to applicant
	cc.sendApplicationSubmittedEmail(ctx, userObjID, req.DisplayName)

	// Send notification to all admins
	cc.sendAdminNewApplicationEmail(ctx, user.Details.Username, req.DisplayName, req.PrimaryPlatform, totalFollowers)

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

	// Check for existing creator profile first (include removed so frontend can show proper state)
	creatorFilter := bson.M{
		"userId": userObjID,
	}
	creator, _ := cc.CCDB.FindOne(ctx, creatorFilter)

	if creator != nil {
		// User has a creator profile (active, warned, or removed), return their profile with entitlements
		entitlements := cc.getCreatorEntitlements(ctx, creator.ID, userObjID)

		creatorResponse := models.ContentCreatorPrivateResponse{
			ID:              creator.ID,
			DisplayName:     creator.DisplayName,
			Slug:            creator.Slug,
			ProfileImage:    creator.ProfileImage,
			Bio:             creator.Bio,
			ThemeColor:      creator.ThemeColor,
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
		Bio:             app.Bio,
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
		if len(req.Bio) > 500 {
			config.ErrorStatus("bio must be at most 500 characters", http.StatusBadRequest, w, nil)
			return
		}
		updateFields["bio"] = req.Bio
	}
	if req.ThemeColor != "" {
		// Normalize to lowercase
		themeColor := strings.ToLower(req.ThemeColor)
		if !isValidThemeColor(themeColor) {
			config.ErrorStatus("invalid theme color - must be a valid hex color (not too dark or too light)", http.StatusBadRequest, w, nil)
			return
		}
		updateFields["themeColor"] = themeColor
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

// GetOwnedCommunitiesHandler returns communities owned by the current user that are eligible for promotion
// GET /api/v1/content-creators/me/owned-communities
func (cc ContentCreator) GetOwnedCommunitiesHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	userIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	userObjID, _ := primitive.ObjectIDFromHex(userIDStr)

	// Verify user is an active/approved creator
	creatorFilter := bson.M{
		"userId": userObjID,
		"status": bson.M{"$in": []string{"active", "approved", "warned"}},
	}
	creator, err := cc.CCDB.FindOne(ctx, creatorFilter)
	if err != nil {
		config.ErrorStatus("you must be an active creator to access this", http.StatusForbidden, w, err)
		return
	}

	// Check if creator already has a community entitlement
	existingEntitlement, _ := cc.EntDB.FindOne(ctx, bson.M{
		"contentCreatorId": creator.ID,
		"targetType":       "community",
		"active":           true,
	})

	var appliedCommunityID string
	if existingEntitlement != nil {
		appliedCommunityID = existingEntitlement.TargetID.Hex()
	}

	// Find communities where user is owner
	communityFilter := bson.M{
		"community.ownerID": userIDStr,
	}

	cursor, err := cc.CDB.Find(ctx, communityFilter, nil)
	if err != nil {
		config.ErrorStatus("failed to fetch communities", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	type CommunityResponse struct {
		ID               string `json:"_id"`
		Name             string `json:"name"`
		HasPromotion     bool   `json:"hasPromotion"`
		CurrentPlan      string `json:"currentPlan,omitempty"`
		IsPromotionApplied bool `json:"isPromotionApplied"`
	}

	communities := make([]CommunityResponse, 0)

	var allCommunities []models.Community
	if err := cursor.All(ctx, &allCommunities); err != nil {
		config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
		return
	}

	for _, comm := range allCommunities {
		isApplied := appliedCommunityID == comm.ID.Hex()
		communities = append(communities, CommunityResponse{
			ID:                 comm.ID.Hex(),
			Name:               comm.Details.Name,
			HasPromotion:       comm.Details.Subscription.Active,
			CurrentPlan:        comm.Details.Subscription.Plan,
			IsPromotionApplied: isApplied,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":              true,
		"communities":          communities,
		"hasAppliedPromotion":  appliedCommunityID != "",
		"appliedCommunityId":   appliedCommunityID,
	})
}

// ApplyCommunityPromotionHandler applies the creator's community promotion to a community they own
// POST /api/v1/content-creators/me/community-promotion
func (cc ContentCreator) ApplyCommunityPromotionHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	userIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	userObjID, _ := primitive.ObjectIDFromHex(userIDStr)

	// Parse request
	var req struct {
		CommunityID string `json:"communityId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}

	if req.CommunityID == "" {
		config.ErrorStatus("communityId is required", http.StatusBadRequest, w, nil)
		return
	}

	communityObjID, err := primitive.ObjectIDFromHex(req.CommunityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Verify user is an active/approved creator
	creatorFilter := bson.M{
		"userId": userObjID,
		"status": bson.M{"$in": []string{"active", "approved", "warned"}},
	}
	creator, err := cc.CCDB.FindOne(ctx, creatorFilter)
	if err != nil {
		config.ErrorStatus("you must be an active creator to use this benefit", http.StatusForbidden, w, err)
		return
	}

	// Check if creator already has a community entitlement
	existingEntitlement, _ := cc.EntDB.FindOne(ctx, bson.M{
		"contentCreatorId": creator.ID,
		"targetType":       "community",
		"active":           true,
	})
	if existingEntitlement != nil {
		config.ErrorStatus("you have already applied your community promotion - you can only apply it to one community", http.StatusConflict, w, nil)
		return
	}

	// Verify user owns the community
	community, err := cc.CDB.FindOne(ctx, bson.M{"_id": communityObjID})
	if err != nil || community == nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	if community.Details.OwnerID != userIDStr {
		config.ErrorStatus("you must be the owner of this community to apply the promotion", http.StatusForbidden, w, nil)
		return
	}

	// Check if community already has an active paid subscription
	if community.Details.Subscription.Active && community.Details.Subscription.Plan != "" && community.Details.Subscription.Plan != "base" {
		config.ErrorStatus("this community already has an active subscription", http.StatusConflict, w, nil)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	// Create the community entitlement
	entitlement := models.ContentCreatorEntitlement{
		ID:               primitive.NewObjectID(),
		ContentCreatorID: creator.ID,
		TargetType:       "community",
		TargetID:         communityObjID,
		Plan:             "base",
		Source:           "content_creator_program",
		Active:           true,
		GrantedAt:        now,
		GrantedBy:        userObjID, // Self-granted by creator
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	_, err = cc.EntDB.InsertOne(ctx, entitlement)
	if err != nil {
		config.ErrorStatus("failed to create entitlement", http.StatusInternalServerError, w, err)
		return
	}

	// Update the community subscription
	subscriptionUpdate := bson.M{
		"$set": bson.M{
			"community.subscription.plan":      "base",
			"community.subscription.active":    true,
			"community.subscription.id":        "cc_program_" + creator.ID.Hex(),
			"community.subscription.createdAt": now,
			"community.subscription.updatedAt": now,
		},
	}
	err = cc.CDB.UpdateOne(ctx, bson.M{"_id": communityObjID}, subscriptionUpdate)
	if err != nil {
		config.ErrorStatus("failed to update community subscription", http.StatusInternalServerError, w, err)
		return
	}

	zap.S().Infow("content creator applied community promotion",
		"creatorId", creator.ID.Hex(),
		"communityId", communityObjID.Hex(),
		"communityName", community.Details.Name,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"message":       fmt.Sprintf("Base Plan promotion applied to %s", community.Details.Name),
		"communityId":   communityObjID.Hex(),
		"communityName": community.Details.Name,
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

	// For approved applications, look up the creator status
	type ApplicationWithCreatorStatus struct {
		models.ContentCreatorApplication
		CreatorStatus string `json:"creatorStatus,omitempty"`
	}

	enrichedApps := make([]ApplicationWithCreatorStatus, len(applications))
	for i, app := range applications {
		enrichedApps[i] = ApplicationWithCreatorStatus{
			ContentCreatorApplication: app,
		}
		// If approved and has a creatorId, look up the creator status
		if app.Status == "approved" && app.CreatorID != nil {
			creator, err := cc.CCDB.FindOne(ctx, bson.M{"_id": *app.CreatorID})
			if err == nil && creator != nil {
				enrichedApps[i].CreatorStatus = creator.Status
			}
		}
	}

	totalPages := int((totalCount + int64(limit) - 1) / int64(limit))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"applications": enrichedApps,
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

	// Build response with optional creator status
	response := struct {
		*models.ContentCreatorApplication
		CreatorStatus string `json:"creatorStatus,omitempty"`
	}{
		ContentCreatorApplication: application,
	}

	// If approved and has a creatorId, look up the creator status
	if application.Status == "approved" && application.CreatorID != nil {
		creator, err := cc.CCDB.FindOne(ctx, bson.M{"_id": *application.CreatorID})
		if err == nil && creator != nil {
			response.CreatorStatus = creator.Status
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// AdminApproveApplicationHandler approves an application (requires 2 approvers, unless owner override)
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

	// Parse optional request body for owner override
	var req struct {
		OwnerOverride bool `json:"ownerOverride"`
	}
	// Attempt to decode body, but don't fail if empty (for backward compatibility)
	json.NewDecoder(r.Body).Decode(&req)

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

	// Check if owner is using override to bypass dual-approval
	isOwnerOverride := false
	if req.OwnerOverride {
		// Verify the admin is actually an owner
		admin, adminErr := cc.AdminDB.FindOne(ctx, bson.M{"_id": adminObjID})
		if adminErr != nil {
			config.ErrorStatus("failed to verify admin role", http.StatusInternalServerError, w, adminErr)
			return
		}
		// Check if admin has owner role (either in Role field or Roles array)
		hasOwnerRole := admin.Role == "owner"
		for _, role := range admin.Roles {
			if role == "owner" {
				hasOwnerRole = true
				break
			}
		}
		if !hasOwnerRole {
			config.ErrorStatus("only owners can use the override option", http.StatusForbidden, w, nil)
			return
		}
		isOwnerOverride = true
	}

	// Check if this is first or second approval (or owner override)
	if application.FirstApprovalBy == nil && !isOwnerOverride {
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

	// Second approval - check it's a different admin (unless owner override)
	if !isOwnerOverride && application.FirstApprovalBy != nil && application.FirstApprovalBy.Hex() == adminObjID.Hex() {
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
		Bio:             application.Bio,
		ThemeColor:      generateRandomThemeColor(),
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

	// Update application with the creatorId for easy lookup
	cc.AppDB.UpdateOne(ctx, bson.M{"_id": appObjID}, bson.M{"$set": bson.M{"creatorId": creator.ID}})

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

	// Only grant Base subscription if user doesn't already have a higher-tier plan
	// Check user's current subscription first
	var currentUser models.User
	shouldUpdateSubscription := true
	if err := cc.UDB.FindOne(ctx, bson.M{"_id": application.UserID}).Decode(&currentUser); err == nil {
		currentPlan := currentUser.Details.Subscription.Plan
		// Don't overwrite if user has a higher-tier plan (premium, premium_plus, etc.)
		higherTierPlans := map[string]bool{"premium": true, "premium_plus": true, "enterprise": true}
		if higherTierPlans[currentPlan] {
			shouldUpdateSubscription = false
			zap.S().Infow("skipping subscription update - user already has higher tier plan",
				"userId", application.UserID.Hex(),
				"currentPlan", currentPlan,
			)
		}
	}

	if shouldUpdateSubscription {
		subscriptionUpdate := bson.M{
			"$set": bson.M{
				"user.subscription.plan":      "base",
				"user.subscription.active":    true,
				"user.subscription.id":        "cc_program_" + creator.ID.Hex(),
				"user.subscription.createdAt": now,
				"user.subscription.updatedAt": now,
			},
		}
		cc.UDB.UpdateOne(ctx, bson.M{"_id": application.UserID}, subscriptionUpdate)
	}

	// Send approval email to the applicant
	cc.sendApplicationDecisionEmail(ctx, application.UserID, application.DisplayName, "approved", "", "")

	// Log the approval with appropriate context
	if isOwnerOverride {
		zap.S().Infow("content creator application approved via owner override",
			"applicationId", appObjID.Hex(),
			"creatorId", creator.ID.Hex(),
			"approvedBy", adminObjID.Hex(),
			"ownerOverride", true,
		)
	} else {
		firstApproverID := ""
		if application.FirstApprovalBy != nil {
			firstApproverID = application.FirstApprovalBy.Hex()
		}
		zap.S().Infow("content creator application approved",
			"applicationId", appObjID.Hex(),
			"creatorId", creator.ID.Hex(),
			"firstApprovalBy", firstApproverID,
			"secondApprovalBy", adminObjID.Hex(),
		)
	}

	responseMessage := "Application approved"
	if isOwnerOverride {
		responseMessage = "Application approved via owner override"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"message":       responseMessage,
		"creator":       creator,
		"ownerOverride": isOwnerOverride,
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

	// Send rejection email to the applicant
	cc.sendApplicationDecisionEmail(ctx, application.UserID, application.DisplayName, "rejected", req.RejectionReason, req.Feedback)

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

	// Fetch the creator to get their details for the email
	creator, err := cc.CCDB.FindOne(ctx, bson.M{"_id": creatorObjID})
	if err != nil {
		config.ErrorStatus("creator not found", http.StatusNotFound, w, err)
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

	// Send removal notification email to the creator
	if creator.UserID != nil {
		cc.sendCreatorRemovedEmail(ctx, *creator.UserID, creator.DisplayName, req.Reason)
	}

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

func (cc ContentCreator) getCreatorEntitlements(ctx context.Context, creatorID primitive.ObjectID, userID primitive.ObjectID) models.EntitlementsSummary {
	summary := models.EntitlementsSummary{
		PersonalPlan:         false,
		PersonalPlanFallback: false,
		CommunityPlan: models.CommunityPlanSummary{
			Active: false,
		},
	}

	// Fetch user's current subscription plan
	var user models.User
	if err := cc.UDB.FindOne(ctx, bson.M{"_id": userID.Hex()}).Decode(&user); err == nil {
		summary.CurrentUserPlan = user.Details.Subscription.Plan
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
			// Check if user has a higher plan than base - if so, entitlement is a fallback
			currentPlan := summary.CurrentUserPlan
			if currentPlan != "" && currentPlan != "free" && currentPlan != "base" {
				summary.PersonalPlanFallback = true
			}
		} else if ent.TargetType == "community" {
			summary.CommunityPlan.Active = true
			summary.CommunityPlan.CommunityID = ent.TargetID.Hex()
			// Fetch community name
			if cc.CDB != nil {
				community, commErr := cc.CDB.FindOne(ctx, bson.M{"_id": ent.TargetID})
				if commErr == nil && community != nil {
					summary.CommunityPlan.CommunityName = community.Details.Name
				}
			}
		}
	}

	return summary
}

// generateRandomThemeColor returns a random hex color for a creator's profile theme
// Excludes very light colors (too close to white) and very dark colors (too close to black)
func generateRandomThemeColor() string {
	// Predefined set of vibrant colors that work well on dark backgrounds
	colors := []string{
		"#fbbf24", // amber
		"#f59e0b", // orange
		"#ef4444", // red
		"#ec4899", // pink
		"#a855f7", // purple
		"#8b5cf6", // violet
		"#6366f1", // indigo
		"#3b82f6", // blue
		"#0ea5e9", // sky
		"#06b6d4", // cyan
		"#14b8a6", // teal
		"#10b981", // emerald
		"#22c55e", // green
		"#84cc16", // lime
	}
	return colors[rand.Intn(len(colors))]
}

// isValidThemeColor checks if a hex color is valid and not too close to black or white
func isValidThemeColor(hex string) bool {
	// Must start with # and be 7 chars total
	if len(hex) != 7 || hex[0] != '#' {
		return false
	}

	// Parse RGB values
	r, err1 := strconv.ParseInt(hex[1:3], 16, 64)
	g, err2 := strconv.ParseInt(hex[3:5], 16, 64)
	b, err3 := strconv.ParseInt(hex[5:7], 16, 64)

	if err1 != nil || err2 != nil || err3 != nil {
		return false
	}

	// Calculate luminance (simplified)
	luminance := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 255.0

	// Reject colors that are too dark (< 0.15) or too light (> 0.85)
	if luminance < 0.15 || luminance > 0.85 {
		return false
	}

	return true
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
