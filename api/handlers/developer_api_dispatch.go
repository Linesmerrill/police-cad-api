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

// Dispatch calls as an integration needs them, rather than as a person clicking
// a form needs them.
//
// The difference that drives this whole file: a game server owns its own
// incident state and ours is a copy. It will reconnect, replay a queue, retry a
// timeout, and restart mid-incident, and none of that may produce a second call
// on a dispatch board. So a call made through this surface is addressed by the
// *caller's* id for it, not ours, and every write is idempotent on that id.
//
// That is also why the caller never has to store our id to carry on working: by
// the time it has lost track of our id is exactly when it most needs to find
// the call again.

// maxExternalIDLen bounds an integration's own identifier.
const maxExternalIDLen = 128

// maxDispatchTextLen bounds the free-text fields a game script fills in.
const maxDispatchTextLen = 2000

type devCallWriteRequest struct {
	// ExternalID is the integration's own id for this incident. Required: it
	// is what makes every write on this surface idempotent.
	ExternalID string `json:"externalId"`
	// Source names the integration, for example "SixPD". Defaults to the
	// key's name so a single-integration developer need not repeat themselves.
	Source string `json:"source"`

	Title string `json:"title"`
	// Description is a pointer for the same reason the lists below are:
	// omitted leaves the current text alone, and an explicit "" clears it.
	// A plain string cannot tell those apart, so an upsert that only sent a
	// title used to blank the description.
	Description *string `json:"description"`
	Location    string `json:"location"`
	Type        string `json:"type"`
	Priority    string `json:"priority"`

	// Caller is optional: a callout a script generates has no player behind it.
	Caller string `json:"caller"`
	// AssignedUnits are player references. Replaces the assignment outright
	// rather than merging, because a dispatch board that drifts out of step
	// with the game is worse than one that lags it.
	//
	// Pointers so that three cases stay distinct, which the OpenAPI already
	// promised and the code did not honour: omitted (nil) leaves the current
	// value alone, an explicit [] clears it, and a non-empty list replaces it.
	// As plain slices, an upsert that simply did not mention assignedUnits
	// wiped the assignment.
	AssignedUnits *[]string `json:"assignedUnits"`
	Departments   *[]string `json:"departments"`

	// Disposition is how it ended. Only meaningful on close.
	Disposition string `json:"disposition"`
}

// devCallView is the shape every dispatch endpoint answers with.
//
// It always carries both ids. The caller's own id is what they look the call up
// by, and ours is what a deep link into the CAD needs.
type devCallView struct {
	ID            string   `json:"id"`
	ExternalID    string   `json:"externalId,omitempty"`
	Source        string   `json:"source,omitempty"`
	Title         string   `json:"title"`
	Description   string   `json:"description,omitempty"`
	Location      string   `json:"location,omitempty"`
	Type          string   `json:"type,omitempty"`
	Priority      string   `json:"priority,omitempty"`
	Status        string   `json:"status"`
	Disposition   string   `json:"disposition,omitempty"`
	AssignedUnits []string `json:"assignedUnits"`
	Departments   []string `json:"departments"`
	CreatedAt     string   `json:"createdAt,omitempty"`
	UpdatedAt     string   `json:"updatedAt,omitempty"`
	// Link opens this call in the CAD, so a game server can put a URL in a
	// player's chat rather than an id they cannot use.
	Link string `json:"link,omitempty"`
}

