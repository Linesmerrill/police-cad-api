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

// sendLowFollowerWarningEmail sends warning email when creator drops below follower threshold
func (cc ContentCreator) sendLowFollowerWarningEmail(ctx context.Context, userID primitive.ObjectID, displayName string, currentFollowers, threshold, gracePeriodDays int) {
	var user struct {
		Details struct {
			Email    string `bson:"email"`
			Username string `bson:"username"`
		} `bson:"user"`
	}
	err := cc.UDB.FindOne(ctx, bson.M{"_id": userID}).Decode(&user)
	if err != nil || user.Details.Email == "" {
		zap.S().Warnw("could not send low follower warning email", "error", err, "userId", userID.Hex())
		return
	}

	subject := "Action Required: Follower Count Below Minimum - Lines Police CAD"
	htmlContent := templates.RenderLowFollowerWarningEmail(displayName, currentFollowers, threshold, gracePeriodDays)
	plainText := fmt.Sprintf("Hi %s, Your follower count (%d) has dropped below our minimum requirement of %d. You have %d days to increase your followers or your creator account will be removed. Visit https://www.linespolice-cad.com/content-creators/me to sync your updated counts.", displayName, currentFollowers, threshold, gracePeriodDays)

	if err := sendContentCreatorEmail(user.Details.Email, displayName, subject, htmlContent, plainText); err != nil {
		zap.S().Errorw("failed to send low follower warning email", "error", err, "userId", userID.Hex())
	}
}

// sendGracePeriodRecoveryEmail sends congratulations email when creator gets back above threshold
func (cc ContentCreator) sendGracePeriodRecoveryEmail(ctx context.Context, userID primitive.ObjectID, displayName string, currentFollowers int) {
	var user struct {
		Details struct {
			Email    string `bson:"email"`
			Username string `bson:"username"`
		} `bson:"user"`
	}
	err := cc.UDB.FindOne(ctx, bson.M{"_id": userID}).Decode(&user)
	if err != nil || user.Details.Email == "" {
		zap.S().Warnw("could not send grace period recovery email", "error", err, "userId", userID.Hex())
		return
	}

	subject := "Great News: Your Account is Back in Good Standing! - Lines Police CAD"
	htmlContent := templates.RenderGracePeriodRecoveryEmail(displayName, currentFollowers)
	plainText := fmt.Sprintf("Congratulations %s! Your follower count (%d) is now above our minimum requirement. Your creator account is back in good standing and all benefits remain active. Keep up the great work!", displayName, currentFollowers)

	if err := sendContentCreatorEmail(user.Details.Email, displayName, subject, htmlContent, plainText); err != nil {
		zap.S().Errorw("failed to send grace period recovery email", "error", err, "userId", userID.Hex())
	}
}

// sendGracePeriodReminderEmail sends reminder email 1 day before grace period ends
func (cc ContentCreator) sendGracePeriodReminderEmail(ctx context.Context, userID primitive.ObjectID, displayName string, currentFollowers, threshold int) {
	var user struct {
		Details struct {
			Email    string `bson:"email"`
			Username string `bson:"username"`
		} `bson:"user"`
	}
	err := cc.UDB.FindOne(ctx, bson.M{"_id": userID}).Decode(&user)
	if err != nil || user.Details.Email == "" {
		zap.S().Warnw("could not send grace period reminder email", "error", err, "userId", userID.Hex())
		return
	}

	subject := "Final Reminder: Creator Account Removal Tomorrow - Lines Police CAD"
	htmlContent := templates.RenderGracePeriodReminderEmail(displayName, currentFollowers, threshold)
	plainText := fmt.Sprintf("Hi %s, This is a final reminder that your creator account will be removed tomorrow due to low follower count (%d, minimum required: %d). If you've increased your followers, please visit https://www.linespolice-cad.com/content-creators/me to sync your updated counts before the deadline.", displayName, currentFollowers, threshold)

	if err := sendContentCreatorEmail(user.Details.Email, displayName, subject, htmlContent, plainText); err != nil {
		zap.S().Errorw("failed to send grace period reminder email", "error", err, "userId", userID.Hex())
	}
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

// CheckSlugAvailabilityHandler checks if a display name would create a conflicting slug
// GET /api/v1/content-creators/check-slug?displayName=xxx
func (cc ContentCreator) CheckSlugAvailabilityHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	displayName := r.URL.Query().Get("displayName")
	if displayName == "" {
		config.ErrorStatus("displayName query parameter is required", http.StatusBadRequest, w, nil)
		return
	}

	// Generate what the slug would be
	slug := generateSlug(displayName)

	if slug == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"available": false,
			"slug":      "",
			"message":   "Display name must contain at least one letter or number",
		})
		return
	}

	// Check if slug already exists (for any status - active, removed, etc.)
	existingCreator, _ := cc.CCDB.FindOne(ctx, bson.M{"slug": slug})

	available := existingCreator == nil

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"available": available,
		"slug":      slug,
		"message": func() string {
			if available {
				return ""
			}
			return "This display name would create a URL that's already taken. Your profile URL will include a unique suffix."
		}(),
	})
}

