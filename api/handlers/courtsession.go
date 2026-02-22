package handlers

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// CourtSession exported for testing purposes
type CourtSession struct {
	DB   databases.CourtSessionDatabase
	CCDB databases.CourtCaseDatabase
	ChDB databases.CourtChatDatabase
}

// CreateCourtSessionHandler creates a new court session with a docket
func (cs CourtSession) CreateCourtSessionHandler(w http.ResponseWriter, r *http.Request) {
	// Decode into a raw map first so we can handle ISO date strings for scheduledStart/scheduledEnd
	var raw map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Re-marshal and decode into the struct, but strip out scheduledStart/scheduledEnd first
	// since they come as ISO strings but primitive.DateTime expects a number
	rawStart, hasStart := raw["scheduledStart"]
	rawEnd, hasEnd := raw["scheduledEnd"]
	delete(raw, "scheduledStart")
	delete(raw, "scheduledEnd")

	b, _ := json.Marshal(raw)
	var session models.CourtSession
	if err := json.Unmarshal(b, &session.Details); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Parse scheduledStart/scheduledEnd from ISO strings
	if hasStart {
		if s, ok := rawStart.(string); ok && s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				session.Details.ScheduledStart = primitive.NewDateTimeFromTime(t)
			}
		}
	}
	if hasEnd {
		if s, ok := rawEnd.(string); ok && s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				session.Details.ScheduledEnd = primitive.NewDateTimeFromTime(t)
			}
		}
	}

	session.ID = primitive.NewObjectID()
	now := primitive.NewDateTimeFromTime(time.Now())
	session.Details.CreatedAt = now
	session.Details.UpdatedAt = now

	if session.Details.Status == "" {
		session.Details.Status = "scheduled"
	}
	if session.Details.Participants == nil {
		session.Details.Participants = []models.SessionParticipant{}
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Initialize docket entry statuses and enrich with civilian info from court cases
	for i := range session.Details.Docket {
		if session.Details.Docket[i].Status == "" {
			session.Details.Docket[i].Status = "pending"
		}
		// Look up the court case to get civilian name and userID
		caseID, err := primitive.ObjectIDFromHex(session.Details.Docket[i].CourtCaseID)
		if err == nil {
			courtCase, err := cs.CCDB.FindOne(ctx, bson.M{"_id": caseID})
			if err == nil && courtCase != nil {
				session.Details.Docket[i].CivilianName = courtCase.Details.CivilianName
				session.Details.Docket[i].UserID = courtCase.Details.UserID
			}
		}
	}

	_, err := cs.DB.InsertOne(ctx, session)
	if err != nil {
		config.ErrorStatus("failed to create court session", http.StatusInternalServerError, w, err)
		return
	}

	// Link court cases to this session
	for _, entry := range session.Details.Docket {
		caseID, err := primitive.ObjectIDFromHex(entry.CourtCaseID)
		if err != nil {
			continue
		}
		_ = cs.CCDB.UpdateOne(ctx,
			bson.M{"_id": caseID},
			bson.M{"$set": bson.M{
				"courtCase.courtSessionID": session.ID.Hex(),
				"courtCase.updatedAt":      now,
			}},
		)
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Court session created successfully",
		"id":      session.ID.Hex(),
	})
}

