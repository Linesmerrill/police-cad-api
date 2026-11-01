package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/helpers"
	"github.com/linesmerrill/police-cad-api/models"
)

// Report drafts: what a game server saw, written down for a person to decide
// about.
//
// The one thing this file does not have, and will not get, is a way to file a
// report. An arrest report in this product produces jail time, a fine and a
// court case. A game server can observe a scuffle; it cannot know whether that
// was an arrest, and an integration that could file one would be making a call
// that belongs to a person in the community. So the API's vocabulary stops at
// "here is what happened" and the verb that files it lives on the website,
// behind the community's own permission.
//
// That boundary is structural rather than remembered. Drafts are their own
// collection (developer_report_drafts); nothing that reads a filed report
// reads it; and the only status a key can write is "draft". A key cannot set
// "submitted" because the handler refuses any status at all — it is not a
// field on the request.
//
// Everything else here follows the dispatch surface's shape, because an
// integration writing a report has the same problem as one writing a call: it
// retries, and a retry must not put a second thing in a person's queue. So a
// draft is addressed by (community, source, externalId) and every write on it
// is idempotent.

// Bounds on what a draft may carry. A reviewer has to read this, so the limits
// are what a person can actually get through rather than what a database could
// hold.
const (
	maxReportNarrativeLen = 8000
	maxReportSummaryLen   = 2000
	maxReportFactFields   = 60
	maxReportFactValueLen = 1000
	maxReportCharges      = 40
	maxReportListEntries  = 25
	maxReportDraftPage    = 100
)

type devReportSubjectRequest struct {
	// Player names the subject as provider:identifier. Optional: an incident
	// report about an unidentified person is a real thing, and refusing it
	// would push integrations into inventing a placeholder player.
	Player     string `json:"player"`
	CivilianID string `json:"civilianId"`
	// DisplayName is what the reviewer sees when there is no linked player.
	DisplayName string `json:"displayName"`
}

type devReportNarrativeRequest struct {
	Text string `json:"text"`
	// Generated is a pointer so "did not say" is distinguishable from "said
	// false". Omitted is treated as generated, because the callers of this
	// surface are automated by definition and the safer mislabel is the one
	// that makes a reviewer look harder.
	Generated   *bool  `json:"generated"`
	GeneratedBy string `json:"generatedBy"`
}

type devReportFactsRequest struct {
	OccurredAt string            `json:"occurredAt"`
	Location   string            `json:"location"`
	Summary    string            `json:"summary"`
	Vehicles   []string          `json:"vehicles"`
	Witnesses  []string          `json:"witnesses"`
	Officers   []string          `json:"officers"`
	ForceUsed  bool              `json:"forceUsed"`
	Fields     map[string]string `json:"fields"`
}

type devReportDraftRequest struct {
	ExternalID string `json:"externalId"`
	Source     string `json:"source"`
	Kind       string `json:"kind"`

	// CallExternalID links the draft to a dispatch call by the integration's
	// own id, which is the id it still has after a restart. CallID accepts
	// ours for a caller that kept it.
	CallExternalID string `json:"callExternalId"`
	CallID         string `json:"callId"`

	// ReportingOfficer is the player who observed this. Required: a draft
	// nobody can be named for is not reviewable, and it is this player's
	// community access that is checked — never the key's.
	ReportingOfficer string `json:"reportingOfficer"`
	DepartmentID     string `json:"departmentId"`
	TemplateSlug     string `json:"templateSlug"`

	Subject   devReportSubjectRequest   `json:"subject"`
	Facts     devReportFactsRequest     `json:"facts"`
	Narrative devReportNarrativeRequest `json:"narrative"`
	// Charges name violations from the community's own penal code. The
	// integration sends names only; the amounts come from the community.
	Charges []string `json:"charges"`
}

