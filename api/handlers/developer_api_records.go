package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/models"
)

// The records a player makes in the game: characters, their vehicles, their
// firearms and their licenses.
//
// This is the half of the feature a player actually sees. They build a
// character in the game, and later open the website or the app and find it
// there, which only works if what a mod writes is indistinguishable from what
// the website writes. So these handlers reuse the same models, the same
// community scoping and the same validation, and add exactly two things: the
// record is tied to a linked player, and it is stamped with the key that made
// it.
//
// A key is not above the community's own settings. If a community turns on
// civilian approval, a mod-made character arrives pending like anyone else's;
// if it caps civilians per player, that cap applies. The key is a way for a
// player to act, not a way around the rules the owner set.

// maxDevRecordPageLimit caps a search page.
const maxDevRecordPageLimit = 100

// devRecordSource builds the stamp that makes a key's output findable.
func devRecordSource(dc *DevAPIContext, player *resolvedPlayer) *models.RecordSource {
	src := &models.RecordSource{
		Type:        models.RecordSourceDeveloperAPI,
		KeyID:       dc.Key.ID.Hex(),
		DeveloperID: dc.Key.DeveloperID,
		CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
	}
	if player != nil {
		src.Provider = player.Provider
		src.Identifier = player.Identifier
	}
	return src
}

// recordStamp builds the source stamp for a record a key is writing, settling
// the synthetic question in one place.
//
// Synthetic is inherited as well as declared: a vehicle belonging to a
// character the integration provisioned as an NPC is an NPC's vehicle whether
// or not the request repeated the flag. That matters because the alternative is
// an integration that marks its people correctly and then forgets on one of
// four record types, leaving half its scenery in the name database.
func (d DeveloperAPI) recordStamp(ctx context.Context, dc *DevAPIContext, player *resolvedPlayer, declared bool, source, externalID string) *models.RecordSource {
	synthetic := declared || d.playerIsSyntheticForKey(ctx, dc, player)
	normalizedSource := strings.TrimSpace(source)
	if normalizedSource == "" && externalID != "" {
		normalizedSource = strings.TrimSpace(dc.Key.Name)
	}
	safeSource, err := devSafeScalar(normalizedSource, 60, "source")
	if err != nil {
		safeSource = ""
	}
	safeExternal := ""
	if externalID != "" {
		if v, vErr := devSafeExternalID(externalID); vErr == nil {
			safeExternal = v
		}
	}
	return devSyntheticSourceStamp(dc, player, synthetic, safeSource, safeExternal)
}

// requireLinkedMember resolves a player reference and checks they are an
// approved member of the community being written to.
//
// Every write that touches a person goes through here. Membership is the
// boundary that makes a player reference safe to accept: it does not matter how
// the mod names someone if the name has to resolve to a member of the one
// community this key is allowed into.
func (d DeveloperAPI) requireLinkedMember(w http.ResponseWriter, r *http.Request, dc *DevAPIContext, cc *CommunityContext, ref string) (*resolvedPlayer, bool) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	player, err := d.resolvePlayerRef(ctx, dc, ref, "", false)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_player", err.Error(), nil)
		return nil, false
	}
	if cc.Community.Details.OwnerID != player.UserID &&
		!isApprovedMemberOfCommunity(ctx, d.UDB, player.UserID, cc.Community.ID.Hex()) {
		d.RecordOutcome(r, http.StatusForbidden)
		devError(w, http.StatusForbidden, "player_not_a_member",
			"That player is not an approved member of this community.",
			map[string]interface{}{"playerRef": ref})
		return nil, false
	}
	return player, true
}

// ---- civilians ----

