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

// MemberStatusCountsHandler answers how many people are in each state with this
// community: members, people waiting on a join request, and people who were
// turned down.
//
// The Members screen needs these up front, to label its filters before anyone
// picks one. A join request that only appears in a notification is a request
// nobody works: the owner has to notice the notification and still has it among
// everything else. A count on the Members screen is the thing that makes a
// waiting request visible at all.
//
// GET /api/v2/community/{communityId}/member-status-counts
func (c Community) MemberStatusCountsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	if _, err := primitive.ObjectIDFromHex(communityID); err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// One count per state, each served by the existing
	// {communities.communityId, communities.status} compound index.
	counts := map[string]int64{}
	for _, status := range []string{
		models.CommunityMemberStatusApproved,
		models.CommunityMemberStatusPending,
		models.CommunityMemberStatusDeclined,
	} {
		total, err := c.UDB.CountDocuments(ctx, bson.M{
			"user.communities": bson.M{
				"$elemMatch": bson.M{
					"communityId": communityID,
					"status":      status,
				},
			},
		})
		if err != nil {
			config.ErrorStatus("failed to count community members", http.StatusInternalServerError, w, err)
			return
		}
		counts[status] = total
	}

	// Banned people are held on the community document rather than counted off
	// the user side, which is where the banned list itself reads from.
	banned := 0
	if cID, err := primitive.ObjectIDFromHex(communityID); err == nil {
		if community, ferr := c.DB.FindOne(ctx, bson.M{"_id": cID}); ferr == nil && community != nil {
			seen := map[string]bool{}
			for _, id := range community.Details.BanList {
				if id == "" || seen[id] {
					continue
				}
				seen[id] = true
				banned++
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"approved": counts[models.CommunityMemberStatusApproved],
		"pending":  counts[models.CommunityMemberStatusPending],
		"declined": counts[models.CommunityMemberStatusDeclined],
		"banned":   banned,
	})
}
