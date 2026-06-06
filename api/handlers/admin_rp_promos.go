package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	templates "github.com/linesmerrill/police-cad-api/templates/html"
)

// Admin RP server promotion moderation.
//
//	POST /api/v1/admin/rp-promos                     — list all promos (+ duplicate flags)
//	POST /api/v1/admin/rp-promos/delete              — remove a promo from Discord
//	POST /api/v1/admin/rp-promos/ban/preview         — compute penalty + render evidence email
//	POST /api/v1/admin/rp-promos/ban                 — issue an escalating ban + send email
//	POST /api/v1/admin/rp-promos/offenses            — list offenses (banned-users view)
//	POST /api/v1/admin/rp-promos/offenses/{id}/reverse — overturn an offense on appeal
//
// All are owner-only, authorized via checkAdminPermissions against the
// self-asserted currentUser in the body — the same pattern every other admin
// console endpoint uses (the console is only rendered to a session-
// authenticated admin).

const (
	rpPromoModerationToSURL     = "https://www.linespolice-cad.com/terms-and-conditions"
	rpPromoModerationAppealInfo = "If you believe this is a mistake, open a ticket in the assistance channel of the Lines Police CAD Discord server. An admin will review it and can reverse the restriction."

	rpPromoModerationDefaultLimit = 25
	rpPromoModerationMaxLimit     = 100
)

// rpPromoOffenseExpiry returns when an offense's restriction lifts, given its
// 1-based number on the escalation ladder. A nil result means permanent.
//
//	1st → 7 days, 2nd → 30 days, 3rd → 1 year, 4th+ → permanent.
func rpPromoOffenseExpiry(offenseNumber int, now time.Time) *primitive.DateTime {
	var until time.Time
	switch offenseNumber {
	case 1:
		until = now.Add(7 * 24 * time.Hour)
	case 2:
		until = now.Add(30 * 24 * time.Hour)
	case 3:
		until = now.Add(365 * 24 * time.Hour)
	default:
		return nil // permanent
	}
	dt := primitive.NewDateTimeFromTime(until)
	return &dt
}

// rpPromoPenaltyLabel is the human label for an offense's restriction length.
func rpPromoPenaltyLabel(offenseNumber int) string {
	switch offenseNumber {
	case 1:
		return "7-day"
	case 2:
		return "30-day"
	case 3:
		return "1-year"
	default:
		return "permanent"
	}
}

// activeRpPromoInForceFilter matches offenses that are active AND not past their
// expiry (a permanent ban has no expiresAt). Used for the enforcement gate.
func activeRpPromoInForceFilter(now time.Time) bson.M {
	nowDT := primitive.NewDateTimeFromTime(now)
	return bson.M{
		"status": models.RpPromoOffenseStatusActive,
		"$or": []bson.M{
			{"expiresAt": bson.M{"$exists": false}},
			{"expiresAt": nil},
			{"expiresAt": bson.M{"$gt": nowDT}},
		},
	}
}

// activeRpPromoBan returns the in-force ban covering any of the given user IDs,
// or nil if none are restricted. When multiple apply it returns the most
// restrictive (a permanent ban, else the one expiring latest).
func (c Community) activeRpPromoBan(ctx context.Context, userIDs ...string) *models.RpPromoOffense {
	if c.OffDB == nil || len(userIDs) == 0 {
		return nil
	}
	filter := activeRpPromoInForceFilter(time.Now())
	filter["userId"] = bson.M{"$in": userIDs}

	cur, err := c.OffDB.Find(ctx, filter)
	if err != nil {
		zap.S().Errorw("rp promo moderation: ban lookup failed", "error", err)
		return nil
	}
	var offenses []models.RpPromoOffense
	if err := cur.All(ctx, &offenses); err != nil || len(offenses) == 0 {
		return nil
	}

	best := offenses[0]
	for _, o := range offenses[1:] {
		if best.ExpiresAt == nil {
			break // already permanent — nothing is more restrictive
		}
		if o.ExpiresAt == nil || o.ExpiresAt.Time().After(best.ExpiresAt.Time()) {
			best = o
		}
	}
	return &best
}