// DevUpsertCallHandler creates a dispatch call, or updates the one that already
// carries this external id.
//
// POST /api/v1/dev/communities/{communityId}/dispatch/calls — calls:write
//
// Idempotent on (community, source, externalId). Calling it twice with the same
// external id updates rather than duplicating, which is what makes a reconnect
// and queue replay safe. The response says which happened.
func (d DeveloperAPI) DevUpsertCallHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var req devCallWriteRequest
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
	title := strings.TrimSpace(req.Title)
	if title == "" {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_title", "title is required.", nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	var assigned []string
	if req.AssignedUnits != nil {
		var ok bool
		assigned, ok = d.resolveAssignedUnits(w, r, dc, cc, *req.AssignedUnits)
		if !ok {
			return
		}
	}

	callerID, callerName := "", ""
	if ref := strings.TrimSpace(req.Caller); ref != "" {
		player, allowed := d.requireLinkedMember(w, r, dc, cc, ref)
		if !allowed {
			return
		}
		callerID, callerName = player.UserID, player.Username
	}

	existing, err := d.findCallByExternalRef(ctx, cc.Community.ID.Hex(), source, externalID)
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not look up that call.", nil)
		return
	}

	now := time.Now()
	if existing != nil {
		set := bson.M{
			"call.title":     title,
			"call.updatedAt": now,
		}
		// Only what the caller actually sent. Writing these unconditionally is
		// what cleared an assignment on an upsert that never mentioned one.
		if req.Description != nil {
			set["call.details"] = truncateDispatchText(*req.Description)
		}
		if req.Departments != nil {
			set["call.departments"] = nonNilStringSlice(*req.Departments)
		}
		if req.AssignedUnits != nil {
			set["call.assignedTo"] = nonNilStringSlice(assigned)
		}
		applyOptionalDispatchFields(set, req)

		updated, uErr := d.CallDB.UpdateOne(ctx, bson.M{"_id": devEq(existing.ID)}, bson.M{"$set": set})
		if uErr != nil || updated == nil {
			devError(w, http.StatusInternalServerError, "server_error", "Could not update that call.", nil)
			return
		}

		// Two events on one write when the assignment changed, because they
		// are two different things to a subscriber: "this call was edited" and
		// "these units are on it". A client that only wants the second should
		// not have to diff every update to find it.
		event := devKeyEvent(dc, models.EventCallUpdated, cc.Community.ID.Hex())
		event.ResourceID = updated.ID
		event.ResourceExternalID = externalID
		event.ResourceSource = source
		event.Data = map[string]string{"title": title, "status": "open"}
		d.emit(event)
		if req.AssignedUnits != nil {
			assignedEvent := devKeyEvent(dc, models.EventCallAssigned, cc.Community.ID.Hex())
			assignedEvent.ResourceID = updated.ID
			assignedEvent.ResourceExternalID = externalID
			assignedEvent.ResourceSource = source
			assignedEvent.Data = map[string]string{
				"units": strings.Join(nonNilStringSlice(assigned), ","),
			}
			d.emit(assignedEvent)
		}

		d.RecordOutcome(r, http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"call":    d.callView(ctx, *updated),
			"created": false,
		})
		return
	}

	callID := primitive.NewObjectID().Hex()
	call := models.Call{
		ID: callID,
		Details: models.CallDetails{
			Title:             title,
			Details:           truncateDispatchText(derefString(req.Description)),
			ShortDescription:  title,
			Location:          truncateDispatchText(req.Location),
			Type:              truncateShort(req.Type),
			Priority:          truncateShort(req.Priority),
			Status:            true,
			CommunityID:       cc.Community.ID.Hex(),
			CreatedByID:       callerID,
			CreatedByUsername: callerName,
			Departments:       nonNilStringSlice(derefStrings(req.Departments)),
			AssignedTo:        nonNilStringSlice(assigned),
			CallNotes:         []models.CallNotes{},
			Source:            source,
			ExternalID:        externalID,
			CreatedByKeyID:    dc.Key.ID.Hex(),
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}

	if _, err := d.CallDB.InsertOne(ctx, call); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not create that call.", nil)
		return
	}

	// Tell the live dashboards, the same way the website's own create does, or
	// a call a game raises sits invisible until somebody refreshes.
	notifyNodeServer("call_created", cc.Community.ID.Hex(), map[string]interface{}{"callID": callID})

	created := devKeyEvent(dc, models.EventCallCreated, cc.Community.ID.Hex())
	created.ResourceID = callID
	created.ResourceExternalID = externalID
	created.ResourceSource = source
	created.Data = map[string]string{"title": title, "status": "open"}
	d.emit(created)

	d.RecordOutcome(r, http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"call":    d.callView(ctx, call),
		"created": true,
	})
}

