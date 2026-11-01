package handlers

import (
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

// The live CAD surface: who is on duty, their status, calls and BOLOs.
//
// This is the part a FiveM server hits continuously, so the shape of it is
// driven by that load rather than by what reads nicely. A server ticking once a
// second with 64 players is 64 requests per second if a mod loops per player —
// 3,840 a minute, over even the paid limit — so the status endpoint takes a
// batch. One call per tick is 60 a minute and inside the free limit.
//
// The single-player status endpoint exists too, because a mod reacting to one
// player going on duty should not have to assemble a batch of one. The docs
// lead with the bulk form.

// maxBulkUnitStatus is how many players one status call may carry.
//
// 100 is above any plausible FiveM slot count, so a mod never has to paginate a
// tick, and low enough that one request cannot become an unbounded write.
const maxBulkUnitStatus = 100

// ---- units ----

// DevListUnitsHandler returns who is on duty.
//
// GET /api/v1/dev/communities/{communityId}/units — units:read
func (d DeveloperAPI) DevListUnitsHandler(w http.ResponseWriter, r *http.Request) {
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	limit, page, skip := api.ParseLimitPage(r, 50, maxDevRecordPageLimit)
	cur, err := d.UDB.Find(ctx, bson.M{
		"user.communities": bson.M{"$elemMatch": bson.M{
			"communityId": devEq(cc.Community.ID.Hex()),
			"status":      devEq(models.CommunityMemberStatusApproved),
		}},
	}, options.Find().SetLimit(limit).SetSkip(skip).SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not load units.", nil)
		return
	}
	var users []models.User
	if err := cur.All(ctx, &users); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not load units.", nil)
		return
	}

	onDutyOnly := strings.EqualFold(r.URL.Query().Get("onDuty"), "true")

	units := make([]map[string]interface{}, 0, len(users))
	for _, u := range users {
		// The community's members map carries the on-duty state. It is NOT a
		// membership source — it only gains a row once somebody sets a ten-code
		// — which is exactly why membership came from the user documents above
		// and only the duty detail is read from here.
		detail, hasDetail := cc.Community.Details.Members[u.ID]
		if onDutyOnly && !hasDetail {
			continue
		}
		unit := map[string]interface{}{
			"playerId":       u.ID,
			"username":       u.Details.Username,
			"callSign":       u.Details.CallSign,
			"isOnline":       u.Details.IsOnline,
			"gameIdentities": u.Details.GameIdentities,
		}
		if hasDetail {
			unit["tenCodeId"] = detail.TenCodeID
			unit["activeDepartmentId"] = detail.ActiveDepartmentID
			unit["activeDepartmentName"] = detail.ActiveDepartmentName
			unit["departmentCallSigns"] = detail.DepartmentCallSigns
			unit["location"] = detail.Location
			if code := findTenCode(&cc.Community, detail.TenCodeID); code != nil {
				unit["tenCode"] = map[string]interface{}{
					"id":          code.ID.Hex(),
					"code":        code.Code,
					"description": code.Description,
					"category":    code.Category,
				}
			}
		}
		units = append(units, unit)
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"units": units, "page": page, "limit": limit,
	})
}

type devUnitStatusEntry struct {
	// Player is the unit, as provider:identifier.
	Player string `json:"player"`
	// TenCodeID is the status to set. Must be one of the community's own codes.
	TenCodeID string `json:"tenCodeId"`
	// Location is free text, as the game reports it ("Grapeseed", a street
	// name, a postal). Optional.
	Location string `json:"location"`
	// DepartmentID is the department the unit is working as. Optional.
	DepartmentID string `json:"departmentId"`
}

type devBulkUnitStatusRequest struct {
	Units []devUnitStatusEntry `json:"units"`
}

// DevSetUnitStatusHandler sets one unit's status.
//
// PUT /api/v1/dev/communities/{communityId}/units/{playerRef}/status — units:write
func (d DeveloperAPI) DevSetUnitStatusHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var entry devUnitStatusEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}
	entry.Player = mux.Vars(r)["playerRef"]

	results, applied := d.applyUnitStatuses(w, r, dc, cc, []devUnitStatusEntry{entry})
	if results == nil {
		return
	}

	status := http.StatusOK
	if applied == 0 {
		// One-unit call: a failure is the whole request, so it should not come
		// back as a 200 with a buried error.
		status = http.StatusUnprocessableEntity
	}
	d.RecordOutcome(r, status)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"unit": results[0]})
}

