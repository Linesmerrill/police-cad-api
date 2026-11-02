package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/models"
)

// Attachment metadata: a link to something that lives somewhere else.
//
// No binary upload, and that is the answer rather than a first step. Accepting
// third-party binaries means scanning them, storing them, paying for them,
// quotas, a takedown path and a legal position on content we host — and none of
// that is needed for the thing people actually want, which is a reviewer being
// able to click through to the clip the server already recorded.
//
// The harder line is on what an attachment may claim to be. `kind` is one of
// clip, screenshot, log, document or other, and **never evidence**. A link an
// integration supplies points at something we have not seen, held by a server
// we do not control. Calling it evidence inside a system that produces jail
// time would give it a standing it has not earned, and a reviewer can look at a
// clip and decide for themselves what it is. The same applies to a generated
// narrative: it is an account of events and the model's shape refuses to label
// it otherwise.

// Limits on what a draft or a call may carry. Bounded because a reviewer has to
// read this, so the limit is what a person can actually work through.
const (
	maxAttachmentsPerRecord = 10
	maxAttachmentURLLen     = 1024
	maxAttachmentLabelLen   = 120
	// maxAttachmentSizeBytes bounds the size an integration may *declare*.
	// We host nothing, so this is not a storage limit — it is a sanity check
	// on a number that goes on screen, so a typo cannot render "4 PB".
	maxAttachmentSizeBytes = int64(2 << 30)
)

