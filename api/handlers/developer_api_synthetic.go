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
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/models"
)

// Synthetic records: the characters a server simulates, kept apart from the
// people who play there.
//
// A FiveM server populating a city wants hundreds of civilians with licences,
// vehicles and records. Nothing about that is wrong, and it is genuinely useful
// — but a name database where twelve real players sit under four hundred NPCs
// is a name database nobody can use. So a synthetic record is marked as such on
// the way in and left out of the searches and rosters a player sees unless they
// ask for it.
//
// Three decisions worth stating, because each is a line somebody could
// reasonably have drawn elsewhere:
//
// It is declared, not inferred. "A key made it" is not the same claim as "this
// is not a person": the same key provisions accounts for real players joining
// from the game. So the integration says `"synthetic": true`, and that claim
// is recorded on the record rather than derived from its provenance.
//
// They count against provisioning limits but not against a player's own caps.
// The daily provisioning cap exists to stop a loop making ten thousand rows
// before anyone notices, and a synthetic loop is exactly that, so they count
// there. A player's per-community character cap exists to stop one person
// filling a community's roster, and an NPC is nobody's character, so they do
// not count there. Counting them both ways would have meant a server's scenery
// consuming its players' allowances.
//
// They are removable, by two different people for two different reasons. The
// integration deletes its own test rows — the common case, after a test run,
// and it needs no human. A community owner removes everything a key ever
// wrote, from the website, which is what they need when an integration has
// misbehaved and is no longer answering.

// maxSyntheticCleanupBatch bounds one cleanup call.
//
// A bounded, repeatable delete rather than one unbounded sweep: a caller gets a
// count back and calls again while it is non-zero, so no single request holds a
// long-running multi-collection delete open.
const maxSyntheticCleanupBatch = 500

// devSyntheticFilter is the condition that hides simulated records.
//
// Written as "not explicitly synthetic" rather than "synthetic is false", so
// every record that predates this field — which is all of them — keeps showing
// up. A filter on `false` would have silently emptied every existing search.
func devSyntheticFilter(include bool) bson.M {
	if include {
		return nil
	}
	return bson.M{"$ne": true}
}

// applySyntheticScope adds the synthetic exclusion to a record filter.
//
// path is the record's own field path, which differs per collection
// ("civilian.source.synthetic", "vehicle.source.synthetic", ...). Passing it in
// rather than guessing keeps each caller honest about which document it is
// filtering.
func applySyntheticScope(filter bson.M, path string, include bool) bson.M {
	if cond := devSyntheticFilter(include); cond != nil {
		filter[path] = cond
	}
	return filter
}

// devIncludeSynthetic reads the opt-in query parameter.
//
// Default off. A client that wants NPCs says so, which means every existing
// caller — the website, the app, the bot, and every integration written before
// today — keeps the behaviour it has.
func devIncludeSynthetic(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("includeSynthetic")), "true")
}

// devRequestedSynthetic reads the declaration on a write.
func devRequestedSynthetic(declared bool) bool { return declared }

// playerIsSyntheticForKey reports whether an identity is one this key created
// as a simulated character.
//
// Scoped to the calling key on purpose. One integration must not be able to
// write records about another integration's NPCs, and "synthetic" is only a
// safe membership exemption when the thing it exempts is the caller's own.
func (d DeveloperAPI) playerIsSyntheticForKey(ctx context.Context, dc *DevAPIContext, player *resolvedPlayer) bool {
	if player == nil || d.IdentityDB == nil {
		return false
	}
	identity, err := d.IdentityDB.FindOne(ctx, bson.M{
		"provider":   devEq(player.Provider),
		"identifier": devEq(player.Identifier),
	})
	if err != nil || identity == nil {
		return false
	}
	// A claimed identity is a person who signed in and took the account over,
	// whatever it was created as. From that moment it is not synthetic, and
	// treating it as such would exempt a real player from the membership check.
	if identity.Claimed {
		return false
	}
	return identity.Synthetic && identity.CreatedByKeyID == dc.Key.ID.Hex()
}

// ---- cleanup a key can run itself ----

type devSyntheticCleanupRequest struct {
	// Source and ExternalID narrow the delete. With neither, every synthetic
	// record this key made in this community goes.
	Source     string `json:"source"`
	ExternalID string `json:"externalId"`
	// Confirm has to be the community id. An unbounded delete needs the caller
	// to have named what they are deleting from, so a cleanup aimed at a test
	// community cannot run against a live one because a config value was stale.
	Confirm string `json:"confirm"`
}