// DevBulkSetUnitStatusHandler sets many units' statuses in one call.
//
// PUT /api/v1/dev/communities/{communityId}/units/status/bulk — units:write
//
// This is the endpoint a FiveM mod should use on a tick. It is partial by
// design: one unknown player in a batch of sixty must not discard the other
// fifty-nine, because a game server cannot usefully retry a whole tick. Each
// entry reports its own outcome.
func (d DeveloperAPI) DevBulkSetUnitStatusHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var req devBulkUnitStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}
	if len(req.Units) == 0 {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_body", "units must not be empty.", nil)
		return
	}
	if len(req.Units) > maxBulkUnitStatus {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "too_many_units",
			fmt.Sprintf("Send at most %d units per call.", maxBulkUnitStatus),
			map[string]interface{}{"limit": maxBulkUnitStatus, "sent": len(req.Units)})
		return
	}

	results, applied := d.applyUnitStatuses(w, r, dc, cc, req.Units)
	if results == nil {
		return
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"units":   results,
		"applied": applied,
		"failed":  len(results) - applied,
	})
}

// applyUnitStatuses validates and writes a batch of status updates.
//
// Returns one result per entry and how many landed. A nil result means the
// whole request was already answered.
func (d DeveloperAPI) applyUnitStatuses(w http.ResponseWriter, r *http.Request, dc *DevAPIContext, cc *CommunityContext, entries []devUnitStatusEntry) ([]map[string]interface{}, int) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	results := make([]map[string]interface{}, 0, len(entries))
	applied := 0

	for _, entry := range entries {
		result := map[string]interface{}{"player": entry.Player}

		player, err := d.resolvePlayerRef(ctx, dc, entry.Player, "", false)
		if err != nil {
			result["ok"] = false
			result["error"] = "invalid_player"
			result["message"] = err.Error()
			results = append(results, result)
			continue
		}
		result["playerId"] = player.UserID

		if cc.Community.Details.OwnerID != player.UserID &&
			!isApprovedMemberOfCommunity(ctx, d.UDB, player.UserID, cc.Community.ID.Hex()) {
			result["ok"] = false
			result["error"] = "player_not_a_member"
			result["message"] = "That player is not an approved member of this community."
			results = append(results, result)
			continue
		}

		// The ten-code has to be one of this community's own. A mod writing an
		// arbitrary id would put a status on the board that no client can
		// render and nobody can clear.
		tenCodeID := ""
		if strings.TrimSpace(entry.TenCodeID) != "" {
			canonical, _, tErr := devSafeObjectID(entry.TenCodeID)
			if tErr != nil {
				result["ok"] = false
				result["error"] = "ten_code_not_found"
				result["message"] = "This community has no ten-code with that id."
				results = append(results, result)
				continue
			}
			tenCodeID = canonical
		}
		if tenCodeID != "" && findTenCode(&cc.Community, tenCodeID) == nil {
			result["ok"] = false
			result["error"] = "ten_code_not_found"
			result["message"] = "This community has no ten-code with that id."
			results = append(results, result)
			continue
		}

		departmentID := ""
		if strings.TrimSpace(entry.DepartmentID) != "" {
			canonical, _, dErr := devSafeObjectID(entry.DepartmentID)
			if dErr != nil {
				result["ok"] = false
				result["error"] = "department_not_found"
				result["message"] = "This community has no department with that id."
				results = append(results, result)
				continue
			}
			departmentID = canonical
		}
		var dept *models.Department
		if departmentID != "" {
			if dept = findCommunityDepartment(&cc.Community, departmentID); dept == nil {
				result["ok"] = false
				result["error"] = "department_not_found"
				result["message"] = "This community has no department with that id."
				results = append(results, result)
				continue
			}
		}

		memberKey, _, keyErr := devSafeObjectID(player.UserID)
		if keyErr != nil {
			result["ok"] = false
			result["error"] = "invalid_player"
			result["message"] = "That player's account id is not usable."
			results = append(results, result)
			continue
		}

		set := bson.M{}
		if tenCodeID != "" {
			set["community.members."+memberKey+".tenCodeID"] = tenCodeID
		}
		if location := strings.TrimSpace(entry.Location); location != "" {
			set["community.members."+memberKey+".location"] = truncateDevLocation(location)
			set["community.members."+memberKey+".locationUpdatedAt"] = primitive.NewDateTimeFromTime(time.Now())
		}
		if dept != nil {
			set["community.members."+memberKey+".activeDepartmentId"] = departmentID
			set["community.members."+memberKey+".activeDepartmentName"] = dept.Name
		}
		if len(set) == 0 {
			result["ok"] = false
			result["error"] = "nothing_to_set"
			result["message"] = "Send at least one of tenCodeId, location or departmentId."
			results = append(results, result)
			continue
		}

		if err := d.CDB.UpdateOne(ctx, bson.M{"_id": devEq(cc.Community.ID)}, bson.M{"$set": set}); err != nil {
			result["ok"] = false
			result["error"] = "server_error"
			result["message"] = "Could not set that status."
			results = append(results, result)
			continue
		}

		result["ok"] = true
		applied++
		results = append(results, result)
	}

	return results, applied
}

