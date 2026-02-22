package handlers

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
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

// CourtCase exported for testing purposes
type CourtCase struct {
	DB  databases.CourtCaseDatabase
	CDB databases.CivilianDatabase
	ADB databases.ArrestReportDatabase
	SDB databases.CourtSessionDatabase // court session DB for updating docket entries on resolve
}

// CreateCourtCaseHandler creates a new court case when a civilian contests records
func (cc CourtCase) CreateCourtCaseHandler(w http.ResponseWriter, r *http.Request) {
	var courtCase models.CourtCase
	if err := json.NewDecoder(r.Body).Decode(&courtCase.Details); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	courtCase.ID = primitive.NewObjectID()
	now := primitive.NewDateTimeFromTime(time.Now())
	courtCase.Details.CreatedAt = now
	courtCase.Details.UpdatedAt = now
	courtCase.Details.Status = "submitted"

	// Initialize history with submission entry
	courtCase.Details.History = []models.CourtCaseHistoryEntry{
		{
			Action:    "submitted",
			UserID:    courtCase.Details.UserID,
			UserName:  courtCase.Details.CivilianName,
			Timestamp: now,
		},
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	_, err := cc.DB.InsertOne(ctx, courtCase)
	if err != nil {
		config.ErrorStatus("failed to create court case", http.StatusInternalServerError, w, err)
		return
	}

	// Mark contested items on the civilian's criminal history
	for _, item := range courtCase.Details.ContestedItems {
		if item.ItemType == "citation" || item.ItemType == "warning" {
			itemID, err := primitive.ObjectIDFromHex(item.ItemID)
			if err != nil {
				continue
			}
			civID, err := primitive.ObjectIDFromHex(courtCase.Details.CivilianID)
			if err != nil {
				continue
			}
			_ = cc.CDB.UpdateOne(ctx,
				bson.M{"_id": civID, "civilian.criminalHistory._id": itemID},
				bson.M{"$set": bson.M{
					"civilian.criminalHistory.$.status":      "contested",
					"civilian.criminalHistory.$.courtCaseID": courtCase.ID.Hex(),
					"civilian.criminalHistory.$.updatedAt":   now,
				}},
			)
		} else if item.ItemType == "arrest" {
			itemID, err := primitive.ObjectIDFromHex(item.ItemID)
			if err != nil {
				continue
			}
			_ = cc.ADB.UpdateOne(ctx,
				bson.M{"_id": itemID},
				bson.M{"$set": bson.M{
					"arrestReport.status":      "contested",
					"arrestReport.courtCaseID": courtCase.ID.Hex(),
					"arrestReport.updatedAt":   now,
				}},
			)
		}
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Court case created successfully",
		"id":      courtCase.ID.Hex(),
		"status":  courtCase.Details.Status,
	})
}

