package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Changelog serves the platform-wide "What's New" changelog: staff-authored
// posts surfaced once per user in a launch-time modal on web and mobile.
type Changelog struct {
	DB  databases.ChangelogPostDatabase
	UDB databases.UserDatabase
}

// validSurfaces are the surfaces a changelog post can target.
var validSurfaces = map[string]bool{
	"web":    true,
	"mobile": true,
}

// CreateChangelogPostHandler stores a new changelog post. Admin-only in
// practice: registered under /admin and reached from the admin console, matching
// the existing beta-feedback admin endpoints.
// POST /api/v1/admin/changelog
func (c Changelog) CreateChangelogPostHandler(w http.ResponseWriter, r *http.Request) {
	var req models.CreateChangelogPostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("failed to decode request", http.StatusBadRequest, w, err)
		return
	}

	// Server-side admin gate: this writes staff-authored trusted HTML shown to
	// every user, so don't rely on the admin console hiding the panel. Requires
	// the owner role, like the other admin-console write endpoints.
	if err := checkAdminPermissions(req.CurrentUser); err != nil {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, err)
		return
	}

	req.Title = strings.TrimSpace(req.Title)
	req.Body = strings.TrimSpace(req.Body)
	if req.Title == "" || req.Body == "" {
		config.ErrorStatus("title and body are required", http.StatusBadRequest, w, nil)
		return
	}
	if len(req.Title) > 200 {
		req.Title = req.Title[:200]
	}
	if len(req.Body) > 10000 {
		req.Body = req.Body[:10000]
	}

	// Normalize + validate surfaces. Empty means "all surfaces".
	surfaces := []string{}
	for _, s := range req.Surfaces {
		s = strings.ToLower(strings.TrimSpace(s))
		if !validSurfaces[s] {
			config.ErrorStatus("surfaces must be 'web' or 'mobile'", http.StatusBadRequest, w, nil)
			return
		}
		surfaces = append(surfaces, s)
	}

	active := true
	if req.Active != nil {
		active = *req.Active
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	doc := models.ChangelogPost{
		ID:          primitive.NewObjectID(),
		Title:       req.Title,
		Body:        req.Body,
		Surfaces:    surfaces,
		Active:      active,
		PublishedAt: now,
		CreatedBy:   resolveActorFromRequest(r),
		CreatedAt:   now,
	}
	if _, err := c.DB.InsertOne(context.Background(), doc); err != nil {
		config.ErrorStatus("failed to save changelog post", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

// ListChangelogPostsHandler returns all changelog posts newest-first for the
// admin console. GET /api/v1/admin/changelog
func (c Changelog) ListChangelogPostsHandler(w http.ResponseWriter, r *http.Request) {
	limit := int64(100)
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	findOpts := options.Find().
		SetSort(bson.D{{Key: "publishedAt", Value: -1}}).
		SetLimit(limit)
	cursor, err := c.DB.Find(context.Background(), bson.M{}, findOpts)
	if err != nil {
		config.ErrorStatus("failed to fetch changelog posts", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())

	posts := []models.ChangelogPost{}
	if err := cursor.All(context.Background(), &posts); err != nil {
		config.ErrorStatus("failed to decode changelog posts", http.StatusInternalServerError, w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": posts})
}

// GetUserAnnouncementsHandler returns the active changelog posts a given user
// has NOT yet seen, for a given surface. Posts published before the user's
// account was created are suppressed so new users don't get the back-catalog.
// GET /api/v1/user/{userId}/announcements?surface=web|mobile
func (c Changelog) GetUserAnnouncementsHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	surface := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("surface")))
	if surface != "" && !validSurfaces[surface] {
		config.ErrorStatus("surface must be 'web' or 'mobile'", http.StatusBadRequest, w, nil)
		return
	}

	ctx := context.Background()

	user := models.User{}
	if err := c.UDB.FindOne(ctx, bson.M{"_id": uID}).Decode(&user); err != nil {
		config.ErrorStatus("failed to get user", http.StatusNotFound, w, err)
		return
	}

	seen := map[string]bool{}
	for _, id := range user.Details.SeenAnnouncements {
		seen[id] = true
	}
	createdAt := coerceToTime(user.Details.CreatedAt)

	// Only active posts. Surface filter matches posts explicitly targeting the
	// surface OR posts with no surface restriction (empty/absent).
	filter := bson.M{"active": true}
	if surface != "" {
		filter["$or"] = []bson.M{
			{"surfaces": surface},
			{"surfaces": bson.M{"$size": 0}},
			{"surfaces": bson.M{"$exists": false}},
			{"surfaces": nil},
		}
	}
	findOpts := options.Find().SetSort(bson.D{{Key: "publishedAt", Value: -1}})
	cursor, err := c.DB.Find(ctx, filter, findOpts)
	if err != nil {
		config.ErrorStatus("failed to fetch announcements", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	all := []models.ChangelogPost{}
	if err := cursor.All(ctx, &all); err != nil {
		config.ErrorStatus("failed to decode announcements", http.StatusInternalServerError, w, err)
		return
	}

	unseen := []models.ChangelogPost{}
	for _, p := range all {
		if seen[p.ID.Hex()] {
			continue
		}
		// Suppress the back-catalog: a post only shows if it was published after
		// the account was created. If we can't determine account age, show it.
		if !createdAt.IsZero() && !p.PublishedAt.Time().After(createdAt) {
			continue
		}
		unseen = append(unseen, p)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": unseen})
}

// MarkAnnouncementSeenHandler records that a user has seen a changelog post so
// it never surfaces again — persisted on the user document, so it survives an
// app reinstall. PUT /api/v1/user/{userId}/mark-announcement-seen
func (c Changelog) MarkAnnouncementSeenHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	var body struct {
		AnnouncementID string `json:"announcementId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.AnnouncementID) == "" {
		config.ErrorStatus("announcementId is required", http.StatusBadRequest, w, nil)
		return
	}
	// Guard against unbounded values being pushed into the array.
	if _, err := primitive.ObjectIDFromHex(body.AnnouncementID); err != nil {
		config.ErrorStatus("invalid announcementId", http.StatusBadRequest, w, err)
		return
	}

	ctx := context.Background()
	if _, err := c.UDB.UpdateOne(ctx, bson.M{"_id": uID}, bson.M{
		"$addToSet": bson.M{"user.seenAnnouncements": body.AnnouncementID},
	}); err != nil {
		config.ErrorStatus("failed to mark announcement seen", http.StatusInternalServerError, w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// coerceToTime best-effort converts a loosely-typed BSON value (as stored on
// User.Details.CreatedAt, an interface{}) into a time.Time. Returns the zero
// time when the value can't be interpreted.
func coerceToTime(v interface{}) time.Time {
	switch t := v.(type) {
	case primitive.DateTime:
		return t.Time()
	case time.Time:
		return t
	case int64:
		return primitive.DateTime(t).Time()
	case string:
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
