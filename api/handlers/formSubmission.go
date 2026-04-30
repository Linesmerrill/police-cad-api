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

	status := body.Status
	if status == "" {
		status = "submitted"
	}

	var history []models.FormSubmissionHistoryEntry
	if status == "submitted" {
		history = []models.FormSubmissionHistoryEntry{{
			Action:   "submitted",
			UserID:   signedBy.UserID,
			Username: signedBy.Username,
			At:       now,
		}}
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

	if _, err := h.DB.InsertOne(ctx, sub); err != nil {
		config.ErrorStatus("failed to create submission", http.StatusInternalServerError, w, err)
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

	// Authorization: a submitted report is locked. Any change (data,
	// links, reopen, etc.) requires the actor to be the original
	// signer or a community admin.
	if existing.Details.Status == "submitted" {
		if !h.canManageSubmission(ctx, actor.UserID, existing.Details) {
			config.ErrorStatus("only the report's author or a community admin can edit a submitted report", http.StatusForbidden, w, fmt.Errorf("forbidden"))
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

	var historyEntry *models.FormSubmissionHistoryEntry
	if body.Status != nil && *body.Status != existing.Details.Status {
		set["formSubmission.status"] = *body.Status
		switch *body.Status {
		case "draft":
			if existing.Details.Status == "submitted" {
				historyEntry = &models.FormSubmissionHistoryEntry{
					Action:   "reopened",
					UserID:   actor.UserID,
					Username: actor.Username,
					At:       now,
				}
			}
		case "submitted":
			action := "submitted"
			if len(existing.Details.History) > 0 {
				action = "resubmitted"
			}
			historyEntry = &models.FormSubmissionHistoryEntry{
				Action:   action,
				UserID:   actor.UserID,
				Username: actor.Username,
				At:       now,
			}
		}
	}

	update := bson.M{"$set": set}
	if historyEntry != nil {
		update["$push"] = bson.M{"formSubmission.history": *historyEntry}
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
		return h.lookupSignedBy(ctx, uid, now)
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
func (h FormSubmission) FormSubmissionsByCommunityHandlerV2(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["community_id"]
	templateSlug := r.URL.Query().Get("templateSlug")
	departmentID := r.URL.Query().Get("departmentID")
	officerID := r.URL.Query().Get("officerID")
	status := r.URL.Query().Get("status")

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
		if vErr != nil {
			return "", nil, 0, vErr
		}
		return tpl.ID.Hex(), v.Details.Sections, tpl.Details.CurrentVersion, nil
	}

	// Fall back to built-in defaults.
	if def, ok := formdefaults.All()[slug]; ok {
		return "", def.Sections, def.CurrentVersion, nil
	}
	return "", nil, 0, fmt.Errorf("template %q not found for community %q", slug, communityID)
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
func (h FormSubmission) lookupSignedBy(ctx context.Context, userID string, now primitive.DateTime) models.FormSubmissionSignature {
	sig := models.FormSubmissionSignature{UserID: userID, SignedAt: now}
	if userID == "" {
		return sig
	}
	var user models.User
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
	if err := h.UDB.FindOne(ctx, bson.M{"_id": uid}).Decode(&user); err != nil {
		return out
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

