package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
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

	// Fetch community details
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("Invalid communityId", http.StatusBadRequest, w, err)
		return
	}

	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("Failed to fetch community", http.StatusInternalServerError, w, err)
		return
	}

	// Fetch user details
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("Invalid userId", http.StatusBadRequest, w, err)
		return
	}

	user := models.User{}
	err = c.UDB.FindOne(context.Background(), bson.M{"_id": uID}).Decode(&user)
	if err != nil {
		config.ErrorStatus("Failed to fetch user", http.StatusInternalServerError, w, err)
		return
	}

	// Prepare the response
	response := map[string]interface{}{
		"community": map[string]interface{}{
			"_id":   community.ID.Hex(),
			"roles": community.Details.Roles, // Assuming roles are already structured as required
		},
		"user": map[string]interface{}{
			"communities": user.Details.Communities, // Assuming communities are already structured as required
		},
	}

	// Send the response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