// GetCourtSessionByIDHandler returns a court session by ID
func (cs CourtSession) GetCourtSessionByIDHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := mux.Vars(r)["session_id"]

	bID, err := primitive.ObjectIDFromHex(sessionID)
	if err != nil {
		config.ErrorStatus("invalid court session ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	dbResp, err := cs.DB.FindOne(ctx, bson.M{"_id": bID})
	if err != nil {
		config.ErrorStatus("failed to find court session", http.StatusNotFound, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(dbResp)
}

// GetCourtSessionsByCommunityHandler returns paginated court sessions for a community
func (cs CourtSession) GetCourtSessionsByCommunityHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["community_id"]
	status := r.URL.Query().Get("status")
	departmentID := r.URL.Query().Get("departmentId")
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 10
	}
	limit64 := int64(Limit)
	Page := getPage(0, r)
	skip64 := int64(Page * Limit)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{
		"courtSession.communityID": communityID,
	}
	if status != "" {
		if strings.Contains(status, ",") {
			statuses := strings.Split(status, ",")
			filter["courtSession.status"] = bson.M{"$in": statuses}
		} else {
			filter["courtSession.status"] = status
		}
	}
	if departmentID != "" {
		filter["courtSession.departmentID"] = departmentID
	}

	type findResult struct {
		sessions []models.CourtSession
		err      error
	}
	type countResult struct {
		count int64
		err   error
	}

	findChan := make(chan findResult, 1)
	countChan := make(chan countResult, 1)

	go func() {
		sessions, err := cs.DB.Find(ctx, filter, &options.FindOptions{
			Limit: &limit64,
			Skip:  &skip64,
			Sort:  bson.M{"_id": -1},
		})
		findChan <- findResult{sessions: sessions, err: err}
	}()

	go func() {
		count, err := cs.DB.CountDocuments(ctx, filter)
		countChan <- countResult{count: count, err: err}
	}()

	findRes := <-findChan
	countRes := <-countChan

	if findRes.err != nil {
		config.ErrorStatus("failed to get court sessions", http.StatusNotFound, w, findRes.err)
		return
	}

	dbResp := findRes.sessions
	var totalCount int64
	if countRes.err != nil {
		totalCount = int64(len(dbResp))
	} else {
		totalCount = countRes.count
	}

	if len(dbResp) == 0 {
		dbResp = []models.CourtSession{}
	}

	totalPages := int(math.Ceil(float64(totalCount) / float64(Limit)))

	response := map[string]interface{}{
		"data":       dbResp,
		"page":       Page,
		"limit":      Limit,
		"totalCount": totalCount,
		"totalPages": totalPages,
	}

	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// StartCourtSessionHandler starts a court session
func (cs CourtSession) StartCourtSessionHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := mux.Vars(r)["session_id"]

	bID, err := primitive.ObjectIDFromHex(sessionID)
	if err != nil {
		config.ErrorStatus("invalid court session ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	existing, err := cs.DB.FindOne(ctx, bson.M{"_id": bID})
	if err != nil {
		config.ErrorStatus("failed to find court session", http.StatusNotFound, w, err)
		return
	}

	if existing.Details.Status != "scheduled" {
		config.ErrorStatus("session is not in scheduled status", http.StatusBadRequest, w, fmt.Errorf("session status is '%s', expected 'scheduled'", existing.Details.Status))
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	update := bson.M{
		"$set": bson.M{
			"courtSession.status":    "in_progress",
			"courtSession.startedAt": now,
			"courtSession.updatedAt": now,
		},
	}

	err = cs.DB.UpdateOne(ctx, bson.M{"_id": bID}, update)
	if err != nil {
		config.ErrorStatus("failed to start court session", http.StatusInternalServerError, w, err)
		return
	}

	// Update linked court cases to in_progress
	for _, entry := range existing.Details.Docket {
		caseID, err := primitive.ObjectIDFromHex(entry.CourtCaseID)
		if err != nil {
			continue
		}
		_ = cs.CCDB.UpdateOne(ctx,
			bson.M{"_id": caseID},
			bson.M{"$set": bson.M{
				"courtCase.status":    "in_progress",
				"courtCase.updatedAt": now,
			}},
		)
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Court session started",
	})
}

// EndCourtSessionHandler ends a court session
func (cs CourtSession) EndCourtSessionHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := mux.Vars(r)["session_id"]

	bID, err := primitive.ObjectIDFromHex(sessionID)
	if err != nil {
		config.ErrorStatus("invalid court session ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Fetch the session to check for unresolved docket entries
	existing, err := cs.DB.FindOne(ctx, bson.M{"_id": bID})
	if err != nil {
		config.ErrorStatus("failed to find court session", http.StatusNotFound, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	// Reset unresolved docket entries (pending/active) and their linked court cases
	unresolvedCount := 0
	updatedDocket := make([]models.DocketEntry, len(existing.Details.Docket))
	copy(updatedDocket, existing.Details.Docket)

	for i, entry := range updatedDocket {
		if entry.Status == "pending" || entry.Status == "active" {
			unresolvedCount++
			// Mark the docket entry as unresolved within the session
			updatedDocket[i].Status = "unresolved"

			caseID, cErr := primitive.ObjectIDFromHex(entry.CourtCaseID)
			if cErr != nil {
				continue
			}
			// Reset the court case back to scheduled and clear the session link
			_ = cs.CCDB.UpdateOne(ctx,
				bson.M{"_id": caseID},
				bson.M{"$set": bson.M{
					"courtCase.status":         "scheduled",
					"courtCase.courtSessionID": "",
					"courtCase.updatedAt":      now,
				}},
			)
		}
	}

	// Use "cancelled" if there were unresolved items, "completed" if all resolved
	finalStatus := "completed"
	if unresolvedCount > 0 {
		finalStatus = "cancelled"
	}

	update := bson.M{
		"$set": bson.M{
			"courtSession.status":    finalStatus,
			"courtSession.endedAt":   now,
			"courtSession.updatedAt": now,
			"courtSession.docket":    updatedDocket,
		},
	}

	err = cs.DB.UpdateOne(ctx, bson.M{"_id": bID}, update)
	if err != nil {
		config.ErrorStatus("failed to end court session", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":         "Court session ended",
		"unresolvedCount": unresolvedCount,
	})
}

// UpdateCourtSessionHandler updates a court session's details (title, times, docket)
func (cs CourtSession) UpdateCourtSessionHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := mux.Vars(r)["session_id"]

	bID, err := primitive.ObjectIDFromHex(sessionID)
	if err != nil {
		config.ErrorStatus("invalid court session ID", http.StatusBadRequest, w, err)
		return
	}

	// Decode into raw map to handle ISO date strings for scheduledStart/scheduledEnd
	var raw map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	rawStart, hasStart := raw["scheduledStart"]
	rawEnd, hasEnd := raw["scheduledEnd"]
	delete(raw, "scheduledStart")
	delete(raw, "scheduledEnd")

	b, _ := json.Marshal(raw)
	var details models.CourtSessionDetails
	if err := json.Unmarshal(b, &details); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if hasStart {
		if s, ok := rawStart.(string); ok && s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				details.ScheduledStart = primitive.NewDateTimeFromTime(t)
			}
		}
	}
	if hasEnd {
		if s, ok := rawEnd.(string); ok && s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				details.ScheduledEnd = primitive.NewDateTimeFromTime(t)
			}
		}
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	existing, err := cs.DB.FindOne(ctx, bson.M{"_id": bID})
	if err != nil {
		config.ErrorStatus("failed to find court session", http.StatusNotFound, w, err)
		return
	}

	// Only allow editing scheduled sessions
	if existing.Details.Status != "scheduled" {
		config.ErrorStatus("can only edit sessions in scheduled status", http.StatusBadRequest, w, fmt.Errorf("session status is '%s'", existing.Details.Status))
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	setFields := bson.M{
		"courtSession.updatedAt": now,
	}

	if details.Title != "" {
		setFields["courtSession.title"] = details.Title
	}
	if details.ScheduledStart != 0 {
		setFields["courtSession.scheduledStart"] = details.ScheduledStart
	}
	if details.ScheduledEnd != 0 {
		setFields["courtSession.scheduledEnd"] = details.ScheduledEnd
	}

	// If docket was provided, enrich and replace
	if details.Docket != nil {
		for i := range details.Docket {
			if details.Docket[i].Status == "" {
				details.Docket[i].Status = "pending"
			}
			caseID, err := primitive.ObjectIDFromHex(details.Docket[i].CourtCaseID)
			if err == nil {
				courtCase, err := cs.CCDB.FindOne(ctx, bson.M{"_id": caseID})
				if err == nil && courtCase != nil {
					details.Docket[i].CivilianName = courtCase.Details.CivilianName
					details.Docket[i].UserID = courtCase.Details.UserID
				}
			}
		}
		setFields["courtSession.docket"] = details.Docket

		// Unlink old cases that are no longer in the docket
		newCaseIDs := make(map[string]bool)
		for _, entry := range details.Docket {
			newCaseIDs[entry.CourtCaseID] = true
		}
		for _, entry := range existing.Details.Docket {
			if !newCaseIDs[entry.CourtCaseID] {
				caseID, err := primitive.ObjectIDFromHex(entry.CourtCaseID)
				if err == nil {
					_ = cs.CCDB.UpdateOne(ctx,
						bson.M{"_id": caseID},
						bson.M{"$unset": bson.M{"courtCase.courtSessionID": ""}, "$set": bson.M{"courtCase.updatedAt": now}},
					)
				}
			}
		}

		// Link new cases
		for _, entry := range details.Docket {
			caseID, err := primitive.ObjectIDFromHex(entry.CourtCaseID)
			if err == nil {
				_ = cs.CCDB.UpdateOne(ctx,
					bson.M{"_id": caseID},
					bson.M{"$set": bson.M{
						"courtCase.courtSessionID": sessionID,
						"courtCase.updatedAt":      now,
					}},
				)
			}
		}
	}

	err = cs.DB.UpdateOne(ctx, bson.M{"_id": bID}, bson.M{"$set": setFields})
	if err != nil {
		config.ErrorStatus("failed to update court session", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Court session updated",
	})
}

// DeleteCourtSessionHandler deletes a court session
func (cs CourtSession) DeleteCourtSessionHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := mux.Vars(r)["session_id"]

	bID, err := primitive.ObjectIDFromHex(sessionID)
	if err != nil {
		config.ErrorStatus("invalid court session ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	existing, err := cs.DB.FindOne(ctx, bson.M{"_id": bID})
	if err != nil {
		config.ErrorStatus("failed to find court session", http.StatusNotFound, w, err)
		return
	}

	// Only allow deleting scheduled sessions
	if existing.Details.Status != "scheduled" {
		config.ErrorStatus("can only delete sessions in scheduled status", http.StatusBadRequest, w, fmt.Errorf("session status is '%s'", existing.Details.Status))
		return
	}

	// Unlink court cases from this session
	now := primitive.NewDateTimeFromTime(time.Now())
	for _, entry := range existing.Details.Docket {
		caseID, err := primitive.ObjectIDFromHex(entry.CourtCaseID)
		if err == nil {
			_ = cs.CCDB.UpdateOne(ctx,
				bson.M{"_id": caseID},
				bson.M{"$unset": bson.M{"courtCase.courtSessionID": ""}, "$set": bson.M{"courtCase.updatedAt": now}},
			)
		}
	}

	err = cs.DB.DeleteOne(ctx, bson.M{"_id": bID})
	if err != nil {
		config.ErrorStatus("failed to delete court session", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Court session deleted",
	})
}

// ActivateDocketEntryHandler activates a specific case in the docket
func (cs CourtSession) ActivateDocketEntryHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := mux.Vars(r)["session_id"]
	caseID := mux.Vars(r)["case_id"]

	bID, err := primitive.ObjectIDFromHex(sessionID)
	if err != nil {
		config.ErrorStatus("invalid court session ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	existing, err := cs.DB.FindOne(ctx, bson.M{"_id": bID})
	if err != nil {
		config.ErrorStatus("failed to find court session", http.StatusNotFound, w, err)
		return
	}

	// Check if this is a skip (previous active case goes back to pending instead of completed)
	skipPrevious := r.URL.Query().Get("skip") == "true"

	// Update docket entries: set the target case to "active", handle previously active case
	updatedDocket := make([]models.DocketEntry, len(existing.Details.Docket))
	for i, entry := range existing.Details.Docket {
		updatedDocket[i] = entry
		if entry.CourtCaseID == caseID {
			updatedDocket[i].Status = "active"
		} else if entry.Status == "active" {
			if skipPrevious {
				updatedDocket[i].Status = "pending"
			} else {
				updatedDocket[i].Status = "completed"
			}
		}
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	update := bson.M{
		"$set": bson.M{
			"courtSession.docket":    updatedDocket,
			"courtSession.updatedAt": now,
		},
	}

	err = cs.DB.UpdateOne(ctx, bson.M{"_id": bID}, update)
	if err != nil {
		config.ErrorStatus("failed to activate docket entry", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Docket entry activated",
	})
}

// JoinCourtSessionHandler adds a participant to a court session
func (cs CourtSession) JoinCourtSessionHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := mux.Vars(r)["session_id"]

	bID, err := primitive.ObjectIDFromHex(sessionID)
	if err != nil {
		config.ErrorStatus("invalid court session ID", http.StatusBadRequest, w, err)
		return
	}

	var participant models.SessionParticipant
	if err := json.NewDecoder(r.Body).Decode(&participant); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	participant.JoinedAt = primitive.NewDateTimeFromTime(time.Now())

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Read-modify-write to handle legacy sessions where participants may be null
	existing, err := cs.DB.FindOne(ctx, bson.M{"_id": bID})
	if err != nil {
		config.ErrorStatus("failed to find court session", http.StatusNotFound, w, err)
		return
	}

	// Build new participants list: remove existing entry for this user, then append
	participants := existing.Details.Participants
	if participants == nil {
		participants = []models.SessionParticipant{}
	}
	filtered := make([]models.SessionParticipant, 0, len(participants))
	for _, p := range participants {
		if p.UserID != participant.UserID {
			filtered = append(filtered, p)
		}
	}
	filtered = append(filtered, participant)

	now := primitive.NewDateTimeFromTime(time.Now())
	err = cs.DB.UpdateOne(ctx, bson.M{"_id": bID}, bson.M{
		"$set": bson.M{
			"courtSession.participants": filtered,
			"courtSession.updatedAt":    now,
		},
	})
	if err != nil {
		config.ErrorStatus("failed to join court session", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Joined court session",
	})
}

// LeaveCourtSessionHandler removes a participant from a court session
func (cs CourtSession) LeaveCourtSessionHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := mux.Vars(r)["session_id"]
	userID := mux.Vars(r)["user_id"]

	bID, err := primitive.ObjectIDFromHex(sessionID)
	if err != nil {
		config.ErrorStatus("invalid court session ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Read-modify-write to handle legacy sessions where participants may be null
	existing, err := cs.DB.FindOne(ctx, bson.M{"_id": bID})
	if err != nil {
		config.ErrorStatus("failed to find court session", http.StatusNotFound, w, err)
		return
	}

	filtered := make([]models.SessionParticipant, 0)
	for _, p := range existing.Details.Participants {
		if p.UserID != userID {
			filtered = append(filtered, p)
		}
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	err = cs.DB.UpdateOne(ctx, bson.M{"_id": bID}, bson.M{
		"$set": bson.M{
			"courtSession.participants": filtered,
			"courtSession.updatedAt":    now,
		},
	})
	if err != nil {
		config.ErrorStatus("failed to leave court session", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Left court session",
	})
}

// GetCourtChatHandler returns paginated chat messages for a court session
func (cs CourtSession) GetCourtChatHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := mux.Vars(r)["session_id"]
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 50
	}
	limit64 := int64(Limit)
	Page := getPage(0, r)
	skip64 := int64(Page * Limit)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"sessionID": sessionID}

	type findResult struct {
		messages []models.CourtChatMessage
		err      error
	}
	type countResult struct {
		count int64
		err   error
	}

	findChan := make(chan findResult, 1)
	countChan := make(chan countResult, 1)

	go func() {
		messages, err := cs.ChDB.Find(ctx, filter, &options.FindOptions{
			Limit: &limit64,
			Skip:  &skip64,
			Sort:  bson.M{"createdAt": 1}, // oldest first for chat
		})
		findChan <- findResult{messages: messages, err: err}
	}()

	go func() {
		count, err := cs.ChDB.CountDocuments(ctx, filter)
		countChan <- countResult{count: count, err: err}
	}()

	findRes := <-findChan
	countRes := <-countChan

	if findRes.err != nil {
		config.ErrorStatus("failed to get chat messages", http.StatusNotFound, w, findRes.err)
		return
	}

	dbResp := findRes.messages
	var totalCount int64
	if countRes.err != nil {
		totalCount = int64(len(dbResp))
	} else {
		totalCount = countRes.count
	}

	if len(dbResp) == 0 {
		dbResp = []models.CourtChatMessage{}
	}

	totalPages := int(math.Ceil(float64(totalCount) / float64(Limit)))

	response := map[string]interface{}{
		"data":       dbResp,
		"page":       Page,
		"limit":      Limit,
		"totalCount": totalCount,
		"totalPages": totalPages,
	}

	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// PostCourtChatHandler posts a chat message to a court session
func (cs CourtSession) PostCourtChatHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := mux.Vars(r)["session_id"]

	var msg models.CourtChatMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	msg.ID = primitive.NewObjectID()
	msg.SessionID = sessionID
	msg.CreatedAt = primitive.NewDateTimeFromTime(time.Now())

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	_, err := cs.ChDB.InsertOne(ctx, msg)
	if err != nil {
		config.ErrorStatus("failed to post chat message", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":     "Chat message posted",
		"chatMessage": msg,
	})
}
