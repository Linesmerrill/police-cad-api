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
	"github.com/linesmerrill/police-cad-api/platforms"
	templates "github.com/linesmerrill/police-cad-api/templates/html"
)

// Automated screening of creator applications.
//
// Review used to be: application submitted -> admins emailed -> a human works
// out whether the channel is real, belongs to the applicant, and is big enough.
// The first two were unanswerable by eye, which is how someone got an
// application through listing a channel they did not own.
//
// The machine-checkable parts now run on a schedule and admins are only emailed
// once an application has passed them. A bogus submission never reaches an
// inbox; the applicant sees exactly which check failed and why.

// ScreenPendingApplications re-runs the automated checks over every application
// awaiting review, persists the outcome, and emails admins the moment one turns
// reviewable. Idempotent: an application already notified is not notified again.
//
// Returns how many were screened, for the caller's log.
func (cc ContentCreator) ScreenPendingApplications(ctx context.Context) (int, error) {
	cursor, err := cc.AppDB.Find(ctx, bson.M{
		"status": bson.M{"$in": []string{"submitted", "under_review"}},
	})
	if err != nil {
		return 0, err
	}
	defer cursor.Close(ctx)

	var apps []models.ContentCreatorApplication
	if err := cursor.All(ctx, &apps); err != nil {
		return 0, err
	}

	screened := 0
	for i := range apps {
		app := &apps[i]
		res := screenApplication(ctx, app, platforms.For)
		now := primitive.NewDateTimeFromTime(time.Now())

		set := bson.M{
			"checks":          res.Checks,
			"checksPassed":    res.Passed,
			"checksLastRunAt": now,
			"checkAttempts":   app.CheckAttempts + 1,
			"updatedAt":       now,
		}
		// Fold in anything the pass learned about the channels themselves, so a
		// creator who verified via the scheduler is as verified as one who used
		// the button.
		for idx := range res.OwnershipVerified {
			p := fmt.Sprintf("platforms.%d.", idx)
			set[p+"verificationStatus"] = models.PlatformVerified
			set[p+"verificationMethod"] = "api"
			set[p+"verifiedAt"] = now
			set[p+"verificationCode"] = ""
		}
		for idx, count := range res.FollowerCounts {
			p := fmt.Sprintf("platforms.%d.", idx)
			if app.Platforms[idx].ReportedFollowerCount == 0 {
				set[p+"reportedFollowerCount"] = app.Platforms[idx].FollowerCount
			}
			set[p+"followerCount"] = count
		}

		// Screening is settled once it is not going to change on its own: either
		// everything passed, or something failed outright. Only a pending check
		// is worth another sweep.
		settled := res.Passed || res.Blocked
		set["checksSettled"] = settled

		notifyAdmin := false
		notifyApplicant := false
		autoReject := false

		switch {
		case res.Passed && app.AdminNotifiedAt == nil:
			notifyAdmin = true

		case res.Blocked && res.FollowerShortfall:
			// A hard program requirement, measured from the channel itself. There
			// is nothing for a reviewer to weigh, so it is decided here rather
			// than sitting in a queue waiting for someone to reach the same
			// conclusion. The applicant is told why and can reapply if they grow.
			autoReject = true

		case res.Blocked && app.ApplicantNotifiedAt == nil:
			// This is the state that used to notify nobody. Admins are only told
			// on pass, the applicant is only told on rejection, so an application
			// that failed screening but was never rejected sat silently forever.
			// The applicant is always told what failed and how to fix it.
			notifyApplicant = true

			// Whether it also deserves an admin's attention depends on what
			// failed. A channel nobody has proved they own is exactly the noise
			// this system exists to absorb. But a real person, on a channel they
			// have proved is theirs, who fell short of a requirement is a
			// judgement call — 252 followers against a 500 minimum might still be
			// someone worth having. That reaches a human.
			if res.OwnershipProven && app.AdminNotifiedAt == nil {
				notifyAdmin = true
			}
		}

		if autoReject {
			set["status"] = "rejected"
			set["rejectionReason"] = res.FollowerReason
			set["reviewedAt"] = now
			set["applicantNotifiedAt"] = now
		}
		if notifyAdmin {
			set["adminNotifiedAt"] = now
		}
		if notifyApplicant {
			set["applicantNotifiedAt"] = now
		}

		if err := cc.AppDB.UpdateOne(ctx, bson.M{"_id": app.ID}, bson.M{"$set": set}); err != nil {
			zap.S().Errorw("failed to persist application screening", "applicationId", app.ID.Hex(), "error", err)
			continue
		}
		screened++

		total := 0
		for _, p := range app.Platforms {
			if c, ok := res.FollowerCounts[indexOfPlatform(app, p)]; ok {
				total += c
			} else {
				total += p.FollowerCount
			}
		}

		if autoReject {
			zap.S().Infow("creator application auto-rejected: below the follower minimum",
				"applicationId", app.ID.Hex(), "reason", res.FollowerReason)
			cc.sendApplicationDecisionEmail(ctx, app.UserID, app.DisplayName, "rejected", res.FollowerReason, "")
		}
		if notifyApplicant {
			zap.S().Infow("creator application failed automated checks, telling the applicant",
				"applicationId", app.ID.Hex(), "reasons", res.FailureReasons,
				"ownershipProven", res.OwnershipProven)
			cc.sendChecksFailedEmail(ctx, app, res.FailureReasons, res.OwnershipProven)
		}
		if notifyAdmin {
			cc.notifyAdminsApplicationReady(ctx, app, total)
		}
	}
	return screened, nil
}

