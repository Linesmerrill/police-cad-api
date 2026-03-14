package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/config"
)

// SendToneHandler dispatches a tone alert to targeted departments.
func (c Community) SendToneHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	var request struct {
		ToneType            string   `json:"toneType"`
		ToneName            string   `json:"toneName"`
		TargetDeptIDs       []string `json:"targetDeptIds"`
		TriggeredByID       string   `json:"triggeredById"`
		TriggeredByName     string   `json:"triggeredByName"`
		TriggeredByCallSign string   `json:"triggeredByCallSign"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if request.ToneType == "" || request.ToneName == "" || len(request.TargetDeptIDs) == 0 || request.TriggeredByID == "" || request.TriggeredByName == "" {
		config.ErrorStatus("toneType, toneName, targetDeptIds, triggeredById, and triggeredByName are required", http.StatusBadRequest, w, fmt.Errorf("missing required fields"))
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	toneLog := models.ToneLog{
		CommunityID:         communityID,
		ToneType:            request.ToneType,
		ToneName:            request.ToneName,
		TargetDeptIDs:       request.TargetDeptIDs,
		TriggeredByID:       request.TriggeredByID,
		TriggeredByName:     request.TriggeredByName,
		TriggeredByCallSign: request.TriggeredByCallSign,
		CreatedAt:           now,
	}

	// Insert tone log entry
	if c.TLDB != nil {
		_, err = c.TLDB.InsertOne(context.Background(), toneLog)
		if err != nil {
			zap.S().Errorf("SendToneHandler: failed to insert tone log: %v", err)
			// Non-fatal: continue with broadcast even if logging fails
		}
	}

	// Broadcast via WebSocket
	toneData := map[string]interface{}{
		"toneType":            request.ToneType,
		"toneName":            request.ToneName,
		"targetDeptIds":       request.TargetDeptIDs,
		"triggeredById":       request.TriggeredByID,
		"triggeredByName":     request.TriggeredByName,
		"triggeredByCallSign": request.TriggeredByCallSign,
		"communityId":         communityID,
		"createdAt":           now,
	}

	broadcastPanicAlertEvent("tone_activated", toneData)

	// Notify Node.js server for Socket.IO relay
	go c.notifyNodeServerPanic("tone_activated", toneData)

	// Send push notifications to targeted department members
	go c.sendTonePushNotifications(cID, communityID, request)

	// Audit log
	logAudit(c.ALDB, cID, "tone.sent", "tone", request.TriggeredByID, request.TriggeredByName, "", request.ToneName, map[string]interface{}{
		"toneType":      request.ToneType,
		"targetDeptIds": request.TargetDeptIDs,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Tone sent successfully",
	})
}

// sendTonePushNotifications sends push notifications to members clocked into the targeted departments.
func (c Community) sendTonePushNotifications(cID primitive.ObjectID, communityID string, request struct {
	ToneType            string   `json:"toneType"`
	ToneName            string   `json:"toneName"`
	TargetDeptIDs       []string `json:"targetDeptIds"`
	TriggeredByID       string   `json:"triggeredById"`
	TriggeredByName     string   `json:"triggeredByName"`
	TriggeredByCallSign string   `json:"triggeredByCallSign"`
}) {
	if c.PTDB == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		zap.S().Errorf("sendTonePushNotifications: failed to fetch community %s: %v", communityID, err)
		return
	}

	// Build a set of target department IDs for fast lookup
	targetSet := make(map[string]bool, len(request.TargetDeptIDs))
	for _, id := range request.TargetDeptIDs {
		targetSet[id] = true
	}

	// Collect user IDs whose active department is in the target set
	var targetUserIDs []string
	for userID, member := range community.Details.Members {
		if userID == request.TriggeredByID {
			continue
		}
		if targetSet[member.ActiveDepartmentID] {
			targetUserIDs = append(targetUserIDs, userID)
		}
	}

	if len(targetUserIDs) == 0 {
		return
	}

	tokens, err := c.PTDB.Find(ctx, bson.M{"userId": bson.M{"$in": targetUserIDs}})
	if err != nil || len(tokens) == 0 {
		return
	}

	var pushTokens []string
	for _, pt := range tokens {
		pushTokens = append(pushTokens, pt.Token)
	}

	title := fmt.Sprintf("TONE OUT — %s", request.ToneName)
	body := fmt.Sprintf("%s has activated a %s tone. Stand by for voice dispatch.", request.TriggeredByName, request.ToneName)
	data := map[string]interface{}{
		"type":          "tone_alert",
		"toneType":      request.ToneType,
		"communityId":   communityID,
		"targetDeptIds": request.TargetDeptIDs,
	}

	if err := SendExpoPushNotifications(pushTokens, title, body, data); err != nil {
		zap.S().Errorf("sendTonePushNotifications: failed to send: %v", err)
	}
}

// GetToneLogHandler retrieves the tone log history for a community.
func (c Community) GetToneLogHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	if c.TLDB == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "logs": []interface{}{}})
		return
	}

	filter := bson.M{"communityId": communityID}
	opts := options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: -1}}).
		SetLimit(int64(limit))

	cursor, err := c.TLDB.Find(context.Background(), filter, opts)
	if err != nil {
		config.ErrorStatus("failed to fetch tone logs", http.StatusInternalServerError, w, err)
		return
	}

	var logs []models.ToneLog
	if err := cursor.All(context.Background(), &logs); err != nil {
		config.ErrorStatus("failed to decode tone logs", http.StatusInternalServerError, w, err)
		return
	}

	if logs == nil {
		logs = []models.ToneLog{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"logs":    logs,
	})
}

// GetToneGroupsHandler retrieves preset and custom tone groups for a community.
func (c Community) GetToneGroupsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community", http.StatusNotFound, w, err)
		return
	}

	// Build preset tone groups from department templates.
	// Template.Name is the legacy embedded template name (e.g. "police", "fire", "ems").
	// It is always populated regardless of whether the department uses the new TemplateRef system.
	var presets []map[string]interface{}
	for _, dept := range community.Details.Departments {
		// Use per-department ToneSound override if set, otherwise derive from template
		toneSound := dept.ToneSound
		if toneSound == "" {
			templateName := strings.ToLower(dept.Template.Name)
			switch templateName {
			case "police":
				toneSound = "leo"
			case "fire":
				toneSound = "fd"
			case "ems":
				toneSound = "ems"
			default:
				continue // skip dispatch, civilian, judicial
			}
		}

		presets = append(presets, map[string]interface{}{
			"_id":           dept.ID,
			"name":          dept.Name,
			"departmentIds": []string{dept.ID.Hex()},
			"toneSound":     toneSound,
			"isPreset":      true,
		})
	}

	customGroups := community.Details.CustomToneGroups
	if customGroups == nil {
		customGroups = []models.CustomToneGroup{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"presets":      presets,
		"customGroups": customGroups,
	})
}

// CreateToneGroupHandler creates a custom tone group for a community.
func (c Community) CreateToneGroupHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	var request struct {
		Name          string   `json:"name"`
		DepartmentIDs []string `json:"departmentIds"`
		ToneSound     string   `json:"toneSound"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if request.Name == "" || len(request.DepartmentIDs) == 0 || request.ToneSound == "" {
		config.ErrorStatus("name, departmentIds, and toneSound are required", http.StatusBadRequest, w, fmt.Errorf("missing required fields"))
		return
	}

	group := models.CustomToneGroup{
		ID:            primitive.NewObjectID(),
		Name:          request.Name,
		DepartmentIDs: request.DepartmentIDs,
		ToneSound:     request.ToneSound,
		CreatedBy:     resolveActorFromRequest(r),
		CreatedAt:     primitive.NewDateTimeFromTime(time.Now()),
	}

	// Initialize customToneGroups if null
	initFilter := bson.M{"_id": cID, "community.customToneGroups": nil}
	initUpdate := bson.M{"$set": bson.M{"community.customToneGroups": bson.A{}}}
	_ = c.DB.UpdateOne(context.Background(), initFilter, initUpdate)

	filter := bson.M{"_id": cID}
	update := bson.M{
		"$push": bson.M{"community.customToneGroups": group},
	}

	if err := c.DB.UpdateOne(context.Background(), filter, update); err != nil {
		config.ErrorStatus("failed to create tone group", http.StatusInternalServerError, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "tone_group.created", "tone", actorID, resolveActorName(c.UDB, actorID), group.ID.Hex(), group.Name, nil)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"group":   group,
	})
}

// DeleteToneGroupHandler deletes a custom tone group from a community.
func (c Community) DeleteToneGroupHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]
	groupID := vars["groupId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	groupObjID, err := primitive.ObjectIDFromHex(groupID)
	if err != nil {
		config.ErrorStatus("invalid group ID", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": cID}
	update := bson.M{
		"$pull": bson.M{
			"community.customToneGroups": bson.M{"_id": groupObjID},
		},
	}

	if err := c.DB.UpdateOne(context.Background(), filter, update); err != nil {
		config.ErrorStatus("failed to delete tone group", http.StatusInternalServerError, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "tone_group.deleted", "tone", actorID, resolveActorName(c.UDB, actorID), groupID, "", nil)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Tone group deleted successfully",
	})
}
