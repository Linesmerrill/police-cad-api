package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// AdminSubscriptionEventsSearchHandler searches the subscription_events
// audit collection so admins can look up a purchase by Google Play /
// App Store / Stripe transaction id, by product id, or by user email.
//
//	GET /api/v1/admin/subscription-events?q=GPA.3377-...&limit=50&page=1
//
// `q` is matched against transactionId, originalTransactionId, productId,
// userEmail, providerEventId (substring, case-insensitive) and userId
// (exact). Returns the standard paginated envelope
// {data, totalCount, page, limit}.
func (h Admin) AdminSubscriptionEventsSearchHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.SEDB == nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "subscription events not configured"})
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	skip := int64((page - 1) * limit)

	filter := bson.M{}
	if q != "" {
		safe := regexp.QuoteMeta(q)
		rx := bson.M{"$regex": safe, "$options": "i"}
		filter["$or"] = []bson.M{
			{"transactionId": rx},
			{"originalTransactionId": rx},
			{"productId": rx},
			{"userEmail": rx},
			{"userId": q},
			{"providerEventId": q},
		}
	}

	total, err := h.SEDB.CountDocuments(r.Context(), filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to count events"})
		return
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: -1}}).
		SetSkip(skip).
		SetLimit(int64(limit))

	cursor, err := h.SEDB.Find(r.Context(), filter, findOpts)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to query events"})
		return
	}
	var events []models.SubscriptionEvent
	if err := cursor.All(r.Context(), &events); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to decode events"})
		return
	}
	if events == nil {
		events = []models.SubscriptionEvent{}
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       events,
		"totalCount": total,
		"page":       page,
		"limit":      limit,
	})
}

// AdminUserSubscriptionEventsHandler returns recent subscription_events
// rows for a single user. Used by the admin user-detail drawer.
//
//	GET /api/v1/admin/users/{user_id}/subscription-events?limit=50
func (h Admin) AdminUserSubscriptionEventsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.SEDB == nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "subscription events not configured"})
		return
	}

	userID := mux.Vars(r)["id"]
	if userID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "user_id required"})
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: -1}}).
		SetLimit(int64(limit))

	cursor, err := h.SEDB.Find(r.Context(), bson.M{"userId": userID}, findOpts)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to query events"})
		return
	}
	var events []models.SubscriptionEvent
	if err := cursor.All(r.Context(), &events); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to decode events"})
		return
	}
	if events == nil {
		events = []models.SubscriptionEvent{}
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": events})
}