// devReportDraftView is how a draft reads back.
//
// It states plainly that nothing has been filed, and carries a link to the
// review screen, so an integration's logs say what actually happened rather
// than implying a report exists.
type devReportDraftView struct {
	ID          string `json:"id"`
	ExternalID  string `json:"externalId"`
	Source      string `json:"source"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	CommunityID string `json:"communityId"`

	CallID         string `json:"callId,omitempty"`
	CallExternalID string `json:"callExternalId,omitempty"`
	TemplateSlug   string `json:"templateSlug,omitempty"`
	DepartmentID   string `json:"departmentId,omitempty"`

	Subject   models.DeveloperReportSubject   `json:"subject"`
	Facts     models.DeveloperReportFacts     `json:"facts"`
	Narrative models.DeveloperReportNarrative `json:"narrative"`
	Charges   []models.ArrestCharge           `json:"charges"`

	ReportingOfficerID   string `json:"reportingOfficerId"`
	ReportingOfficerName string `json:"reportingOfficerName,omitempty"`

	ReviewedByName  string `json:"reviewedByName,omitempty"`
	ReviewedAt      string `json:"reviewedAt,omitempty"`
	ReviewNotes     string `json:"reviewNotes,omitempty"`
	FiledRecordID   string `json:"filedRecordId,omitempty"`
	FiledRecordKind string `json:"filedRecordKind,omitempty"`

	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`

	// Filed says whether a person has turned this into a record. Spelled out
	// rather than left to be inferred from Status, because the one thing an
	// integration must not conclude from a 201 here is that a report exists.
	Filed bool `json:"filed"`
	// ReviewLink opens the draft on the website for whoever may act on it.
	ReviewLink string `json:"reviewLink,omitempty"`
}

// DevUpsertReportDraftHandler writes a draft, or updates the one that already
// carries this external id.
//
// POST /api/v1/dev/communities/{communityId}/reports/drafts — reports:write
//
// Idempotent on (community, source, externalId). A 201 means a draft was
// created and nothing was filed; a 200 means the existing draft was updated.
// A draft a person has already filed or rejected is answered 409 and left
// exactly as they left it.
func (d DeveloperAPI) DevUpsertReportDraftHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}
	if d.ReportDraftDB == nil {
		devError(w, http.StatusInternalServerError, "server_error", "Report drafts are not available.", nil)
		return
	}

	var req devReportDraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}

	externalID, source, err := normalizeExternalRef(req.ExternalID, req.Source, dc.Key.Name)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_external_id", err.Error(), nil)
		return
	}

	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		kind = models.ReportDraftKindIncident
	}
	if !models.IsValidReportDraftKind(kind) {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_kind",
			"kind must be one of: "+strings.Join(models.ReportDraftKinds(), ", "),
			map[string]interface{}{"kinds": models.ReportDraftKinds()})
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// The reporting officer, and their access. This is the check that matters:
	// a key with reports:write still cannot produce a draft unless it can name
	// a player who is entitled to write one in this community.
	officer, ok := d.requireReportingOfficer(w, r, dc, cc, req.ReportingOfficer)
	if !ok {
		return
	}

	departmentID, ok := d.resolveReportDepartment(w, r, cc, officer, req.DepartmentID)
	if !ok {
		return
	}

	templateSlug, ok := d.resolveReportTemplate(w, r, cc, officer, departmentID, req.TemplateSlug)
	if !ok {
		return
	}

	subject, ok := d.resolveReportSubject(w, r, dc, cc, req.Subject)
	if !ok {
		return
	}

	charges, ok := d.resolveReportCharges(w, r, cc, req.Charges)
	if !ok {
		return
	}

	callID, callExternalID, ok := d.resolveReportCall(w, r, cc, source, req.CallID, req.CallExternalID)
	if !ok {
		return
	}

	facts, err := buildReportFacts(req.Facts)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_facts", err.Error(), nil)
		return
	}
	facts.Charges = chargeNames(charges)

	narrative := buildReportNarrative(req.Narrative)

	existing, err := d.findReportDraft(ctx, cc.Community.ID.Hex(), source, externalID)
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not look up that draft.", nil)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	if existing != nil {
		if !existing.IsOpenForWrites() {
			// A person has already acted on this. Their decision is the record
			// now, and a retry arriving late must not overwrite it.
			d.RecordOutcome(r, http.StatusConflict)
			devError(w, http.StatusConflict, "draft_already_reviewed",
				"A person has already reviewed that draft, so it can no longer be changed. "+
					"Write a new draft with a new externalId if there is more to report.",
				map[string]interface{}{
					"externalId": externalID,
					"status":     existing.Status,
				})
			return
		}

		set := bson.M{
			"kind":                 kind,
			"callId":               callID,
			"callExternalId":       callExternalID,
			"templateSlug":         templateSlug,
			"departmentId":         departmentID,
			"subject":              subject,
			"facts":                facts,
			"narrative":            narrative,
			"charges":              charges,
			"reportingOfficerId":   officer.UserID,
			"reportingOfficerName": officer.Username,
			"updatedAt":            now,
		}
		updated, uErr := d.ReportDraftDB.UpdateOne(ctx,
			bson.M{"_id": devEq(existing.ID)}, bson.M{"$set": set})
		if uErr != nil || updated == nil {
			devError(w, http.StatusInternalServerError, "server_error", "Could not update that draft.", nil)
			return
		}

		d.RecordOutcome(r, http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"draft":   devReportDraftViewOf(*updated),
			"created": false,
		})
		return
	}

	draft := models.DeveloperReportDraft{
		ID:                   primitive.NewObjectID(),
		CommunityID:          cc.Community.ID.Hex(),
		Kind:                 kind,
		Status:               models.ReportDraftStatusDraft,
		SourceName:           source,
		ExternalID:           externalID,
		CallID:               callID,
		CallExternalID:       callExternalID,
		TemplateSlug:         templateSlug,
		DepartmentID:         departmentID,
		Subject:              subject,
		Facts:                facts,
		Narrative:            narrative,
		Charges:              charges,
		ReportingOfficerID:   officer.UserID,
		ReportingOfficerName: officer.Username,
		Source:               devReportRecordSource(dc, source, externalID),
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	if _, err := d.ReportDraftDB.InsertOne(ctx, draft); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not write that draft.", nil)
		return
	}

	logAudit(d.ALDB, cc.Community.ID, "report_draft.created_by_developer_key", "developer_api",
		officer.UserID, officer.Username, draft.ID.Hex(), kind,
		map[string]interface{}{
			"keyId":      dc.Key.ID.Hex(),
			"externalId": externalID,
			"source":     source,
		})

	d.RecordOutcome(r, http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"draft":   devReportDraftViewOf(draft),
		"created": true,
	})
}

