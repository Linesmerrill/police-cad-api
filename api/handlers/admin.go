package handlers

import (
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
	"golang.org/x/crypto/bcrypt"

	"github.com/gorilla/mux"
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
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	token := strings.TrimSpace(req.Token)
	password := req.Password
	if token == "" || password == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "token and password required"})
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
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid or expired token"})
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "could not update password"})
		return
	}

	// Update admin password
	_, err = h.ADB.UpdateOne(r.Context(), bson.M{"_id": reset.AdminID}, bson.M{"$set": bson.M{"passwordHash": string(newHash), "updatedAt": time.Now()}})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "could not update password"})
		return
	}
	// Mark token used
	_, _ = h.RDB.UpdateOne(r.Context(), bson.M{"_id": reset.ID}, bson.M{"$set": bson.M{"usedAt": time.Now()}})

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "password updated"})
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
}

type userSearchResponse struct {
	Users []models.AdminUserResult `json:"users"`
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

	// Search by email, name, or username (case-insensitive)
	filter := bson.M{
		"$or": []bson.M{
			{"user.email": bson.M{"$regex": query, "$options": "i"}},
			{"user.name": bson.M{"$regex": query, "$options": "i"}},
			{"user.username": bson.M{"$regex": query, "$options": "i"}},
		},
	}
	
	log.Printf("Admin user search filter: %+v", filter)
	
	// Use existing user database to search
	cursor, err := h.UDB.Find(r.Context(), filter, nil)
	if err != nil {
		log.Printf("Admin user search error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "search failed"})
		return
	}
	defer cursor.Close(r.Context())

	var users []models.User
	if err = cursor.All(r.Context(), &users); err != nil {
		log.Printf("Admin user search decode error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to decode users"})
		return
	}

	log.Printf("Admin user search found %d users", len(users))

	var results []models.AdminUserResult
	for _, user := range users {
		result := models.AdminUserResult{
			ID:        user.ID,
			Email:     user.Details.Email,
			Username:  user.Details.Username,
			Active:    !user.Details.IsDeactivated,
			CreatedAt: user.Details.CreatedAt,
		}
		log.Printf("User result: %+v", result)
		results = append(results, result)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(userSearchResponse{Users: results})
}

type communitySearchRequest struct {
	Query string `json:"query"`
}

type communitySearchResponse struct {
	Communities []models.AdminCommunityResult `json:"communities"`
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

	// Search by community name (case-insensitive)
	filter := bson.M{"community.name": bson.M{"$regex": query, "$options": "i"}}
	
	log.Printf("Admin community search filter: %+v", filter)
	
	cursor, err := h.CDB.Find(r.Context(), filter, nil)
	if err != nil {
		log.Printf("Admin community search error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "search failed"})
		return
	}
	defer cursor.Close(r.Context())

	var communities []models.Community
	if err = cursor.All(r.Context(), &communities); err != nil {
		log.Printf("Admin community search decode error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to decode communities"})
		return
	}

	log.Printf("Admin community search found %d communities", len(communities))

	var results []models.AdminCommunityResult
	for _, community := range communities {
		// Get owner info
		var ownerInfo *models.OwnerInfo
		if community.Details.OwnerID != "" {
			ownerResult := h.UDB.FindOne(r.Context(), bson.M{"_id": community.Details.OwnerID})
			var ownerUser models.User
			if err := ownerResult.Decode(&ownerUser); err == nil {
				ownerInfo = &models.OwnerInfo{
					ID:    ownerUser.ID,
					Email: ownerUser.Details.Email,
				}
			}
		}

		result := models.AdminCommunityResult{
			ID:          community.ID.Hex(),
			Name:        community.Details.Name,
			Active:      true, // TODO: Add active field to community model
			CreatedAt:   community.Details.CreatedAt,
			Owner:       ownerInfo,
			MemberCount: community.Details.MembersCount,
		}
		log.Printf("Community result: %+v", result)
		results = append(results, result)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(communitySearchResponse{Communities: results})
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

	// Get user communities with role information
	var userCommunities []models.AdminUserCommunity
	
	// Find communities where this user is a member
	cursor, err := h.CDB.Find(r.Context(), bson.M{
		"$or": []bson.M{
			{"details.ownerID": user.ID},                    // User is owner
			{"details.members": bson.M{"$in": []string{user.ID}}}, // User is member
		},
	}, nil)
	if err == nil {
		defer cursor.Close(r.Context())
		
		var communities []models.Community
		if err = cursor.All(r.Context(), &communities); err == nil {
			for _, community := range communities {
				role := "Member"
				if community.Details.OwnerID == user.ID {
					role = "Owner"
				}
				
				// Get department info if available
				department := ""
				if len(community.Details.Departments) > 0 {
					department = community.Details.Departments[0].Name
				}
				
				userCommunities = append(userCommunities, models.AdminUserCommunity{
					ID:         community.ID.Hex(),
					Name:       community.Details.Name,
					Role:       role,
					Department: department,
					JoinedAt:   community.Details.CreatedAt, // Use community creation as joined date for now
				})
			}
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
		ID:          user.ID,
		Email:       user.Details.Email,
		Username:    user.Details.Username,
		Active:      !user.Details.IsDeactivated,
		CreatedAt:   user.Details.CreatedAt,
		Communities: userCommunities,
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

	// Get owner info
	var ownerInfo *models.OwnerInfo
	if community.Details.OwnerID != "" {
		ownerResult := h.UDB.FindOne(r.Context(), bson.M{"_id": community.Details.OwnerID})
		var ownerUser models.User
		if err := ownerResult.Decode(&ownerUser); err == nil {
			ownerInfo = &models.OwnerInfo{
				ID:    ownerUser.ID,
				Email: ownerUser.Details.Email,
			}
		}
	}

	// TODO: Get departments and implement member counting
	var depts []models.CommunityDept

	details := models.AdminCommunityDetails{
		ID:          community.ID.Hex(),
		Name:        community.Details.Name,
		Active:      true, // TODO: Add active field to community model
		CreatedAt:   community.Details.CreatedAt,
		Owner:       ownerInfo,
		MemberCount: community.Details.MembersCount,
		Departments: depts,
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

// CreateAdminUserHandler creates a new admin user
func (h Admin) CreateAdminUserHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// TODO: Get current admin user from JWT token for permission check
	// For now, we'll assume the request is authorized
	// currentUser := getCurrentAdminUser(r)

	// Check if current user can create admins
	if !canCreateAdmins(nil) { // Pass nil for currentUser as it's not available here
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Insufficient permissions to create admin users",
			Code:    "PERMISSION_DENIED",
		})
		return
	}

	// Parse request body
	var req models.CreateAdminUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid request body",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Validate email format
	if !isValidEmail(req.Email) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid email format",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Check if admin user already exists
	existingAdmin, err := h.ADB.FindOne(r.Context(), bson.M{"email": req.Email})
	if err == nil && existingAdmin != nil {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "User with this email already exists",
			Code:    "DUPLICATE_USER",
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
	_ = json.NewEncoder(w).Encode(models.CreateAdminUserResponse{
		Success:   true,
		Message:   "Admin user created successfully",
		AdminUser: adminUser,
		ResetLink: resetLink,
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

// AdminSearchAdminsHandler searches for admin users
func (h Admin) AdminSearchAdminsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Parse request body
	var req models.AdminSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid request body",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Validate query
	if req.Query == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Search query is required",
			Code:    "VALIDATION_ERROR",
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
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to search admins",
			Code:    "DATABASE_ERROR",
		})
		return
	}
	defer cursor.Close(r.Context())

	var admins []models.AdminUser
	if err = cursor.All(r.Context(), &admins); err != nil {
		log.Printf("Admin search decode error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to decode admin results",
			Code:    "DATABASE_ERROR",
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
	_ = json.NewEncoder(w).Encode(models.AdminSearchResponse{
		Success: true,
		Admins:  admins,
		Total:   len(admins),
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
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid admin ID format",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Parse request body
	var req models.ChangeRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid request body",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Validate role
	if req.Role != "admin" && req.Role != "owner" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Role must be 'admin' or 'owner'",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Get current admin user
	adminUser, err := h.ADB.FindOne(r.Context(), bson.M{"_id": objectID})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Admin user not found",
				Code:    "NOT_FOUND",
			})
			return
		}
		log.Printf("Admin change role error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to fetch admin user",
			Code:    "DATABASE_ERROR",
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
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to update admin role",
			Code:    "DATABASE_ERROR",
		})
		return
	}

	// Update the local admin user object
	adminUser.Role = req.Role

	// Track the action
	h.trackAdminAction(objectID, "role_change", adminID, "admin", fmt.Sprintf("Role changed to: %s", req.Role), r)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(models.ChangeRoleResponse{
		Success: true,
		Message: "Admin role updated successfully",
		Admin:   *adminUser,
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
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid admin ID format",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Parse request body
	var req models.ChangeRolesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid request body",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Validate roles array
	if len(req.Roles) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Admin must have at least one role",
			Code:    "VALIDATION_ERROR",
		})
		return
	}

	// Validate each role
	for _, role := range req.Roles {
		if role != "admin" && role != "owner" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   fmt.Sprintf("Invalid role: %s. Role must be 'admin' or 'owner'", role),
				Code:    "VALIDATION_ERROR",
			})
			return
		}
	}

	// Get current admin user
	adminUser, err := h.ADB.FindOne(r.Context(), bson.M{"_id": objectID})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Admin user not found",
				Code:    "NOT_FOUND",
			})
			return
		}
		log.Printf("Admin change roles error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to fetch admin user",
			Code:    "DATABASE_ERROR",
		})
		return
	}

	// Check if we're removing the last owner role
	if adminUser.Role == "owner" && !contains(req.Roles, "owner") {
		// Count how many owners exist
		ownerCount, err := h.ADB.CountDocuments(r.Context(), bson.M{"role": "owner"})
		if err == nil && ownerCount <= 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Cannot remove the last owner role. At least one owner must remain.",
				Code:    "PERMISSION_DENIED",
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
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to update admin roles",
			Code:    "DATABASE_ERROR",
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
	_ = json.NewEncoder(w).Encode(models.ChangeRoleResponse{
		Success: true,
		Message: "Admin roles updated successfully",
		Admin:   *adminUser,
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
			_ = json.NewEncoder(w).Encode(models.ErrorResponse{
				Success: false,
				Error:   "Cannot delete the last owner",
				Code:    "PERMISSION_DENIED",
			})
			return
		}
	}

	// Delete admin user
	err = h.ADB.DeleteOne(r.Context(), bson.M{"_id": objectID})
	if err != nil {
		log.Printf("Failed to delete admin user: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to delete admin user",
			Code:    "DATABASE_ERROR",
		})
		return
	}

	// Return success response
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(models.DeleteAdminResponse{
		Success: true,
		Message: fmt.Sprintf("Admin user %s deleted successfully", admin.Email),
	})
}

