package handlers

import (
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/databases"
)

// CommunityPendingGate returns mux middleware that intercepts requests for a
// community currently in pending-deletion state and responds 410 Gone with the
// standard pending_deletion payload. The mobile and web clients listen for
// that response globally and bounce the member out — this closes the race
// where an owner soft-deletes a community while members are actively using it.
//
// The gate is a no-op for any request whose matched route doesn't carry a
// {community_id} or {communityId} mux var, so it's safe to attach to a
// subrouter without per-route bookkeeping. Admin restore endpoints (under
// /api/v1/admin/) are skipped because staff need to interact with pending
// communities to bring them back.
func CommunityPendingGate(cdb databases.CommunityDatabase) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/v1/admin/") {
				next.ServeHTTP(w, r)
				return
			}
			vars := mux.Vars(r)
			cidStr := vars["community_id"]
			if cidStr == "" {
				cidStr = vars["communityId"]
			}
			if cidStr == "" {
				next.ServeHTTP(w, r)
				return
			}
			cID, err := primitive.ObjectIDFromHex(cidStr)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			ctx, cancel := api.WithQueryTimeout(r.Context())
			defer cancel()
			comm, err := cdb.FindOneIncludingPending(ctx, bson.M{"_id": cID})
			if err == nil && comm != nil && comm.Details.PendingDeletionAt != nil {
				writePendingDeletionGone(w, comm)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
