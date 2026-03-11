package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// getDepartmentType returns the lowercase department type from the embedded template name.
func getDepartmentType(dept *models.Department) string {
	if dept.Template.Name != "" {
		return strings.ToLower(dept.Template.Name)
	}
	return ""
}

// ---------- helpers ----------

// findDepartment returns the index and a pointer to the department within the community, or -1 if not found.
func findDepartment(community *models.Community, departmentID string) (int, *models.Department) {
	deptObjID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		return -1, nil
	}
	for i := range community.Details.Departments {
		if community.Details.Departments[i].ID == deptObjID {
			return i, &community.Details.Departments[i]
		}
	}
	return -1, nil
}

// ---------- Rank CRUD ----------

// GetRanksHandler returns the ranks for a department, sorted by displayOrder.
// GET /api/v1/community/{communityId}/departments/{departmentId}/ranks
func (c Community) GetRanksHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	_, dept := findDepartment(community, departmentID)
	if dept == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, fmt.Errorf("department %s not found", departmentID))
		return
	}

	ranks := dept.Ranks
	if ranks == nil {
		ranks = []models.Rank{}
	}
	sort.Slice(ranks, func(i, j int) bool { return ranks[i].DisplayOrder < ranks[j].DisplayOrder })

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ranks": ranks,
	})
}

// CreateRankHandler creates a new rank in a department.
// POST /api/v1/community/{communityId}/departments/{departmentId}/ranks
func (c Community) CreateRankHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	var rank models.Rank
	if err := json.NewDecoder(r.Body).Decode(&rank); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	rank.ID = primitive.NewObjectID()
	if rank.Requirements == nil {
		rank.Requirements = []models.RankRequirement{}
	}
	rank.Requirements = ensureRequirementIDs(rank.Requirements)

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}
	deptObjID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		config.ErrorStatus("invalid department ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Find the community to compute displayOrder
	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	deptIdx, dept := findDepartment(community, departmentID)
	if dept == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, fmt.Errorf("department not found"))
		return
	}

	// Auto-assign displayOrder to end of list
	rank.DisplayOrder = len(dept.Ranks)

	// Default canViewStats to true for new ranks
	rank.CanViewStats = true

	// If this rank is default, clear isDefault on existing ranks first
	if rank.IsDefault && len(dept.Ranks) > 0 {
		clearFields := bson.M{}
		for i, rk := range dept.Ranks {
			if rk.IsDefault {
				clearPath := fmt.Sprintf("community.departments.%d.ranks.%d.isDefault", deptIdx, i)
				clearFields[clearPath] = false
			}
		}
		if len(clearFields) > 0 {
			_ = c.DB.UpdateOne(ctx, bson.M{"_id": cID}, bson.M{"$set": clearFields})
		}
	}

	// Push rank into the department's ranks array using positional filter
	filter := bson.M{"_id": cID, "community.departments._id": deptObjID}
	update := bson.M{"$push": bson.M{"community.departments.$.ranks": rank}}
	err = c.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to create rank", http.StatusInternalServerError, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "rank.created", "rank", actorID, resolveActorName(c.UDB, actorID), rank.ID.Hex(), rank.Name, map[string]interface{}{
		"departmentId": departmentID,
	})

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Rank created successfully",
		"rank":    rank,
	})
}

// UpdateRankHandler updates a rank's name, prefix, requirements, autoPromote, or canViewStats.
// PUT /api/v1/community/{communityId}/departments/{departmentId}/ranks/{rankId}
func (c Community) UpdateRankHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]
	rankID := mux.Vars(r)["rankId"]

	var updatedFields map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updatedFields); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Fetch community to find the department index and rank
	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	deptIdx, dept := findDepartment(community, departmentID)
	if dept == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, fmt.Errorf("department not found"))
		return
	}

	rankIdx := -1
	for i, rk := range dept.Ranks {
		if rk.ID.Hex() == rankID {
			rankIdx = i
			break
		}
	}
	if rankIdx == -1 {
		config.ErrorStatus("rank not found", http.StatusNotFound, w, fmt.Errorf("rank %s not found", rankID))
		return
	}

	// Build update map for allowed fields
	setFields := bson.M{}
	prefix := fmt.Sprintf("community.departments.%d.ranks.%d.", deptIdx, rankIdx)

	allowedFields := map[string]string{
		"name":         prefix + "name",
		"prefix":       prefix + "prefix",
		"requirements": prefix + "requirements",
		"autoPromote":  prefix + "autoPromote",
		"canViewStats": prefix + "canViewStats",
		"isDefault":    prefix + "isDefault",
	}

	for field, bsonPath := range allowedFields {
		if val, ok := updatedFields[field]; ok {
			setFields[bsonPath] = val
		}
	}

	// Ensure requirement IDs are assigned for new/existing requirements
	reqsPath := prefix + "requirements"
	if rawReqs, ok := setFields[reqsPath]; ok {
		// Re-marshal and unmarshal to typed slice so we can assign IDs
		reqBytes, _ := json.Marshal(rawReqs)
		var reqs []models.RankRequirement
		if json.Unmarshal(reqBytes, &reqs) == nil {
			reqs = ensureRequirementIDs(reqs)
			setFields[reqsPath] = reqs
		}
	}

	if len(setFields) == 0 {
		config.ErrorStatus("no valid fields to update", http.StatusBadRequest, w, fmt.Errorf("no valid fields"))
		return
	}

	// If setting isDefault=true, clear isDefault on all other ranks in this department first
	if isDefault, ok := updatedFields["isDefault"]; ok {
		if def, isBool := isDefault.(bool); isBool && def {
			for i, rk := range dept.Ranks {
				if rk.ID.Hex() != rankID && rk.IsDefault {
					clearPath := fmt.Sprintf("community.departments.%d.ranks.%d.isDefault", deptIdx, i)
					setFields[clearPath] = false
				}
			}
		}
	}

	filter := bson.M{"_id": cID}
	update := bson.M{"$set": setFields}
	err = c.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to update rank", http.StatusInternalServerError, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "rank.updated", "rank", actorID, resolveActorName(c.UDB, actorID), rankID, "", map[string]interface{}{
		"departmentId": departmentID,
	})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Rank updated successfully",
	})
}

