package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// jobRequestBody is the shape accepted by AddJobHandler / UpdateJobHandler.
// All numeric fields use cents for money and seconds/minutes for durations,
// matching the persistence shape — the frontend converts at the edges.
type jobRequestBody struct {
	Name                     string `json:"name"`
	Description              string `json:"description"`
	Icon                     string `json:"icon"`
	Color                    string `json:"color"`
	PayPerHour               int64  `json:"payPerHour"`
	MaxSessionMinutes        int    `json:"maxSessionMinutes"`
	AfkPromptIntervalSeconds int    `json:"afkPromptIntervalSeconds"`
	AfkGraceSeconds          int    `json:"afkGraceSeconds"`
	PayoutMode               string `json:"payoutMode"`
	RequiresFirearmLicense   bool   `json:"requiresFirearmLicense"`
	RequiresDriversLicense   bool   `json:"requiresDriversLicense"`
	Archived                 bool   `json:"archived"`
	SortOrder                int    `json:"sortOrder"`
}

func (b jobRequestBody) intoModel(now primitive.DateTime) models.Job {
	payoutMode := b.PayoutMode
	if payoutMode != "on_clockout" {
		payoutMode = "on_heartbeat"
	}
	maxSession := b.MaxSessionMinutes
	if maxSession <= 0 {
		maxSession = 120
	}
	afkPrompt := b.AfkPromptIntervalSeconds
	if afkPrompt <= 0 {
		afkPrompt = 600
	}
	afkGrace := b.AfkGraceSeconds
	if afkGrace <= 0 {
		afkGrace = 60
	}
	return models.Job{
		Name:                     b.Name,
		Description:              b.Description,
		Icon:                     b.Icon,
		Color:                    b.Color,
		PayPerHour:               b.PayPerHour,
		MaxSessionMinutes:        maxSession,
		AfkPromptIntervalSeconds: afkPrompt,
		AfkGraceSeconds:          afkGrace,
		PayoutMode:               payoutMode,
		RequiresFirearmLicense:   b.RequiresFirearmLicense,
		RequiresDriversLicense:   b.RequiresDriversLicense,
		Archived:                 b.Archived,
		SortOrder:                b.SortOrder,
		UpdatedAt:                now,
	}
}

// AddJobHandler appends a new Job to a community's jobs[] array.
// POST /api/v1/community/{communityId}/jobs
func (c Community) AddJobHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	var body jobRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}
	if body.Name == "" {
		config.ErrorStatus("name is required", http.StatusBadRequest, w, nil)
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	now := primitive.NewDateTimeFromTime(time.Now())
	job := body.intoModel(now)
	job.ID = primitive.NewObjectID()
	job.CreatedAt = now

	err = c.DB.UpdateOne(ctx, bson.M{"_id": cID}, bson.M{"$push": bson.M{"community.jobs": job}})
	if err != nil {
		config.ErrorStatus("failed to add job", http.StatusInternalServerError, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "job.created", "economy", actorID, resolveActorName(c.UDB, actorID), job.ID.Hex(), job.Name, nil)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{"job": job})
}

// UpdateJobHandler patches fields on an existing Job within a community.
// PUT /api/v1/community/{communityId}/jobs/{jobId}
func (c Community) UpdateJobHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	jobID := mux.Vars(r)["jobId"]

	var body jobRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}
	jOID, err := primitive.ObjectIDFromHex(jobID)
	if err != nil {
		config.ErrorStatus("invalid job ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	now := primitive.NewDateTimeFromTime(time.Now())
	patch := body.intoModel(now)
	filter := bson.M{"_id": cID, "community.jobs._id": jOID}
	update := bson.M{"$set": bson.M{
		"community.jobs.$.name":                     patch.Name,
		"community.jobs.$.description":              patch.Description,
		"community.jobs.$.icon":                     patch.Icon,
		"community.jobs.$.color":                    patch.Color,
		"community.jobs.$.payPerHour":               patch.PayPerHour,
		"community.jobs.$.maxSessionMinutes":        patch.MaxSessionMinutes,
		"community.jobs.$.afkPromptIntervalSeconds": patch.AfkPromptIntervalSeconds,
		"community.jobs.$.afkGraceSeconds":          patch.AfkGraceSeconds,
		"community.jobs.$.payoutMode":               patch.PayoutMode,
		"community.jobs.$.requiresFirearmLicense":   patch.RequiresFirearmLicense,
		"community.jobs.$.requiresDriversLicense":   patch.RequiresDriversLicense,
		"community.jobs.$.archived":                 patch.Archived,
		"community.jobs.$.sortOrder":                patch.SortOrder,
		"community.jobs.$.updatedAt":                now,
	}}
	if err := c.DB.UpdateOne(ctx, filter, update); err != nil {
		config.ErrorStatus("failed to update job", http.StatusInternalServerError, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "job.updated", "economy", actorID, resolveActorName(c.UDB, actorID), jobID, patch.Name, nil)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message":"Job updated"}`))
}

// DeleteJobHandler removes a Job entry from a community.
// DELETE /api/v1/community/{communityId}/jobs/{jobId}
func (c Community) DeleteJobHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	jobID := mux.Vars(r)["jobId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}
	jOID, err := primitive.ObjectIDFromHex(jobID)
	if err != nil {
		config.ErrorStatus("invalid job ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	err = c.DB.UpdateOne(ctx, bson.M{"_id": cID}, bson.M{"$pull": bson.M{"community.jobs": bson.M{"_id": jOID}}})
	if err != nil {
		config.ErrorStatus("failed to delete job", http.StatusInternalServerError, w, err)
		return
	}

	actorID := resolveActorFromRequest(r)
	logAudit(c.ALDB, cID, "job.deleted", "economy", actorID, resolveActorName(c.UDB, actorID), jobID, "", nil)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message":"Job deleted"}`))
}
