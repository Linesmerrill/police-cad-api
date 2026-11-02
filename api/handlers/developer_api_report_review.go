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
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/api/handlers/formdefaults"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// The other half of report drafts: a person reading one and deciding.
//
// These routes are on the first-party surface, authenticated as the website
// authenticates anyone else, and gated on the community's own "manage records"
// permission. That placement is the point. A developer key cannot reach any of
// them — not because a scope is withheld, but because they are not registered
// under the developer prefix at all. Filing a report is an act with
// consequences inside a community, so the only way to do it is to be a person
// the community trusted with records.
//
// What filing actually produces depends on the kind:
//
//	arrest   -> an ArrestReport, in the same collection the CAD writes to, so
//	            it appears in the arrest list, the court flow and the totals
//	            exactly like one typed by hand;
//	citation -> a FormSubmission against the community's citation form, or the
//	incident    built-in Incident Report when the draft named no template.
//
// Either way the draft keeps a pointer to what was filed, and the integration
// can read that pointer back. So the loop closes: a game server says what it
// saw, a person decides, and the server finds out what they decided.

// maxReviewNotesLen bounds what a reviewer types.
const maxReviewNotesLen = 2000

// reportDraftReviewPermissions is the community permission that governs this.
//
// "manage records" rather than a new permission of its own. Filing a draft
// creates a record and rejecting one discards a proposed record, which is
// exactly what that permission already covers — and a new permission would
// have to be added to the API's list, the website's roles modal, the mobile
// roles editor and the bot before anybody could grant it, for no gain.
var reportDraftReviewPermissions = []string{"manage records"}

// ListCommunityReportDraftsHandler returns the drafts awaiting a decision.
//
// GET /api/v1/developer/community/{communityId}/report-drafts
//
// Paginated, newest first, filterable by status and kind. Defaults to the
// drafts nobody has acted on, because that is the queue.
func (d DeveloperAPI) ListCommunityReportDraftsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cc, ok := d.resolveCommunityForDraftReview(w, r)
	if !ok {
		return
	}
	if d.ReportDraftDB == nil {
		config.ErrorStatus("report drafts unavailable", http.StatusInternalServerError, w,
			fmt.Errorf("no draft store"))
		return
	}

	filter := bson.M{"communityId": devEq(cc.ID.Hex())}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = models.ReportDraftStatusDraft
	}
	if !strings.EqualFold(status, "all") {
		safe, err := devSafeReportStatus(status)
		if err != nil {
			config.InfoStatus("invalid status", http.StatusBadRequest, w, err)
			return
		}
		filter["status"] = devEq(safe)
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("kind")); raw != "" {
		kind, kErr := devSafeReportKind(raw)
		if kErr != nil {
			config.InfoStatus("invalid kind", http.StatusBadRequest, w, kErr)
			return
		}
		filter["kind"] = devEq(kind)
	}

	page, limit := devPageParams(r, maxReportDraftPage)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	total, _ := d.ReportDraftDB.CountDocuments(ctx, filter)
	drafts, err := d.ReportDraftDB.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).
		SetSkip(int64((page-1)*limit)).
		SetLimit(int64(limit)))
	if err != nil {
		config.ErrorStatus("failed to list report drafts", http.StatusInternalServerError, w, err)
		return
	}

	views := make([]map[string]interface{}, 0, len(drafts))
	for _, draft := range drafts {
		views = append(views, d.reviewView(draft))
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       views,
		"totalCount": total,
		"page":       page,
		"limit":      limit,
	})
}

// GetCommunityReportDraftHandler returns one draft in full.
//
// GET /api/v1/developer/community/{communityId}/report-drafts/{draftId}
func (d DeveloperAPI) GetCommunityReportDraftHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cc, ok := d.resolveCommunityForDraftReview(w, r)
	if !ok {
		return
	}
	draft, ok := d.loadDraftForReview(w, r, cc)
	if !ok {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"draft": d.reviewView(*draft)})
}

