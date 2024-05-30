package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"time"

	"github.com/google/uuid"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/shaj13/go-guardian/auth"
	"github.com/shaj13/go-guardian/auth/strategies/bearer"

	"github.com/shaj13/go-guardian/auth/strategies/basic"
	"github.com/shaj13/go-guardian/store"
	"go.mongodb.org/mongo-driver/bson"
	"go.uber.org/zap"
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
		user, err := authenticator.Authenticate(r)
		if err != nil {
			zap.S().Errorw("unauthorized",
				"url", r.URL)
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}
		zap.S().Debugf("User %s Authenticated\n", user.UserName())
		next.ServeHTTP(w, r)
	})
}

// CreateToken returns a token
func CreateToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	email, _, ok := r.BasicAuth()
	if !ok {
		http.Error(w, "basic auth failed", http.StatusUnauthorized)
		return
	}
	token := uuid.New().String()
	user := auth.NewDefaultUser(email, "1", nil, nil)
	tokenStrategy := authenticator.Strategy(bearer.CachedStrategyKey)
	auth.Append(tokenStrategy, token, user, r)
	body := fmt.Sprintf(`{"token": "%s"}`, token)
	w.Write([]byte(body))
}

// SetupGoGuardian sets up the go-guardian middleware
func (m MiddlewareDB) SetupGoGuardian() {
	authenticator = auth.New()
	cache = store.NewFIFO(context.Background(), time.Minute*10)
	basicStrategy := basic.New(m.ValidateUser, cache)
	tokenStrategy := bearer.New(bearer.NoOpAuthenticate, cache)

	authenticator.EnableStrategy(basic.StrategyKey, basicStrategy)
	authenticator.EnableStrategy(bearer.CachedStrategyKey, tokenStrategy)
}

// ValidateUser validates a user
func (m MiddlewareDB) ValidateUser(ctx context.Context, r *http.Request, email, password string) (auth.Info, error) {
	usernameHash := sha256.Sum256([]byte(email))

	// fetch email & pass from db
	dbEmailResp, err := m.DB.Find(context.Background(), bson.M{"user.email": email})
	if err != nil {
		return nil, fmt.Errorf("failed to get user by ID")
	}
	if len(dbEmailResp) == 0 {
		return nil, fmt.Errorf("no matching email found")
	}

	expectedUsernameHash := sha256.Sum256([]byte(dbEmailResp[0].Details.Email))
	usernameMatch := subtle.ConstantTimeCompare(usernameHash[:], expectedUsernameHash[:]) == 1

	err = bcrypt.CompareHashAndPassword([]byte(dbEmailResp[0].Details.Password), []byte(password))
	if err != nil {
		return nil, fmt.Errorf("failed to compare password")
	}

	if usernameMatch {
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