// sendChecksFailedEmail tells an applicant which automated check failed and what
// to do about it, so a failed application is never a silence.
func (cc ContentCreator) sendChecksFailedEmail(ctx context.Context, app *models.ContentCreatorApplication, reasons []string, ownershipProven bool) {
	var user struct {
		Details struct {
			Email    string `bson:"email"`
			Username string `bson:"username"`
		} `bson:"user"`
	}
	if err := cc.UDB.FindOne(ctx, bson.M{"_id": app.UserID}).Decode(&user); err != nil || user.Details.Email == "" {
		zap.S().Errorw("cannot tell applicant their checks failed: no email on file",
			"applicationId", app.ID.Hex(), "error", err)
		return
	}

	subject := "Your Creator Program application needs a change - Lines Police CAD"
	htmlContent := templates.RenderChecksFailedEmail(app.DisplayName, reasons, ownershipProven)
	plainText := fmt.Sprintf(
		"Hi %s, we ran the automatic checks on your Creator Program application and something needs your attention: %s. "+
			"Fix it at https://www.linespolice-cad.com/content-creators/me and we will re-check automatically. %s",
		app.DisplayName, strings.Join(reasons, " "),
		map[bool]string{
			true:  "Your application stays open and a member of our team will also take a look.",
			false: "Your application stays open until this is resolved.",
		}[ownershipProven],
	)

	go func() {
		if err := sendContentCreatorEmail(user.Details.Email, app.DisplayName, subject, htmlContent, plainText); err != nil {
			zap.S().Errorw("failed to send checks-failed email", "error", err, "applicationId", app.ID.Hex())
		}
	}()
}

