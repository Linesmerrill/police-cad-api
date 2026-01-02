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
	"go.mongodb.org/mongo-driver/mongo"
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
			// Check if Basic Auth header is present
			email, _, hasAuth := r.BasicAuth()
			if !hasAuth {
				zap.S().Warnw("auth/token: missing basic auth header",
					"url", r.URL.Path,
					"method", r.Method)
				w.Header().Set("WWW-Authenticate", `Basic realm="restricted"`)
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error": "unauthorized", "message": "Basic authentication required"}`))
				return
			}

			zap.S().Debugw("auth/token: attempting authentication",
				"email", email,
				"url", r.URL.Path)

			user, err := authenticator.Authenticate(r)
			if err != nil {
				zap.S().Errorw("auth/token: authentication failed",
					"url", r.URL.Path,
					"email", email,
					"error", err)
				w.Header().Set("WWW-Authenticate", `Basic realm="restricted"`)
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(fmt.Sprintf(`{"error": "unauthorized", "message": "%s"}`, err.Error())))
				return
			}
			zap.S().Infow("auth/token: authentication successful",
				"email", email,
				"username", user.UserName())
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

	email = strings.ToLower(email)

	// Use request context with timeout for database query
	ctx, cancel := WithQueryTimeout(r.Context())
	defer cancel()

	user := models.User{}
	err := m.DB.FindOne(ctx, bson.M{
		"$expr": bson.M{
			"$eq": []interface{}{
				bson.M{"$toLower": "$user.email"},
				email,
			},
		},
	}).Decode(&user)
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
	usernameHash := sha256.Sum256([]byte(strings.ToLower(email)))

	email = strings.ToLower(email)

	// Use context with timeout for database query (preserves request trace if available)
	queryCtx, cancel := WithQueryTimeout(ctx)
	defer cancel()

	dbEmailResp := models.User{}
	err := m.DB.FindOne(queryCtx, bson.M{
		"$expr": bson.M{
			"$eq": []interface{}{
				bson.M{"$toLower": "$user.email"},
				email,
			},
		},
	}).Decode(&dbEmailResp)
	if err != nil {
		// User not found is normal during authentication - log as debug/warn, not error
		// Only log as error if it's a database connection issue (timeout, network, etc.)
		if err == mongo.ErrNoDocuments || strings.Contains(err.Error(), "no documents") {
			zap.S().Debugw("ValidateUser: user not found",
				"email", email)
		} else {
			// Database connection/timeout errors are actual errors
			zap.S().Errorw("ValidateUser: database error while looking up user",
				"email", email,
				"error", err)
		}
		return nil, fmt.Errorf("failed to validate user by email, %v", err)
	}

	expectedUsernameHash := sha256.Sum256([]byte(strings.ToLower(dbEmailResp.Details.Email)))
	usernameMatch := subtle.ConstantTimeCompare(usernameHash[:], expectedUsernameHash[:]) == 1

	if !usernameMatch {
		zap.S().Warnw("ValidateUser: username hash mismatch",
			"email", email,
			"dbEmail", dbEmailResp.Details.Email)
		return nil, fmt.Errorf("invalid credentials")
	}

	// Check if password field is empty
	if dbEmailResp.Details.Password == "" {
		zap.S().Errorw("ValidateUser: password field is empty",
			"email", email,
			"userID", dbEmailResp.ID)
		return nil, fmt.Errorf("invalid credentials")
	}

	err = bcrypt.CompareHashAndPassword([]byte(dbEmailResp.Details.Password), []byte(password))
	if err != nil {
		zap.S().Warnw("ValidateUser: password mismatch",
			"email", email,
			"userID", dbEmailResp.ID,
			"error", err)
		return nil, fmt.Errorf("invalid credentials")
	}

	// Check if the user is deactivated
	if dbEmailResp.Details.IsDeactivated {
		zap.S().Warnw("ValidateUser: account is deactivated",
			"email", email,
			"userID", dbEmailResp.ID)
		return nil, fmt.Errorf("account is deactivated. Please contact support to restore access")
	}

	return auth.NewDefaultUser(email, "1", nil, nil), nil
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