// inForceBannedUserSet returns the set of user IDs currently under an in-force
// ban, for annotating the promos list in one query rather than per row.
func (c Community) inForceBannedUserSet(ctx context.Context) map[string]bool {
	set := map[string]bool{}
	if c.OffDB == nil {
		return set
	}
	cur, err := c.OffDB.Find(ctx, activeRpPromoInForceFilter(time.Now()))
	if err != nil {
		return set
	}
	var offenses []models.RpPromoOffense
	if err := cur.All(ctx, &offenses); err != nil {
		return set
	}
	for _, o := range offenses {
		set[o.UserID] = true
	}
	return set
}

// countActiveRpPromoOffenses counts a user's upheld (non-reversed) offenses.
// Expired-by-time offenses still count — escalation is cumulative — so the
// next offense number is this count + 1.
func (c Community) countActiveRpPromoOffenses(ctx context.Context, userID string) (int64, error) {
	if c.OffDB == nil {
		return 0, nil
	}
	return c.OffDB.CountDocuments(ctx, bson.M{
		"userId": userID,
		"status": models.RpPromoOffenseStatusActive,
	})
}

var rpPromoNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// rpPromoNormalizeName lowercases and strips non-alphanumerics so "Vice City
// Rejects" and "ViceCity Rejects" collapse to the same key for the advisory
// name-duplicate flag.
func rpPromoNormalizeName(s string) string {
	return rpPromoNonAlnum.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "")
}

// resolveUserContact returns a user's email and username from their ID.
func resolveUserContact(udb databases.UserDatabase, userID string) (email, username string) {
	if userID == "" {
		return "", ""
	}
	objID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var user models.User
	if err := udb.FindOne(ctx, bson.M{"_id": objID}).Decode(&user); err != nil {
		return "", ""
	}
	return user.Details.Email, user.Details.Username
}

// ---- request/response shapes ----

type adminCurrentUserBody struct {
	CurrentUser map[string]interface{} `json:"currentUser"`
}

type adminRpPromoItem struct {
	CommunityID   string   `json:"communityId"`
	CommunityName string   `json:"communityName"`
	OwnerID       string   `json:"ownerId"`
	OwnerName     string   `json:"ownerName"`
	PostID        string   `json:"postId"`
	PostedBy      string   `json:"postedBy"`
	PostedByName  string   `json:"postedByName"`
	PostedAt      string   `json:"postedAt"`
	Tier          string   `json:"tier"`
	ServerName    string   `json:"serverName"`
	Game          string   `json:"game"`
	Consoles      []string `json:"consoles"`
	InviteURL     string   `json:"inviteUrl"`
	MessageID     string   `json:"messageId"`
	MessageLink   string   `json:"messageLink"`
	Removed       bool     `json:"removed"`
	OwnerDup      bool     `json:"ownerDup"`
	NameDup       bool     `json:"nameDup"`
	InviteDup     bool     `json:"inviteDup"`
	OwnerBanned   bool     `json:"ownerBanned"`
}

type rpPromoAggRow struct {
	CommunityID   primitive.ObjectID     `bson:"communityId"`
	CommunityName string                 `bson:"communityName"`
	OwnerID       string                 `bson:"ownerId"`
	Post          models.RpPromotionPost `bson:"post"`
}

