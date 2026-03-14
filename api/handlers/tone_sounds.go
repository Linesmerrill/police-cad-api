package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// GetToneSoundsHandler returns all custom tone sounds for a community,
// plus the current template defaults.
func (c Community) GetToneSoundsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community", http.StatusNotFound, w, err)
		return
	}

	sounds := community.Details.CustomToneSounds
	if sounds == nil {
		sounds = []models.CustomToneSound{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"sounds":   sounds,
		"defaults": map[string]string{
			"leo": community.Details.DefaultToneLeo,
			"fd":  community.Details.DefaultToneFd,
			"ems": community.Details.DefaultToneEms,
		},
	})
}

// CreateToneSoundHandler adds a custom tone sound to a community.
func (c Community) CreateToneSoundHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	var request struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if request.Name == "" || request.URL == "" {
		config.ErrorStatus("name and url are required", http.StatusBadRequest, w, fmt.Errorf("missing required fields"))
		return
	}

	sound := models.CustomToneSound{
		Key:       fmt.Sprintf("custom_%s", primitive.NewObjectID().Hex()),
		Name:      request.Name,
		URL:       request.URL,
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
	}

	// Initialize customToneSounds if null
	initFilter := bson.M{"_id": cID, "community.customToneSounds": nil}
	initUpdate := bson.M{"$set": bson.M{"community.customToneSounds": bson.A{}}}
	_ = c.DB.UpdateOne(context.Background(), initFilter, initUpdate)

	filter := bson.M{"_id": cID}
	update := bson.M{
		"$push": bson.M{"community.customToneSounds": sound},
	}

	if err := c.DB.UpdateOne(context.Background(), filter, update); err != nil {
		config.ErrorStatus("failed to create tone sound", http.StatusInternalServerError, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "tone_sound.created", "tone", actorID, resolveActorName(c.UDB, actorID), sound.Key, sound.Name, nil)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"sound":   sound,
	})
}

// UpdateToneSoundHandler updates a custom tone sound's name.
func (c Community) UpdateToneSoundHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]
	soundKey := vars["soundKey"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	var request struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if request.Name == "" {
		config.ErrorStatus("name is required", http.StatusBadRequest, w, fmt.Errorf("missing required fields"))
		return
	}

	filter := bson.M{"_id": cID, "community.customToneSounds.key": soundKey}
	update := bson.M{
		"$set": bson.M{"community.customToneSounds.$.name": request.Name},
	}

	if err := c.DB.UpdateOne(context.Background(), filter, update); err != nil {
		config.ErrorStatus("failed to update tone sound", http.StatusInternalServerError, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "tone_sound.updated", "tone", actorID, resolveActorName(c.UDB, actorID), soundKey, request.Name, nil)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Tone sound updated successfully",
	})
}

// DeleteToneSoundHandler deletes a custom tone sound and clears any template defaults referencing it.
func (c Community) DeleteToneSoundHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]
	soundKey := vars["soundKey"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Remove the sound from the array
	filter := bson.M{"_id": cID}
	update := bson.M{
		"$pull": bson.M{
			"community.customToneSounds": bson.M{"key": soundKey},
		},
	}

	if err := c.DB.UpdateOne(context.Background(), filter, update); err != nil {
		config.ErrorStatus("failed to delete tone sound", http.StatusInternalServerError, w, err)
		return
	}

	// Clear any template defaults that reference this sound
	unsetFields := bson.M{}
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err == nil {
		if community.Details.DefaultToneLeo == soundKey {
			unsetFields["community.defaultToneLeo"] = ""
		}
		if community.Details.DefaultToneFd == soundKey {
			unsetFields["community.defaultToneFd"] = ""
		}
		if community.Details.DefaultToneEms == soundKey {
			unsetFields["community.defaultToneEms"] = ""
		}
	}
	if len(unsetFields) > 0 {
		_ = c.DB.UpdateOne(context.Background(), bson.M{"_id": cID}, bson.M{"$unset": unsetFields})
	}

	// Clear any per-department toneSound overrides that reference this sound
	// (departments using deleted custom tone revert to template default)
	if community != nil {
		for i, dept := range community.Details.Departments {
			if dept.ToneSound == soundKey {
				deptPath := fmt.Sprintf("community.departments.%d.toneSound", i)
				_ = c.DB.UpdateOne(context.Background(), bson.M{"_id": cID}, bson.M{"$set": bson.M{deptPath: ""}})
			}
		}
	}

	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "tone_sound.deleted", "tone", actorID, resolveActorName(c.UDB, actorID), soundKey, "", nil)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Tone sound deleted successfully",
	})
}

// SetToneDefaultHandler sets or clears a template default tone sound.
func (c Community) SetToneDefaultHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	var request struct {
		Template string `json:"template"` // "leo", "fd", or "ems"
		SoundKey string `json:"soundKey"` // custom tone key, or "" to reset to built-in
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	var field string
	switch request.Template {
	case "leo":
		field = "community.defaultToneLeo"
	case "fd":
		field = "community.defaultToneFd"
	case "ems":
		field = "community.defaultToneEms"
	default:
		config.ErrorStatus("template must be 'leo', 'fd', or 'ems'", http.StatusBadRequest, w, fmt.Errorf("invalid template: %s", request.Template))
		return
	}

	filter := bson.M{"_id": cID}
	var update bson.M
	if request.SoundKey == "" {
		update = bson.M{"$unset": bson.M{field: ""}}
	} else {
		update = bson.M{"$set": bson.M{field: request.SoundKey}}
	}

	if err := c.DB.UpdateOne(context.Background(), filter, update); err != nil {
		config.ErrorStatus("failed to set tone default", http.StatusInternalServerError, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "tone_default.updated", "tone", actorID, resolveActorName(c.UDB, actorID), "", request.Template, map[string]interface{}{
		"soundKey": request.SoundKey,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Tone default updated successfully",
	})
}