type devCreateCivilianRequest struct {
	// Player owns the character, as provider:identifier.
	Player string `json:"player"`

	FirstName  string `json:"firstName"`
	LastName   string `json:"lastName"`
	Birthday   string `json:"birthday"`
	Gender     string `json:"gender"`
	Race       string `json:"race"`
	HairColor  string `json:"hairColor"`
	EyeColor   string `json:"eyeColor"`
	Height     string `json:"height"`
	Weight     string `json:"weight"`
	Address    string `json:"address"`
	Occupation string `json:"occupation"`
	Image      string `json:"image"`

	// Synthetic declares this record as belonging to a simulated character
	// rather than a person, which keeps it out of the searches and rosters
	// players see. It is also inherited: a record whose owner was provisioned
	// as synthetic is synthetic whether or not this field says so, so an
	// integration marks its NPCs once and everything they own follows.
	Synthetic bool `json:"synthetic"`
	// ExternalID and Source are the integration's own handle for the thing
	// this record stands for. Optional, and only useful for a synthetic
	// record: they are what makes one addressable for cleanup after the
	// integration has forgotten our ids.
	ExternalID string `json:"externalId"`
	Source     string `json:"source"`
}

// DevCreateCivilianHandler creates a character for a linked player.
//
// POST /api/v1/dev/communities/{communityId}/civilians — civilians:write
func (d DeveloperAPI) DevCreateCivilianHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var req devCreateCivilianRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}
	first := strings.TrimSpace(req.FirstName)
	last := strings.TrimSpace(req.LastName)
	if first == "" || last == "" {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_name", "firstName and lastName are required.", nil)
		return
	}

	player, ok := d.requireLinkedMember(w, r, dc, cc, req.Player)
	if !ok {
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// The community's own civilian cap applies. A key is not above the settings
	// the owner chose.
	if cc.Community.Details.CivilianCreationLimitsEnabled && cc.Community.Details.CivilianCreationLimit > 0 {
		existing, _ := d.CivDB.CountDocuments(ctx, bson.M{
			"civilian.userID":            player.UserID,
			"civilian.activeCommunityID": devEq(cc.Community.ID.Hex()),
		})
		if int(existing) >= cc.Community.Details.CivilianCreationLimit {
			d.RecordOutcome(r, http.StatusConflict)
			devError(w, http.StatusConflict, "civilian_limit_reached",
				fmt.Sprintf("This community allows %d characters per player and that player has %d.",
					cc.Community.Details.CivilianCreationLimit, existing),
				map[string]interface{}{"limit": cc.Community.Details.CivilianCreationLimit})
			return
		}
	}

	// And its approval system. A mod-made character arrives pending like
	// anyone else's when the community vets them.
	approvalStatus := ""
	if cc.Community.Details.CivilianApprovalSystemEnabled {
		approvalStatus = "pending"
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	name := first + " " + last
	civilian := models.Civilian{
		ID: primitive.NewObjectID(),
		Details: models.CivilianDetails{
			FirstName:         first,
			LastName:          last,
			Name:              name,
			SearchName:        normalizeDevSearchName(name),
			Birthday:          strings.TrimSpace(req.Birthday),
			Gender:            strings.TrimSpace(req.Gender),
			Race:              strings.TrimSpace(req.Race),
			HairColor:         strings.TrimSpace(req.HairColor),
			EyeColor:          strings.TrimSpace(req.EyeColor),
			Height:            strings.TrimSpace(req.Height),
			Weight:            strings.TrimSpace(req.Weight),
			Address:           strings.TrimSpace(req.Address),
			Occupation:        strings.TrimSpace(req.Occupation),
			Image:             strings.TrimSpace(req.Image),
			ApprovalStatus:    approvalStatus,
			ActiveCommunityID: cc.Community.ID.Hex(),
			UserID:            player.UserID,
			CriminalHistory:   []models.CriminalHistory{},
			CreatedAt:         now,
			UpdatedAt:         now,
			Source:            d.recordStamp(ctx, dc, player, req.Synthetic, req.Source, req.ExternalID),
		},
	}

	if _, err := d.CivDB.InsertOne(ctx, civilian); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not create that character.", nil)
		return
	}

	// One event type for all four record kinds, with the kind in the payload.
	// A subscriber watching "a record appeared" wants one subscription, and the
	// four shapes have nothing in common beyond that.
	recordEvent := devKeyEvent(dc, models.EventRecordCreated, cc.Community.ID.Hex())
	recordEvent.ResourceID = civilian.ID.Hex()
	recordEvent.ResourceExternalID = req.ExternalID
	recordEvent.ResourceSource = req.Source
	recordEvent.Data = map[string]string{
		"recordKind": "civilian",
		"playerRef":  player.Provider + ":" + player.Identifier,
	}
	d.emit(recordEvent)

	d.RecordOutcome(r, http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"civilian": map[string]interface{}{
			"id":             civilian.ID.Hex(),
			"name":           name,
			"playerId":       player.UserID,
			"approvalStatus": approvalStatus,
			"createdAt":      now.Time().UTC().Format(time.RFC3339),
		},
		// Said out loud because a mod author will otherwise wonder why the
		// character does not appear on the roster yet.
		"pendingApproval": approvalStatus == "pending",
	})
}