// DevGetCallByExternalIDHandler looks a call up by the caller's own id.
//
// GET /api/v1/dev/communities/{communityId}/dispatch/calls/{externalId} — calls:read
//
// The point of this endpoint is reconciliation after an outage: a game server
// that restarted knows its own incident ids and nothing else, and needs to find
// out what we think the state is before it resumes.
func (d DeveloperAPI) DevGetCallByExternalIDHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	externalID, source, err := normalizeExternalRef(mux.Vars(r)["externalId"], r.URL.Query().Get("source"), dc.Key.Name)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_external_id", err.Error(), nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	call, err := d.findCallByExternalRef(ctx, cc.Community.ID.Hex(), source, externalID)
	if err != nil || call == nil {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "call_not_found",
			"No call in this community carries that external id.",
			map[string]interface{}{"externalId": externalID, "source": source})
		return
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"call": d.callView(ctx, *call)})
}

// DevCloseCallByExternalIDHandler closes a call and records how it ended.
//
// POST /api/v1/dev/communities/{communityId}/dispatch/calls/{externalId}/close — calls:write
//
// Closing an already-closed call is a no-op rather than an error, because a
// retry after a timeout must not become a failure the caller has to handle.
func (d DeveloperAPI) DevCloseCallByExternalIDHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var req devCallWriteRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	externalID, source, err := normalizeExternalRef(mux.Vars(r)["externalId"], req.Source, dc.Key.Name)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_external_id", err.Error(), nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	call, err := d.findCallByExternalRef(ctx, cc.Community.ID.Hex(), source, externalID)
	if err != nil || call == nil {
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "call_not_found",
			"No call in this community carries that external id.", nil)
		return
	}

	if !call.Details.Status {
		d.RecordOutcome(r, http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"call": d.callView(ctx, *call), "closed": false,
			"message": "That call was already closed.",
		})
		return
	}

	set := bson.M{"call.status": false, "call.updatedAt": time.Now()}
	if disposition := truncateShort(req.Disposition); disposition != "" {
		set["call.disposition"] = disposition
	}

	updated, err := d.CallDB.UpdateOne(ctx, bson.M{"_id": devEq(call.ID)}, bson.M{"$set": set})
	if err != nil || updated == nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not close that call.", nil)
		return
	}

	closed := devKeyEvent(dc, models.EventCallClosed, cc.Community.ID.Hex())
	closed.ResourceID = call.ID
	closed.ResourceExternalID = externalID
	closed.ResourceSource = source
	closed.Data = map[string]string{
		"status":      "closed",
		"disposition": truncateShort(req.Disposition),
	}
	d.emit(closed)

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"call": d.callView(ctx, *updated), "closed": true,
	})
}

// ---- helpers ----

// normalizeExternalRef validates an integration's own identifier and the source
// that scopes it.
//
// Source defaults to the key's name so a developer running one integration need
// not repeat it on every call, while a developer running two can keep their id
// spaces apart.
func normalizeExternalRef(externalID, source, keyName string) (string, string, error) {
	id, err := devSafeExternalID(externalID)
	if err != nil {
		return "", "", err
	}

	// Source scopes the id space. It falls back to the key's name, which we
	// control the shape of, and then to a constant, so it is only validated
	// when the caller supplied one.
	src := strings.TrimSpace(source)
	if src == "" {
		src = strings.TrimSpace(keyName)
	}
	if src == "" {
		src = "developer_api"
	}
	if len(src) > 60 {
		src = src[:60]
	}
	safeSource, err := devSafeScalar(src, 60, "source")
	if err != nil {
		return "", "", err
	}
	return id, safeSource, nil
}

// findCallByExternalRef resolves a call by the caller's own id.
//
// Scoped by community in the query, so an external id a developer reuses across
// two of their communities resolves to the right call in each.
func (d DeveloperAPI) findCallByExternalRef(ctx context.Context, communityID, source, externalID string) (*models.Call, error) {
	calls, err := d.CallDB.Find(ctx, bson.M{
		"call.communityID": devEq(communityID),
		"call.source":      devEq(source),
		"call.externalId":  devEq(externalID),
	}, options.Find().SetLimit(1))
	if err != nil {
		return nil, err
	}
	if len(calls) == 0 {
		return nil, nil
	}
	return &calls[0], nil
}