// GetCourtCaseByIDHandler returns a court case by ID
func (cc CourtCase) GetCourtCaseByIDHandler(w http.ResponseWriter, r *http.Request) {
	caseID := mux.Vars(r)["case_id"]

	bID, err := primitive.ObjectIDFromHex(caseID)
	if err != nil {
		config.ErrorStatus("invalid court case ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	dbResp, err := cc.DB.FindOne(ctx, bson.M{"_id": bID})
	if err != nil {
		config.ErrorStatus("failed to find court case", http.StatusNotFound, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(dbResp)
}

// GetCourtCasesByCommunityHandler returns paginated court cases for a community
func (cc CourtCase) GetCourtCasesByCommunityHandler(w http.ResponseWriter, r *http.Request) {
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
		"courtCase.communityID": communityID,
	}
	if status != "" {
		filter["courtCase.status"] = status
	}
	if departmentID != "" {
		filter["courtCase.departmentID"] = departmentID
	}
	if r.URL.Query().Get("unassigned") == "true" {
		filter["courtCase.courtSessionID"] = bson.M{
			"$in": []interface{}{nil, ""},
		}
	}

	type findResult struct {
		cases []models.CourtCase
		err   error
	}
	type countResult struct {
		count int64
		err   error
	}

	findChan := make(chan findResult, 1)
	countChan := make(chan countResult, 1)

	go func() {
		cases, err := cc.DB.Find(ctx, filter, &options.FindOptions{
			Limit: &limit64,
			Skip:  &skip64,
			Sort:  bson.M{"_id": -1},
		})
		findChan <- findResult{cases: cases, err: err}
	}()

	go func() {
		count, err := cc.DB.CountDocuments(ctx, filter)
		countChan <- countResult{count: count, err: err}
	}()

	findRes := <-findChan
	countRes := <-countChan

	if findRes.err != nil {
		config.ErrorStatus("failed to get court cases", http.StatusNotFound, w, findRes.err)
		return
	}

	dbResp := findRes.cases
	var totalCount int64
	if countRes.err != nil {
		totalCount = int64(len(dbResp))
	} else {
		totalCount = countRes.count
	}

	if len(dbResp) == 0 {
		dbResp = []models.CourtCase{}
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

// GetCourtCasesByCivilianHandler returns court cases for a specific civilian
func (cc CourtCase) GetCourtCasesByCivilianHandler(w http.ResponseWriter, r *http.Request) {
	civilianID := mux.Vars(r)["civilian_id"]
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
		"courtCase.civilianID": civilianID,
	}

	type findResult struct {
		cases []models.CourtCase
		err   error
	}
	type countResult struct {
		count int64
		err   error
	}

	findChan := make(chan findResult, 1)
	countChan := make(chan countResult, 1)

	go func() {
		cases, err := cc.DB.Find(ctx, filter, &options.FindOptions{
			Limit: &limit64,
			Skip:  &skip64,
			Sort:  bson.M{"_id": -1},
		})
		findChan <- findResult{cases: cases, err: err}
	}()

	go func() {
		count, err := cc.DB.CountDocuments(ctx, filter)
		countChan <- countResult{count: count, err: err}
	}()

	findRes := <-findChan
	countRes := <-countChan

	if findRes.err != nil {
		config.ErrorStatus("failed to get court cases", http.StatusNotFound, w, findRes.err)
		return
	}

	dbResp := findRes.cases
	var totalCount int64
	if countRes.err != nil {
		totalCount = int64(len(dbResp))
	} else {
		totalCount = countRes.count
	}

	if len(dbResp) == 0 {
		dbResp = []models.CourtCase{}
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

// AssignCourtCaseHandler allows a judge to assign themselves to a case
func (cc CourtCase) AssignCourtCaseHandler(w http.ResponseWriter, r *http.Request) {
	caseID := mux.Vars(r)["case_id"]

	bID, err := primitive.ObjectIDFromHex(caseID)
	if err != nil {
		config.ErrorStatus("invalid court case ID", http.StatusBadRequest, w, err)
		return
	}

	var assignData struct {
		JudgeID   string `json:"judgeID"`
		JudgeName string `json:"judgeName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&assignData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	existing, err := cc.DB.FindOne(ctx, bson.M{"_id": bID})
	if err != nil {
		config.ErrorStatus("failed to find court case", http.StatusNotFound, w, err)
		return
	}

	if existing.Details.Status != "submitted" {
		config.ErrorStatus("case is not in submitted status", http.StatusBadRequest, w, fmt.Errorf("case status is '%s', expected 'submitted'", existing.Details.Status))
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	update := bson.M{
		"$set": bson.M{
			"courtCase.judgeID":   assignData.JudgeID,
			"courtCase.judgeName": assignData.JudgeName,
			"courtCase.status":   "in_review",
			"courtCase.updatedAt": now,
		},
		"$push": bson.M{
			"courtCase.history": models.CourtCaseHistoryEntry{
				Action:    "assigned",
				UserID:    assignData.JudgeID,
				UserName:  assignData.JudgeName,
				Timestamp: now,
			},
		},
	}

	err = cc.DB.UpdateOne(ctx, bson.M{"_id": bID}, update)
	if err != nil {
		config.ErrorStatus("failed to assign court case", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Court case assigned successfully",
	})
}

// ScheduleCourtCaseHandler allows a judge to schedule a court date
func (cc CourtCase) ScheduleCourtCaseHandler(w http.ResponseWriter, r *http.Request) {
	caseID := mux.Vars(r)["case_id"]

	bID, err := primitive.ObjectIDFromHex(caseID)
	if err != nil {
		config.ErrorStatus("invalid court case ID", http.StatusBadRequest, w, err)
		return
	}

	var scheduleData struct {
		ScheduledDate primitive.DateTime `json:"scheduledDate"`
		JudgeID       string             `json:"judgeID"`
		JudgeName     string             `json:"judgeName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&scheduleData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	now := primitive.NewDateTimeFromTime(time.Now())
	update := bson.M{
		"$set": bson.M{
			"courtCase.scheduledDate": scheduleData.ScheduledDate,
			"courtCase.status":       "scheduled",
			"courtCase.updatedAt":    now,
		},
		"$push": bson.M{
			"courtCase.history": models.CourtCaseHistoryEntry{
				Action:    "scheduled",
				UserID:    scheduleData.JudgeID,
				UserName:  scheduleData.JudgeName,
				Timestamp: now,
			},
		},
	}

	err = cc.DB.UpdateOne(ctx, bson.M{"_id": bID}, update)
	if err != nil {
		config.ErrorStatus("failed to schedule court case", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Court case scheduled successfully",
	})
}

// ResolveCourtCaseHandler allows a judge to resolve individual items in a court case
func (cc CourtCase) ResolveCourtCaseHandler(w http.ResponseWriter, r *http.Request) {
	caseID := mux.Vars(r)["case_id"]

	bID, err := primitive.ObjectIDFromHex(caseID)
	if err != nil {
		config.ErrorStatus("invalid court case ID", http.StatusBadRequest, w, err)
		return
	}

	var resolveData struct {
		Resolutions []models.CaseResolution `json:"resolutions"`
		JudgeID     string                  `json:"judgeID"`
		JudgeName   string                  `json:"judgeName"`
		JudgeNotes  string                  `json:"judgeNotes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&resolveData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	now := primitive.NewDateTimeFromTime(time.Now())

	// Set resolvedAt on each resolution
	for i := range resolveData.Resolutions {
		resolveData.Resolutions[i].ResolvedAt = now
	}

	update := bson.M{
		"$set": bson.M{
			"courtCase.resolutions": resolveData.Resolutions,
			"courtCase.judgeNotes":  resolveData.JudgeNotes,
			"courtCase.status":      "completed",
			"courtCase.updatedAt":   now,
		},
		"$push": bson.M{
			"courtCase.history": models.CourtCaseHistoryEntry{
				Action:    "completed",
				UserID:    resolveData.JudgeID,
				UserName:  resolveData.JudgeName,
				Notes:     resolveData.JudgeNotes,
				Timestamp: now,
			},
		},
	}

	err = cc.DB.UpdateOne(ctx, bson.M{"_id": bID}, update)
	if err != nil {
		config.ErrorStatus("failed to resolve court case", http.StatusInternalServerError, w, err)
		return
	}

	// Mark the docket entry as "completed" in the court session (if linked)
	courtCase, _ := cc.DB.FindOne(ctx, bson.M{"_id": bID})
	if courtCase != nil && courtCase.Details.CourtSessionID != "" {
		sessionOID, sErr := primitive.ObjectIDFromHex(courtCase.Details.CourtSessionID)
		if sErr == nil {
			session, sErr := cc.SDB.FindOne(ctx, bson.M{"_id": sessionOID})
			if sErr == nil && session != nil {
				updatedDocket := make([]models.DocketEntry, len(session.Details.Docket))
				for i, entry := range session.Details.Docket {
					updatedDocket[i] = entry
					if entry.CourtCaseID == caseID {
						updatedDocket[i].Status = "completed"
					}
				}
				_ = cc.SDB.UpdateOne(ctx, bson.M{"_id": sessionOID}, bson.M{
					"$set": bson.M{
						"courtSession.docket":    updatedDocket,
						"courtSession.updatedAt": now,
					},
				})
			}
		}
	}

	// Apply resolutions to the original records
	for _, resolution := range resolveData.Resolutions {
		if resolution.Verdict == "dismissed" || resolution.Verdict == "upheld" {
			if resolution.ItemType == "citation" || resolution.ItemType == "warning" {
				itemID, err := primitive.ObjectIDFromHex(resolution.ItemID)
				if err != nil {
					continue
				}
				// Find the court case to get the civilian ID
				courtCase, err := cc.DB.FindOne(ctx, bson.M{"_id": bID})
				if err != nil {
					continue
				}
				civID, err := primitive.ObjectIDFromHex(courtCase.Details.CivilianID)
				if err != nil {
					continue
				}
				_ = cc.CDB.UpdateOne(ctx,
					bson.M{"_id": civID, "civilian.criminalHistory._id": itemID},
					bson.M{"$set": bson.M{
						"civilian.criminalHistory.$.status":      resolution.Verdict,
						"civilian.criminalHistory.$.dismissedBy": resolveData.JudgeName,
						"civilian.criminalHistory.$.updatedAt":   now,
					}},
				)
			} else if resolution.ItemType == "arrest" {
				itemID, err := primitive.ObjectIDFromHex(resolution.ItemID)
				if err != nil {
					continue
				}
				_ = cc.ADB.UpdateOne(ctx,
					bson.M{"_id": itemID},
					bson.M{"$set": bson.M{
						"arrestReport.status":      resolution.Verdict,
						"arrestReport.dismissedBy": resolveData.JudgeName,
						"arrestReport.updatedAt":   now,
					}},
				)
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Court case resolved successfully",
	})
}

// UpdateCourtCaseStatusHandler updates the status of a court case
func (cc CourtCase) UpdateCourtCaseStatusHandler(w http.ResponseWriter, r *http.Request) {
	caseID := mux.Vars(r)["case_id"]

	bID, err := primitive.ObjectIDFromHex(caseID)
	if err != nil {
		config.ErrorStatus("invalid court case ID", http.StatusBadRequest, w, err)
		return
	}

	var statusData struct {
		Status   string `json:"status"`
		UserID   string `json:"userID"`
		UserName string `json:"userName"`
		Notes    string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&statusData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Validate status
	validStatuses := map[string]bool{
		"submitted": true, "in_review": true, "scheduled": true, "in_progress": true, "completed": true,
	}
	if !validStatuses[statusData.Status] {
		config.ErrorStatus("invalid status", http.StatusBadRequest, w, fmt.Errorf("status '%s' is not valid", statusData.Status))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	now := primitive.NewDateTimeFromTime(time.Now())
	setFields := bson.M{
		"courtCase.status":    statusData.Status,
		"courtCase.updatedAt": now,
	}

	// Clear scheduled date when returning to queue
	if statusData.Status == "in_review" || statusData.Status == "submitted" {
		setFields["courtCase.scheduledDate"] = nil
	}

	update := bson.M{
		"$set": setFields,
		"$push": bson.M{
			"courtCase.history": models.CourtCaseHistoryEntry{
				Action:    statusData.Status,
				UserID:    statusData.UserID,
				UserName:  statusData.UserName,
				Notes:     statusData.Notes,
				Timestamp: now,
			},
		},
	}

	err = cc.DB.UpdateOne(ctx, bson.M{"_id": bID}, update)
	if err != nil {
		config.ErrorStatus("failed to update court case status", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Court case status updated successfully",
	})
}