// FileCommunityReportDraftHandler turns a draft into a record.
//
// POST /api/v1/developer/community/{communityId}/report-drafts/{draftId}/file
//
// The one endpoint in this feature that creates a legal record, and the reason
// it is here rather than on the developer surface. It is idempotent on the
// draft's own state: a draft already filed returns what was filed rather than
// filing a second copy, because a double-click must not produce two arrest
// reports.
func (d DeveloperAPI) FileCommunityReportDraftHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cc, ok := d.resolveCommunityForDraftReview(w, r)
	if !ok {
		return
	}
	draft, ok := d.loadDraftForReview(w, r, cc)
	if !ok {
		return
	}

	var body struct {
		Notes string `json:"notes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	actorID := resolveActorFromRequest(r)
	actorName := resolveActorName(d.UDB, actorID)

	if draft.Status == models.ReportDraftStatusSubmitted && draft.FiledRecordID != "" {
		// Already filed. Hand back what exists rather than making a second
		// record: a reviewer double-clicking must not produce two arrest
		// reports, and the integration polling for the outcome wants the same
		// answer either way.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"draft":  d.reviewView(*draft),
			"filed":  false,
			"record": map[string]interface{}{"id": draft.FiledRecordID, "kind": draft.FiledRecordKind},
		})
		return
	}
	if !draft.IsOpenForWrites() {
		config.InfoStatus("draft is not open", http.StatusConflict, w,
			fmt.Errorf("draft status is %s", draft.Status))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	recordID, recordKind, err := d.fileDraft(ctx, cc, *draft, actorID, actorName)
	if err != nil {
		config.ErrorStatus("failed to file report draft", http.StatusInternalServerError, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	updated, err := d.ReportDraftDB.UpdateOne(ctx, bson.M{"_id": devEq(draft.ID)}, bson.M{"$set": bson.M{
		"status":          models.ReportDraftStatusSubmitted,
		"reviewedBy":      actorID,
		"reviewedByName":  actorName,
		"reviewedAt":      now,
		"reviewNotes":     truncateReportText(body.Notes, maxReviewNotesLen),
		"filedRecordId":   recordID,
		"filedRecordKind": recordKind,
		"updatedAt":       now,
	}})
	if err != nil || updated == nil {
		// The record exists and the draft does not know it. Say so plainly
		// rather than reporting failure, because a reviewer who is told this
		// failed will file it again.
		config.ErrorStatus("the record was created but the draft could not be marked filed",
			http.StatusInternalServerError, w, fmt.Errorf("recordId=%s: %v", recordID, err))
		return
	}

	logAudit(d.ALDB, cc.ID, "report_draft.filed", "community",
		actorID, actorName, draft.ID.Hex(), draft.Kind,
		map[string]interface{}{
			"recordId":   recordID,
			"recordKind": recordKind,
			"externalId": draft.ExternalID,
			"source":     draft.SourceName,
		})

	// The integration has been waiting for exactly this. An event means it
	// does not have to poll the draft to find out, and the record id is in the
	// payload so it can deep-link the player straight to what was filed.
	EmitDeveloperEvent(d.EventDB, devEvent{
		Type:               models.EventReportDraftFiled,
		CommunityID:        cc.ID.Hex(),
		ResourceID:         draft.ID.Hex(),
		ResourceExternalID: draft.ExternalID,
		ResourceSource:     draft.SourceName,
		Actor:              models.EventActorWebsite,
		Data: map[string]string{
			"kind":            draft.Kind,
			"filedRecordId":   recordID,
			"filedRecordKind": recordKind,
			"reviewedBy":      actorName,
		},
	})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"draft":  d.reviewView(*updated),
		"filed":  true,
		"record": map[string]interface{}{"id": recordID, "kind": recordKind},
	})
}

// RejectCommunityReportDraftHandler declines a draft.
//
// POST /api/v1/developer/community/{communityId}/report-drafts/{draftId}/reject
//
// Notes are required. A rejection with no reason is one the officer cannot act
// on, and the integration shows this text to the player who filed it.
func (d DeveloperAPI) RejectCommunityReportDraftHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cc, ok := d.resolveCommunityForDraftReview(w, r)
	if !ok {
		return
	}
	draft, ok := d.loadDraftForReview(w, r, cc)
	if !ok {
		return
	}

	var body struct {
		Notes string `json:"notes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	notes := truncateReportText(body.Notes, maxReviewNotesLen)
	if notes == "" {
		config.InfoStatus("notes are required", http.StatusBadRequest, w,
			fmt.Errorf("a rejection needs a reason the officer can act on"))
		return
	}

	if !draft.IsOpenForWrites() {
		config.InfoStatus("draft is not open", http.StatusConflict, w,
			fmt.Errorf("draft status is %s", draft.Status))
		return
	}

	actorID := resolveActorFromRequest(r)
	actorName := resolveActorName(d.UDB, actorID)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	now := primitive.NewDateTimeFromTime(time.Now())
	updated, err := d.ReportDraftDB.UpdateOne(ctx, bson.M{"_id": devEq(draft.ID)}, bson.M{"$set": bson.M{
		"status":         models.ReportDraftStatusRejected,
		"reviewedBy":     actorID,
		"reviewedByName": actorName,
		"reviewedAt":     now,
		"reviewNotes":    notes,
		"updatedAt":      now,
	}})
	if err != nil || updated == nil {
		config.ErrorStatus("failed to reject report draft", http.StatusInternalServerError, w, err)
		return
	}

	logAudit(d.ALDB, cc.ID, "report_draft.rejected", "community",
		actorID, actorName, draft.ID.Hex(), draft.Kind,
		map[string]interface{}{"externalId": draft.ExternalID, "source": draft.SourceName})

	// The reason travels with the event, because it is the thing the officer in
	// the game needs to be told and the integration is what tells them.
	EmitDeveloperEvent(d.EventDB, devEvent{
		Type:               models.EventReportDraftRejected,
		CommunityID:        cc.ID.Hex(),
		ResourceID:         draft.ID.Hex(),
		ResourceExternalID: draft.ExternalID,
		ResourceSource:     draft.SourceName,
		Actor:              models.EventActorWebsite,
		Data: map[string]string{
			"kind":       draft.Kind,
			"reviewedBy": actorName,
			"notes":      notes,
		},
	})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"draft": d.reviewView(*updated)})
}

