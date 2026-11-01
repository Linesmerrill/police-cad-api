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
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/models"
)

// Warrants: an integration can look them up, and it can ask for one.
//
// Asking is the interesting half, and the shape of it is one decision: a
// warrant requested through a key always lands pending, whatever the
// community's approval mode says.
//
// That needs explaining, because it is deliberately NOT what the website does.
// A community can set its warrant mode to auto-approve, and most have, so an
// officer clicking "request warrant" in the CAD gets an approved warrant
// immediately. Auto-approve is a convenience for a person who is already in the
// CAD, has already passed the officer check, and is making one decision they
// can see the result of. A game server is none of those things: it fires on a
// trigger, it retries, and nobody is watching the moment it happens. Letting a
// script trip auto-approve would mean a key could manufacture an approved
// arrest warrant on a loop, and no community that turned on auto-approve for
// its officers was agreeing to that.
//
// So the request lands in the pending queue the community already has, a person
// reviews it with the tools they already use, and the integration finds out by
// reading it back. Everything else about it — the history, the judge fields,
// the execution path — is the existing warrant lifecycle untouched.

// devWarrantPageLimit caps a warrant page.
const devWarrantPageLimit = 100

// Warrant types a request may ask for, matching the website's own.
const (
	devWarrantTypeArrest = "arrest"
	devWarrantTypeSearch = "search"
	devWarrantTypeBench  = "bench"
)

// devWarrantTypes lists them, for the error message that names the valid ones.
func devWarrantTypes() []string {
	return []string{devWarrantTypeArrest, devWarrantTypeSearch, devWarrantTypeBench}
}

// devSafeWarrantType validates a type and returns our own constant.
//
// Returning the constant rather than the caller's string, for the same reason
// the status and kind helpers do: the value is stored and may later be
// compared against, and a value this file constructed is one a reader can
// follow. A bool check followed by using the original is not the same thing.
func devSafeWarrantType(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", devWarrantTypeArrest:
		// Arrest is the default, because it is what nearly every request is.
		return devWarrantTypeArrest, nil
	case devWarrantTypeSearch:
		return devWarrantTypeSearch, nil
	case devWarrantTypeBench:
		return devWarrantTypeBench, nil
	}
	return "", fmt.Errorf("warrantType must be one of: %s", strings.Join(devWarrantTypes(), ", "))
}

type devWarrantRequestBody struct {
	// ExternalID is the integration's own id for this request, and what makes
	// the write idempotent: a retried request must not put a second warrant in
	// a judge's queue.
	ExternalID string `json:"externalId"`
	Source     string `json:"source"`

	WarrantType string `json:"warrantType"`

	// RequestingOfficer is the player asking. Their community access is what
	// is checked; the key's scope only decides whether it may ask at all.
	RequestingOfficer string `json:"requestingOfficer"`
	DepartmentID      string `json:"departmentId"`

	// Accused names the subject. A player reference resolves to a real
	// account; the name fields cover a suspect the game knows by name only.
	Accused           string `json:"accused"`
	AccusedFirstName  string `json:"accusedFirstName"`
	AccusedLastName   string `json:"accusedLastName"`
	AccusedCivilianID string `json:"accusedCivilianId"`

	ProbableCause  string   `json:"probableCause"`
	Charges        []string `json:"charges"`
	SearchLocation string   `json:"searchLocation"`
}

// devWarrantView is how a warrant reads on this surface.
//
// Trimmed to what an integration can act on. The judge's own notes are
// included because an officer in the game needs to be told why a request was
// refused; nothing about the reviewer beyond their display name is.
type devWarrantView struct {
	ID          string `json:"id"`
	ExternalID  string `json:"externalId,omitempty"`
	Source      string `json:"source,omitempty"`
	WarrantType string `json:"warrantType"`
	Status      string `json:"status"`

	AccusedID        string `json:"accusedId,omitempty"`
	AccusedFirstName string `json:"accusedFirstName,omitempty"`
	AccusedLastName  string `json:"accusedLastName,omitempty"`

	ProbableCause  string   `json:"probableCause,omitempty"`
	Charges        []string `json:"charges"`
	SearchLocation string   `json:"searchLocation,omitempty"`

	RequestingOfficerID   string `json:"requestingOfficerId,omitempty"`
	RequestingOfficerName string `json:"requestingOfficerName,omitempty"`
	DepartmentID          string `json:"departmentId,omitempty"`

	JudgeName  string `json:"judgeName,omitempty"`
	JudgeNotes string `json:"judgeNotes,omitempty"`
	ReviewedAt string `json:"reviewedAt,omitempty"`

	ExpiresAt string `json:"expiresAt,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`

	// Approved is the question a game server is actually asking. Spelled out
	// so nobody has to decide whether "executed" counts.
	Approved bool `json:"approved"`
	// AwaitingReview says a person has not looked at it yet, which is the
	// state every request this surface creates starts in.
	AwaitingReview bool `json:"awaitingReview"`
}