// AdminOverrideCheckHandler lets an admin pass or fail an individual check by
// hand.
//
// Automation cannot cover everything — TikTok has no public API, a platform can
// be down for days, and a reviewer may know something the checks cannot see. The
// automated result is never the last word; a human can always override any
// single step in either direction, and the override records who did it.
func (cc ContentCreator) AdminOverrideCheckHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	adminIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}

	appObjID, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		config.ErrorStatus("invalid application ID", http.StatusBadRequest, w, err)
		return
	}

	var req struct {
		Key      string `json:"key"`      // channel_resolves | ownership | followers
		Platform string `json:"platform"` // "" matches the aggregate followers check
		Handle   string `json:"handle"`
		Status   string `json:"status"` // passed | failed
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if req.Status != models.CheckPassed && req.Status != models.CheckFailed {
		config.ErrorStatus("status must be passed or failed", http.StatusBadRequest, w, nil)
		return
	}

	app, err := cc.AppDB.FindOne(ctx, bson.M{"_id": appObjID})
	if err != nil || app == nil {
		config.ErrorStatus("application not found", http.StatusNotFound, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	reason := strings.TrimSpace(req.Reason)
	if reason == "" && req.Status == models.CheckPassed {
		reason = "Confirmed by our team."
	}

	matched := false
	checks := make([]models.ApplicationCheck, len(app.Checks))
	copy(checks, app.Checks)
	for i := range checks {
		if checks[i].Key != req.Key {
			continue
		}
		// Platform/handle narrow the match when the same check exists per
		// platform. An empty platform in the request matches any.
		if req.Platform != "" && checks[i].Platform != req.Platform {
			continue
		}
		if req.Handle != "" && checks[i].Handle != req.Handle {
			continue
		}
		checks[i].Status = req.Status
		checks[i].Reason = reason
		checks[i].CheckedAt = &now
		matched = true
	}
	if !matched {
		config.ErrorStatus("no matching check on this application", http.StatusNotFound, w, nil)
		return
	}

	// An override can be the thing that makes an application reviewable, so
	// recompute rather than leaving the old verdict in place.
	passed := true
	for _, c := range checks {
		if c.Status == models.CheckFailed || c.Status == models.CheckPending {
			passed = false
			break
		}
	}

	set := bson.M{"checks": checks, "checksPassed": passed, "updatedAt": now}
	notifyNow := passed && app.AdminNotifiedAt == nil
	if notifyNow {
		set["adminNotifiedAt"] = now
	}
	if err := cc.AppDB.UpdateOne(ctx, bson.M{"_id": appObjID}, bson.M{"$set": set}); err != nil {
		config.ErrorStatus("failed to record override", http.StatusInternalServerError, w, err)
		return
	}

	cc.auditScreening(r, app, "creator_application_check_override", map[string]interface{}{
		"key":          req.Key,
		"platform":     req.Platform,
		"handle":       req.Handle,
		"status":       req.Status,
		"reason":       reason,
		"checksPassed": passed,
	}, fmt.Sprintf("Manually marked %s as %s for %s: %s", req.Key, req.Status, app.DisplayName, reason))

	zap.S().Infow("admin overrode application check",
		"applicationId", appObjID.Hex(), "check", req.Key, "platform", req.Platform,
		"status", req.Status, "adminId", adminIDStr, "reason", reason)

	if notifyNow {
		total := 0
		for _, p := range app.Platforms {
			total += p.FollowerCount
		}
		cc.notifyAdminsApplicationReady(ctx, app, total)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true, "checksPassed": passed, "checks": checks,
	})
}

// AdminRescreenApplicationHandler re-runs the automated checks for one
// application immediately, instead of waiting up to 4 hours for the next sweep.
//
// The common case is an applicant saying "I've added the code now" — a reviewer
// should be able to act on that straight away rather than telling them to wait.
// Optionally narrows to a single check via ?key=, so one stuck step can be
// retried without re-hitting every platform.
func (cc ContentCreator) AdminRescreenApplicationHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	adminIDStr, _ := getUserIDFromRequest(r)

	appObjID, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		config.ErrorStatus("invalid application ID", http.StatusBadRequest, w, err)
		return
	}
	app, err := cc.AppDB.FindOne(ctx, bson.M{"_id": appObjID})
	if err != nil || app == nil {
		config.ErrorStatus("application not found", http.StatusNotFound, w, err)
		return
	}

	onlyKey := strings.TrimSpace(r.URL.Query().Get("key"))
	res := screenApplication(ctx, app, platforms.For)
	now := primitive.NewDateTimeFromTime(time.Now())

	checks := res.Checks
	if onlyKey != "" {
		// Keep every other check as it was and splice in only the re-run one, so
		// retrying a stuck step cannot quietly undo an admin override elsewhere.
		merged := make([]models.ApplicationCheck, 0, len(app.Checks))
		for _, existing := range app.Checks {
			if existing.Key != onlyKey {
				merged = append(merged, existing)
			}
		}
		for _, fresh := range res.Checks {
			if fresh.Key == onlyKey {
				merged = append(merged, fresh)
			}
		}
		checks = merged
	}

	passed := true
	for _, c := range checks {
		if c.Status == models.CheckFailed || c.Status == models.CheckPending {
			passed = false
			break
		}
	}

	set := bson.M{
		"checks":          checks,
		"checksPassed":    passed,
		"checksLastRunAt": now,
		"updatedAt":       now,
	}
	for idx := range res.OwnershipVerified {
		p := fmt.Sprintf("platforms.%d.", idx)
		set[p+"verificationStatus"] = models.PlatformVerified
		set[p+"verificationMethod"] = "api"
		set[p+"verifiedAt"] = now
		set[p+"verificationCode"] = ""
	}
	for idx, count := range res.FollowerCounts {
		p := fmt.Sprintf("platforms.%d.", idx)
		if app.Platforms[idx].ReportedFollowerCount == 0 {
			set[p+"reportedFollowerCount"] = app.Platforms[idx].FollowerCount
		}
		set[p+"followerCount"] = count
	}
	notifyNow := passed && app.AdminNotifiedAt == nil
	if notifyNow {
		set["adminNotifiedAt"] = now
	}

	if err := cc.AppDB.UpdateOne(ctx, bson.M{"_id": appObjID}, bson.M{"$set": set}); err != nil {
		config.ErrorStatus("failed to persist screening", http.StatusInternalServerError, w, err)
		return
	}

	cc.auditScreening(r, app, "creator_application_rescreen", map[string]interface{}{
		"key":          onlyKey,
		"checksPassed": passed,
	}, fmt.Sprintf("Re-ran %s checks for %s", orAll(onlyKey), app.DisplayName))

	zap.S().Infow("admin re-screened creator application",
		"applicationId", appObjID.Hex(), "key", onlyKey, "adminId", adminIDStr, "passed", passed)

	if notifyNow {
		total := 0
		for _, p := range app.Platforms {
			total += p.FollowerCount
		}
		cc.notifyAdminsApplicationReady(ctx, app, total)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true, "checksPassed": passed, "checks": checks,
	})
}

