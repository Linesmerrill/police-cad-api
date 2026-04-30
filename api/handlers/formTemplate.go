package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/api/handlers/formdefaults"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// FormTemplate exposes endpoints for managing community-scoped form templates.
type FormTemplate struct {
	DB  databases.FormTemplateDatabase
	VDB databases.FormTemplateVersionDatabase
	TDB databases.DepartmentFormToggleDatabase
}

// CreateFormTemplateHandler creates a new community-scoped form template
// plus its initial version row.
//
// Request body: a FormTemplateDetails plus a top-level `sections` field.
// The sections are stripped off and stored in formTemplateVersions; the
// metadata-only details land in formTemplates.
func (h FormTemplate) CreateFormTemplateHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		models.FormTemplateDetails
		Sections []models.FormSection `json:"sections"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	if body.CommunityID == "" {
		config.ErrorStatus("communityID is required", http.StatusBadRequest, w, fmt.Errorf("missing communityID"))
		return
	}
	if body.Slug == "" {
		config.ErrorStatus("slug is required", http.StatusBadRequest, w, fmt.Errorf("missing slug"))
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	tplID := primitive.NewObjectID()

	tpl := models.FormTemplate{
		ID: tplID,
		Details: models.FormTemplateDetails{
			CommunityID:      body.CommunityID,
			DepartmentID:     body.DepartmentID,
			Name:             body.Name,
			Slug:             body.Slug,
			Description:      body.Description,
			Icon:             body.Icon,
			CurrentVersion:   1,
			NumberFormat:     defaultIfBlank(body.NumberFormat, "RR-{YYYY}-{NNNNNN}"),
			VisibleToRoles:   body.VisibleToRoles,
			EditableByRoles:  body.EditableByRoles,
			LinkableEntities: body.LinkableEntities,
			IsHidden:         false,
			DefaultSlug:      "",
			IsArchived:       false,
			CreatedAt:        now,
			UpdatedAt:        now,
			CreatedBy:        api.GetAuthenticatedUserIDFromContext(r.Context()),
		},
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	if _, err := h.DB.InsertOne(ctx, tpl); err != nil {
		config.ErrorStatus("failed to create form template", http.StatusInternalServerError, w, err)
		return
	}

	version := models.FormTemplateVersion{
		ID: primitive.NewObjectID(),
		Details: models.FormTemplateVersionDetails{
			FormTemplateID: tplID.Hex(),
			CommunityID:    body.CommunityID,
			Slug:           body.Slug,
			Version:        1,
			Sections:       body.Sections,
			PublishedAt:    now,
			PublishedBy:    tpl.Details.CreatedBy,
		},
	}
	if _, err := h.VDB.InsertOne(ctx, version); err != nil {
		config.ErrorStatus("failed to create initial template version", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":      "Form template created",
		"id":           tplID.Hex(),
		"version":      1,
		"formTemplate": tpl,
	})
}

// FormTemplateByIDHandler returns a single template hydrated with its current version's sections.
func (h FormTemplate) FormTemplateByIDHandler(w http.ResponseWriter, r *http.Request) {
	tplID := mux.Vars(r)["template_id"]
	objID, err := primitive.ObjectIDFromHex(tplID)
	if err != nil {
		config.ErrorStatus("invalid template id", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	tpl, err := h.DB.FindOne(ctx, bson.M{"_id": objID})
	if err != nil {
		config.ErrorStatus("form template not found", http.StatusNotFound, w, err)
		return
	}

	view := storedTemplateToView(*tpl)
	if sections, err := h.fetchVersionSections(ctx, tplID, tpl.Details.CurrentVersion); err == nil {
		view.Sections = sections
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(view)
}

// UpdateFormTemplateHandler bumps the template's version and appends a new
// version row containing the new sections. Immediate publish — every save
// becomes the new current version.
func (h FormTemplate) UpdateFormTemplateHandler(w http.ResponseWriter, r *http.Request) {
	tplID := mux.Vars(r)["template_id"]
	objID, err := primitive.ObjectIDFromHex(tplID)
	if err != nil {
		config.ErrorStatus("invalid template id", http.StatusBadRequest, w, err)
		return
	}

	var body struct {
		Name             *string              `json:"name,omitempty"`
		Description      *string              `json:"description,omitempty"`
		Icon             *string              `json:"icon,omitempty"`
		NumberFormat     *string              `json:"numberFormat,omitempty"`
		VisibleToRoles   *[]string            `json:"visibleToRoles,omitempty"`
		EditableByRoles  *[]string            `json:"editableByRoles,omitempty"`
		LinkableEntities *[]string            `json:"linkableEntities,omitempty"`
		IsArchived       *bool                `json:"isArchived,omitempty"`
		Sections         []models.FormSection `json:"sections"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	existing, err := h.DB.FindOne(ctx, bson.M{"_id": objID})
	if err != nil {
		config.ErrorStatus("form template not found", http.StatusNotFound, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	// Only bump the version when sections actually change. Metadata-only
	// toggles (archive/unarchive, role updates) used to bump too, which
	// orphaned the version pointer — fetchVersionSections couldn't find
	// a row at the new number and rendered the template as 0 sections /
	// 0 fields. Versions are immutable section snapshots; flags don't
	// create new ones.
	bumpVersion := body.Sections != nil
	newVersion := existing.Details.CurrentVersion
	if bumpVersion {
		newVersion = existing.Details.CurrentVersion + 1
	}

	set := bson.M{
		"formTemplate.updatedAt": now,
	}
	if bumpVersion {
		set["formTemplate.currentVersion"] = newVersion
	}
	if body.Name != nil {
		set["formTemplate.name"] = *body.Name
	}
	if body.Description != nil {
		set["formTemplate.description"] = *body.Description
	}
	if body.Icon != nil {
		set["formTemplate.icon"] = *body.Icon
	}
	if body.NumberFormat != nil {
		set["formTemplate.numberFormat"] = *body.NumberFormat
	}
	if body.VisibleToRoles != nil {
		set["formTemplate.visibleToRoles"] = *body.VisibleToRoles
	}
	if body.EditableByRoles != nil {
		set["formTemplate.editableByRoles"] = *body.EditableByRoles
	}
	if body.LinkableEntities != nil {
		set["formTemplate.linkableEntities"] = *body.LinkableEntities
	}
	if body.IsArchived != nil {
		set["formTemplate.isArchived"] = *body.IsArchived
	}

	if err := h.DB.UpdateOne(ctx, bson.M{"_id": objID}, bson.M{"$set": set}); err != nil {
		config.ErrorStatus("failed to update form template", http.StatusInternalServerError, w, err)
		return
	}

	// Append new version row when sections were provided.
	if bumpVersion {
		version := models.FormTemplateVersion{
			ID: primitive.NewObjectID(),
			Details: models.FormTemplateVersionDetails{
				FormTemplateID: tplID,
				CommunityID:    existing.Details.CommunityID,
				Slug:           existing.Details.Slug,
				Version:        newVersion,
				Sections:       body.Sections,
				PublishedAt:    now,
				PublishedBy:    api.GetAuthenticatedUserIDFromContext(r.Context()),
			},
		}
		if _, err := h.VDB.InsertOne(ctx, version); err != nil {
			config.ErrorStatus("failed to append template version", http.StatusInternalServerError, w, err)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":        "Form template updated",
		"currentVersion": newVersion,
	})
}

// FormTemplateVersionsHandler lists all stored versions for a template (newest first).
func (h FormTemplate) FormTemplateVersionsHandler(w http.ResponseWriter, r *http.Request) {
	tplID := mux.Vars(r)["template_id"]

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	versions, err := h.VDB.Find(ctx, bson.M{"formTemplateVersion.formTemplateID": tplID}, options.Find().SetSort(bson.M{"formTemplateVersion.version": -1}))
	if err != nil {
		config.ErrorStatus("failed to fetch versions", http.StatusInternalServerError, w, err)
		return
	}
	if versions == nil {
		versions = []models.FormTemplateVersion{}
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(versions)
}

// HideDefaultFormTemplateHandler upserts a hide-marker row for a built-in
// default template in a community, or removes it (un-hide) when ?hidden=false.
func (h FormTemplate) HideDefaultFormTemplateHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["community_id"]
	slug := mux.Vars(r)["slug"]
	if _, ok := formdefaults.All()[slug]; !ok {
		config.ErrorStatus("unknown default template slug", http.StatusBadRequest, w, fmt.Errorf("slug %q is not a built-in default", slug))
		return
	}

	hidden := r.URL.Query().Get("hidden") != "false" // default true

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	now := primitive.NewDateTimeFromTime(time.Now())
	filter := bson.M{
		"formTemplate.communityID": communityID,
		"formTemplate.defaultSlug": slug,
	}
	if !hidden {
		// Un-hide by deleting the marker row entirely.
		if err := h.DB.DeleteOne(ctx, filter); err != nil {
			config.ErrorStatus("failed to remove hide marker", http.StatusInternalServerError, w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"message": "default template restored", "slug": slug, "hidden": false})
		return
	}

	update := bson.M{
		"$set": bson.M{
			"formTemplate.communityID": communityID,
			"formTemplate.slug":        slug,
			"formTemplate.defaultSlug": slug,
			"formTemplate.isHidden":    true,
			"formTemplate.updatedAt":   now,
		},
		"$setOnInsert": bson.M{
			"_id":                     primitive.NewObjectID(),
			"formTemplate.createdAt":  now,
			"formTemplate.createdBy":  api.GetAuthenticatedUserIDFromContext(r.Context()),
		},
		"$inc": bson.M{"__v": 0},
	}
	upsert := true
	if err := h.DB.UpdateOne(ctx, filter, update, &options.UpdateOptions{Upsert: &upsert}); err != nil {
		config.ErrorStatus("failed to hide default template", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"message": "default template hidden", "slug": slug, "hidden": true})
}

// FormTemplatesByCommunityHandlerV2 returns every template available to a
// community: stored rows merged with built-in defaults. Defaults the
// community has marked hidden are normally filtered out; ?includeHidden=true
// surfaces them with IsHidden=true so admin UIs can show a Restore action.
// Each row is hydrated with its current version's sections.
func (h FormTemplate) FormTemplatesByCommunityHandlerV2(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["community_id"]
	includeArchived := r.URL.Query().Get("includeArchived") == "true"
	includeHidden := r.URL.Query().Get("includeHidden") == "true"

	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 50
	}
	Page := getPage(0, r)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	stored, err := h.DB.Find(ctx, bson.M{"formTemplate.communityID": communityID})
	if err != nil {
		config.ErrorStatus("failed to fetch templates", http.StatusInternalServerError, w, err)
		return
	}

	views := h.mergeTemplatesAndDefaults(ctx, stored, includeArchived, includeHidden)

	// Pagination over the merged list.
	totalCount := int64(len(views))
	from := Page * Limit
	to := from + Limit
	if from > len(views) {
		from = len(views)
	}
	if to > len(views) {
		to = len(views)
	}
	pageSlice := views[from:to]

	totalPages := int(math.Ceil(float64(totalCount) / float64(Limit)))
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       pageSlice,
		"page":       Page,
		"limit":      Limit,
		"totalCount": totalCount,
		"totalPages": totalPages,
	})
}