// DevSearchCiviliansHandler looks characters up by name inside the community.
//
// GET /api/v1/dev/communities/{communityId}/civilians?name= — civilians:read
//
// Scoped to the community by the query itself, not by filtering afterwards, so
// there is no path by which a result from another community could be returned.
func (d DeveloperAPI) DevSearchCiviliansHandler(w http.ResponseWriter, r *http.Request) {
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	name, err := devSafeSearchTerm(r.URL.Query().Get("name"), "name")
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_query", err.Error(), nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	limit, _, skip := api.ParseLimitPage(r, 25, maxDevRecordPageLimit)
	civilians, err := d.CivDB.Find(ctx, applySyntheticScope(bson.M{
		"civilian.activeCommunityID": devEq(cc.Community.ID.Hex()),
		"$or": []bson.M{
			// Both branches are escaped. normalizeDevSearchName only lowercases
			// and collapses whitespace — it does not strip regex
			// metacharacters, so escaping has to happen after it, not instead
			// of it.
			{"civilian.searchName": bson.M{"$regex": regexEscapeDev(normalizeDevSearchName(name)), "$options": "i"}},
			{"civilian.name": bson.M{"$regex": regexEscapeDev(name), "$options": "i"}},
		},
	}, "civilian.source.synthetic", devIncludeSynthetic(r)),
		options.Find().SetLimit(limit).SetSkip(skip).SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not search characters.", nil)
		return
	}

	out := make([]map[string]interface{}, 0, len(civilians))
	for _, c := range civilians {
		out = append(out, devCivilianSummary(c))
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"civilians": out})
}

// devCivilianSummary is what a mod sees of a character.
//
// The CAD-relevant facts, and not the owner's account. A mod running a lookup
// on a traffic stop needs the licence status and the warrants, not the user id
// of whoever is playing them.
func devCivilianSummary(c models.Civilian) map[string]interface{} {
	return map[string]interface{}{
		"id":             c.ID.Hex(),
		"name":           c.Details.Name,
		"firstName":      c.Details.FirstName,
		"lastName":       c.Details.LastName,
		"birthday":       c.Details.Birthday,
		"gender":         c.Details.Gender,
		"race":           c.Details.Race,
		"address":        c.Details.Address,
		"licenseStatus":  c.Details.LicenseStatus,
		"occupation":     c.Details.Occupation,
		"image":          c.Details.Image,
		"approvalStatus": c.Details.ApprovalStatus,
		"deceased":       c.Details.Deceased,
		"onProbation":    c.Details.OnProbation,
		"onParole":       c.Details.OnParole,
	}
}

// ---- vehicles ----