// GetContentCreatorStatsHandler returns public stats about the creator program
// GET /api/v1/content-creators/stats
func (cc ContentCreator) GetContentCreatorStatsHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Count active creators
	activeCount, err := cc.CCDB.CountDocuments(ctx, bson.M{"status": "active"})
	if err != nil {
		config.ErrorStatus("failed to count creators", http.StatusInternalServerError, w, err)
		return
	}

	// Calculate combined reach (total followers across all active creators)
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"status": "active"}}},
		{{Key: "$unwind", Value: "$platforms"}},
		{{Key: "$group", Value: bson.M{
			"_id":            nil,
			"totalFollowers": bson.M{"$sum": "$platforms.followerCount"},
		}}},
	}

	cursor, err := cc.CCDB.Aggregate(ctx, pipeline)
	if err != nil {
		config.ErrorStatus("failed to calculate reach", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	var combinedReach int64 = 0
	if cursor.Next(ctx) {
		var result struct {
			TotalFollowers int64 `bson:"totalFollowers"`
		}
		if err := cursor.Decode(&result); err == nil {
			combinedReach = result.TotalFollowers
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"activeCount":   activeCount,
		"combinedReach": combinedReach,
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

	// Check if user already has a pending application (submitted or under_review)
	// Note: "approved" applications are allowed to reapply if their creator record was removed
	existingFilter := bson.M{
		"userId": userObjID,
		"status": bson.M{"$in": []string{"submitted", "under_review"}},
	}
	existingApp, _ := cc.AppDB.FindOne(ctx, existingFilter)
	if existingApp != nil {
		config.ErrorStatus("you already have a pending application", http.StatusConflict, w, nil)
		return
	}

	// Check if user is already an active creator (not removed)
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

	// First, check for an active/warned creator profile (prioritize non-removed)
	activeCreatorFilter := bson.M{
		"userId": userObjID,
		"status": bson.M{"$in": []string{"active", "warned"}},
	}
	creator, _ := cc.CCDB.FindOne(ctx, activeCreatorFilter)

	// If no active creator, check for a removed one
	if creator == nil {
		removedCreatorFilter := bson.M{
			"userId": userObjID,
			"status": "removed",
		}
		creator, _ = cc.CCDB.FindOne(ctx, removedCreatorFilter)
	}

	// If creator is removed, check if they have a new pending application
	// If so, return the application instead of the removed creator profile
	if creator != nil && creator.Status == "removed" {
		pendingAppFilter := bson.M{
			"userId": userObjID,
			"status": bson.M{"$in": []string{"submitted", "under_review"}},
		}
		pendingApp, _ := cc.AppDB.FindOne(ctx, pendingAppFilter)
		if pendingApp != nil {
			// Return the pending application instead of the removed creator
			appResponse := models.ContentCreatorApplicationResponse{
				ID:              pendingApp.ID,
				DisplayName:     pendingApp.DisplayName,
				PrimaryPlatform: pendingApp.PrimaryPlatform,
				Platforms:       pendingApp.Platforms,
				Description:     pendingApp.Description,
				Bio:             pendingApp.Bio,
				Status:          pendingApp.Status,
				CreatedAt:       pendingApp.CreatedAt,
			}
			response := models.ContentCreatorMeResponse{
				Success:     true,
				Application: &appResponse,
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(response)
			return
		}
	}

	if creator != nil {
		// User has a creator profile (active, warned, or removed), return their profile with entitlements
		entitlements := cc.getCreatorEntitlements(ctx, creator.ID, userObjID)

		creatorResponse := models.ContentCreatorPrivateResponse{
			ID:                   creator.ID,
			DisplayName:          creator.DisplayName,
			Slug:                 creator.Slug,
			ProfileImage:         creator.ProfileImage,
			Bio:                  creator.Bio,
			ThemeColor:           creator.ThemeColor,
			PrimaryPlatform:      creator.PrimaryPlatform,
			Platforms:            creator.Platforms,
			Status:               creator.Status,
			Featured:             creator.Featured,
			WarnedAt:             creator.WarnedAt,
			WarningMessage:       creator.WarningMessage,
			JoinedAt:             creator.JoinedAt,
			Entitlements:         entitlements,
			GracePeriodStartedAt: creator.GracePeriodStartedAt,
			GracePeriodEndsAt:    creator.GracePeriodEndsAt,
			LastSyncedAt:         creator.LastSyncedAt,
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

	// Find all active entitlements before revoking (to update subscriptions)
	entitlementsCursor, _ := cc.EntDB.Find(ctx, bson.M{"contentCreatorId": creator.ID, "active": true}, nil)
	var entitlements []models.ContentCreatorEntitlement
	entitlementsCursor.All(ctx, &entitlements)

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

	// Deactivate subscriptions for revoked entitlements
	// Only deactivate if the subscription is "base" or "free" (from the program)
	// Don't touch higher-tier paid subscriptions
	subscriptionDeactivate := bson.M{
		"$set": bson.M{
			"community.subscription.active":    false,
			"community.subscription.updatedAt": now,
		},
	}
	userSubscriptionDeactivate := bson.M{
		"$set": bson.M{
			"user.subscription.active":    false,
			"user.subscription.updatedAt": now,
		},
	}
	for _, ent := range entitlements {
		if ent.TargetType == "community" {
			// Only deactivate if plan is "base" or "free" (from program)
			cc.CDB.UpdateOne(ctx, bson.M{
				"_id": ent.TargetID,
				"community.subscription.plan": bson.M{"$in": []string{"base", "free", ""}},
			}, subscriptionDeactivate)
		} else if ent.TargetType == "user" {
			// Only deactivate if plan is "base" or "free" (from program)
			cc.UDB.UpdateOne(ctx, bson.M{
				"_id": ent.TargetID,
				"user.subscription.plan": bson.M{"$in": []string{"base", "free", ""}},
			}, userSubscriptionDeactivate)
		}
	}

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

	// Check if community already has an active paid subscription (not free or base)
	if community.Details.Subscription.Active && community.Details.Subscription.Plan != "" && community.Details.Subscription.Plan != "base" && community.Details.Subscription.Plan != "free" {
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

// SyncFollowersHandler allows creators to manually sync their follower counts
// POST /api/v1/content-creators/me/sync
func (cc ContentCreator) SyncFollowersHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	userIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	userObjID, _ := primitive.ObjectIDFromHex(userIDStr)

	// Find the creator
	creatorFilter := bson.M{
		"userId": userObjID,
		"status": bson.M{"$in": []string{"active", "warned"}},
	}
	creator, err := cc.CCDB.FindOne(ctx, creatorFilter)
	if err != nil {
		config.ErrorStatus("creator profile not found", http.StatusNotFound, w, err)
		return
	}

	// Check rate limit: can only sync once per 24 hours
	if creator.LastSyncedAt != nil {
		lastSync := creator.LastSyncedAt.Time()
		if time.Since(lastSync) < 24*time.Hour {
			nextSyncTime := lastSync.Add(24 * time.Hour)
			config.ErrorStatus(fmt.Sprintf("you can only sync once per 24 hours - next sync available at %s", nextSyncTime.Format(time.RFC3339)), http.StatusTooManyRequests, w, nil)
			return
		}
	}

	var req models.SyncFollowersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}

	if len(req.Platforms) == 0 {
		config.ErrorStatus("at least one platform is required", http.StatusBadRequest, w, nil)
		return
	}

	// Validate platforms match existing platforms
	existingPlatforms := make(map[string]int) // platform type -> index
	for i, p := range creator.Platforms {
		existingPlatforms[p.Type] = i
	}

	// Update follower counts for existing platforms
	updatedPlatforms := make([]models.ContentCreatorPlatform, len(creator.Platforms))
	copy(updatedPlatforms, creator.Platforms)

	var maxFollowers int
	var totalFollowers int

	for _, syncPlatform := range req.Platforms {
		if idx, exists := existingPlatforms[syncPlatform.Type]; exists {
			updatedPlatforms[idx].FollowerCount = syncPlatform.FollowerCount
		}
	}

	// Calculate max and total followers
	for _, p := range updatedPlatforms {
		totalFollowers += p.FollowerCount
		if p.FollowerCount > maxFollowers {
			maxFollowers = p.FollowerCount
		}
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	// Create a snapshot for history
	snapshot := models.ContentCreatorFollowerSnapshot{
		ID:               primitive.NewObjectID(),
		ContentCreatorID: creator.ID,
		Platforms:        updatedPlatforms,
		TotalFollowers:   totalFollowers,
		MaxFollowers:     maxFollowers,
		Source:           "manual",
		RecordedAt:       now,
		RecordedBy:       &userObjID,
	}
	_, err = cc.SnapDB.InsertOne(ctx, snapshot)
	if err != nil {
		zap.S().Warnw("failed to create follower snapshot", "error", err, "creatorId", creator.ID.Hex())
		// Don't fail the request if snapshot creation fails
	}

	// Build update
	updateFields := bson.M{
		"platforms":    updatedPlatforms,
		"lastSyncedAt": now,
		"updatedAt":    now,
	}

	// Check if below threshold and handle grace period
	followerThreshold := 500
	statusChanged := false
	var responseMessage string

	if maxFollowers < followerThreshold {
		// Below threshold
		if creator.GracePeriodStartedAt == nil {
			// Start grace period
			gracePeriodEnd := primitive.NewDateTimeFromTime(time.Now().Add(30 * 24 * time.Hour))
			updateFields["gracePeriodStartedAt"] = now
			updateFields["gracePeriodEndsAt"] = gracePeriodEnd
			updateFields["status"] = "warned"
			updateFields["warningReason"] = "low_followers"
			updateFields["warningMessage"] = fmt.Sprintf("Your highest follower count (%d) is below our minimum requirement of %d. You have 30 days to increase your followers or your creator account will be removed.", maxFollowers, followerThreshold)
			statusChanged = true
			responseMessage = fmt.Sprintf("Warning: Your follower count (%d) is below the minimum requirement of %d. You have 30 days to resolve this.", maxFollowers, followerThreshold)

			// Send low follower warning email
			go cc.sendLowFollowerWarningEmail(ctx, *creator.UserID, creator.DisplayName, maxFollowers, followerThreshold, 30)
		} else {
			responseMessage = fmt.Sprintf("Followers updated. Note: Your follower count (%d) is still below the minimum requirement of %d.", maxFollowers, followerThreshold)
		}
	} else {
		// Above threshold
		if creator.GracePeriodStartedAt != nil {
			// Clear grace period - they've recovered
			updateFields["gracePeriodStartedAt"] = nil
			updateFields["gracePeriodEndsAt"] = nil
			updateFields["gracePeriodNotifiedAt"] = nil
			if creator.Status == "warned" && creator.WarningReason == "low_followers" {
				updateFields["status"] = "active"
				updateFields["warningReason"] = ""
				updateFields["warningMessage"] = ""
				updateFields["warnedAt"] = nil
				statusChanged = true
			}
			responseMessage = "Congratulations! Your follower count is now above the minimum requirement. Your account is in good standing."

			// Send recovery email
			go cc.sendGracePeriodRecoveryEmail(ctx, *creator.UserID, creator.DisplayName, maxFollowers)
		} else {
			responseMessage = "Followers updated successfully."
		}
	}

	// Apply update
	err = cc.CCDB.UpdateOne(ctx, bson.M{"_id": creator.ID}, bson.M{"$set": updateFields})
	if err != nil {
		config.ErrorStatus("failed to update follower counts", http.StatusInternalServerError, w, err)
		return
	}

	zap.S().Infow("content creator synced followers",
		"creatorId", creator.ID.Hex(),
		"userId", userObjID.Hex(),
		"maxFollowers", maxFollowers,
		"totalFollowers", totalFollowers,
		"statusChanged", statusChanged,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"message":        responseMessage,
		"maxFollowers":   maxFollowers,
		"totalFollowers": totalFollowers,
		"platforms":      updatedPlatforms,
		"lastSyncedAt":   now.Time().Format(time.RFC3339),
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

	// For approved applications, look up the creator status and admin names
	type ApplicationWithExtras struct {
		models.ContentCreatorApplication
		CreatorStatus       string `json:"creatorStatus,omitempty"`
		FirstApprovalByName string `json:"firstApprovalByName,omitempty"`
		ReviewedByName      string `json:"reviewedByName,omitempty"`
	}

	// Helper to get admin name (firstName + lastName from admin_users collection)
	getAdminName := func(adminID *primitive.ObjectID) string {
		if adminID == nil {
			return ""
		}
		// Look up from admin_users collection
		admin, err := cc.AdminDB.FindOne(ctx, bson.M{"_id": *adminID})
		if err == nil && admin != nil {
			if admin.FirstName != "" || admin.LastName != "" {
				name := strings.TrimSpace(admin.FirstName + " " + admin.LastName)
				if name != "" {
					return name
				}
			}
			// Fallback to email prefix
			if admin.Email != "" {
				return strings.Split(admin.Email, "@")[0]
			}
		}
		return ""
	}

	enrichedApps := make([]ApplicationWithExtras, len(applications))
	for i, app := range applications {
		enrichedApps[i] = ApplicationWithExtras{
			ContentCreatorApplication: app,
		}
		// Look up first approval admin name
		if app.FirstApprovalBy != nil {
			enrichedApps[i].FirstApprovalByName = getAdminName(app.FirstApprovalBy)
		}
		// Look up second approval (reviewedBy) admin name
		if app.ReviewedBy != nil {
			enrichedApps[i].ReviewedByName = getAdminName(app.ReviewedBy)
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

	// Build response with optional creator status and admin names
	response := struct {
		*models.ContentCreatorApplication
		CreatorStatus       string `json:"creatorStatus,omitempty"`
		FirstApprovalByName string `json:"firstApprovalByName,omitempty"`
		ReviewedByName      string `json:"reviewedByName,omitempty"`
	}{
		ContentCreatorApplication: application,
	}

	// Helper to get admin name (firstName + lastName from admin_users collection)
	getAdminName := func(adminID *primitive.ObjectID) string {
		if adminID == nil {
			return ""
		}
		// Look up from admin_users collection
		admin, err := cc.AdminDB.FindOne(ctx, bson.M{"_id": *adminID})
		if err == nil && admin != nil {
			if admin.FirstName != "" || admin.LastName != "" {
				name := strings.TrimSpace(admin.FirstName + " " + admin.LastName)
				if name != "" {
					return name
				}
			}
			// Fallback to email prefix
			if admin.Email != "" {
				return strings.Split(admin.Email, "@")[0]
			}
		}
		return ""
	}

	// Look up admin names
	if application.FirstApprovalBy != nil {
		response.FirstApprovalByName = getAdminName(application.FirstApprovalBy)
	}
	if application.ReviewedBy != nil {
		response.ReviewedByName = getAdminName(application.ReviewedBy)
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

	// Find all active entitlements before revoking (to update subscriptions)
	entitlementsCursor, _ := cc.EntDB.Find(ctx, bson.M{"contentCreatorId": creatorObjID, "active": true}, nil)
	var entitlements []models.ContentCreatorEntitlement
	entitlementsCursor.All(ctx, &entitlements)

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

	// Deactivate subscriptions for revoked entitlements
	// Only deactivate if the subscription is "base" or "free" (from the program)
	// Don't touch higher-tier paid subscriptions
	subscriptionDeactivate := bson.M{
		"$set": bson.M{
			"community.subscription.active":    false,
			"community.subscription.updatedAt": now,
		},
	}
	userSubscriptionDeactivate := bson.M{
		"$set": bson.M{
			"user.subscription.active":    false,
			"user.subscription.updatedAt": now,
		},
	}
	for _, ent := range entitlements {
		if ent.TargetType == "community" {
			// Only deactivate if plan is "base" or "free" (from program)
			cc.CDB.UpdateOne(ctx, bson.M{
				"_id": ent.TargetID,
				"community.subscription.plan": bson.M{"$in": []string{"base", "free", ""}},
			}, subscriptionDeactivate)
		} else if ent.TargetType == "user" {
			// Only deactivate if plan is "base" or "free" (from program)
			cc.UDB.UpdateOne(ctx, bson.M{
				"_id": ent.TargetID,
				"user.subscription.plan": bson.M{"$in": []string{"base", "free", ""}},
			}, userSubscriptionDeactivate)
		}
	}

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

// AdminGetGracePeriodCreatorsHandler returns all creators currently in grace period
// GET /api/v1/admin/content-creators/grace-period
func (cc ContentCreator) AdminGetGracePeriodCreatorsHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	// Find creators with active grace periods (status = warned and gracePeriodEndsAt is set)
	filter := bson.M{
		"status":            "warned",
		"gracePeriodEndsAt": bson.M{"$ne": nil},
	}

	totalCount, err := cc.CCDB.CountDocuments(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to count creators in grace period", http.StatusInternalServerError, w, err)
		return
	}

	skip := int64((page - 1) * limit)
	findOptions := options.Find().
		SetSkip(skip).
		SetLimit(int64(limit)).
		SetSort(bson.D{{Key: "gracePeriodEndsAt", Value: 1}}) // Sort by expiration date (soonest first)

	cursor, err := cc.CCDB.Find(ctx, filter, findOptions)
	if err != nil {
		config.ErrorStatus("failed to fetch creators in grace period", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	var creators []models.ContentCreator
	if err := cursor.All(ctx, &creators); err != nil {
		config.ErrorStatus("failed to decode creators", http.StatusInternalServerError, w, err)
		return
	}

	// Build response with grace period details
	type GracePeriodCreatorResponse struct {
		ID                   primitive.ObjectID              `json:"_id"`
		DisplayName          string                          `json:"displayName"`
		Slug                 string                          `json:"slug"`
		Status               string                          `json:"status"`
		Platforms            []models.ContentCreatorPlatform `json:"platforms"`
		MaxFollowers         int                             `json:"maxFollowers"`
		GracePeriodStartedAt *primitive.DateTime             `json:"gracePeriodStartedAt"`
		GracePeriodEndsAt    *primitive.DateTime             `json:"gracePeriodEndsAt"`
		DaysRemaining        int                             `json:"daysRemaining"`
		LastSyncedAt         *primitive.DateTime             `json:"lastSyncedAt,omitempty"`
	}

	var responseCreators []GracePeriodCreatorResponse
	for _, creator := range creators {
		// Calculate max followers
		maxFollowers := 0
		for _, p := range creator.Platforms {
			if p.FollowerCount > maxFollowers {
				maxFollowers = p.FollowerCount
			}
		}

		// Calculate days remaining
		daysRemaining := 0
		if creator.GracePeriodEndsAt != nil {
			remaining := creator.GracePeriodEndsAt.Time().Sub(time.Now())
			daysRemaining = int(remaining.Hours() / 24)
			if daysRemaining < 0 {
				daysRemaining = 0
			}
		}

		responseCreators = append(responseCreators, GracePeriodCreatorResponse{
			ID:                   creator.ID,
			DisplayName:          creator.DisplayName,
			Slug:                 creator.Slug,
			Status:               creator.Status,
			Platforms:            creator.Platforms,
			MaxFollowers:         maxFollowers,
			GracePeriodStartedAt: creator.GracePeriodStartedAt,
			GracePeriodEndsAt:    creator.GracePeriodEndsAt,
			DaysRemaining:        daysRemaining,
			LastSyncedAt:         creator.LastSyncedAt,
		})
	}

	totalPages := (int(totalCount) + limit - 1) / limit

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"creators": responseCreators,
		"pagination": map[string]interface{}{
			"currentPage": page,
			"totalPages":  totalPages,
			"totalItems":  totalCount,
			"hasNextPage": page < totalPages,
			"hasPrevPage": page > 1,
		},
	})
}

// AdminTriggerSyncAllHandler triggers a manual sync check for all creators
// POST /api/v1/admin/content-creators/sync-all
func (cc ContentCreator) AdminTriggerSyncAllHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	zap.S().Info("Admin triggered manual sync-all for content creators")

	// Find all active creators
	filter := bson.M{
		"status": bson.M{"$in": []string{"active", "warned"}},
	}

	cursor, err := cc.CCDB.Find(ctx, filter)
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

	processedCount := 0
	lowFollowerCount := 0
	recoveredCount := 0
	followerThreshold := 500

	for _, creator := range creators {
		// Calculate max followers
		maxFollowers := 0
		for _, p := range creator.Platforms {
			if p.FollowerCount > maxFollowers {
				maxFollowers = p.FollowerCount
			}
		}

		now := primitive.NewDateTimeFromTime(time.Now())

		if maxFollowers < followerThreshold {
			// Below threshold
			if creator.GracePeriodStartedAt == nil {
				// Start grace period
				gracePeriodEnd := primitive.NewDateTimeFromTime(time.Now().Add(30 * 24 * time.Hour))
				update := bson.M{
					"$set": bson.M{
						"status":               "warned",
						"gracePeriodStartedAt": now,
						"gracePeriodEndsAt":    gracePeriodEnd,
						"warningReason":        "low_followers",
						"warningMessage":       fmt.Sprintf("Your highest follower count (%d) is below our minimum requirement of %d. You have 30 days to increase your followers.", maxFollowers, followerThreshold),
						"warnedAt":             now,
						"updatedAt":            now,
					},
				}
				err := cc.CCDB.UpdateOne(ctx, bson.M{"_id": creator.ID}, update)
				if err != nil {
					zap.S().Warnw("failed to start grace period", "error", err, "creatorId", creator.ID.Hex())
					continue
				}

				// Send warning email
				if creator.UserID != nil {
					go cc.sendLowFollowerWarningEmail(ctx, *creator.UserID, creator.DisplayName, maxFollowers, followerThreshold, 30)
				}
				lowFollowerCount++
			}
		} else {
			// Above threshold - check if they were in grace period
			if creator.GracePeriodStartedAt != nil && creator.Status == "warned" {
				// Clear grace period - they've recovered
				update := bson.M{
					"$set": bson.M{
						"status":                "active",
						"gracePeriodStartedAt":  nil,
						"gracePeriodEndsAt":     nil,
						"gracePeriodNotifiedAt": nil,
						"warningReason":         "",
						"warningMessage":        "",
						"warnedAt":              nil,
						"updatedAt":             now,
					},
				}
				err := cc.CCDB.UpdateOne(ctx, bson.M{"_id": creator.ID}, update)
				if err != nil {
					zap.S().Warnw("failed to clear grace period", "error", err, "creatorId", creator.ID.Hex())
					continue
				}

				// Send recovery email
				if creator.UserID != nil {
					go cc.sendGracePeriodRecoveryEmail(ctx, *creator.UserID, creator.DisplayName, maxFollowers)
				}
				recoveredCount++
			}
		}
		processedCount++
	}

	zap.S().Infow("Admin sync-all complete",
		"processedCount", processedCount,
		"lowFollowerCount", lowFollowerCount,
		"recoveredCount", recoveredCount,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":          true,
		"message":          "Sync completed",
		"processedCount":   processedCount,
		"lowFollowerCount": lowFollowerCount,
		"recoveredCount":   recoveredCount,
	})
}