// DevGetReportDraftHandler reads a draft back by the integration's own id.
//
// GET /api/v1/dev/communities/{communityId}/reports/drafts/{externalId} — reports:read
//
// This is how an integration finds out what a person decided. The status, the
// reviewer's notes and the id of whatever they filed are all here, so a game
// server can tell a player "your report was filed" rather than guessing.
func (d DeveloperAPI) DevGetReportDraftHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}
	if d.ReportDraftDB == nil {
		devError(w, http.StatusInternalServerError, "server_error", "Report drafts are not available.", nil)
		return
	}

	externalID, source, err := normalizeExternalRef(mux.Vars(r)["externalId"],
		r.URL.Query().Get("source"), dc.Key.Name)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_external_id", err.Error(), nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	draft, err := d.findReportDraft(ctx, cc.Community.ID.Hex(), source, externalID)
	if err != nil || draft == nil {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "draft_not_found",
			"No report draft in this community carries that external id.",
			map[string]interface{}{"externalId": externalID, "source": source})
		return
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"draft": devReportDraftViewOf(*draft)})
}

// DevListReportDraftsHandler lists the drafts this key wrote in a community.
//
// GET /api/v1/dev/communities/{communityId}/reports/drafts — reports:read
//
// Scoped to the calling key, not to the community. A key reads back its own
// output; it is not a window onto what other integrations, or the community's
// own officers, have written.
func (d DeveloperAPI) DevListReportDraftsHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}
	if d.ReportDraftDB == nil {
		devError(w, http.StatusInternalServerError, "server_error", "Report drafts are not available.", nil)
		return
	}

	page, limit := devPageParams(r, maxReportDraftPage)

	filter := bson.M{
		"communityId":  devEq(cc.Community.ID.Hex()),
		"source.keyId": devEq(dc.Key.ID.Hex()),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		status, sErr := devSafeReportStatus(raw)
		if sErr != nil {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_status", sErr.Error(), nil)
			return
		}
		filter["status"] = devEq(status)
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("kind")); raw != "" {
		if !models.IsValidReportDraftKind(raw) {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_kind",
				"kind must be one of: "+strings.Join(models.ReportDraftKinds(), ", "), nil)
			return
		}
		filter["kind"] = devEq(strings.ToLower(strings.TrimSpace(raw)))
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	total, _ := d.ReportDraftDB.CountDocuments(ctx, filter)

	// Sorted before skipped. A skip with no sort is a page out of an unordered
	// result, which silently repeats and omits rows as the collection changes.
	opts := options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}).
		SetSkip(int64((page - 1) * limit)).
		SetLimit(int64(limit))

	drafts, err := d.ReportDraftDB.Find(ctx, filter, opts)
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not list those drafts.", nil)
		return
	}

	views := make([]devReportDraftView, 0, len(drafts))
	for _, draft := range drafts {
		views = append(views, devReportDraftViewOf(draft))
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       views,
		"totalCount": total,
		"page":       page,
		"limit":      limit,
	})
}