type devCreateVehicleRequest struct {
	Player     string `json:"player"`
	CivilianID string `json:"civilianId"`

	Plate             string `json:"plate"`
	LicensePlateState string `json:"licensePlateState"`
	Vin               string `json:"vin"`
	Make              string `json:"make"`
	Model             string `json:"model"`
	Year              string `json:"year"`
	Color             string `json:"color"`
	Type              string `json:"type"`
	Image             string `json:"image"`

	// Synthetic declares this record as belonging to a simulated character
	// rather than a person, which keeps it out of the searches and rosters
	// players see. It is also inherited: a record whose owner was provisioned
	// as synthetic is synthetic whether or not this field says so, so an
	// integration marks its NPCs once and everything they own follows.
	Synthetic bool `json:"synthetic"`
	// ExternalID and Source are the integration's own handle for the thing
	// this record stands for. Optional, and only useful for a synthetic
	// record: they are what makes one addressable for cleanup after the
	// integration has forgotten our ids.
	ExternalID string `json:"externalId"`
	Source     string `json:"source"`
}

// DevCreateVehicleHandler registers a vehicle to a character.
//
// POST /api/v1/dev/communities/{communityId}/vehicles — vehicles:write
func (d DeveloperAPI) DevCreateVehicleHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var req devCreateVehicleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}
	plate := strings.ToUpper(strings.TrimSpace(req.Plate))
	if plate == "" {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_plate", "plate is required.", nil)
		return
	}

	player, ok := d.requireLinkedMember(w, r, dc, cc, req.Player)
	if !ok {
		return
	}
	civilian, ok := d.requireOwnedCivilian(w, r, cc, player, req.CivilianID)
	if !ok {
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	now := primitive.NewDateTimeFromTime(time.Now())
	vehicle := models.Vehicle{
		ID: primitive.NewObjectID(),
		Details: models.VehicleDetails{
			Plate:             plate,
			LicensePlateState: strings.TrimSpace(req.LicensePlateState),
			Vin:               strings.ToUpper(strings.TrimSpace(req.Vin)),
			Make:              strings.TrimSpace(req.Make),
			Model:             strings.TrimSpace(req.Model),
			Year:              strings.TrimSpace(req.Year),
			Color:             strings.TrimSpace(req.Color),
			Type:              strings.TrimSpace(req.Type),
			Image:             strings.TrimSpace(req.Image),
			LinkedCivilianID:  civilian.ID.Hex(),
			RegisteredOwner:   civilian.Details.Name,
			// Registration and insurance default to valid, and stolen to not,
			// written in the canonical encoding (models/vehicle_flags.go).
			// These fields carry two historical encodings with opposite
			// polarity, so the constants are used rather than a literal.
			ValidRegistration: models.FlagTrue,
			ValidInsurance:    models.FlagTrue,
			IsStolen:          models.FlagFalse,
			ActiveCommunityID: cc.Community.ID.Hex(),
			UserID:            player.UserID,
			CreatedAt:         now,
			UpdatedAt:         now,
			Source:            d.recordStamp(ctx, dc, player, req.Synthetic, req.Source, req.ExternalID),
		},
	}

	if _, err := d.VehDB.InsertOne(ctx, vehicle); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not register that vehicle.", nil)
		return
	}

	// One event type for all four record kinds, with the kind in the payload.
	// A subscriber watching "a record appeared" wants one subscription, and the
	// four shapes have nothing in common beyond that.
	recordEvent := devKeyEvent(dc, models.EventRecordCreated, cc.Community.ID.Hex())
	recordEvent.ResourceID = vehicle.ID.Hex()
	recordEvent.ResourceExternalID = req.ExternalID
	recordEvent.ResourceSource = req.Source
	recordEvent.Data = map[string]string{
		"recordKind": "vehicle",
		"playerRef":  player.Provider + ":" + player.Identifier,
		"plate":      plate,
	}
	d.emit(recordEvent)

	d.RecordOutcome(r, http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"vehicle": map[string]interface{}{
		"id":         vehicle.ID.Hex(),
		"plate":      plate,
		"civilianId": civilian.ID.Hex(),
	}})
}

