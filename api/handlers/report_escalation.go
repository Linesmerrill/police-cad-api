package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// legalHoldYears is how long an escalated account's data must be retained. A
// completed CyberTipline submission is treated as a preservation request,
// which carries a one-year obligation.
const legalHoldYears = 1

// cyberTiplinePackage is everything the CyberTipline web form asks for, in the
// order it asks for it, assembled so a person can paste rather than retype.
//
// Submission itself is manual and deliberately so. The reporting API needs
// credentials issued through NCMEC's ESP application and vetting process, which
// is not a key we can generate. At this volume, assembling the package and
// having a person file it is the right shape, and if registration ever happens
// the assembly is already the hard part.
type cyberTiplinePackage struct {
	SubmittedBy string `json:"submittedBy"`
	PreparedAt  string `json:"preparedAt"`

	IncidentType string `json:"incidentType"`
	IncidentTime string `json:"incidentTime"`

	SuspectUsername  string `json:"suspectUsername"`
	SuspectEmail     string `json:"suspectEmail"`
	SuspectUserID    string `json:"suspectUserId"`
	SuspectSignupAt  string `json:"suspectSignupAt,omitempty"`
	SuspectCommunity string `json:"suspectCommunity,omitempty"`

	ReporterUsername string `json:"reporterUsername,omitempty"`
	ReporterUserID   string `json:"reporterUserId,omitempty"`

	ReportFiledAt string `json:"reportFiledAt"`
	ReportedIssue string `json:"reportedIssue"`
	// ReportText is reproduced verbatim. It is evidence, not copy, so it is
	// never paraphrased or trimmed.
	ReportText string `json:"reportText"`

	// Related are the other open child safety reports about the same account,
	// filed by other people. Each is reproduced verbatim like the first.
	Related []cyberTiplineRelated `json:"related,omitempty"`

	// PlainText is the whole package as one block for the clipboard.
	PlainText string `json:"plainText"`
}

type cyberTiplineRelated struct {
	FiledAt          string `json:"filedAt"`
	ReporterUsername string `json:"reporterUsername,omitempty"`
	ReporterUserID   string `json:"reporterUserId,omitempty"`
	ReportText       string `json:"reportText"`
}

// addRelatedReports appends further reports about the same account to the
// package, and to its clipboard text, above the actions-taken footer.
func (pkg *cyberTiplinePackage) addRelatedReports(reports []models.Report, reporterName func(string) string) {
	if len(reports) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("\nFURTHER REPORTS ABOUT THE SAME ACCOUNT (" + fmt.Sprint(len(reports)) + ")\n")
	for i, rep := range reports {
		rel := cyberTiplineRelated{
			FiledAt:          rep.CreatedAt.Time().UTC().Format(time.RFC3339),
			ReporterUsername: reporterName(rep.ReportedByID),
			ReporterUserID:   rep.ReportedByID,
			ReportText:       rep.AdditionalDetails,
		}
		pkg.Related = append(pkg.Related, rel)

		b.WriteString(fmt.Sprintf("  %d. Filed %s by %s (%s)\n", i+1, rel.FiledAt, orNotRecorded(rel.ReporterUsername), orNotRecorded(rel.ReporterUserID)))
		if strings.TrimSpace(rel.ReportText) == "" {
			b.WriteString("     (no detail written)\n")
			continue
		}
		for _, line := range strings.Split(rel.ReportText, "\n") {
			b.WriteString("     " + line + "\n")
		}
	}
	marker := "\nPLATFORM ACTIONS TAKEN\n"
	if i := strings.Index(pkg.PlainText, marker); i >= 0 {
		pkg.PlainText = pkg.PlainText[:i] + b.String() + pkg.PlainText[i:]
	} else {
		pkg.PlainText += b.String()
	}
}

