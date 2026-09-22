package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Report handles report-related requests
type Report struct {
	RDB databases.ReportDatabase
}

// MaxReportDetailsLength bounds the free-text note. The website and the
// mobile app enforce the same limit in their inputs, so a player hits it while
// typing rather than on submit.
const MaxReportDetailsLength = 2000

// validateNewReport checks a report before it is stored. It used to accept
// anything, including a report with no target or no reporter, which then sat
// in the queue unattributable.
func validateNewReport(r models.Report) error {
	if _, err := primitive.ObjectIDFromHex(r.ItemID); err != nil {
		return fmt.Errorf("itemId must be a valid id")
	}
	switch r.ItemType {
	case reportItemTypeUser, reportItemTypeCommunity:
	default:
		return fmt.Errorf("itemType must be user or community")
	}
	if !r.ReportType.IsValid() {
		return fmt.Errorf("reportType is not recognised")
	}
	if strings.TrimSpace(r.ReportedIssue) == "" {
		return fmt.Errorf("reportedIssue is required")
	}
	if r.ReportedByID == "" {
		return fmt.Errorf("the reporting user could not be identified")
	}
	if len([]rune(r.AdditionalDetails)) > MaxReportDetailsLength {
		return fmt.Errorf("additional details must be %d characters or fewer", MaxReportDetailsLength)
	}
	return nil
}

// CreateReportHandler creates a new report
func (re Report) CreateReportHandler(w http.ResponseWriter, r *http.Request) {
	var report models.Report

	// Parse the request body to get the report details
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// The reporter is whoever the token says it is. reportedById in the body was
	// taken on trust, so anyone could file a report in someone else's name.
	// The mobile app sends a token; the website files through its own server
	// with the session user, and has no token to send.
	if actor := api.GetAuthenticatedUserIDFromContext(r.Context()); actor != "" {
		report.ReportedByID = actor
	}
	report.ItemType = strings.ToLower(strings.TrimSpace(report.ItemType))
	report.ReportedIssue = strings.TrimSpace(report.ReportedIssue)

	if err := validateNewReport(report); err != nil {
		config.ErrorStatus(err.Error(), http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// One open report per person per target. A second tap, or someone filing
	// the same complaint over and over, adds nothing a moderator does not
	// already have in front of them, and would inflate the case count. Once the
	// first is decided they can report again.
	if re.hasOpenReport(ctx, report) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "Report already received", "duplicate": true}`))
		return
	}

	// Generate a new _id for the report
	report.ID = primitive.NewObjectID()
	// Set the createdAt field to the current time
	report.CreatedAt = primitive.NewDateTimeFromTime(time.Now())
	// Set the report to active by default
	report.Active = true
	// New reports enter the moderation queue unread, ranked so the severe ones
	// sort to the top of it.
	report.Status = models.ReportStatusNew
	rank := models.ReportSeverityRank(report.ReportedIssue)
	report.SeverityRank = &rank
	report.Tier = models.ReportTierForIssue(report.ReportedIssue)

	// Insert the new report into the database
	if _, err := re.RDB.InsertOne(ctx, report); err != nil {
		config.ErrorStatus("failed to insert report", http.StatusInternalServerError, w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(`{"message": "Report created successfully"}`))
}

// hasOpenReport reports whether this person already has an undecided report
// about this target. A lookup failure is not a reason to drop a report, so it
// errs toward storing it.
func (re Report) hasOpenReport(ctx context.Context, report models.Report) bool {
	n, err := re.RDB.CountDocuments(ctx, andClauses(
		bson.M{"reportedById": report.ReportedByID, "itemId": report.ItemID},
		typeClause(report.ItemType),
		bson.M{"$or": []bson.M{
			{"status": models.ReportStatusNew},
			{"status": models.ReportStatusUnderReview},
			{"status": bson.M{"$eq": nil}},
		}},
	))
	return err == nil && n > 0
}

// OpenReportHandler says whether the caller already has an undecided report
// about a target, so a client can say "you've already reported this" when the
// form opens rather than walking the player through it only to discard the
// second report on submit.
//
// GET /api/v1/report/open?itemId=&itemType=
//
// The caller is the token user (the mobile app). The website has no token and
// asks through its own server, which names the session user in reportedById
// and proves it is our server with the gateway secret; a reportedById without
// that secret is ignored, so nobody can probe another player's reports.
func (re Report) OpenReportHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	report := models.Report{
		ItemID:   strings.TrimSpace(q.Get("itemId")),
		ItemType: strings.ToLower(strings.TrimSpace(q.Get("itemType"))),
	}

	report.ReportedByID = api.GetAuthenticatedUserIDFromContext(r.Context())
	if report.ReportedByID == "" && HasValidGatewaySecret(r) {
		report.ReportedByID = strings.TrimSpace(q.Get("reportedById"))
	}
	if report.ReportedByID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no reporting user"))
		return
	}
	if _, err := primitive.ObjectIDFromHex(report.ItemID); err != nil {
		config.ErrorStatus("itemId must be a valid id", http.StatusBadRequest, w, err)
		return
	}
	if report.ItemType != reportItemTypeUser && report.ItemType != reportItemTypeCommunity {
		config.ErrorStatus("itemType must be user or community", http.StatusBadRequest, w, fmt.Errorf("itemType %q", report.ItemType))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	writeJSON(w, http.StatusOK, map[string]bool{"open": re.hasOpenReport(ctx, report)})
}