// DevSearchVehiclesHandler looks a vehicle up by plate inside the community.
//
// GET /api/v1/dev/communities/{communityId}/vehicles?plate= — vehicles:read
func (d DeveloperAPI) DevSearchVehiclesHandler(w http.ResponseWriter, r *http.Request) {
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	plate, err := devSafeSearchTerm(r.URL.Query().Get("plate"), "plate")
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_query", err.Error(), nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	limit, _, skip := api.ParseLimitPage(r, 25, maxDevRecordPageLimit)
	vehicles, err := d.VehDB.Find(ctx, applySyntheticScope(bson.M{
		"vehicle.activeCommunityID": devEq(cc.Community.ID.Hex()),
		"vehicle.plate":             bson.M{"$regex": "^" + regexEscapeDev(plate), "$options": "i"},
	}, "vehicle.source.synthetic", devIncludeSynthetic(r)), options.Find().SetLimit(limit).SetSkip(skip).SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not search vehicles.", nil)
		return
	}

	out := make([]map[string]interface{}, 0, len(vehicles))
	for _, v := range vehicles {
		out = append(out, map[string]interface{}{
			"id":              v.ID.Hex(),
			"plate":           v.Details.Plate,
			"make":            v.Details.Make,
			"model":           v.Details.Model,
			"year":            v.Details.Year,
			"color":           v.Details.Color,
			"registeredOwner": v.Details.RegisteredOwner,
			"civilianId":      v.Details.LinkedCivilianID,
			// Read through the shared helpers on VehicleDetails, because the
			// stored values use two encodings with per-field polarity and a
			// hand-rolled truth test gets them backwards.
			"stolen":            v.Details.Stolen(),
			"validRegistration": v.Details.RegistrationValid(),
			"validInsurance":    v.Details.InsuranceValid(),
		})
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"vehicles": out})
}

// ---- firearms ----

type devCreateFirearmRequest struct {
	Player     string `json:"player"`
	CivilianID string `json:"civilianId"`

	SerialNumber string `json:"serialNumber"`
	Name         string `json:"name"`
	WeaponType   string `json:"weaponType"`
	Caliber      string `json:"caliber"`
	Image        string `json:"image"`
	// Synthetic declares this record as belonging to a simulated character
	// rather than a person, which keeps it out of the searches and rosters
	// players see. It is also inherited: a record whose owner was provisioned
	// as synthetic is synthetic whether or not this field says so, so an
	// integration marks its NPCs once and everything they own follows.
	Synthetic bool `json:"synthetic"`
	// ExternalID and Source are the integration's own handle for the thing
	// this record stands for. Optional, and only useful for a synthetic
	// record: they are what makes one addressable for cleanup after the
	// integration has forgotten our ids.
	ExternalID string `json:"externalId"`
	Source     string `json:"source"`
}

// DevCreateFirearmHandler registers a firearm to a character.
//
// POST /api/v1/dev/communities/{communityId}/firearms — firearms:write
func (d DeveloperAPI) DevCreateFirearmHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var req devCreateFirearmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}
	serial := strings.ToUpper(strings.TrimSpace(req.SerialNumber))
	if serial == "" {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_serial", "serialNumber is required.", nil)
		return
	}

	player, ok := d.requireLinkedMember(w, r, dc, cc, req.Player)
	if !ok {
		return
	}
	civilian, ok := d.requireOwnedCivilian(w, r, cc, player, req.CivilianID)
	if !ok {
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	now := primitive.NewDateTimeFromTime(time.Now())
	firearm := models.Firearm{
		ID: primitive.NewObjectID(),
		Details: models.FirearmDetails{
			SerialNumber:      serial,
			Name:              strings.TrimSpace(req.Name),
			WeaponType:        strings.TrimSpace(req.WeaponType),
			Caliber:           strings.TrimSpace(req.Caliber),
			Image:             strings.TrimSpace(req.Image),
			LinkedCivilianID:  civilian.ID.Hex(),
			RegisteredOwner:   civilian.Details.Name,
			IsStolen:          models.FlagFalse,
			ActiveCommunityID: cc.Community.ID.Hex(),
			UserID:            player.UserID,
			CreatedAt:         now,
			UpdatedAt:         now,
			Source:            d.recordStamp(ctx, dc, player, req.Synthetic, req.Source, req.ExternalID),
		},
	}

	if _, err := d.FirDB.InsertOne(ctx, firearm); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not register that firearm.", nil)
		return
	}

	// One event type for all four record kinds, with the kind in the payload.
	// A subscriber watching "a record appeared" wants one subscription, and the
	// four shapes have nothing in common beyond that.
	recordEvent := devKeyEvent(dc, models.EventRecordCreated, cc.Community.ID.Hex())
	recordEvent.ResourceID = firearm.ID.Hex()
	recordEvent.ResourceExternalID = req.ExternalID
	recordEvent.ResourceSource = req.Source
	recordEvent.Data = map[string]string{
		"recordKind": "firearm",
		"playerRef":  player.Provider + ":" + player.Identifier,
	}
	d.emit(recordEvent)

	d.RecordOutcome(r, http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"firearm": map[string]interface{}{
		"id":           firearm.ID.Hex(),
		"serialNumber": serial,
		"civilianId":   civilian.ID.Hex(),
	}})
}