// DeleteRankHandler removes a rank from a department and clears it from any members.
// DELETE /api/v1/community/{communityId}/departments/{departmentId}/ranks/{rankId}
func (c Community) DeleteRankHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]
	rankID := mux.Vars(r)["rankId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}
	deptObjID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		config.ErrorStatus("invalid department ID", http.StatusBadRequest, w, err)
		return
	}
	rankObjID, err := primitive.ObjectIDFromHex(rankID)
	if err != nil {
		config.ErrorStatus("invalid rank ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Fetch community to find affected members
	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	_, dept := findDepartment(community, departmentID)
	if dept == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, fmt.Errorf("department not found"))
		return
	}

	// Clear rankId from any department members that have this rank
	deptIdx := -1
	for i := range community.Details.Departments {
		if community.Details.Departments[i].ID == deptObjID {
			deptIdx = i
			break
		}
	}

	clearFields := bson.M{}
	for i, member := range dept.Members {
		if member.RankID == rankID {
			path := fmt.Sprintf("community.departments.%d.members.%d.rankId", deptIdx, i)
			clearFields[path] = ""
			pathAssigned := fmt.Sprintf("community.departments.%d.members.%d.rankAssignedAt", deptIdx, i)
			clearFields[pathAssigned] = primitive.DateTime(0)
			pathType := fmt.Sprintf("community.departments.%d.members.%d.rankAssignmentType", deptIdx, i)
			clearFields[pathType] = ""
		}
	}

	// Pull the rank from the department's ranks array
	filter := bson.M{"_id": cID, "community.departments._id": deptObjID}
	update := bson.M{"$pull": bson.M{"community.departments.$.ranks": bson.M{"_id": rankObjID}}}

	// If we also need to clear member ranks, do that in a separate update
	err = c.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to delete rank", http.StatusInternalServerError, w, err)
		return
	}

	if len(clearFields) > 0 {
		err = c.DB.UpdateOne(ctx, bson.M{"_id": cID}, bson.M{"$set": clearFields})
		if err != nil {
			config.ErrorStatus("failed to clear rank from members", http.StatusInternalServerError, w, err)
			return
		}
	}

	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "rank.deleted", "rank", actorID, resolveActorName(c.UDB, actorID), rankID, "", map[string]interface{}{
		"departmentId": departmentID,
	})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Rank deleted successfully",
	})
}