// DevListWarrantsHandler lists a community's warrants.
//
// GET /api/v1/dev/communities/{communityId}/warrants — warrants:read
//
// Community-scoped and paginated. Filterable by status and by the accused, so
// the question a game server actually asks — "is there a warrant out for this
// person?" — is one request rather than a scan.
func (d DeveloperAPI) DevListWarrantsHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}
	if d.WarrantDB == nil {
		devError(w, http.StatusInternalServerError, "server_error", "Warrants are not available.", nil)
		return
	}

	filter := bson.M{"warrant.activeCommunityID": devEq(cc.Community.ID.Hex())}

	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		status, err := devSafeWarrantStatus(raw)
		if err != nil {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_status", err.Error(), nil)
			return
		}
		filter["warrant.status"] = devEq(status)
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Narrowing by the accused goes through the same player resolution every
	// other reference does, so a key cannot name someone by an id it invented.
	if ref := strings.TrimSpace(r.URL.Query().Get("accused")); ref != "" {
		player, err := d.resolvePlayerRef(ctx, dc, ref, "", false)
		if err != nil {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_player", "accused: "+err.Error(), nil)
			return
		}
		filter["warrant.accusedID"] = devEq(player.UserID)
	}

	page, limit := devPageParams(r, devWarrantPageLimit)
	total, _ := d.WarrantDB.CountDocuments(ctx, filter)

	opts := options.Find().
		SetSort(bson.D{{Key: "warrant.createdAt", Value: -1}, {Key: "_id", Value: -1}}).
		SetSkip(int64((page - 1) * limit)).
		SetLimit(int64(limit))

	warrants, err := d.WarrantDB.Find(ctx, filter, opts)
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not list those warrants.", nil)
		return
	}

	views := make([]devWarrantView, 0, len(warrants))
	for _, warrant := range warrants {
		views = append(views, devWarrantViewOf(warrant))
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       views,
		"totalCount": total,
		"page":       page,
		"limit":      limit,
	})
}

// DevGetWarrantHandler reads one warrant by our id.
//
// GET /api/v1/dev/communities/{communityId}/warrants/{warrantId} — warrants:read
func (d DeveloperAPI) DevGetWarrantHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := DevContextFrom(r.Context()); !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}
	if d.WarrantDB == nil {
		devError(w, http.StatusInternalServerError, "server_error", "Warrants are not available.", nil)
		return
	}

	_, oid, err := devSafeObjectID(mux.Vars(r)["warrantId"])
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_warrant", "That warrant id is not valid.", nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Community is in the filter, not checked after the read, so a warrant id
	// from another community is a 404 and not a leak of its existence.
	warrant, err := d.WarrantDB.FindOne(ctx, bson.M{
		"_id":                       devEq(oid),
		"warrant.activeCommunityID": devEq(cc.Community.ID.Hex()),
	})
	if err != nil || warrant == nil {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "warrant_not_found",
			"No warrant in this community has that id.", nil)
		return
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"warrant": devWarrantViewOf(*warrant)})
}