// DevSearchFirearmsHandler looks a firearm up by serial inside the community.
//
// GET /api/v1/dev/communities/{communityId}/firearms?serial= — firearms:read
func (d DeveloperAPI) DevSearchFirearmsHandler(w http.ResponseWriter, r *http.Request) {
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	serial, err := devSafeSearchTerm(r.URL.Query().Get("serial"), "serial")
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_query", err.Error(), nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	limit, _, skip := api.ParseLimitPage(r, 25, maxDevRecordPageLimit)
	firearms, err := d.FirDB.Find(ctx, applySyntheticScope(bson.M{
		"firearm.activeCommunityID": devEq(cc.Community.ID.Hex()),
		"firearm.serialNumber":      bson.M{"$regex": "^" + regexEscapeDev(serial), "$options": "i"},
	}, "firearm.source.synthetic", devIncludeSynthetic(r)), options.Find().SetLimit(limit).SetSkip(skip).SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not search firearms.", nil)
		return
	}

	out := make([]map[string]interface{}, 0, len(firearms))
	for _, f := range firearms {
		out = append(out, map[string]interface{}{
			"id":              f.ID.Hex(),
			"serialNumber":    f.Details.SerialNumber,
			"name":            f.Details.Name,
			"weaponType":      f.Details.WeaponType,
			"caliber":         f.Details.Caliber,
			"registeredOwner": f.Details.RegisteredOwner,
			"civilianId":      f.Details.LinkedCivilianID,
			"stolen":          f.Details.IsStolen == models.FlagTrue || f.Details.IsStolen == "1",
		})
	}

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"firearms": out})
}

// ---- licenses ----

type devCreateLicenseRequest struct {
	Player         string `json:"player"`
	CivilianID     string `json:"civilianId"`
	Type           string `json:"type"`
	Status         string `json:"status"`
	ExpirationDate string `json:"expirationDate"`
	Notes          string `json:"notes"`

	// Synthetic declares this record as belonging to a simulated character
	// rather than a person, which keeps it out of the searches and rosters
	// players see. It is also inherited: a record whose owner was provisioned
	// as synthetic is synthetic whether or not this field says so, so an
	// integration marks its NPCs once and everything they own follows.
	Synthetic bool `json:"synthetic"`
	// ExternalID and Source are the integration's own handle for the thing
	// this record stands for. Optional, and only useful for a synthetic
	// record: they are what makes one addressable for cleanup after the
	// integration has forgotten our ids.
	ExternalID string `json:"externalId"`
	Source     string `json:"source"`
}