// ReorderRanksHandler reorders ranks within a department.
// PUT /api/v1/community/{communityId}/departments/{departmentId}/ranks/reorder
func (c Community) ReorderRanksHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	var requestBody struct {
		RankIDs []string `json:"rankIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if len(requestBody.RankIDs) == 0 {
		config.ErrorStatus("rankIds array is required", http.StatusBadRequest, w, fmt.Errorf("empty rankIds"))
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	deptIdx, dept := findDepartment(community, departmentID)
	if dept == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, fmt.Errorf("department not found"))
		return
	}

	existingRanks := dept.Ranks
	if len(requestBody.RankIDs) != len(existingRanks) {
		config.ErrorStatus("rankIds count does not match existing ranks count", http.StatusBadRequest, w,
			fmt.Errorf("expected %d, got %d", len(existingRanks), len(requestBody.RankIDs)))
		return
	}

	rankMap := make(map[string]models.Rank, len(existingRanks))
	for _, rk := range existingRanks {
		rankMap[rk.ID.Hex()] = rk
	}

	seen := make(map[string]bool, len(requestBody.RankIDs))
	reordered := make([]models.Rank, 0, len(requestBody.RankIDs))
	for i, id := range requestBody.RankIDs {
		rk, exists := rankMap[id]
		if !exists {
			config.ErrorStatus(fmt.Sprintf("rank ID %s not found", id), http.StatusBadRequest, w, fmt.Errorf("unknown rank ID"))
			return
		}
		if seen[id] {
			config.ErrorStatus(fmt.Sprintf("duplicate rank ID %s", id), http.StatusBadRequest, w, fmt.Errorf("duplicate rank ID"))
			return
		}
		seen[id] = true
		rk.DisplayOrder = i
		reordered = append(reordered, rk)
	}

	path := fmt.Sprintf("community.departments.%d.ranks", deptIdx)
	filter := bson.M{"_id": cID}
	update := bson.M{"$set": bson.M{path: reordered}}
	err = c.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to reorder ranks", http.StatusInternalServerError, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "rank.reordered", "rank", actorID, resolveActorName(c.UDB, actorID), "", "", map[string]interface{}{
		"departmentId": departmentID,
	})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Ranks reordered successfully",
	})
}

// GetMetricTypesHandler returns the list of available metric types for rank requirements.
// GET /api/v1/community/{communityId}/ranks/metric-types
func (c Community) GetMetricTypesHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"metricTypes": models.MetricTypeRegistry,
	})
}

// GetDepartmentMetricTypesHandler returns metric types filtered for a specific department's type.
// GET /api/v1/community/{communityId}/departments/{departmentId}/ranks/metric-types
func (c Community) GetDepartmentMetricTypesHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	_, dept := findDepartment(community, departmentID)
	if dept == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, fmt.Errorf("department not found"))
		return
	}

	deptType := getDepartmentType(dept)
	var metricTypes []models.MetricTypeDef
	if deptType != "" {
		metricTypes = models.MetricTypesForDepartment(deptType)
	}
	// If no metrics match (e.g. civilian), return empty list — don't fall back to all
	if metricTypes == nil {
		metricTypes = []models.MetricTypeDef{}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"metricTypes":    metricTypes,
		"departmentType": deptType,
	})
}

// ---------- Officer Stats & Rank Progress ----------

// OfficerMetric represents one metric's current value for an officer
type OfficerMetric struct {
	MetricType   string `json:"metricType"`
	DisplayName  string `json:"displayName"`
	CurrentValue int    `json:"currentValue"`
}

// RankProgress shows progress toward a single requirement
type RankProgress struct {
	MetricType    string  `json:"metricType"`
	DisplayName   string  `json:"displayName"`
	CurrentValue  int     `json:"currentValue"`
	Threshold     int     `json:"threshold"`
	Percentage    float64 `json:"percentage"`
	Met           bool    `json:"met"`
	IsCustom      bool    `json:"isCustom,omitempty"`
	CustomLabel   string  `json:"customLabel,omitempty"`
	RequirementID string  `json:"requirementId,omitempty"`
}

// containsString checks if a string slice contains a value.
func containsString(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// ensureRequirementIDs assigns IDs to any requirements that don't have one.
func ensureRequirementIDs(reqs []models.RankRequirement) []models.RankRequirement {
	for i := range reqs {
		if reqs[i].ID == "" {
			reqs[i].ID = primitive.NewObjectID().Hex()
		}
	}
	return reqs
}

// checkAllRequirementsMet checks if all requirements (tracked + custom) are met for a member.
func checkAllRequirementsMet(reqs []models.RankRequirement, metricsMap map[string]int, customMet []string) bool {
	for _, req := range reqs {
		if req.MetricType == "custom" {
			if !containsString(customMet, req.ID) {
				return false
			}
		} else {
			if metricsMap[req.MetricType] < req.Threshold {
				return false
			}
		}
	}
	return true
}

// buildRequirementProgress builds RankProgress entries for a set of requirements.
func buildRequirementProgress(reqs []models.RankRequirement, metricsMap map[string]int, customMet []string) []RankProgress {
	var progress []RankProgress
	for _, req := range reqs {
		if req.MetricType == "custom" {
			met := containsString(customMet, req.ID)
			currentVal := 0
			pct := float64(0)
			if met {
				currentVal = 1
				pct = 1.0
			}
			progress = append(progress, RankProgress{
				MetricType:    "custom",
				DisplayName:   req.CustomLabel,
				CustomLabel:   req.CustomLabel,
				IsCustom:      true,
				RequirementID: req.ID,
				CurrentValue:  currentVal,
				Threshold:     1,
				Percentage:    pct,
				Met:           met,
			})
		} else {
			current := metricsMap[req.MetricType]
			pct := float64(0)
			if req.Threshold > 0 {
				pct = float64(current) / float64(req.Threshold)
				if pct > 1.0 {
					pct = 1.0
				}
			}
			progress = append(progress, RankProgress{
				MetricType:   req.MetricType,
				DisplayName:  models.MetricTypeDisplayNames[req.MetricType],
				CurrentValue: current,
				Threshold:    req.Threshold,
				Percentage:   pct,
				Met:          current >= req.Threshold,
			})
		}
	}
	return progress
}

// computeOfficerMetrics aggregates metric values for a given officer in a department.
// deptType filters which pipelines to run (e.g. "police", "ems", "fire", "dispatch", "judicial").
// If deptType is empty, all pipelines run (backward compat).
func (c Community) computeOfficerMetrics(ctx context.Context, communityID, departmentID, userID, deptType string) (map[string]int, error) {
	metrics := make(map[string]int)

	// Build set of relevant metric types for this department
	relevant := make(map[string]bool)
	if deptType != "" {
		for _, mt := range models.MetricTypesForDepartment(deptType) {
			relevant[mt.Type] = true
		}
	}
	needMetric := func(metricType string) bool {
		if deptType == "" {
			return true // no filter, run all
		}
		return relevant[metricType]
	}

	// Citations, Warnings, Arrests — from civilians collection
	for _, entry := range []struct {
		metricType   string
		crimHistType string
	}{
		{"citations_issued", "Citation"},
		{"warnings_issued", "Warning"},
		{"arrests_made", "Arrest"},
	} {
		if !needMetric(entry.metricType) {
			continue
		}
		pipeline := bson.A{
			bson.M{"$match": bson.M{"civilian.activeCommunityID": communityID}},
			bson.M{"$unwind": "$civilian.criminalHistory"},
			bson.M{"$match": bson.M{
				"civilian.criminalHistory.officerID":    userID,
				"civilian.criminalHistory.type":         entry.crimHistType,
				"civilian.criminalHistory.departmentId": departmentID,
			}},
			bson.M{"$count": "total"},
		}
		count, err := c.runCountPipeline(ctx, "civilians", pipeline)
		if err != nil {
			return nil, err
		}
		metrics[entry.metricType] = count
	}

	// Calls Created
	if needMetric("calls_created") {
		count, err := c.runCountPipeline(ctx, "calls", bson.A{
			bson.M{"$match": bson.M{
				"call.communityID": communityID,
				"call.createdByID": userID,
				"call.departments": departmentID,
			}},
			bson.M{"$count": "total"},
		})
		if err != nil {
			return nil, err
		}
		metrics["calls_created"] = count
	}

	// Calls Responded
	if needMetric("calls_responded") {
		count, err := c.runCountPipeline(ctx, "calls", bson.A{
			bson.M{"$match": bson.M{
				"call.communityID": communityID,
				"call.assignedTo":  userID,
				"call.departments": departmentID,
			}},
			bson.M{"$count": "total"},
		})
		if err != nil {
			return nil, err
		}
		metrics["calls_responded"] = count
	}

	// Calls Cleared
	if needMetric("calls_cleared") {
		count, err := c.runCountPipeline(ctx, "calls", bson.A{
			bson.M{"$match": bson.M{
				"call.communityID":       communityID,
				"call.clearingOfficerID": userID,
				"call.departments":       departmentID,
			}},
			bson.M{"$count": "total"},
		})
		if err != nil {
			return nil, err
		}
		metrics["calls_cleared"] = count
	}

	// BOLOs Created
	if needMetric("bolos_created") {
		count, err := c.runCountPipeline(ctx, "bolos", bson.A{
			bson.M{"$match": bson.M{
				"bolo.communityID":  communityID,
				"bolo.reportedByID": userID,
				"bolo.departmentID": departmentID,
			}},
			bson.M{"$count": "total"},
		})
		if err != nil {
			return nil, err
		}
		metrics["bolos_created"] = count
	}

	// Warrants Requested
	if needMetric("warrants_requested") {
		count, err := c.runCountPipeline(ctx, "warrants", bson.A{
			bson.M{"$match": bson.M{
				"warrant.activeCommunityID":   communityID,
				"warrant.requestingOfficerID": userID,
				"warrant.departmentId":        departmentID,
			}},
			bson.M{"$count": "total"},
		})
		if err != nil {
			return nil, err
		}
		metrics["warrants_requested"] = count
	}

	// Warrants Executed
	if needMetric("warrants_executed") {
		count, err := c.runCountPipeline(ctx, "warrants", bson.A{
			bson.M{"$match": bson.M{
				"warrant.activeCommunityID":  communityID,
				"warrant.executingOfficerID": userID,
				"warrant.departmentId":       departmentID,
			}},
			bson.M{"$count": "total"},
		})
		if err != nil {
			return nil, err
		}
		metrics["warrants_executed"] = count
	}

	// Medical Reports Created — EMS/Fire
	if needMetric("medical_reports_created") {
		count, err := c.runCountPipeline(ctx, "medicalreports", bson.A{
			bson.M{"$match": bson.M{
				"medicalReport.activeCommunityID": communityID,
				"medicalReport.reportingEmsID":    userID,
			}},
			bson.M{"$count": "total"},
		})
		if err != nil {
			return nil, err
		}
		metrics["medical_reports_created"] = count
	}

	// Calls Dispatched — Dispatch (same as calls_created but scoped for dispatch departments)
	if needMetric("calls_dispatched") {
		count, err := c.runCountPipeline(ctx, "calls", bson.A{
			bson.M{"$match": bson.M{
				"call.communityID": communityID,
				"call.createdByID": userID,
			}},
			bson.M{"$count": "total"},
		})
		if err != nil {
			return nil, err
		}
		metrics["calls_dispatched"] = count
	}

	// Warrants Reviewed — Judicial (judge who reviewed the warrant)
	if needMetric("warrants_reviewed") {
		count, err := c.runCountPipeline(ctx, "warrants", bson.A{
			bson.M{"$match": bson.M{
				"warrant.activeCommunityID": communityID,
				"warrant.judgeID":           userID,
			}},
			bson.M{"$count": "total"},
		})
		if err != nil {
			return nil, err
		}
		metrics["warrants_reviewed"] = count
	}

	// Court Cases Completed — Judicial
	if needMetric("court_cases_completed") {
		count, err := c.runCountPipeline(ctx, "courtcases", bson.A{
			bson.M{"$match": bson.M{
				"courtCase.communityID": communityID,
				"courtCase.judgeID":     userID,
				"courtCase.status":      "completed",
			}},
			bson.M{"$count": "total"},
		})
		if err != nil {
			return nil, err
		}
		metrics["court_cases_completed"] = count
	}

	return metrics, nil
}

// runCountPipeline runs an aggregation pipeline and returns the count from the first result's "total" field.
func (c Community) runCountPipeline(ctx context.Context, collection string, pipeline bson.A) (int, error) {
	cursor, err := c.DBHelper.Collection(collection).Aggregate(ctx, pipeline)
	if err != nil {
		return 0, err
	}
	defer cursor.Close(ctx)

	var result []struct {
		Total int `bson:"total"`
	}
	if err := cursor.All(ctx, &result); err != nil {
		return 0, err
	}
	if len(result) == 0 {
		return 0, nil
	}
	return result[0].Total, nil
}

// GetOfficerStatsHandler returns aggregated metrics for an officer in a department.
// GET /api/v1/community/{communityId}/departments/{departmentId}/members/{userId}/stats
func (c Community) GetOfficerStatsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]
	userID := mux.Vars(r)["userId"]

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Look up department to determine type for metric filtering
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}
	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}
	_, dept := findDepartment(community, departmentID)
	deptType := ""
	if dept != nil {
		deptType = getDepartmentType(dept)
	}

	metricsMap, err := c.computeOfficerMetrics(ctx, communityID, departmentID, userID, deptType)
	if err != nil {
		config.ErrorStatus("failed to compute officer metrics", http.StatusInternalServerError, w, err)
		return
	}

	// Build response with display names, filtered to relevant metrics
	registry := models.MetricTypeRegistry
	if deptType != "" {
		registry = models.MetricTypesForDepartment(deptType)
	}
	var metrics []OfficerMetric
	for _, mt := range registry {
		metrics = append(metrics, OfficerMetric{
			MetricType:   mt.Type,
			DisplayName:  mt.DisplayName,
			CurrentValue: metricsMap[mt.Type],
		})
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"userId":       userID,
		"departmentId": departmentID,
		"metrics":      metrics,
	})
}

// GetRankProgressHandler returns the officer's current rank, stats, and progress toward the next rank.
// It also triggers auto-promotion if eligible.
// GET /api/v1/community/{communityId}/departments/{departmentId}/members/{userId}/rank-progress
func (c Community) GetRankProgressHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]
	userID := mux.Vars(r)["userId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	deptIdx, dept := findDepartment(community, departmentID)
	if dept == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, fmt.Errorf("department not found"))
		return
	}

	// Find member in department
	memberIdx := -1
	var memberStatus *models.MemberStatus
	for i := range dept.Members {
		if dept.Members[i].UserID == userID {
			memberIdx = i
			memberStatus = &dept.Members[i]
			break
		}
	}
	if memberStatus == nil {
		// For public departments, auto-add the user as a member so they can participate in ranks
		if !dept.ApprovalRequired {
			newMember := models.MemberStatus{
				UserID: userID,
				Status: "approved",
			}
			pushPath := fmt.Sprintf("community.departments.%d.members", deptIdx)
			_ = c.DB.UpdateOne(ctx, bson.M{"_id": cID}, bson.M{"$push": bson.M{pushPath: newMember}})
			// Re-read community to get updated member index
			community, err = c.DB.FindOne(ctx, bson.M{"_id": cID})
			if err != nil {
				config.ErrorStatus("failed to re-read community", http.StatusInternalServerError, w, err)
				return
			}
			_, dept = findDepartment(community, departmentID)
			for i := range dept.Members {
				if dept.Members[i].UserID == userID {
					memberIdx = i
					memberStatus = &dept.Members[i]
					break
				}
			}
		}
		if memberStatus == nil {
			config.ErrorStatus("member not found in department", http.StatusNotFound, w, fmt.Errorf("member not found"))
			return
		}
	}

	// Compute metrics
	metricsMap, err := c.computeOfficerMetrics(ctx, communityID, departmentID, userID, getDepartmentType(dept))
	if err != nil {
		config.ErrorStatus("failed to compute officer metrics", http.StatusInternalServerError, w, err)
		return
	}

	// Sort ranks by displayOrder
	ranks := make([]models.Rank, len(dept.Ranks))
	copy(ranks, dept.Ranks)
	sort.Slice(ranks, func(i, j int) bool { return ranks[i].DisplayOrder < ranks[j].DisplayOrder })

	// Find current rank
	var currentRank *models.Rank
	currentRankOrder := len(ranks) // default: below all ranks
	for i := range ranks {
		if ranks[i].ID.Hex() == memberStatus.RankID {
			currentRank = &ranks[i]
			currentRankOrder = ranks[i].DisplayOrder
			break
		}
	}

	// If no rank assigned, use the default rank and persist it
	if currentRank == nil && memberStatus.RankID == "" {
		for i := range ranks {
			if ranks[i].IsDefault {
				currentRank = &ranks[i]
				currentRankOrder = ranks[i].DisplayOrder
				// Persist the default rank assignment
				memberStatus.RankID = ranks[i].ID.Hex()
				memberStatus.RankAssignmentType = "auto"
				rankPath := fmt.Sprintf("community.departments.%d.members.%d.rankId", deptIdx, memberIdx)
				typePath := fmt.Sprintf("community.departments.%d.members.%d.rankAssignmentType", deptIdx, memberIdx)
				_ = c.DB.UpdateOne(ctx, bson.M{"_id": cID}, bson.M{"$set": bson.M{
					rankPath: memberStatus.RankID,
					typePath:  memberStatus.RankAssignmentType,
				}})
				break
			}
		}
	}

	// Save previous rank before promotion check
	var previousRank *models.Rank
	if currentRank != nil {
		prevCopy := *currentRank
		previousRank = &prevCopy
	}

	// Auto-promotion check: try to promote to the highest eligible rank
	promoted := false
	for i := range ranks {
		// Only consider ranks above current (lower displayOrder = higher rank)
		if ranks[i].DisplayOrder >= currentRankOrder {
			continue
		}
		if !ranks[i].AutoPromote {
			continue
		}
		// Check if all requirements are met (tracked + custom)
		allMet := len(ranks[i].Requirements) > 0 && checkAllRequirementsMet(ranks[i].Requirements, metricsMap, memberStatus.CustomRequirementsMet)
		if allMet {
			// Promote to this rank
			memberStatus.RankID = ranks[i].ID.Hex()
			memberStatus.RankAssignedAt = primitive.NewDateTimeFromTime(time.Now())
			memberStatus.RankAssignmentType = "auto"
			currentRank = &ranks[i]
			currentRankOrder = ranks[i].DisplayOrder
			promoted = true
			// Continue to check for even higher ranks
		}
	}

	// Persist promotion if it happened
	if promoted {
		setFields := bson.M{
			fmt.Sprintf("community.departments.%d.members.%d.rankId", deptIdx, memberIdx):             memberStatus.RankID,
			fmt.Sprintf("community.departments.%d.members.%d.rankAssignedAt", deptIdx, memberIdx):     memberStatus.RankAssignedAt,
			fmt.Sprintf("community.departments.%d.members.%d.rankAssignmentType", deptIdx, memberIdx): memberStatus.RankAssignmentType,
		}
		err = c.DB.UpdateOne(ctx, bson.M{"_id": cID}, bson.M{"$set": setFields})
		if err != nil {
			config.ErrorStatus("failed to persist auto-promotion", http.StatusInternalServerError, w, err)
			return
		}
		logAudit(c.ALDB, cID, "rank.auto_promoted", "rank", userID, "", memberStatus.RankID, currentRank.Name, map[string]interface{}{
			"departmentId": departmentID,
		})
	}

	// Find next rank (the one immediately above current)
	var nextRank *models.Rank
	for i := len(ranks) - 1; i >= 0; i-- {
		if ranks[i].DisplayOrder < currentRankOrder {
			nextRank = &ranks[i]
			break
		}
	}

	// Build metrics list, filtered to relevant metrics for this department type
	deptType := getDepartmentType(dept)
	metricsRegistry := models.MetricTypeRegistry
	if deptType != "" {
		metricsRegistry = models.MetricTypesForDepartment(deptType)
	}
	var metricsResponse []OfficerMetric
	for _, mt := range metricsRegistry {
		metricsResponse = append(metricsResponse, OfficerMetric{
			MetricType:   mt.Type,
			DisplayName:  mt.DisplayName,
			CurrentValue: metricsMap[mt.Type],
		})
	}

	// Build progress toward next rank
	var progress []RankProgress
	if nextRank != nil {
		progress = buildRequirementProgress(nextRank.Requirements, metricsMap, memberStatus.CustomRequirementsMet)
	}

	allMet := len(progress) > 0
	for _, p := range progress {
		if !p.Met {
			allMet = false
			break
		}
	}

	// If all requirements met for a non-auto-promote rank, notify the community owner
	if allMet && nextRank != nil && !nextRank.AutoPromote && !promoted {
		// Resolve officer username for the notification message
		officerName := userID
		if userObjID, parseErr := primitive.ObjectIDFromHex(userID); parseErr == nil {
			var user models.User
			if decodeErr := c.UDB.FindOne(ctx, bson.M{"_id": userObjID}).Decode(&user); decodeErr == nil {
				if user.Details.Username != "" {
					officerName = user.Details.Username
				}
			}
		}
		// Use a background context — the request ctx will be cancelled by defer cancel() before the goroutine runs
		bgCtx, bgCancel := context.WithTimeout(context.Background(), 10*time.Second)
		go func() {
			defer bgCancel()
			c.sendPromotionEligibleNotification(bgCtx, community, departmentID, dept.Name, userID, officerName)
		}()
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"userId":              userID,
		"departmentId":        departmentID,
		"currentRank":         currentRank,
		"nextRank":            nextRank,
		"metrics":             metricsResponse,
		"progress":            progress,
		"allRequirementsMet":  allMet,
		"promoted":            promoted,
		"previousRank":        previousRank,
		"rankAssignedAt":      memberStatus.RankAssignedAt,
		"rankAssignmentType":  memberStatus.RankAssignmentType,
	})
}

// ---------- Rank Assignment ----------

// AssignMemberRankHandler manually assigns a rank to a department member.
// PUT /api/v1/community/{communityId}/departments/{departmentId}/members/{userId}/rank
func (c Community) AssignMemberRankHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]
	userID := mux.Vars(r)["userId"]

	var requestBody struct {
		RankID string `json:"rankId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	deptIdx, dept := findDepartment(community, departmentID)
	if dept == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, fmt.Errorf("department not found"))
		return
	}

	// Validate rank exists (unless clearing)
	if requestBody.RankID != "" {
		found := false
		for _, rk := range dept.Ranks {
			if rk.ID.Hex() == requestBody.RankID {
				found = true
				break
			}
		}
		if !found {
			config.ErrorStatus("rank not found in department", http.StatusBadRequest, w, fmt.Errorf("rank %s not found", requestBody.RankID))
			return
		}
	}

	// Find member index
	memberIdx := -1
	for i, m := range dept.Members {
		if m.UserID == userID {
			memberIdx = i
			break
		}
	}
	if memberIdx == -1 {
		config.ErrorStatus("member not found in department", http.StatusNotFound, w, fmt.Errorf("member not found"))
		return
	}

	setFields := bson.M{
		fmt.Sprintf("community.departments.%d.members.%d.rankId", deptIdx, memberIdx):             requestBody.RankID,
		fmt.Sprintf("community.departments.%d.members.%d.rankAssignedAt", deptIdx, memberIdx):     primitive.NewDateTimeFromTime(time.Now()),
		fmt.Sprintf("community.departments.%d.members.%d.rankAssignmentType", deptIdx, memberIdx): "manual",
	}

	err = c.DB.UpdateOne(ctx, bson.M{"_id": cID}, bson.M{"$set": setFields})
	if err != nil {
		config.ErrorStatus("failed to assign rank", http.StatusInternalServerError, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "rank.assigned", "rank", actorID, resolveActorName(c.UDB, actorID), requestBody.RankID, "", map[string]interface{}{
		"departmentId": departmentID,
		"userId":       userID,
	})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Rank assigned successfully",
	})
}