// DevRequestWarrantHandler asks for a warrant on an officer's behalf.
//
// POST /api/v1/dev/communities/{communityId}/warrants/requests — warrants:write
//
// Always pending. See this file's opening comment for why a key does not get
// the community's auto-approve shortcut: it is a convenience for a person
// making one visible decision, not a behaviour to hand to a trigger that fires
// on its own and retries.
//
// Idempotent on (community, source, externalId): a retry returns the pending
// request it already made, so a queue replay cannot flood a judge.
func (d DeveloperAPI) DevRequestWarrantHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}
	if d.WarrantDB == nil {
		devError(w, http.StatusInternalServerError, "server_error", "Warrants are not available.", nil)
		return
	}

	var req devWarrantRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}

	externalID, source, err := normalizeExternalRef(req.ExternalID, req.Source, dc.Key.Name)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_external_id", err.Error(), nil)
		return
	}

	warrantType, err := devSafeWarrantType(req.WarrantType)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_warrant_type", err.Error(),
			map[string]interface{}{"warrantTypes": devWarrantTypes()})
		return
	}

	probableCause := truncateReportText(req.ProbableCause, maxReportSummaryLen)
	if probableCause == "" {
		// Not a formality. A judge cannot review a request that does not say
		// what it is for, and a request with nothing in this field would sit in
		// the queue until somebody deleted it.
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "probable_cause_required",
			"probableCause is required: a reviewer has to be told what the warrant is for.", nil)
		return
	}

	// The officer asking, and their access. Requesting a warrant is a police
	// action, so the bar is the department membership the CAD itself requires —
	// not a key scope, which only decides whether the integration may ask.
	officer, ok := d.requireReportingOfficer(w, r, dc, cc, req.RequestingOfficer)
	if !ok {
		return
	}
	departmentID, ok := d.resolveReportDepartment(w, r, cc, officer, req.DepartmentID)
	if !ok {
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	accused, ok := d.resolveWarrantAccused(w, r, dc, cc, req)
	if !ok {
		return
	}

	charges, ok := d.resolveReportCharges(w, r, cc, req.Charges)
	if !ok {
		return
	}

	existing, err := d.findWarrantByExternalRef(ctx, cc.Community.ID.Hex(), source, externalID)
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not look up that request.", nil)
		return
	}
	if existing != nil {
		// A repeat is answered with the request that already exists, at
		// whatever stage it has reached. Never a second warrant, and never an
		// error the caller has to special-case.
		d.RecordOutcome(r, http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"warrant": devWarrantViewOf(*existing),
			"created": false,
		})
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	warrant := models.Warrant{ID: primitive.NewObjectID()}
	warrant.Details = models.WarrantDetails{
		WarrantType: warrantType,
		// Pending, always. The community's approval mode is honoured by the
		// reviewer it points at, not bypassed by a script.
		Status:                models.WarrantStatusPending,
		AccusedID:             accused.UserID,
		AccusedFirstName:      accused.FirstName,
		AccusedLastName:       accused.LastName,
		ProbableCause:         probableCause,
		Charges:               chargeNames(charges),
		SearchLocation:        truncateDispatchText(req.SearchLocation),
		RequestingOfficerID:   officer.UserID,
		RequestingOfficerName: officer.Username,
		DepartmentID:          departmentID,
		Source:                source,
		ExternalID:            externalID,
		CreatedByKeyID:        dc.Key.ID.Hex(),
		RecordSource:          devReportRecordSource(dc, source, externalID),
		ActiveCommunityID:     cc.Community.ID.Hex(),
		CreatedAt:             now,
		UpdatedAt:             now,
		History: []models.WarrantHistoryEntry{{
			Action:   "created",
			UserID:   officer.UserID,
			UserName: officer.Username,
			// The history entry names the integration, so a judge reading it
			// knows a script raised this and not the officer at their desk.
			Notes:     "Requested through the Developer API by " + source + ". Awaiting review.",
			Timestamp: now,
		}},
	}

	if _, err := d.WarrantDB.InsertOne(ctx, warrant); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not record that request.", nil)
		return
	}

	logAudit(d.ALDB, cc.Community.ID, "warrant.requested_by_developer_key", "developer_api",
		officer.UserID, officer.Username, warrant.ID.Hex(), warrantType,
		map[string]interface{}{
			"keyId":      dc.Key.ID.Hex(),
			"externalId": externalID,
			"source":     source,
		})

	d.RecordOutcome(r, http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"warrant": devWarrantViewOf(warrant),
		"created": true,
	})
}

// ---- helpers ----

// devWarrantAccused is the subject of a request, resolved as far as we can.
type devWarrantAccused struct {
	UserID    string
	FirstName string
	LastName  string
}

