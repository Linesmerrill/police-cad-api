package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// What the developer console needs that the first cut of the management API did
// not offer: editing a key in place, giving one key several communities, and an
// account-wide usage roll-up for the charts.
//
// The shape of a key was always editable server-side; only the form insisted a
// key had exactly one community. A grant is its own document keyed by
// (keyId, communityId), and every community-scoped route reads the community
// from the URL, so several live grants on one key need no migration and no
// change on the request path.
//
// What they do change is budgeting. Rate and provisioning limits are counted
// per key, so a key with three communities spends one allowance across all
// three. That is stated on the key page rather than left for someone to work
// out from a 429.

// updateDeveloperKeyRequest is a partial update. A nil pointer means "leave it
// alone", which is what lets the console save a rename without having to send
// the scope list back, and send scopes without being able to blank the name by
// omission.
type updateDeveloperKeyRequest struct {
	Name   *string   `json:"name"`
	Scopes *[]string `json:"scopes"`
}

// UpdateDeveloperKeyHandler edits a key's name and permissions in place.
//
// The secret does not change. That is the whole reason this exists: widening a
// key's permissions used to mean revoking it, creating another, and redeploying
// whatever held it, so people over-scoped keys at creation to avoid the churn.
// Narrowing one has the same problem in reverse, and the safe direction being
// the inconvenient one is how keys end up with scopes nobody wanted.
func (d DeveloperAPI) UpdateDeveloperKeyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	keyID, oid, err := devSafeObjectID(mux.Vars(r)["keyId"])
	if err != nil {
		config.InfoStatus("invalid key id", http.StatusBadRequest, w, err)
		return
	}

	var req updateDeveloperKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if req.Name == nil && req.Scopes == nil {
		config.InfoStatus("nothing to update", http.StatusBadRequest, w, fmt.Errorf("empty update"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Scoped to this actor's own keys, so a key id belonging to someone else is
	// a 404 rather than a 403: the id of a key you do not own is not something
	// this endpoint should confirm the existence of.
	key, err := d.KeyDB.FindOne(ctx, bson.M{"_id": devEq(oid), "userId": devEq(actorID)})
	if err != nil || key == nil {
		config.InfoStatus("key not found", http.StatusNotFound, w, fmt.Errorf("not found"))
		return
	}
	if key.Status == models.DeveloperKeyRevoked {
		config.InfoStatus("that key is revoked", http.StatusConflict, w, fmt.Errorf("revoked key"))
		return
	}

	set := bson.M{"updatedAt": primitive.NewDateTimeFromTime(time.Now())}
	details := map[string]interface{}{}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			config.InfoStatus("a key name is required", http.StatusBadRequest, w, fmt.Errorf("empty name"))
			return
		}
		if len(name) > maxDeveloperKeyNameLen {
			name = name[:maxDeveloperKeyNameLen]
		}
		if name != key.Name {
			set["name"] = name
			details["nameFrom"] = key.Name
			details["nameTo"] = name
		}
	}

	if req.Scopes != nil {
		// Same validator as creation, so an unknown scope is refused here too
		// rather than dropped. A key that silently does less than asked for
		// reports itself as a 403 in production, which is the worst place to
		// find out.
		scopes, sErr := normalizeDeveloperScopes(*req.Scopes)
		if sErr != nil {
			config.InfoStatus(sErr.Error(), http.StatusBadRequest, w, sErr)
			return
		}
		// A key with no community and no way to make one can do nothing. The
		// same rule creation applies, re-checked here because an edit can
		// reach that state by removing the scope rather than by never having
		// had it.
		if !containsString(scopes, models.ScopeCommunitiesProvision) {
			live, cErr := d.GrantDB.CountDocuments(ctx, bson.M{
				"keyId":  devEq(keyID),
				"status": bson.M{"$ne": models.DeveloperKeyRevoked},
			})
			if cErr != nil {
				config.ErrorStatus("failed to count communities", http.StatusInternalServerError, w, cErr)
				return
			}
			if live == 0 {
				config.InfoStatus(
					"this key has no communities, so it needs the communities:provision permission to do anything",
					http.StatusBadRequest, w, fmt.Errorf("no community and no provisioning scope"))
				return
			}
		}
		if strings.Join(scopes, ",") != strings.Join(key.Scopes, ",") {
			set["scopes"] = scopes
			details["scopesFrom"] = key.Scopes
			details["scopesTo"] = scopes
		}
	}

	if len(details) == 0 {
		// Nothing actually differs. Report the key as it stands rather than
		// writing an audit entry for a save that changed nothing.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"key":         key,
			"communities": d.grantsForKey(ctx, keyID),
			"changed":     false,
		})
		return
	}

	if _, err := d.KeyDB.UpdateOne(ctx, bson.M{"_id": devEq(oid), "userId": devEq(actorID)},
		bson.M{"$set": set}); err != nil {
		config.ErrorStatus("failed to update key", http.StatusInternalServerError, w, err)
		return
	}

	updated, err := d.KeyDB.FindOne(ctx, bson.M{"_id": devEq(oid), "userId": devEq(actorID)})
	if err != nil || updated == nil {
		config.ErrorStatus("failed to reload key", http.StatusInternalServerError, w, fmt.Errorf("reload failed"))
		return
	}

	// Audited against every community the key can reach, so a community owner
	// reading their own log sees a key gaining permissions on their data
	// without having to go and ask the developer.
	grants := d.grantsForKey(ctx, keyID)
	d.auditKeyChangeAcrossCommunities(grants, "developer_key.updated", actorID, keyID, updated.Name, details)

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"key":         updated,
		"communities": grants,
		"changed":     true,
	})
}