// DevCreateLicenseHandler issues a license to a character.
//
// POST /api/v1/dev/communities/{communityId}/licenses — licenses:write
//
// Written with the modern key set only (type / status / civilianID). The
// collection holds two historical key sets and the API tolerates both on read,
// but a new writer should not add to the legacy half.
func (d DeveloperAPI) DevCreateLicenseHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var req devCreateLicenseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		devError(w, http.StatusBadRequest, "invalid_body", "Could not read that request body.", nil)
		return
	}
	licenseType := strings.TrimSpace(req.Type)
	if licenseType == "" {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_type",
			"type is required, for example \"Driver License\".", nil)
		return
	}

	player, ok := d.requireLinkedMember(w, r, dc, cc, req.Player)
	if !ok {
		return
	}
	civilian, ok := d.requireOwnedCivilian(w, r, cc, player, req.CivilianID)
	if !ok {
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "Approved"
	}
	now := time.Now()
	license := models.License{
		ID: primitive.NewObjectID(),
		Details: models.LicenseDetails{
			Type:              licenseType,
			Status:            status,
			ExpirationDate:    strings.TrimSpace(req.ExpirationDate),
			Notes:             strings.TrimSpace(req.Notes),
			CivilianID:        civilian.ID.Hex(),
			ActiveCommunityID: cc.Community.ID.Hex(),
			UserID:            player.UserID,
			CreatedAt:         now,
			UpdatedAt:         now,
			Source:            d.recordStamp(ctx, dc, player, req.Synthetic, req.Source, req.ExternalID),
		},
	}

	if _, err := d.LicDB.InsertOne(ctx, license); err != nil {
		devError(w, http.StatusInternalServerError, "server_error", "Could not issue that license.", nil)
		return
	}

	// One event type for all four record kinds, with the kind in the payload.
	// A subscriber watching "a record appeared" wants one subscription, and the
	// four shapes have nothing in common beyond that.
	recordEvent := devKeyEvent(dc, models.EventRecordCreated, cc.Community.ID.Hex())
	recordEvent.ResourceID = license.ID.Hex()
	recordEvent.ResourceExternalID = req.ExternalID
	recordEvent.ResourceSource = req.Source
	recordEvent.Data = map[string]string{
		"recordKind":  "license",
		"playerRef":   player.Provider + ":" + player.Identifier,
		"licenseType": licenseType,
	}
	d.emit(recordEvent)

	d.RecordOutcome(r, http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"license": map[string]interface{}{
		"id":         license.ID.Hex(),
		"type":       licenseType,
		"status":     status,
		"civilianId": civilian.ID.Hex(),
	}})
}

// ---- shared ----

// requireOwnedCivilian resolves a character and checks the player owns it and
// it belongs to this community.
//
// Both halves matter. Without the ownership check a mod could hang a vehicle
// off somebody else's character; without the community check it could reach a
// character in a community this key has no grant for.
func (d DeveloperAPI) requireOwnedCivilian(w http.ResponseWriter, r *http.Request, cc *CommunityContext, player *resolvedPlayer, civilianID string) (*models.Civilian, bool) {
	_, oid, err := devSafeObjectID(civilianID)
	if err != nil {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "invalid_civilian", "civilianId is required and must be a valid id.", nil)
		return nil, false
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	civilian, err := d.CivDB.FindOne(ctx, bson.M{
		"_id":                        oid,
		"civilian.userID":            player.UserID,
		"civilian.activeCommunityID": devEq(cc.Community.ID.Hex()),
	})
	if err != nil || civilian == nil {
		// One answer for "no such character", "not theirs" and "not in this
		// community", so a key cannot probe for character ids.
		d.RecordOutcome(r, http.StatusNotFound)
		devError(w, http.StatusNotFound, "civilian_not_found",
			"That player has no character with that id in this community.", nil)
		return nil, false
	}
	return civilian, true
}

// normalizeDevSearchName matches how the rest of the API builds searchName, so
// a character a mod creates is findable by the website's own search.
func normalizeDevSearchName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

// regexEscapeDev neutralizes a user-supplied search term before it becomes part
// of a Mongo regex.
//
// Without this, a plate of ".*" matches every vehicle in the community and a
// term like "(((" is a malformed-regex error rather than an empty result.
func regexEscapeDev(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\.+*?()|[]{}^$`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