// DevWithdrawReportDraftHandler takes back a draft nobody has acted on.
//
// DELETE /api/v1/dev/communities/{communityId}/reports/drafts/{externalId} — reports:write
//
// Withdrawn rather than deleted, and only while it is still a draft: once a
// person has filed or rejected it, what they decided stays on the record. An
// integration that wrote a draft by mistake can take it out of the queue; it
// cannot erase a decision.
func (d DeveloperAPI) DevWithdrawReportDraftHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}
	if d.ReportDraftDB == nil {
		devError(w, http.StatusInternalServerError, "server_error", "Report drafts are not available.", nil)
		return
	}

	externalID, source, err := normalizeExternalRef(mux.Vars(r)["externalId"],
		r.URL.Query().Get("source"), dc.Key.Name)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_external_id", err.Error(), nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	draft, err := d.findReportDraft(ctx, cc.Community.ID.Hex(), source, externalID)
	if err != nil || draft == nil {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "draft_not_found",
			"No report draft in this community carries that external id.", nil)
		return
	}

	if draft.Status == models.ReportDraftStatusWithdrawn {
		// A repeat is a no-op rather than an error, for the same reason closing
		// a closed call is: a retry after a timeout must not become a failure
		// the caller has to special-case.
		d.RecordOutcome(r, http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"draft": devReportDraftViewOf(*draft), "withdrawn": false,
			"message": "That draft was already withdrawn.",
		})
		return
	}
	if !draft.IsOpenForWrites() {
		d.RecordOutcome(r, http.StatusConflict)
		devError(w, http.StatusConflict, "draft_already_reviewed",
			"A person has already reviewed that draft, so it cannot be withdrawn.",
			map[string]interface{}{"status": draft.Status})
		return
	}

	updated, err := d.ReportDraftDB.UpdateOne(ctx, bson.M{"_id": devEq(draft.ID)},
		bson.M{"$set": bson.M{
			"status":    models.ReportDraftStatusWithdrawn,
			"updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		}})
	if err != nil || updated == nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not withdraw that draft.", nil)
		return
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"draft": devReportDraftViewOf(*updated), "withdrawn": true,
	})
}

// ---- resolution and validation ----

// requireReportingOfficer resolves the player a draft is attributed to and
// checks they may write one in this community.
//
// The bar is the CAD's own: an approved, unbanned member who belongs to a
// department. That is who can open a report form on the website, so it is who
// an integration may name. Deliberately NOT a community permission like
// "manage records" — that one gates filing and deleting, which is exactly the
// thing this surface does not do.
func (d DeveloperAPI) requireReportingOfficer(w http.ResponseWriter, r *http.Request, dc *DevAPIContext, cc *CommunityContext, ref string) (*resolvedPlayer, bool) {
	if strings.TrimSpace(ref) == "" {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "reporting_officer_required",
			"reportingOfficer is required: a draft has to be attributed to the player who observed it.", nil)
		return nil, false
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	officer, err := d.resolvePlayerRef(ctx, dc, ref, "", false)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_reporting_officer", err.Error(), nil)
		return nil, false
	}

	isOwner := cc.Community.Details.OwnerID == officer.UserID
	if !isOwner && !isApprovedMemberOfCommunity(ctx, d.UDB, officer.UserID, cc.Community.ID.Hex()) {
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "reporting_officer_not_a_member",
			"The reporting officer is not an approved member of this community.",
			map[string]interface{}{"playerRef": ref})
		return nil, false
	}
	if userBannedFromCommunity(&cc.Community, officer.UserID) {
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "reporting_officer_banned",
			"The reporting officer is banned from this community.", nil)
		return nil, false
	}
	if !isOwner && !helpers.IsCommunityAdmin(&cc.Community, officer.UserID) &&
		!playerIsInAnyDepartment(&cc.Community, officer.UserID) {
		// Not in a department is not an error the integration can fix by
		// retrying, so it says what has to change instead.
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "reporting_officer_has_no_department",
			"The reporting officer is not in any department in this community, so they cannot write a report. "+
				"Add them to a department first.",
			map[string]interface{}{"playerRef": ref})
		return nil, false
	}
	return officer, true
}

// playerIsInAnyDepartment reports whether a user belongs to a department.
func playerIsInAnyDepartment(community *models.Community, userID string) bool {
	for _, dept := range community.Details.Departments {
		for _, m := range dept.Members {
			if m.UserID == userID {
				return true
			}
		}
	}
	return false
}

