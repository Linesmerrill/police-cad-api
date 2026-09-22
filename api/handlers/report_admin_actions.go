package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
	templates "github.com/linesmerrill/police-cad-api/templates/html"
)

// noticeDateFormat is how a lift date is written to a player.
const noticeDateFormat = "January 2, 2006"

// offenseTarget is who an action lands on and who hears about it.
type offenseTarget struct {
	Scope         string
	UserID        string
	CommunityID   string
	CommunityName string
	// ContactUsername and ContactEmail are the notified person: the reported
	// user, or the community's owner for a community-scoped action.
	ContactUsername string
	ContactEmail    string
	// OwnerID is set for community scope, so the notice reaches a person.
	OwnerID string
}

// resolveOffenseTarget works out who an upheld report acts on.
func (ra ReportAdmin) resolveOffenseTarget(ctx context.Context, report models.Report) (offenseTarget, error) {
	if report.ItemID == "" {
		return offenseTarget{}, fmt.Errorf("report has no target")
	}

	if report.ItemType == "community" {
		oid, err := primitive.ObjectIDFromHex(report.ItemID)
		if err != nil {
			return offenseTarget{}, fmt.Errorf("invalid community id: %w", err)
		}
		community, err := ra.CDB.FindOneIncludingPending(ctx, bson.M{"_id": oid})
		if err != nil || community == nil {
			return offenseTarget{}, fmt.Errorf("community not found")
		}
		target := offenseTarget{
			Scope:         models.ContentOffenseScopeCommunity,
			CommunityID:   report.ItemID,
			CommunityName: community.Details.Name,
			OwnerID:       community.Details.OwnerID,
		}
		// The notice goes to the owner, who is the person answerable for it.
		if username, email := ra.contactFor(ctx, community.Details.OwnerID); email != "" || username != "" {
			target.ContactUsername, target.ContactEmail = username, email
		}
		return target, nil
	}

	username, email := ra.contactFor(ctx, report.ItemID)
	return offenseTarget{
		Scope:           models.ContentOffenseScopeUser,
		UserID:          report.ItemID,
		ContactUsername: username,
		ContactEmail:    email,
	}, nil
}

// contactFor resolves a user's display name and email.
func (ra ReportAdmin) contactFor(ctx context.Context, userID string) (username, email string) {
	oid, err := primitive.ObjectIDFromHex(userID)
	if err != nil || ra.UDB == nil {
		return "", ""
	}
	var user models.User
	if err := ra.UDB.FindOne(ctx, bson.M{"_id": oid}).Decode(&user); err != nil {
		return "", ""
	}
	return user.Details.Username, user.Details.Email
}

// planForReport assembles the plan for upholding a report: the ladder rung, the
// target, and the notice that would be sent.
func (ra ReportAdmin) planForReport(ctx context.Context, report models.Report) (offensePlan, offenseTarget, error) {
	target, err := ra.resolveOffenseTarget(ctx, report)
	if err != nil {
		return offensePlan{}, offenseTarget{}, err
	}

	filter := offenseTargetFilter(report)
	priorActive, err := ra.CODB.CountDocuments(ctx, mergeFilter(filter, bson.M{"status": models.ContentOffenseStatusActive}))
	if err != nil {
		return offensePlan{}, target, fmt.Errorf("failed to count prior offenses: %w", err)
	}

	inForce := ra.currentPenalty(ctx, filter)
	plan := buildOffensePlan(target.Scope, report.ReportedIssue, int(priorActive), inForce, time.Now())
	return plan, target, nil
}

