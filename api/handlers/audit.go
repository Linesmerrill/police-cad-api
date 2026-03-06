package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// logAudit inserts an audit log entry in a fire-and-forget goroutine.
// It never blocks the caller or causes the parent request to fail.
func logAudit(aldb databases.AuditLogDatabase, communityID primitive.ObjectID, action, category, actorID, actorName, targetID, targetName string, details map[string]interface{}) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		entry := models.AuditLog{
			CommunityID: communityID,
			Action:      action,
			Category:    category,
			ActorID:     actorID,
			ActorName:   actorName,
			TargetID:    targetID,
			TargetName:  targetName,
			Details:     details,
			CreatedAt:   time.Now(),
		}

		if _, err := aldb.InsertOne(ctx, entry); err != nil {
			zap.S().Errorw("failed to insert audit log",
				"action", action,
				"communityId", communityID.Hex(),
				"error", err)
		}
	}()
}

// resolveActorName looks up a username by user ID. Returns empty string on failure.
func resolveActorName(udb databases.UserDatabase, actorID string) string {
	if actorID == "" {
		return ""
	}
	objID, err := primitive.ObjectIDFromHex(actorID)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var user models.User
	if err := udb.FindOne(ctx, bson.M{"_id": objID}).Decode(&user); err != nil {
		return ""
	}
	return user.Details.Username
}

// GetCommunityAuditLogsHandler returns paginated audit logs for a community.
// GET /api/v2/community/{communityId}/audit-logs
// Query params: page, limit, category, action, actorId
func (c Community) GetCommunityAuditLogsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Validate community ID
	communityObjID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid communityId", http.StatusBadRequest, w, err)
		return
	}

	// Permission check: user must have "view audit logs" or "administrator"
	actorID := api.GetAuthenticatedUserIDFromContext(r.Context())
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Fetch community to check permissions
	community, err := c.DB.FindOne(ctx, bson.M{"_id": communityObjID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	hasPermission := false
	if community.Details.OwnerID == actorID {
		hasPermission = true
	} else {
		for _, role := range community.Details.Roles {
			isMember := false
			for _, m := range role.Members {
				if m == actorID {
					isMember = true
					break
				}
			}
			if !isMember {
				continue
			}
			for _, p := range role.Permissions {
				if p.Enabled && (p.Name == "view audit logs" || p.Name == "administrator") {
					hasPermission = true
					break
				}
			}
			if hasPermission {
				break
			}
		}
	}
	if !hasPermission {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, fmt.Errorf("user lacks view audit logs permission"))
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	// Build filter
	filter := bson.M{"communityId": communityObjID}
	if category := r.URL.Query().Get("category"); category != "" {
		filter["category"] = category
	}
	if action := r.URL.Query().Get("action"); action != "" {
		filter["action"] = action
	}
	if filterActorID := r.URL.Query().Get("actorId"); filterActorID != "" {
		filter["actorId"] = filterActorID
	}

	// Count + fetch in parallel
	type countResult struct {
		count int64
		err   error
	}
	countCh := make(chan countResult, 1)
	go func() {
		c, e := c.ALDB.CountDocuments(ctx, filter)
		countCh <- countResult{c, e}
	}()

	opts := options.Find().
		SetSkip(int64(offset)).
		SetLimit(int64(limit)).
		SetSort(bson.D{{"createdAt", -1}})

	cursor, err := c.ALDB.Find(ctx, filter, opts)
	if err != nil {
		config.ErrorStatus("failed to fetch audit logs", http.StatusInternalServerError, w, err)
		return
	}

	var logs []models.AuditLog
	if err := cursor.All(ctx, &logs); err != nil {
		config.ErrorStatus("failed to decode audit logs", http.StatusInternalServerError, w, err)
		return
	}

	cr := <-countCh
	if cr.err != nil {
		config.ErrorStatus("failed to count audit logs", http.StatusInternalServerError, w, cr.err)
		return
	}

	totalCount := cr.count
	totalPages := int((totalCount + int64(limit) - 1) / int64(limit))

	if logs == nil {
		logs = []models.AuditLog{}
	}

	response := map[string]interface{}{
		"data":       logs,
		"totalCount": totalCount,
		"page":       page,
		"limit":      limit,
		"pagination": map[string]interface{}{
			"currentPage": page,
			"totalPages":  totalPages,
			"totalCount":  totalCount,
			"hasNextPage": page < totalPages,
			"hasPrevPage": page > 1,
			"limit":       limit,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