// PendingPromotion represents a member eligible for admin-review promotion.
type PendingPromotion struct {
	UserID         string         `json:"userId"`
	Username       string         `json:"username"`
	ProfilePicture string         `json:"profilePicture,omitempty"`
	CurrentRank    *models.Rank   `json:"currentRank"`
	NextRank       *models.Rank   `json:"nextRank"`
	Progress       []RankProgress `json:"progress"`
}

// GetPendingPromotionsHandler returns members eligible for promotion where the next rank requires admin review (autoPromote=false).
// GET /api/v1/community/{communityId}/departments/{departmentId}/pending-promotions
func (c Community) GetPendingPromotionsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	_, dept := findDepartment(community, departmentID)
	if dept == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, fmt.Errorf("department not found"))
		return
	}

	// Sort ranks by displayOrder
	ranks := make([]models.Rank, len(dept.Ranks))
	copy(ranks, dept.Ranks)
	sort.Slice(ranks, func(i, j int) bool { return ranks[i].DisplayOrder < ranks[j].DisplayOrder })

	var pending []PendingPromotion
	seen := make(map[string]bool)

	for _, member := range dept.Members {
		// Deduplicate by userID
		if seen[member.UserID] {
			continue
		}
		seen[member.UserID] = true

		// Find current rank
		currentRankOrder := len(ranks) // below all
		var currentRank *models.Rank
		for i := range ranks {
			if ranks[i].ID.Hex() == member.RankID {
				currentRank = &ranks[i]
				currentRankOrder = ranks[i].DisplayOrder
				break
			}
		}

		// If no rank, check for default
		if currentRank == nil {
			for i := range ranks {
				if ranks[i].IsDefault {
					currentRank = &ranks[i]
					currentRankOrder = ranks[i].DisplayOrder
					break
				}
			}
		}

		// Find next rank above current (lower displayOrder)
		var nextRank *models.Rank
		for i := len(ranks) - 1; i >= 0; i-- {
			if ranks[i].DisplayOrder < currentRankOrder {
				nextRank = &ranks[i]
				break
			}
		}

		// Skip if no next rank, or if next rank is auto-promote (those are handled automatically)
		if nextRank == nil || nextRank.AutoPromote || len(nextRank.Requirements) == 0 {
			continue
		}

		// Compute metrics for this member
		metricsMap, err := c.computeOfficerMetrics(ctx, communityID, departmentID, member.UserID, getDepartmentType(dept))
		if err != nil {
			continue
		}

		// Check progress toward next rank — show in pending if all TRACKED metrics are met
		// (custom requirements can be toggled by admin from the pending promotions panel)
		progress := buildRequirementProgress(nextRank.Requirements, metricsMap, member.CustomRequirementsMet)
		trackedMet := true
		for _, req := range nextRank.Requirements {
			if req.MetricType != "custom" && metricsMap[req.MetricType] < req.Threshold {
				trackedMet = false
				break
			}
		}

		if !trackedMet {
			continue
		}

		// Resolve user info — member.UserID is an ObjectID hex string, lookup by _id
		username := member.UserID
		profilePic := ""
		if c.UDB != nil {
			if userObjID, parseErr := primitive.ObjectIDFromHex(member.UserID); parseErr == nil {
				var user models.User
				if decodeErr := c.UDB.FindOne(ctx, bson.M{"_id": userObjID}).Decode(&user); decodeErr == nil {
					if user.Details.Username != "" {
						username = user.Details.Username
					}
					profilePic = user.Details.ProfilePicture
				}
			}
		}

		pending = append(pending, PendingPromotion{
			UserID:         member.UserID,
			Username:       username,
			ProfilePicture: profilePic,
			CurrentRank:    currentRank,
			NextRank:       nextRank,
			Progress:       progress,
		})
	}

	if pending == nil {
		pending = []PendingPromotion{}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"pending": pending,
		"count":   len(pending),
	})
}

