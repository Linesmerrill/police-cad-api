package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/api/handlers/formdefaults"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// FormSubmission handles CRUD for filled form instances. It also handles
// the auto-fill / draft pre-population workflow.
type FormSubmission struct {
	DB      databases.FormSubmissionDatabase
	TDB     databases.FormTemplateDatabase
	VDB     databases.FormTemplateVersionDatabase
	CDB     databases.FormCounterDatabase
	UDB     databases.UserDatabase
	CommDB  databases.CommunityDatabase
	CallDB  databases.CallDatabase
	ARDB    databases.ArrestReportDatabase
	CivDB   databases.CivilianDatabase
	VehDB   databases.VehicleDatabase
	FirDB   databases.FirearmDatabase
}

// SourceRef is a request-side reference to an existing entity used as an
// auto-fill source when creating or drafting a submission.
type SourceRef struct {
	Type    string `json:"type"`              // call, arrestReport, civilian, vehicle, firearm
	ID      string `json:"id"`
	ChildID string `json:"childId,omitempty"` // for citations (criminalHistoryID inside civilian)
}

// CreateFormSubmissionHandler inserts a new submission, snapshotting the
// current template version, applying auto-fill from sources + auth
// context, and generating a report number when one wasn't provided.
//
// Request body:
//
//	{
//	  "communityID":      "...",
//	  "departmentId":     "...",
//	  "formTemplateSlug": "incident-report",
//	  "data":             { ...overrides keyed by field.id... },
//	  "links":            [{type, id, childId?, label?}, ...],
//	  "sources":          [{type, id, childId?}, ...],
//	  "reportNumber":     "RR-2026-000123"   // optional; auto-generated if blank
//	  "status":           "draft" | "submitted"
//	}
func (h FormSubmission) CreateFormSubmissionHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CommunityID      string                          `json:"communityID"`
		DepartmentID     string                          `json:"departmentId"`
		FormTemplateSlug string                          `json:"formTemplateSlug"`
		Data             map[string]interface{}          `json:"data"`
		Links            []models.FormSubmissionLink     `json:"links"`
		Sources          []SourceRef                     `json:"sources"`
		ReportNumber     string                          `json:"reportNumber"`
		Status           string                          `json:"status"`
		SignedBy         *models.FormSubmissionSignature `json:"signedBy,omitempty"` // server-trusted fallback when auth context is missing (e.g. website proxy)
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	if body.CommunityID == "" || body.FormTemplateSlug == "" {
		config.ErrorStatus("communityID and formTemplateSlug are required", http.StatusBadRequest, w, fmt.Errorf("missing required fields"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	tplID, sections, version, err := h.resolveActiveTemplate(ctx, body.CommunityID, body.FormTemplateSlug)
	if err != nil {
		config.ErrorStatus("template not found", http.StatusNotFound, w, err)
		return
	}

	prefilled := h.buildPrefill(ctx, sections, body.Sources, r)

	// Officer overrides win.
	for k, v := range body.Data {
		prefilled[k] = v
	}

	reportNumber := body.ReportNumber
	if strings.TrimSpace(reportNumber) == "" {
		reportNumber = h.nextReportNumber(ctx, body.CommunityID, body.FormTemplateSlug, w, r)
		if reportNumber == "" {
			return // error already written
		}
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	signedBy := h.lookupSignedBy(ctx, api.GetAuthenticatedUserIDFromContext(r.Context()), now)
	// Fallback: when no auth-context user (e.g. website server proxying with a
	// system token), accept body-supplied signedBy.
	if signedBy.UserID == "" && body.SignedBy != nil && body.SignedBy.UserID != "" {
		signedBy.UserID = body.SignedBy.UserID
		signedBy.Username = body.SignedBy.Username
		signedBy.SignedAt = now
	}
	// Defense in depth: if auth context resolved a userID but the user
	// lookup couldn't fetch a username (e.g. legacy _id shape, db hiccup),
	// trust a client-supplied username from body.SignedBy when it's
	// available. Without this the report renders as "Unsigned" / "by
	// Unknown" even though we know who created it.
	if signedBy.Username == "" && body.SignedBy != nil && body.SignedBy.Username != "" {
		signedBy.Username = body.SignedBy.Username
	}

	status := body.Status
	if status == "" {
		status = "submitted"
	}

	// Always seed history with a "created" entry so the audit trail
	// records who originated the report — this avoids the "by Unknown"
	// fallback the clients used to render synthetically. For a direct-
	// submit (status=submitted on insert), append the submit entry too.
	history := []models.FormSubmissionHistoryEntry{{
		Action:   "created",
		UserID:   signedBy.UserID,
		Username: signedBy.Username,
		At:       now,
	}}
	if status == "submitted" {
		history = append(history, models.FormSubmissionHistoryEntry{
			Action:   "submitted",
			UserID:   signedBy.UserID,
			Username: signedBy.Username,
			At:       now,
		})
	}

	sub := models.FormSubmission{
		ID: primitive.NewObjectID(),
		Details: models.FormSubmissionDetails{
			CommunityID:         body.CommunityID,
			DepartmentID:        body.DepartmentID,
			FormTemplateID:      tplID,
			FormTemplateSlug:    body.FormTemplateSlug,
			FormTemplateVersion: version,
			ReportNumber:        reportNumber,
			Data:                prefilled,
			Links:               body.Links,
			SignedBy:            signedBy,
			Status:              status,
			History:             history,
			CreatedAt:           now,
			UpdatedAt:           now,
		},
	}

	// Insert with retry on duplicate-report-number collisions. The
	// formCounter is keyed by (communityID, slug, year) but the
	// unique index on submissions is (communityID, reportNumber) —
	// so a brand-new template starts at seq=1 and collides with the
	// existing template's first reports. On the first collision,
	// catch the per-slug counter up to the highest used seq across
	// all slugs in this community/year, then retry with a small budget
	// to absorb concurrent-write races.
	const maxAttempts = 5
	autoNumber := strings.TrimSpace(body.ReportNumber) == ""
	year := time.Now().Year()
	var insertErr error
	caughtUp := false
	for attempt := 0; attempt < maxAttempts; attempt++ {
		_, insertErr = h.DB.InsertOne(ctx, sub)
		if insertErr == nil {
			break
		}
		if !mongo.IsDuplicateKeyError(insertErr) || !autoNumber || attempt == maxAttempts-1 {
			break
		}
		if !caughtUp {
			// One-shot jump-ahead: find the highest reportNumber
			// already in use for this community/year and bump our
			// per-slug counter past it. Bounds the retry depth even
			// when another template has hundreds of submissions.
			if maxSeq, ok := h.maxReportNumberSeq(ctx, body.CommunityID, year); ok {
				_ = h.CDB.CatchUp(ctx, body.CommunityID, body.FormTemplateSlug, year, maxSeq)
			}
			caughtUp = true
		}
		nextNum := h.nextReportNumber(ctx, body.CommunityID, body.FormTemplateSlug, w, r)
		if nextNum == "" {
			return // error already written by nextReportNumber
		}
		reportNumber = nextNum
		sub.Details.ReportNumber = reportNumber
		sub.ID = primitive.NewObjectID()
	}
	if insertErr != nil {
		config.ErrorStatus("failed to create submission", http.StatusInternalServerError, w, insertErr)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":      "Submission created",
		"id":           sub.ID.Hex(),
		"reportNumber": reportNumber,
		"submission":   sub,
	})
}

// FormSubmissionByIDHandler returns a single submission by ID.
func (h FormSubmission) FormSubmissionByIDHandler(w http.ResponseWriter, r *http.Request) {
	subID := mux.Vars(r)["submission_id"]
	objID, err := primitive.ObjectIDFromHex(subID)
	if err != nil {
		config.ErrorStatus("invalid submission id", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	sub, err := h.DB.FindOne(ctx, bson.M{"_id": objID})
	if err != nil {
		config.ErrorStatus("submission not found", http.StatusNotFound, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(sub)
}

// UpdateFormSubmissionHandler patches a submission. Officer-editable
// fields only — formTemplateVersion and signedBy are NOT updatable.
//
// Lock rules: while status="submitted", any change requires the actor
// to be the original signer or a community admin. Status transitions
// (submitted ↔ draft) append an entry to the history audit trail.
func (h FormSubmission) UpdateFormSubmissionHandler(w http.ResponseWriter, r *http.Request) {
	subID := mux.Vars(r)["submission_id"]
	objID, err := primitive.ObjectIDFromHex(subID)
	if err != nil {
		config.ErrorStatus("invalid submission id", http.StatusBadRequest, w, err)
		return
	}

	var body struct {
		Data         map[string]interface{}          `json:"data,omitempty"`
		Links        []models.FormSubmissionLink     `json:"links,omitempty"`
		Status       *string                         `json:"status,omitempty"`
		ReportNumber *string                         `json:"reportNumber,omitempty"`
		Archived     *bool                           `json:"archived,omitempty"`
		DepartmentID *string                         `json:"departmentId,omitempty"`
		Actor        *models.FormSubmissionSignature `json:"actor,omitempty"` // server-trusted website fallback when no auth context
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	existing, err := h.DB.FindOne(ctx, bson.M{"_id": objID})
	if err != nil || existing == nil {
		config.ErrorStatus("submission not found", http.StatusNotFound, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	actor := h.resolveActor(ctx, r, body.Actor, now)

	// Authorization: a submitted OR archived report is locked. Any
	// change (data, links, reopen, archive toggle, etc.) requires the
	// actor to be the original signer or a community admin.
	if existing.Details.Status == "submitted" || existing.Details.Archived {
		if !h.canManageSubmission(ctx, actor.UserID, existing.Details) {
			config.ErrorStatus("only the report's author or a community admin can edit a locked or archived report", http.StatusForbidden, w, fmt.Errorf("forbidden"))
			return
		}
	}

	set := bson.M{"formSubmission.updatedAt": now}
	if body.Data != nil {
		set["formSubmission.data"] = body.Data
	}
	if body.Links != nil {
		set["formSubmission.links"] = body.Links
	}
	if body.ReportNumber != nil {
		set["formSubmission.reportNumber"] = *body.ReportNumber
	}

	var historyEntries []models.FormSubmissionHistoryEntry
	if body.Status != nil && *body.Status != existing.Details.Status {
		set["formSubmission.status"] = *body.Status
		switch *body.Status {
		case "draft":
			if existing.Details.Status == "submitted" {
				historyEntries = append(historyEntries, models.FormSubmissionHistoryEntry{
					Action:   "reopened",
					UserID:   actor.UserID,
					Username: actor.Username,
					At:       now,
				})
			}
		case "submitted":
			action := "submitted"
			if len(existing.Details.History) > 0 {
				action = "resubmitted"
			}
			historyEntries = append(historyEntries, models.FormSubmissionHistoryEntry{
				Action:   action,
				UserID:   actor.UserID,
				Username: actor.Username,
				At:       now,
			})
		}
	}

	// Department reassignment: allowed when the report is a draft going
	// into this write (so the dept gets stamped before any submit/lock
	// happens), OR when this same write is reopening a submitted report
	// back to draft. The order is conceptually: dept change → status
	// flip, so a draft-then-submit-with-new-dept is valid.
	if body.DepartmentID != nil && *body.DepartmentID != existing.Details.DepartmentID {
		wasEditable := existing.Details.Status == "draft" && !existing.Details.Archived
		isReopening := body.Status != nil && *body.Status == "draft"
		if !wasEditable && !isReopening {
			config.ErrorStatus("department can only be changed while the report is a draft", http.StatusBadRequest, w, fmt.Errorf("department locked"))
			return
		}
		set["formSubmission.departmentId"] = *body.DepartmentID
	}

	if body.Archived != nil && *body.Archived != existing.Details.Archived {
		set["formSubmission.archived"] = *body.Archived
		if *body.Archived {
			sig := models.FormSubmissionSignature{UserID: actor.UserID, Username: actor.Username, SignedAt: now}
			set["formSubmission.archivedBy"] = sig
			historyEntries = append(historyEntries, models.FormSubmissionHistoryEntry{
				Action:   "archived",
				UserID:   actor.UserID,
				Username: actor.Username,
				At:       now,
			})
		} else {
			set["formSubmission.archivedBy"] = nil
			historyEntries = append(historyEntries, models.FormSubmissionHistoryEntry{
				Action:   "unarchived",
				UserID:   actor.UserID,
				Username: actor.Username,
				At:       now,
			})
		}
	}

	// Draft-save audit: when a draft is being saved (no status flip,
	// not archive/unarchive) but data, links, or department changed,
	// log an "edited" entry so the history reflects every save.
	// Skipped for submitted/archived writes since those already get
	// their own action-specific entries above.
	if len(historyEntries) == 0 && existing.Details.Status == "draft" && !existing.Details.Archived {
		dataChanged := body.Data != nil
		linksChanged := body.Links != nil
		deptChanged := body.DepartmentID != nil && *body.DepartmentID != existing.Details.DepartmentID
		if dataChanged || linksChanged || deptChanged {
			historyEntries = append(historyEntries, models.FormSubmissionHistoryEntry{
				Action:   "edited",
				UserID:   actor.UserID,
				Username: actor.Username,
				At:       now,
			})
		}
	}

	update := bson.M{"$set": set}
	if len(historyEntries) > 0 {
		update["$push"] = bson.M{"formSubmission.history": bson.M{"$each": historyEntries}}
	}

	if err := h.DB.UpdateOne(ctx, bson.M{"_id": objID}, update); err != nil {
		config.ErrorStatus("failed to update submission", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"message": "Submission updated"})
}

// resolveActor returns the user performing this request. Prefers the
// auth-context user; falls back to a website-supplied actor block when
// the API is being proxied by a server-token caller.
func (h FormSubmission) resolveActor(ctx context.Context, r *http.Request, fallback *models.FormSubmissionSignature, now primitive.DateTime) models.FormSubmissionSignature {
	if uid := api.GetAuthenticatedUserIDFromContext(r.Context()); uid != "" {
		sig := h.lookupSignedBy(ctx, uid, now)
		// Defense in depth: if the user lookup couldn't resolve a
		// username, accept a client-supplied one so history entries
		// don't get stamped with an empty actor.
		if sig.Username == "" && fallback != nil && fallback.Username != "" {
			sig.Username = fallback.Username
		}
		return sig
	}
	if fallback != nil && fallback.UserID != "" {
		return models.FormSubmissionSignature{
			UserID:   fallback.UserID,
			Username: fallback.Username,
			SignedAt: now,
		}
	}
	return models.FormSubmissionSignature{SignedAt: now}
}

// canManageSubmission returns true when the user is the original
// submitter, the community owner, or holds a role with the
// "administrator" permission in the submission's community.
func (h FormSubmission) canManageSubmission(ctx context.Context, userID string, sub models.FormSubmissionDetails) bool {
	if userID == "" {
		return false
	}
	if sub.SignedBy.UserID != "" && sub.SignedBy.UserID == userID {
		return true
	}
	if sub.CommunityID == "" {
		return false
	}
	commID, err := primitive.ObjectIDFromHex(sub.CommunityID)
	if err != nil {
		return false
	}
	community, err := h.CommDB.FindOne(ctx, bson.M{"_id": commID})
	if err != nil || community == nil {
		return false
	}
	if community.Details.OwnerID == userID {
		return true
	}
	for _, role := range community.Details.Roles {
		inRole := false
		for _, member := range role.Members {
			if member == userID {
				inRole = true
				break
			}
		}
		if !inRole {
			continue
		}
		for _, perm := range role.Permissions {
			if perm.Enabled && perm.Name == "administrator" {
				return true
			}
		}
	}
	return false
}

// DeleteFormSubmissionHandler hard-deletes a submission.
func (h FormSubmission) DeleteFormSubmissionHandler(w http.ResponseWriter, r *http.Request) {
	subID := mux.Vars(r)["submission_id"]
	objID, err := primitive.ObjectIDFromHex(subID)
	if err != nil {
		config.ErrorStatus("invalid submission id", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	if err := h.DB.DeleteOne(ctx, bson.M{"_id": objID}); err != nil {
		config.ErrorStatus("failed to delete submission", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"message": "Submission deleted"})
}

// FormSubmissionDraftHandler returns a non-persisted pre-filled data map
// based on a template + sources. Used by clients to open the create form
// pre-filled. Body identical to CreateFormSubmissionHandler minus persistence.
func (h FormSubmission) FormSubmissionDraftHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CommunityID      string      `json:"communityID"`
		FormTemplateSlug string      `json:"formTemplateSlug"`
		Sources          []SourceRef `json:"sources"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	if body.CommunityID == "" || body.FormTemplateSlug == "" {
		config.ErrorStatus("communityID and formTemplateSlug are required", http.StatusBadRequest, w, fmt.Errorf("missing required fields"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	_, sections, version, err := h.resolveActiveTemplate(ctx, body.CommunityID, body.FormTemplateSlug)
	if err != nil {
		config.ErrorStatus("template not found", http.StatusNotFound, w, err)
		return
	}
	prefilled := h.buildPrefill(ctx, sections, body.Sources, r)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":                prefilled,
		"formTemplateSlug":    body.FormTemplateSlug,
		"formTemplateVersion": version,
	})
}

// FormSubmissionsByCommunityHandlerV2 returns paginated submissions for a community.
//
// Filters via query params:
//
//	templateSlug, departmentID, officerID, status — equality filters
//	createdFrom, createdTo                         — RFC3339 timestamps; both inclusive
//	archived                                       — "true" | "false" | "all"; default "false"
func (h FormSubmission) FormSubmissionsByCommunityHandlerV2(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["community_id"]
	templateSlug := r.URL.Query().Get("templateSlug")
	departmentID := r.URL.Query().Get("departmentID")
	officerID := r.URL.Query().Get("officerID")
	status := r.URL.Query().Get("status")
	createdFromRaw := r.URL.Query().Get("createdFrom")
	createdToRaw := r.URL.Query().Get("createdTo")
	archivedRaw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("archived")))

	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 20
	}
	limit64 := int64(Limit)
	Page := getPage(0, r)
	skip64 := int64(Page * Limit)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"formSubmission.communityID": communityID}
	if templateSlug != "" {
		filter["formSubmission.formTemplateSlug"] = templateSlug
	}
	if departmentID != "" {
		filter["formSubmission.departmentId"] = departmentID
	}
	if officerID != "" {
		filter["formSubmission.signedBy.userID"] = officerID
	}
	if status != "" {
		filter["formSubmission.status"] = status
	}

	// Default behaviour pre-feature was to return everything regardless of
	// archived state. Preserve that default — only apply the filter when
	// the caller explicitly asks for "true" or "false".
	switch archivedRaw {
	case "true":
		filter["formSubmission.archived"] = true
	case "false":
		filter["formSubmission.archived"] = bson.M{"$ne": true}
	case "", "all":
		// no filter
	}

	createdRange := bson.M{}
	if createdFromRaw != "" {
		if t, err := time.Parse(time.RFC3339, createdFromRaw); err == nil {
			createdRange["$gte"] = primitive.NewDateTimeFromTime(t)
		}
	}
	if createdToRaw != "" {
		if t, err := time.Parse(time.RFC3339, createdToRaw); err == nil {
			createdRange["$lte"] = primitive.NewDateTimeFromTime(t)
		}
	}
	if len(createdRange) > 0 {
		filter["formSubmission.createdAt"] = createdRange
	}

	subs, err := h.DB.Find(ctx, filter, &options.FindOptions{
		Limit: &limit64,
		Skip:  &skip64,
		Sort:  bson.M{"_id": -1},
	})
	if err != nil {
		config.ErrorStatus("failed to fetch submissions", http.StatusInternalServerError, w, err)
		return
	}
	totalCount, err := h.DB.CountDocuments(ctx, filter)
	if err != nil {
		totalCount = int64(len(subs))
	}
	if subs == nil {
		subs = []models.FormSubmission{}
	}
	totalPages := int(math.Ceil(float64(totalCount) / float64(Limit)))
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       subs,
		"page":       Page,
		"limit":      Limit,
		"totalCount": totalCount,
		"totalPages": totalPages,
	})
}

// FormSubmissionsByLinkHandlerV2 returns submissions linked to a given
// entity (used by civilian/vehicle/firearm/citation profile pages).
func (h FormSubmission) FormSubmissionsByLinkHandlerV2(w http.ResponseWriter, r *http.Request) {
	linkType := mux.Vars(r)["link_type"]
	linkID := mux.Vars(r)["link_id"]

	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 20
	}
	limit64 := int64(Limit)
	Page := getPage(0, r)
	skip64 := int64(Page * Limit)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{
		"formSubmission.links": bson.M{
			"$elemMatch": bson.M{
				"type": linkType,
				"id":   linkID,
			},
		},
	}

	subs, err := h.DB.Find(ctx, filter, &options.FindOptions{
		Limit: &limit64,
		Skip:  &skip64,
		Sort:  bson.M{"_id": -1},
	})
	if err != nil {
		config.ErrorStatus("failed to fetch submissions", http.StatusInternalServerError, w, err)
		return
	}
	totalCount, err := h.DB.CountDocuments(ctx, filter)
	if err != nil {
		totalCount = int64(len(subs))
	}
	if subs == nil {
		subs = []models.FormSubmission{}
	}
	totalPages := int(math.Ceil(float64(totalCount) / float64(Limit)))
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       subs,
		"page":       Page,
		"limit":      Limit,
		"totalCount": totalCount,
		"totalPages": totalPages,
	})
}

// FormSubmissionsDepartmentsHandler returns the distinct (departmentId,
// departmentName) tuples that have at least one submission in the
// community. Backs the website/mobile department-filter dropdown.
func (h FormSubmission) FormSubmissionsDepartmentsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["community_id"]
	if communityID == "" {
		config.ErrorStatus("communityID is required", http.StatusBadRequest, w, fmt.Errorf("missing communityID"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	pipeline := []bson.M{
		{"$match": bson.M{
			"formSubmission.communityID":  communityID,
			"formSubmission.departmentId": bson.M{"$ne": ""},
		}},
		{"$group": bson.M{"_id": "$formSubmission.departmentId", "count": bson.M{"$sum": 1}}},
		{"$limit": 200},
	}
	cur, err := h.DB.Aggregate(ctx, pipeline)
	if err != nil {
		config.ErrorStatus("failed to aggregate departments", http.StatusInternalServerError, w, err)
		return
	}
	var rows []struct {
		ID    string `bson:"_id"`
		Count int    `bson:"count"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		config.ErrorStatus("failed to read departments", http.StatusInternalServerError, w, err)
		return
	}

	// Resolve department names from the community document.
	commID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community id", http.StatusBadRequest, w, err)
		return
	}
	community, err := h.CommDB.FindOne(ctx, bson.M{"_id": commID})
	nameByID := map[string]string{}
	if err == nil && community != nil {
		for _, d := range community.Details.Departments {
			nameByID[d.ID.Hex()] = d.Name
		}
	}

	type deptOut struct {
		ID    string `json:"departmentId"`
		Name  string `json:"departmentName"`
		Count int    `json:"count"`
	}
	out := make([]deptOut, 0, len(rows))
	for _, row := range rows {
		out = append(out, deptOut{
			ID:    row.ID,
			Name:  nameByID[row.ID],
			Count: row.Count,
		})
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"data": out})
}

// BulkArchiveFormSubmissionsHandler soft-archives submissions in a
// community matching a date range and optional department filter. Admin
// gated by the website proxy (the route is registered on apiCreate which
// already requires a server token); the actor block is required for the
// audit trail.
func (h FormSubmission) BulkArchiveFormSubmissionsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["community_id"]
	if communityID == "" {
		config.ErrorStatus("communityID is required", http.StatusBadRequest, w, fmt.Errorf("missing communityID"))
		return
	}

	var body struct {
		CreatedFrom  string                          `json:"createdFrom"`
		CreatedTo    string                          `json:"createdTo"`
		DepartmentID string                          `json:"departmentId,omitempty"`
		Actor        *models.FormSubmissionSignature `json:"actor,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	if body.CreatedFrom == "" || body.CreatedTo == "" {
		config.ErrorStatus("createdFrom and createdTo are required", http.StatusBadRequest, w, fmt.Errorf("missing date range"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	now := primitive.NewDateTimeFromTime(time.Now())
	actor := h.resolveActor(ctx, r, body.Actor, now)
	if actor.UserID == "" {
		config.ErrorStatus("actor is required", http.StatusBadRequest, w, fmt.Errorf("missing actor"))
		return
	}

	from, ferr := time.Parse(time.RFC3339, body.CreatedFrom)
	to, terr := time.Parse(time.RFC3339, body.CreatedTo)
	if ferr != nil || terr != nil {
		config.ErrorStatus("createdFrom and createdTo must be RFC3339 timestamps", http.StatusBadRequest, w, fmt.Errorf("bad date format"))
		return
	}

	filter := bson.M{
		"formSubmission.communityID": communityID,
		"formSubmission.archived":    bson.M{"$ne": true},
		"formSubmission.createdAt": bson.M{
			"$gte": primitive.NewDateTimeFromTime(from),
			"$lte": primitive.NewDateTimeFromTime(to),
		},
	}
	if body.DepartmentID != "" {
		filter["formSubmission.departmentId"] = body.DepartmentID
	}

	const bulkArchiveCap = 1000
	count, err := h.DB.CountDocuments(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to count submissions", http.StatusInternalServerError, w, err)
		return
	}
	if count > bulkArchiveCap {
		config.ErrorStatus(fmt.Sprintf("too many submissions to archive in one call (%d > %d). Narrow the date range and try again.", count, bulkArchiveCap), http.StatusUnprocessableEntity, w, fmt.Errorf("over cap"))
		return
	}

	historyEntry := models.FormSubmissionHistoryEntry{
		Action:   "archived",
		UserID:   actor.UserID,
		Username: actor.Username,
		At:       now,
	}
	update := bson.M{
		"$set": bson.M{
			"formSubmission.archived":   true,
			"formSubmission.archivedBy": actor,
			"formSubmission.updatedAt":  now,
		},
		"$push": bson.M{"formSubmission.history": historyEntry},
	}
	modified, err := h.DB.UpdateMany(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to archive submissions", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":  "Submissions archived",
		"archived": modified,
	})
}

// PurgeFormSubmissionsHandler hard-deletes submissions by ID. Refuses
// unless EVERY target is already archived — preventing accidental
// destruction of unarchived audit data.
func (h FormSubmission) PurgeFormSubmissionsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["community_id"]
	if communityID == "" {
		config.ErrorStatus("communityID is required", http.StatusBadRequest, w, fmt.Errorf("missing communityID"))
		return
	}

	var body struct {
		IDs   []string                        `json:"ids"`
		Actor *models.FormSubmissionSignature `json:"actor,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	if len(body.IDs) == 0 {
		config.ErrorStatus("ids is required", http.StatusBadRequest, w, fmt.Errorf("missing ids"))
		return
	}
	if len(body.IDs) > 500 {
		config.ErrorStatus("too many ids in one call (max 500)", http.StatusUnprocessableEntity, w, fmt.Errorf("over cap"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	objIDs := make([]primitive.ObjectID, 0, len(body.IDs))
	for _, id := range body.IDs {
		oid, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			config.ErrorStatus(fmt.Sprintf("invalid submission id: %s", id), http.StatusBadRequest, w, err)
			return
		}
		objIDs = append(objIDs, oid)
	}

	// Refuse if any target is not archived in the requested community.
	notArchivedFilter := bson.M{
		"_id":                        bson.M{"$in": objIDs},
		"formSubmission.communityID": communityID,
		"formSubmission.archived":    bson.M{"$ne": true},
	}
	bad, err := h.DB.CountDocuments(ctx, notArchivedFilter)
	if err != nil {
		config.ErrorStatus("failed to verify archived state", http.StatusInternalServerError, w, err)
		return
	}
	if bad > 0 {
		config.ErrorStatus("some submissions are not archived; archive them first", http.StatusConflict, w, fmt.Errorf("%d unarchived in target set", bad))
		return
	}

	deleted, err := h.DB.DeleteMany(ctx, bson.M{
		"_id":                        bson.M{"$in": objIDs},
		"formSubmission.communityID": communityID,
		"formSubmission.archived":    true,
	})
	if err != nil {
		config.ErrorStatus("failed to purge submissions", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Submissions purged",
		"deleted": deleted,
	})
}

// --- helpers ---

// resolveActiveTemplate looks up either a stored template by (communityID,
// slug) or a built-in default. Returns the template ID (empty for
// defaults), its current sections, and its current version number.
func (h FormSubmission) resolveActiveTemplate(ctx context.Context, communityID, slug string) (string, []models.FormSection, int32, error) {
	tpl, err := h.TDB.FindOne(ctx, bson.M{
		"formTemplate.communityID": communityID,
		"formTemplate.slug":        slug,
		"formTemplate.isHidden":    bson.M{"$ne": true},
	})
	if err == nil && tpl != nil {
		v, vErr := h.VDB.FindOne(ctx, bson.M{
			"formTemplateVersion.formTemplateID": tpl.ID.Hex(),
			"formTemplateVersion.version":        tpl.Details.CurrentVersion,
		})
		if vErr == nil && v != nil {
			return tpl.ID.Hex(), v.Details.Sections, tpl.Details.CurrentVersion, nil
		}
		// Recovery: an older bug bumped formTemplate.currentVersion on
		// metadata-only updates (archive/unarchive) without writing a
		// matching version row. Without this fallback, a save against
		// such a template fails with 404 ("no documents in result").
		// Use the most recent version that does exist instead — same
		// strategy as formTemplate.fetchVersionSections.
		versions, lerr := h.VDB.Find(
			ctx,
			bson.M{"formTemplateVersion.formTemplateID": tpl.ID.Hex()},
			options.Find().SetSort(bson.M{"formTemplateVersion.version": -1}).SetLimit(1),
		)
		if lerr == nil && len(versions) > 0 {
			latest := versions[0]
			return tpl.ID.Hex(), latest.Details.Sections, latest.Details.Version, nil
		}
		// Last-ditch: if the slug matches a built-in default, fall through
		// to the defaults branch below so the user can still file a report.
	}

	// Fall back to built-in defaults.
	if def, ok := formdefaults.All()[slug]; ok {
		return "", def.Sections, def.CurrentVersion, nil
	}
	return "", nil, 0, fmt.Errorf("template %q not found for community %q", slug, communityID)
}

// maxReportNumberSeq returns the highest numeric tail across all
// reportNumbers used in this community for the given year, e.g.
// "RR-2026-000042" → 42. Used to seed a stale per-slug counter that
// trails the actually-used number space. Returns ok=false when no
// matching submissions exist.
func (h FormSubmission) maxReportNumberSeq(ctx context.Context, communityID string, year int) (int64, bool) {
	yearStr := strconv.Itoa(year)
	filter := bson.M{
		"formSubmission.communityID":  communityID,
		"formSubmission.reportNumber": bson.M{"$regex": "-" + yearStr + "-"},
	}
	opts := options.FindOne().SetSort(bson.M{"formSubmission.reportNumber": -1})
	top, err := h.DB.FindOne(ctx, filter, opts)
	if err != nil || top == nil {
		return 0, false
	}
	parts := strings.Split(top.Details.ReportNumber, "-")
	if len(parts) == 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// nextReportNumber atomically generates the next sequence and formats it
// per the template's NumberFormat. On failure it writes an error response
// and returns "".
func (h FormSubmission) nextReportNumber(ctx context.Context, communityID, slug string, w http.ResponseWriter, r *http.Request) string {
	year := time.Now().Year()
	seq, err := h.CDB.NextSeq(ctx, communityID, slug, year)
	if err != nil {
		config.ErrorStatus("failed to allocate report number", http.StatusInternalServerError, w, err)
		return ""
	}

	format := "RR-{YYYY}-{NNNNNN}"
	tpl, err := h.TDB.FindOne(ctx, bson.M{
		"formTemplate.communityID": communityID,
		"formTemplate.slug":        slug,
	})
	if err == nil && tpl != nil && tpl.Details.NumberFormat != "" {
		format = tpl.Details.NumberFormat
	} else if def, ok := formdefaults.All()[slug]; ok && def.NumberFormat != "" {
		format = def.NumberFormat
	}

	out := strings.ReplaceAll(format, "{YYYY}", strconv.Itoa(year))
	out = strings.ReplaceAll(out, "{NNNNNN}", fmt.Sprintf("%06d", seq))
	return out
}

// lookupSignedBy resolves the username for the auth-context user ID and
// returns a stamped signature struct.
//
// The users collection stores _id as ObjectID in Mongo (the Go model
// decodes it into a string field, but the stored type is still
// ObjectID). Querying by the raw string never matches, so we try the
// ObjectID-cast first and fall back to the string form for any legacy
// users whose _id was stored as a string.
func (h FormSubmission) lookupSignedBy(ctx context.Context, userID string, now primitive.DateTime) models.FormSubmissionSignature {
	sig := models.FormSubmissionSignature{UserID: userID, SignedAt: now}
	if userID == "" {
		return sig
	}
	var user models.User
	if oid, err := primitive.ObjectIDFromHex(userID); err == nil {
		if err := h.UDB.FindOne(ctx, bson.M{"_id": oid}).Decode(&user); err == nil {
			sig.Username = user.Details.Username
			return sig
		}
	}
	if err := h.UDB.FindOne(ctx, bson.M{"_id": userID}).Decode(&user); err == nil {
		sig.Username = user.Details.Username
	}
	return sig
}

// buildPrefill walks the template's top-level fields and resolves each
// field's PopulateFrom mappings against the fetched source entities, plus
// any DefaultExpr (auth/today/now) values. Repeatable section row
// pre-population is deferred to the client for v1 — only top-level scalar
// fields populate here.
func (h FormSubmission) buildPrefill(ctx context.Context, sections []models.FormSection, sources []SourceRef, r *http.Request) map[string]interface{} {
	prefill := map[string]interface{}{}

	sourceData := h.fetchSources(ctx, sources)
	authCtx := h.buildAuthContext(ctx, r)

	for _, section := range sections {
		if section.Repeatable {
			continue // top-level scalar fields only in v1
		}
		for _, field := range section.Fields {
			if v := resolveDefaultExpr(field.DefaultExpr, authCtx); v != nil {
				prefill[field.ID] = v
			}
			for _, m := range field.PopulateFrom {
				src, ok := sourceData[m.Source]
				if !ok {
					continue
				}
				if v := resolveJSONPath(src, m.Path); v != nil {
					prefill[field.ID] = v
					break
				}
			}
		}
	}
	return prefill
}

// fetchSources loads each source entity and returns a map keyed by source
// type. Only the first source of each type is honored in v1 (multiple
// citations would need v2 array-row expansion).
func (h FormSubmission) fetchSources(ctx context.Context, sources []SourceRef) map[string]interface{} {
	out := map[string]interface{}{}
	for _, s := range sources {
		if _, alreadySet := out[s.Type]; alreadySet {
			continue
		}
		switch s.Type {
		case "call":
			if call, err := h.CallDB.FindOne(ctx, bson.M{"_id": s.ID}); err == nil && call != nil {
				out["call"] = toMap(call.Details)
			}
		case "arrestReport":
			if oid, err := primitive.ObjectIDFromHex(s.ID); err == nil {
				if ar, err := h.ARDB.FindOne(ctx, bson.M{"_id": oid}); err == nil && ar != nil {
					out["arrestReport"] = toMap(ar.Details)
				}
			}
		case "civilian":
			if oid, err := primitive.ObjectIDFromHex(s.ID); err == nil {
				if civ, err := h.CivDB.FindOne(ctx, bson.M{"_id": oid}); err == nil && civ != nil {
					out["civilian"] = toMap(civ.Details)
				}
			}
		case "citation":
			// Citations are embedded in civilian.criminalHistory[]. The
			// client passes ID=<civilianID>, ChildID=<criminalHistoryID>;
			// we find the matching item and surface a flattened map plus
			// a nested "civilian" map for cross-field paths.
			if s.ChildID == "" {
				zap.S().Debugw("citation source missing childID", "id", s.ID)
				continue
			}
			civID, err := primitive.ObjectIDFromHex(s.ID)
			if err != nil {
				zap.S().Debugw("citation source invalid civilian id", "id", s.ID, "err", err)
				continue
			}
			civ, err := h.CivDB.FindOne(ctx, bson.M{"_id": civID})
			if err != nil || civ == nil {
				continue
			}
			childID, _ := primitive.ObjectIDFromHex(s.ChildID)
			var match *models.CriminalHistory
			for i := range civ.Details.CriminalHistory {
				if civ.Details.CriminalHistory[i].ID == childID {
					match = &civ.Details.CriminalHistory[i]
					break
				}
			}
			if match == nil {
				continue
			}
			cit := toMap(match)
			cit["civilian"] = toMap(civ.Details)
			out["citation"] = cit
		case "vehicle":
			if oid, err := primitive.ObjectIDFromHex(s.ID); err == nil {
				if veh, err := h.VehDB.FindOne(ctx, bson.M{"_id": oid}); err == nil && veh != nil {
					out["vehicle"] = toMap(veh.Details)
				}
			}
		case "firearm":
			if oid, err := primitive.ObjectIDFromHex(s.ID); err == nil {
				if firearm, err := h.FirDB.FindOne(ctx, bson.M{"_id": oid}); err == nil && firearm != nil {
					out["firearm"] = toMap(firearm.Details)
				}
			}
		}
	}
	return out
}

// buildAuthContext fetches the authenticated user (if any) and returns a
// map suitable for DefaultExpr resolution.
func (h FormSubmission) buildAuthContext(ctx context.Context, r *http.Request) map[string]interface{} {
	out := map[string]interface{}{}
	uid := api.GetAuthenticatedUserIDFromContext(r.Context())
	if uid == "" {
		return out
	}
	var user models.User
	found := false
	if oid, err := primitive.ObjectIDFromHex(uid); err == nil {
		if err := h.UDB.FindOne(ctx, bson.M{"_id": oid}).Decode(&user); err == nil {
			found = true
		}
	}
	if !found {
		if err := h.UDB.FindOne(ctx, bson.M{"_id": uid}).Decode(&user); err != nil {
			return out
		}
	}
	out["username"] = user.Details.Username
	return out
}

// resolveDefaultExpr returns a value for a built-in default expression.
//
// Supported: "today", "now", "auth.username".
func resolveDefaultExpr(expr string, authCtx map[string]interface{}) interface{} {
	switch expr {
	case "":
		return nil
	case "today":
		return time.Now().Format("2006-01-02")
	case "now":
		return time.Now().Format(time.RFC3339)
	case "auth.username":
		if v, ok := authCtx["username"]; ok {
			return v
		}
	}
	return nil
}

// resolveJSONPath walks a dot-separated path through nested maps and
// returns the leaf value, or nil when missing. Array indices and array
// member-projection are not yet supported.
func resolveJSONPath(root interface{}, path string) interface{} {
	if root == nil || path == "" {
		return nil
	}
	if strings.HasPrefix(path, "@const:") {
		return strings.TrimPrefix(path, "@const:")
	}
	parts := strings.Split(path, ".")
	cur := root
	for _, p := range parts {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		v, ok := m[p]
		if !ok {
			return nil
		}
		cur = v
	}
	return cur
}

// toMap JSON-roundtrips a struct into a map[string]interface{} for path traversal.
func toMap(v interface{}) map[string]interface{} {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out map[string]interface{}
	_ = json.Unmarshal(b, &out)
	return out
}

