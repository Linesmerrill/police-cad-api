package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

// UserPreferences exported for testing purposes
type UserPreferences struct {
	DB  databases.UserPreferencesDatabase
	UDB databases.UserDatabase
}

// GetUserPreferencesHandler returns user preferences for a given userID
func (up UserPreferences) GetUserPreferencesHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]

	zap.S().Debugf("user_id: %v", userID)

	var userPreferences models.UserPreferences
	err := up.DB.FindOne(context.Background(), bson.M{"userId": userID}).Decode(&userPreferences)
	if err != nil {
		// If no preferences found, return empty preferences structure
		if errors.Is(err, mongo.ErrNoDocuments) {
			userPreferences = models.UserPreferences{
				UserID:               userID,
				CommunityPreferences: make(map[string]models.CommunityPreference),
			}
		} else {
			config.ErrorStatus("failed to get user preferences", http.StatusInternalServerError, w, err)
			return
		}
	}

	b, err := json.Marshal(userPreferences)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// CreateUserPreferencesHandler creates new user preferences
func (up UserPreferences) CreateUserPreferencesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var userPreferences models.UserPreferences
	err := json.NewDecoder(r.Body).Decode(&userPreferences)
	if err != nil {
		config.ErrorStatus("failed to decode request", http.StatusBadRequest, w, err)
		return
	}

	// Check if preferences already exist for this user
	existingPreferences := models.UserPreferences{}
	dbErr := up.DB.FindOne(context.Background(), bson.M{"userId": userPreferences.UserID}).Decode(&existingPreferences)
	if dbErr != nil && !errors.Is(dbErr, mongo.ErrNoDocuments) {
		// If there's a database error (other than no documents found), return it
		config.ErrorStatus("failed to check existing preferences", http.StatusInternalServerError, w, dbErr)
		return
	}

	// If preferences exist (no error or error was ErrNoDocuments but we found a document)
	if existingPreferences.ID != primitive.NilObjectID {
		config.ErrorStatus("user preferences already exist", http.StatusConflict, w, nil)
		return
	}

	// Set timestamps
	now := time.Now()
	userPreferences.CreatedAt = now
	userPreferences.UpdatedAt = now

	// Initialize empty community preferences if not provided
	if userPreferences.CommunityPreferences == nil {
		userPreferences.CommunityPreferences = make(map[string]models.CommunityPreference)
	}

	res, err := up.DB.InsertOne(context.Background(), userPreferences)
	if err != nil {
		config.ErrorStatus("failed to create user preferences", http.StatusInternalServerError, w, err)
		return
	}

	insertedID := res.Decode()
	if insertedID == nil {
		config.ErrorStatus("failed to retrieve inserted ID", http.StatusInternalServerError, w, nil)
		return
	}

	objectID, ok := insertedID.(primitive.ObjectID)
	if !ok {
		config.ErrorStatus("failed to convert inserted ID to ObjectID", http.StatusInternalServerError, w, nil)
		return
	}
	userPreferences.ID = objectID

	b, err := json.Marshal(userPreferences)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	w.Write(b)
}

// UpdateUserPreferencesHandler updates existing user preferences
func (up UserPreferences) UpdateUserPreferencesHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]

	var updateData map[string]interface{}
	err := json.NewDecoder(r.Body).Decode(&updateData)
	if err != nil {
		config.ErrorStatus("failed to decode request", http.StatusBadRequest, w, err)
		return
	}

	// Add updated timestamp
	updateData["updatedAt"] = time.Now()

	// Use upsert to create if doesn't exist, update if it does
	opts := options.Update().SetUpsert(true)
	_, err = up.DB.UpdateOne(
		context.Background(),
		bson.M{"userId": userID},
		bson.M{"$set": updateData},
		opts,
	)
	if err != nil {
		config.ErrorStatus("failed to update user preferences", http.StatusInternalServerError, w, err)
		return
	}

	// If betaCivDashboard preference changed, notify admin metrics listeners
	if _, hasBeta := updateData["betaCivDashboard"]; hasBeta {
		go up.notifyMetricsUpdate()
	}

	// Return the updated preferences
	up.GetUserPreferencesHandler(w, r)
}