// resolveReportDepartment validates a named department and the officer's
// membership of it, or picks the one they are in when the caller said nothing.
func (d DeveloperAPI) resolveReportDepartment(w http.ResponseWriter, r *http.Request, cc *CommunityContext, officer *resolvedPlayer, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		// One department is the common case, so inferring it saves an
		// integration a lookup. With several, leaving it unset is correct: the
		// reviewer picks, rather than us guessing wrong.
		var found string
		for _, dept := range cc.Community.Details.Departments {
			for _, m := range dept.Members {
				if m.UserID == officer.UserID {
					if found != "" {
						return "", true
					}
					found = dept.ID.Hex()
				}
			}
		}
		return found, true
	}

	departmentID, _, err := devSafeObjectID(raw)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_department", "That department id is not valid.", nil)
		return "", false
	}
	if findCommunityDepartment(&cc.Community, departmentID) == nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "department_not_found",
			"No department in this community has that id.",
			map[string]interface{}{"departmentId": departmentID})
		return "", false
	}
	if !helpers.IsCommunityAdmin(&cc.Community, officer.UserID) &&
		!helpers.UserIsDepartmentMember(&cc.Community, departmentID, officer.UserID) {
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "reporting_officer_not_in_department",
			"The reporting officer is not a member of that department.",
			map[string]interface{}{"departmentId": departmentID})
		return "", false
	}
	return departmentID, true
}

// resolveReportTemplate honours the community's own forms.
//
// A community that built an incident form, restricted it to a department and
// gated it by rank has said who may use it. An integration naming that form
// has to clear the same gates, checked against the reporting officer with the
// same helper the website uses (helpers.UserCanEditForm), so the two cannot
// drift apart.
//
// A draft with no template is still fine: most integrations will not know the
// community's form layout, and refusing them would make the feature unusable
// for the common case to enforce a rule nobody set.
func (d DeveloperAPI) resolveReportTemplate(w http.ResponseWriter, r *http.Request, cc *CommunityContext, officer *resolvedPlayer, departmentID, raw string) (string, bool) {
	slug := strings.ToLower(strings.TrimSpace(raw))
	if slug == "" {
		return "", true
	}
	safeSlug, err := devSafeScalar(slug, 80, "templateSlug")
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_template", err.Error(), nil)
		return "", false
	}
	if d.FormTemplateDB == nil {
		// No template store wired means we cannot check the gates, and
		// accepting the slug unchecked would record an access decision we never
		// made. Refusing is the honest answer.
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "template_not_found",
			"This community has no form with that slug.", nil)
		return "", false
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	template, err := d.FormTemplateDB.FindOne(ctx, bson.M{
		"formTemplate.communityID": devEq(cc.Community.ID.Hex()),
		"formTemplate.slug":        devEq(safeSlug),
	})
	if err != nil || template == nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "template_not_found",
			"This community has no form with that slug.",
			map[string]interface{}{"templateSlug": safeSlug})
		return "", false
	}
	if template.Details.IsArchived || template.Details.IsHidden {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "template_unavailable",
			"That form is archived or hidden in this community.",
			map[string]interface{}{"templateSlug": safeSlug})
		return "", false
	}
	if !helpers.UserCanEditForm(&cc.Community, officer.UserID, template.Details.EffectiveRankGates()) {
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "template_not_permitted",
			"The reporting officer may not use that form in this community. "+
				"A key never grants more than the player it reports for.",
			map[string]interface{}{"templateSlug": safeSlug})
		return "", false
	}
	if departmentID != "" && d.DeptFormToggleDB != nil {
		toggles, tErr := d.DeptFormToggleDB.Find(ctx, bson.M{
			"departmentFormToggle.communityID":      devEq(cc.Community.ID.Hex()),
			"departmentFormToggle.departmentId":     devEq(departmentID),
			"departmentFormToggle.formTemplateSlug": devEq(safeSlug),
		}, options.Find().SetLimit(1))
		// No row means enabled, which is the product's own default: toggles are
		// only written when an admin overrides it. Only an explicit disable
		// refuses.
		if tErr == nil && len(toggles) == 1 && !toggles[0].Details.IsEnabled {
			d.RecordOutcome(r, http.StatusForbidden)
			devError(w, http.StatusForbidden, "template_disabled_for_department",
				"That form is turned off for this department.",
				map[string]interface{}{"templateSlug": safeSlug, "departmentId": departmentID})
			return "", false
		}
	}
	return safeSlug, true
}