// CheckAllPromotionsHandler checks and auto-promotes all eligible members in a department.
// POST /api/v1/community/{communityId}/departments/{departmentId}/ranks/check-promotions
func (c Community) CheckAllPromotionsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	deptIdx, dept := findDepartment(community, departmentID)
	if dept == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, fmt.Errorf("department not found"))
		return
	}

	// Sort ranks by displayOrder
	ranks := make([]models.Rank, len(dept.Ranks))
	copy(ranks, dept.Ranks)
	sort.Slice(ranks, func(i, j int) bool { return ranks[i].DisplayOrder < ranks[j].DisplayOrder })

	promotedCount := 0
	setFields := bson.M{}

	for memberIdx, member := range dept.Members {
		metricsMap, err := c.computeOfficerMetrics(ctx, communityID, departmentID, member.UserID, getDepartmentType(dept))
		if err != nil {
			continue // skip members we can't compute metrics for
		}

		currentRankOrder := len(ranks) // default: below all
		for _, rk := range ranks {
			if rk.ID.Hex() == member.RankID {
				currentRankOrder = rk.DisplayOrder
				break
			}
		}

		bestRankID := member.RankID
		bestRankOrder := currentRankOrder
		var bestRankName string

		for i := range ranks {
			if ranks[i].DisplayOrder >= bestRankOrder {
				continue
			}
			if !ranks[i].AutoPromote || len(ranks[i].Requirements) == 0 {
				continue
			}
			if checkAllRequirementsMet(ranks[i].Requirements, metricsMap, member.CustomRequirementsMet) {
				bestRankID = ranks[i].ID.Hex()
				bestRankOrder = ranks[i].DisplayOrder
				bestRankName = ranks[i].Name
			}
		}

		if bestRankID != member.RankID {
			setFields[fmt.Sprintf("community.departments.%d.members.%d.rankId", deptIdx, memberIdx)] = bestRankID
			setFields[fmt.Sprintf("community.departments.%d.members.%d.rankAssignedAt", deptIdx, memberIdx)] = primitive.NewDateTimeFromTime(time.Now())
			setFields[fmt.Sprintf("community.departments.%d.members.%d.rankAssignmentType", deptIdx, memberIdx)] = "auto"
			promotedCount++

			logAudit(c.ALDB, cID, "rank.auto_promoted", "rank", member.UserID, "", bestRankID, bestRankName, map[string]interface{}{
				"departmentId": departmentID,
			})
		}
	}

	if len(setFields) > 0 {
		err = c.DB.UpdateOne(ctx, bson.M{"_id": cID}, bson.M{"$set": setFields})
		if err != nil {
			config.ErrorStatus("failed to persist promotions", http.StatusInternalServerError, w, err)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":       "Promotion check completed",
		"promotedCount": promotedCount,
	})
}

