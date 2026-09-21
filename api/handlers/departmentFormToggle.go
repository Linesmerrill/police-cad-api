package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// DepartmentFormToggle handles enable/disable of templates per department.
type DepartmentFormToggle struct {
	DB     databases.DepartmentFormToggleDatabase
	CommDB databases.CommunityDatabase
}

// SetDepartmentFormToggleHandler upserts the department's enable/disable
// state for a given template slug. Body: { communityID, isEnabled }.
//
// We only persist a row when the department needs to override the
// implicit default (everything enabled). When isEnabled=true, the row is
// removed so the implicit default takes over again.
func (h DepartmentFormToggle) SetDepartmentFormToggleHandler(w http.ResponseWriter, r *http.Request) {
	deptID := mux.Vars(r)["dept_id"]
	slug := mux.Vars(r)["slug"]

	var body struct {
		CommunityID string `json:"communityID"`
		IsEnabled   bool   `json:"isEnabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	if body.CommunityID == "" {
		config.ErrorStatus("communityID is required", http.StatusBadRequest, w, fmt.Errorf("missing communityID"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Turning a form on or off for a department is administering the community's
	// forms, so it needs the same permission as editing one.
	if h.CommDB != nil {
		commID, err := primitive.ObjectIDFromHex(body.CommunityID)
		if err != nil {
			config.ErrorStatus("invalid community id", http.StatusBadRequest, w, err)
			return
		}
		community, err := h.CommDB.FindOne(ctx, bson.M{"_id": commID})
		if err != nil || community == nil {
			config.ErrorStatus("community not found", http.StatusNotFound, w, err)
			return
		}
		if !authorizeCommunityAction(w, r, community, models.PermissionManageForms) {
			return
		}
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	filter := bson.M{
		"departmentFormToggle.communityID":      body.CommunityID,
		"departmentFormToggle.departmentId":     deptID,
		"departmentFormToggle.formTemplateSlug": slug,
	}
	update := bson.M{
		"$set": bson.M{
			"departmentFormToggle.communityID":      body.CommunityID,
			"departmentFormToggle.departmentId":     deptID,
			"departmentFormToggle.formTemplateSlug": slug,
			"departmentFormToggle.isEnabled":        body.IsEnabled,
			"departmentFormToggle.updatedAt":        now,
			"departmentFormToggle.updatedBy":        api.GetAuthenticatedUserIDFromContext(r.Context()),
		},
		"$setOnInsert": bson.M{"_id": primitive.NewObjectID()},
	}
	upsert := true
	if err := h.DB.UpdateOne(ctx, filter, update, &options.UpdateOptions{Upsert: &upsert}); err != nil {
		config.ErrorStatus("failed to update department toggle", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "department form toggle updated",
		"slug":      slug,
		"isEnabled": body.IsEnabled,
	})
}