// addDeveloperKeyCommunityRequest names the community to add.
type addDeveloperKeyCommunityRequest struct {
	CommunityID string `json:"communityId"`
}

// AddDeveloperKeyCommunityHandler gives an existing key one more community.
//
// Authority is decided exactly as it is at creation: someone who can manage the
// community makes the grant live, an ordinary approved member creates a request
// the owner has to approve, and a non-member gets nothing. Adding a community
// to an existing key must not be an easier path than binding one at creation,
// or it becomes the path people use to get around the check.
func (d DeveloperAPI) AddDeveloperKeyCommunityHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	keyID, keyOID, err := devSafeObjectID(mux.Vars(r)["keyId"])
	if err != nil {
		config.InfoStatus("invalid key id", http.StatusBadRequest, w, err)
		return
	}

	var req addDeveloperKeyCommunityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	communityID, communityOID, err := devSafeObjectID(req.CommunityID)
	if err != nil {
		config.InfoStatus("invalid community id", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	key, err := d.KeyDB.FindOne(ctx, bson.M{"_id": devEq(keyOID), "userId": devEq(actorID)})
	if err != nil || key == nil {
		config.InfoStatus("key not found", http.StatusNotFound, w, fmt.Errorf("not found"))
		return
	}
	if key.Status == models.DeveloperKeyRevoked {
		config.InfoStatus("that key is revoked", http.StatusConflict, w, fmt.Errorf("revoked key"))
		return
	}

	account, err := d.AccountDB.FindOne(ctx, bson.M{"userId": devEq(actorID)})
	if err != nil || account == nil {
		config.InfoStatus("enable a developer account first", http.StatusForbidden, w, fmt.Errorf("no developer account"))
		return
	}

	community, err := d.CDB.FindOne(ctx, bson.M{"_id": devEq(communityOID)})
	if err != nil || community == nil {
		config.InfoStatus("community not found", http.StatusNotFound, w, fmt.Errorf("no community"))
		return
	}

	status := models.DeveloperKeyPending
	if userHasCommunityPermission(community, actorID, "manage community settings") {
		status = models.DeveloperKeyActive
	} else if !isApprovedMemberOfCommunity(ctx, d.UDB, actorID, communityID) {
		config.InfoStatus("you are not a member of that community", http.StatusForbidden, w,
			fmt.Errorf("not a member"))
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	// A revoked grant is revived rather than duplicated, so a community removed
	// and added back does not leave two rows claiming different things about
	// the same pair. A grant that is already live is reported as-is: asking for
	// what you have is not an error.
	existing, _ := d.GrantDB.FindOne(ctx, bson.M{
		"keyId":       devEq(keyID),
		"communityId": devEq(communityID),
	})
	if existing != nil {
		if existing.Status != models.DeveloperKeyRevoked {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"grant":         existing,
				"communityName": community.Details.Name,
				"added":         false,
			})
			return
		}
		update := bson.M{
			"status":      status,
			"requestedAt": now,
			"source":      models.DeveloperGrantOwnerApproved,
		}
		unset := bson.M{"revokedBy": "", "revokedAt": "", "revokeReason": ""}
		if status == models.DeveloperKeyActive {
			update["approvedBy"] = actorID
			update["approvedAt"] = now
		} else {
			unset["approvedBy"] = ""
			unset["approvedAt"] = ""
		}
		if _, err := d.GrantDB.UpdateOne(ctx, bson.M{"_id": devEq(existing.ID)},
			bson.M{"$set": update, "$unset": unset}); err != nil {
			config.ErrorStatus("failed to restore the grant", http.StatusInternalServerError, w, err)
			return
		}
		logAudit(d.ALDB, communityOID, "developer_key.community_added", "developer_api",
			actorID, resolveActorName(d.UDB, actorID), keyID, key.Name,
			map[string]interface{}{"communityId": communityID, "grantStatus": status, "restored": true})
		reloaded, _ := d.GrantDB.FindOne(ctx, bson.M{"_id": devEq(existing.ID)})
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"grant":         reloaded,
			"communityName": community.Details.Name,
			"added":         true,
		})
		return
	}

	grant := models.DeveloperCommunityGrant{
		ID:          primitive.NewObjectID(),
		KeyID:       keyID,
		DeveloperID: account.ID.Hex(),
		CommunityID: communityID,
		Source:      models.DeveloperGrantOwnerApproved,
		Status:      status,
		RequestedAt: now,
	}
	if status == models.DeveloperKeyActive {
		grant.ApprovedBy = actorID
		grant.ApprovedAt = &now
	}
	if _, err := d.GrantDB.InsertOne(ctx, grant); err != nil {
		config.ErrorStatus("failed to add the community", http.StatusInternalServerError, w, err)
		return
	}

	logAudit(d.ALDB, communityOID, "developer_key.community_added", "developer_api",
		actorID, resolveActorName(d.UDB, actorID), keyID, key.Name,
		map[string]interface{}{"communityId": communityID, "grantStatus": status})

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"grant":         grant,
		"communityName": community.Details.Name,
		"added":         true,
	})
}

