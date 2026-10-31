package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/models"
)

// The two endpoints a developer calls first.
//
// whoami is how an integration checks its own credentials without having to
// guess at a real endpoint, and it is the first thing we point people at when
// something is not working: it answers "is my key live, what can it do, and
// which communities can it see" in one call.

// DevWhoamiHandler describes the calling key.
//
// GET /api/v1/dev/whoami — no scope required.
//
// Deliberately scope-free. A key with the wrong scopes still needs to be able
// to find out what it does have, or debugging a 403 means guessing.
func (d DeveloperAPI) DevWhoamiHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	communities := d.grantedCommunitySummaries(ctx, dc.Key.ID.Hex())

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"key": map[string]interface{}{
			"id":            dc.Key.ID.Hex(),
			"name":          dc.Key.Name,
			"displayPrefix": dc.Key.DisplayPrefix,
			"scopes":        dc.Key.Scopes,
			"createdAt":     dc.Key.CreatedAt.Time().UTC().Format(time.RFC3339),
		},
		"plan":        dc.Account.EffectivePlan(),
		"limits":      dc.Limits,
		"communities": communities,
		"usage": map[string]interface{}{
			"requestsToday":      dc.Usage.Requests,
			"accountsCreatedToday": dc.Usage.AccountsCreated,
			"communitiesCreatedToday": dc.Usage.CommunitiesCreated,
			"dailyLimitResetsAt": time.Now().UTC().Add(time.Duration(secondsToNextUTCDay()) * time.Second).Format(time.RFC3339),
		},
	})
}

// DevListCommunitiesHandler lists the communities the calling key may act in.
//
// GET /api/v1/dev/communities — community:read.
//
// This is the only place a key learns community ids, and it only ever returns
// communities already granted to it. There is no search and no listing of
// anything else: a key cannot use us to discover communities it has no
// business with.
func (d DeveloperAPI) DevListCommunitiesHandler(w http.ResponseWriter, r *http.Request) {
	dc, ok := DevContextFrom(r.Context())
	if !ok {
		devError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.", nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	d.RecordOutcome(r, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"communities": d.grantedCommunitySummaries(ctx, dc.Key.ID.Hex()),
	})
}

// devCommunitySummary is the slim shape a key sees for a community it holds.
//
// Presentation and identity only. A key asking which communities it can touch
// has no need for members, roles, codes or settings, and the smallest answer
// that does the job is the one that leaks least.
type devCommunitySummary struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	GrantSource       string `json:"grantSource"`
	GrantStatus       string `json:"grantStatus"`
	MembersCount      int    `json:"membersCount"`
	PendingDeletion   bool   `json:"pendingDeletion"`
}

// grantedCommunitySummaries loads every community this key holds a grant on,
// including pending and revoked grants so a developer can see a request they
// are waiting on rather than wondering whether it was ever made.
func (d DeveloperAPI) grantedCommunitySummaries(ctx context.Context, keyID string) []devCommunitySummary {
	out := []devCommunitySummary{}

	cur, err := d.GrantDB.Find(ctx, bson.M{"keyId": keyID})
	if err != nil {
		return out
	}
	var grants []models.DeveloperCommunityGrant
	if err := cur.All(ctx, &grants); err != nil {
		return out
	}

	for _, g := range grants {
		summary := devCommunitySummary{
			ID:          g.CommunityID,
			GrantSource: g.Source,
			GrantStatus: g.Status,
		}
		if c, err := d.CDB.FindOneIncludingPending(ctx, bson.M{"_id": objectIDOrZero(g.CommunityID)}); err == nil && c != nil {
			summary.Name = c.Details.Name
			summary.MembersCount = c.Details.MembersCount
			summary.PendingDeletion = c.Details.PendingDeletionAt != nil
		}
		out = append(out, summary)
	}
	return out
}
