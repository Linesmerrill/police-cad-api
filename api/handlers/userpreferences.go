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
	if userPreferences.BetaCommandDashboard {
		userPreferences.CommandDashboardOptedAt = now
	}

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
	now := time.Now()
	updateData["updatedAt"] = now

	// Track when the user opted into the command dashboard
	if val, ok := updateData["betaCommandDashboard"]; ok {
		if enabled, isBool := val.(bool); isBool && enabled {
			updateData["commandDashboardOptedAt"] = now
		}
	}

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

	// If betaCivDashboard or betaCommandDashboard preference changed, notify admin metrics listeners
	if _, hasBeta := updateData["betaCivDashboard"]; hasBeta {
		go up.notifyMetricsUpdate()
	}
	if _, hasBetaCmd := updateData["betaCommandDashboard"]; hasBetaCmd {
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

// GetBetaDashboardMetricsHandler returns adoption metrics for both the beta civilian
// dashboard and the command dashboard.
func (up UserPreferences) GetBetaDashboardMetricsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	optedIn, err := up.DB.CountDocuments(ctx, bson.M{"betaCivDashboard": true})
	if err != nil {
		config.ErrorStatus("failed to count beta dashboard opt-ins", http.StatusInternalServerError, w, err)
		return
	}

	cmdOptedIn, err := up.DB.CountDocuments(ctx, bson.M{"betaCommandDashboard": true})
	if err != nil {
		config.ErrorStatus("failed to count command dashboard opt-ins", http.StatusInternalServerError, w, err)
		return
	}

	total, err := up.UDB.CountDocuments(ctx, bson.M{})
	if err != nil {
		config.ErrorStatus("failed to count total users", http.StatusInternalServerError, w, err)
		return
	}

	classic := total - optedIn

	response := map[string]interface{}{
		"optedIn":        optedIn,
		"classic":        classic,
		"total":          total,
		"cmdOptedIn":     cmdOptedIn,
		"cmdClassic":     total - cmdOptedIn,
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

// GetBetaDashboardMetricsDailyHandler returns daily beta adoption and user registration
// counts for the past 7 days, used for trend charts on the admin metrics panel.
func (up UserPreferences) GetBetaDashboardMetricsDailyHandler(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	// Calculate 8 days ago (to ensure we cover 7 full days)
	now := time.Now().UTC()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	sevenDaysAgo := startOfToday.AddDate(0, 0, -6) // 6 days back + today = 7 days

	// Pipeline: daily beta opt-ins (users who opted in, grouped by updatedAt date)
	betaPipeline := bson.A{
		bson.M{"$match": bson.M{
			"betaCivDashboard": true,
			"updatedAt":        bson.M{"$gte": sevenDaysAgo},
		}},
		bson.M{"$group": bson.M{
			"_id":   bson.M{"$dateToString": bson.M{"format": "%Y-%m-%d", "date": "$updatedAt"}},
			"count": bson.M{"$sum": 1},
		}},
		bson.M{"$sort": bson.M{"_id": 1}},
	}

	betaCursor, err := up.DB.Aggregate(ctx, betaPipeline)
	if err != nil {
		config.ErrorStatus("failed to aggregate beta daily metrics", http.StatusInternalServerError, w, err)
		return
	}

	type dailyCount struct {
		Date  string `json:"date" bson:"_id"`
		Count int    `json:"count" bson:"count"`
	}

	var betaDaily []dailyCount
	if err := betaCursor.All(ctx, &betaDaily); err != nil {
		config.ErrorStatus("failed to decode beta daily metrics", http.StatusInternalServerError, w, err)
		return
	}

	// Pipeline: daily command dashboard opt-ins
	// Use commandDashboardOptedAt if available, fall back to updatedAt for older records
	cmdPipeline := bson.A{
		bson.M{"$match": bson.M{
			"betaCommandDashboard": true,
		}},
		bson.M{"$addFields": bson.M{
			"_optDate": bson.M{"$ifNull": bson.A{"$commandDashboardOptedAt", "$updatedAt"}},
		}},
		bson.M{"$match": bson.M{
			"_optDate": bson.M{"$gte": sevenDaysAgo},
		}},
		bson.M{"$group": bson.M{
			"_id":   bson.M{"$dateToString": bson.M{"format": "%Y-%m-%d", "date": "$_optDate"}},
			"count": bson.M{"$sum": 1},
		}},
		bson.M{"$sort": bson.M{"_id": 1}},
	}

	cmdCursor, err := up.DB.Aggregate(ctx, cmdPipeline)
	if err != nil {
		config.ErrorStatus("failed to aggregate command dashboard daily metrics", http.StatusInternalServerError, w, err)
		return
	}

	var cmdDaily []dailyCount
	if err := cmdCursor.All(ctx, &cmdDaily); err != nil {
		config.ErrorStatus("failed to decode command dashboard daily metrics", http.StatusInternalServerError, w, err)
		return
	}

	// Pipeline: daily new user registrations (grouped by user.createdAt date)
	userPipeline := bson.A{
		bson.M{"$match": bson.M{
			"user.createdAt": bson.M{"$gte": sevenDaysAgo},
		}},
		bson.M{"$group": bson.M{
			"_id":   bson.M{"$dateToString": bson.M{"format": "%Y-%m-%d", "date": "$user.createdAt"}},
			"count": bson.M{"$sum": 1},
		}},
		bson.M{"$sort": bson.M{"_id": 1}},
	}

	userCursor, err := up.UDB.Aggregate(ctx, userPipeline)
	if err != nil {
		config.ErrorStatus("failed to aggregate user daily metrics", http.StatusInternalServerError, w, err)
		return
	}

	var userDaily []dailyCount
	if err := userCursor.All(ctx, &userDaily); err != nil {
		config.ErrorStatus("failed to decode user daily metrics", http.StatusInternalServerError, w, err)
		return
	}

	// Build a map for quick lookup, then fill in all 7 days (including zero-count days)
	betaMap := make(map[string]int)
	for _, d := range betaDaily {
		betaMap[d.Date] = d.Count
	}
	cmdMap := make(map[string]int)
	for _, d := range cmdDaily {
		cmdMap[d.Date] = d.Count
	}
	userMap := make(map[string]int)
	for _, d := range userDaily {
		userMap[d.Date] = d.Count
	}

	type dayEntry struct {
		Date         string `json:"date"`
		BetaOptIns   int    `json:"betaOptIns"`
		CmdOptIns    int    `json:"cmdOptIns"`
		NewUsers     int    `json:"newUsers"`
	}

	days := make([]dayEntry, 7)
	for i := 0; i < 7; i++ {
		date := sevenDaysAgo.AddDate(0, 0, i).Format("2006-01-02")
		days[i] = dayEntry{
			Date:       date,
			BetaOptIns: betaMap[date],
			CmdOptIns:  cmdMap[date],
			NewUsers:   userMap[date],
		}
	}

	response := map[string]interface{}{
		"days": days,
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

	cmdOptedIn, err := up.DB.CountDocuments(ctx, bson.M{"betaCommandDashboard": true})
	if err != nil {
		zap.S().Errorf("notifyMetricsUpdate: failed to count command dashboard opt-ins: %v", err)
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
		"data": map[string]interface{}{
			"optedIn":    optedIn,
			"classic":    classic,
			"total":      total,
			"cmdOptedIn": cmdOptedIn,
			"cmdClassic": total - cmdOptedIn,
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