// resolveWarrantAccused works out who a request is against.
//
// A player reference resolves to a real account and must be a member, like
// every other reference on this surface. Names alone are accepted because a
// game frequently knows a suspect only by name, and refusing those would push
// integrations into attaching the warrant to the wrong person.
func (d DeveloperAPI) resolveWarrantAccused(w http.ResponseWriter, r *http.Request, dc *DevAPIContext, cc *CommunityContext, req devWarrantRequestBody) (devWarrantAccused, bool) {
	out := devWarrantAccused{
		FirstName: truncateShort(req.AccusedFirstName),
		LastName:  truncateShort(req.AccusedLastName),
	}

	ref := strings.TrimSpace(req.Accused)
	if ref == "" {
		if out.FirstName == "" && out.LastName == "" {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "accused_required",
				"Name the accused, either as accused (provider:identifier) or as "+
					"accusedFirstName and accusedLastName.", nil)
			return out, false
		}
		return out, true
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	player, err := d.resolvePlayerRef(ctx, dc, ref, "", false)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_player", "accused: "+err.Error(), nil)
		return out, false
	}
	synthetic := d.playerIsSyntheticForKey(ctx, dc, player)
	if !synthetic &&
		cc.Community.Details.OwnerID != player.UserID &&
		!isApprovedMemberOfCommunity(ctx, d.UDB, player.UserID, cc.Community.ID.Hex()) {
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "accused_not_a_member",
			"The accused is not an approved member of this community.",
			map[string]interface{}{"playerRef": ref})
		return out, false
	}
	out.UserID = player.UserID

	// A named character wins over the account's username: a warrant is against
	// the character the officer dealt with, not the person's login.
	if raw := strings.TrimSpace(req.AccusedCivilianID); raw != "" {
		civilian, ok := d.requireOwnedCivilian(w, r, cc, player, raw)
		if !ok {
			return out, false
		}
		if out.FirstName == "" {
			out.FirstName = civilian.Details.FirstName
		}
		if out.LastName == "" {
			out.LastName = civilian.Details.LastName
		}
	}
	if out.FirstName == "" && out.LastName == "" {
		out.FirstName = player.Username
	}
	return out, true
}

// findWarrantByExternalRef resolves a request by the integration's own id,
// scoped by community.
func (d DeveloperAPI) findWarrantByExternalRef(ctx context.Context, communityID, source, externalID string) (*models.Warrant, error) {
	warrants, err := d.WarrantDB.Find(ctx, bson.M{
		"warrant.activeCommunityID": devEq(communityID),
		"warrant.source":            devEq(source),
		"warrant.externalId":        devEq(externalID),
	}, options.Find().SetLimit(1))
	if err != nil {
		return nil, err
	}
	if len(warrants) == 0 {
		return nil, nil
	}
	return &warrants[0], nil
}

// devSafeWarrantStatus validates a status used to filter a list.
func devSafeWarrantStatus(raw string) (string, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	for _, valid := range models.WarrantStatuses() {
		if status == valid {
			return valid, nil
		}
	}
	return "", fmt.Errorf("status must be one of: %s", strings.Join(models.WarrantStatuses(), ", "))
}

// devWarrantViewOf shapes a warrant for the wire.
func devWarrantViewOf(warrant models.Warrant) devWarrantView {
	details := warrant.Details
	view := devWarrantView{
		ID:                    warrant.ID.Hex(),
		ExternalID:            details.ExternalID,
		Source:                details.Source,
		WarrantType:           details.WarrantType,
		Status:                details.Status,
		AccusedID:             details.AccusedID,
		AccusedFirstName:      details.AccusedFirstName,
		AccusedLastName:       details.AccusedLastName,
		ProbableCause:         details.ProbableCause,
		Charges:               nonNilStringSlice(details.Charges),
		SearchLocation:        details.SearchLocation,
		RequestingOfficerID:   details.RequestingOfficerID,
		RequestingOfficerName: details.RequestingOfficerName,
		DepartmentID:          details.DepartmentID,
		JudgeName:             details.JudgeName,
		JudgeNotes:            details.JudgeNotes,
		ReviewedAt:            formatDevDateTime(details.ReviewedAt),
		ExpiresAt:             formatDevDateTime(details.ExpirationDate),
		CreatedAt:             formatDevDateTime(details.CreatedAt),
		UpdatedAt:             formatDevDateTime(details.UpdatedAt),
		Approved:              details.Status == models.WarrantStatusApproved,
		AwaitingReview:        details.Status == models.WarrantStatusPending,
	}
	return view
}