// mergeFilter combines two filters without mutating either.
func mergeFilter(a, b bson.M) bson.M {
	out := bson.M{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// currentPenalty finds the target's unexpired penalty, if any. Warnings are
// excluded: a warning is a record, not a restriction, and must not block the
// next rung.
func (ra ReportAdmin) currentPenalty(ctx context.Context, targetFilter bson.M) *models.ContentOffense {
	if targetFilter == nil {
		return nil
	}
	filter := mergeFilter(targetFilter, bson.M{
		"status":  models.ContentOffenseStatusActive,
		"penalty": bson.M{"$ne": models.PenaltyActionWarning},
		"$or": []bson.M{
			{"expiresAt": bson.M{"$eq": nil}},
			{"expiresAt": bson.M{"$type": "date", "$gt": primitive.NewDateTimeFromTime(time.Now())}},
		},
	})
	offense, err := ra.CODB.FindOne(ctx, filter)
	if err != nil {
		return nil
	}
	return offense
}

// noticeParams renders the notice for a plan.
func noticeParams(plan offensePlan, target offenseTarget, issue string, testBanner string) templates.ContentOffenseEmailParams {
	params := templates.ContentOffenseEmailParams{
		Username:      target.ContactUsername,
		Scope:         target.Scope,
		CommunityName: target.CommunityName,
		IssuePhrase:   models.IssuePhrase(issue),
		Action:        plan.Action,
		PenaltyLabel:  plan.PenaltyLabel,
		OffenseNumber: plan.OffenseNumber,
		NextPenalty:   plan.NextLabel,
		TestBanner:    testBanner,
	}
	if plan.ExpiresAt != nil {
		params.LiftsAt = plan.ExpiresAt.UTC().Format(noticeDateFormat)
	}
	return params
}

// AdminUpholdPreviewHandler computes what upholding a report would do and
// renders the exact notice, changing nothing.
//
// POST /api/v1/admin/reports/{reportId}/uphold/preview
func (ra ReportAdmin) AdminUpholdPreviewHandler(w http.ResponseWriter, r *http.Request) {
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

	c, err := ra.caseFor(ctx, *report)
	if err != nil {
		config.ErrorStatus("failed to load the case", http.StatusInternalServerError, w, err)
		return
	}

	plan, target, err := ra.planForReport(ctx, c.subject)
	if err != nil {
		config.ErrorStatus("failed to build the plan", http.StatusInternalServerError, w, err)
		return
	}

	body := map[string]interface{}{
		"plan":         plan,
		"contactEmail": target.ContactEmail,
		"targetName":   noticeTargetName(target),
		"reportIds":    reportIDs(c.ladder),
		"reportCount":  len(c.ladder),
		"issue":        c.subject.ReportedIssue,
	}
	if c.blockedByEscalation {
		body["blockedByEscalation"] = true
	}
	if plan.Ladders && !plan.AlreadyInForce && !c.blockedByEscalation {
		params := noticeParams(plan, target, c.subject.ReportedIssue, "")
		htmlBody, textBody := templates.RenderContentOffenseEmail(params)
		body["email"] = map[string]string{
			"subject": templates.ContentOffenseSubject(params),
			"from":    templates.ContentOffenseSenderName() + " <no-reply@linespolice-cad.com>",
			"html":    htmlBody,
			"text":    textBody,
		}
	}
	writeJSON(w, http.StatusOK, body)
}

func noticeTargetName(target offenseTarget) string {
	if target.Scope == models.ContentOffenseScopeCommunity {
		return target.CommunityName
	}
	return target.ContactUsername
}

// AdminUpholdReportHandler records the offense, applies the penalty, sends the
// notice and resolves the report.
//
// POST /api/v1/admin/reports/{reportId}/uphold
func (ra ReportAdmin) AdminUpholdReportHandler(w http.ResponseWriter, r *http.Request) {
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

	report, err := ra.findReport(ctx, mux.Vars(r)["reportId"])
	if err != nil {
		config.InfoStatus("report not found", http.StatusNotFound, w, err)
		return
	}
	if !report.IsOpen() {
		config.ErrorStatus("report has already been decided", http.StatusConflict, w,
			fmt.Errorf("status is %s", report.EffectiveStatus()))
		return
	}

	c, err := ra.caseFor(ctx, *report)
	if err != nil {
		config.ErrorStatus("failed to load the case", http.StatusInternalServerError, w, err)
		return
	}
	if c.blockedByEscalation {
		config.ErrorStatus("this account has an open child safety report", http.StatusConflict, w,
			fmt.Errorf("escalate the child safety report before upholding anything else against the same target"))
		return
	}

	plan, target, err := ra.planForReport(ctx, c.subject)
	if err != nil {
		config.ErrorStatus("failed to build the plan", http.StatusInternalServerError, w, err)
		return
	}
	if !plan.Ladders {
		config.ErrorStatus("this report does not take an automated action", http.StatusBadRequest, w,
			fmt.Errorf("tier %s must be escalated or handled as welfare, not upheld", plan.Tier))
		return
	}
	if plan.AlreadyInForce {
		config.ErrorStatus("this target is already serving a penalty", http.StatusConflict, w,
			fmt.Errorf("reverse the existing offense before issuing another"))
		return
	}

	admin := adminDisplayName(req.CurrentUser)
	now := time.Now()

	offense := models.ContentOffense{
		ID:            primitive.NewObjectID(),
		Scope:         target.Scope,
		UserID:        target.UserID,
		CommunityID:   target.CommunityID,
		CommunityName: target.CommunityName,
		Username:      target.ContactUsername,
		Email:         target.ContactEmail,
		ReportIDs:     reportIDs(c.ladder),
		ReportedIssue: c.subject.ReportedIssue,
		Tier:          plan.Tier,
		OffenseNumber: plan.OffenseNumber,
		Penalty:       plan.Action,
		Reason:        strings.TrimSpace(req.Reason),
		IssuedBy:      admin,
		IssuedByID:    adminID(req.CurrentUser),
		IssuedAt:      primitive.NewDateTimeFromTime(now),
		Status:        models.ContentOffenseStatusActive,
	}
	if plan.ExpiresAt != nil {
		expires := primitive.NewDateTimeFromTime(*plan.ExpiresAt)
		offense.ExpiresAt = &expires
	}

	if _, err := ra.CODB.InsertOne(ctx, offense); err != nil {
		config.ErrorStatus("failed to record the offense", http.StatusInternalServerError, w, err)
		return
	}

	// The offense row is written before the penalty is applied, so a failure
	// here leaves a record to reconcile rather than a silent punishment.
	if err := ra.applyPenalty(ctx, offense, target, admin, now); err != nil {
		zap.S().Errorw("failed to apply penalty", "offenseId", offense.ID.Hex(), "error", err)
		config.ErrorStatus("failed to apply the penalty", http.StatusInternalServerError, w, err)
		return
	}

	emailed := false
	if req.SendEmail == nil || *req.SendEmail {
		params := noticeParams(plan, target, c.subject.ReportedIssue, "")
		if err := ra.sendOffenseNotice(ctx, offense, params); err == nil {
			emailed = true
		}
	}

	// One strike, and every report it answers is closed with it.
	for _, rep := range c.ladder {
		ra.closeReport(ctx, rep, models.ReportStatusResolved, admin, adminID(req.CurrentUser), req.Note, offense.ID.Hex(), plan.Action, now)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":     "report upheld",
		"offenseId":   offense.ID.Hex(),
		"reportCount": len(c.ladder),
		"penalty":     plan.Action,
		"expiresAt":   plan.ExpiresAt,
		"emailed":     emailed,
	})
}

// applyPenalty writes the suspension or delisting the offense calls for.
//
// A warning applies nothing: it is a record that makes the next rung
// defensible, not a restriction.
func (ra ReportAdmin) applyPenalty(ctx context.Context, offense models.ContentOffense, target offenseTarget, admin string, now time.Time) error {
	if offense.Penalty == models.PenaltyActionWarning {
		return nil
	}

	if target.Scope == models.ContentOffenseScopeCommunity {
		oid, err := primitive.ObjectIDFromHex(target.CommunityID)
		if err != nil {
			return err
		}
		suspension := models.ListingSuspension{
			Until:     offense.ExpiresAt,
			OffenseID: offense.ID.Hex(),
			Reason:    offense.Reason,
			IssuedBy:  admin,
			IssuedAt:  primitive.NewDateTimeFromTime(now),
		}
		// community.visibility is never touched: it is the owner's setting, and
		// relisting must not hand back a state we invented.
		return ra.CDB.UpdateOne(ctx,
			bson.M{"_id": oid},
			bson.M{"$set": bson.M{"community.listingSuspension": suspension}})
	}

	oid, err := primitive.ObjectIDFromHex(target.UserID)
	if err != nil {
		return err
	}
	suspension := models.Suspension{
		Until:     offense.ExpiresAt,
		OffenseID: offense.ID.Hex(),
		Reason:    offense.Reason,
		IssuedBy:  admin,
		IssuedAt:  primitive.NewDateTimeFromTime(now),
	}
	_, err = ra.UDB.UpdateOne(ctx,
		bson.M{"_id": oid},
		bson.M{"$set": bson.M{"user.suspension": suspension}})
	return err
}

// closeReport records the decision on the report itself.
func (ra ReportAdmin) closeReport(ctx context.Context, report models.Report, status, admin, adminAccountID, note, offenseID, action string, now time.Time) {
	set := bson.M{
		"status":         status,
		"reviewedByName": admin,
		"reviewedById":   adminAccountID,
		"reviewedAt":     primitive.NewDateTimeFromTime(now),
		"updatedAt":      primitive.NewDateTimeFromTime(now),
		"tier":           report.EffectiveTier(),
		// active=false retires the legacy flag as each report is decided,
		// rather than in one sweep that would rewrite history.
		"active": false,
	}
	if note != "" {
		set["internalNote"] = note
	}
	if offenseID != "" {
		set["offenseId"] = offenseID
	}
	if action != "" {
		set["actionTaken"] = action
	}
	if err := ra.RDB.UpdateOne(ctx, bson.M{"_id": report.ID}, bson.M{"$set": set}); err != nil {
		zap.S().Errorw("failed to close report", "reportId", report.ID.Hex(), "error", err)
	}
}

// sendOffenseNotice emails the target and stamps EmailSentAt on success.
//
// A missing address is not an error worth failing the action over: there is no
// bounce handling anywhere in the product, so a send is best-effort by
// definition. A null EmailSentAt means we could not tell them, which the
// console shows.
func (ra ReportAdmin) sendOffenseNotice(ctx context.Context, offense models.ContentOffense, params templates.ContentOffenseEmailParams) error {
	if strings.TrimSpace(offense.Email) == "" {
		zap.S().Infow("no email on file for offense notice", "offenseId", offense.ID.Hex())
		return fmt.Errorf("no email on file")
	}

	htmlBody, textBody := templates.RenderContentOffenseEmail(params)
	if err := sendContentOffenseEmail(offense.Email, offense.Username, templates.ContentOffenseSubject(params), htmlBody, textBody); err != nil {
		return err
	}

	sentAt := primitive.NewDateTimeFromTime(time.Now())
	if err := ra.CODB.UpdateOne(ctx, bson.M{"_id": offense.ID}, bson.M{"$set": bson.M{"emailSentAt": sentAt}}); err != nil {
		zap.S().Warnw("notice sent but the stamp failed", "offenseId", offense.ID.Hex(), "error", err)
	}
	return nil
}

// sendContentOffenseEmail sends a moderation notice. The From display name is
// the Content Resolution Team; the address stays the shared no-reply one.
func sendContentOffenseEmail(toEmail, toName, subject, htmlContent, plainText string) error {
	from := mail.NewEmail(templates.ContentOffenseSenderName(), "no-reply@linespolice-cad.com")
	to := mail.NewEmail(toName, toEmail)
	message := mail.NewSingleEmail(from, subject, to, plainText, htmlContent)

	client := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))
	response, err := client.Send(message)
	if err != nil {
		zap.S().Errorw("failed to send offense notice", "error", err, "to", toEmail)
		return err
	}
	if response.StatusCode >= 400 {
		zap.S().Errorw("sendgrid rejected the offense notice", "status", response.StatusCode, "to", toEmail)
		return fmt.Errorf("sendgrid error: status %d", response.StatusCode)
	}
	return nil
}

