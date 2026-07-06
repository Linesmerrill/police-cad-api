package handlers

import (
	"encoding/json"
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

// ArrestReport exported for testing purposes
type ArrestReport struct {
	DB     databases.ArrestReportDatabase
	CDB    databases.CivilianDatabase
	CommDB databases.CommunityDatabase
}

// PaginatedDataResponse holds the structure for paginated responses
type PaginatedDataResponse struct {
	Page       int         `json:"page"`
	TotalCount int64       `json:"totalCount"`
	Data       interface{} `json:"data"`
}

// GetArrestReportByIDHandler retrieves a Arrest report by its ID
func (a ArrestReport) GetArrestReportByIDHandler(w http.ResponseWriter, r *http.Request) {
	arrestReportID := mux.Vars(r)["arrest_report_id"]

	bID, err := primitive.ObjectIDFromHex(arrestReportID)
	if err != nil {
		config.ErrorStatus("invalid Arrest report ID", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": bID}
	
	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	
	arrestReport, err := a.DB.FindOne(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to find Arrest report", http.StatusNotFound, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(arrestReport)
}

// CreateArrestReportHandler creates a new ArrestReport
func (a ArrestReport) CreateArrestReportHandler(w http.ResponseWriter, r *http.Request) {
	var newArrestReport models.ArrestReport
	if err := json.NewDecoder(r.Body).Decode(&newArrestReport); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	newArrestReport.ID = primitive.NewObjectID()
	newArrestReport.Details.CreatedAt = primitive.NewDateTimeFromTime(time.Now())

	// Recompute the canonical fine + jail-time totals from the structured charge
	// list so stored values never depend on the submitting client's own math.
	newArrestReport.Details.SentenceMode = models.NormalizeSentenceMode(newArrestReport.Details.SentenceMode)
	tf, ts, tl := models.ComputeArrestTotals(newArrestReport.Details.ChargesList, newArrestReport.Details.SentenceMode)
	newArrestReport.Details.TotalFine = tf
	newArrestReport.Details.TotalJailTimeSeconds = ts
	newArrestReport.Details.TotalJailTimeLabel = tl

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	_, err := a.DB.InsertOne(ctx, newArrestReport)
	if err != nil {
		config.ErrorStatus("failed to create new Arrest report", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Arrest report created successfully",
		"id":      newArrestReport.ID.Hex(),
	})
}

// UpdateArrestReportHandler updates the details of an existing Arrest report
func (a ArrestReport) UpdateArrestReportHandler(w http.ResponseWriter, r *http.Request) {
	arrestReportID := mux.Vars(r)["arrest_report_id"]

	var updatedDetails map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updatedDetails); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	bID, err := primitive.ObjectIDFromHex(arrestReportID)
	if err != nil {
		config.ErrorStatus("invalid Arrest report ID", http.StatusBadRequest, w, err)
		return
	}

	update := bson.M{}
	for key, value := range updatedDetails {
		update["arrestReport."+key] = value
	}

	// When the update touches the charge list, recompute the canonical totals
	// server-side (overriding anything the client sent) so stored fine/jail-time
	// values never drift from the authoritative math. The edit form always
	// resubmits chargesList alongside sentenceMode.
	if raw, ok := updatedDetails["chargesList"]; ok {
		var charges []models.ArrestCharge
		if b, mErr := json.Marshal(raw); mErr == nil {
			_ = json.Unmarshal(b, &charges)
		}
		mode, _ := updatedDetails["sentenceMode"].(string)
		mode = models.NormalizeSentenceMode(mode)
		tf, ts, tl := models.ComputeArrestTotals(charges, mode)
		update["arrestReport.sentenceMode"] = mode
		update["arrestReport.totalFine"] = tf
		update["arrestReport.totalJailTimeSeconds"] = ts
		update["arrestReport.totalJailTimeLabel"] = tl
	}

	// Set updatedAt to the current time
	update["arrestReport.updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	filter := bson.M{"_id": bID}
	
	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	
	err = a.DB.UpdateOne(ctx, filter, bson.M{"$set": update})
	if err != nil {
		config.ErrorStatus("failed to update Arrest report", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Arrest report updated successfully"}`))
}

// DeleteArrestReportHandler deletes an existing Arrest report
func (a ArrestReport) DeleteArrestReportHandler(w http.ResponseWriter, r *http.Request) {
	arrestReportID := mux.Vars(r)["arrest_report_id"]

	bID, err := primitive.ObjectIDFromHex(arrestReportID)
	if err != nil {
		config.ErrorStatus("invalid Arrest report ID", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": bID}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Gate: per-issuing-department RestrictCivilianRecordDeletion. Block the
	// delete unless the requester has community-level bypass (owner /
	// administrator / manage-records).
	if a.CommDB != nil {
		report, ferr := a.DB.FindOne(ctx, filter)
		if ferr != nil {
			config.ErrorStatus("failed to find Arrest report", http.StatusNotFound, w, ferr)
			return
		}
		if report != nil {
			requesterID := api.GetAuthenticatedUserIDFromContext(r.Context())
			if denied, derr := enforceRecordDeleteRestriction(ctx, w, a.CommDB, report.Details.DepartmentID, requesterID); derr != nil || denied {
				return
			}
		}
	}

	err = a.DB.DeleteOne(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to delete Arrest report", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Arrest report deleted successfully"}`))
}

// GetArrestReportsByCommunityHandler returns paginated arrest reports for a
// community, sorted most-recent-first. Used by the configurable forms picker
// so officers can start a report from an existing arrest record.
func (a ArrestReport) GetArrestReportsByCommunityHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["community_id"]

	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 50
	}
	Page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || Page < 0 {
		Page = 0
	}
	skip := int64(Page * Limit)
	limit64 := int64(Limit)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"arrestReport.activeCommunityID": communityID}

	dbResp, err := a.DB.Find(ctx, filter, &options.FindOptions{
		Limit: &limit64,
		Skip:  &skip,
		Sort:  bson.M{"_id": -1},
	})
	if err != nil {
		config.ErrorStatus("failed to get arrest reports", http.StatusInternalServerError, w, err)
		return
	}
	totalCount, err := a.DB.CountDocuments(ctx, filter)
	if err != nil {
		totalCount = int64(len(dbResp))
	}
	if dbResp == nil {
		dbResp = []models.ArrestReport{}
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       dbResp,
		"page":       Page,
		"limit":      Limit,
		"totalCount": totalCount,
	})
}

// GetArrestReportsByArresteeIDHandler retrieves all Arrest reports that contain the given arresteeID
func (a ArrestReport) GetArrestReportsByArresteeIDHandler(w http.ResponseWriter, r *http.Request) {
	arresteeID := mux.Vars(r)["arrestee_id"]

	// Parse pagination parameters
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 10 // Default limit
	}
	Page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || Page < 0 {
		Page = 0 // Default page
	}
	skip := int64(Page * Limit)
	limit64 := int64(Limit)

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Create the filter
	filter := bson.M{
		"arrestReport.arrestee.id": arresteeID,
	}

	// Execute queries in parallel for better performance
	type findResult struct {
		reports []models.ArrestReport
		err     error
	}
	type countResult struct {
		total int64
		err   error
	}

	findChan := make(chan findResult, 1)
	countChan := make(chan countResult, 1)

	// Fetch paginated data (async)
	go func() {
		dbResp, err := a.DB.Find(ctx, filter, &options.FindOptions{
			Limit: &limit64,
			Skip:  &skip,
		})
		findChan <- findResult{reports: dbResp, err: err}
	}()

	// Fetch total count (async)
	go func() {
		total, err := a.DB.CountDocuments(ctx, filter)
		countChan <- countResult{total: total, err: err}
	}()

	// Wait for both queries to complete
	findRes := <-findChan
	countRes := <-countChan

	if findRes.err != nil {
		config.ErrorStatus("failed to get arrest reports", http.StatusNotFound, w, findRes.err)
		return
	}

	if countRes.err != nil {
		config.ErrorStatus("failed to get total count of arrest reports", http.StatusInternalServerError, w, countRes.err)
		return
	}

	dbResp := findRes.reports
	totalCount := countRes.total

	// Create paginated response
	paginatedResponse := PaginatedDataResponse{
		Page:       Page,
		TotalCount: totalCount,
		Data:       dbResp,
	}

	// Encode and send the response
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(paginatedResponse)
}