type devAttachmentRequest struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	URL         string `json:"url"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
}

// DevAddDraftAttachmentHandler attaches metadata to a report draft.
//
// POST /api/v1/dev/communities/{communityId}/reports/drafts/{externalId}/attachments — reports:write
//
// Only while the draft is still a draft. Once a person has filed or rejected
// it, what they decided is the record, and a clip arriving afterwards must not
// change what they signed off.
func (d DeveloperAPI) DevAddDraftAttachmentHandler(w http.ResponseWriter, r *http.Request) {
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

	var req devAttachmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}

	attachment, err := buildAttachment(dc, req)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_attachment", err.Error(),
			map[string]interface{}{"kinds": models.ReportAttachmentKinds()})
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
	if !draft.IsOpenForWrites() {
		d.RecordOutcome(r, http.StatusConflict)
		devError(w, http.StatusConflict, "draft_already_reviewed",
			"A person has already reviewed that draft, so nothing more can be attached to it.",
			map[string]interface{}{"status": draft.Status})
		return
	}
	if len(draft.Attachments) >= maxAttachmentsPerRecord {
		d.RecordOutcome(r, http.StatusConflict)
		devError(w, http.StatusConflict, "attachment_limit_reached",
			fmt.Sprintf("A draft may carry %d attachments.", maxAttachmentsPerRecord),
			map[string]interface{}{"limit": maxAttachmentsPerRecord})
		return
	}
	// The same URL twice is almost always a retry, so it is answered with the
	// attachment that exists rather than adding a duplicate a reviewer would
	// have to look at twice.
	for _, existing := range draft.Attachments {
		if existing.URL == attachment.URL {
			d.RecordOutcome(r, http.StatusOK)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"attachment": existing, "added": false,
			})
			return
		}
	}

	updated, err := d.ReportDraftDB.UpdateOne(ctx, bson.M{"_id": devEq(draft.ID)}, bson.M{
		"$push": bson.M{"attachments": attachment},
		"$set":  bson.M{"updatedAt": primitive.NewDateTimeFromTime(time.Now())},
	})
	if err != nil || updated == nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not attach that.", nil)
		return
	}

	// Audited, because an attachment is a claim about what happened and a
	// reviewer may act on it. Who attached what, and when.
	logAudit(d.ALDB, cc.Community.ID, "report_draft.attachment_added", "developer_api",
		draft.ReportingOfficerID, draft.ReportingOfficerName, draft.ID.Hex(), attachment.Kind,
		map[string]interface{}{
			"keyId":        dc.Key.ID.Hex(),
			"externalId":   externalID,
			"attachmentId": attachment.ID,
			"url":          attachment.URL,
		})

	d.RecordOutcome(r, http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"attachment": attachment, "added": true,
	})
}

// DevRemoveDraftAttachmentHandler detaches one.
//
// DELETE /api/v1/dev/communities/{communityId}/reports/drafts/{externalId}/attachments/{attachmentId} — reports:write
func (d DeveloperAPI) DevRemoveDraftAttachmentHandler(w http.ResponseWriter, r *http.Request) {
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
	attachmentID, err := devSafeScalar(mux.Vars(r)["attachmentId"], 64, "attachmentId")
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_attachment_id", err.Error(), nil)
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
	if !draft.IsOpenForWrites() {
		d.RecordOutcome(r, http.StatusConflict)
		devError(w, http.StatusConflict, "draft_already_reviewed",
			"A person has already reviewed that draft, so its attachments are fixed.", nil)
		return
	}

	if _, err := d.ReportDraftDB.UpdateOne(ctx, bson.M{"_id": devEq(draft.ID)}, bson.M{
		"$pull": bson.M{"attachments": bson.M{"id": devEq(attachmentID)}},
		"$set":  bson.M{"updatedAt": primitive.NewDateTimeFromTime(time.Now())},
	}); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not detach that.", nil)
		return
	}

	logAudit(d.ALDB, cc.Community.ID, "report_draft.attachment_removed", "developer_api",
		draft.ReportingOfficerID, draft.ReportingOfficerName, draft.ID.Hex(), attachmentID,
		map[string]interface{}{"keyId": dc.Key.ID.Hex(), "externalId": externalID})

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"removed": true})
}

// DevAddCallAttachmentHandler attaches metadata to a dispatch call.
//
// POST /api/v1/dev/communities/{communityId}/dispatch/calls/{externalId}/attachments — calls:write
//
// On a call as well as a draft, because the clip usually exists while the call
// is still open and before anybody has written a report — which is the moment a
// dispatcher wants to see it.
func (d DeveloperAPI) DevAddCallAttachmentHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	externalID, source, err := normalizeExternalRef(mux.Vars(r)["externalId"],
		r.URL.Query().Get("source"), dc.Key.Name)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_external_id", err.Error(), nil)
		return
	}

	var req devAttachmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}
	attachment, err := buildAttachment(dc, req)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_attachment", err.Error(),
			map[string]interface{}{"kinds": models.ReportAttachmentKinds()})
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	call, err := d.findCallByExternalRef(ctx, cc.Community.ID.Hex(), source, externalID)
	if err != nil || call == nil {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "call_not_found",
			"No call in this community carries that external id.", nil)
		return
	}
	if len(call.Details.Attachments) >= maxAttachmentsPerRecord {
		d.RecordOutcome(r, http.StatusConflict)
		devError(w, http.StatusConflict, "attachment_limit_reached",
			fmt.Sprintf("A call may carry %d attachments.", maxAttachmentsPerRecord), nil)
		return
	}
	for _, existing := range call.Details.Attachments {
		if existing.URL == attachment.URL {
			d.RecordOutcome(r, http.StatusOK)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"attachment": existing, "added": false,
			})
			return
		}
	}

	if _, err := d.CallDB.UpdateOne(ctx, bson.M{"_id": devEq(call.ID)}, bson.M{
		"$push": bson.M{"call.attachments": attachment},
		"$set":  bson.M{"call.updatedAt": time.Now()},
	}); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not attach that.", nil)
		return
	}

	logAudit(d.ALDB, cc.Community.ID, "call.attachment_added", "developer_api",
		dc.Key.UserID, resolveActorName(d.UDB, dc.Key.UserID), call.ID, attachment.Kind,
		map[string]interface{}{
			"keyId":        dc.Key.ID.Hex(),
			"externalId":   externalID,
			"attachmentId": attachment.ID,
			"url":          attachment.URL,
		})

	d.RecordOutcome(r, http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"attachment": attachment, "added": true,
	})
}

// ---- validation ----

// buildAttachment validates and constructs one attachment.
//
// The URL has to be http or https and is rebuilt from the parsed value, like
// every other outside string on this surface. Note what is deliberately NOT
// done: we do not fetch it. We never follow a link an integration gives us, so
// the SSRF checks the webhooks need do not apply here — a URL on this record is
// something a person clicks in their own browser, not something we request.
func buildAttachment(dc *DevAPIContext, req devAttachmentRequest) (models.DeveloperReportAttachment, error) {
	out := models.DeveloperReportAttachment{}

	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		kind = models.ReportAttachmentKindOther
	}
	if strings.EqualFold(kind, "evidence") {
		// Named explicitly rather than falling through to "not a valid kind",
		// because an integration author who tries this deserves to be told why
		// rather than left guessing at our enum.
		return out, fmt.Errorf(
			"there is no \"evidence\" attachment kind. A link we have not seen is not evidence; " +
				"use clip, screenshot, log, document or other, and let a person decide what it is")
	}
	if !models.IsValidReportAttachmentKind(kind) {
		return out, fmt.Errorf("kind must be one of: %s",
			strings.Join(models.ReportAttachmentKinds(), ", "))
	}

	raw := strings.TrimSpace(req.URL)
	if raw == "" {
		return out, fmt.Errorf("url is required: this surface stores a link, not a file")
	}
	if len(raw) > maxAttachmentURLLen {
		return out, fmt.Errorf("url must be %d characters or fewer", maxAttachmentURLLen)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return out, fmt.Errorf("that is not a URL")
	}
	if !strings.EqualFold(parsed.Scheme, "https") && !strings.EqualFold(parsed.Scheme, "http") {
		return out, fmt.Errorf("an attachment url must be http or https")
	}
	if parsed.Host == "" {
		return out, fmt.Errorf("that URL has no host")
	}
	// Credentials in the URL would end up on a reviewer's screen and in the
	// audit log.
	if parsed.User != nil {
		return out, fmt.Errorf("do not put credentials in an attachment url")
	}

	if req.SizeBytes < 0 || req.SizeBytes > maxAttachmentSizeBytes {
		return out, fmt.Errorf("sizeBytes must be between 0 and %d", maxAttachmentSizeBytes)
	}

	label := strings.TrimSpace(req.Label)
	if len(label) > maxAttachmentLabelLen {
		label = strings.TrimSpace(label[:maxAttachmentLabelLen])
	}
	if label != "" {
		if _, err := devSafeScalar(label, maxAttachmentLabelLen, "label"); err != nil {
			return out, err
		}
	}
	contentType := strings.TrimSpace(req.ContentType)
	if len(contentType) > 120 {
		contentType = contentType[:120]
	}
	if contentType != "" {
		if _, err := devSafeScalar(contentType, 120, "contentType"); err != nil {
			return out, err
		}
	}

	rebuilt := url.URL{
		Scheme:   strings.ToLower(parsed.Scheme),
		Host:     parsed.Host,
		Path:     parsed.Path,
		RawQuery: parsed.RawQuery,
		Fragment: parsed.Fragment,
	}

	out = models.DeveloperReportAttachment{
		ID:          primitive.NewObjectID().Hex(),
		Kind:        kind,
		Label:       label,
		URL:         rebuilt.String(),
		ContentType: contentType,
		SizeBytes:   req.SizeBytes,
		AddedAt:     primitive.NewDateTimeFromTime(time.Now()),
	}
	if dc != nil {
		out.AddedByKey = dc.Key.ID.Hex()
	}
	return out, nil
}