// AdminDismissReportHandler closes a report with no action. Nothing is recorded
// against the accused: a filed report that we did not uphold must leave no mark.
//
// POST /api/v1/admin/reports/{reportId}/dismiss
func (ra ReportAdmin) AdminDismissReportHandler(w http.ResponseWriter, r *http.Request) {
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
	if !report.IsOpen() {
		config.ErrorStatus("report has already been decided", http.StatusConflict, w,
			fmt.Errorf("status is %s", report.EffectiveStatus()))
		return
	}

	status := models.ReportStatusDismissed
	// A self-harm report is not an accusation to dismiss. It closes as welfare,
	// which records that a person read it and that no penalty was appropriate.
	if report.EffectiveTier() == models.ReportTierWelfare {
		status = models.ReportStatusWelfare
	}

	// Close the whole case on this track. Other tracks stay open: dismissing
	// spam must not close a child safety allegation against the same account.
	group, err := ra.openReportsAgainst(ctx, *report)
	if err != nil {
		config.ErrorStatus("failed to load the case", http.StatusInternalServerError, w, err)
		return
	}
	closing := onTrack(group, reportTrack(*report))
	admin := adminDisplayName(req.CurrentUser)
	now := time.Now()
	for _, rep := range closing {
		ra.closeReport(ctx, rep, status, admin, adminID(req.CurrentUser), req.Note, "", "", now)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "report closed", "status": status, "reportCount": len(closing)})
}

// reportCase is the set of open reports about one target that a decision on a
// ladder report would act on.
type reportCase struct {
	// ladder is every open minor or serious report about the target. Upholding
	// issues one strike for all of them.
	ladder []models.Report
	// subject is the report the strike is issued under: the most severe issue
	// in the case, so a case of one hate and two spam reports is a hate case.
	subject models.Report
	// blockedByEscalation is set when the same target has an open child safety
	// report. That has to be escalated first, not overtaken by a week's ban.
	blockedByEscalation bool
}

func (ra ReportAdmin) caseFor(ctx context.Context, report models.Report) (reportCase, error) {
	group, err := ra.openReportsAgainst(ctx, report)
	if err != nil {
		return reportCase{}, err
	}
	c := reportCase{subject: report}
	if reportTrack(report) != reportTrackLadder {
		c.ladder = []models.Report{report}
		return c, nil
	}
	c.ladder = onTrack(group, reportTrackLadder)
	c.subject.ReportedIssue = mostSevereIssue(c.ladder, report.ReportedIssue)
	c.blockedByEscalation = len(onTrack(group, reportTrackEscalate)) > 0
	return c, nil
}
