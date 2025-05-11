package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"time"

	"github.com/google/uuid"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"github.com/shaj13/go-guardian/auth"
	"github.com/shaj13/go-guardian/auth/strategies/bearer"
	"go.uber.org/zap"

	"github.com/shaj13/go-guardian/auth/strategies/basic"
	"github.com/shaj13/go-guardian/store"
	"go.mongodb.org/mongo-driver/bson"
	"golang.org/x/crypto/bcrypt"
)

// MiddlewareDB is a struct that holds the database
type MiddlewareDB struct {
	DB databases.UserDatabase
}

var authenticator auth.Authenticator
var cache store.Cache

// Middleware adds some basic header authentication around accessing the routes
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// TODO: rework this to use the proper format
		// Bypass authentication for all routes except login
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth/token") {
			user, err := authenticator.Authenticate(r)
			if err != nil {
				zap.S().Errorw("unauthorized",
					"url", r.URL, "error", err)
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(fmt.Sprintf(`{"error": "unauthorized", "message": "%s"}`, err.Error())))
				return
			}
			zap.S().Debugf("User %s Authenticated\n", user.UserName())
			next.ServeHTTP(w, r)

		} else {
			next.ServeHTTP(w, r)
			return
		}

	})
}

// CreateToken returns a token
func (m MiddlewareDB) CreateToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	email, _, ok := r.BasicAuth()
	if !ok {
		http.Error(w, "basic auth failed", http.StatusBadRequest)
		return
	}

	user := models.User{}
	err := m.DB.FindOne(context.Background(), bson.M{"user.email": email}).Decode(&user)
	if err != nil {
		http.Error(w, "failed to get user by email", http.StatusNotFound)
		return
	}

	token := uuid.New().String()
	authUser := auth.NewDefaultUser(email, user.ID, nil, nil)
	tokenStrategy := authenticator.Strategy(bearer.CachedStrategyKey)
	auth.Append(tokenStrategy, token, authUser, r)

	response := map[string]string{
		"token": token,
		"_id":   user.ID,
	}

	responseBody, err := json.Marshal(response)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Write(responseBody)
}

// SetupGoGuardian sets up the go-guardian middleware
func (m MiddlewareDB) SetupGoGuardian() {
	authenticator = auth.New()
	cache = store.NewFIFO(context.Background(), time.Hour*24*365*100) // 100 years ttl
	basicStrategy := basic.New(m.ValidateUser, cache)
	tokenStrategy := bearer.New(bearer.NoOpAuthenticate, cache)

	authenticator.EnableStrategy(basic.StrategyKey, basicStrategy)
	authenticator.EnableStrategy(bearer.CachedStrategyKey, tokenStrategy)
}

// ValidateUser validates a user
func (m MiddlewareDB) ValidateUser(ctx context.Context, r *http.Request, email, password string) (auth.Info, error) {
	usernameHash := sha256.Sum256([]byte(email))

	dbEmailResp := models.User{}
	err := m.DB.FindOne(context.Background(), bson.M{"user.email": email}).Decode(&dbEmailResp)
	if err != nil {
		return nil, fmt.Errorf("failed to get user by email, %v", err)
	}

	expectedUsernameHash := sha256.Sum256([]byte(dbEmailResp.Details.Email))
	usernameMatch := subtle.ConstantTimeCompare(usernameHash[:], expectedUsernameHash[:]) == 1

	err = bcrypt.CompareHashAndPassword([]byte(dbEmailResp.Details.Password), []byte(password))
	if err != nil {
		return nil, fmt.Errorf("failed to compare password")
	}

	if usernameMatch {
		// Check if the user is deactivated
		if dbEmailResp.Details.IsDeactivated {
			return nil, fmt.Errorf("account is deactivated. Please contact support to restore access")
		}

		return auth.NewDefaultUser(email, "1", nil, nil), nil
	}
	return nil, fmt.Errorf("invalid credentials")
}

// RevokeToken revokes a token
func RevokeToken(w http.ResponseWriter, r *http.Request) {
	reqToken := r.Header.Get("Authorization")
	splitToken := strings.Split(reqToken, "Bearer ")
	reqToken = splitToken[1]

	tokenStrategy := authenticator.Strategy(bearer.CachedStrategyKey)
	auth.Revoke(tokenStrategy, reqToken, r)
	body := fmt.Sprintf(`{"revoked token": "%s"}`, reqToken)
	w.Write([]byte(body))
}
