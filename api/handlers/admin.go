package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// Admin holds dependencies for admin endpoints
type Admin struct {
	ADB databases.AdminDatabase
	RDB databases.AdminResetDatabase
	UDB databases.UserDatabase
	CDB databases.CommunityDatabase
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

	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid credentials"})
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

	// Check if user exists
	userResult := h.UDB.FindOne(r.Context(), bson.M{"_id": userID})
	
	var user models.User
	if err := userResult.Decode(&user); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "user not found"})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to fetch user"})
		}
		return
	}

	// TODO: Get user communities and implement member counting
	var userCommunities []models.AdminUserCommunity

	details := models.AdminUserDetails{
		ID:          user.ID,
		Email:       user.Details.Email,
		Username:    user.Details.Username,
		Active:      !user.Details.IsDeactivated,
		CreatedAt:   user.Details.CreatedAt,
		Communities: userCommunities,
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

// AdminUserResetPasswordHandler sends a password reset email for a user
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

	// Check if user exists
	userResult := h.UDB.FindOne(r.Context(), bson.M{"_id": userID})
	var user models.User
	if err := userResult.Decode(&user); err != nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "user not found"})
		return
	}

	// Create reset token
	plain, hashHex, genErr := generateResetToken()
	if genErr == nil {
		_, _ = h.RDB.InsertOne(r.Context(), models.AdminPasswordReset{
			AdminID:   primitive.NewObjectID(), // Generate new ID for reset
			TokenHash: hashHex,
			ExpiresAt: time.Now().Add(1 * time.Hour),
			CreatedAt: time.Now(),
		})
		_ = sendUserResetEmail(user.Details.Email, buildUserResetLink(os.Getenv("PUBLIC_WEB_BASE_URL"), plain))
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "reset email sent"})
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
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid user ID"})
		return
	}
	userID := pathParts[len(pathParts)-2]

	// Check if user exists
	userResult := h.UDB.FindOne(r.Context(), bson.M{"_id": userID})
	var user models.User
	if err := userResult.Decode(&user); err != nil {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "user not found"})
		return
	}

	// Generate temp password
	tempPassword := generateTempPassword()
	hash, err := bcrypt.GenerateFromPassword([]byte(tempPassword), bcrypt.DefaultCost)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to hash password"})
		return
	}

	// Update user password
	_, err = h.UDB.UpdateOne(r.Context(), bson.M{"_id": userID}, bson.M{"$set": bson.M{"user.password": string(hash), "updatedAt": time.Now()}})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to update password"})
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(tempPasswordResponse{TempPassword: tempPassword})
}

// Helper functions
func buildUserResetLink(baseURL, token string) string {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = "https://www.linespolice-cad.com"
	}
	return base + "/reset-password?token=" + token
}

func sendUserResetEmail(toEmail, resetLink string) error {
	from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
	subject := "LPC-APP Password Reset"
	to := mail.NewEmail("", toEmail)
	plain := "Reset your password using this link: " + resetLink
	html := templates.RenderAdminPasswordReset(resetLink)
	msg := mail.NewSingleEmail(from, subject, to, plain, html)
	client := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))
	_, err := client.Send(msg)
	return err
}

func generateTempPassword() string {
	// Generate a readable temp password
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

// AdminDebugUsersHandler lists all users for debugging
func (h Admin) AdminDebugUsersHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get all users
	cursor, err := h.UDB.Find(r.Context(), bson.M{}, nil)
	if err != nil {
		log.Printf("Admin debug users error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to fetch users"})
		return
	}
	defer cursor.Close(r.Context())

	var users []models.User
	if err = cursor.All(r.Context(), &users); err != nil {
		log.Printf("Admin debug users decode error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to decode users"})
		return
	}

	log.Printf("Admin debug found %d total users", len(users))

	// Return first few users for debugging
	var debugUsers []map[string]interface{}
	for i, user := range users {
		if i >= 5 { // Limit to first 5 users
			break
		}
		debugUsers = append(debugUsers, map[string]interface{}{
			"id":       user.ID,
			"email":    user.Details.Email,
			"username": user.Details.Username,
			"name":     user.Details.Name,
		})
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"totalUsers": len(users),
		"sampleUsers": debugUsers,
	})
}

// AdminDebugCommunitiesHandler lists all communities for debugging
func (h Admin) AdminDebugCommunitiesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get all communities
	cursor, err := h.CDB.Find(r.Context(), bson.M{}, nil)
	if err != nil {
		log.Printf("Admin debug communities error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to fetch communities"})
		return
	}
	defer cursor.Close(r.Context())

	var communities []models.Community
	if err = cursor.All(r.Context(), &communities); err != nil {
		log.Printf("Admin debug communities decode error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to decode communities"})
		return
	}

	log.Printf("Admin debug found %d total communities", len(communities))

	// Return first few communities for debugging
	var debugCommunities []map[string]interface{}
	for i, community := range communities {
		if i >= 5 { // Limit to first 5 communities
			break
		}
		debugCommunities = append(debugCommunities, map[string]interface{}{
			"id":   community.ID.Hex(),
			"name": community.Details.Name,
		})
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"totalCommunities": len(communities),
		"sampleCommunities": debugCommunities,
	})
}