// resolveReportSubject resolves who the draft is about.
//
// A linked player must be someone we know, and for a real person must be a
// member of this community — the same boundary every other write applies. A
// synthetic subject is exempt from the membership check because an NPC is not
// a member of anything; it is still only accepted when the identity itself was
// created by this key, so one integration cannot write drafts about another's
// NPCs.
func (d DeveloperAPI) resolveReportSubject(w http.ResponseWriter, r *http.Request, dc *DevAPIContext, cc *CommunityContext, req devReportSubjectRequest) (models.DeveloperReportSubject, bool) {
	out := models.DeveloperReportSubject{
		DisplayName: truncateShort(req.DisplayName),
	}

	ref := strings.TrimSpace(req.Player)
	if ref == "" {
		if strings.TrimSpace(req.CivilianID) != "" {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_subject",
				"subject.civilianId needs subject.player, so we know whose character it is.", nil)
			return out, false
		}
		return out, true
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	player, err := d.resolvePlayerRef(ctx, dc, ref, "", false)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_subject", "subject.player: "+err.Error(), nil)
		return out, false
	}

	synthetic := d.playerIsSyntheticForKey(ctx, dc, player)
	if !synthetic &&
		cc.Community.Details.OwnerID != player.UserID &&
		!isApprovedMemberOfCommunity(ctx, d.UDB, player.UserID, cc.Community.ID.Hex()) {
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "subject_not_a_member",
			"The subject is not an approved member of this community.",
			map[string]interface{}{"playerRef": ref})
		return out, false
	}

	out.PlayerRef = player.Provider + ":" + player.Identifier
	out.UserID = player.UserID
	out.Synthetic = synthetic
	if out.DisplayName == "" {
		out.DisplayName = player.Username
	}

	if raw := strings.TrimSpace(req.CivilianID); raw != "" {
		civilian, ok := d.requireOwnedCivilian(w, r, cc, player, raw)
		if !ok {
			return out, false
		}
		out.CivilianID = civilian.ID.Hex()
		if out.DisplayName == "" {
			out.DisplayName = strings.TrimSpace(
				civilian.Details.FirstName + " " + civilian.Details.LastName)
		}
	}
	return out, true
}

// resolveReportCharges turns charge names into priced charges from the
// community's own penal code.
//
// The integration sends names; the amounts come from the community. A key
// cannot invent a fine or a jail term, which is the whole reason this is a
// lookup rather than a passthrough — and it means a community that retunes its
// penal code sees new drafts priced the new way without the integration
// knowing anything changed.
func (d DeveloperAPI) resolveReportCharges(w http.ResponseWriter, r *http.Request, cc *CommunityContext, names []string) ([]models.ArrestCharge, bool) {
	out := []models.ArrestCharge{}
	if len(names) == 0 {
		return out, true
	}
	if len(names) > maxReportCharges {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "too_many_charges",
			fmt.Sprintf("A draft may name at most %d charges.", maxReportCharges), nil)
		return nil, false
	}

	for _, raw := range names {
		name, err := devSafeScalar(raw, 200, "charge")
		if err != nil {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_charge", err.Error(), nil)
			return nil, false
		}
		charge, found := lookupPenalCodeCharge(&cc.Community, name)
		if !found {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "charge_not_in_penal_code",
				"This community's penal code has no violation by that name. "+
					"Read the penal code from the community endpoint and send the names it lists.",
				map[string]interface{}{"charge": name})
			return nil, false
		}
		out = append(out, charge)
	}
	return out, true
}

// lookupPenalCodeCharge finds a violation in a community's penal code,
// case-insensitively, and prices it.
func lookupPenalCodeCharge(community *models.Community, name string) (models.ArrestCharge, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, category := range community.Details.PenalCodes.Categories {
		for _, violation := range category.Violations {
			if strings.ToLower(strings.TrimSpace(violation.Name)) != want {
				continue
			}
			return models.ArrestCharge{
				Name:     violation.Name,
				Category: category.Name,
				Amount:   float64(violation.Fine),
				JailTime: violation.JailTime,
			}, true
		}
	}
	return models.ArrestCharge{}, false
}

func chargeNames(charges []models.ArrestCharge) []string {
	if len(charges) == 0 {
		return nil
	}
	out := make([]string, 0, len(charges))
	for _, c := range charges {
		out = append(out, c.Name)
	}
	return out
}

