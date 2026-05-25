package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// GetCommunityPenalCodesHandler returns community penal codes, initializing defaults if empty
func (c Community) GetCommunityPenalCodesHandler(w http.ResponseWriter, r *http.Request) {
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
		config.ErrorStatus("failed to find community", http.StatusNotFound, w, err)
		return
	}

	penalCodes := community.Details.PenalCodes
	// Lazy initialization: if no categories exist, set defaults
	if len(penalCodes.Categories) == 0 {
		penalCodes = models.DefaultCommunityPenalCodes()
		// Persist defaults so this only happens once
		filter := bson.M{"_id": cID}
		update := bson.M{"$set": bson.M{"community.penalCodes": penalCodes}}
		_ = c.DB.UpdateOne(ctx, filter, update)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(penalCodes)
}

// SetCommunityPenalCodesHandler updates the community penal codes
func (c Community) SetCommunityPenalCodesHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	var penalCodesData models.CommunityPenalCode
	if err := json.NewDecoder(r.Body).Decode(&penalCodesData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": cID}
	update := bson.M{
		"$set": bson.M{
			"community.penalCodes": penalCodesData,
		},
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	err = c.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to update community penal codes", http.StatusInternalServerError, w, err)
		return
	}

	// Audit log
	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "penal_codes.updated", "penal_codes", actorID, resolveActorName(c.UDB, actorID), "", "", nil)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Community penal codes updated successfully"}`))
}

// ResetCommunityPenalCodesHandler resets community penal codes to defaults
func (c Community) ResetCommunityPenalCodesHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	defaults := models.DefaultCommunityPenalCodes()

	filter := bson.M{"_id": cID}
	update := bson.M{
		"$set": bson.M{
			"community.penalCodes": defaults,
		},
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	err = c.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to reset community penal codes", http.StatusInternalServerError, w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(defaults)
}