// maxDevLocationLen caps a reported location.
//
// Free text straight from a game script, and it ends up on a dispatch board, so
// it is bounded here rather than trusted.
const maxDevLocationLen = 120

func truncateDevLocation(location string) string {
	if len(location) <= maxDevLocationLen {
		return location
	}
	return strings.TrimSpace(location[:maxDevLocationLen])
}

// findTenCode looks a ten-code up by hex id within a community.
func findTenCode(community *models.Community, tenCodeID string) *models.TenCodes {
	oid := objectIDOrZero(tenCodeID)
	if oid.IsZero() {
		return nil
	}
	for i := range community.Details.TenCodes {
		if community.Details.TenCodes[i].ID == oid {
			return &community.Details.TenCodes[i]
		}
	}
	return nil
}

// ---- calls ----

type devCreateCallRequest struct {
	// Caller is optional: a call a script raises has no player behind it.
	Caller      string `json:"caller"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Location is where the call is. The call model has no location field, so
	// it is written into the top of the description text instead of into a new
	// field nothing renders — a location the website and the app cannot show is
	// worse than none, because a player would report it and nobody would see
	// it.
	Location    string   `json:"location"`
	Departments []string `json:"departments"`
}

// DevListCallsHandler returns the community's calls.
//
// GET /api/v1/dev/communities/{communityId}/calls — calls:read
func (d DeveloperAPI) DevListCallsHandler(w http.ResponseWriter, r *http.Request) {
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"call.communityID": devEq(cc.Community.ID.Hex())}
	// Open calls by default: a mod asking for calls almost always wants the
	// ones that still need a unit.
	if !strings.EqualFold(r.URL.Query().Get("includeClosed"), "true") {
		filter["call.status"] = true
	}

	limit, page, skip := api.ParseLimitPage(r, 25, maxDevRecordPageLimit)
	calls, err := d.CallDB.Find(ctx, filter,
		options.Find().SetLimit(limit).SetSkip(skip).SetSort(bson.D{{Key: "_id", Value: -1}}))
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not load calls.", nil)
		return
	}

	out := make([]map[string]interface{}, 0, len(calls))
	for _, c := range calls {
		out = append(out, map[string]interface{}{
			"id":          c.ID,
			"title":       c.Details.Title,
			"description": c.Details.Details,
			"status":      c.Details.Status,
			"assignedTo":  c.Details.AssignedTo,
			"departments": c.Details.Departments,
			"createdAt":   c.Details.CreatedAt,
		})
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"calls": out, "page": page, "limit": limit})
}

// DevCreateCallHandler raises a call.
//
// POST /api/v1/dev/communities/{communityId}/calls — calls:write
func (d DeveloperAPI) DevCreateCallHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var req devCreateCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_title", "title is required.", nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// A named caller has to be a member. An unnamed one is fine: plenty of
	// calls are raised by a script rather than by a person.
	callerID := ""
	callerName := ""
	if ref := strings.TrimSpace(req.Caller); ref != "" {
		player, ok := d.requireLinkedMember(w, r, dc, cc, ref)
		if !ok {
			return
		}
		callerID = player.UserID
		callerName = player.Username
	}

	now := time.Now()
	details := strings.TrimSpace(req.Description)
	if location := strings.TrimSpace(req.Location); location != "" {
		// Prepended rather than stored separately, so every surface that
		// already renders a call's text shows the location too.
		details = strings.TrimSpace("Location: " + location + "\n\n" + details)
	}
	callID := primitive.NewObjectID().Hex()
	call := models.Call{
		ID: callID,
		Details: models.CallDetails{
			Title:             title,
			Details:           details,
			ShortDescription:  title,
			Status:            true,
			CommunityID:       cc.Community.ID.Hex(),
			CreatedByID:       callerID,
			CreatedByUsername: callerName,
			Departments:       req.Departments,
			AssignedTo:        []string{},
			CallNotes:         []models.CallNotes{},
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}

	if _, err := d.CallDB.InsertOne(ctx, call); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not create that call.", nil)
		return
	}

	// Tell the live dashboards, the same way the website's own create does, or
	// a call a mod raises sits invisible until somebody refreshes.
	notifyNodeServer("call_created", cc.Community.ID.Hex(), map[string]interface{}{
		"callID": callID,
	})

	d.RecordOutcome(r, http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"call": map[string]interface{}{
		"id":     callID,
		"title":  title,
		"status": true,
	}})
}

// DevCloseCallHandler closes a call.
//
// POST /api/v1/dev/communities/{communityId}/calls/{callId}/close — calls:write
func (d DeveloperAPI) DevCloseCallHandler(w http.ResponseWriter, r *http.Request) {
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	// The canonical hex from the parsed id. Call ids are stored as strings in
	// this collection, so the filter takes the re-serialized value rather than
	// the caller's.
	callID, _, err := devSafeObjectID(mux.Vars(r)["callId"])
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_call", "That call id is not valid.", nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Scoped by community in the filter, so a key cannot close a call in a
	// community it has no grant for even with a valid call id. UpdateOne on
	// this store returns the matched call, so a miss is distinguishable from a
	// write failure.
	updated, updErr := d.CallDB.UpdateOne(ctx,
		bson.M{"_id": devEq(callID), "call.communityID": devEq(cc.Community.ID.Hex())},
		bson.M{"$set": bson.M{"call.status": false, "call.updatedAt": time.Now()}})
	if updErr != nil || updated == nil {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "call_not_found", "This community has no call with that id.", nil)
		return
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"callId": callID, "closed": true})
}

// ---- BOLOs ----

type devCreateBoloRequest struct {
	Reporter    string `json:"reporter"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Location    string `json:"location"`
	// Plate goes into the description text, for the same reason a call's
	// location does: the BOLO model has no plate field, and a plate stored
	// where no surface renders it is a plate no officer will ever see.
	Plate        string `json:"plate"`
	DepartmentID string `json:"departmentId"`
}

