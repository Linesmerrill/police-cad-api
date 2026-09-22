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

// minReopenReasonLength matches the other free-text reasons: long enough that
// "x" is not accepted, short enough not to be a chore.
const minReopenReasonLength = 3

// AdminReopenReportHandler puts a report closed without action back in the
// queue, recording who did it and why.
//
// POST /api/v1/admin/reports/{reportId}/reopen
//
// A dismissal closes every report in the case, so this reopens every report
// that same decision closed, not just the one clicked. Reports dismissed before
// decisions were tracked reopen on their own.
func (ra ReportAdmin) AdminReopenReportHandler(w http.ResponseWriter, r *http.Request) {
	var req reportAdminRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	if !authorizeReportAdmin(w, r, req.CurrentUser) {
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if len([]rune(reason)) < minReopenReasonLength {
		config.ErrorStatus("a reason is required to reopen a report", http.StatusBadRequest, w,
			fmt.Errorf("reason must be at least %d characters", minReopenReasonLength))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	report, err := ra.findReport(ctx, mux.Vars(r)["reportId"])
	if err != nil {
		config.InfoStatus("report not found", http.StatusNotFound, w, err)
		return
	}

	if !report.IsReopenable() {
		msg := "this report cannot be reopened"
		switch report.EffectiveStatus() {
		case models.ReportStatusResolved:
			msg = "this report was upheld; reverse the strike instead of reopening it"
		case models.ReportStatusEscalated:
			msg = "escalated reports cannot be reopened"
		case models.ReportStatusNew, models.ReportStatusUnderReview:
			msg = "this report is already open"
		}
		config.ErrorStatus(msg, http.StatusConflict, w, fmt.Errorf("status is %s", report.EffectiveStatus()))
		return
	}

	reopening, err := ra.closedTogether(ctx, *report)
	if err != nil {
		config.ErrorStatus("failed to load the case", http.StatusInternalServerError, w, err)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	admin := adminDisplayName(req.CurrentUser)
	for _, rep := range reopening {
		event := models.ReportEvent{
			Action:         models.ReportEventReopened,
			PreviousStatus: rep.EffectiveStatus(),
			By:             admin,
			ByID:           adminID(req.CurrentUser),
			Reason:         reason,
			DecisionID:     rep.DecisionID,
			At:             now,
		}
		// The decision fields describe the latest decision, and there no longer
		// is one. What was decided, by whom, stays in the history.
		update := bson.M{
			"$set": bson.M{
				"status":    models.ReportStatusNew,
				"active":    true,
				"updatedAt": now,
			},
			"$unset": bson.M{
				"reviewedByName": "",
				"reviewedById":   "",
				"reviewedAt":     "",
				"decisionId":     "",
			},
			"$push": bson.M{"history": event},
		}
		if err := ra.RDB.UpdateOne(ctx, bson.M{"_id": rep.ID, "status": rep.EffectiveStatus()}, update); err != nil {
			zap.S().Errorw("failed to reopen report", "reportId", rep.ID.Hex(), "error", err)
			config.ErrorStatus("failed to reopen the report", http.StatusInternalServerError, w, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":     "report reopened",
		"reportCount": len(reopening),
	})
}

// closedTogether returns the reports the same decision closed, still in the
// state it left them. A report later decided again by something else is not
// swept back in.
func (ra ReportAdmin) closedTogether(ctx context.Context, report models.Report) ([]models.Report, error) {
	if report.DecisionID == "" {
		return []models.Report{report}, nil
	}
	cursor, err := ra.RDB.Find(ctx, bson.M{
		"decisionId": report.DecisionID,
		"status":     report.EffectiveStatus(),
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var reports []models.Report
	if err := cursor.All(ctx, &reports); err != nil {
		return nil, err
	}
	for _, rep := range reports {
		if rep.ID == report.ID {
			return reports, nil
		}
	}
	return append([]models.Report{report}, reports...), nil
}