// AdminListRpPromosHandler returns every promotion across all communities,
// newest first, annotated with advisory duplicate flags and the owner's current
// ban status. Owner-only.
func (c Community) AdminListRpPromosHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		adminCurrentUserBody
		Search         string `json:"search"`
		DuplicatesOnly bool   `json:"duplicatesOnly"`
		Page           int    `json:"page"`
		Limit          int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if err := checkAdminPermissions(req.CurrentUser); err != nil {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"community.rpPromotion.history.0": bson.M{"$exists": true}}}},
		{{Key: "$unwind", Value: "$community.rpPromotion.history"}},
		{{Key: "$project", Value: bson.M{
			"communityId":   "$_id",
			"communityName": "$community.name",
			"ownerId":       "$community.ownerID",
			"post":          "$community.rpPromotion.history",
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "post.postedAt", Value: -1}}}},
	}

	cur, err := c.DB.AggregateIncludingPending(ctx, pipeline)
	if err != nil {
		config.ErrorStatus("failed to load promotions", http.StatusInternalServerError, w, err)
		return
	}
	var rows []rpPromoAggRow
	if err := cur.All(ctx, &rows); err != nil {
		config.ErrorStatus("failed to decode promotions", http.StatusInternalServerError, w, err)
		return
	}

	// First pass: tally duplicate keys across the whole set.
	ownerCount := map[string]int{}
	inviteCount := map[string]int{}
	nameCommunities := map[string]map[string]bool{} // normName -> set of communityIds
	for _, row := range rows {
		ownerCount[row.OwnerID]++
		if inv := strings.ToLower(strings.TrimSpace(row.Post.Data.InviteURL)); inv != "" {
			inviteCount[inv]++
		}
		if nm := rpPromoNormalizeName(row.Post.Data.ServerName); nm != "" {
			if nameCommunities[nm] == nil {
				nameCommunities[nm] = map[string]bool{}
			}
			nameCommunities[nm][row.CommunityID.Hex()] = true
		}
	}

	banned := c.inForceBannedUserSet(ctx)
	nameCache := map[string]string{}
	resolveName := func(id string) string {
		if id == "" {
			return ""
		}
		if v, ok := nameCache[id]; ok {
			return v
		}
		v := resolveActorName(c.UDB, id)
		nameCache[id] = v
		return v
	}

	// Second pass: build items with flags applied.
	items := make([]adminRpPromoItem, 0, len(rows))
	for _, row := range rows {
		p := row.Post
		inv := strings.ToLower(strings.TrimSpace(p.Data.InviteURL))
		nm := rpPromoNormalizeName(p.Data.ServerName)
		postedByName := p.PostedByName
		if postedByName == "" {
			postedByName = resolveName(p.PostedBy)
		}
		items = append(items, adminRpPromoItem{
			CommunityID:   row.CommunityID.Hex(),
			CommunityName: row.CommunityName,
			OwnerID:       row.OwnerID,
			OwnerName:     resolveName(row.OwnerID),
			PostID:        p.ID,
			PostedBy:      p.PostedBy,
			PostedByName:  postedByName,
			PostedAt:      p.PostedAt.Time().UTC().Format(time.RFC3339),
			Tier:          p.Tier,
			ServerName:    p.Data.ServerName,
			Game:          p.Data.Game,
			Consoles:      p.Data.Consoles,
			InviteURL:     p.Data.InviteURL,
			MessageID:     p.MessageID,
			MessageLink:   rpPromotionMessageLink(p.ChannelID, p.MessageID),
			Removed:       p.RemovedAt != nil,
			OwnerDup:      ownerCount[row.OwnerID] > 1,
			NameDup:       nm != "" && len(nameCommunities[nm]) > 1,
			InviteDup:     inv != "" && inviteCount[inv] > 1,
			OwnerBanned:   banned[row.OwnerID],
		})
	}

	// Filters (applied in memory so duplicate flags reflect the global set).
	search := strings.ToLower(strings.TrimSpace(req.Search))
	filtered := items[:0]
	for _, it := range items {
		if req.DuplicatesOnly && !(it.OwnerDup || it.NameDup || it.InviteDup) {
			continue
		}
		if search != "" {
			hay := strings.ToLower(strings.Join([]string{
				it.CommunityName, it.ServerName, it.OwnerName, it.PostedByName, it.InviteURL,
			}, " "))
			if !strings.Contains(hay, search) {
				continue
			}
		}
		filtered = append(filtered, it)
	}

	// Pagination.
	limit := req.Limit
	if limit <= 0 {
		limit = rpPromoModerationDefaultLimit
	}
	if limit > rpPromoModerationMaxLimit {
		limit = rpPromoModerationMaxLimit
	}
	page := req.Page
	if page < 1 {
		page = 1
	}
	total := len(filtered)
	start := (page - 1) * limit
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       filtered[start:end],
		"totalCount": total,
		"page":       page,
		"limit":      limit,
	})
}