// DevListBolosHandler returns the community's BOLOs.
//
// GET /api/v1/dev/communities/{communityId}/bolos — bolos:read
func (d DeveloperAPI) DevListBolosHandler(w http.ResponseWriter, r *http.Request) {
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	limit, page, skip := api.ParseLimitPage(r, 25, maxDevRecordPageLimit)
	bolos, err := d.BoloDB.Find(ctx, bson.M{"bolo.communityID": devEq(cc.Community.ID.Hex())},
		options.Find().SetLimit(limit).SetSkip(skip).SetSort(bson.D{{Key: "_id", Value: -1}}))
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not load BOLOs.", nil)
		return
	}

	out := make([]map[string]interface{}, 0, len(bolos))
	for _, b := range bolos {
		out = append(out, map[string]interface{}{
			"id":          b.ID.Hex(),
			"title":       b.Details.Title,
			"description": b.Details.Description,
			"location":    b.Details.Location,
			"scope":       b.Details.Scope,
			"status":      b.Details.Status,
			"createdAt":   b.Details.CreatedAt.Time().UTC().Format(time.RFC3339),
		})
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"bolos": out, "page": page, "limit": limit})
}

// DevCreateBoloHandler raises a BOLO.
//
// POST /api/v1/dev/communities/{communityId}/bolos — bolos:write
func (d DeveloperAPI) DevCreateBoloHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var req devCreateBoloRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_title", "title is required.", nil)
		return
	}

	// A BOLO names an officer who raised it, and that officer has to be a real
	// member: a BOLO is acted on by other players, so an unattributable one is
	// worse than none.
	reporter, ok := d.requireLinkedMember(w, r, dc, cc, req.Reporter)
	if !ok {
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	scope := "Community"
	departmentID := ""
	if strings.TrimSpace(req.DepartmentID) != "" {
		canonical, _, dErr := devSafeObjectID(req.DepartmentID)
		if dErr != nil {
			d.RecordOutcome(r, http.StatusNotFound)
			devError(w, http.StatusNotFound, "department_not_found",
				"This community has no department with that id.", nil)
			return
		}
		departmentID = canonical
	}
	if departmentID != "" {
		if findCommunityDepartment(&cc.Community, departmentID) == nil {
			d.RecordOutcome(r, http.StatusNotFound)
			devError(w, http.StatusNotFound, "department_not_found",
				"This community has no department with that id.", nil)
			return
		}
		scope = "Department"
	}

	description := strings.TrimSpace(req.Description)
	if plate := strings.ToUpper(strings.TrimSpace(req.Plate)); plate != "" {
		description = strings.TrimSpace("Plate: " + plate + "\n\n" + description)
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	bolo := models.Bolo{
		ID: primitive.NewObjectID(),
		Details: models.BoloDetails{
			Title:        title,
			Description:  description,
			Location:     strings.TrimSpace(req.Location),
			Status:       true,
			Scope:        scope,
			CommunityID:  cc.Community.ID.Hex(),
			DepartmentID: departmentID,
			ReportedByID: reporter.UserID,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}

	if _, err := d.BoloDB.InsertOne(ctx, bolo); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not create that BOLO.", nil)
		return
	}

	d.RecordOutcome(r, http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"bolo": map[string]interface{}{
		"id":    bolo.ID.Hex(),
		"title": title,
		"scope": scope,
	}})
}

