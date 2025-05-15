package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/linesmerrill/police-cad-api/config"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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

	community, err := c.DB.FindOne(
		context.Background(),
		bson.M{
			"_id":             cID,
			"community.roles": bson.M{"$exists": true}, // Ensures roles field exists
		},
	)
	if err != nil {
		config.ErrorStatus("Failed to fetch community", http.StatusInternalServerError, w, err)
		return
	}

	// Check if the user is a member of the community
	isMember := false
	for _, role := range community.Details.Roles {
		for _, member := range role.Members {
			if member == userID {
				isMember = true
				break
			}
		}
		if isMember {
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