// UpdateDepartmentOrderHandler updates the department order for a specific community
func (up UserPreferences) UpdateDepartmentOrderHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]
	communityID := mux.Vars(r)["community_id"]

	var requestBody struct {
		DepartmentOrder []models.DepartmentOrder `json:"departmentOrder"`
	}

	err := json.NewDecoder(r.Body).Decode(&requestBody)
	if err != nil {
		config.ErrorStatus("failed to decode request", http.StatusBadRequest, w, err)
		return
	}

	// Build the update query to set the department order for the specific community
	updateQuery := bson.M{
		"$set": bson.M{
			"communityPreferences." + communityID + ".departmentOrder": requestBody.DepartmentOrder,
			"updatedAt": time.Now(),
		},
	}

	// Use upsert to create if doesn't exist, update if it does
	opts := options.Update().SetUpsert(true)
	_, err = up.DB.UpdateOne(
		context.Background(),
		bson.M{"userId": userID},
		updateQuery,
		opts,
	)
	if err != nil {
		config.ErrorStatus("failed to update department order", http.StatusInternalServerError, w, err)
		return
	}

	// Return the updated preferences
	up.GetUserPreferencesHandler(w, r)
}

// GetDepartmentOrderHandler returns the department order for a specific community
func (up UserPreferences) GetDepartmentOrderHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]
	communityID := mux.Vars(r)["community_id"]

	var userPreferences models.UserPreferences
	err := up.DB.FindOne(context.Background(), bson.M{"userId": userID}).Decode(&userPreferences)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			// Return empty department order if no preferences exist
			response := map[string]interface{}{
				"departmentOrder": []models.DepartmentOrder{},
			}
			json.NewEncoder(w).Encode(response)
			return
		}
		config.ErrorStatus("failed to get user preferences", http.StatusInternalServerError, w, err)
		return
	}

	// Get department order for the specific community
	communityPref, exists := userPreferences.CommunityPreferences[communityID]
	if !exists {
		// Return empty department order if community doesn't exist in preferences
		response := map[string]interface{}{
			"departmentOrder": []models.DepartmentOrder{},
		}
		json.NewEncoder(w).Encode(response)
		return
	}

	response := map[string]interface{}{
		"departmentOrder": communityPref.DepartmentOrder,
	}
	json.NewEncoder(w).Encode(response)
}

// DeleteUserPreferencesHandler deletes user preferences
func (up UserPreferences) DeleteUserPreferencesHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]

	err := up.DB.DeleteOne(context.Background(), bson.M{"userId": userID})
	if err != nil {
		config.ErrorStatus("failed to delete user preferences", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetBetaDashboardMetricsHandler returns adoption metrics for the beta civilian dashboard
func (up UserPreferences) GetBetaDashboardMetricsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	optedIn, err := up.DB.CountDocuments(ctx, bson.M{"betaCivDashboard": true})
	if err != nil {
		config.ErrorStatus("failed to count beta dashboard opt-ins", http.StatusInternalServerError, w, err)
		return
	}

	total, err := up.UDB.CountDocuments(ctx, bson.M{})
	if err != nil {
		config.ErrorStatus("failed to count total users", http.StatusInternalServerError, w, err)
		return
	}

	classic := total - optedIn

	response := map[string]int64{
		"optedIn": optedIn,
		"classic": classic,
		"total":   total,
	}

	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// notifyMetricsUpdate sends a webhook to the Node.js server to broadcast
// updated beta dashboard metrics via Socket.IO to admin console listeners.
// Runs in a goroutine to avoid blocking the HTTP response.
func (up UserPreferences) notifyMetricsUpdate() {
	nodeServerURL := os.Getenv("NODE_SERVER_WEBHOOK_URL")
	apiKey := os.Getenv("NODE_SERVER_API_KEY")

	if nodeServerURL == "" {
		return
	}

	// Derive metrics broadcast URL from the panic broadcast URL
	metricsURL := strings.Replace(nodeServerURL, "/internal/panic-broadcast", "/internal/metrics-broadcast", 1)

	ctx := context.Background()

	optedIn, err := up.DB.CountDocuments(ctx, bson.M{"betaCivDashboard": true})
	if err != nil {
		zap.S().Errorf("notifyMetricsUpdate: failed to count opted-in users: %v", err)
		return
	}

	total, err := up.UDB.CountDocuments(ctx, bson.M{})
	if err != nil {
		zap.S().Errorf("notifyMetricsUpdate: failed to count total users: %v", err)
		return
	}

	classic := total - optedIn

	payload := map[string]interface{}{
		"event": "beta_metrics_updated",
		"data": map[string]int64{
			"optedIn": optedIn,
			"classic": classic,
			"total":   total,
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		zap.S().Errorf("notifyMetricsUpdate: failed to marshal payload: %v", err)
		return
	}

	req, err := http.NewRequest("POST", metricsURL, bytes.NewBuffer(jsonData))
	if err != nil {
		zap.S().Errorf("notifyMetricsUpdate: failed to create request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-API-Key", apiKey)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		zap.S().Errorf("notifyMetricsUpdate: failed to send webhook: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		zap.S().Warnf("notifyMetricsUpdate: webhook returned status %d", resp.StatusCode)
	}
}