// AdminDeleteRpPromoHandler removes a single promotion from the Discord channel
// and marks its history entry removed (retained as evidence). Owner-only.
func (c Community) AdminDeleteRpPromoHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		adminCurrentUserBody
		CommunityID string `json:"communityId"`
		PostID      string `json:"postId"`
		Reason      string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if err := checkAdminPermissions(req.CurrentUser); err != nil {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, err)
		return
	}
	adminEmail, _ := req.CurrentUser["email"].(string)

	communityObjID, err := primitive.ObjectIDFromHex(req.CommunityID)
	if err != nil {
		config.ErrorStatus("invalid communityId", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	community, err := c.DB.FindOneIncludingPending(ctx, bson.M{"_id": communityObjID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	// Locate the post in history to recover its Discord message ID.
	var target *models.RpPromotionPost
	if community.Details.RpPromotion != nil {
		for i := range community.Details.RpPromotion.History {
			if community.Details.RpPromotion.History[i].ID == req.PostID {
				target = &community.Details.RpPromotion.History[i]
				break
			}
		}
	}
	if target == nil {
		config.ErrorStatus("promotion not found", http.StatusNotFound, w, nil)
		return
	}

	// Best-effort Discord removal. A missing message ID or already-deleted
	// message is not an error — the goal is "not live", which is then true.
	webhookURL := os.Getenv(rpPromoWebhookEnv)
	if target.MessageID != "" {
		if err := deleteRpPromotionWebhookMessage(webhookURL, target.MessageID); err != nil {
			config.ErrorStatus("failed to remove the Discord message", http.StatusBadGateway, w, err)
			return
		}
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	update := bson.M{"$set": bson.M{
		"community.rpPromotion.history.$[elem].removedAt":    now,
		"community.rpPromotion.history.$[elem].removedBy":    adminEmail,
		"community.rpPromotion.history.$[elem].removeReason": strings.TrimSpace(req.Reason),
	}}
	opts := options.Update().SetArrayFilters(options.ArrayFilters{
		Filters: []interface{}{bson.M{"elem.id": req.PostID}},
	})
	if err := c.DB.UpdateOne(ctx, bson.M{"_id": communityObjID}, update, opts); err != nil {
		config.ErrorStatus("removed from Discord but failed to update record", http.StatusInternalServerError, w, err)
		return
	}

	logAudit(c.ALDB, communityObjID, "rp_promotion.removed", "community", "", adminEmail, req.PostID, "",
		map[string]interface{}{"serverName": target.Data.ServerName, "reason": req.Reason})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"message": "promotion removed"})
}

// adminRpPromoEvidenceInput is the wire shape for an evidence promo.
type adminRpPromoEvidenceInput struct {
	CommunityID   string `json:"communityId"`
	CommunityName string `json:"communityName"`
	ServerName    string `json:"serverName"`
	InviteURL     string `json:"inviteUrl"`
	MessageID     string `json:"messageId"`
	PostID        string `json:"postId"`
	PostedAt      string `json:"postedAt"`
}

func parseEvidence(in []adminRpPromoEvidenceInput) []models.RpPromoEvidence {
	out := make([]models.RpPromoEvidence, 0, len(in))
	for _, e := range in {
		postedAt := primitive.NewDateTimeFromTime(time.Now())
		if t, err := time.Parse(time.RFC3339, e.PostedAt); err == nil {
			postedAt = primitive.NewDateTimeFromTime(t)
		}
		out = append(out, models.RpPromoEvidence{
			CommunityID:   e.CommunityID,
			CommunityName: e.CommunityName,
			ServerName:    e.ServerName,
			InviteURL:     e.InviteURL,
			MessageID:     e.MessageID,
			PostedAt:      postedAt,
		})
	}
	return out
}

func evidenceEmailLines(ev []models.RpPromoEvidence) []templates.RpPromoOffenseEvidenceLine {
	lines := make([]templates.RpPromoOffenseEvidenceLine, 0, len(ev))
	for _, e := range ev {
		lines = append(lines, templates.RpPromoOffenseEvidenceLine{
			CommunityName: e.CommunityName,
			ServerName:    e.ServerName,
			InviteURL:     e.InviteURL,
			PostedAt:      e.PostedAt.Time().UTC().Format("Jan 2, 2006 15:04 UTC"),
		})
	}
	return lines
}

// AdminRpPromoBanPreviewHandler computes the penalty that would apply and
// renders the evidence email, without writing anything. Owner-only.
func (c Community) AdminRpPromoBanPreviewHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		adminCurrentUserBody
		UserID   string                      `json:"userId"`
		Reason   string                      `json:"reason"`
		Evidence []adminRpPromoEvidenceInput `json:"evidence"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if err := checkAdminPermissions(req.CurrentUser); err != nil {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, err)
		return
	}
	if strings.TrimSpace(req.UserID) == "" {
		config.ErrorStatus("userId is required", http.StatusBadRequest, w, nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	prior, err := c.countActiveRpPromoOffenses(ctx, req.UserID)
	if err != nil {
		config.ErrorStatus("failed to compute offense history", http.StatusInternalServerError, w, err)
		return
	}
	offenseNumber := int(prior) + 1
	now := time.Now()
	expiresAt := rpPromoOffenseExpiry(offenseNumber, now)

	email, username := resolveUserContact(c.UDB, req.UserID)
	htmlBody, textBody := renderOffenseEmail(username, offenseNumber, expiresAt, req.Reason, parseEvidence(req.Evidence))

	expiresStr := ""
	if expiresAt != nil {
		expiresStr = expiresAt.Time().UTC().Format(time.RFC3339)
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"offenseNumber":  offenseNumber,
		"penaltyLabel":   rpPromoPenaltyLabel(offenseNumber),
		"expiresAt":      expiresStr,
		"recipientEmail": email,
		"username":       username,
		"emailHtml":      htmlBody,
		"emailText":      textBody,
	})
}

// renderOffenseEmail builds the offense email bodies for both preview and send.
func renderOffenseEmail(username string, offenseNumber int, expiresAt *primitive.DateTime, reason string, ev []models.RpPromoEvidence) (htmlBody, textBody string) {
	liftsAt := ""
	if expiresAt != nil {
		liftsAt = expiresAt.Time().UTC().Format("Jan 2, 2006")
	}
	return templates.RenderRpPromoOffenseEmail(templates.RpPromoOffenseEmailParams{
		Username:      username,
		OffenseNumber: offenseNumber,
		PenaltyLabel:  rpPromoPenaltyLabel(offenseNumber),
		LiftsAt:       liftsAt,
		Reason:        reason,
		Evidence:      evidenceEmailLines(ev),
		ToSURL:        rpPromoModerationToSURL,
		AppealInfo:    rpPromoModerationAppealInfo,
	})
}

// AdminRpPromoBanHandler issues an escalating ban against a user, optionally
// removes their live promos from Discord, and sends the evidence email.
// Owner-only.
func (c Community) AdminRpPromoBanHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		adminCurrentUserBody
		UserID         string                      `json:"userId"`
		Reason         string                      `json:"reason"`
		Evidence       []adminRpPromoEvidenceInput `json:"evidence"`
		RemoveLivePosts bool                       `json:"removeLivePosts"`
		SendEmail      *bool                       `json:"sendEmail"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if err := checkAdminPermissions(req.CurrentUser); err != nil {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, err)
		return
	}
	if strings.TrimSpace(req.UserID) == "" {
		config.ErrorStatus("userId is required", http.StatusBadRequest, w, nil)
		return
	}
	adminEmail, _ := req.CurrentUser["email"].(string)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	prior, err := c.countActiveRpPromoOffenses(ctx, req.UserID)
	if err != nil {
		config.ErrorStatus("failed to compute offense history", http.StatusInternalServerError, w, err)
		return
	}
	offenseNumber := int(prior) + 1
	now := time.Now()
	nowDT := primitive.NewDateTimeFromTime(now)
	expiresAt := rpPromoOffenseExpiry(offenseNumber, now)
	evidence := parseEvidence(req.Evidence)

	email, username := resolveUserContact(c.UDB, req.UserID)

	offense := models.RpPromoOffense{
		UserID:        req.UserID,
		Username:      username,
		Email:         email,
		OffenseNumber: offenseNumber,
		Reason:        strings.TrimSpace(req.Reason),
		Evidence:      evidence,
		IssuedBy:      adminEmail,
		IssuedAt:      nowDT,
		ExpiresAt:     expiresAt,
		Status:        models.RpPromoOffenseStatusActive,
	}

	// Optionally remove the user's live promos from Discord first, so a partial
	// failure here surfaces before we record the ban.
	removed := 0
	if req.RemoveLivePosts {
		removed = c.removeEvidencePosts(ctx, evidence, adminEmail)
	}

	// Send the evidence email (default on). A send failure does not block the
	// ban — the restriction still applies; we just report emailSent=false.
	emailSent := false
	if req.SendEmail == nil || *req.SendEmail {
		if email != "" {
			if err := sendOffenseEmail(email, username, offenseNumber, expiresAt, req.Reason, evidence); err != nil {
				zap.S().Errorw("rp promo moderation: offense email failed", "userId", req.UserID, "error", err)
			} else {
				emailSent = true
				offense.EmailSentAt = &nowDT
			}
		}
	}

	res, err := c.OffDB.InsertOne(ctx, offense)
	if err != nil {
		config.ErrorStatus("failed to record the ban", http.StatusInternalServerError, w, err)
		return
	}
	offenseID := ""
	if oid, ok := res.Decode().(primitive.ObjectID); ok {
		offenseID = oid.Hex()
	}

	logAudit(c.ALDB, primitive.NilObjectID, "rp_promotion.banned", "user", "", adminEmail, req.UserID, email,
		map[string]interface{}{"offenseNumber": offenseNumber, "reason": req.Reason, "removedPosts": removed})

	expiresStr := "permanent"
	if expiresAt != nil {
		expiresStr = expiresAt.Time().UTC().Format(time.RFC3339)
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"message":       "ban issued",
		"offenseId":     offenseID,
		"offenseNumber": offenseNumber,
		"expiresAt":     expiresStr,
		"emailSent":     emailSent,
		"removedPosts":  removed,
	})
}