// resolveReportCall links a draft to a dispatch call.
//
// By the integration's own id first, because that is the id it still holds
// after a restart; ours is accepted for a caller that kept it. Either way the
// call has to be one in this community, so a draft cannot be pinned to an
// incident somewhere else.
func (d DeveloperAPI) resolveReportCall(w http.ResponseWriter, r *http.Request, cc *CommunityContext, source, rawCallID, rawExternal string) (string, string, bool) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	if ref := strings.TrimSpace(rawExternal); ref != "" {
		externalID, err := devSafeExternalID(ref)
		if err != nil {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_call_external_id", err.Error(), nil)
			return "", "", false
		}
		call, cErr := d.findCallByExternalRef(ctx, cc.Community.ID.Hex(), source, externalID)
		if cErr != nil || call == nil {
			d.RecordOutcome(r, http.StatusNotFound)
			devError(w, http.StatusNotFound, "call_not_found",
				"No call in this community carries that external id.",
				map[string]interface{}{"callExternalId": externalID, "source": source})
			return "", "", false
		}
		return call.ID, externalID, true
	}

	if raw := strings.TrimSpace(rawCallID); raw != "" {
		callID, err := devSafeScalar(raw, 64, "callId")
		if err != nil {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_call_id", err.Error(), nil)
			return "", "", false
		}
		calls, cErr := d.CallDB.Find(ctx, bson.M{
			"_id":              devEq(callID),
			"call.communityID": devEq(cc.Community.ID.Hex()),
		}, options.Find().SetLimit(1))
		if cErr != nil || len(calls) == 0 {
			d.RecordOutcome(r, http.StatusNotFound)
			devError(w, http.StatusNotFound, "call_not_found",
				"No call in this community has that id.",
				map[string]interface{}{"callId": callID})
			return "", "", false
		}
		return calls[0].ID, calls[0].Details.ExternalID, true
	}
	return "", "", true
}

// buildReportFacts bounds and copies the structured half of a draft.
func buildReportFacts(req devReportFactsRequest) (models.DeveloperReportFacts, error) {
	facts := models.DeveloperReportFacts{
		OccurredAt: truncateShort(req.OccurredAt),
		Location:   truncateDispatchText(req.Location),
		Summary:    truncateReportText(req.Summary, maxReportSummaryLen),
		ForceUsed:  req.ForceUsed,
		Vehicles:   boundedStringList(req.Vehicles),
		Witnesses:  boundedStringList(req.Witnesses),
		Officers:   boundedStringList(req.Officers),
	}
	if len(req.Fields) > maxReportFactFields {
		return facts, fmt.Errorf("facts.fields may hold at most %d entries", maxReportFactFields)
	}
	if len(req.Fields) > 0 {
		fields := make(map[string]string, len(req.Fields))
		for key, value := range req.Fields {
			// A key becomes part of a document path when the website renders
			// it, so the same allowlist every other scalar passes applies.
			safeKey, err := devSafeScalar(key, 80, "facts.fields key")
			if err != nil {
				return facts, err
			}
			fields[safeKey] = truncateReportText(value, maxReportFactValueLen)
		}
		facts.Fields = fields
	}
	return facts, nil
}

// buildReportNarrative copies the prose half and settles the generated claim.
//
// A caller that does not say is treated as generated. The surface exists for
// automated callers, so "a machine wrote this" is the likely truth, and of the
// two possible mislabels it is the one that makes a reviewer read more
// carefully rather than less.
func buildReportNarrative(req devReportNarrativeRequest) models.DeveloperReportNarrative {
	generated := true
	if req.Generated != nil {
		generated = *req.Generated
	}
	out := models.DeveloperReportNarrative{
		Text:      truncateReportText(req.Text, maxReportNarrativeLen),
		Generated: generated,
	}
	if generated {
		out.GeneratedBy = truncateShort(req.GeneratedBy)
	}
	return out
}

func truncateReportText(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return strings.TrimSpace(s[:max])
	}
	return s
}

func boundedStringList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	if len(in) > maxReportListEntries {
		in = in[:maxReportListEntries]
	}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = truncateShort(v); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// devSafeReportStatus validates a status used to filter a list.
func devSafeReportStatus(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case models.ReportDraftStatusDraft:
		return models.ReportDraftStatusDraft, nil
	case models.ReportDraftStatusSubmitted:
		return models.ReportDraftStatusSubmitted, nil
	case models.ReportDraftStatusRejected:
		return models.ReportDraftStatusRejected, nil
	case models.ReportDraftStatusWithdrawn:
		return models.ReportDraftStatusWithdrawn, nil
	}
	return "", fmt.Errorf("status must be one of: draft, submitted, rejected, withdrawn")
}

