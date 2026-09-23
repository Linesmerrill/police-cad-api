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
	// DBHelper reads the reported content so the server can snapshot it
	// itself. A client-supplied copy would let a reporter invent what someone
	// wrote, and would vanish the moment the author edited it.
	DBHelper databases.DatabaseHelper
}

// MaxReportDetailsLength bounds the free-text note. The website and the
// mobile app enforce the same limit in their inputs, so a player hits it while
// typing rather than on submit.
const MaxReportDetailsLength = 2000

// MinReportDetailsLength is what a report needs to be actionable. Every report
// filed before this was a category and nothing else, or a sentence about
// something that happened on Discord. Staff cannot verify either.
//
// Only asked of clients that send a location, so an older mobile build keeps
// working until people update.
const MinReportDetailsLength = 20

// issueImpersonation needs to know who is being impersonated, or the claim
// cannot be checked at all.
const issueImpersonation = "impersonation"

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
	if len([]rune(r.ImpersonatedName)) > MaxReportDetailsLength {
		return fmt.Errorf("the name being impersonated must be %d characters or fewer", MaxReportDetailsLength)
	}

	switch r.Location {
	case models.LocationUnknown:
		// An older mobile build, which never asked. Accepted and labelled.
		return nil
	case models.LocationInApp:
	default:
		// Clients send people to the right platform instead of filing here, so
		// anything else is a client that skipped the question.
		return fmt.Errorf("reports can only be filed about content in Lines Police CAD")
	}

	if len([]rune(strings.TrimSpace(r.AdditionalDetails))) < MinReportDetailsLength {
		return fmt.Errorf("please describe what you saw, in at least %d characters", MinReportDetailsLength)
	}
	if strings.EqualFold(strings.TrimSpace(r.ReportedIssue), issueImpersonation) &&
		strings.TrimSpace(r.ImpersonatedName) == "" {
		return fmt.Errorf("tell us who this account is pretending to be")
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
	report.Location = strings.ToLower(strings.TrimSpace(report.Location))
	report.ImpersonatedName = strings.TrimSpace(report.ImpersonatedName)

	if err := validateNewReport(report); err != nil {
		config.ErrorStatus(err.Error(), http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Load the reported content and copy what it says. Nothing the client sent
	// about the content is trusted.
	if report.Target != nil && re.DBHelper != nil {
		resolver := targetResolver{db: re.DBHelper}
		resolved, err := resolver.resolveTarget(ctx, *report.Target)
		if err != nil {
			config.ErrorStatus(err.Error(), http.StatusBadRequest, w, err)
			return
		}
		if !resolver.reporterCanSee(ctx, report.ReportedByID, resolved.requiresMembershipOf) {
			// Members-only content. Without this anyone could file reports
			// about a community they have never been in.
			config.ErrorStatus("you can only report things you can see", http.StatusForbidden, w,
				fmt.Errorf("reporter is not a member of %s", resolved.requiresMembershipOf))
			return
		}
		report.Snapshot = &resolved.snapshot
	}

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

// ReportableTargetsHandler lists what can be reported and which parts of each,
// so the website and the app render the same choices without repeating them.
//
// GET /api/v1/report/targets
func (re Report) ReportableTargetsHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"kinds": models.ReportableKinds()})
}