// ---- filing ----

// fileDraft creates the record a draft was proposing.
//
// Returns the new record's id and what kind it is, which the draft then points
// at. The ordering matters: the record is created first and the draft marked
// afterwards, so a failure between the two leaves a real record and a draft
// that still looks open. That is the safe direction — a reviewer sees the draft
// again and the handler's already-filed check hands them the existing record
// rather than making a second one. The reverse ordering would mark a draft
// filed with nothing behind it.
func (d DeveloperAPI) fileDraft(ctx context.Context, cc *models.Community, draft models.DeveloperReportDraft, actorID, actorName string) (string, string, error) {
	switch draft.Kind {
	case models.ReportDraftKindArrest:
		return d.fileArrestReport(ctx, cc, draft, actorID)
	default:
		return d.fileFormSubmission(ctx, cc, draft, actorID, actorName)
	}
}

// fileArrestReport writes an ArrestReport into the collection the CAD reads.
//
// Into the real collection, not a parallel one, so a report a mod proposed
// behaves identically to one an officer typed: it appears in the arrest list,
// the court flow and the community's totals.
//
// The officer on the report is the draft's reporting officer, not the reviewer.
// The reviewer's name is on the draft. Putting the reviewer on the report would
// misattribute the arrest to whoever happened to approve it.
func (d DeveloperAPI) fileArrestReport(ctx context.Context, cc *models.Community, draft models.DeveloperReportDraft, actorID string) (string, string, error) {
	if d.ArrestReportDB == nil {
		return "", "", fmt.Errorf("arrest reports unavailable")
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	report := models.ArrestReport{ID: primitive.NewObjectID()}
	report.Details = models.ArrestReportDetails{
		ArrestLocation:   draft.Facts.Location,
		IncidentLocation: draft.Facts.Location,
		Arrestee: models.Arrestee{
			ID:   draft.Subject.CivilianID,
			Name: draft.Subject.DisplayName,
		},
		Officer:           models.Officer{Name: draft.ReportingOfficerName},
		OfficerID:         draft.ReportingOfficerID,
		ActiveCommunityID: cc.ID.Hex(),
		DepartmentID:      draft.DepartmentID,
		ChargesList:       draft.Charges,
		Charges:           strings.Join(chargeNames(draft.Charges), ", "),
		// The narrative goes in the narrative field, labelled. The structured
		// summary is prepended rather than merged, so a reviewer reading the
		// filed report still sees what the server observed apart from what was
		// written about it.
		Narrative: composeFiledNarrative(draft),
		Witnesses: strings.Join(draft.Facts.Witnesses, ", "),
		ForceUsed: draft.Facts.ForceUsed,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if _, err := d.ArrestReportDB.InsertOne(ctx, report); err != nil {
		return "", "", err
	}
	_ = actorID
	return report.ID.Hex(), "arrestReport", nil
}

// fileFormSubmission writes an incident or citation draft as a form submission.
//
// Against the template the draft named, or the built-in Incident Report when it
// named none — which is the template every community has and the one the CAD
// itself falls back to.
//
// The report number is allocated through the same reportNumbering the website's
// own form handler uses, and must be: formSubmissions carries a unique index on
// (communityID, reportNumber), so filing without a number succeeded once per
// community and then failed on every report after it.
func (d DeveloperAPI) fileFormSubmission(ctx context.Context, cc *models.Community, draft models.DeveloperReportDraft, actorID, actorName string) (string, string, error) {
	if d.FormSubmissionDB == nil {
		return "", "", fmt.Errorf("form submissions unavailable")
	}

	slug := draft.TemplateSlug
	if slug == "" {
		slug = formdefaults.IncidentReportSlug
	}

	data := map[string]interface{}{}
	for key, value := range draft.Facts.Fields {
		data[key] = value
	}
	// The draft's own structure, under stable keys, so nothing is lost when the
	// template has no field for it. A reviewer sees the facts and the narrative
	// whether or not the community's form happens to have matching questions.
	data["lpcReportedLocation"] = draft.Facts.Location
	data["lpcReportedOccurredAt"] = draft.Facts.OccurredAt
	data["lpcReportedSummary"] = draft.Facts.Summary
	data["lpcNarrative"] = composeFiledNarrative(draft)
	if len(draft.Charges) > 0 {
		data["lpcCharges"] = strings.Join(chargeNames(draft.Charges), ", ")
	}

	numbering := reportNumbering{
		Subs:      d.FormSubmissionDB,
		Counters:  d.FormCounterDB,
		Templates: d.FormTemplateDB,
	}
	reportNumber, err := numbering.NextNumber(ctx, cc.ID.Hex(), slug)
	if err != nil {
		return "", "", err
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	submission := models.FormSubmission{ID: primitive.NewObjectID()}
	submission.Details = models.FormSubmissionDetails{
		CommunityID:      cc.ID.Hex(),
		DepartmentID:     draft.DepartmentID,
		FormTemplateSlug: slug,
		ReportNumber:     reportNumber,
		Data:             data,
		Status:           "submitted",
		SignedBy: models.FormSubmissionSignature{
			UserID:   draft.ReportingOfficerID,
			Username: draft.ReportingOfficerName,
			SignedAt: now,
		},
		History: []models.FormSubmissionHistoryEntry{{
			Action: "submitted", UserID: actorID, Username: actorName, At: now,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if draft.CallID != "" {
		submission.Details.Links = []models.FormSubmissionLink{{Type: "call", ID: draft.CallID}}
	}

	if err := numbering.Insert(ctx, &submission, true); err != nil {
		return "", "", err
	}
	return submission.ID.Hex(), "formSubmission", nil
}

// composeFiledNarrative renders the prose half for a filed record, keeping the
// generated label attached to it.
//
// The label travels with the text into the record. A reviewer decided to file
// something a model wrote, and whoever reads the report in three months should
// be able to see that too — otherwise the provenance is lost at exactly the
// point it starts to matter.
func composeFiledNarrative(draft models.DeveloperReportDraft) string {
	parts := []string{}
	if draft.Facts.Summary != "" {
		parts = append(parts, draft.Facts.Summary)
	}
	if draft.Narrative.Text != "" {
		if draft.Narrative.Generated {
			label := "[Narrative generated by " + draft.SourceName
			if draft.Narrative.GeneratedBy != "" {
				label += " / " + draft.Narrative.GeneratedBy
			}
			label += ", reviewed before filing. Not evidence.]"
			parts = append(parts, label)
		}
		parts = append(parts, draft.Narrative.Text)
	}
	return strings.Join(parts, "\n\n")
}

// ---- shared ----

// resolveCommunityForDraftReview loads the community and checks the caller may
// review its drafts.
func (d DeveloperAPI) resolveCommunityForDraftReview(w http.ResponseWriter, r *http.Request) (*models.Community, bool) {
	if !requireFirstPartyCaller(w, r, "report draft review") {
		return nil, false
	}
	_, oid, err := devSafeObjectID(mux.Vars(r)["communityId"])
	if err != nil {
		config.InfoStatus("invalid community id", http.StatusBadRequest, w, err)
		return nil, false
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := d.CDB.FindOne(ctx, bson.M{"_id": devEq(oid)})
	if err != nil || community == nil {
		config.InfoStatus("community not found", http.StatusNotFound, w, fmt.Errorf("not found"))
		return nil, false
	}
	if !authorizeCommunityAction(w, r, community, reportDraftReviewPermissions...) {
		return nil, false
	}
	return community, true
}

// loadDraftForReview reads one draft, with the community in the filter.
//
// In the filter rather than checked afterwards, so a draft id from another
// community is a 404 and its existence is never confirmed.
func (d DeveloperAPI) loadDraftForReview(w http.ResponseWriter, r *http.Request, cc *models.Community) (*models.DeveloperReportDraft, bool) {
	if d.ReportDraftDB == nil {
		config.ErrorStatus("report drafts unavailable", http.StatusInternalServerError, w,
			fmt.Errorf("no draft store"))
		return nil, false
	}
	_, oid, err := devSafeObjectID(mux.Vars(r)["draftId"])
	if err != nil {
		config.InfoStatus("invalid draft id", http.StatusBadRequest, w, err)
		return nil, false
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	drafts, err := d.ReportDraftDB.Find(ctx, bson.M{
		"_id":         devEq(oid),
		"communityId": devEq(cc.ID.Hex()),
	}, options.Find().SetLimit(1))
	if err != nil || len(drafts) == 0 {
		config.InfoStatus("report draft not found", http.StatusNotFound, w, fmt.Errorf("not found"))
		return nil, false
	}
	return &drafts[0], true
}

// reviewView is what the website renders.
//
// The developer view plus who wrote it, because a reviewer's first question
// about a draft that appeared in their queue is which integration put it there.
func (d DeveloperAPI) reviewView(draft models.DeveloperReportDraft) map[string]interface{} {
	out := map[string]interface{}{"draft": devReportDraftViewOf(draft)}
	if draft.Source != nil {
		out["writtenByKeyId"] = draft.Source.KeyID
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// The key's name, not just its id. "SixPD dispatch bridge" is something
		// a community owner can act on; a 24-character hex string is not.
		if key, err := d.KeyDB.FindOne(ctx, bson.M{"_id": objectIDOrZero(draft.Source.KeyID)}); err == nil && key != nil {
			out["writtenByKeyName"] = key.Name
			out["writtenByDeveloper"] = resolveActorName(d.UDB, key.UserID)
		}
	}
	return out
}

// requireFirstPartyCaller insists the request came through the website's own
// server rather than straight from a browser.
//
// The same posture the moderation-reports console takes, and for the same
// reason: these endpoints file a legal record or delete a community's records,
// so the permission check that governs them has to rest on something stronger
// than what a browser can present. The gateway secret only the website backend
// holds gives it that — these routes are called from a server route, never
// from page JavaScript.
//
// Fail-open when the secret is unconfigured, matching the rest of the gateway
// so that deploying this cannot brick the console, but logged loudly: that
// case means the protection is off.
func requireFirstPartyCaller(w http.ResponseWriter, r *http.Request, what string) bool {
	if !GatewaySecretConfigured() {
		zap.S().Warnw("developer api: served without gateway enforcement; API_GATEWAY_KEY is not set",
			"endpoint", what)
		return true
	}
	if !HasValidGatewaySecret(r) {
		config.ErrorStatus("forbidden", http.StatusForbidden, w,
			fmt.Errorf("%s requires the first-party gateway secret", what))
		return false
	}
	return true
}