// RemoveDeveloperKeyCommunityHandler takes a community off a key, at once.
//
// Revoked rather than deleted, like a key: the usage rows and the audit trail
// point at this pair, and a row that vanished would take the record of what the
// key did in that community with it.
//
// Immediate and unconditional. Removing access is the safe direction, so it
// does not wait on an owner's approval even where adding it would have.
func (d DeveloperAPI) RemoveDeveloperKeyCommunityHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	keyID, keyOID, err := devSafeObjectID(mux.Vars(r)["keyId"])
	if err != nil {
		config.InfoStatus("invalid key id", http.StatusBadRequest, w, err)
		return
	}
	communityID, communityOID, err := devSafeObjectID(mux.Vars(r)["communityId"])
	if err != nil {
		config.InfoStatus("invalid community id", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	key, err := d.KeyDB.FindOne(ctx, bson.M{"_id": devEq(keyOID), "userId": devEq(actorID)})
	if err != nil || key == nil {
		config.InfoStatus("key not found", http.StatusNotFound, w, fmt.Errorf("not found"))
		return
	}

	grant, err := d.GrantDB.FindOne(ctx, bson.M{
		"keyId":       devEq(keyID),
		"communityId": devEq(communityID),
	})
	if err != nil || grant == nil {
		config.InfoStatus("that key is not on that community", http.StatusNotFound, w, fmt.Errorf("no grant"))
		return
	}
	if grant.Status == models.DeveloperKeyRevoked {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"removed": false})
		return
	}

	// Refusing the last community would leave the developer unable to remove
	// access they no longer want, so the key is left permission-only instead
	// and the console says it can no longer reach any community. A key that
	// reaches nothing is inert, not dangerous.
	now := primitive.NewDateTimeFromTime(time.Now())
	if _, err := d.GrantDB.UpdateOne(ctx, bson.M{"_id": devEq(grant.ID)}, bson.M{"$set": bson.M{
		"status":       models.DeveloperKeyRevoked,
		"revokedBy":    actorID,
		"revokedAt":    now,
		"revokeReason": "removed by the developer",
	}}); err != nil {
		config.ErrorStatus("failed to remove the community", http.StatusInternalServerError, w, err)
		return
	}

	logAudit(d.ALDB, communityOID, "developer_key.community_removed", "developer_api",
		actorID, resolveActorName(d.UDB, actorID), keyID, key.Name,
		map[string]interface{}{"communityId": communityID})

	remaining, _ := d.GrantDB.CountDocuments(ctx, bson.M{
		"keyId":  devEq(keyID),
		"status": bson.M{"$ne": models.DeveloperKeyRevoked},
	})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"removed":            true,
		"remainingCommunity": remaining,
		// True when the key can no longer act in any community and cannot make
		// one either, which the console shows as a warning on the key.
		"keyNowInert": remaining == 0 && !containsString(key.Scopes, models.ScopeCommunitiesProvision),
	})
}