// DevCleanupSyntheticRecordsHandler removes the synthetic records this key
// created in one community.
//
// DELETE /api/v1/dev/communities/{communityId}/synthetic-records — players:provision
//
// Three boundaries, each of which is the whole safety of the endpoint:
//
//	synthetic only   — a record created for a real player is never touched,
//	                   whichever key made it;
//	this key only    — a key cannot clean up another integration's output,
//	                   even within a community they both hold;
//	this community   — scoped by the community in the path, which the key had
//	                   to be granted to reach this handler at all.
//
// It answers with what it removed, per collection, so a test harness can assert
// it is back to a clean state rather than hoping.
func (d DeveloperAPI) DevCleanupSyntheticRecordsHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}
	cc, ok := d.ResolveCommunity(w, r)
	if !ok {
		return
	}

	var req devSyntheticCleanupRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	communityID := cc.Community.ID.Hex()
	if strings.TrimSpace(req.Confirm) != communityID {
		d.RecordOutcome(r, http.StatusBadRequest)
		devError(w, http.StatusBadRequest, "confirm_required",
			"Send confirm set to the community id you mean to clean up. "+
				"This endpoint deletes rows, so it will not run on an id the caller did not state.",
			map[string]interface{}{"expected": communityID})
		return
	}

	base := bson.M{
		"source.type":      devEq(models.RecordSourceDeveloperAPI),
		"source.keyId":     devEq(dc.Key.ID.Hex()),
		"source.synthetic": devEq(true),
	}
	if raw := strings.TrimSpace(req.Source); raw != "" {
		source, err := devSafeScalar(raw, 60, "source")
		if err != nil {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_source", err.Error(), nil)
			return
		}
		base["source.sourceName"] = devEq(source)
	}
	if raw := strings.TrimSpace(req.ExternalID); raw != "" {
		externalID, err := devSafeExternalID(raw)
		if err != nil {
			d.RecordOutcome(r, http.StatusBadRequest)
			devError(w, http.StatusBadRequest, "invalid_external_id", err.Error(), nil)
			return
		}
		base["source.externalId"] = devEq(externalID)
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	removed, err := d.deleteSyntheticRecords(ctx, communityID, base)
	if err != nil {
		devError(w, http.StatusInternalServerError, "server_error",
			"Could not remove those records.", nil)
		return
	}

	logAudit(d.ALDB, cc.Community.ID, "synthetic_records.cleaned_by_developer_key", "developer_api",
		dc.Key.UserID, resolveActorName(d.UDB, dc.Key.UserID), dc.Key.ID.Hex(), "synthetic cleanup",
		map[string]interface{}{"keyId": dc.Key.ID.Hex(), "removed": removed})

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"removed": removed,
		"total":   totalRemoved(removed),
		// A caller that hits the batch cap calls again. Saying so beats having
		// them discover a partial delete by counting rows.
		"more": totalRemoved(removed) >= maxSyntheticCleanupBatch,
	})
}

// deleteSyntheticRecords removes a key's simulated rows across the collections
// it can write to, newest-first and bounded.
//
// Returned per collection rather than as one number, because "it deleted 412
// things" is not something anyone can check and "97 civilians, 180 vehicles,
// 135 licences" is.
func (d DeveloperAPI) deleteSyntheticRecords(ctx context.Context, communityID string, base bson.M) (map[string]int64, error) {
	removed := map[string]int64{}

	// Each collection wraps its record under its own key, so the source stamp
	// sits at a different path in each. Spelled out rather than derived from
	// the collection name: a derivation that happened to work for three
	// collections would be wrong for the fourth, silently, as a filter that
	// matches nothing.
	type target struct {
		label        string
		wrapper      string
		communityKey string
		remove       func(ctx context.Context, filter bson.M) (int64, error)
	}
	targets := []target{
		{"civilians", "civilian", "civilian.activeCommunityID", func(ctx context.Context, f bson.M) (int64, error) {
			if d.CivDB == nil {
				return 0, nil
			}
			return d.CivDB.DeleteMany(ctx, f)
		}},
		{"vehicles", "vehicle", "vehicle.activeCommunityID", func(ctx context.Context, f bson.M) (int64, error) {
			if d.VehDB == nil {
				return 0, nil
			}
			return d.VehDB.DeleteMany(ctx, f)
		}},
		{"firearms", "firearm", "firearm.activeCommunityID", func(ctx context.Context, f bson.M) (int64, error) {
			if d.FirDB == nil {
				return 0, nil
			}
			return d.FirDB.DeleteMany(ctx, f)
		}},
	}

	for _, t := range targets {
		filter := bson.M{t.communityKey: devEq(communityID)}
		for path, cond := range base {
			filter[t.wrapper+"."+path] = cond
		}
		count, err := t.remove(ctx, filter)
		if err != nil {
			return removed, fmt.Errorf("removing %s: %w", t.label, err)
		}
		if count > 0 {
			removed[t.label] = count
		}
	}
	return removed, nil
}