// ---- ten-codes and departments, so a mod can discover ids ----

// DevListTenCodesHandler returns the community's ten-codes.
//
// GET /api/v1/dev/communities/{communityId}/ten-codes — community:read
//
// A mod needs these ids to set a status, and the only alternative is hardcoding
// ids per community, which breaks the moment an owner edits a code.
func (d DeveloperAPI) DevListTenCodesHandler(w http.ResponseWriter, r *http.Request) {
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	codes := make([]map[string]interface{}, 0, len(cc.Community.Details.TenCodes))
	for _, c := range cc.Community.Details.TenCodes {
		codes = append(codes, map[string]interface{}{
			"id":          c.ID.Hex(),
			"code":        c.Code,
			"description": c.Description,
			"category":    c.Category,
		})
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"tenCodes": codes})
}

// DevListDepartmentsHandler returns the community's departments and ranks.
//
// GET /api/v1/dev/communities/{communityId}/departments — community:read
func (d DeveloperAPI) DevListDepartmentsHandler(w http.ResponseWriter, r *http.Request) {
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	departments := make([]map[string]interface{}, 0, len(cc.Community.Details.Departments))
	for _, dept := range cc.Community.Details.Departments {
		ranks := make([]map[string]interface{}, 0, len(dept.Ranks))
		for _, rank := range dept.Ranks {
			ranks = append(ranks, map[string]interface{}{"id": rank.ID.Hex(), "name": rank.Name})
		}
		departments = append(departments, map[string]interface{}{
			"id":    dept.ID.Hex(),
			"name":  dept.Name,
			"ranks": ranks,
		})
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"departments": departments})
}