// FormTemplatesByDepartmentHandlerV2 returns templates a specific
// department has enabled. Default behavior is "everything is enabled
// unless explicitly toggled off" — explicit isEnabled=false rows in
// departmentFormToggles suppress entries.
func (h FormTemplate) FormTemplatesByDepartmentHandlerV2(w http.ResponseWriter, r *http.Request) {
	deptID := mux.Vars(r)["dept_id"]
	communityID := r.URL.Query().Get("communityID")
	if communityID == "" {
		config.ErrorStatus("communityID query param is required", http.StatusBadRequest, w, fmt.Errorf("missing communityID"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	stored, err := h.DB.Find(ctx, bson.M{"formTemplate.communityID": communityID})
	if err != nil {
		config.ErrorStatus("failed to fetch templates", http.StatusInternalServerError, w, err)
		return
	}
	views := h.mergeTemplatesAndDefaults(ctx, stored, false, false)

	toggles, err := h.TDB.Find(ctx, bson.M{
		"departmentFormToggle.communityID":  communityID,
		"departmentFormToggle.departmentId": deptID,
	})
	if err != nil {
		config.ErrorStatus("failed to fetch toggles", http.StatusInternalServerError, w, err)
		return
	}
	disabled := map[string]bool{}
	for _, t := range toggles {
		if !t.Details.IsEnabled {
			disabled[t.Details.FormTemplateSlug] = true
		}
	}

	enabled := make([]models.FormTemplateView, 0, len(views))
	for _, v := range views {
		if !disabled[v.Slug] {
			enabled = append(enabled, v)
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       enabled,
		"totalCount": len(enabled),
	})
}

// --- helpers ---

// mergeTemplatesAndDefaults combines built-in defaults with stored rows
// for a community, suppressing defaults that have a hide-marker row
// (unless includeHidden is true, in which case hidden defaults are
// surfaced with IsHidden=true so admin UIs can offer a Restore action).
// Each custom row is hydrated with its current version's sections.
func (h FormTemplate) mergeTemplatesAndDefaults(ctx context.Context, stored []models.FormTemplate, includeArchived, includeHidden bool) []models.FormTemplateView {
	hiddenDefaults := map[string]bool{}
	customRows := make([]models.FormTemplate, 0, len(stored))
	for _, t := range stored {
		if t.Details.IsHidden && t.Details.DefaultSlug != "" {
			hiddenDefaults[t.Details.DefaultSlug] = true
			continue
		}
		if t.Details.IsArchived && !includeArchived {
			continue
		}
		customRows = append(customRows, t)
	}

	out := make([]models.FormTemplateView, 0, len(formdefaults.All())+len(customRows))

	// Built-in defaults first (stable order).
	for slug, def := range formdefaults.All() {
		if hiddenDefaults[slug] {
			if !includeHidden {
				continue
			}
			// Surface the hidden default with IsHidden=true set so admin
			// UIs can render a Restore action against it.
			hidden := def
			hidden.IsHidden = true
			out = append(out, hidden)
			continue
		}
		out = append(out, def)
	}

	// Stored rows, hydrated with their current-version sections.
	for _, t := range customRows {
		view := storedTemplateToView(t)
		if sections, err := h.fetchVersionSections(ctx, t.ID.Hex(), t.Details.CurrentVersion); err == nil {
			view.Sections = sections
		}
		out = append(out, view)
	}
	return out
}

func (h FormTemplate) fetchVersionSections(ctx context.Context, formTemplateID string, version int32) ([]models.FormSection, error) {
	v, err := h.VDB.FindOne(ctx, bson.M{
		"formTemplateVersion.formTemplateID": formTemplateID,
		"formTemplateVersion.version":        version,
	})
	if err == nil && v != nil {
		return v.Details.Sections, nil
	}
	// Fallback: an older bug bumped formTemplate.currentVersion on
	// metadata-only updates (archive/unarchive) without writing a matching
	// version row. Recover by returning the most recent version that does
	// exist for this template — better than rendering 0 sections.
	versions, lerr := h.VDB.Find(ctx,
		bson.M{"formTemplateVersion.formTemplateID": formTemplateID},
		options.Find().SetSort(bson.M{"formTemplateVersion.version": -1}).SetLimit(1),
	)
	if lerr == nil && len(versions) > 0 {
		return versions[0].Details.Sections, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, lerr
}

func storedTemplateToView(t models.FormTemplate) models.FormTemplateView {
	return models.FormTemplateView{
		ID:               t.ID.Hex(),
		CommunityID:      t.Details.CommunityID,
		DepartmentID:     t.Details.DepartmentID,
		Name:             t.Details.Name,
		Slug:             t.Details.Slug,
		Description:      t.Details.Description,
		Icon:             t.Details.Icon,
		CurrentVersion:   t.Details.CurrentVersion,
		NumberFormat:     t.Details.NumberFormat,
		VisibleToRoles:   t.Details.VisibleToRoles,
		EditableByRoles:  t.Details.EditableByRoles,
		LinkableEntities: t.Details.LinkableEntities,
		IsDefault:        false,
		IsHidden:         t.Details.IsHidden,
		IsArchived:       t.Details.IsArchived,
		CreatedAt:        t.Details.CreatedAt,
		UpdatedAt:        t.Details.UpdatedAt,
	}
}

func defaultIfBlank(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