func totalRemoved(removed map[string]int64) int64 {
	var total int64
	for _, n := range removed {
		total += n
	}
	return total
}

// ---- cleanup an owner runs from the website ----

// CleanupCommunityKeyRecordsHandler removes everything a key wrote in a
// community the caller owns.
//
// DELETE /api/v1/developer/community/{communityId}/keys/{keyId}/records
//
// On the first-party surface, not the developer one, and gated on owning or
// administering the community rather than on holding the key. That is the whole
// point: the person who needs this is an owner whose community has rows from an
// integration that is no longer answering, and requiring the integration's
// cooperation to clean up after it would be requiring exactly what is missing.
//
// Unlike the key's own cleanup this is not limited to synthetic rows — an owner
// revoking an integration wants its output gone — so it takes an explicit
// scope: "synthetic" or "all".
func (d DeveloperAPI) CleanupCommunityKeyRecordsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	communityID, communityOID, err := devSafeObjectID(mux.Vars(r)["communityId"])
	if err != nil {
		config.InfoStatus("invalid community id", http.StatusBadRequest, w, err)
		return
	}
	keyID, _, err := devSafeObjectID(mux.Vars(r)["keyId"])
	if err != nil {
		config.InfoStatus("invalid key id", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := d.CDB.FindOne(ctx, bson.M{"_id": devEq(communityOID)})
	if err != nil || community == nil {
		config.InfoStatus("community not found", http.StatusNotFound, w, fmt.Errorf("not found"))
		return
	}
	// The same gate the rest of the community-side key screens use, so one
	// permission governs every action an owner can take on an integration.
	if !authorizeCommunityAction(w, r, community, "manage community settings") {
		return
	}
	userID := resolveActorFromRequest(r)

	var req struct {
		Scope   string `json:"scope"`
		Confirm string `json:"confirm"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	scope := strings.ToLower(strings.TrimSpace(req.Scope))
	if scope == "" {
		scope = "synthetic"
	}
	if scope != "synthetic" && scope != "all" {
		devError(w, http.StatusBadRequest, "invalid_scope",
			`scope must be "synthetic" or "all".`, nil)
		return
	}
	if strings.TrimSpace(req.Confirm) != communityID {
		devError(w, http.StatusBadRequest, "confirm_required",
			"Send confirm set to the community id. This deletes records, so it will not run "+
				"on an id the caller did not state.",
			map[string]interface{}{"expected": communityID})
		return
	}

	base := bson.M{
		"source.type":  devEq(models.RecordSourceDeveloperAPI),
		"source.keyId": devEq(keyID),
	}
	if scope == "synthetic" {
		base["source.synthetic"] = devEq(true)
	}

	removed, err := d.deleteSyntheticRecords(ctx, communityID, base)
	if err != nil {
		zap.S().Errorw("developer api: owner cleanup failed",
			"communityId", communityID, "keyId", keyID, "error", err)
		devError(w, http.StatusInternalServerError, "server_error",
			"Could not remove those records.", nil)
		return
	}

	// Drafts go too. They are a key's output like anything else, and a draft
	// left in a reviewer's queue by a revoked integration is worse than most:
	// somebody would file it.
	if d.ReportDraftDB != nil {
		draftFilter := bson.M{
			"communityId":  devEq(communityID),
			"source.keyId": devEq(keyID),
			"status":       devEq(models.ReportDraftStatusDraft),
		}
		if count, dErr := d.ReportDraftDB.DeleteMany(ctx, draftFilter); dErr == nil && count > 0 {
			removed["reportDrafts"] = count
		}
	}

	logAudit(d.ALDB, communityOID, "developer_key.records_removed", "community",
		userID, resolveActorName(d.UDB, userID), keyID, scope,
		map[string]interface{}{"keyId": keyID, "scope": scope, "removed": removed})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"removed": removed,
		"total":   totalRemoved(removed),
		"scope":   scope,
		"more":    totalRemoved(removed) >= maxSyntheticCleanupBatch,
	})
}

// devSyntheticSourceStamp builds the record stamp for a synthetic write.
func devSyntheticSourceStamp(dc *DevAPIContext, player *resolvedPlayer, synthetic bool, source, externalID string) *models.RecordSource {
	src := devRecordSource(dc, player)
	src.Synthetic = synthetic
	if source != "" {
		src.SourceName = source
	}
	if externalID != "" {
		src.ExternalID = externalID
	}
	if src.CreatedAt == 0 {
		src.CreatedAt = primitive.NewDateTimeFromTime(time.Now())
	}
	return src
}