// findReportDraft resolves a draft by the caller's own id, scoped by community.
func (d DeveloperAPI) findReportDraft(ctx context.Context, communityID, source, externalID string) (*models.DeveloperReportDraft, error) {
	drafts, err := d.ReportDraftDB.Find(ctx, bson.M{
		"communityId": devEq(communityID),
		"sourceName":  devEq(source),
		"externalId":  devEq(externalID),
	}, options.Find().SetLimit(1))
	if err != nil {
		return nil, err
	}
	if len(drafts) == 0 {
		return nil, nil
	}
	return &drafts[0], nil
}

// devReportRecordSource stamps a draft with the key that wrote it, including
// the integration's own reference, so a key's drafts can be found and removed
// together after the key itself is gone.
func devReportRecordSource(dc *DevAPIContext, source, externalID string) *models.RecordSource {
	src := devRecordSource(dc, nil)
	src.SourceName = source
	src.ExternalID = externalID
	return src
}

// devPageParams reads page and limit, 1-based, bounded.
//
// 1-based because every v2 list in this product is, and a developer reading two
// of our endpoints should not have to discover that one of them counts from
// zero.
func devPageParams(r *http.Request, maxLimit int) (int, int) {
	page := 1
	if v, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page"))); err == nil && v > 1 {
		page = v
	}
	limit := 25
	if v, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit"))); err == nil && v > 0 {
		limit = v
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	return page, limit
}

// devReportDraftViewOf shapes a draft for the wire.
func devReportDraftViewOf(draft models.DeveloperReportDraft) devReportDraftView {
	view := devReportDraftView{
		ID:                   draft.ID.Hex(),
		ExternalID:           draft.ExternalID,
		Source:               draft.SourceName,
		Kind:                 draft.Kind,
		Status:               draft.Status,
		CommunityID:          draft.CommunityID,
		CallID:               draft.CallID,
		CallExternalID:       draft.CallExternalID,
		TemplateSlug:         draft.TemplateSlug,
		DepartmentID:         draft.DepartmentID,
		Subject:              draft.Subject,
		Facts:                draft.Facts,
		Narrative:            draft.Narrative,
		Charges:              draft.Charges,
		ReportingOfficerID:   draft.ReportingOfficerID,
		ReportingOfficerName: draft.ReportingOfficerName,
		ReviewedByName:       draft.ReviewedByName,
		ReviewNotes:          draft.ReviewNotes,
		FiledRecordID:        draft.FiledRecordID,
		FiledRecordKind:      draft.FiledRecordKind,
		CreatedAt:            formatDevDateTime(draft.CreatedAt),
		UpdatedAt:            formatDevDateTime(draft.UpdatedAt),
		Filed:                draft.Status == models.ReportDraftStatusSubmitted && draft.FiledRecordID != "",
		ReviewLink:           reportDraftReviewLink(draft.CommunityID, draft.ID.Hex()),
	}
	if view.Charges == nil {
		view.Charges = []models.ArrestCharge{}
	}
	if draft.ReviewedAt != nil {
		view.ReviewedAt = formatDevDateTime(*draft.ReviewedAt)
	}
	return view
}

// reportDraftReviewLink opens a draft on the review screen, so a game server
// can put a URL in a supervisor's chat rather than an id they cannot use.
func reportDraftReviewLink(communityID, draftID string) string {
	if communityID == "" || draftID == "" {
		return ""
	}
	base := publicWebBaseURL()
	if base == "" {
		return ""
	}
	return fmt.Sprintf("%s/community/%s/report-drafts/%s", base, communityID, draftID)
}

// publicWebBaseURL is the website a deep link points at.
//
// One spelling, used by every link this surface hands out, so a self-hosted
// deployment that sets PUBLIC_WEB_BASE_URL does not get half its links right.
func publicWebBaseURL() string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_WEB_BASE_URL")), "/")
	if base == "" {
		base = "https://www.linespolice-cad.com"
	}
	return base
}

// formatDevDateTime renders a stored timestamp as RFC3339 UTC, or empty when
// it was never set — rather than the Unix epoch, which reads as a real date.
func formatDevDateTime(v primitive.DateTime) string {
	if v == 0 {
		return ""
	}
	return v.Time().UTC().Format(time.RFC3339)
}
