package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/bson"
	"golang.org/x/crypto/bcrypt"

	"github.com/linesmerrill/police-cad-api/databases"
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