func orAll(key string) string {
	if key == "" {
		return "all"
	}
	return key
}

// auditScreening records manual intervention in screening to the admin audit
// trail, so an override or retry can always be traced to a person and a reason.
func (cc ContentCreator) auditScreening(r *http.Request, app *models.ContentCreatorApplication, action string, after map[string]interface{}, details string) {
	if cc.AuditDB == nil {
		return
	}
	adminIDStr, _ := getUserIDFromRequest(r)
	audit := models.AdminAudit{
		AdminID:   adminIDStr,
		Action:    action,
		Before:    map[string]interface{}{"checksPassed": app.ChecksPassed},
		After:     after,
		Details:   details,
		Timestamp: time.Now(),
		IP:        getClientIP(r),
	}
	if _, err := cc.AuditDB.InsertOne(r.Context(), audit); err != nil {
		zap.S().Errorw("failed to write screening audit log", "action", action, "error", err)
	}
}

// AdminSetReviewChecklistHandler records a reviewer confirming (or un-confirming)
// one of the human judgement calls.
//
// The automated checks answer "is this channel real and theirs". They cannot
// answer "is this content we want associated with us", which is the actual
// decision. Keeping those confirmations on the application means a second
// reviewer sees what the first already looked at, and each toggle lands in the
// audit log so an approval can be traced to what was actually confirmed.
func (cc ContentCreator) AdminSetReviewChecklistHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	adminIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	adminObjID, _ := primitive.ObjectIDFromHex(adminIDStr)

	appObjID, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		config.ErrorStatus("invalid application ID", http.StatusBadRequest, w, err)
		return
	}

	var req struct {
		Key     string `json:"key"`
		Checked bool   `json:"checked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	// Guard the key so arbitrary fields cannot be written into the document.
	if !models.IsReviewChecklistKey(req.Key) {
		config.ErrorStatus("unknown checklist item", http.StatusBadRequest, w, nil)
		return
	}

	app, err := cc.AppDB.FindOne(ctx, bson.M{"_id": appObjID})
	if err != nil || app == nil {
		config.ErrorStatus("application not found", http.StatusNotFound, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	items := make([]models.ReviewChecklistItem, 0, len(models.ReviewChecklistKeys))
	seen := map[string]bool{}
	for _, it := range app.ReviewChecklist {
		if it.Key == req.Key {
			it.Checked = req.Checked
			if req.Checked {
				it.CheckedBy = &adminObjID
				it.CheckedAt = &now
			} else {
				// Un-ticking clears the attribution; leaving the old name on an
				// unchecked item would misrepresent who stands behind it.
				it.CheckedBy = nil
				it.CheckedAt = nil
			}
		}
		items = append(items, it)
		seen[it.Key] = true
	}
	if !seen[req.Key] {
		item := models.ReviewChecklistItem{Key: req.Key, Checked: req.Checked}
		if req.Checked {
			item.CheckedBy = &adminObjID
			item.CheckedAt = &now
		}
		items = append(items, item)
	}

	if err := cc.AppDB.UpdateOne(ctx, bson.M{"_id": appObjID},
		bson.M{"$set": bson.M{"reviewChecklist": items, "updatedAt": now}}); err != nil {
		config.ErrorStatus("failed to record checklist", http.StatusInternalServerError, w, err)
		return
	}

	verb := "confirmed"
	if !req.Checked {
		verb = "un-confirmed"
	}
	cc.auditScreening(r, app, "creator_application_review_checklist", map[string]interface{}{
		"key":      req.Key,
		"checked":  req.Checked,
		"complete": models.ReviewChecklistComplete(items),
	}, fmt.Sprintf("Reviewer %s \"%s\" for %s", verb, req.Key, app.DisplayName))

	zap.S().Infow("reviewer checklist updated",
		"applicationId", appObjID.Hex(), "key", req.Key, "checked", req.Checked, "adminId", adminIDStr)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"checklist": items,
		"complete":  models.ReviewChecklistComplete(items),
	})
}

// indexOfPlatform finds a platform's position for follower lookup.
func indexOfPlatform(app *models.ContentCreatorApplication, target models.ContentCreatorPlatform) int {
	for i, p := range app.Platforms {
		if p.Type == target.Type && p.Handle == target.Handle {
			return i
		}
	}
	return -1
}

// notifyAdminsApplicationReady sends the review email, now that the application
// has cleared the automated checks.
func (cc ContentCreator) notifyAdminsApplicationReady(ctx context.Context, app *models.ContentCreatorApplication, totalFollowers int) {
	var applicant struct {
		Details struct {
			Username string `bson:"username"`
		} `bson:"user"`
	}
	username := "Unknown"
	if err := cc.UDB.FindOne(ctx, bson.M{"_id": app.UserID}).Decode(&applicant); err == nil {
		username = applicant.Details.Username
	}
	zap.S().Infow("creator application cleared automated checks, notifying admins",
		"applicationId", app.ID.Hex(), "displayName", app.DisplayName)
	cc.sendAdminNewApplicationEmail(ctx, username, app.DisplayName, app.PrimaryPlatform, totalFollowers)
}

// maxOwnershipAttempts bounds how long we keep re-checking for a code that
// never appears. Generous on purpose: people apply, then get to editing their
// channel hours later.
const maxOwnershipAttempts = 12

// screenResult is the outcome of one screening pass over one application.
type screenResult struct {
	Checks []models.ApplicationCheck
	// Passed means every check is passed or manual, so a human should look.
	Passed bool
	// Blocked means at least one check failed outright and the applicant has to
	// act. Distinct from "still pending", which just means try again later.
	Blocked bool
	// FollowerCounts holds the real counts read from each platform, indexed by
	// platform position, so the caller can write them back.
	FollowerCounts map[int]int
	// OwnershipVerified marks platform indexes whose code was found this pass.
	OwnershipVerified map[int]bool
	// OwnershipProven is true once ANY channel has been proved to belong to the
	// applicant. It separates "a real person who fell short of a requirement"
	// from "a channel nobody has shown they own", which decides whether a failed
	// application is worth an admin's attention.
	OwnershipProven bool
	// FailureReasons are the applicant-facing reasons the application is
	// blocked, for the email that tells them.
	FailureReasons []string
	// FollowerShortfall means we successfully read a real follower count and it
	// is under the minimum. That is a program requirement, not a judgement call,
	// so it is auto-rejected rather than queued for a human. Never set when the
	// count could not be read — an unreadable channel is our problem.
	FollowerShortfall bool
	FollowerReason    string
}

// channelFetcher lets tests substitute platform responses. Production passes
// platforms.For.
type channelFetcher func(platformType string) (platforms.Fetcher, error)

// screenApplication runs every check for an application and reports what the
// applicant should be told. It performs no writes — the caller persists.
func screenApplication(ctx context.Context, app *models.ContentCreatorApplication, fetcherFor channelFetcher) screenResult {
	now := primitive.NewDateTimeFromTime(time.Now())
	res := screenResult{
		FollowerCounts:    map[int]int{},
		OwnershipVerified: map[int]bool{},
	}

	// The follower requirement is met by the BIGGEST channel, matching how
	// existing creators are assessed, so a small second channel is not a reason
	// to reject someone.
	bestFollowers := 0
	anyFollowerData := false

	for i, p := range app.Platforms {
		label := p.Handle
		if label == "" {
			label = p.URL
		}
		add := func(key, status, reason string) {
			res.Checks = append(res.Checks, models.ApplicationCheck{
				Key: key, Platform: p.Type, Handle: label,
				Status: status, Reason: reason, CheckedAt: &now,
			})
		}

		// Already-verified platforms are not re-fetched. Their stored follower
		// count is trusted because verification wrote it.
		if p.IsVerified() {
			res.OwnershipProven = true
			add(models.CheckChannelResolves, models.CheckPassed, "")
			add(models.CheckOwnership, models.CheckPassed, "")
			if p.FollowerCount > bestFollowers {
				bestFollowers = p.FollowerCount
			}
			anyFollowerData = true
			continue
		}

		fetcher, err := fetcherFor(p.Type)
		if err == platforms.ErrManualOnly {
			// No public API. Not the applicant's problem and not a failure.
			add(models.CheckChannelResolves, models.CheckManual, "This platform is reviewed by our team.")
			add(models.CheckOwnership, models.CheckManual, "Leave the code in your bio; our team confirms it.")
			continue
		}

		info, ferr := fetcher.Fetch(ctx, p.Handle)
		switch {
		case ferr == platforms.ErrChannelNotFound:
			add(models.CheckChannelResolves, models.CheckFailed,
				fmt.Sprintf("We could not find a %s channel at \"%s\". Check the link is correct.", p.Type, label))
			res.Blocked = true
			continue
		case ferr != nil:
			// Our side is unwell (no key, quota, network). Never blame the
			// applicant for it — stay pending and try again next run.
			add(models.CheckChannelResolves, models.CheckPending, "Checking your channel, this can take a little while.")
			continue
		}

		add(models.CheckChannelResolves, models.CheckPassed, "")
		res.FollowerCounts[i] = info.FollowerCount
		if info.FollowerCount > bestFollowers {
			bestFollowers = info.FollowerCount
		}
		anyFollowerData = true

		if platforms.ContainsCode(info.Description, p.VerificationCode) && p.VerificationCode != "" {
			add(models.CheckOwnership, models.CheckPassed, "")
			res.OwnershipVerified[i] = true
			res.OwnershipProven = true
			continue
		}

		if app.CheckAttempts >= maxOwnershipAttempts {
			add(models.CheckOwnership, models.CheckFailed,
				"We could not find your verification code in this channel's description. Add it and request a new check.")
			res.Blocked = true
			continue
		}
		add(models.CheckOwnership, models.CheckPending,
			"Waiting for your verification code to appear in this channel's description.")
	}

	// Follower check is judged against the minimum, not against what the
	// applicant claimed. Someone who guessed their own count wrong should still
	// get in if the real number clears the bar.
	switch {
	case !anyFollowerData:
		res.Checks = append(res.Checks, models.ApplicationCheck{
			Key: models.CheckFollowers, Status: models.CheckPending,
			Reason: "We will check your follower count once your channel is reachable.", CheckedAt: &now,
		})
	case bestFollowers >= models.MinFollowers:
		res.Checks = append(res.Checks, models.ApplicationCheck{
			Key: models.CheckFollowers, Status: models.CheckPassed,
			Reason:    fmt.Sprintf("%s followers on your largest channel.", formatCount(bestFollowers)),
			CheckedAt: &now,
		})
	default:
		reason := fmt.Sprintf("Your largest channel has %s followers. The program needs at least %s.",
			formatCount(bestFollowers), formatCount(models.MinFollowers))
		res.Checks = append(res.Checks, models.ApplicationCheck{
			Key: models.CheckFollowers, Status: models.CheckFailed,
			Reason: reason, CheckedAt: &now,
		})
		res.Blocked = true
		res.FollowerShortfall = true
		// The rejection reason is read on its own in an email, away from the
		// checks list, so it carries the way back in. The check reason above
		// stays terse — it sits in a table.
		res.FollowerReason = reason + fmt.Sprintf(
			" You are very welcome to apply again once you are over %s.", formatCount(models.MinFollowers))
	}

	// Passed means a human should now look: nothing failed, nothing outstanding.
	res.Passed = !res.Blocked
	for _, c := range res.Checks {
		if c.Status == models.CheckPending {
			res.Passed = false
			break
		}
	}
	for _, c := range res.Checks {
		if c.Status == models.CheckFailed && c.Reason != "" {
			res.FailureReasons = append(res.FailureReasons, c.Reason)
		}
	}
	return res
}

// AdminWithdrawApprovalHandler lets an admin take back the first approval they
// gave.
//
// Approvals were one-way: give one by mistake, or change your mind after seeing
// something on the channel, and the only escape was for a second admin to
// approve anyway or for the whole application to be rejected. Neither is
// honest. Withdrawing returns the application to awaiting-review.
//
// Only the admin who gave the approval can take it back, or an owner. An admin
// quietly removing somebody else's approval would defeat the point of recording
// who gave it.
func (cc ContentCreator) AdminWithdrawApprovalHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	adminIDStr, ok := getUserIDFromRequest(r)
	if !ok {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, nil)
		return
	}
	adminObjID, err := primitive.ObjectIDFromHex(adminIDStr)
	if err != nil {
		config.ErrorStatus("invalid admin id", http.StatusBadRequest, w, err)
		return
	}

	appObjID, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		config.ErrorStatus("invalid application ID", http.StatusBadRequest, w, err)
		return
	}

	app, err := cc.AppDB.FindOne(ctx, bson.M{"_id": appObjID})
	if err != nil || app == nil {
		config.ErrorStatus("application not found", http.StatusNotFound, w, err)
		return
	}

	// Nothing to withdraw, and a decided application is not reopened this way.
	if app.FirstApprovalBy == nil {
		config.InfoStatus("there is no approval to withdraw", http.StatusConflict, w, nil)
		return
	}
	if app.Status != "submitted" && app.Status != "under_review" {
		config.InfoStatus("this application has already been decided", http.StatusConflict, w, nil)
		return
	}

	isOwner := false
	if admin, aErr := cc.AdminDB.FindOne(ctx, bson.M{"_id": adminObjID}); aErr == nil && admin != nil {
		isOwner = admin.Role == "owner"
		for _, role := range admin.Roles {
			if role == "owner" {
				isOwner = true
				break
			}
		}
	}
	if app.FirstApprovalBy.Hex() != adminObjID.Hex() && !isOwner {
		config.ErrorStatus("only the admin who approved this, or an owner, can withdraw that approval",
			http.StatusForbidden, w, nil)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	if err := cc.AppDB.UpdateOne(ctx, bson.M{"_id": appObjID}, bson.M{
		"$set":   bson.M{"status": "submitted", "updatedAt": now},
		"$unset": bson.M{"firstApprovalBy": "", "firstApprovalAt": ""},
	}); err != nil {
		config.ErrorStatus("failed to withdraw approval", http.StatusInternalServerError, w, err)
		return
	}

	onBehalf := ""
	if app.FirstApprovalBy.Hex() != adminObjID.Hex() {
		onBehalf = fmt.Sprintf(" (given by %s)", app.FirstApprovalBy.Hex())
	}
	cc.auditScreening(r, app, "creator_application_approval_withdrawn", map[string]interface{}{
		"withdrawnBy":      adminIDStr,
		"originalApprover": app.FirstApprovalBy.Hex(),
		"ownerAction":      isOwner && app.FirstApprovalBy.Hex() != adminObjID.Hex(),
	}, fmt.Sprintf("Withdrew the first approval on %s%s", app.DisplayName, onBehalf))

	zap.S().Infow("creator application approval withdrawn",
		"applicationId", appObjID.Hex(), "withdrawnBy", adminIDStr,
		"originalApprover", app.FirstApprovalBy.Hex())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
