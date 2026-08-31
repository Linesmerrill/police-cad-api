package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// HousePromo serves internal promotions to the website and the mobile app.
//
// These render in an ad slot that did not fill rather than in a slot of their
// own, so the clients only ask for one when they already have empty space to
// put it in.
type HousePromo struct {
	DB databases.HousePromoDatabase
}

// validHousePromoSurfaces are the surfaces a promo can target. No "bot" entry:
// the Discord bot has no ad slot for one of these to take the place of.
var validHousePromoSurfaces = map[string]bool{
	"web":    true,
	"mobile": true,
}

// ListHousePromosHandler returns the live promos for a surface, newest first.
// Flat array, matching the v1 convention.
//
// GET /api/v1/house-promos?surface=web|mobile
//
// An unknown or absent surface returns every live promo rather than an error:
// this is read-only display data, and a typo in a query string should not be
// worth a 400.
func (h HousePromo) ListHousePromosHandler(w http.ResponseWriter, r *http.Request) {
	now := primitive.NewDateTimeFromTime(time.Now())

	// Expiry is enforced here as well as on the clients. A campaign that has
	// finished should stop being served whether or not anybody remembered to
	// flip Active, and a released mobile build cannot be relied on to hold up
	// its end of that.
	filter := bson.M{
		"active": true,
		"$and": []bson.M{{
			"$or": []bson.M{
				{"endsAt": bson.M{"$exists": false}},
				{"endsAt": nil},
				{"endsAt": bson.M{"$gt": now}},
			},
		}},
	}

	surface := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("surface")))
	if validHousePromoSurfaces[surface] {
		// An empty or absent surfaces array means "every surface", so a record
		// only gets filtered out when it explicitly lists others.
		and := filter["$and"].([]bson.M)
		filter["$and"] = append(and, bson.M{
			"$or": []bson.M{
				{"surfaces": surface},
				{"surfaces": bson.M{"$size": 0}},
				{"surfaces": bson.M{"$exists": false}},
			},
		})
	}

	findOpts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(20)
	cursor, err := h.DB.Find(context.Background(), filter, findOpts)
	if err != nil {
		config.ErrorStatus("failed to fetch house promos", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())

	promos := []models.HousePromo{}
	if err := cursor.All(context.Background(), &promos); err != nil {
		config.ErrorStatus("failed to decode house promos", http.StatusInternalServerError, w, err)
		return
	}

	// A promo with nowhere to send you is not a promo. Dropping it here means
	// no client has to decide what to render for a half-written document.
	live := []models.HousePromo{}
	for i := range promos {
		normalizeHousePromo(&promos[i])
		if promos[i].CTAURL != "" && promos[i].Title != "" {
			live = append(live, promos[i])
		}
	}

	// The ceiling that matters: this is the delay between switching a promo off
	// and every surface having stopped showing it.
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Type", "application/json")
	// Having no promos running is the normal steady state, not a problem.
	// Return an empty array rather than a 404 through ErrorStatus, which logs a
	// stacktrace per call and floods the logs.
	_ = json.NewEncoder(w).Encode(live)
}

// normalizeHousePromo fills in defaults for fields a hand-edited document may
// omit. These records are maintained directly in Mongo, so the read path stays
// tolerant of a partial document rather than shipping a broken card.
func normalizeHousePromo(p *models.HousePromo) {
	if p.CTALabel == "" {
		p.CTALabel = "Learn more"
	}
	if p.Surfaces == nil {
		p.Surfaces = []string{}
	}
}