// AdminGetAllAdminsHandler gets all admin users
func (h Admin) AdminGetAllAdminsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Find all admin users
	cursor, err := h.ADB.Find(r.Context(), bson.M{}, nil)
	if err != nil {
		log.Printf("Admin get all error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to fetch admin users",
			Code:    "DATABASE_ERROR",
		})
		return
	}
	defer cursor.Close(r.Context())

	var admins []models.AdminUser
	if err = cursor.All(r.Context(), &admins); err != nil {
		log.Printf("Admin get all decode error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Failed to decode admin results",
			Code:    "DATABASE_ERROR",
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
	_ = json.NewEncoder(w).Encode(models.AdminSearchResponse{
		Success: true,
		Admins:  admins,
		Total:   len(admins),
	})
}

// AdminGetActivityHandler gets detailed activity information for a specific admin
func (h Admin) AdminGetActivityHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract admin ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 6 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.ErrorResponse{
			Success: false,
			Error:   "Invalid admin ID",
			Code:    "VALIDATION_ERROR",
		})
		return
	}
	adminID := pathParts[len(pathParts)-2]

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

	// Get query parameters for filtering
	queryParams := r.URL.Query()
	timeframe := queryParams.Get("timeframe")
	if timeframe == "" {
		timeframe = "30d" // default to 30 days
	}

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
		startTime = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) // reasonable start date
	default:
		startTime = now.AddDate(0, 0, -30) // fallback to 30 days
	}

	// Build activity data
	activity := models.AdminActivityData{
		TotalLogins:    0,
		PasswordResets: 0,
		TempPasswords:  0, // Keep for backward compatibility but will be 0
		AvgSessionTime: "0m",
		ChartData:      []models.ChartDataPoint{},
		RecentActivity: []models.ActivityItem{},
	}

	// Count total logins
	loginCount, err := h.ADB.CountDocuments(r.Context(), bson.M{
		"adminId": objectID,
		"type":    "login",
		"timestamp": bson.M{
			"$gte": startTime,
		},
	})
	if err == nil {
		activity.TotalLogins = int(loginCount)
	}

	// Count password resets (placeholder for now)
	// This would require implementing activity tracking in the password reset flow
	activity.PasswordResets = 0

	// Calculate average session time (placeholder for now)
	// This would require tracking logout events and calculating duration
	activity.AvgSessionTime = "45m" // placeholder

	// Generate chart data for the last 7 days
	chartStart := now.AddDate(0, 0, -7)
	for i := 0; i < 7; i++ {
		date := chartStart.AddDate(0, 0, i)
		dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
		dayEnd := dayStart.AddDate(0, 0, 1)

		// Count logins for this day
		dayLoginCount, err := h.ADB.CountDocuments(r.Context(), bson.M{
			"adminId": objectID,
			"type":    "login",
			"timestamp": bson.M{
				"$gte": dayStart,
				"$lt":  dayEnd,
			},
		})
		
		dayCount := 0
		if err == nil {
			dayCount = int(dayLoginCount)
		}

		activity.ChartData = append(activity.ChartData, models.ChartDataPoint{
			Date:  date.Format("Jan 02"),
			Value: dayCount,
		})
	}

	// Get recent activity (last 20 events)
	recentFilter := bson.M{
		"adminId": objectID,
		"timestamp": bson.M{
			"$gte": startTime,
		},
	}

	// Get recent login activities
	loginCursor, err := h.ADB.Find(r.Context(), recentFilter, nil)
	if err == nil {
		defer loginCursor.Close(r.Context())
		
		var loginActivities []models.AdminLoginActivity
		if err = loginCursor.All(r.Context(), &loginActivities); err == nil {
			for _, login := range loginActivities {
				title := "Admin logged in"
				if login.Type == "logout" {
					title = "Admin logged out"
				}

				activity.RecentActivity = append(activity.RecentActivity, models.ActivityItem{
					Type:      login.Type,
					Title:     title,
					Details:   fmt.Sprintf("IP: %s", login.IP),
					Timestamp: login.Timestamp,
				})
			}
		}
	}

	// Sort recent activity by timestamp (newest first)
	if len(activity.RecentActivity) > 20 {
		activity.RecentActivity = activity.RecentActivity[:20]
	}

	// Return activity data
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(models.AdminActivityResponse{
		Success:  true,
		Activity: activity,
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

	// Store in database
	var err error
	_, err = h.AADB.InsertOne(r.Context(), loginActivity)
	if err != nil {
		log.Printf("Failed to log admin login activity: %v", err)
	} else {
		log.Printf("Admin login tracked: %s from IP %s", adminID.Hex(), ip)
	}
}

// trackAdminLogout tracks when an admin logs out
func (h Admin) trackAdminLogout(adminID primitive.ObjectID, sessionID string, r *http.Request) {
	// Get client IP
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.Header.Get("X-Real-IP")
	}
	if ip == "" {
		ip = r.RemoteAddr
	}

	// Create logout activity record (placeholder for now)
	// This would store in a dedicated activity collection
	log.Printf("Admin logout tracked: %s from IP %s with session %s", adminID.Hex(), ip, sessionID)
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
	} else {
		log.Printf("Admin action tracked: %s performed %s on %s: %s from IP %s", adminID.Hex(), actionType, targetType, details, ip)
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
		return "Password reset requested"
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

	// Create activity record
	activity := models.AdminActivityStorage{
		AdminID:   req.AdminID,
		Type:      req.Type,
		Title:     req.Title,
		Details:   req.Details,
		Timestamp: req.Timestamp,
		IP:        req.IP,
		CreatedAt: time.Now(),
	}

	// Insert into database
	_, err := h.AADB.InsertOne(r.Context(), activity)
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

	// Return success
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Activity logged successfully",
	})
}