// removeEvidencePosts removes from Discord, and marks removed, each evidence
// promo that is still live. Returns how many were removed. Best-effort: a
// single failure is logged and skipped so one bad message can't block a ban.
func (c Community) removeEvidencePosts(ctx context.Context, evidence []models.RpPromoEvidence, adminEmail string) int {
	webhookURL := os.Getenv(rpPromoWebhookEnv)
	removed := 0
	for _, e := range evidence {
		if e.MessageID == "" || e.CommunityID == "" {
			continue
		}
		communityObjID, err := primitive.ObjectIDFromHex(e.CommunityID)
		if err != nil {
			continue
		}
		if err := deleteRpPromotionWebhookMessage(webhookURL, e.MessageID); err != nil {
			zap.S().Warnw("rp promo moderation: failed to delete evidence post", "messageId", e.MessageID, "error", err)
			continue
		}
		now := primitive.NewDateTimeFromTime(time.Now())
		update := bson.M{"$set": bson.M{
			"community.rpPromotion.history.$[elem].removedAt":    now,
			"community.rpPromotion.history.$[elem].removedBy":    adminEmail,
			"community.rpPromotion.history.$[elem].removeReason": "removed with ban",
		}}
		opts := options.Update().SetArrayFilters(options.ArrayFilters{
			Filters: []interface{}{bson.M{"elem.messageId": e.MessageID}},
		})
		if err := c.DB.UpdateOne(ctx, bson.M{"_id": communityObjID}, update, opts); err != nil {
			zap.S().Warnw("rp promo moderation: deleted from discord but failed to mark removed", "messageId", e.MessageID, "error", err)
		}
		removed++
	}
	return removed
}