// auditKeyChangeAcrossCommunities writes one audit entry per community a key
// can reach, so the change is visible from either side.
func (d DeveloperAPI) auditKeyChangeAcrossCommunities(
	grants []map[string]interface{}, action, actorID, keyID, keyName string, details map[string]interface{},
) {
	actorName := resolveActorName(d.UDB, actorID)
	for _, row := range grants {
		grant, ok := row["grant"].(models.DeveloperCommunityGrant)
		if !ok || grant.Status == models.DeveloperKeyRevoked {
			continue
		}
		logAudit(d.ALDB, objectIDOrZero(grant.CommunityID), action, "developer_api",
			actorID, actorName, keyID, keyName, details)
	}
}

// ---- account-wide usage ----

// developerUsageDay is one day of the account's traffic, summed over its keys.
type developerUsageDay struct {
	Day          string `json:"day"`
	Requests     int    `json:"requests"`
	Rejected401  int    `json:"rejected401"`
	Rejected403  int    `json:"rejected403"`
	Rejected429  int    `json:"rejected429"`
	Rejected     int    `json:"rejected"`
	AccountsMade int    `json:"accountsMade"`
}

// developerUsageKey is one key's share of the range.
type developerUsageKeyTotal struct {
	KeyID      string `json:"keyId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Requests   int    `json:"requests"`
	Rejected   int    `json:"rejected"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
}