// resolveAssignedUnits turns player references into user ids, checking each one
// is a member.
//
// All or nothing, unlike the bulk status endpoint. An assignment list is a
// statement about who is on a call, and silently dropping one name would show
// dispatch a roster that is quietly wrong — worse than a refusal the caller can
// act on.
func (d DeveloperAPI) resolveAssignedUnits(w http.ResponseWriter, r *http.Request, dc *DevAPIContext, cc *CommunityContext, refs []string) ([]string, bool) {
	out := []string{}
	if len(refs) == 0 {
		return out, true
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	for _, ref := range refs {
		player, err := d.resolvePlayerRef(ctx, dc, ref, "", false)
		if err != nil {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_player",
				fmt.Sprintf("assignedUnits: %s", err.Error()),
				map[string]interface{}{"playerRef": ref})
			return nil, false
		}
		if cc.Community.Details.OwnerID != player.UserID &&
			!isApprovedMemberOfCommunity(ctx, d.UDB, player.UserID, cc.Community.ID.Hex()) {
			d.RecordOutcome(r, http.StatusForbidden)
			devError(w, http.StatusForbidden, "player_not_a_member",
				"A player in assignedUnits is not an approved member of this community.",
				map[string]interface{}{"playerRef": ref})
			return nil, false
		}
		if !containsString(out, player.UserID) {
			out = append(out, player.UserID)
		}
	}
	return out, true
}

// derefString reads an optional string, treating omitted as empty. Only for
// the create path, where there is nothing to leave alone.
func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// derefStrings reads an optional list, treating omitted as empty. Only for the
// create path, where there is nothing to leave alone.
func derefStrings(v *[]string) []string {
	if v == nil {
		return nil
	}
	return *v
}

// applyOptionalDispatchFields sets only the dispatch fields the caller sent.
//
// Absent means "leave it alone", not "clear it". A game server sending a status
// update should not have to resend the location and type it set when the call
// was raised.
func applyOptionalDispatchFields(set bson.M, req devCallWriteRequest) {
	if v := truncateDispatchText(req.Location); v != "" {
		set["call.location"] = v
	}
	if v := truncateShort(req.Type); v != "" {
		set["call.type"] = v
	}
	if v := truncateShort(req.Priority); v != "" {
		set["call.priority"] = v
	}
	if v := truncateShort(req.Disposition); v != "" {
		set["call.disposition"] = v
	}
}

// callView shapes a call for the developer surface, resolving assigned units to
// something a game can display.
func (d DeveloperAPI) callView(ctx context.Context, call models.Call) devCallView {
	status := "open"
	if !call.Details.Status {
		status = "closed"
	}

	view := devCallView{
		ID:            call.ID,
		ExternalID:    call.Details.ExternalID,
		Source:        call.Details.Source,
		Title:         call.Details.Title,
		Description:   call.Details.Details,
		Location:      call.Details.Location,
		Type:          call.Details.Type,
		Priority:      call.Details.Priority,
		Status:        status,
		Disposition:   call.Details.Disposition,
		AssignedUnits: nonNilStringSlice(call.Details.AssignedTo),
		Departments:   nonNilStringSlice(call.Details.Departments),
		CreatedAt:     formatDispatchTime(call.Details.CreatedAt),
		UpdatedAt:     formatDispatchTime(call.Details.UpdatedAt),
		Link:          dispatchCallLink(call.Details.CommunityID, call.ID),
	}
	_ = ctx
	return view
}

// dispatchCallLink is a deep link into the CAD for a call.
func dispatchCallLink(communityID, callID string) string {
	base := publicWebBaseURL()
	if base == "" || communityID == "" {
		return ""
	}
	return fmt.Sprintf("%s/community/%s?call=%s", base, communityID, callID)
}

// formatDispatchTime renders the call model's loosely-typed timestamps.
//
// CreatedAt is an interface{} on this model and has been written as a
// time.Time, a primitive.DateTime and a string over the years, so each is
// handled rather than assumed.
func formatDispatchTime(value interface{}) string {
	switch v := value.(type) {
	case time.Time:
		return v.UTC().Format(time.RFC3339)
	case primitive.DateTime:
		return v.Time().UTC().Format(time.RFC3339)
	case string:
		return v
	default:
		return ""
	}
}

func truncateDispatchText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxDispatchTextLen {
		return strings.TrimSpace(s[:maxDispatchTextLen])
	}
	return s
}

func truncateShort(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 60 {
		return strings.TrimSpace(s[:60])
	}
	return s
}

func nonNilStringSlice(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
