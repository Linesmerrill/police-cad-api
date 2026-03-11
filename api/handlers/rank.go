package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

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

// ---------- Officer Stats & Rank Progress ----------

// OfficerMetric represents one metric's current value for an officer
type OfficerMetric struct {
	MetricType   string `json:"metricType"`
	DisplayName  string `json:"displayName"`
	CurrentValue int    `json:"currentValue"`
}

// RankProgress shows progress toward a single requirement
type RankProgress struct {
	MetricType   string  `json:"metricType"`
	DisplayName  string  `json:"displayName"`
	CurrentValue int     `json:"currentValue"`
	Threshold    int     `json:"threshold"`
	Percentage   float64 `json:"percentage"`
	Met          bool    `json:"met"`
}

// computeOfficerMetrics aggregates all metric values for a given officer in a department.
func (c Community) computeOfficerMetrics(ctx context.Context, communityID, departmentID, userID string) (map[string]int, error) {
	metrics := make(map[string]int)

	// Citations, Warnings, Arrests — from civilians collection
	for _, entry := range []struct {
		metricType     string
		crimHistType   string
	}{
		{"citations_issued", "Citation"},
		{"warnings_issued", "Warning"},
		{"arrests_made", "Arrest"},
	} {
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

	// Calls Responded
	count, err = c.runCountPipeline(ctx, "calls", bson.A{
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

	// Calls Cleared
	count, err = c.runCountPipeline(ctx, "calls", bson.A{
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

	// BOLOs Created
	count, err = c.runCountPipeline(ctx, "bolos", bson.A{
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

	// Warrants Requested
	count, err = c.runCountPipeline(ctx, "warrants", bson.A{
		bson.M{"$match": bson.M{
			"warrant.activeCommunityID":  communityID,
			"warrant.requestingOfficerID": userID,
			"warrant.departmentId":        departmentID,
		}},
		bson.M{"$count": "total"},
	})
	if err != nil {
		return nil, err
	}
	metrics["warrants_requested"] = count

	// Warrants Executed
	count, err = c.runCountPipeline(ctx, "warrants", bson.A{
		bson.M{"$match": bson.M{
			"warrant.activeCommunityID": communityID,
			"warrant.executingOfficerID": userID,
			"warrant.departmentId":       departmentID,
		}},
		bson.M{"$count": "total"},
	})
	if err != nil {
		return nil, err
	}
	metrics["warrants_executed"] = count

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

	metricsMap, err := c.computeOfficerMetrics(ctx, communityID, departmentID, userID)
	if err != nil {
		config.ErrorStatus("failed to compute officer metrics", http.StatusInternalServerError, w, err)
		return
	}

	// Build response with display names
	var metrics []OfficerMetric
	for _, mt := range models.MetricTypeRegistry {
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
		config.ErrorStatus("member not found in department", http.StatusNotFound, w, fmt.Errorf("member not found"))
		return
	}

	// Compute metrics
	metricsMap, err := c.computeOfficerMetrics(ctx, communityID, departmentID, userID)
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
		// Check if all requirements are met
		allMet := true
		for _, req := range ranks[i].Requirements {
			if metricsMap[req.MetricType] < req.Threshold {
				allMet = false
				break
			}
		}
		if allMet && len(ranks[i].Requirements) > 0 {
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

	// Build metrics list
	var metricsResponse []OfficerMetric
	for _, mt := range models.MetricTypeRegistry {
		metricsResponse = append(metricsResponse, OfficerMetric{
			MetricType:   mt.Type,
			DisplayName:  mt.DisplayName,
			CurrentValue: metricsMap[mt.Type],
		})
	}

	// Build progress toward next rank
	var progress []RankProgress
	if nextRank != nil {
		for _, req := range nextRank.Requirements {
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

	allMet := len(progress) > 0
	for _, p := range progress {
		if !p.Met {
			allMet = false
			break
		}
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
		metricsMap, err := c.computeOfficerMetrics(ctx, communityID, departmentID, member.UserID)
		if err != nil {
			continue
		}

		// Check progress toward next rank
		allMet := true
		var progress []RankProgress
		for _, req := range nextRank.Requirements {
			current := metricsMap[req.MetricType]
			pct := float64(0)
			if req.Threshold > 0 {
				pct = float64(current) / float64(req.Threshold)
				if pct > 1.0 {
					pct = 1.0
				}
			}
			met := current >= req.Threshold
			if !met {
				allMet = false
			}
			progress = append(progress, RankProgress{
				MetricType:   req.MetricType,
				DisplayName:  models.MetricTypeDisplayNames[req.MetricType],
				CurrentValue: current,
				Threshold:    req.Threshold,
				Percentage:   pct,
				Met:          met,
			})
		}

		if !allMet {
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
		metricsMap, err := c.computeOfficerMetrics(ctx, communityID, departmentID, member.UserID)
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
			allMet := true
			for _, req := range ranks[i].Requirements {
				if metricsMap[req.MetricType] < req.Threshold {
					allMet = false
					break
				}
			}
			if allMet {
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
