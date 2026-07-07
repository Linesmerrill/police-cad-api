package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
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

// CourtCase exported for testing purposes
type CourtCase struct {
	DB     databases.CourtCaseDatabase
	CDB    databases.CivilianDatabase
	ADB    databases.ArrestReportDatabase
	SDB    databases.CourtSessionDatabase     // court session DB for updating docket entries on resolve
	UDB    databases.UserDatabase             // for community-membership checks on search
	CommDB databases.CommunityDatabase        // for judicial-role checks on delete
	IDB    databases.InboxItemDatabase        // economy inbox; nil-safe (hook no-ops when nil)
}

// CreateCourtCaseHandler creates a new court case when a civilian contests records
func (cc CourtCase) CreateCourtCaseHandler(w http.ResponseWriter, r *http.Request) {
	var courtCase models.CourtCase
	if err := json.NewDecoder(r.Body).Decode(&courtCase); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	courtCase.ID = primitive.NewObjectID()
	now := primitive.NewDateTimeFromTime(time.Now())
	courtCase.Details.CreatedAt = now
	courtCase.Details.UpdatedAt = now
	courtCase.Details.Status = "submitted"

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Generate human-readable case number (CC-YYYY-NNNNNN), unique per community.
	caseNumber, err := cc.DB.NextCaseNumber(ctx, courtCase.Details.CommunityID)
	if err != nil {
		config.ErrorStatus("failed to generate case number", http.StatusInternalServerError, w, err)
		return
	}
	courtCase.Details.CaseNumber = caseNumber

	// Initialize history with submission entry
	courtCase.Details.History = []models.CourtCaseHistoryEntry{
		{
			Action:    "submitted",
			UserID:    courtCase.Details.UserID,
			UserName:  courtCase.Details.CivilianName,
			Timestamp: now,
		},
	}

	_, err = cc.DB.InsertOne(ctx, courtCase)
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
		"message":    "Court case created successfully",
		"id":         courtCase.ID.Hex(),
		"caseNumber": courtCase.Details.CaseNumber,
		"status":     courtCase.Details.Status,
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
		Resolutions  []models.CaseResolution `json:"resolutions"`
		JudgeID      string                  `json:"judgeID"`
		JudgeName    string                  `json:"judgeName"`
		JudgeNotes   string                  `json:"judgeNotes"`
		SentenceMode string                  `json:"sentenceMode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&resolveData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	now := primitive.NewDateTimeFromTime(time.Now())
	mode := models.NormalizeSentenceMode(resolveData.SentenceMode)

	for i := range resolveData.Resolutions {
		res := &resolveData.Resolutions[i]
		res.ResolvedAt = now

		// Derive each charge's back-compat Verdict from its Disposition so the
		// source-record write-back further down still sets the right fine status
		// (reduced/amended still count as upheld charges).
		for j := range res.ChargeResolutions {
			switch strings.ToLower(strings.TrimSpace(res.ChargeResolutions[j].Disposition)) {
			case models.DispositionDismissed:
				res.ChargeResolutions[j].Verdict = models.DispositionDismissed
			case models.DispositionUpheld, models.DispositionReduced, models.DispositionAmended:
				res.ChargeResolutions[j].Verdict = models.DispositionUpheld
			}
		}

		// Server-authoritative per-item sentencing totals from the dispositions.
		res.TotalFine, res.TotalJailTimeSeconds, res.TotalJailTimeLabel =
			models.ComputeResolutionTotals(res.ChargeResolutions, mode)
	}

	// Case-wide final-judgment totals across every charge.
	caseFine, caseSeconds, caseLabel := models.ComputeCourtCaseTotals(resolveData.Resolutions, mode)

	update := bson.M{
		"$set": bson.M{
			"courtCase.resolutions":          resolveData.Resolutions,
			"courtCase.judgeNotes":           resolveData.JudgeNotes,
			"courtCase.status":               "completed",
			"courtCase.sentenceMode":         mode,
			"courtCase.totalFine":            caseFine,
			"courtCase.totalJailTimeSeconds": caseSeconds,
			"courtCase.totalJailTimeLabel":   caseLabel,
			"courtCase.updatedAt":            now,
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

	// Economy: drop verdict-driven inbox items for upheld resolutions.
	// Fire-and-forget; never blocks the response.
	if courtCaseForHook, _ := cc.DB.FindOne(ctx, bson.M{"_id": bID}); courtCaseForHook != nil {
		hookRes := make([]judicialResolution, 0, len(resolveData.Resolutions))
		for _, r := range resolveData.Resolutions {
			hookRes = append(hookRes, judicialResolution{
				ItemID: r.ItemID, ItemType: r.ItemType, Verdict: r.Verdict, JudgeNotes: resolveData.JudgeNotes,
			})
		}
		dropJudicialInboxItem(
			inboxHookDeps{IDB: cc.IDB, CivDB: cc.CDB, CommDB: cc.CommDB},
			caseID,
			courtCaseForHook.Details.CommunityID,
			courtCaseForHook.Details.CivilianID,
			courtCaseForHook.Details.UserID,
			courtCaseForHook.Details.CaseNumber,
			hookRes,
		)

		// Settle the ORIGINAL fine inbox item that was contested (if any).
		// Without this the civilian's row stays "Contested — awaiting judge
		// review" forever even after the judge resolves the case. We resolve
		// items in contested|pending|delinquent state — paid items are left
		// alone since a refund flow isn't built yet.
		settleLinkedInboxItems(cc.IDB, cc.CDB, ctx, resolveData.Resolutions, resolveData.JudgeID, now)
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

	// Derive top-level verdict on resolutions that carry per-charge rulings so
	// downstream callers (settle hook, drop hook, civilian record update) all
	// see a coherent "partial" / "upheld" / "dismissed" outcome.
	for i := range resolveData.Resolutions {
		if len(resolveData.Resolutions[i].ChargeResolutions) > 0 {
			resolveData.Resolutions[i].Verdict = deriveTopVerdict(resolveData.Resolutions[i].ChargeResolutions)
		}
	}

	// Apply resolutions to the original records
	for _, resolution := range resolveData.Resolutions {
		if resolution.Verdict == "dismissed" || resolution.Verdict == "upheld" || resolution.Verdict == "partial" {
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
				// If the judge ruled per-charge, write the per-fine statuses
				// back to the criminalHistory entry. We do this by reading the
				// civilian, mutating the matching entry's Fines slice, and
				// writing the whole entry back — fines have no IDs so this is
				// the cleanest path.
				if len(resolution.ChargeResolutions) > 0 {
					civ, err := cc.CDB.FindOne(ctx, bson.M{"_id": civID})
					if err == nil && civ != nil {
						for chIdx, entry := range civ.Details.CriminalHistory {
							if entry.ID != itemID {
								continue
							}
							for _, cr := range resolution.ChargeResolutions {
								if cr.FineIndex < 0 || cr.FineIndex >= len(civ.Details.CriminalHistory[chIdx].Fines) {
									continue
								}
								if cr.Verdict == "upheld" || cr.Verdict == "dismissed" {
									civ.Details.CriminalHistory[chIdx].Fines[cr.FineIndex].Status = cr.Verdict
								}
							}
							civ.Details.CriminalHistory[chIdx].Status = resolution.Verdict
							civ.Details.CriminalHistory[chIdx].DismissedBy = resolveData.JudgeName
							civ.Details.CriminalHistory[chIdx].UpdatedAt = now
							path := fmt.Sprintf("civilian.criminalHistory.%d", chIdx)
							_ = cc.CDB.UpdateOne(ctx, bson.M{"_id": civID}, bson.M{"$set": bson.M{path: civ.Details.CriminalHistory[chIdx]}})
							break
						}
					}
				} else {
					_ = cc.CDB.UpdateOne(ctx,
						bson.M{"_id": civID, "civilian.criminalHistory._id": itemID},
						bson.M{"$set": bson.M{
							"civilian.criminalHistory.$.status":      resolution.Verdict,
							"civilian.criminalHistory.$.dismissedBy": resolveData.JudgeName,
							"civilian.criminalHistory.$.updatedAt":   now,
						}},
					)
				}
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

// DeleteCourtCaseHandler deletes a court case by ID.
// If the case is not yet completed, it reverts any contested items (citations, warnings, arrests)
// back to their original state (status: "", courtCaseID: "").
// If the case is linked to a court session, the docket entry is removed.
func (cc CourtCase) DeleteCourtCaseHandler(w http.ResponseWriter, r *http.Request) {
	caseID := mux.Vars(r)["case_id"]

	bID, err := primitive.ObjectIDFromHex(caseID)
	if err != nil {
		config.ErrorStatus("invalid court case ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	courtCase, err := cc.DB.FindOne(ctx, bson.M{"_id": bID})
	if err != nil {
		config.ErrorStatus("failed to find court case", http.StatusNotFound, w, err)
		return
	}

	// Authorization: only the case owner OR a judicial member of the case's community may delete.
	// Requires `userId` query param to identify the requester.
	requesterID := strings.TrimSpace(r.URL.Query().Get("userId"))
	if requesterID == "" {
		config.ErrorStatus("userId required", http.StatusBadRequest, w, fmt.Errorf("userId query param is empty"))
		return
	}
	if !cc.canDeleteCase(ctx, requesterID, courtCase) {
		config.ErrorStatus("forbidden", http.StatusForbidden, w, fmt.Errorf("user %s cannot delete this case", requesterID))
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	// Revert contested items only if the case was not yet completed
	if courtCase.Details.Status != "completed" {
		civID, civErr := primitive.ObjectIDFromHex(courtCase.Details.CivilianID)
		for _, item := range courtCase.Details.ContestedItems {
			if item.ItemType == "citation" || item.ItemType == "warning" {
				if civErr != nil {
					continue
				}
				itemID, err := primitive.ObjectIDFromHex(item.ItemID)
				if err != nil {
					continue
				}
				_ = cc.CDB.UpdateOne(ctx,
					bson.M{"_id": civID, "civilian.criminalHistory._id": itemID},
					bson.M{"$set": bson.M{
						"civilian.criminalHistory.$.status":      "",
						"civilian.criminalHistory.$.courtCaseID": "",
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
						"arrestReport.status":      "",
						"arrestReport.courtCaseID": "",
						"arrestReport.updatedAt":   now,
					}},
				)
			}
		}
	}

	// Remove from court session docket if linked
	if courtCase.Details.CourtSessionID != "" {
		sessionOID, sErr := primitive.ObjectIDFromHex(courtCase.Details.CourtSessionID)
		if sErr == nil {
			session, sErr := cc.SDB.FindOne(ctx, bson.M{"_id": sessionOID})
			if sErr == nil && session != nil {
				filteredDocket := make([]models.DocketEntry, 0, len(session.Details.Docket))
				for _, entry := range session.Details.Docket {
					if entry.CourtCaseID != caseID {
						filteredDocket = append(filteredDocket, entry)
					}
				}
				_ = cc.SDB.UpdateOne(ctx, bson.M{"_id": sessionOID}, bson.M{
					"$set": bson.M{
						"courtSession.docket":    filteredDocket,
						"courtSession.updatedAt": now,
					},
				})
			}
		}
	}

	err = cc.DB.DeleteOne(ctx, bson.M{"_id": bID})
	if err != nil {
		config.ErrorStatus("failed to delete court case", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Court case deleted successfully",
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

	// Prevent unscheduling a case that is assigned to a court session
	if statusData.Status == "in_review" || statusData.Status == "submitted" {
		existing, err := cc.DB.FindOne(ctx, bson.M{"_id": bID})
		if err == nil && existing != nil && existing.Details.CourtSessionID != "" {
			config.ErrorStatus("cannot unschedule a case that is assigned to a court session", http.StatusConflict, w,
				fmt.Errorf("case is assigned to session '%s'", existing.Details.CourtSessionID))
			return
		}
	}

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

// CourtCaseSearchRequest is the body for POST /api/v2/court-cases/search
type CourtCaseSearchRequest struct {
	Query        string `json:"query"`
	CommunityID  string `json:"communityId"`
	UserID       string `json:"userId"`       // requesting user; server verifies membership
	DepartmentID string `json:"departmentId"` // optional: scope to a specific department
	Page         int    `json:"page"`
	Limit        int    `json:"limit"`
}

// SearchCourtCasesHandler searches court cases by case number (exact, case-insensitive)
// or civilian name (partial, case-insensitive) within a community. Optionally scopes
// to a single department. Verifies the requesting user belongs to the community
// (defense in depth — the existing community-scoped court-case endpoints currently
// don't enforce this; tracked as a follow-up).
func (cc CourtCase) SearchCourtCasesHandler(w http.ResponseWriter, r *http.Request) {
	var req CourtCaseSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		config.ErrorStatus("query required", http.StatusBadRequest, w, fmt.Errorf("query is empty"))
		return
	}
	if req.CommunityID == "" {
		config.ErrorStatus("communityId required", http.StatusBadRequest, w, fmt.Errorf("communityId is empty"))
		return
	}
	if req.UserID == "" {
		config.ErrorStatus("userId required", http.StatusBadRequest, w, fmt.Errorf("userId is empty"))
		return
	}

	page := req.Page
	if page < 0 {
		page = 0
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	skip64 := int64(page * limit)
	limit64 := int64(limit)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Verify the requesting user belongs to the community.
	// Some users have a string _id, others have an ObjectID _id — try string first, then ObjectID.
	user := models.User{}
	if err := cc.UDB.FindOne(ctx, bson.M{"_id": req.UserID}).Decode(&user); err != nil {
		if oid, oidErr := primitive.ObjectIDFromHex(req.UserID); oidErr == nil {
			var userObj struct {
				ID      primitive.ObjectID `bson:"_id"`
				Details models.UserDetails `bson:"user"`
			}
			if err2 := cc.UDB.FindOne(ctx, bson.M{"_id": oid}).Decode(&userObj); err2 != nil {
				config.ErrorStatus("failed to verify user", http.StatusForbidden, w, err2)
				return
			}
			user = models.User{ID: userObj.ID.Hex(), Details: userObj.Details}
		} else {
			config.ErrorStatus("failed to verify user", http.StatusForbidden, w, err)
			return
		}
	}
	memberOf := user.Details.ActiveCommunity == req.CommunityID
	if !memberOf {
		for _, c := range user.Details.Communities {
			if c.CommunityID == req.CommunityID {
				memberOf = true
				break
			}
		}
	}
	if !memberOf {
		config.ErrorStatus("forbidden", http.StatusForbidden, w, fmt.Errorf("user not a member of community"))
		return
	}

	// Build filter: communityID required + (caseNumber exact OR civilianName partial).
	escaped := regexp.QuoteMeta(query)
	filter := bson.M{
		"$and": []bson.M{
			{"courtCase.communityID": req.CommunityID},
			{"$or": []bson.M{
				{"courtCase.caseNumber": bson.M{"$regex": "^" + escaped + "$", "$options": "i"}},
				{"courtCase.civilianName": bson.M{"$regex": escaped, "$options": "i"}},
			}},
		},
	}
	if req.DepartmentID != "" {
		filter["$and"] = append(filter["$and"].([]bson.M), bson.M{"courtCase.departmentID": req.DepartmentID})
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
		config.ErrorStatus("failed to search court cases", http.StatusInternalServerError, w, findRes.err)
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
	totalPages := int(math.Ceil(float64(totalCount) / float64(limit)))

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       dbResp,
		"page":       page,
		"limit":      limit,
		"totalCount": totalCount,
		"totalPages": totalPages,
	})
}

// canDeleteCase returns true if the requester owns the case or is a judicial-department
// member of the case's community. Used to authorize DELETE /court-cases/{id}.
func (cc CourtCase) canDeleteCase(ctx context.Context, requesterID string, courtCase *models.CourtCase) bool {
	// Owner check first — cheapest and the common civilian path.
	if courtCase.Details.UserID != "" && requesterID == courtCase.Details.UserID {
		return true
	}

	// Otherwise, look up the case's community and check whether the requester is a
	// member of any department whose template name is "judicial".
	if courtCase.Details.CommunityID == "" {
		return false
	}
	commOID, err := primitive.ObjectIDFromHex(courtCase.Details.CommunityID)
	if err != nil {
		return false
	}
	community, err := cc.CommDB.FindOne(ctx, bson.M{"_id": commOID})
	if err != nil || community == nil {
		return false
	}
	for _, dept := range community.Details.Departments {
		if !strings.EqualFold(dept.Template.Name, "judicial") {
			continue
		}
		for _, m := range dept.Members {
			if m.UserID == requesterID {
				return true
			}
		}
	}
	return false
}

// ResettleInboxHandler re-runs settleLinkedInboxItems for a previously
// completed court case. Useful for one-off reconciliation when settle was
// broken at the time the case was resolved (e.g. the pre-fix ordering bug
// where partial amounts were never reduced).
//
//   POST /api/v2/court-cases/{case_id}/resettle-inbox
//
// Idempotent — running it more than once on an already-correct item just
// overwrites the same fields. Only touches items still in
// {pending, delinquent, contested} state, so paid items are never disturbed.
func (cc CourtCase) ResettleInboxHandler(w http.ResponseWriter, r *http.Request) {
	caseID := mux.Vars(r)["case_id"]
	bID, err := primitive.ObjectIDFromHex(caseID)
	if err != nil {
		config.ErrorStatus("invalid court case ID", http.StatusBadRequest, w, err)
		return
	}
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	courtCase, err := cc.DB.FindOne(ctx, bson.M{"_id": bID})
	if err != nil || courtCase == nil {
		config.ErrorStatus("court case not found", http.StatusNotFound, w, err)
		return
	}
	if len(courtCase.Details.Resolutions) == 0 {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"message": "no resolutions to settle", "settled": 0})
		return
	}
	judgeID := courtCase.Details.JudgeID
	now := primitive.NewDateTimeFromTime(time.Now())
	settleLinkedInboxItems(cc.IDB, cc.CDB, ctx, courtCase.Details.Resolutions, judgeID, now)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Re-settle invoked",
		"caseID":  caseID,
	})
}

// ResettleInboxByItemHandler resettles a stuck inbox item by looking up the
// court case that ruled on its underlying criminal-history entry, then re-
// running settleLinkedInboxItems against the case's stored resolutions.
//
//   POST /api/v2/inbox/{id}/resettle
//
// Convenience wrapper around ResettleInboxHandler for callers that only have
// the inbox item ID at hand (e.g. fixing a row directly from /inbox).
func (cc CourtCase) ResettleInboxByItemHandler(w http.ResponseWriter, r *http.Request) {
	itemID := mux.Vars(r)["id"]
	bID, err := primitive.ObjectIDFromHex(itemID)
	if err != nil {
		config.ErrorStatus("invalid inbox item ID", http.StatusBadRequest, w, err)
		return
	}
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	if cc.IDB == nil {
		config.ErrorStatus("inbox database not configured", http.StatusInternalServerError, w, nil)
		return
	}
	item, err := cc.IDB.FindOne(ctx, bson.M{"_id": bID})
	if err != nil || item == nil {
		config.ErrorStatus("inbox item not found", http.StatusNotFound, w, err)
		return
	}
	if item.RefType != "criminalHistoryId" || item.RefID == "" {
		config.ErrorStatus("inbox item has no linked criminal history", http.StatusBadRequest, w, nil)
		return
	}
	cases, err := cc.DB.Find(ctx, bson.M{"courtCase.resolutions.itemID": item.RefID})
	if err != nil || len(cases) == 0 {
		config.ErrorStatus("no resolved court case found for this inbox item", http.StatusNotFound, w, err)
		return
	}
	// Pick the most-recently updated case in case there are multiples.
	target := cases[0]
	for _, c := range cases[1:] {
		if c.Details.UpdatedAt > target.Details.UpdatedAt {
			target = c
		}
	}
	judgeID := target.Details.JudgeID
	now := primitive.NewDateTimeFromTime(time.Now())
	settleLinkedInboxItems(cc.IDB, cc.CDB, ctx, target.Details.Resolutions, judgeID, now)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Re-settle invoked",
		"itemID":  itemID,
		"caseID":  target.ID.Hex(),
	})
}

// settleLinkedInboxItems updates any pending/contested fine inbox items that
// were linked to the criminal-history entries a judge just ruled on. Without
// this the civilian's inbox + wallet would keep showing the fine as
// "contested" indefinitely even after the case is resolved.
//
//   - verdict="dismissed" → mark the inbox item dismissed (with resolution flag)
//   - verdict="upheld"    → flip the item back to pending with resolution=upheld
//
// Items already paid are left alone — a refund flow isn't built yet, and
// silently changing the status of a paid item would obscure history. Items in
// any other status (dismissed already, paid, etc.) are also skipped.
//
// Each successful update broadcasts inbox.updated so subscribed clients
// (inbox page, civ-card badge) refresh in place.
func settleLinkedInboxItems(idb databases.InboxItemDatabase, cdb databases.CivilianDatabase, ctx context.Context, resolutions []models.CaseResolution, judgeID string, now primitive.DateTime) {
	for _, res := range resolutions {
		if res.ItemID == "" {
			continue
		}
		filter := bson.M{
			"refType": "criminalHistoryId",
			"refId":   res.ItemID,
			"status":  bson.M{"$in": []string{"contested", "pending", "delinquent"}},
		}
		items, err := idb.Find(ctx, filter)
		if err != nil || len(items) == 0 {
			continue
		}

		// Per-charge resolution → compute the reduced amount and updated body
		// from the resolution's ChargeResolutions list (the source of truth
		// the judge just submitted), paired with the original Fines slice on
		// the civilian's criminalHistory entry (for amounts + labels).
		//
		// Reading verdicts from res.ChargeResolutions instead of from
		// fine.Status decouples this from the order in which the resolve
		// handler writes statuses back to the civilian — so settle no
		// longer needs to run after that loop.
		var partial *struct {
			amountCents     int64
			upheldLabels    []string
			dismissedLabels []string
		}
		// Look up the civilian's criminal-history entry once so we can also
		// rebuild the per-charge `charges` slice for every verdict type, not
		// just partial. Cheap because we already need it for partial anyway.
		var historyFines []models.Fine
		if len(items) > 0 {
			it := items[0]
			if it.CivilianID != "" {
				if cID, perr := primitive.ObjectIDFromHex(it.CivilianID); perr == nil {
					if civ, ferr := cdb.FindOne(ctx, bson.M{"_id": cID}); ferr == nil && civ != nil {
						for _, entry := range civ.Details.CriminalHistory {
							if entry.ID.Hex() == res.ItemID {
								historyFines = entry.Fines
								break
							}
						}
					}
				}
			}
		}
		if res.Verdict == "partial" && len(historyFines) > 0 && len(res.ChargeResolutions) > 0 {
			verdictByIdx := make(map[int]string, len(res.ChargeResolutions))
			for _, cr := range res.ChargeResolutions {
				verdictByIdx[cr.FineIndex] = cr.Verdict
			}
			p := &struct {
				amountCents     int64
				upheldLabels    []string
				dismissedLabels []string
			}{}
			for i, f := range historyFines {
				v := verdictByIdx[i]
				if v == "dismissed" {
					if f.FineType != "" {
						p.dismissedLabels = append(p.dismissedLabels, f.FineType)
					}
					continue
				}
				// upheld or unset (defensive — treat as still owed)
				p.amountCents += int64(f.FineAmount) * 100
				if f.FineType != "" {
					p.upheldLabels = append(p.upheldLabels, f.FineType)
				}
			}
			partial = p
		}

		// Build the per-charge slice (also useful for the inbox UI). Maps the
		// original fines to {label, amount, status} based on the verdict.
		var chargesAfter []models.InboxCharge
		if len(historyFines) > 0 {
			verdictByIdx := make(map[int]string, len(res.ChargeResolutions))
			for _, cr := range res.ChargeResolutions {
				verdictByIdx[cr.FineIndex] = cr.Verdict
			}
			chargesAfter = make([]models.InboxCharge, 0, len(historyFines))
			for i, f := range historyFines {
				st := ""
				switch res.Verdict {
				case "dismissed":
					st = "dismissed"
				case "upheld":
					st = "upheld"
				case "partial":
					if v, ok := verdictByIdx[i]; ok && v == "dismissed" {
						st = "dismissed"
					} else {
						st = "upheld"
					}
				}
				chargesAfter = append(chargesAfter, models.InboxCharge{
					Label:  f.FineType,
					Amount: int64(f.FineAmount) * 100,
					Status: st,
				})
			}
		}

		var set bson.M
		switch res.Verdict {
		case "dismissed":
			set = bson.M{
				"status":      "dismissed",
				"dismissedAt": now,
				"dismissedBy": judgeID,
				"resolvedAt":  now,
				"resolvedBy":  judgeID,
				"resolution":  "dismissed",
				"updatedAt":   now,
			}
		case "upheld":
			set = bson.M{
				"status":     "pending",
				"resolvedAt": now,
				"resolvedBy": judgeID,
				"resolution": "upheld",
				"updatedAt":  now,
			}
		case "partial":
			set = bson.M{
				"status":     "pending",
				"resolvedAt": now,
				"resolvedBy": judgeID,
				"resolution": "partial",
				"updatedAt":  now,
			}
			if partial != nil {
				if partial.amountCents == 0 {
					// Every charge ended up dismissed — same as a full dismiss.
					set["status"] = "dismissed"
					set["dismissedAt"] = now
					set["dismissedBy"] = judgeID
					set["resolution"] = "dismissed"
				} else {
					set["amount"] = partial.amountCents
					body := ""
					if len(partial.upheldLabels) > 0 {
						body = strings.Join(partial.upheldLabels, ", ")
					}
					if len(partial.dismissedLabels) > 0 {
						if body != "" {
							body += " — "
						}
						body += "dismissed: " + strings.Join(partial.dismissedLabels, ", ")
					}
					if body != "" {
						set["body"] = body
					}
				}
			}
		default:
			continue
		}
		if len(chargesAfter) > 0 {
			set["charges"] = chargesAfter
		}
		for _, it := range items {
			if err := idb.UpdateOne(ctx, bson.M{"_id": it.ID}, bson.M{"$set": set}); err != nil {
				continue
			}
			if updated, _ := idb.FindOne(ctx, bson.M{"_id": it.ID}); updated != nil {
				go BroadcastInboxEvent("inbox.updated", updated.CommunityID, updated)
			}
		}
	}
}

// deriveTopVerdict returns the top-level outcome of a per-charge resolution:
// "upheld" if every charge was upheld, "dismissed" if every charge was
// dismissed, "partial" when the rulings are mixed. Returns "" when the
// resolution carries no per-charge rulings.
func deriveTopVerdict(charges []models.ChargeResolution) string {
	if len(charges) == 0 {
		return ""
	}
	upheld, dismissed := 0, 0
	for _, c := range charges {
		switch c.Verdict {
		case "upheld":
			upheld++
		case "dismissed":
			dismissed++
		}
	}
	if upheld > 0 && dismissed == 0 {
		return "upheld"
	}
	if dismissed > 0 && upheld == 0 {
		return "dismissed"
	}
	if upheld > 0 && dismissed > 0 {
		return "partial"
	}
	return ""
}
