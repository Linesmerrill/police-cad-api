package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Countdown serves platform-wide launch countdowns (GTA 6 and whatever comes
// after it) to the website, the mobile app, and the Discord bot.
type Countdown struct {
	DB databases.CountdownDatabase
}

// validCountdownSurfaces are the surfaces a countdown can target. Distinct from
// the changelog's set because the Discord bot renders countdowns too.
var validCountdownSurfaces = map[string]bool{
	"web":    true,
	"mobile": true,
	"bot":    true,
}

// defaultPostLaunchHours is how long a launched countdown keeps showing its
// "out now" state before the clients retire it, when the record does not say.
const defaultPostLaunchHours = 72

// ListCountdownsHandler returns the active countdowns for a surface,
// soonest-first. Flat array, matching the v1 convention.
//
// GET /api/v1/countdowns?surface=web|mobile|bot
//
// An unknown or absent surface returns every active countdown rather than an
// error: this is read-only display data, and a typo in a query string should
// not blank a promo card.
func (c Countdown) ListCountdownsHandler(w http.ResponseWriter, r *http.Request) {
	filter := bson.M{"active": true}

	surface := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("surface")))
	if validCountdownSurfaces[surface] {
		// An empty or absent surfaces array means "every surface", so a
		// record only gets filtered out when it explicitly lists others.
		filter["$or"] = []bson.M{
			{"surfaces": surface},
			{"surfaces": bson.M{"$size": 0}},
			{"surfaces": bson.M{"$exists": false}},
		}
	}

	findOpts := options.Find().SetSort(bson.D{{Key: "launchesAt", Value: 1}}).SetLimit(20)
	cursor, err := c.DB.Find(context.Background(), filter, findOpts)
	if err != nil {
		config.ErrorStatus("failed to fetch countdowns", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())

	countdowns := []models.Countdown{}
	if err := cursor.All(context.Background(), &countdowns); err != nil {
		config.ErrorStatus("failed to decode countdowns", http.StatusInternalServerError, w, err)
		return
	}

	for i := range countdowns {
		normalizeCountdown(&countdowns[i])
	}

	// Countdowns change roughly never, and every client also holds its own
	// fallback, so a few minutes of staleness is free. The ceiling matters:
	// this is the delay between correcting a slipped launch date and every
	// surface showing the new one.
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Type", "application/json")
	// Having no countdowns is the normal steady state, not a problem. Return
	// an empty array rather than a 404 through ErrorStatus, which logs a
	// stacktrace per call and floods the logs.
	_ = json.NewEncoder(w).Encode(countdowns)
}

// normalizeCountdown fills in defaults for fields a hand-edited document may
// omit. These records are maintained directly in Mongo, so the read path stays
// tolerant of a partial document rather than shipping a broken card.
func normalizeCountdown(c *models.Countdown) {
	if c.Mode != models.CountdownModeInstant {
		c.Mode = models.CountdownModeLocalMidnight
	}
	if c.PostLaunchHours <= 0 {
		c.PostLaunchHours = defaultPostLaunchHours
	}
	if c.Surfaces == nil {
		c.Surfaces = []string{}
	}
}
