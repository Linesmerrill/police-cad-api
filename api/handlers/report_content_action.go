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

	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Taking down the reported content.
//
// Upholding a report puts a strike on the author, but until now it left what
// they wrote on screen. This removes the specific fields that were reported,
// and nothing else: a slur in a description does not justify wiping a
// community's events.
//
// The snapshot is not touched. It is the record of what was there, and the
// evidence for the strike and any appeal.

// AdminRemoveReportedContentHandler clears the reported fields.
//
// POST /api/v1/admin/reports/{reportId}/remove-content
func (ra ReportAdmin) AdminRemoveReportedContentHandler(w http.ResponseWriter, r *http.Request) {
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
	if report.Target == nil {
		config.ErrorStatus("this report does not point at any content", http.StatusBadRequest, w,
			fmt.Errorf("report %s has no target", report.ID.Hex()))
		return
	}
	if ra.DBHelper == nil {
		config.ErrorStatus("content removal is not available", http.StatusInternalServerError, w,
			fmt.Errorf("no database handle"))
		return
	}

	removed, err := removeReportedContent(ctx, ra.DBHelper, *report.Target)
	if err != nil {
		config.ErrorStatus(err.Error(), http.StatusBadRequest, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	admin := adminDisplayName(req.CurrentUser)
	event := models.ReportEvent{
		Action:         reportEventContentRemoved,
		PreviousStatus: report.EffectiveStatus(),
		By:             admin,
		ByID:           adminID(req.CurrentUser),
		Reason:         strings.TrimSpace(req.Reason),
		At:             now,
	}
	if err := ra.RDB.UpdateOne(ctx, bson.M{"_id": report.ID},
		bson.M{"$set": bson.M{"contentRemovedAt": now, "updatedAt": now}, "$push": bson.M{"history": event}}); err != nil {
		zap.S().Errorw("content removed but the report was not updated", "reportId", report.ID.Hex(), "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "content removed",
		"fields":  removed,
	})
}

// reportEventContentRemoved is the history action for a takedown.
const reportEventContentRemoved = "content_removed"

// removeReportedContent clears the reported fields and returns what it
// cleared. Only fields in the registry can be named, so this can never be
// pointed at an owner id or a subscription.
func removeReportedContent(ctx context.Context, db databases.DatabaseHelper, target models.ReportTarget) ([]string, error) {
	kind, ok := models.LookupReportableKind(target.Kind)
	if !ok {
		return nil, fmt.Errorf("%s is not something that can be reported", target.Kind)
	}
	fields := target.Fields
	if len(fields) == 0 {
		// The whole thing was reported. Clearing every field of a community
		// profile at once is not something a single click should do, so staff
		// pick the fields on the report first.
		return nil, fmt.Errorf("choose which parts to remove")
	}
	if err := kind.ValidateFields(fields); err != nil {
		return nil, err
	}

	switch kind.Kind {
	case models.TargetCommunity:
		return clearFields(ctx, db, "communities", target.ID, "community.", fields)
	case models.TargetUserProfile:
		return clearFields(ctx, db, "users", target.ID, "user.", fields)
	case models.TargetAnnouncement:
		return clearFields(ctx, db, "announcements", target.ID, "", fields)
	case models.TargetAnnouncementComment:
		return clearArrayFields(ctx, db, "announcements", target.ParentID, "comments", target.ID, fields)
	case models.TargetCommunityEvent:
		return clearArrayFields(ctx, db, "communities", target.ParentID, "community.events", target.ID, fields)
	case models.TargetFeatureRequest:
		return clearFields(ctx, db, "featureRequests", target.ID, "", fields)
	case models.TargetFeatureRequestReply:
		return clearArrayFields(ctx, db, "featureRequests", target.ParentID, "comments", target.ID, fields)
	case models.TargetCivilian, models.TargetVehicle, models.TargetFirearm:
		// Blanking the name or the photo leaves the record itself intact. The
		// character keeps its history and its owner can rename it; we are
		// removing the wording, not deleting somebody's roleplay.
		spec := roleplayCollections[kind.Kind]
		return clearFields(ctx, db, spec.collection, target.ID, spec.wrapper+".", fields)
	default:
		// Promotions are taken down through the Server Promos panel, which
		// also deletes the Discord message. Creator profiles are removed
		// through the Content Creators panel.
		return nil, fmt.Errorf("remove this through the %s panel", kind.Label)
	}
}

// clearFields blanks top-level fields on one document.
func clearFields(ctx context.Context, db databases.DatabaseHelper, collection, id, prefix string, fields []string) ([]string, error) {
	oid, err := objectID(id)
	if err != nil {
		return nil, errTargetGone
	}
	set := bson.M{}
	for _, f := range fields {
		set[prefix+f] = ""
	}
	if _, err := db.Collection(collection).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": set}); err != nil {
		return nil, fmt.Errorf("could not remove that content")
	}
	return fields, nil
}

// clearArrayFields blanks fields on one element of an array, matched by its id.
func clearArrayFields(ctx context.Context, db databases.DatabaseHelper, collection, parentID, arrayPath, elementID string, fields []string) ([]string, error) {
	parentOID, err := objectID(parentID)
	if err != nil {
		return nil, errTargetGone
	}
	elementOID, err := objectID(elementID)
	if err != nil {
		return nil, errTargetGone
	}
	set := bson.M{}
	for _, f := range fields {
		set[arrayPath+".$[el]."+f] = ""
	}
	opts := options.Update().SetArrayFilters(options.ArrayFilters{
		Filters: []interface{}{bson.M{"el._id": elementOID}},
	})
	if _, err := db.Collection(collection).UpdateOne(ctx, bson.M{"_id": parentOID}, bson.M{"$set": set}, opts); err != nil {
		return nil, fmt.Errorf("could not remove that content")
	}
	return fields, nil
}
