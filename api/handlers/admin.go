package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	templates "github.com/linesmerrill/police-cad-api/templates/html"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type adminLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type adminLoginResponse struct {
	Token string `json:"token"`
	Admin struct {
		ID    string   `json:"id"`
		Email string   `json:"email"`
		Roles []string `json:"roles"`
	} `json:"admin"`
}

// Admin represents the admin handler
type Admin struct {
	ADB databases.AdminDatabase
	RDB databases.AdminResetDatabase
	UDB databases.UserDatabase
	CDB databases.CommunityDatabase
	AADB databases.AdminActivityDatabase
	PVDB databases.PendingVerificationDatabase
}

// checkAdminPermissions validates if the current user has sufficient permissions
func checkAdminPermissions(currentUser map[string]interface{}) error {
	// Extract roles from currentUser
	rolesInterface, exists := currentUser["roles"]
	if !exists {
		return errors.New("user roles not provided")
	}
	
	// Convert to string slice
	var roles []string
	switch v := rolesInterface.(type) {
	case []string:
		roles = v
	case []interface{}:
		for _, role := range v {
			if str, ok := role.(string); ok {
				roles = append(roles, str)
			}
		}
	default:
		return errors.New("invalid roles format")
	}
	
	// Check if user has owner role
	hasOwnerRole := false
	for _, role := range roles {
		if role == "owner" {
			hasOwnerRole = true
			break
		}
	}
	
	if !hasOwnerRole {
		return errors.New("insufficient permissions to perform admin operations")
	}
	
	return nil
}

