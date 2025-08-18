package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
	"go.mongodb.org/mongo-driver/bson"
	"golang.org/x/crypto/bcrypt"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	templates "github.com/linesmerrill/police-cad-api/templates/html"
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