// ---------- Pending Promotion Counts (bulk) ----------

// GetPendingPromotionCountsHandler returns pending promotion counts for all departments in a community.
// GET /api/v1/community/{communityId}/pending-promotion-counts
func (c Community) GetPendingPromotionCountsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	counts := make(map[string]int)

	for _, dept := range community.Details.Departments {
		deptID := dept.ID.Hex()
		if len(dept.Ranks) == 0 {
			continue
		}

		// Sort ranks by displayOrder
		ranks := make([]models.Rank, len(dept.Ranks))
		copy(ranks, dept.Ranks)
		sort.Slice(ranks, func(i, j int) bool { return ranks[i].DisplayOrder < ranks[j].DisplayOrder })

		seen := make(map[string]bool)
		count := 0

		for _, member := range dept.Members {
			if seen[member.UserID] {
				continue
			}
			seen[member.UserID] = true

			// Find current rank
			currentRankOrder := len(ranks)
			for i := range ranks {
				if ranks[i].ID.Hex() == member.RankID {
					currentRankOrder = ranks[i].DisplayOrder
					break
				}
			}
			// Check default rank if none assigned
			if member.RankID == "" {
				for i := range ranks {
					if ranks[i].IsDefault {
						currentRankOrder = ranks[i].DisplayOrder
						break
					}
				}
			}

			// Find next rank above current (lower displayOrder)
			var nextRank *models.Rank
			for i := len(ranks) - 1; i >= 0; i-- {
				if ranks[i].DisplayOrder < currentRankOrder {
					nextRank = &ranks[i]
					break
				}
			}

			if nextRank == nil || nextRank.AutoPromote || len(nextRank.Requirements) == 0 {
				continue
			}

			// Compute metrics for this member
			metricsMap, err := c.computeOfficerMetrics(ctx, communityID, deptID, member.UserID, getDepartmentType(&dept))
			if err != nil {
				continue
			}

			// Count as pending if all tracked metrics are met (custom reqs can be toggled by admin)
			trackedMet := true
			for _, req := range nextRank.Requirements {
				if req.MetricType != "custom" && metricsMap[req.MetricType] < req.Threshold {
					trackedMet = false
					break
				}
			}
			if trackedMet {
				count++
			}
		}

		if count > 0 {
			counts[deptID] = count
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"counts": counts,
	})
}