// AdminLogoutHandler handles admin logout and tracks the activity
func (h Admin) AdminLogoutHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract admin ID from JWT token or request body
	var req struct {
		AdminID string `json:"adminId"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request body",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Validate admin ID format
	objectID, err := primitive.ObjectIDFromHex(req.AdminID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid admin ID format",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Track admin logout activity
	h.trackAdminLogout(objectID, r)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Admin logout tracked successfully",
	})
}

// AdminLoginHandler handles admin login via email/password and returns a JWT
func (h Admin) AdminLoginHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req adminLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" || req.Password == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "email and password required"})
		return
	}

	admin, err := h.ADB.FindOne(r.Context(), bson.M{"email": email, "active": true})
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid credentials"})
		return
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(req.Password)); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid credentials",
			Code:    "INVALID_CREDENTIALS",
		})
		return
	}

	jwtSecret := []byte(os.Getenv("JWT_SECRET"))
	if len(jwtSecret) == 0 {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "server misconfigured"})
		return
	}

	claims := jwt.MapClaims{
		"sub":   admin.ID.Hex(),
		"email": admin.Email,
		"roles": admin.Roles,
		"scope": "admin",
		"typ":   "access",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(24 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(jwtSecret)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "token generation failed"})
		return
	}

	var resp adminLoginResponse
	resp.Token = signed
	resp.Admin.ID = admin.ID.Hex()
	resp.Admin.Email = admin.Email
	resp.Admin.Roles = admin.Roles

	// Track admin login activity
	h.trackAdminLogin(admin.ID, r)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

type forgotRequest struct {
	Email string `json:"email"`
}

// AdminForgotPasswordHandler sends a password reset email if the admin exists (no-op otherwise)
func (h Admin) AdminForgotPasswordHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req forgotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "email required"})
		return
	}

	admin, err := h.ADB.FindOne(r.Context(), bson.M{"email": email, "active": true})
	if err == nil {
		// Create reset token
		plain, hashHex, genErr := generateResetToken()
		if genErr == nil {
			_, _ = h.RDB.InsertOne(r.Context(), models.AdminPasswordReset{
				AdminID:   admin.ID,
				TokenHash: hashHex,
				ExpiresAt: time.Now().Add(1 * time.Hour),
				CreatedAt: time.Now(),
			})
			_ = sendResetEmail(email, buildResetLink(os.Getenv("PUBLIC_WEB_BASE_URL"), plain))
			
			// Track password reset initiated
			h.trackPasswordResetInitiated(admin.ID, r)
		}
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "If that admin email exists, a reset link has been sent."})
}

type resetRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// AdminResetPasswordHandler resets the admin password with a valid token
func (h Admin) AdminResetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req resetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "invalid request",
		})
		return
	}

	token := strings.TrimSpace(req.Token)
	password := req.Password
	if token == "" || password == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "token and password required",
		})
		return
	}

	hashHex := hashToken(token)
	reset, err := h.RDB.FindOne(r.Context(), bson.M{
		"tokenHash": hashHex,
		"usedAt":    bson.M{"$exists": false},
		"expiresAt": bson.M{"$gt": time.Now()},
	})
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "invalid or expired token",
		})
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "could not update password",
		})
		return
	}

	// Update admin password
	_, err = h.ADB.UpdateOne(r.Context(), bson.M{"_id": reset.AdminID}, bson.M{"$set": bson.M{"password": string(newHash)}})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "could not update password",
		})
		return
	}
	// Mark token used
	_, _ = h.RDB.UpdateOne(r.Context(), bson.M{"_id": reset.ID}, bson.M{"$set": bson.M{"usedAt": time.Now()}})

	// Track password reset completed
	h.trackPasswordResetCompleted(reset.AdminID, r)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Password updated successfully",
	})
}

// helpers
func generateResetToken() (plain string, hashHex string, err error) {
	b := make([]byte, 32)
	_, err = rand.Read(b)
	if err != nil {
		return "", "", err
	}
	pln := hex.EncodeToString(b)
	return pln, hashToken(pln), nil
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func buildResetLink(baseURL, token string) string {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = "https://www.linespolice-cad.com"
	}
	return base + "/admin/reset-password?token=" + token
}

func sendResetEmail(toEmail, resetLink string) error {
	from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
	subject := "LPC-APP Admin Password Reset"
	to := mail.NewEmail("", toEmail)
	plain := "Reset your admin password using this link: " + resetLink
	html := templates.RenderAdminPasswordReset(resetLink)
	msg := mail.NewSingleEmail(from, subject, to, plain, html)
	client := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))
	_, err := client.Send(msg)
	return err
}

// Admin console handlers

type userSearchRequest struct {
	Query string `json:"query"`
	Page  int    `json:"page"`  // Page number (0-based)
	Limit int    `json:"limit"` // Records per page
}

type userSearchResponse struct {
	Users      []models.AdminUserResult `json:"users"`
	Pagination map[string]interface{}  `json:"pagination"`
}

// AdminUserSearchHandler searches for users by email or username
func (h Admin) AdminUserSearchHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req userSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "query required"})
		return
	}

	// Set pagination defaults
	page := req.Page
	if page < 0 {
		page = 0 // Default to first page (0-based)
	}
	
	limit := req.Limit
	if limit <= 0 {
		limit = 10 // Default to 10 records per page
	}
	
	// Calculate skip value for MongoDB
	skip := int64(page * limit)
	limit64 := int64(limit)

	// OPTIMIZATION: Use $text search for name/username (much faster than regex)
	// For email, use direct field lookup with collation (has index)
	queryLen := len(query)
	isEmailQuery := strings.Contains(query, "@") // Detect if query looks like an email
	var filter bson.M
	var findOpts *options.FindOptions

	// Use request context with timeout
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	
	var err error

	if isEmailQuery {
		// For email queries, use direct lookup only (fastest - uses user_email_idx index)
		filter = bson.M{"user.email": query} // Direct lookup uses user_email_idx (case-insensitive collation)
		findOpts = &options.FindOptions{
			Skip:  &skip,
			Limit: &limit64,
			Sort:  bson.M{"user.email": 1},
		}
	} else if queryLen >= 3 {
		// Use $text search for name/username queries
		filter = bson.M{
			"$text": bson.M{"$search": query}, // Uses user_search_text_idx for name/username
		}
		findOpts = &options.FindOptions{
			Skip:  &skip,
			Limit: &limit64,
			Sort:  bson.M{"score": bson.M{"$meta": "textScore"}, "user.email": 1},
		}
	} else {
		// For very short queries, use regex but limit aggressively
		filter = bson.M{
			"$or": []bson.M{
				{"user.email": bson.M{"$regex": "^" + query, "$options": "i"}}, // Prefix match
				{"user.name": bson.M{"$regex": "^" + query, "$options": "i"}},
				{"user.username": bson.M{"$regex": "^" + query, "$options": "i"}},
			},
		}
		findOpts = &options.FindOptions{
			Skip:  &skip,
			Limit: &limit64,
			Sort:  bson.M{"user.email": 1},
		}
	}
	
	// Get total count for pagination metadata (skip for very short queries to avoid slow count)
	var totalCount int64
	if isEmailQuery || queryLen >= 3 {
		totalCount, err = h.UDB.CountDocuments(ctx, filter)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "search count failed"})
			return
		}
	} else {
		// For short queries, estimate count based on results (faster)
		totalCount = 0 // Will be estimated from results
	}
	
	// Use existing user database to search with pagination
	cursor, err := h.UDB.Find(ctx, filter, findOpts)
	if err != nil {
		// If text search fails (e.g., index doesn't exist), fallback to regex for queries >= 3 chars (non-email)
		if !isEmailQuery && queryLen >= 3 {
			log.Printf("AdminUserSearchHandler: text search failed, falling back to regex: %v", err)
			// Fallback to regex-based search (no $text search)
			filter = bson.M{
				"$or": []bson.M{
					{"user.email": bson.M{"$regex": query, "$options": "i"}},
					{"user.name": bson.M{"$regex": query, "$options": "i"}},
					{"user.username": bson.M{"$regex": query, "$options": "i"}},
				},
			}
			findOpts = &options.FindOptions{
				Skip:  &skip,
				Limit: &limit64,
				Sort:  bson.M{"user.email": 1},
			}
			// Retry count with new filter
			totalCount, err = h.UDB.CountDocuments(ctx, filter)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "search count failed"})
				return
			}
			// Retry find with new filter
			cursor, err = h.UDB.Find(ctx, filter, findOpts)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "search failed"})
				return
			}
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "search failed"})
			return
		}
	}
	defer cursor.Close(ctx)
	
	// If we skipped count for short queries, estimate from results
	if queryLen < 3 && totalCount == 0 {
		// Estimate: if we got full limit, there might be more
		// This is a rough estimate to avoid slow CountDocuments
		totalCount = limit64 * int64(page + 1) // Rough estimate
	}

	var users []models.User
	if err = cursor.All(ctx, &users); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to decode users"})
		return
	}

	var results []models.AdminUserResult
	for _, user := range users {
		result := models.AdminUserResult{
			ID:            user.ID,
			Email:         user.Details.Email,
			Username:      user.Details.Username,
			IsDeactivated: user.Details.IsDeactivated,
			CreatedAt:     user.Details.CreatedAt,
		}
		results = append(results, result)
	}

	// Create pagination metadata
	pagination := map[string]interface{}{
		"currentPage": page,
		"limit":       limit,
		"totalRecords": totalCount,
		"totalPages":  int((totalCount + int64(limit) - 1) / int64(limit)),
		"hasNextPage": page < int((totalCount + int64(limit) - 1) / int64(limit)) - 1,
		"hasPrevPage": page > 0,
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(userSearchResponse{
		Users:      results,
		Pagination: pagination,
	})
}

type communitySearchRequest struct {
	Query string `json:"query"`
	Page  int    `json:"page"`  // Page number (0-based)
	Limit int    `json:"limit"` // Records per page
}

type communitySearchResponse struct {
	Communities []models.AdminCommunityResult `json:"communities"`
	Pagination map[string]interface{}        `json:"pagination"`
}

// AdminCommunitySearchHandler searches for communities by name
func (h Admin) AdminCommunitySearchHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req communitySearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "query required"})
		return
	}

	// Set pagination defaults
	page := req.Page
	if page < 0 {
		page = 0 // Default to first page (0-based)
	}
	
	limit := req.Limit
	if limit <= 0 {
		limit = 10 // Default to 10 records per page
	}
	
	// Calculate skip value for MongoDB
	skip := int64(page * limit)
	limit64 := int64(limit)

	// Use request context with timeout
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Use regex search for community names (text index may not be available/configured)
	// For queries >= 3 chars, use full regex match, otherwise use prefix match
	queryLen := len(query)
	var filter bson.M
	var findOpts *options.FindOptions
	
	if queryLen >= 3 {
		// Use regex search for queries >= 3 chars
		filter = bson.M{"community.name": bson.M{"$regex": query, "$options": "i"}}
	} else {
		// For short queries, use prefix regex
		filter = bson.M{"community.name": bson.M{"$regex": "^" + query, "$options": "i"}}
	}
	
	findOpts = options.Find().
		SetSkip(skip).
		SetLimit(limit64).
		SetSort(bson.M{"community.name": 1})
	
	// Get total count for pagination metadata
	var totalCount int64
	var err error
	totalCount, err = h.CDB.CountDocuments(ctx, filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "search count failed"})
		return
	}
	
	// Use community database to search with pagination
	cursor, err := h.CDB.Find(ctx, filter, findOpts)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "search failed"})
		return
	}
	defer cursor.Close(ctx)

	var communities []models.Community
	if err = cursor.All(ctx, &communities); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to decode communities"})
		return
	}

	var results []models.AdminCommunityResult
	for _, community := range communities {
		// Get owner info with username
		var ownerInfo *models.OwnerInfo
		if community.Details.OwnerID != "" {
			// Try to find owner by string ID first
			var ownerUser models.User
			err := h.UDB.FindOne(r.Context(), bson.M{"_id": community.Details.OwnerID}).Decode(&ownerUser)
			
			// If that fails, try ObjectID format
			if err != nil {
				if oid, oidErr := primitive.ObjectIDFromHex(community.Details.OwnerID); oidErr == nil {
					err = h.UDB.FindOne(r.Context(), bson.M{"_id": oid}).Decode(&ownerUser)
				}
			}
			
			if err == nil {
				ownerInfo = &models.OwnerInfo{
					ID:       ownerUser.ID,
					Email:    ownerUser.Details.Email,
					Username: ownerUser.Details.Username,
				}
			}
		}

		// Get departments info - just count for search results
		departmentCount := len(community.Details.Departments)

		// Set default visibility if empty
		visibility := community.Details.Visibility
		if visibility == "" {
			visibility = "public" // Default to public if not specified
		}

		result := models.AdminCommunityResult{
			ID:             community.ID.Hex(),
			Name:           community.Details.Name,
			Visibility:     visibility,
			CreatedAt:      community.Details.CreatedAt,
			Owner:          ownerInfo,
			MemberCount:    community.Details.MembersCount,
			DepartmentCount: departmentCount,
		}
		results = append(results, result)
	}

	// Create pagination metadata
	pagination := map[string]interface{}{
		"currentPage": page,
		"limit":       limit,
		"totalRecords": totalCount,
		"totalPages":  int((totalCount + int64(limit) - 1) / int64(limit)),
		"hasNextPage": page < int((totalCount + int64(limit) - 1) / int64(limit)) - 1,
		"hasPrevPage": page > 0,
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(communitySearchResponse{
		Communities: results,
		Pagination:  pagination,
	})
}

type pendingVerificationSearchRequest struct {
	Query string `json:"query"`
	Page  int    `json:"page"`  // Page number (0-based)
	Limit int    `json:"limit"` // Records per page
}

type pendingVerificationSearchResponse struct {
	PendingVerifications []models.AdminPendingVerificationResult `json:"pendingVerifications"`
	Pagination           map[string]interface{}                 `json:"pagination"`
}

// AdminPendingVerificationSearchHandler searches for pending verifications by email
func (h Admin) AdminPendingVerificationSearchHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req pendingVerificationSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "query required"})
		return
	}

	// Set pagination defaults
	page := req.Page
	if page < 0 {
		page = 0 // Default to first page (0-based)
	}
	
	limit := req.Limit
	if limit <= 0 {
		limit = 10 // Default to 10 records per page
	}
	
	// Calculate skip value for MongoDB
	skip := int64(page * limit)
	limit64 := int64(limit)

	// Search by email (case-insensitive)
	filter := bson.M{
		"email": bson.M{"$regex": query, "$options": "i"},
	}
	
	// Get total count for pagination metadata
	totalCount, err := h.PVDB.CountDocuments(r.Context(), filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "search count failed"})
		return
	}
	
	// Use pending verification database to search with pagination
	cursor, err := h.PVDB.Find(r.Context(), filter, &options.FindOptions{
		Skip:  &skip,
		Limit: &limit64,
		Sort:  bson.M{"email": 1}, // Sort by email for consistent results
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "search failed"})
		return
	}
	defer cursor.Close(r.Context())

	var pendingVerifications []models.PendingVerification
	if err = cursor.All(r.Context(), &pendingVerifications); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to decode pending verifications"})
		return
	}

	var results []models.AdminPendingVerificationResult
	for _, pv := range pendingVerifications {
		result := models.AdminPendingVerificationResult{
			ID:        pv.ID,
			Email:     pv.Email,
			Code:      pv.Code,
			Attempts:  pv.Attempts,
			CreatedAt: pv.CreatedAt,
		}
		results = append(results, result)
	}

	// Create pagination metadata
	pagination := map[string]interface{}{
		"currentPage": page,
		"limit":       limit,
		"totalRecords": totalCount,
		"totalPages":  int((totalCount + int64(limit) - 1) / int64(limit)),
		"hasNextPage": page < int((totalCount + int64(limit) - 1) / int64(limit)) - 1,
		"hasPrevPage": page > 0,
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(pendingVerificationSearchResponse{
		PendingVerifications: results,
		Pagination:          pagination,
	})
}

type userDetailsResponse struct {
	User models.AdminUserDetails `json:"user"`
}

// AdminUserDetailsHandler gets detailed user information
func (h Admin) AdminUserDetailsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract user ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 5 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid user ID"})
		return
	}
	userID := pathParts[len(pathParts)-1]

	// Try string ID first
	var user models.User
	err := h.UDB.FindOne(r.Context(), bson.M{"_id": userID}).Decode(&user)
	if err != nil {
		// If that fails, try ObjectID form
		if oid, oidErr := primitive.ObjectIDFromHex(userID); oidErr == nil {
			var userObj struct {
				ID      primitive.ObjectID `bson:"_id"`
				Details models.UserDetails `bson:"user"`
				Version int32             `bson:"__v"`
			}
			if err2 := h.UDB.FindOne(r.Context(), bson.M{"_id": oid}).Decode(&userObj); err2 == nil {
				user = models.User{ID: userObj.ID.Hex(), Details: userObj.Details, Version: userObj.Version}
				// proceed
			} else {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "user not found"})
				return
			}
		} else {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "user not found"})
			return
		}
	}

	// Get user communities with role and status information
	var userCommunities []models.AdminUserCommunity
	var approvedCommunitiesCount int
	
	// Get the user's communities from the user document
	if len(user.Details.Communities) > 0 {
		for _, userComm := range user.Details.Communities {
			// Convert community ID string to ObjectID
			communityObjectID, err := primitive.ObjectIDFromHex(userComm.CommunityID)
			if err != nil {
				continue
			}
			
			// Query the community collection for this community
			community, err := h.CDB.FindOne(r.Context(), bson.M{"_id": communityObjectID})
			if err != nil {
				continue
			}
			
			// Get owner details (we'll use this later if needed)
			var ownerUser models.User
			h.UDB.FindOne(r.Context(), bson.M{"_id": community.Details.OwnerID}).Decode(&ownerUser)
			
			// Determine role (owner or member)
			role := "Member"
			if community.Details.OwnerID == user.ID {
				role = "Owner"
			}
			
			// Get department info if available
			department := ""
			if len(community.Details.Departments) > 0 {
				department = community.Details.Departments[0].Name
			}
			
			// Only count approved communities
			if userComm.Status == "approved" {
				approvedCommunitiesCount++
			}
			
			userCommunities = append(userCommunities, models.AdminUserCommunity{
				ID:         community.ID.Hex(),
				Name:       community.Details.Name,
				Status:     userComm.Status,
				Role:       role,
				Department: department,
				JoinedAt:   community.Details.CreatedAt,
			})
		}
	}

	// Get password reset status
	var resetPasswordToken string
	var resetPasswordExpires interface{}
	
	// Try to get password reset fields from user document
	if user.Details.ResetPasswordToken != "" {
		resetPasswordToken = user.Details.ResetPasswordToken
		resetPasswordExpires = user.Details.ResetPasswordExpires
	}

	details := models.AdminUserDetails{
		ID:                   user.ID,
		Email:                user.Details.Email,
		Username:             user.Details.Username,
		IsDeactivated:        user.Details.IsDeactivated,
		CreatedAt:            user.Details.CreatedAt,
		Communities:          userCommunities,
		CommunitiesCount:     approvedCommunitiesCount,
		// Add password reset fields for frontend
		ResetPasswordToken:   resetPasswordToken,
		ResetPasswordExpires: resetPasswordExpires,
	}



	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(userDetailsResponse{User: details})
}

type communityDetailsResponse struct {
	Community models.AdminCommunityDetails `json:"community"`
}

// AdminCommunityDetailsHandler gets detailed community information
func (h Admin) AdminCommunityDetailsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract community ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 5 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid community ID"})
		return
	}
	communityID := pathParts[len(pathParts)-1]

	// Validate ObjectID
	objectID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid community ID format"})
		return
	}

	community, err := h.CDB.FindOne(r.Context(), bson.M{"_id": objectID})
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "community not found"})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to fetch community"})
		}
		return
	}

	// Get owner info with username
	var ownerInfo *models.OwnerInfo
	if community.Details.OwnerID != "" {
		// Try to find owner by string ID first
		var ownerUser models.User
		err := h.UDB.FindOne(r.Context(), bson.M{"_id": community.Details.OwnerID}).Decode(&ownerUser)
		
		// If that fails, try ObjectID format
		if err != nil {
			if oid, oidErr := primitive.ObjectIDFromHex(community.Details.OwnerID); oidErr == nil {
				err = h.UDB.FindOne(r.Context(), bson.M{"_id": oid}).Decode(&ownerUser)
			}
		}
		
		if err == nil {
			ownerInfo = &models.OwnerInfo{
				ID:       ownerUser.ID,
				Email:    ownerUser.Details.Email,
				Username: ownerUser.Details.Username,
			}
		}
	}

	// Get departments information
	var depts []models.CommunityDept
	if len(community.Details.Departments) > 0 {
		for _, dept := range community.Details.Departments {
			// Count members in this department
			memberCount := 0
			if dept.Members != nil {
				memberCount = len(dept.Members)
			}
			
			depts = append(depts, models.CommunityDept{
				ID:          dept.ID.Hex(),
				Name:        dept.Name,
				MemberCount: memberCount,
			})
		}
	}

	// Set default visibility if empty
	visibility := community.Details.Visibility
	if visibility == "" {
		visibility = "public" // Default to public if not specified
	}

	// Get subscription information
	var subscription *models.CommunitySubscription
	if community.Details.Subscription.Active {
		subscription = &models.CommunitySubscription{
			Active:           community.Details.Subscription.Active,
			CreatedAt:        community.Details.Subscription.CreatedAt,
			DurationMonths:   community.Details.Subscription.DurationMonths,
			ExpirationDate:   community.Details.Subscription.ExpirationDate,
			ID:               community.Details.Subscription.ID,
			IsAnnual:         community.Details.Subscription.IsAnnual,
			Plan:             community.Details.Subscription.Plan,
			PurchaseDate:     community.Details.Subscription.PurchaseDate,
			UpdatedAt:        community.Details.Subscription.UpdatedAt,
		}
	}

	details := models.AdminCommunityDetails{
		ID:             community.ID.Hex(),
		Name:           community.Details.Name,
		Visibility:     visibility,
		CreatedAt:      community.Details.CreatedAt,
		Owner:          ownerInfo,
		MemberCount:    community.Details.MembersCount,
		Departments:    depts,
		DepartmentCount: len(community.Details.Departments),
		Subscription:   subscription,
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(communityDetailsResponse{Community: details})
}

// AdminUserResetPasswordHandler sends a password reset email for a regular user
func (h Admin) AdminUserResetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract user ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 6 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid user ID"})
		return
	}
	userID := pathParts[len(pathParts)-2]

	// Check if user exists (string ID first, then ObjectID)
	var user models.User
	err := h.UDB.FindOne(r.Context(), bson.M{"_id": userID}).Decode(&user)
	if err != nil {
		if oid, oidErr := primitive.ObjectIDFromHex(userID); oidErr == nil {
			var userObj struct {
				ID      primitive.ObjectID `bson:"_id"`
				Details models.UserDetails `bson:"user"`
				Version int32             `bson:"__v"`
			}
			if err2 := h.UDB.FindOne(r.Context(), bson.M{"_id": oid}).Decode(&userObj); err2 == nil {
				user = models.User{ID: userObj.ID.Hex(), Details: userObj.Details, Version: userObj.Version}
			} else {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "user not found"})
				return
			}
		} else {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "user not found"})
			return
		}
	}

	// Generate reset token and expiration
	resetToken := generateUserResetToken()
	expiresAt := time.Now().Add(24 * time.Hour)
	
	// Hash the token for storage
	hashedToken := hashToken(resetToken)
	
	// Update user with reset token and expiration (using regular user fields)
	filter := bson.M{"_id": userID}
	if oid, oidErr := primitive.ObjectIDFromHex(userID); oidErr == nil {
		filter = bson.M{"$or": []bson.M{{"_id": userID}, {"_id": oid}}}
	}
	
	_, err = h.UDB.UpdateOne(r.Context(), filter, bson.M{
		"$set": bson.M{
			"user.resetPasswordToken":   hashedToken,
			"user.resetPasswordExpires": expiresAt,
			"updatedAt":                 time.Now(),
		},
	})
	if err != nil {
		log.Printf("Failed to update user with reset token: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to create reset token"})
		return
	}
	
	// Build reset link for regular user (not admin)
	resetLink := buildUserResetLink(resetToken)
	
	// Send email to user using regular user template
	err = sendUserResetEmail(user.Details.Email, resetLink)
	if err != nil {
		// Log the error but don't expose it to the client
		log.Printf("Failed to send reset email to %s: %v", user.Details.Email, err)
		// Still return success since the token was created
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "Password reset email sent successfully"})
}

// AdminUserReactivateHandler handles reactivating a deactivated user account
func (h Admin) AdminUserReactivateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract user ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 6 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid user ID",
			Code:    "VALIDATION_ERROR",
		})
		return
	}
	userID := pathParts[len(pathParts)-2]

	// Parse request body
	var req struct {
		CurrentUser struct {
			Email string   `json:"email"`
			Roles []string `json:"roles"`
		} `json:"currentUser"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid request body",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Validate current user permissions
	hasPermission := false
	for _, role := range req.CurrentUser.Roles {
		if role == "admin" || role == "owner" {
			hasPermission = true
			break
		}
	}

	if !hasPermission {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Insufficient permissions to reactivate user accounts",
			Code:    "PERMISSION_DENIED",
		})
		return
	}

	// Try string ID first, then ObjectID
	var user models.User
	err := h.UDB.FindOne(r.Context(), bson.M{"_id": userID}).Decode(&user)
	if err != nil {
		if oid, oidErr := primitive.ObjectIDFromHex(userID); oidErr == nil {
			var userObj struct {
				ID      primitive.ObjectID `bson:"_id"`
				Details models.UserDetails `bson:"user"`
				Version int32             `bson:"__v"`
			}
			if err2 := h.UDB.FindOne(r.Context(), bson.M{"_id": oid}).Decode(&userObj); err2 == nil {
				user = models.User{ID: userObj.ID.Hex(), Details: userObj.Details, Version: userObj.Version}
			} else {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(models.ErrorResponse{
					Success: false,
					Error:   "User not found",
					Code:    "NOT_FOUND",
				})
				return
			}
		} else {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "User not found",
				Code:    "NOT_FOUND",
			})
			return
		}
	}

	// Check if user is actually deactivated
	if !user.Details.IsDeactivated {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "User account is already active",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Reactivate the user
	filter := bson.M{"_id": userID}
	if oid, oidErr := primitive.ObjectIDFromHex(userID); oidErr == nil {
		filter = bson.M{"$or": []bson.M{{"_id": userID}, {"_id": oid}}}
	}

	_, err = h.UDB.UpdateOne(r.Context(), filter, bson.M{
		"$set": bson.M{
			"user.isDeactivated": false,
			"updatedAt":          time.Now(),
		},
	})

	if err != nil {
		log.Printf("Failed to reactivate user: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to reactivate user",
			Code:    "DATABASE_ERROR",
		})
		return
	}

	// Track the action - use account_reactivated type
	// Extract admin ID from current user email
	if req.CurrentUser.Email != "" {
		admin, err := h.ADB.FindOne(r.Context(), bson.M{"email": req.CurrentUser.Email})
		if err == nil {
			h.trackAdminAction(admin.ID, "account_reactivated", userID, "user", fmt.Sprintf("Reactivated user account: %s", user.Details.Email), r)
		}
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "User account reactivated successfully",
		"user": map[string]interface{}{
			"id":            user.ID,
			"email":         user.Details.Email,
			"username":      user.Details.Username,
			"isDeactivated": false,
			"updatedAt":     time.Now(),
		},
	})
}