// sendOffenseEmail renders and sends the offense email via SendGrid.
func sendOffenseEmail(toEmail, username string, offenseNumber int, expiresAt *primitive.DateTime, reason string, ev []models.RpPromoEvidence) error {
	htmlBody, textBody := renderOffenseEmail(username, offenseNumber, expiresAt, reason, ev)
	subject := "Action taken on your Lines Police CAD server promotions"
	from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
	to := mail.NewEmail(username, toEmail)
	message := mail.NewSingleEmail(from, subject, to, textBody, htmlBody)
	client := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))
	resp, err := client.Send(message)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return &sendgridStatusError{status: resp.StatusCode}
	}
	return nil
}

type sendgridStatusError struct{ status int }

func (e *sendgridStatusError) Error() string {
	return "sendgrid returned status " + http.StatusText(e.status)
}

// AdminListRpPromoOffensesHandler lists offenses for the banned-users view,
// optionally filtered to one user or to currently in-force bans. Owner-only.
func (c Community) AdminListRpPromoOffensesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		adminCurrentUserBody
		UserID    string `json:"userId"`
		ActiveOnly bool  `json:"activeOnly"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if err := checkAdminPermissions(req.CurrentUser); err != nil {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{}
	if strings.TrimSpace(req.UserID) != "" {
		filter["userId"] = req.UserID
	}
	if req.ActiveOnly {
		for k, v := range activeRpPromoInForceFilter(time.Now()) {
			filter[k] = v
		}
	}

	cur, err := c.OffDB.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "issuedAt", Value: -1}}))
	if err != nil {
		config.ErrorStatus("failed to load offenses", http.StatusInternalServerError, w, err)
		return
	}
	var offenses []models.RpPromoOffense
	if err := cur.All(ctx, &offenses); err != nil {
		config.ErrorStatus("failed to decode offenses", http.StatusInternalServerError, w, err)
		return
	}
	// Newest-first is already applied by the sort; keep stable on equal times.
	sort.SliceStable(offenses, func(i, j int) bool {
		return offenses[i].IssuedAt.Time().After(offenses[j].IssuedAt.Time())
	})

	now := time.Now()
	out := make([]map[string]interface{}, 0, len(offenses))
	for _, o := range offenses {
		inForce := o.Status == models.RpPromoOffenseStatusActive && (o.ExpiresAt == nil || o.ExpiresAt.Time().After(now))
		expiresStr := "permanent"
		if o.ExpiresAt != nil {
			expiresStr = o.ExpiresAt.Time().UTC().Format(time.RFC3339)
		}
		out = append(out, map[string]interface{}{
			"id":            o.ID.Hex(),
			"userId":        o.UserID,
			"username":      o.Username,
			"email":         o.Email,
			"offenseNumber": o.OffenseNumber,
			"reason":        o.Reason,
			"evidence":      o.Evidence,
			"issuedBy":      o.IssuedBy,
			"issuedAt":      o.IssuedAt.Time().UTC().Format(time.RFC3339),
			"expiresAt":     expiresStr,
			"status":        o.Status,
			"inForce":       inForce,
			"reversedBy":    o.ReversedBy,
			"reversalReason": o.ReversalReason,
		})
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": out, "totalCount": len(out)})
}

// AdminReverseRpPromoOffenseHandler overturns an offense on appeal — it stops
// counting toward escalation and lifts the ban. Owner-only.
func (c Community) AdminReverseRpPromoOffenseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	offenseID := mux.Vars(r)["id"]
	offenseObjID, err := primitive.ObjectIDFromHex(offenseID)
	if err != nil {
		config.ErrorStatus("invalid offense id", http.StatusBadRequest, w, err)
		return
	}

	var req struct {
		adminCurrentUserBody
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if err := checkAdminPermissions(req.CurrentUser); err != nil {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, err)
		return
	}
	adminEmail, _ := req.CurrentUser["email"].(string)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	now := primitive.NewDateTimeFromTime(time.Now())
	update := bson.M{"$set": bson.M{
		"status":         models.RpPromoOffenseStatusReversed,
		"reversedBy":     adminEmail,
		"reversedAt":     now,
		"reversalReason": strings.TrimSpace(req.Reason),
	}}
	if err := c.OffDB.UpdateOne(ctx, bson.M{"_id": offenseObjID}, update); err != nil {
		config.ErrorStatus("failed to reverse offense", http.StatusInternalServerError, w, err)
		return
	}

	logAudit(c.ALDB, primitive.NilObjectID, "rp_promotion.ban_reversed", "user", "", adminEmail, offenseID, "",
		map[string]interface{}{"reason": req.Reason})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"message": "offense reversed"})
}