// GetDeveloperUsageHandler reports the whole account's traffic over a range.
//
// The console charts the account, not one key, and asking the browser to fetch
// every key's series and add them up would make the page's cost grow with the
// number of keys and put the arithmetic somewhere the limits are not enforced.
// One query over the account's keys, summed here.
//
// Days with no traffic are filled in with zeroes. A chart that skips empty days
// draws a quiet week as a straight line between two busy ones, which reads as
// steady use rather than none.
func (d DeveloperAPI) GetDeveloperUsageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	account, err := d.AccountDB.FindOne(ctx, bson.M{"userId": devEq(actorID)})
	if err != nil || account == nil {
		config.InfoStatus("no developer account", http.StatusNotFound, w, fmt.Errorf("no developer account"))
		return
	}

	// Bounded, and validated to a date shape before it reaches a query. 90 days
	// is as far back as the console offers; a longer window is a report, not a
	// dashboard.
	days := 30
	if v := strings.TrimSpace(r.URL.Query().Get("days")); v != "" {
		switch v {
		case "7":
			days = 7
		case "30":
			days = 30
		case "90":
			days = 90
		default:
			config.InfoStatus("days must be 7, 30 or 90", http.StatusBadRequest, w,
				fmt.Errorf("unsupported range"))
			return
		}
	}
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	to := now.Format("2006-01-02")

	cur, err := d.KeyDB.Find(ctx, bson.M{"developerId": devEq(account.ID.Hex())},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}))
	if err != nil {
		config.ErrorStatus("failed to load keys", http.StatusInternalServerError, w, err)
		return
	}
	var keys []models.DeveloperAPIKey
	if err := cur.All(ctx, &keys); err != nil {
		config.ErrorStatus("failed to decode keys", http.StatusInternalServerError, w, err)
		return
	}

	plan := models.DeveloperPlanFor(d.ownerOf(ctx, account.UserID)).Plan
	limits := models.LimitsForPlan(plan)

	byDay := map[string]*developerUsageDay{}
	for i := 0; i < days; i++ {
		day := now.AddDate(0, 0, -(days - 1 - i)).Format("2006-01-02")
		byDay[day] = &developerUsageDay{Day: day}
	}

	keyTotals := make([]developerUsageKeyTotal, 0, len(keys))
	for _, key := range keys {
		keyID := key.ID.Hex()
		total := developerUsageKeyTotal{
			KeyID:  keyID,
			Name:   key.Name,
			Status: key.Status,
		}
		if key.LastUsedAt != nil {
			total.LastUsedAt = key.LastUsedAt.Time().UTC().Format(time.RFC3339)
		}

		// The key id is this account's own, read back from the database rather
		// than from the request, and the bounds are formatted dates.
		rows, rErr := d.UsageDB.Find(ctx, bson.M{
			"keyId": devEq(keyID),
			"day":   bson.M{"$gte": from, "$lte": to},
		}, options.Find().SetSort(bson.D{{Key: "day", Value: 1}}))
		if rErr != nil {
			config.ErrorStatus("failed to load usage", http.StatusInternalServerError, w, rErr)
			return
		}
		var usage []models.DeveloperAPIUsage
		if dErr := rows.All(ctx, &usage); dErr != nil {
			config.ErrorStatus("failed to decode usage", http.StatusInternalServerError, w, dErr)
			return
		}
		for _, u := range usage {
			total.Requests += u.Requests
			rejected := u.Rejected401 + u.Rejected403 + u.Rejected429
			total.Rejected += rejected
			bucket, ok := byDay[u.Day]
			if !ok {
				continue
			}
			bucket.Requests += u.Requests
			bucket.Rejected401 += u.Rejected401
			bucket.Rejected403 += u.Rejected403
			bucket.Rejected429 += u.Rejected429
			bucket.Rejected += rejected
			bucket.AccountsMade += u.AccountsCreated
		}
		keyTotals = append(keyTotals, total)
	}

	series := make([]developerUsageDay, 0, days)
	for i := 0; i < days; i++ {
		day := now.AddDate(0, 0, -(days - 1 - i)).Format("2006-01-02")
		series = append(series, *byDay[day])
	}

	// Busiest first, so the console's "which key is this" question is answered
	// by the order rather than by reading every row.
	sort.SliceStable(keyTotals, func(i, j int) bool {
		return keyTotals[i].Requests > keyTotals[j].Requests
	})

	todayKey := now.Format("2006-01-02")
	thisMonth := now.Format("2006-01")
	requestsToday, rejectedToday, requestsThisMonth, peakDay := 0, 0, 0, 0
	for _, day := range series {
		if day.Day == todayKey {
			requestsToday = day.Requests
			rejectedToday = day.Rejected
		}
		if strings.HasPrefix(day.Day, thisMonth) {
			requestsThisMonth += day.Requests
		}
		if day.Requests > peakDay {
			peakDay = day.Requests
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"plan":              plan,
		"limits":            limits,
		"days":              days,
		"from":              from,
		"to":                to,
		"series":            series,
		"keys":              keyTotals,
		"requestsToday":     requestsToday,
		"rejectedToday":     rejectedToday,
		"requestsThisMonth": requestsThisMonth,
		"peakDay":           peakDay,
		// UTC, because that is when the counters actually roll over. The console
		// renders it in the reader's own timezone; it does not re-derive it.
		"dailyLimitResetsAt": now.Add(time.Duration(secondsToNextUTCDay()) * time.Second).Format(time.RFC3339),
	})
}

// ---- one key, in full ----

// GetDeveloperKeyHandler returns a single key with its communities.
//
// The console's key page could filter the list endpoint in the browser, but
// then a link to a key that was revoked on another device renders as an empty
// page rather than saying so, and the page's cost would grow with the number
// of keys the account has.
func (d DeveloperAPI) GetDeveloperKeyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	actorID := devActorID(r)
	if actorID == "" {
		config.ErrorStatus("unauthorized", http.StatusUnauthorized, w, fmt.Errorf("no authenticated user"))
		return
	}

	keyID, oid, err := devSafeObjectID(mux.Vars(r)["keyId"])
	if err != nil {
		config.InfoStatus("invalid key id", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	key, err := d.KeyDB.FindOne(ctx, bson.M{"_id": devEq(oid), "userId": devEq(actorID)})
	if err != nil || key == nil {
		config.InfoStatus("key not found", http.StatusNotFound, w, fmt.Errorf("not found"))
		return
	}

	account, _ := d.AccountDB.FindOne(ctx, bson.M{"userId": devEq(actorID)})
	plan := models.DeveloperPlanFree
	if account != nil {
		plan = models.DeveloperPlanFor(d.ownerOf(ctx, account.UserID)).Plan
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"key":         key,
		"communities": d.grantsForKey(ctx, keyID),
		"limits":      models.LimitsForPlan(plan),
		"plan":        plan,
		// Which scopes exist, so the key page can offer the same permission
		// list the create form does without a second request.
		"availableScopes": models.DeveloperScopes(),
	})
}