type tempPasswordResponse struct {
	TempPassword string `json:"tempPassword"`
}

// AdminUserTempPasswordHandler generates a temporary password for a user
func (h Admin) AdminUserTempPasswordHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract user ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 6 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid user ID",
			Code:    "VALIDATION_ERROR",
		})
		return
	}
	userID := pathParts[len(pathParts)-1]

	// Validate ObjectID
	objectID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid user ID format",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Get user from database
	var user models.User
	err = h.UDB.FindOne(r.Context(), bson.M{"_id": objectID}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "User not found",
				Code:    "NOT_FOUND",
			})
			return
		}
		log.Printf("Admin user temp password error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to fetch user",
			Code:    "DATABASE_ERROR",
		})
		return
	}

	// Generate temporary password
	tempPassword := generateTempPassword()

	// Hash the temporary password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(tempPassword), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Admin user temp password hash error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to generate password hash",
			Code:    "DATABASE_ERROR",
		})
		return
	}

	// Update user with temporary password
	_, err = h.UDB.UpdateOne(r.Context(), bson.M{"_id": objectID}, bson.M{
		"$set": bson.M{
			"user.password": string(hashedPassword),
		},
	})
	if err != nil {
		log.Printf("Admin user temp password update error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to update user password",
			Code:    "DATABASE_ERROR",
		})
		return
	}

	// Track the action
	h.trackAdminAction(objectID, "temp_password_created", userID, "user", fmt.Sprintf("Temporary password created for user: %s", user.Details.Email), r)

	// Return temporary password
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Temporary password created successfully",
		"tempPassword": tempPassword,
		"userEmail": user.Details.Email,
	})
}

