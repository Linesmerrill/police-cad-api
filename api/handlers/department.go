package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// GetDepartmentsScreenDataHandler handles the request to get departments screen data
func (c Community) GetDepartmentsScreenDataHandler(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	communityID := r.URL.Query().Get("communityId")
	userID := r.URL.Query().Get("userId")

	if communityID == "" || userID == "" {
		config.ErrorStatus("communityId and userId are required", http.StatusBadRequest, w, nil)
		return
	}

	// Fetch community details with only _id and roles
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("Invalid communityId", http.StatusBadRequest, w, err)
		return
	}

	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("Invalid userId", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOne(
		ctx,
		bson.M{
			"_id":             cID,
			"community.roles": bson.M{"$exists": true}, // Ensures roles field exists
		},
	)
	if err != nil {
		config.ErrorStatus("Failed to fetch community", http.StatusInternalServerError, w, err)
		return
	}

	userFilter := bson.M{"_id": uID}
	userData := models.User{}
	err = c.UDB.FindOne(ctx, userFilter).Decode(&userData)
	if err != nil {
		config.ErrorStatus("failed to get friend by ID", http.StatusNotFound, w, err)
		return
	}

	// Check if the user is a member of the community
	isMember := false
	for _, communityDetails := range userData.Details.Communities {
		if communityDetails.CommunityID == communityID && communityDetails.Status == "approved" {
			isMember = true
			break
		}
	}

	if !isMember {
		response := map[string]bool{
			"isMember":             false,
			"canManageDepartments": false,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Check if the user has permission to manage departments
	canManageDepartments := false
	for _, role := range community.Details.Roles {
		isMember := false
		for _, member := range role.Members {
			if member == userID {
				isMember = true
				break
			}
		}
		if isMember {
			for _, permission := range role.Permissions {
				if (permission.Name == "manage departments" || permission.Name == "administrator") && permission.Enabled {
					canManageDepartments = true
					break
				}
			}
		}
		if canManageDepartments {
			break
		}
	}

	// Return the response
	response := map[string]bool{
		"isMember":             true,
		"canManageDepartments": canManageDepartments,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// GetDepartmentMembersHandler handles the request to get department members
func (c Community) GetDepartmentMembersHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	// Parse pagination parameters
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1 // Default to page 1
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 10 // Default limit
	}
	offset := (page - 1) * limit

	// Convert communityID and departmentID to ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		http.Error(w, "Invalid community ID", http.StatusBadRequest)
		return
	}
	dID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		http.Error(w, "Invalid department ID", http.StatusBadRequest)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Fetch the community
	community, err := c.DB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		http.Error(w, "Community not found", http.StatusNotFound)
		return
	}

	// Find the department by ID
	var department *models.Department
	for _, dept := range community.Details.Departments {
		if dept.ID == dID {
			department = &dept
			break
		}
	}
	if department == nil {
		http.Error(w, "Department not found", http.StatusNotFound)
		return
	}

	// Build a map of rankId by userID from department members for cross-referencing
	rankByUserID := make(map[string]string)
	for _, member := range department.Members {
		if member.RankID != "" {
			rankByUserID[member.UserID] = member.RankID
		}
	}

	var enrichedMembers []map[string]interface{}
	var totalCount int

	if !department.ApprovalRequired {
		// PUBLIC department: return all community members, cross-referenced with rank data
		filter := bson.M{
			"user.communities": bson.M{
				"$elemMatch": bson.M{
					"communityId": communityID,
					"status":      "approved",
				},
			},
		}

		// Count total community members
		total, err := c.UDB.CountDocuments(ctx, filter)
		if err != nil {
			totalCount = 0
		} else {
			totalCount = int(total)
		}

		// Fetch paginated community members
		findOptions := options.Find().SetSkip(int64(offset)).SetLimit(int64(limit))
		cursor, err := c.UDB.Find(ctx, filter, findOptions)
		if err != nil {
			http.Error(w, "Failed to fetch community members", http.StatusInternalServerError)
			return
		}
		defer cursor.Close(ctx)

		var users []models.User
		if err = cursor.All(ctx, &users); err != nil {
			http.Error(w, "Failed to decode community members", http.StatusInternalServerError)
			return
		}

		for _, user := range users {
			memberData := map[string]interface{}{
				"_id": user.ID,
				"user": map[string]interface{}{
					"userID":         user.ID,
					"username":       user.Details.Username,
					"subscription":   user.Details.Subscription,
					"profilePicture": user.Details.ProfilePicture,
				},
			}
			if rankID, ok := rankByUserID[user.ID]; ok {
				memberData["rankId"] = rankID
			}
			enrichedMembers = append(enrichedMembers, memberData)
		}
	} else {
		// PRIVATE department: return only approved department members
		seen := make(map[string]bool)
		var approvedMembers []models.MemberStatus
		for _, member := range department.Members {
			if member.Status == "approved" && !seen[member.UserID] {
				seen[member.UserID] = true
				approvedMembers = append(approvedMembers, member)
			}
		}

		totalCount = len(approvedMembers)

		// Paginate the approved members
		start := offset
		end := offset + limit
		if start > totalCount {
			start = totalCount
		}
		if end > totalCount {
			end = totalCount
		}
		paginatedMembers := approvedMembers[start:end]

		// Batch fetch all users in a single query
		userIDs := make([]primitive.ObjectID, 0, len(paginatedMembers))
		for _, member := range paginatedMembers {
			userID, err := primitive.ObjectIDFromHex(member.UserID)
			if err != nil {
				continue
			}
			userIDs = append(userIDs, userID)
		}

		var users []models.User
		if len(userIDs) > 0 {
			cursor, err := c.UDB.Find(ctx, bson.M{"_id": bson.M{"$in": userIDs}})
			if err == nil {
				defer cursor.Close(ctx)
				cursor.All(ctx, &users)
			}
		}

		userMap := make(map[string]models.User)
		for _, user := range users {
			userMap[user.ID] = user
		}

		for _, member := range paginatedMembers {
			user, exists := userMap[member.UserID]
			if !exists {
				continue
			}

			memberData := map[string]interface{}{
				"_id": member.UserID,
				"user": map[string]interface{}{
					"userID":         member.UserID,
					"username":       user.Details.Username,
					"subscription":   user.Details.Subscription,
					"profilePicture": user.Details.ProfilePicture,
				},
			}
			if member.RankID != "" {
				memberData["rankId"] = member.RankID
			}
			enrichedMembers = append(enrichedMembers, memberData)
		}
	}

	// Return the response
	response := map[string]interface{}{
		"page":             page,
		"limit":            limit,
		"totalCount":       totalCount,
		"approvalRequired": department.ApprovalRequired,
		"data":             enrichedMembers,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