// buildCyberTiplinePackage assembles the submission.
func buildCyberTiplinePackage(report models.Report, target offenseTarget, suspectSignup, reporterName, admin string, now time.Time) cyberTiplinePackage {
	pkg := cyberTiplinePackage{
		SubmittedBy:      admin,
		PreparedAt:       now.UTC().Format(time.RFC3339),
		IncidentType:     "Online enticement / child safety report from a platform user",
		IncidentTime:     report.CreatedAt.Time().UTC().Format(time.RFC3339),
		SuspectUsername:  target.ContactUsername,
		SuspectEmail:     target.ContactEmail,
		SuspectUserID:    target.UserID,
		SuspectSignupAt:  suspectSignup,
		SuspectCommunity: target.CommunityName,
		ReporterUsername: reporterName,
		ReporterUserID:   report.ReportedByID,
		ReportFiledAt:    report.CreatedAt.Time().UTC().Format(time.RFC3339),
		ReportedIssue:    report.ReportedIssue,
		ReportText:       report.AdditionalDetails,
	}

	var b strings.Builder
	b.WriteString("LINES POLICE CAD - CYBERTIPLINE SUBMISSION PACKAGE\n")
	b.WriteString("Prepared " + pkg.PreparedAt + " by " + pkg.SubmittedBy + "\n\n")
	b.WriteString("INCIDENT\n")
	b.WriteString("  Type: " + pkg.IncidentType + "\n")
	b.WriteString("  Reported at: " + pkg.ReportFiledAt + "\n")
	b.WriteString("  Category selected by the reporter: " + pkg.ReportedIssue + "\n\n")
	b.WriteString("SUSPECT ACCOUNT\n")
	b.WriteString("  Username: " + orNotRecorded(pkg.SuspectUsername) + "\n")
	b.WriteString("  Email: " + orNotRecorded(pkg.SuspectEmail) + "\n")
	b.WriteString("  Internal user id: " + orNotRecorded(pkg.SuspectUserID) + "\n")
	if pkg.SuspectSignupAt != "" {
		b.WriteString("  Account created: " + pkg.SuspectSignupAt + "\n")
	}
	if pkg.SuspectCommunity != "" {
		b.WriteString("  Community: " + pkg.SuspectCommunity + "\n")
	}
	b.WriteString("\nREPORTING USER\n")
	b.WriteString("  Username: " + orNotRecorded(pkg.ReporterUsername) + "\n")
	b.WriteString("  Internal user id: " + orNotRecorded(pkg.ReporterUserID) + "\n")
	b.WriteString("\nREPORT AS SUBMITTED (verbatim)\n")
	if strings.TrimSpace(pkg.ReportText) == "" {
		b.WriteString("  (the reporter selected the category but wrote no detail)\n")
	} else {
		for _, line := range strings.Split(pkg.ReportText, "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
	b.WriteString("\nPLATFORM ACTIONS TAKEN\n")
	b.WriteString("  The account has been suspended and placed under a legal hold.\n")
	b.WriteString("  Account data is retained and excluded from every deletion path.\n")
	pkg.PlainText = b.String()
	return pkg
}

func orNotRecorded(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(not recorded)"
	}
	return v
}

// AdminEscalateReportHandler prepares a CyberTipline submission, suspends the
// account and places it under a legal hold.
//
// POST /api/v1/admin/reports/{reportId}/escalate
//
// No notice is sent. Telling an account that a child-safety report about it is
// being escalated is exactly the wrong move.
func (ra ReportAdmin) AdminEscalateReportHandler(w http.ResponseWriter, r *http.Request) {
	var req reportAdminRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	if !authorizeReportAdmin(w, r, req.CurrentUser) {
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	report, err := ra.findReport(ctx, mux.Vars(r)["reportId"])
	if err != nil {
		config.InfoStatus("report not found", http.StatusNotFound, w, err)
		return
	}

	target, err := ra.resolveOffenseTarget(ctx, *report)
	if err != nil {
		config.ErrorStatus("failed to resolve the reported account", http.StatusInternalServerError, w, err)
		return
	}

	admin := adminDisplayName(req.CurrentUser)
	now := time.Now()

	// Every open child safety report about the same account is part of the
	// same escalation. The report acted on is included even if it was filed
	// under another category, because a person decided it belongs here.
	escalating := []models.Report{*report}
	if group, err := ra.openReportsAgainst(ctx, *report); err == nil {
		for _, rep := range onTrack(group, reportTrackEscalate) {
			if rep.ID != report.ID {
				escalating = append(escalating, rep)
			}
		}
	}

	reporterName, _ := ra.contactFor(ctx, report.ReportedByID)
	pkg := buildCyberTiplinePackage(*report, target, ra.signupDate(ctx, target.UserID), reporterName, admin, now)
	pkg.addRelatedReports(escalating[1:], func(id string) string {
		name, _ := ra.contactFor(ctx, id)
		return name
	})

	// Suspend indefinitely and hold the data. Order matters: the hold is what
	// stops a later deletion destroying what has to be preserved, so it is
	// written even if the suspension fails.
	if err := ra.placeLegalHold(ctx, target, report.ID.Hex(), admin, now); err != nil {
		config.ErrorStatus("failed to place the legal hold", http.StatusInternalServerError, w, err)
		return
	}
	if err := ra.suspendIndefinitely(ctx, target, report.ID.Hex(), admin, now); err != nil {
		zap.S().Errorw("legal hold placed but suspension failed", "reportId", report.ID.Hex(), "error", err)
	}

	set := bson.M{
		"status":         models.ReportStatusEscalated,
		"tier":           models.ReportTierEscalate,
		"escalatedAt":    primitive.NewDateTimeFromTime(now),
		"escalatedBy":    admin,
		"reviewedByName": admin,
		"reviewedById":   adminID(req.CurrentUser),
		"reviewedAt":     primitive.NewDateTimeFromTime(now),
		"updatedAt":      primitive.NewDateTimeFromTime(now),
		"active":         false,
	}
	if strings.TrimSpace(req.Note) != "" {
		set["internalNote"] = req.Note
	}
	for _, rep := range escalating {
		if err := ra.RDB.UpdateOne(ctx, bson.M{"_id": rep.ID}, bson.M{"$set": set}); err != nil {
			zap.S().Errorw("failed to mark report escalated", "reportId", rep.ID.Hex(), "error", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":    "report escalated, account suspended and held",
		"submitAt":   "https://report.cybertip.org",
		"package":    pkg,
		"legalHold":  true,
		"holdExpiry": now.AddDate(legalHoldYears, 0, 0).UTC().Format(time.RFC3339),
	})
}

// signupDate reads the account creation timestamp, best effort.
func (ra ReportAdmin) signupDate(ctx context.Context, userID string) string {
	oid, err := primitive.ObjectIDFromHex(userID)
	if err != nil || ra.UDB == nil {
		return ""
	}
	var user models.User
	if err := ra.UDB.FindOne(ctx, bson.M{"_id": oid}).Decode(&user); err != nil {
		return ""
	}
	switch v := user.Details.CreatedAt.(type) {
	case primitive.DateTime:
		return v.Time().UTC().Format(time.RFC3339)
	case time.Time:
		return v.UTC().Format(time.RFC3339)
	case string:
		return v
	default:
		// The ObjectID carries its own creation timestamp, which is a
		// reasonable stand-in when the field was never written.
		return oid.Timestamp().UTC().Format(time.RFC3339)
	}
}

// placeLegalHold blocks every deletion path for the account.
func (ra ReportAdmin) placeLegalHold(ctx context.Context, target offenseTarget, reportID, admin string, now time.Time) error {
	userID := target.UserID
	if userID == "" {
		userID = target.OwnerID
	}
	if userID == "" {
		return fmt.Errorf("no account to hold")
	}
	oid, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return err
	}

	expires := primitive.NewDateTimeFromTime(now.AddDate(legalHoldYears, 0, 0))
	hold := models.LegalHold{
		SetAt:     primitive.NewDateTimeFromTime(now),
		SetBy:     admin,
		ReportID:  reportID,
		ExpiresAt: &expires,
		Note:      "Escalated to the NCMEC CyberTipline. Retain for one year.",
	}
	_, err = ra.UDB.UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": bson.M{"user.legalHold": hold}})
	return err
}

// suspendIndefinitely locks the account with no expiry. An escalation is not a
// ladder rung and does not lift itself.
func (ra ReportAdmin) suspendIndefinitely(ctx context.Context, target offenseTarget, reportID, admin string, now time.Time) error {
	userID := target.UserID
	if userID == "" {
		userID = target.OwnerID
	}
	oid, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return err
	}
	suspension := models.Suspension{
		Reason:   "Escalated child-safety report " + reportID,
		IssuedBy: admin,
		IssuedAt: primitive.NewDateTimeFromTime(now),
	}
	_, err = ra.UDB.UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": bson.M{"user.suspension": suspension}})
	return err
}

// AdminReverseOffenseHandler overturns an offense on appeal: it stops being
// enforced and stops counting toward the next rung, so a successful appeal
// genuinely un-escalates.
//
// POST /api/v1/admin/offenses/{offenseId}/reverse
func (ra ReportAdmin) AdminReverseOffenseHandler(w http.ResponseWriter, r *http.Request) {
	var req reportAdminRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	if !authorizeReportAdmin(w, r, req.CurrentUser) {
		return
	}
	if len(strings.TrimSpace(req.Reason)) < 3 {
		config.ErrorStatus("a reason is required", http.StatusBadRequest, w,
			fmt.Errorf("reason must be at least 3 characters"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["offenseId"])
	if err != nil {
		config.ErrorStatus("invalid offense id", http.StatusBadRequest, w, err)
		return
	}
	offense, err := ra.CODB.FindOne(ctx, bson.M{"_id": oid})
	if err != nil || offense == nil {
		config.InfoStatus("offense not found", http.StatusNotFound, w, err)
		return
	}
	if offense.Status == models.ContentOffenseStatusReversed {
		config.ErrorStatus("offense is already reversed", http.StatusConflict, w,
			fmt.Errorf("offense %s", oid.Hex()))
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	admin := adminDisplayName(req.CurrentUser)
	if err := ra.CODB.UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": bson.M{
		"status":         models.ContentOffenseStatusReversed,
		"reversedBy":     admin,
		"reversedById":   adminID(req.CurrentUser),
		"reversedAt":     now,
		"reversalReason": strings.TrimSpace(req.Reason),
	}}); err != nil {
		config.ErrorStatus("failed to reverse the offense", http.StatusInternalServerError, w, err)
		return
	}

	// Lift the penalty this offense applied, but only if it is still the one in
	// force: a later offense may have replaced it, and clearing that would
	// release someone we did not mean to.
	if err := ra.liftPenalty(ctx, *offense); err != nil {
		zap.S().Errorw("offense reversed but the penalty was not lifted", "offenseId", oid.Hex(), "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "offense reversed"})
}

// liftPenalty clears a suspension or delisting that this offense placed.
func (ra ReportAdmin) liftPenalty(ctx context.Context, offense models.ContentOffense) error {
	if offense.Penalty == models.PenaltyActionWarning {
		return nil
	}
	if offense.Scope == models.ContentOffenseScopeCommunity {
		oid, err := primitive.ObjectIDFromHex(offense.CommunityID)
		if err != nil {
			return err
		}
		return ra.CDB.UpdateOne(ctx,
			bson.M{"_id": oid, "community.listingSuspension.offenseId": offense.ID.Hex()},
			bson.M{"$unset": bson.M{"community.listingSuspension": ""}})
	}
	oid, err := primitive.ObjectIDFromHex(offense.UserID)
	if err != nil {
		return err
	}
	_, err = ra.UDB.UpdateOne(ctx,
		bson.M{"_id": oid, "user.suspension.offenseId": offense.ID.Hex()},
		bson.M{"$unset": bson.M{"user.suspension": ""}})
	return err
}