// Note: AdminInitiateUserResetHandler removed - frontend will use existing /forgot-password route directly
// This simplifies the system by leveraging existing password reset logic instead of duplicating it

// Helper functions for temporary password generation (kept for backward compatibility)

// generateTempPassword generates a readable temporary password
func generateTempPassword() string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 8)
	for i := range b {
		// Use crypto/rand to get a random byte and map it to charset
		randBytes := make([]byte, 1)
		rand.Read(randBytes)
		b[i] = charset[int(randBytes[0])%len(charset)]
	}
	return string(b)
}

// generateUserResetToken generates a secure random token for user password reset
func generateUserResetToken() string {
	// Generate a secure random token for user password reset
	buf := make([]byte, 32)
	_, err := rand.Read(buf)
	if err != nil {
		// Fallback to timestamp-based token if crypto/rand fails
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// buildUserResetLink creates the password reset link for users
func buildUserResetLink(token string) string {
	baseURL := os.Getenv("PUBLIC_WEB_BASE_URL")
	if baseURL == "" {
		baseURL = "https://www.linespolice-cad.com"
	}
	base := strings.TrimRight(baseURL, "/")
	return base + "/reset-password?token=" + token
}

// sendUserResetEmail sends a password reset email to the user
func sendUserResetEmail(toEmail, resetLink string) error {
	from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
	subject := "Password Reset Request"
	to := mail.NewEmail("", toEmail)
	plain := fmt.Sprintf(`Hello,

You have requested a password reset for your account.

Click the following link to reset your password:
%s

This link will expire in 24 hours.

If you did not request this reset, please ignore this email.

Best regards,
Lines Police CAD Team`, resetLink)
	
	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>Password Reset Request</title>
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
    <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
        <h2 style="color: #2c3e50;">Password Reset Request</h2>
        <p>Hello,</p>
        <p>You have requested a password reset for your account.</p>
        <p>Click the following button to reset your password:</p>
        <div style="text-align: center; margin: 30px 0;">
            <a href="%s" style="background-color: #3498db; color: white; padding: 12px 24px; text-decoration: none; border-radius: 5px; display: inline-block;">Reset Password</a>
        </div>
        <p>Or copy and paste this link into your browser:</p>
        <p style="word-break: break-all; color: #3498db;">%s</p>
        <p><strong>This link will expire in 24 hours.</strong></p>
        <p>If you did not request this reset, please ignore this email.</p>
        <hr style="margin: 30px 0; border: none; border-top: 1px solid #eee;">
        <p style="color: #7f8c8d; font-size: 14px;">Best regards,<br>Lines Police CAD Team</p>
    </div>
</body>
</html>`, resetLink, resetLink)
	
	msg := mail.NewSingleEmail(from, subject, to, plain, html)
	client := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))
	_, err := client.Send(msg)
	return err
}

// canCreateAdmins checks if the current user has permission to create admin users
func canCreateAdmins(currentUser *models.AdminUser) bool {
	if currentUser == nil {
		return false
	}
	// Check if user has owner role (either in legacy field or roles array)
	if currentUser.Role == "owner" {
		return true
	}
	if currentUser.Roles != nil {
		for _, role := range currentUser.Roles {
			if role == "owner" {
				return true
			}
		}
	}
	return false
}

// isValidEmail checks if an email address is valid
func isValidEmail(email string) bool {
	// Simple email validation - you might want to use a more robust library
	return strings.Contains(email, "@") && strings.Contains(email, ".")
}

// CreateAdminUserRequest represents the request to create a new admin user with permission check
type CreateAdminUserRequest struct {
	Email       string                 `json:"email"`
	Role        string                 `json:"role"`
	CurrentUser map[string]interface{} `json:"currentUser"`
}

// CreateAdminUserHandler creates a new admin user
func (h Admin) CreateAdminUserHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req CreateAdminUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request body",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Check permissions
	if err := checkAdminPermissions(req.CurrentUser); err != nil {
		response := map[string]interface{}{
			"success": false,
			"error":   err.Error(),
			"code":    "INSUFFICIENT_PERMISSIONS",
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Normalize email to lowercase
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	// Validate email format
	if !isValidEmail(req.Email) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid email format",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Check if admin user already exists
	existingAdmin, err := h.ADB.FindOne(r.Context(), bson.M{"email": req.Email})
	if err == nil && existingAdmin != nil {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "User with this email already exists",
			"code":    "DUPLICATE_USER",
		})
		return
	}

	// Create new admin user
	adminUser := models.AdminUser{
		Email:     req.Email,
		Role:      req.Role,
		Roles:     []string{req.Role}, // Initialize roles array with the single role
		Active:    true,
		CreatedAt: time.Now(),
		CreatedBy: "System", // Assuming system created for now
	}

	// Insert admin user into database
	result, err := h.ADB.InsertOne(r.Context(), adminUser)
	if err != nil {
		log.Printf("Admin user creation error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to create admin user",
			Code:    "DATABASE_ERROR",
		})
		return
	}

	// Get the inserted ID
	insertedID := result.Decode()
	adminUser.ID = insertedID.(primitive.ObjectID)

	// Track admin user creation
	h.trackAdminAction(adminUser.ID, "admin_created", adminUser.ID.Hex(), "admin", fmt.Sprintf("Created admin user: %s", adminUser.Email), r)

	// Generate password reset token
	resetToken := generateAdminResetToken()
	expiresAt := time.Now().Add(24 * time.Hour)
	
	// Hash the token for storage
	hashedToken := hashToken(resetToken)
	
	// Create password reset record
	resetRecord := models.AdminPasswordReset{
		AdminID:   adminUser.ID,
		TokenHash: hashedToken,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
	
	_, err = h.RDB.InsertOne(r.Context(), resetRecord)
	if err != nil {
		log.Printf("Failed to create reset record: %v", err)
		// Continue anyway since admin user was created
	}

	// Build reset link
	resetLink := buildAdminResetLink(os.Getenv("PUBLIC_WEB_BASE_URL"), resetToken)

	// Send password reset email
	err = sendAdminResetEmail(req.Email, resetLink)
	if err != nil {
		log.Printf("Admin reset email error: %v", err)
		// Don't fail the request, just log the error
		// The admin user was created successfully
	}

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"message":   "Admin user created successfully",
		"resetLink": resetLink,
		"user": map[string]interface{}{
			"id":        adminUser.ID.Hex(),
			"email":     adminUser.Email,
			"role":      adminUser.Role,
			"createdAt": adminUser.CreatedAt,
		},
	})
}

// SendAdminResetEmailHandler sends a password reset email to an admin user
func (h Admin) SendAdminResetEmailHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Parse request body
	var req models.SendAdminResetEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid request body",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Normalize email to lowercase
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	// Validate email format
	if req.Email == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Email address is required",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Find admin user by email
	adminUser, err := h.ADB.FindOne(r.Context(), bson.M{"email": req.Email})
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Admin user not found",
				Code:    "USER_NOT_FOUND",
			})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Failed to find admin user",
				Code:    "DATABASE_ERROR",
			})
		}
		return
	}

	// Generate new reset token
	resetToken := generateAdminResetToken()
	expiresAt := time.Now().Add(24 * time.Hour)
	
	// Hash the token for storage
	hashedToken := hashToken(resetToken)
	
	// Create new password reset record
	resetRecord := models.AdminPasswordReset{
		AdminID:   adminUser.ID,
		TokenHash: hashedToken,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
	
	_, err = h.RDB.InsertOne(r.Context(), resetRecord)
	if err != nil {
		log.Printf("Failed to create reset record: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to create reset token",
			Code:    "DATABASE_ERROR",
		})
		return
	}

	// Build reset link
	resetLink := buildAdminResetLink(os.Getenv("PUBLIC_WEB_BASE_URL"), resetToken)
	
	// Send reset email
	err = sendAdminResetEmail(adminUser.Email, resetLink)
	if err != nil {
		log.Printf("Failed to send reset email: %v", err)
		// Still return success since token was created
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(models.SendAdminResetEmailResponse{
		Success:   true,
		Message:   "Password reset email sent successfully",
		EmailSent: true,
	})
}

// Helper function to generate admin reset token
func generateAdminResetToken() string {
	buf := make([]byte, 32)
	_, err := rand.Read(buf)
	if err != nil {
		// Fallback to timestamp-based token if crypto/rand fails
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// Helper function to build admin reset link
func buildAdminResetLink(baseURL, token string) string {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = "https://www.linespolice-cad.com"
	}
	return base + "/admin/reset-password?token=" + token
}

// Helper function to send admin reset email
func sendAdminResetEmail(toEmail, resetLink string) error {
	from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
	subject := "Admin Password Reset Request"
	to := mail.NewEmail("", toEmail)
	plain := fmt.Sprintf(`Hello,

You have requested a password reset for your admin account.

Click the following link to reset your password:
%s

This link will expire in 24 hours.

If you did not request this reset, please ignore this email.

Best regards,
Lines Police CAD Team`, resetLink)

	html := templates.RenderAdminPasswordReset(resetLink)

	msg := mail.NewSingleEmail(from, subject, to, plain, html)
	client := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))
	_, err := client.Send(msg)
	return err
}

// AdminSearchAdminsRequest represents the request to search for admin users with permission check
type AdminSearchAdminsRequest struct {
	Query       string                 `json:"query"`
	CurrentUser map[string]interface{} `json:"currentUser"`
}

// AdminSearchAdminsHandler searches for admin users
func (h Admin) AdminSearchAdminsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req AdminSearchAdminsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request body",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Check permissions
	if err := checkAdminPermissions(req.CurrentUser); err != nil {
		response := map[string]interface{}{
			"success": false,
			"error":   err.Error(),
			"code":    "INSUFFICIENT_PERMISSIONS",
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Validate query
	if req.Query == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Search query is required",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Build search filter
	filter := bson.M{
		"$or": []bson.M{
			{"email": bson.M{"$regex": req.Query, "$options": "i"}},
			{"roles": bson.M{"$regex": req.Query, "$options": "i"}},
		},
	}

	// Find admin users
	cursor, err := h.ADB.Find(r.Context(), filter, nil)
	if err != nil {
		log.Printf("Admin search error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to search admins",
			"code":    "DATABASE_ERROR",
		})
		return
	}
	defer cursor.Close(r.Context())

	var admins []models.AdminUser
	if err = cursor.All(r.Context(), &admins); err != nil {
		log.Printf("Admin search decode error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to decode admin results",
			"code":    "DATABASE_ERROR",
		})
		return
	}

	// Ensure all admin objects have consistent role field population for backward compatibility
	for i := range admins {
		if admins[i].Role == "" && len(admins[i].Roles) > 0 {
			admins[i].Role = admins[i].Roles[0]
		} else if admins[i].Role != "" && len(admins[i].Roles) == 0 {
			admins[i].Roles = []string{admins[i].Role}
		}
	}

	// Return search results
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"admins":  admins,
	})
}

// AdminGetAdminDetailsHandler gets detailed information about a specific admin
func (h Admin) AdminGetAdminDetailsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract admin ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 5 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid admin ID",
			Code:    "VALIDATION_ERROR",
		})
		return
	}
	adminID := pathParts[len(pathParts)-1]

	// Validate ObjectID
	objectID, err := primitive.ObjectIDFromHex(adminID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid admin ID format",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Find admin user
	admin, err := h.ADB.FindOne(r.Context(), bson.M{"_id": objectID})
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Admin user not found",
				Code:    "USER_NOT_FOUND",
			})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Failed to fetch admin user",
				Code:    "DATABASE_ERROR",
			})
		}
		return
	}

	// Ensure role field is populated from roles array for backward compatibility
	if admin.Role == "" && len(admin.Roles) > 0 {
		admin.Role = admin.Roles[0]
	} else if admin.Role != "" && len(admin.Roles) == 0 {
		// If we have legacy role but no roles array, populate it
		admin.Roles = []string{admin.Role}
	}

	// Return admin details
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(models.AdminDetailsResponse{
		Success: true,
		Admin:   *admin,
	})
}

// AdminUpdateLastLoginHandler updates an admin's last login time
func (h Admin) AdminUpdateLastLoginHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract admin ID from URL path using mux.Vars
	vars := mux.Vars(r)
	adminID, ok := vars["id"]
	if !ok || adminID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid admin ID",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Validate ObjectID
	objectID, err := primitive.ObjectIDFromHex(adminID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid admin ID format",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Parse request body
	var req models.UpdateAdminLastLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid request data",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Check if admin exists
	admin, err := h.ADB.FindOne(r.Context(), bson.M{"_id": objectID})
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Admin not found",
				Code:    "NOT_FOUND",
			})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Failed to fetch admin",
				Code:    "DATABASE_ERROR",
			})
		}
		return
	}

	// Update lastLoginAt
	_, err = h.ADB.UpdateOne(r.Context(), bson.M{"_id": objectID}, bson.M{
		"$set": bson.M{
			"lastLoginAt": req.LastLoginAt,
		},
	})
	if err != nil {
		log.Printf("Failed to update admin lastLoginAt: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to update admin last login",
			Code:    "DATABASE_ERROR",
		})
		return
	}

	// Fetch updated admin
	updatedAdmin, err := h.ADB.FindOne(r.Context(), bson.M{"_id": objectID})
	if err != nil {
		// If fetch fails, return success with original admin data
		admin.LastLoginAt = &req.LastLoginAt
		updatedAdmin = admin
	} else {
		updatedAdmin.LastLoginAt = &req.LastLoginAt
	}

	// Ensure role field is populated for backward compatibility
	if updatedAdmin.Role == "" && len(updatedAdmin.Roles) > 0 {
		updatedAdmin.Role = updatedAdmin.Roles[0]
	} else if updatedAdmin.Role != "" && len(updatedAdmin.Roles) == 0 {
		updatedAdmin.Roles = []string{updatedAdmin.Role}
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Admin last login updated",
		"admin":   updatedAdmin,
	})
}

// AdminUpdateProfileHandler updates an admin's profile (firstName, lastName, email, profilePicture)
// PATCH /api/v1/admin/admins/{id}/profile
func (h Admin) AdminUpdateProfileHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract admin ID from URL path
	vars := mux.Vars(r)
	adminID, ok := vars["id"]
	if !ok || adminID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid admin ID",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Validate ObjectID
	objectID, err := primitive.ObjectIDFromHex(adminID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid admin ID format",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Parse request body
	var req models.UpdateAdminProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid request data",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Security: Verify the currentUser's ID matches the profile being updated OR is an owner
	currentUserID, _ := req.CurrentUser["id"].(string)
	currentUserRoles, _ := req.CurrentUser["roles"].([]interface{})

	isOwner := false
	for _, role := range currentUserRoles {
		if roleStr, ok := role.(string); ok && roleStr == "owner" {
			isOwner = true
			break
		}
	}

	if currentUserID != adminID && !isOwner {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "You can only update your own profile",
			Code:    "FORBIDDEN",
		})
		return
	}

	// Check if admin exists
	admin, err := h.ADB.FindOne(r.Context(), bson.M{"_id": objectID})
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Admin not found",
				Code:    "NOT_FOUND",
			})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Failed to fetch admin",
				Code:    "DATABASE_ERROR",
			})
		}
		return
	}

	// Build update document with only provided fields
	updateFields := bson.M{}
	if req.FirstName != "" {
		updateFields["firstName"] = strings.TrimSpace(req.FirstName)
	}
	if req.LastName != "" {
		updateFields["lastName"] = strings.TrimSpace(req.LastName)
	}
	if req.Email != "" {
		newEmail := strings.ToLower(strings.TrimSpace(req.Email))
		// Check if email is being changed and if it's unique
		if newEmail != admin.Email {
			existingAdmin, _ := h.ADB.FindOne(r.Context(), bson.M{"email": newEmail})
			if existingAdmin != nil {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(models.ErrorResponse{
					Success: false,
					Error:   "Email already in use by another admin",
					Code:    "DUPLICATE_EMAIL",
				})
				return
			}
		}
		updateFields["email"] = newEmail
	}
	if req.ProfilePicture != "" {
		updateFields["profilePicture"] = req.ProfilePicture
	}

	// If no fields to update
	if len(updateFields) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "No fields to update",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Update admin document
	_, err = h.ADB.UpdateOne(r.Context(), bson.M{"_id": objectID}, bson.M{"$set": updateFields})
	if err != nil {
		log.Printf("Failed to update admin profile: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to update admin profile",
			Code:    "DATABASE_ERROR",
		})
		return
	}

	// Fetch updated admin
	updatedAdmin, err := h.ADB.FindOne(r.Context(), bson.M{"_id": objectID})
	if err != nil {
		// Return success with a note that fetch failed
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(models.UpdateAdminProfileResponse{
			Success: true,
			Message: "Profile updated successfully",
			Admin:   *admin,
		})
		return
	}

	// Ensure role field is populated for backward compatibility
	if updatedAdmin.Role == "" && len(updatedAdmin.Roles) > 0 {
		updatedAdmin.Role = updatedAdmin.Roles[0]
	} else if updatedAdmin.Role != "" && len(updatedAdmin.Roles) == 0 {
		updatedAdmin.Roles = []string{updatedAdmin.Role}
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(models.UpdateAdminProfileResponse{
		Success: true,
		Message: "Profile updated successfully",
		Admin:   *updatedAdmin,
	})
}

// AdminChangeRoleHandler changes an admin's role
func (h Admin) AdminChangeRoleHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract admin ID from URL path
	vars := mux.Vars(r)
	adminID := vars["id"]

	// Validate admin ID format
	objectID, err := primitive.ObjectIDFromHex(adminID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid admin ID format",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Parse request body
	var req models.ChangeRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request body",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Validate role
	if req.Role != "admin" && req.Role != "owner" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Role must be 'admin' or 'owner'",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Get current admin user
	adminUser, err := h.ADB.FindOne(r.Context(), bson.M{"_id": objectID})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Admin user not found",
				"code":    "NOT_FOUND",
			})
			return
		}
		log.Printf("Admin change role error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to fetch admin user",
			"code":    "DATABASE_ERROR",
		})
		return
	}

	// Update admin with new role
	update := bson.M{
		"$set": bson.M{
			"role": req.Role,
		},
	}

	_, err = h.ADB.UpdateOne(r.Context(), bson.M{"_id": objectID}, update)
	if err != nil {
		log.Printf("Admin change role update error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to update admin role",
			"code":    "DATABASE_ERROR",
		})
		return
	}

	// Update the local admin user object
	adminUser.Role = req.Role

	// Track the action
	h.trackAdminAction(objectID, "role_change", adminID, "admin", fmt.Sprintf("Role changed to: %s", req.Role), r)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Admin role updated successfully",
		"admin":   adminUser,
	})
}

// AdminChangeRolesHandler changes an admin's roles (array-based)
func (h Admin) AdminChangeRolesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract admin ID from URL path
	vars := mux.Vars(r)
	adminID := vars["id"]

	// Validate admin ID format
	objectID, err := primitive.ObjectIDFromHex(adminID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid admin ID format",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Parse request body
	var req models.ChangeRolesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request body",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Validate roles array
	if len(req.Roles) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Admin must have at least one role",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Validate each role
	for _, role := range req.Roles {
		if role != "admin" && role != "owner" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   fmt.Sprintf("Invalid role: %s. Role must be 'admin' or 'owner'", role),
				"code":    "VALIDATION_ERROR",
			})
			return
		}
	}

	// Get current admin user
	adminUser, err := h.ADB.FindOne(r.Context(), bson.M{"_id": objectID})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Admin user not found",
				"code":    "NOT_FOUND",
			})
			return
		}
		log.Printf("Admin change roles error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to fetch admin user",
			"code":    "DATABASE_ERROR",
		})
		return
	}

	// Check if we're removing the last owner role
	if adminUser.Role == "owner" && !contains(req.Roles, "owner") {
		// Count how many owners exist
		ownerCount, err := h.ADB.CountDocuments(r.Context(), bson.M{"role": "owner"})
		if err == nil && ownerCount <= 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Cannot remove the last owner role. At least one owner must remain.",
				"code":    "PERMISSION_DENIED",
			})
			return
		}
	}

	// Update admin with new roles
	update := bson.M{
		"$set": bson.M{
			"roles": req.Roles,
			"role":  req.Roles[0], // Keep legacy field for backward compatibility
		},
	}

	_, err = h.ADB.UpdateOne(r.Context(), bson.M{"_id": objectID}, update)
	if err != nil {
		log.Printf("Admin change roles update error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to update admin roles",
			"code":    "DATABASE_ERROR",
		})
		return
	}

	// Update the local admin user object
	adminUser.Roles = req.Roles
	adminUser.Role = req.Roles[0] // Legacy field

	// Track the action
	rolesStr := strings.Join(req.Roles, ", ")
	h.trackAdminAction(objectID, "roles_change", adminID, "admin", fmt.Sprintf("Roles changed to: %s", rolesStr), r)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Admin roles updated successfully",
		"admin":   adminUser,
	})
}



// AdminDeleteAdminHandler deletes an admin user
func (h Admin) AdminDeleteAdminHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract admin ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 5 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid admin ID",
			Code:    "VALIDATION_ERROR",
		})
		return
	}
	adminID := pathParts[len(pathParts)-1]

	// Validate ObjectID
	objectID, err := primitive.ObjectIDFromHex(adminID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid admin ID format",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Find admin user first to get email for logging
	admin, err := h.ADB.FindOne(r.Context(), bson.M{"_id": objectID})
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Admin user not found",
				Code:    "USER_NOT_FOUND",
			})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Failed to fetch admin user",
				Code:    "DATABASE_ERROR",
			})
		}
		return
	}

	// Check if trying to delete the last owner
	if len(admin.Roles) > 0 && admin.Roles[0] == "owner" {
		// Count total owners
		ownerCount, err := h.ADB.CountDocuments(r.Context(), bson.M{"roles": "owner"})
		if err == nil && ownerCount <= 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Cannot delete the last owner",
				"code":    "PERMISSION_DENIED",
			})
			return
		}
	}

	// Delete admin user
	err = h.ADB.DeleteOne(r.Context(), bson.M{"_id": objectID})
	if err != nil {
		log.Printf("Failed to delete admin user: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to delete admin user",
			"code":    "DATABASE_ERROR",
		})
		return
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Admin user %s deleted successfully", admin.Email),
	})
}

// AdminGetAllAdminsRequest represents the request to get all admin users with permission check
type AdminGetAllAdminsRequest struct {
	CurrentUser map[string]interface{} `json:"currentUser"`
}

// AdminGetAllAdminsHandler gets all admin users
func (h Admin) AdminGetAllAdminsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req AdminGetAllAdminsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request body",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Check permissions
	if err := checkAdminPermissions(req.CurrentUser); err != nil {
		response := map[string]interface{}{
			"success": false,
			"error":   err.Error(),
			"code":    "INSUFFICIENT_PERMISSIONS",
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Find all admin users with limit and sort for better performance
	// Note: admin_users collection is small (3 docs), but adding limit/sort is best practice
	opts := options.Find().
		SetLimit(1000). // Reasonable upper limit
		SetSort(bson.M{"_id": 1}) // Sort by _id for consistent ordering
	cursor, err := h.ADB.Find(r.Context(), bson.M{}, opts)
	if err != nil {
		log.Printf("Admin get all error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to fetch admin users",
			"code":    "DATABASE_ERROR",
		})
		return
	}
	defer cursor.Close(r.Context())

	var admins []models.AdminUser
	if err = cursor.All(r.Context(), &admins); err != nil {
		log.Printf("Admin get all decode error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to decode admin results",
			"code":    "DATABASE_ERROR",
		})
		return
	}

	// Ensure all admin objects have consistent role field population for backward compatibility
	for i := range admins {
		if admins[i].Role == "" && len(admins[i].Roles) > 0 {
			admins[i].Role = admins[i].Roles[0]
		} else if admins[i].Role != "" && len(admins[i].Roles) == 0 {
			admins[i].Roles = []string{admins[i].Role}
		}
	}

	// Return all admin users
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"admins":  admins,
	})
}

// AdminGetAdminCountHandler returns the count of admin users
func (h Admin) AdminGetAdminCountHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	count, err := h.ADB.CountDocuments(r.Context(), bson.M{})
	if err != nil {
		log.Printf("Admin count error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to count admin users",
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"count":   count,
	})
}

// AdminGetActivityRequest represents the request to get admin activity with permission check
type AdminGetActivityRequest struct {
	CurrentUser map[string]interface{} `json:"currentUser"`
	Timeframe   string                 `json:"timeframe"`
	Page        int                    `json:"page"`        // Page number (1-based)
	Limit       int                    `json:"limit"`       // Records per page
}

// AdminGetActivityHandler gets detailed activity information for a specific admin
func (h Admin) AdminGetActivityHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req AdminGetActivityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request body",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Check permissions - only owners can view admin activity
	if err := checkAdminPermissions(req.CurrentUser); err != nil {
		response := map[string]interface{}{
			"success": false,
			"error":   "Insufficient permissions to view admin activity",
			"code":    "INSUFFICIENT_PERMISSIONS",
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Extract admin ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 6 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid admin ID",
			"code":    "VALIDATION_ERROR",
		})
		return
	}
	adminID := pathParts[len(pathParts)-2]

	// Validate ObjectID
	objectID, err := primitive.ObjectIDFromHex(adminID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid admin ID format",
			"code":    "VALIDATION_ERROR",
		})
		return
	}

	// Check if admin exists
	admin, err := h.ADB.FindOne(r.Context(), bson.M{"_id": objectID})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Admin user not found",
				"code":    "ADMIN_NOT_FOUND",
			})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to fetch admin user",
			"code":    "DATABASE_ERROR",
		})
		return
	}

	// Log the admin being viewed for audit purposes
	log.Printf("Admin activity requested for: %s (%s)", admin.Email, admin.ID.Hex())
	
	// Log admin activity request for audit purposes
	log.Printf("Admin activity requested for: %s (%s)", admin.Email, admin.ID.Hex())

	// Get timeframe from request body, default to 30 days if not specified
	timeframe := req.Timeframe
	if timeframe == "" {
		timeframe = "30d" // default to 30 days
	}

	// Set pagination defaults
	page := req.Page
	if page <= 0 {
		page = 1 // Default to first page
	}
	
	limit := req.Limit
	if limit <= 0 {
		limit = 10 // Default to 10 records per page
	}
	
	// Calculate skip value for MongoDB
	skip := int64((page - 1) * limit)
	limit64 := int64(limit)

	// Calculate time range based on timeframe
	now := time.Now()
	var startTime time.Time
	
	switch timeframe {
	case "1d":
		startTime = now.AddDate(0, 0, -1)
	case "7d":
		startTime = now.AddDate(0, 0, -7)
	case "30d":
		startTime = now.AddDate(0, 0, -30)
	case "1m":
		startTime = now.AddDate(0, -1, 0)
	case "3m":
		startTime = now.AddDate(0, -3, 0)
	case "6m":
		startTime = now.AddDate(0, -6, 0)
	case "1y":
		startTime = now.AddDate(-1, 0, 0)
	case "all":
		startTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC) // Very old date for all time
	default:
		startTime = now.AddDate(0, 0, -30) // fallback to 30 days
	}

	// Calculate LIFETIME TOTALS (not filtered by timeframe)
	// Count total logins - LIFETIME TOTAL
	loginCount, err := h.AADB.CountDocuments(r.Context(), bson.M{
		"adminId": objectID.Hex(),
		"type":    "login",
	})
	if err != nil {
		log.Printf("Failed to count logins: %v", err)
		loginCount = 0
	}

	// Count password resets - LIFETIME TOTAL
	passwordResetCount, err := h.AADB.CountDocuments(r.Context(), bson.M{
		"adminId": objectID.Hex(),
		"type":    "password_reset",
	})
	if err != nil {
		log.Printf("Failed to count password resets: %v", err)
		passwordResetCount = 0
	}

	// Count accounts reactivated - LIFETIME TOTAL (replaces passwordResetsInitiated)
	accountsReactivatedCount, err := h.AADB.CountDocuments(r.Context(), bson.M{
		"adminId": objectID.Hex(),
		"type":    "account_reactivated",
	})
	if err != nil {
		log.Printf("Failed to count accounts reactivated: %v", err)
		accountsReactivatedCount = 0
	}

	// Calculate average session time - can use timeframe or lifetime
	// Using timeframe for now, but can be changed to lifetime
	avgSessionTime, err := h.calculateAverageSessionTime(r.Context(), objectID, startTime)
	if err != nil {
		avgSessionTime = "0m"
	}

	// Build stats object (lifetime totals)
	stats := map[string]interface{}{
		"totalLogins":        int(loginCount),
		"passwordResets":     int(passwordResetCount),
		"accountsReactivated": int(accountsReactivatedCount),
		"avgSessionTime":    avgSessionTime,
	}

	// Generate timeline data (filtered by timeframe) - format: {date, count}
	timeline := []map[string]interface{}{}
	
	// Calculate number of days to show based on timeframe
	var daysToShow int
	switch timeframe {
	case "1d":
		daysToShow = 1
	case "7d":
		daysToShow = 7
	case "30d":
		daysToShow = 30
	case "1m":
		daysToShow = 30
	case "3m":
		daysToShow = 90
	case "6m":
		daysToShow = 180
	case "1y":
		daysToShow = 365
	case "all":
		// For "all", show daily data from admin creation date
		// Use admin.CreatedAt if available, otherwise use a default date
		adminCreatedAt := admin.CreatedAt
		if adminCreatedAt.IsZero() {
			// If CreatedAt is not set, use a default date (1 year ago)
			adminCreatedAt = now.AddDate(-1, 0, 0)
		}
		daysToShow = int(now.Sub(adminCreatedAt).Hours() / 24)
		if daysToShow > 365 {
			daysToShow = 365 // Cap at 1 year for performance
		}
		if daysToShow < 1 {
			daysToShow = 1 // Ensure at least 1 day
		}
	default:
		daysToShow = 30
	}
	
	// Generate timeline entries for each day in the timeframe
	for i := daysToShow - 1; i >= 0; i-- {
		date := now.AddDate(0, 0, -i)
		dateStr := date.Format("2006-01-02")
		
		// Count activities for this day (filtered by timeframe)
		dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
		dayEnd := dayStart.AddDate(0, 0, 1)
		
		// Only count if within the requested timeframe
		if dayStart.Before(startTime) {
			continue
		}
		
		count, _ := h.AADB.CountDocuments(r.Context(), bson.M{
			"adminId": objectID.Hex(),
			"timestamp": bson.M{
				"$gte": dayStart,
				"$lt":  dayEnd,
			},
		})
		
		timeline = append(timeline, map[string]interface{}{
			"date":  dateStr,
			"count": int(count),
		})
	}

	// Get recent activity with pagination
	recentFilter := bson.M{
		"adminId": objectID.Hex(),
		"timestamp": bson.M{
			"$gte": startTime,
		},
	}

	// Get total count for pagination metadata
	totalCount, _ := h.AADB.CountDocuments(r.Context(), recentFilter)

	// Get recent activities from admin activity database with pagination (filtered by timeframe)
	recentCursor, err := h.AADB.Find(r.Context(), recentFilter, &options.FindOptions{
		Sort:  bson.M{"timestamp": -1}, // Sort by timestamp descending (newest first)
		Skip:  &skip,
		Limit: &limit64,
	})
	
	recentActivity := []map[string]interface{}{}
	if err == nil {
		defer recentCursor.Close(r.Context())
		
		var recentActivities []models.AdminActivityStorage
		if err = recentCursor.All(r.Context(), &recentActivities); err == nil {
			for _, activityItem := range recentActivities {
				activityEntry := map[string]interface{}{
					"id":        activityItem.ID.Hex(),
					"type":      activityItem.Type,
					"title":     activityItem.Title,
					"details":   activityItem.Details,
					"timestamp": activityItem.Timestamp,
					"ip":        activityItem.IP,
				}
				
				// Add sessionDuration if present (for logout events)
				if activityItem.SessionDuration != "" {
					activityEntry["sessionDuration"] = activityItem.SessionDuration
				}
				
				recentActivity = append(recentActivity, activityEntry)
			}
		}
	}

	// Calculate pagination metadata
	totalPages := int((totalCount + int64(limit) - 1) / int64(limit))
	if totalPages == 0 {
		totalPages = 1
	}

	// Build response matching the spec
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"activity": map[string]interface{}{
			"stats": stats,
			"timeline": timeline,
			"recentActivity": recentActivity,
			"pagination": map[string]interface{}{
				"currentPage":  page,
				"totalPages":   totalPages,
				"totalRecords": totalCount,
				"limit":        limit,
			},
		},
	})
}

// trackAdminLogin tracks when an admin logs in
func (h Admin) trackAdminLogin(adminID primitive.ObjectID, r *http.Request) {
	// Get client IP
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.Header.Get("X-Real-IP")
	}
	if ip == "" {
		ip = r.RemoteAddr
	}

	// Create login activity record
	loginActivity := models.AdminActivityStorage{
		AdminID:   adminID.Hex(),
		Type:      "login",
		Title:     "Admin logged in",
		Details:   fmt.Sprintf("IP: %s", ip),
		Timestamp: time.Now(),
		IP:        ip,
		CreatedAt: time.Now(),
	}

	// Insert the activity record
	_, err := h.AADB.InsertOne(r.Context(), loginActivity)
	if err != nil {
		log.Printf("Failed to track admin login: %v", err)
	}
}

// trackAdminLogout tracks when an admin logs out and calculates session duration
func (h Admin) trackAdminLogout(adminID primitive.ObjectID, r *http.Request) {
	// Get client IP
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.Header.Get("X-Real-IP")
	}
	if ip == "" {
		ip = r.RemoteAddr
	}

	logoutTime := time.Now()
	sessionDuration := ""

	// Find the most recent login event for this admin
	loginFilter := bson.M{
		"adminId": adminID.Hex(),
		"type":    "login",
	}
	
	limitOne := int64(1)
	loginCursor, err := h.AADB.Find(r.Context(), loginFilter, &options.FindOptions{
		Sort:  bson.M{"timestamp": -1}, // Most recent first
		Limit: &limitOne,
	})
	
	if err == nil {
		defer loginCursor.Close(r.Context())
		
		var loginEvents []models.AdminActivityStorage
		if err = loginCursor.All(r.Context(), &loginEvents); err == nil && len(loginEvents) > 0 {
			// Calculate session duration from most recent login
			loginTime := loginEvents[0].Timestamp
			duration := logoutTime.Sub(loginTime)
			
			// Format duration as "Xh Ym", "Ym", or "Xs"
			seconds := int(duration.Seconds())
			minutes := seconds / 60
			hours := minutes / 60
			
			if hours > 0 {
				remainingMinutes := minutes % 60
				if remainingMinutes > 0 {
					sessionDuration = fmt.Sprintf("%dh %dm", hours, remainingMinutes)
				} else {
					sessionDuration = fmt.Sprintf("%dh", hours)
				}
			} else if minutes > 0 {
				sessionDuration = fmt.Sprintf("%dm", minutes)
			} else {
				sessionDuration = fmt.Sprintf("%ds", seconds)
			}
		}
	}

	// Create logout activity record with session duration
	logoutActivity := models.AdminActivityStorage{
		AdminID:        adminID.Hex(),
		Type:           "logout",
		Title:          "Admin logged out",
		Details:        fmt.Sprintf("IP: %s", ip),
		Timestamp:      logoutTime,
		IP:             ip,
		CreatedAt:      time.Now(),
		SessionDuration: sessionDuration,
	}

	// Insert the activity record
	_, err = h.AADB.InsertOne(r.Context(), logoutActivity)
	if err != nil {
		log.Printf("Failed to track admin logout: %v", err)
	}
}

// trackPasswordResetInitiated tracks when a password reset is initiated
func (h Admin) trackPasswordResetInitiated(adminID primitive.ObjectID, r *http.Request) {
	// Get client IP
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.Header.Get("X-Real-IP")
	}
	if ip == "" {
		ip = r.RemoteAddr
	}

	// Create password reset initiated activity record
	resetInitiatedActivity := models.AdminActivityStorage{
		AdminID:   adminID.Hex(),
		Type:      "password_reset_initiated",
		Title:     "Password reset initiated",
		Details:   fmt.Sprintf("Password reset email sent to %s", adminID.Hex()),
		Timestamp: time.Now(),
		IP:        ip,
		CreatedAt: time.Now(),
	}

	// Insert the activity record
	_, err := h.AADB.InsertOne(r.Context(), resetInitiatedActivity)
	if err != nil {
		log.Printf("Failed to track password reset initiated: %v", err)
	}
}

// trackPasswordResetCompleted tracks when a password reset is completed
func (h Admin) trackPasswordResetCompleted(adminID primitive.ObjectID, r *http.Request) {
	// Get client IP
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.Header.Get("X-Real-IP")
	}
	if ip == "" {
		ip = r.RemoteAddr
	}

	// Create password reset completed activity record
	resetCompletedActivity := models.AdminActivityStorage{
		AdminID:   adminID.Hex(),
		Type:      "password_reset",
		Title:     "Password reset completed",
		Details:   "Password successfully updated",
		Timestamp: time.Now(),
		IP:        ip,
		CreatedAt: time.Now(),
	}

	// Insert the activity record
	_, err := h.AADB.InsertOne(r.Context(), resetCompletedActivity)
	if err != nil {
		log.Printf("Failed to track password reset completed: %v", err)
	}
}

// calculateAverageSessionTime calculates the average session time for an admin
func (h Admin) calculateAverageSessionTime(ctx context.Context, adminID primitive.ObjectID, startTime time.Time) (string, error) {
	// Find all login events for the admin in the time range
	loginFilter := bson.M{
		"adminId": adminID.Hex(),
		"type":    "login",
		"timestamp": bson.M{
			"$gte": startTime,
		},
	}

	loginCursor, err := h.AADB.Find(ctx, loginFilter, nil)
	if err != nil {
		return "0m", err
	}
	defer loginCursor.Close(ctx)

	var loginEvents []models.AdminActivityStorage
	if err = loginCursor.All(ctx, &loginEvents); err != nil {
		return "0m", err
	}

	if len(loginEvents) == 0 {
		return "0m", nil
	}

	var totalDuration time.Duration
	var sessionCount int

	// For each login, find the corresponding logout
	for _, login := range loginEvents {
		// Find logout event that comes after this login
		logoutFilter := bson.M{
			"adminId": adminID.Hex(),
			"type":    "logout",
			"timestamp": bson.M{
				"$gt": login.Timestamp,
			},
		}

		logoutCursor, err := h.AADB.Find(ctx, logoutFilter, nil)
		if err != nil {
			continue
		}

		var logoutEvents []models.AdminActivityStorage
		if err = logoutCursor.All(ctx, &logoutEvents); err != nil {
			logoutCursor.Close(ctx)
			continue
		}
		logoutCursor.Close(ctx)

		// Find the closest logout after this login
		var closestLogout *models.AdminActivityStorage
		for _, logout := range logoutEvents {
			if closestLogout == nil || logout.Timestamp.Sub(login.Timestamp) < closestLogout.Timestamp.Sub(login.Timestamp) {
				closestLogout = &logout
			}
		}

		if closestLogout != nil {
			duration := closestLogout.Timestamp.Sub(login.Timestamp)
			totalDuration += duration
			sessionCount++
		}
	}

	if sessionCount == 0 {
		return "0m", nil
	}

	avgDuration := totalDuration / time.Duration(sessionCount)
	avgMinutes := int(avgDuration.Minutes())

	if avgMinutes < 60 {
		return fmt.Sprintf("%dm", avgMinutes), nil
	}

	avgHours := avgMinutes / 60
	remainingMinutes := avgMinutes % 60
	return fmt.Sprintf("%dh%dm", avgHours, remainingMinutes), nil
}





// trackAdminAction tracks administrative actions
func (h Admin) trackAdminAction(adminID primitive.ObjectID, actionType, targetID, targetType, details string, r *http.Request) {
	// Get client IP
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.Header.Get("X-Real-IP")
	}
	if ip == "" {
		ip = r.RemoteAddr
	}

	// Create action activity record
	actionActivity := models.AdminActivityStorage{
		AdminID:   adminID.Hex(),
		Type:      actionType,
		Title:     getActionTitle(actionType),
		Details:   details,
		Timestamp: time.Now(),
		IP:        ip,
		CreatedAt: time.Now(),
	}

	// Store in database
	_, err := h.AADB.InsertOne(r.Context(), actionActivity)
	if err != nil {
		log.Printf("Failed to log admin action: %v", err)
	}
}

// getActionTitle returns a human-readable title for action types
func getActionTitle(actionType string) string {
	switch actionType {
	case "login":
		return "Admin logged in"
	case "logout":
		return "Admin logged out"
	case "password_reset":
		return "Password reset completed"
	case "account_reactivated":
		return "User account reactivated"
	case "user_reset_initiated":
		return "User password reset initiated"
	case "temp_password_created":
		return "Temporary password created"
	case "role_change":
		return "Admin role changed"
	case "roles_change":
		return "Admin roles changed"
	case "user_reset_password":
		return "User password reset sent"
	default:
		return "Administrative action performed"
	}
}

// AdminActivityLogHandler logs admin activity events
func (h Admin) AdminActivityLogHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Parse request body
	var req models.AdminActivityLogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid request body",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Validate required fields
	if req.AdminID == "" || req.Type == "" || req.Title == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Missing required fields",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Check permissions if currentUser is provided
	if req.CurrentUser != nil {
		if err := checkAdminPermissions(req.CurrentUser); err != nil {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Insufficient permissions",
				Code:    "INSUFFICIENT_PERMISSIONS",
			})
			return
		}
	}

	// Get client IP if not provided
	if req.IP == "" {
		req.IP = r.Header.Get("X-Forwarded-For")
		if req.IP == "" {
			req.IP = r.Header.Get("X-Real-IP")
		}
		if req.IP == "" {
			req.IP = r.RemoteAddr
		}
	}

	// Create activity record
	activity := models.AdminActivityStorage{
		AdminID:        req.AdminID,
		Type:           req.Type,
		Title:          req.Title,
		Details:        req.Details,
		Timestamp:      req.Timestamp,
		IP:             req.IP,
		CreatedAt:      time.Now(),
		SessionDuration: req.SessionDuration,
	}

	// Insert into database
	result, err := h.AADB.InsertOne(r.Context(), activity)
	if err != nil {
		log.Printf("Error logging admin activity: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to log activity",
			Code:    "DATABASE_ERROR",
		})
		return
	}

	// Get the inserted ID
	insertedID := result.Decode()
	var activityIDStr string
	if oid, ok := insertedID.(primitive.ObjectID); ok {
		activityIDStr = oid.Hex()
	} else if str, ok := insertedID.(string); ok {
		activityIDStr = str
	}

	// Return success with activity ID
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"message":    "Activity logged successfully",
		"activityId": activityIDStr,
	})
}

// AdminSendEmailHandler sends a generic email via SendGrid. Owner-only.
func (h Admin) AdminSendEmailHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req models.AdminSendEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid request body",
			Code:    "INVALID_REQUEST",
		})
		return
	}

	// Validate required fields
	toEmail := strings.TrimSpace(req.ToEmail)
	subject := strings.TrimSpace(req.Subject)
	body := strings.TrimSpace(req.Body)

	if toEmail == "" || subject == "" || body == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Email address, subject, and body are all required",
			Code:    "MISSING_FIELDS",
		})
		return
	}

	if len(body) > 10000 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Email body must be under 10,000 characters",
			Code:    "BODY_TOO_LONG",
		})
		return
	}

	// Authorize: owner-only
	if err := checkAdminPermissions(req.CurrentUser); err != nil {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Insufficient permissions: owner role required",
			Code:    "FORBIDDEN",
		})
		return
	}

	// Build and send email
	plainText := body
	htmlContent := templates.RenderGenericEmail(subject, body)

	from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
	to := mail.NewEmail("", toEmail)
	message := mail.NewSingleEmail(from, subject, to, plainText, htmlContent)
	client := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))

	response, err := client.Send(message)
	if err != nil {
		log.Printf("Failed to send email to %s: %v", toEmail, err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to send email",
			Code:    "SENDGRID_ERROR",
		})
		return
	}

	if response.StatusCode >= 400 {
		log.Printf("SendGrid returned status %d for email to %s: %s", response.StatusCode, toEmail, response.Body)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Email service returned an error",
			Code:    "SENDGRID_ERROR",
		})
		return
	}

	log.Printf("Email sent successfully to %s (subject: %s)", toEmail, subject)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(models.AdminSendEmailResponse{
		Success: true,
		Message: "Email sent successfully",
	})
}