// ---------- Custom Requirement Toggle ----------

// ToggleCustomRequirementHandler marks a custom requirement as met or unmet for a member.
// PUT /api/v1/community/{communityId}/departments/{departmentId}/members/{userId}/custom-requirements/{requirementId}
func (c Community) ToggleCustomRequirementHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]
	userID := mux.Vars(r)["userId"]
	requirementID := mux.Vars(r)["requirementId"]

	var body struct {
		Met bool `json:"met"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	deptIdx, dept := findDepartment(community, departmentID)
	if dept == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, fmt.Errorf("department not found"))
		return
	}

	memberIdx := -1
	for i, m := range dept.Members {
		if m.UserID == userID {
			memberIdx = i
			break
		}
	}
	if memberIdx == -1 {
		config.ErrorStatus("member not found", http.StatusNotFound, w, fmt.Errorf("member not found in department"))
		return
	}

	member := dept.Members[memberIdx]
	path := fmt.Sprintf("community.departments.%d.members.%d.customRequirementsMet", deptIdx, memberIdx)

	var update bson.M
	if body.Met {
		// Add requirement ID if not already present
		if !containsString(member.CustomRequirementsMet, requirementID) {
			update = bson.M{"$push": bson.M{path: requirementID}}
		}
	} else {
		// Remove requirement ID
		update = bson.M{"$pull": bson.M{path: requirementID}}
	}

	if update != nil {
		err = c.DB.UpdateOne(ctx, bson.M{"_id": cID}, update)
		if err != nil {
			config.ErrorStatus("failed to toggle custom requirement", http.StatusInternalServerError, w, err)
			return
		}
	}

	actorID := resolveActorFromRequest(r)
	action := "custom_requirement.met"
	if !body.Met {
		action = "custom_requirement.unmet"
	}
	logAudit(c.ALDB, cID, action, "rank", actorID, resolveActorName(c.UDB, actorID), requirementID, "", map[string]interface{}{
		"departmentId": departmentID,
		"userId":       userID,
	})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Custom requirement updated",
		"met":     body.Met,
	})
}

// ---------- Promotion Eligibility Notification ----------

// sendPromotionEligibleNotification sends a notification to the community owner
// when an officer meets all requirements for a non-auto-promote rank.
// It checks for duplicate unseen notifications to avoid spam.
func (c Community) sendPromotionEligibleNotification(ctx context.Context, community *models.Community, departmentID, departmentName, userID, username string) {
	if c.UDB == nil {
		return
	}

	ownerID := community.Details.OwnerID
	if ownerID == "" {
		return
	}

	// Look up owner by userID field (auth ID), not _id
	var owner models.User
	if err := c.UDB.FindOne(ctx, bson.M{"userID": ownerID}).Decode(&owner); err != nil {
		return
	}
	ownerObjID := owner.ID // string hex of _id

	// Check for existing unseen notification about this user+department to avoid spam
	for _, n := range owner.Details.Notifications {
		if n.Type == "promotion_eligible" && n.Data1 == userID && n.Data2 == departmentID && !n.Seen {
			return // already notified
		}
	}

	notification := models.Notification{
		ID:         primitive.NewObjectID().Hex(),
		SentFromID: userID,
		SentToID:   ownerObjID,
		Type:       "promotion_eligible",
		Message:    username + " is eligible for promotion in " + departmentName,
		Data1:      userID,             // eligible officer's user ID
		Data2:      departmentID,       // department ID
		Data3:      community.ID.Hex(), // community ID
		Data4:      departmentName,     // department name (for display)
		Seen:       false,
		CreatedAt:  time.Now(),
	}

	ownerDocID, parseErr := primitive.ObjectIDFromHex(ownerObjID)
	if parseErr != nil {
		return
	}
	filter := bson.M{"_id": ownerDocID}
	update := bson.M{"$push": bson.M{"user.notifications": notification}}
	if _, err := c.UDB.UpdateOne(ctx, filter, update); err != nil {
		return
	}

	sendNotificationToUser(ownerObjID, notification)
}
