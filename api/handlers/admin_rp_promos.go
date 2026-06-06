package handlers

import (
	"context"
	"encoding/json"
	"fmt"
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
// All require staff (admin or owner), authorized via
// checkAdminOrOwnerPermissions against the self-asserted currentUser in the
// body — the same pattern every other admin console endpoint uses (the console
// is only rendered to a session-authenticated admin).

const (
	rpPromoModerationToSURL     = "https://www.linespolice-cad.com/terms-and-conditions"
	rpPromoModerationAppealInfo = "If you believe this is a mistake, you can appeal by opening a ticket in our Discord assistance channel: https://discord.gg/Y9ytW2ZMp4 — an admin will review it and can reverse the restriction."

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

// rpPromoPenaltyLabel is the compact label for an offense's restriction length,
// used in the admin UI (e.g. "7-day restriction").
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

// rpPromoDurationPhrase is the grammatical duration used in email sentences
// (e.g. "restricted ... for 7 days").
func rpPromoDurationPhrase(offenseNumber int) string {
	switch offenseNumber {
	case 1:
		return "7 days"
	case 2:
		return "30 days"
	case 3:
		return "1 year"
	default:
		return "permanent"
	}
}

// rpPromoInForceExpiryOr returns the expiry clauses for an in-force ban (active
// and not past expiry; a permanent ban has no expiresAt).
func rpPromoInForceExpiryOr(now time.Time) []bson.M {
	nowDT := primitive.NewDateTimeFromTime(now)
	return []bson.M{
		{"expiresAt": bson.M{"$exists": false}},
		{"expiresAt": nil},
		{"expiresAt": bson.M{"$gt": nowDT}},
	}
}

// activeRpPromoInForceFilter matches offenses that are active AND not past their
// expiry. Used for list annotation.
func activeRpPromoInForceFilter(now time.Time) bson.M {
	return bson.M{
		"status": models.RpPromoOffenseStatusActive,
		"$or":    rpPromoInForceExpiryOr(now),
	}
}

// activeRpPromoBan returns the in-force ban covering any of the given user IDs
// OR the given community, or nil if none are restricted. When multiple apply it
// returns the most restrictive (a permanent ban, else the one expiring latest).
func (c Community) activeRpPromoBan(ctx context.Context, communityID string, userIDs ...string) *models.RpPromoOffense {
	if c.OffDB == nil {
		return nil
	}
	subject := []bson.M{}
	if len(userIDs) > 0 {
		subject = append(subject, bson.M{"userId": bson.M{"$in": userIDs}})
	}
	if communityID != "" {
		subject = append(subject, bson.M{"communityId": communityID})
	}
	if len(subject) == 0 {
		return nil
	}
	filter := bson.M{
		"status": models.RpPromoOffenseStatusActive,
		"$and": []bson.M{
			{"$or": rpPromoInForceExpiryOr(time.Now())},
			{"$or": subject},
		},
	}

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

// inForceBannedSets returns the sets of user IDs and community IDs currently
// under an in-force ban, for annotating the promos list in one query.
func (c Community) inForceBannedSets(ctx context.Context) (users, communities map[string]bool) {
	users, communities = map[string]bool{}, map[string]bool{}
	if c.OffDB == nil {
		return
	}
	cur, err := c.OffDB.Find(ctx, activeRpPromoInForceFilter(time.Now()))
	if err != nil {
		return
	}
	var offenses []models.RpPromoOffense
	if err := cur.All(ctx, &offenses); err != nil {
		return
	}
	for _, o := range offenses {
		if o.UserID != "" {
			users[o.UserID] = true
		}
		if o.CommunityID != "" {
			communities[o.CommunityID] = true
		}
	}
	return
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

// countActiveRpPromoCommunityOffenses counts a community's upheld offenses, for
// the community ban escalation ladder.
func (c Community) countActiveRpPromoCommunityOffenses(ctx context.Context, communityID string) (int64, error) {
	if c.OffDB == nil {
		return 0, nil
	}
	return c.OffDB.CountDocuments(ctx, bson.M{
		"communityId": communityID,
		"status":      models.RpPromoOffenseStatusActive,
	})
}

var rpPromoNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// rpPromoNormalizeName lowercases and strips non-alphanumerics so "Vice City
// Rejects" and "ViceCity Rejects" collapse to the same key for the advisory
// name-duplicate flag.
func rpPromoNormalizeName(s string) string {
	return rpPromoNonAlnum.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "")
}

// rpPromoIgnoredOwners are internal test accounts (by username, lowercased)
// whose promotions are excluded from duplicate detection so they never produce
// false-positive flags.
var rpPromoIgnoredOwners = map[string]bool{
	"thecandyman": true,
	"lpswebsite":  true,
}

func rpPromoIsIgnoredOwner(username string) bool {
	return rpPromoIgnoredOwners[strings.ToLower(strings.TrimSpace(username))]
}

// rpPromoNormalizeInvite canonicalizes a Discord invite for duplicate matching:
// lowercased, trimmed, scheme/host-prefix removed, and trailing slash dropped,
// so "https://discord.gg/abc" and "discord.gg/abc/" match.
func rpPromoNormalizeInvite(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	v = strings.TrimSuffix(v, "/")
	for _, prefix := range []string{"https://", "http://", "www.", "discord.gg/", "discord.com/invite/"} {
		v = strings.TrimPrefix(v, prefix)
	}
	return v
}

// alertIfRpPromoFlagged checks whether a just-posted promotion is a possible
// cross-community duplicate (same invite under another community, or same name
// under another community owned by the same person) and, if so, posts a single
// review nudge to the staff alerts channel. Best-effort; runs in its own
// goroutine with a detached context.
func (c Community) alertIfRpPromoFlagged(communityID, communityName, ownerID string, data models.RpPromotionData) {
	ownerName := resolveActorName(c.UDB, ownerID)
	if rpPromoIsIgnoredOwner(ownerName) {
		return
	}
	objID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	matchKind := ""
	matched := []string{}

	// Same invite under a DIFFERENT community = literally the same server.
	if strings.TrimSpace(data.InviteURL) != "" {
		if other, oErr := c.DB.FindOneIncludingPending(ctx, bson.M{
			"_id":                                       bson.M{"$ne": objID},
			"community.rpPromotion.history.data.inviteUrl": data.InviteURL,
		}); oErr == nil && other != nil {
			matchKind = "same invite"
			matched = append(matched, other.Details.Name)
		}
	}

	// Same normalized name under another community owned by the same person.
	if matchKind == "" {
		nm := rpPromoNormalizeName(data.ServerName)
		if nm != "" {
			cur, fErr := c.DB.FindIncludingPending(ctx, bson.M{
				"_id":                            bson.M{"$ne": objID},
				"community.ownerID":              ownerID,
				"community.rpPromotion.history.0": bson.M{"$exists": true},
			})
			if fErr == nil {
				var comms []models.Community
				if cur.All(ctx, &comms) == nil {
					for _, oc := range comms {
						if oc.Details.RpPromotion == nil {
							continue
						}
						for _, h := range oc.Details.RpPromotion.History {
							if rpPromoNormalizeName(h.Data.ServerName) == nm {
								matchKind = "same name"
								matched = append(matched, oc.Details.Name)
								break
							}
						}
						if matchKind != "" {
							break
						}
					}
				}
			}
		}
	}

	if matchKind == "" {
		return
	}
	allNames := append([]string{communityName}, matched...)
	sendRpPromoFlaggedAlert(data.ServerName, ownerName, matchKind, allNames, data.InviteURL, communityID+"|"+matchKind)
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
	Removed         bool   `json:"removed"`
	OwnerBanned     bool   `json:"ownerBanned"`
	CommunityBanned bool   `json:"communityBanned"`

	// Duplicate grouping. A promo is only flagged when its invite or normalized
	// server name is shared across 2+ DISTINCT communities — a single community
	// re-promoting on different days (respecting the cooldown) is legitimate and
	// never flagged. Members of the same set share DupGroupID so the UI can
	// group/pair them under one header.
	DupGroupID        string `json:"dupGroupId,omitempty"`        // stable key, e.g. "inv:<url>" or "name:<norm>"
	DupGroupType      string `json:"dupGroupType,omitempty"`      // "invite" | "name"
	DupGroupValue     string `json:"dupGroupValue,omitempty"`     // the shared invite URL or server name, for display
	DupCommunityCount int    `json:"dupCommunityCount,omitempty"` // distinct communities sharing the key
	DupAlsoName       bool   `json:"dupAlsoName,omitempty"`       // invite-grouped row that ALSO shares its name cross-community
	DupAlsoInvite     bool   `json:"dupAlsoInvite,omitempty"`     // name-grouped row that ALSO shares its invite cross-community
}

type rpPromoAggRow struct {
	CommunityID   primitive.ObjectID     `bson:"communityId"`
	CommunityName string                 `bson:"communityName"`
	OwnerID       string                 `bson:"ownerId"`
	Post          models.RpPromotionPost `bson:"post"`
}

// computeRpPromoItems loads every promotion across all communities (newest
// first), annotates each with advisory duplicate flags + ban status, and groups
// duplicate-set members adjacently. Shared by the list endpoint and the
// flagged-count badge so both apply identical rules.
func (c Community) computeRpPromoItems(ctx context.Context) ([]adminRpPromoItem, error) {
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
		return nil, err
	}
	var rows []rpPromoAggRow
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}

	bannedUsers, bannedCommunities := c.inForceBannedSets(ctx)
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

	// First pass: tally the set of DISTINCT communities behind each invite and
	// each name. Only cross-community reuse is suspicious — one community
	// re-promoting on different days (respecting the cooldown) is fine.
	//
	// Invite is owner-agnostic: the same Discord invite under two communities is
	// literally the same server, whoever owns it. Name is scoped per OWNER
	// (key = name + ownerID): generic names like "San Andreas State Roleplay"
	// collide across unrelated communities constantly, so a name match only
	// signals evasion when the SAME owner is behind both communities. Internal
	// test accounts are skipped entirely so they never produce flags.
	inviteCommunities := map[string]map[string]bool{} // invite -> set of communityIds
	nameCommunities := map[string]map[string]bool{}   // name+ownerID -> set of communityIds
	for _, row := range rows {
		if rpPromoIsIgnoredOwner(resolveName(row.OwnerID)) {
			continue
		}
		cid := row.CommunityID.Hex()
		if inv := rpPromoNormalizeInvite(row.Post.Data.InviteURL); inv != "" {
			if inviteCommunities[inv] == nil {
				inviteCommunities[inv] = map[string]bool{}
			}
			inviteCommunities[inv][cid] = true
		}
		if nm := rpPromoNormalizeName(row.Post.Data.ServerName); nm != "" {
			key := nm + "\x00" + row.OwnerID
			if nameCommunities[key] == nil {
				nameCommunities[key] = map[string]bool{}
			}
			nameCommunities[key][cid] = true
		}
	}

	// Second pass: build items, assigning each a duplicate group when its invite
	// or name is shared across communities (invite preferred, name fallback).
	items := make([]adminRpPromoItem, 0, len(rows))
	for _, row := range rows {
		p := row.Post
		ownerName := resolveName(row.OwnerID)
		ignored := rpPromoIsIgnoredOwner(ownerName)
		inv := rpPromoNormalizeInvite(p.Data.InviteURL)
		nm := rpPromoNormalizeName(p.Data.ServerName)
		nameKey := ""
		if nm != "" {
			nameKey = nm + "\x00" + row.OwnerID
		}
		dupInvite := !ignored && inv != "" && len(inviteCommunities[inv]) > 1
		dupName := !ignored && nameKey != "" && len(nameCommunities[nameKey]) > 1

		postedByName := p.PostedByName
		if postedByName == "" {
			postedByName = resolveName(p.PostedBy)
		}
		it := adminRpPromoItem{
			CommunityID:     row.CommunityID.Hex(),
			CommunityName:   row.CommunityName,
			OwnerID:         row.OwnerID,
			OwnerName:       ownerName,
			PostID:          p.ID,
			PostedBy:        p.PostedBy,
			PostedByName:    postedByName,
			PostedAt:        p.PostedAt.Time().UTC().Format(time.RFC3339),
			Tier:            p.Tier,
			ServerName:      p.Data.ServerName,
			Game:            p.Data.Game,
			Consoles:        p.Data.Consoles,
			InviteURL:       p.Data.InviteURL,
			MessageID:       p.MessageID,
			MessageLink:     rpPromotionMessageLink(p.ChannelID, p.MessageID),
			Removed:         p.RemovedAt != nil,
			OwnerBanned:     bannedUsers[row.OwnerID],
			CommunityBanned: bannedCommunities[row.CommunityID.Hex()],
		}
		switch {
		case dupInvite:
			it.DupGroupID = "inv:" + inv
			it.DupGroupType = "invite"
			it.DupGroupValue = p.Data.InviteURL
			it.DupCommunityCount = len(inviteCommunities[inv])
			it.DupAlsoName = dupName
		case dupName:
			it.DupGroupID = "name:" + nameKey
			it.DupGroupType = "name"
			it.DupGroupValue = p.Data.ServerName
			it.DupCommunityCount = len(nameCommunities[nameKey])
		}
		items = append(items, it)
	}

	// Group duplicate-set members adjacently (grouped sets first, newest first
	// within a set), then the rest newest first. PostedAt is RFC3339 so string
	// comparison is chronological.
	sort.SliceStable(items, func(i, j int) bool {
		gi, gj := items[i].DupGroupID, items[j].DupGroupID
		if (gi == "") != (gj == "") {
			return gi != "" // grouped rows first
		}
		if gi != gj {
			return gi < gj // keep a set together
		}
		return items[i].PostedAt > items[j].PostedAt // newest first
	})
	return items, nil
}

// AdminListRpPromosHandler returns every promotion across all communities,
// newest first, annotated with advisory duplicate flags and the owner's current
// ban status. Staff (admin or owner).
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
	if err := checkAdminOrOwnerPermissions(req.CurrentUser); err != nil {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	items, err := c.computeRpPromoItems(ctx)
	if err != nil {
		config.ErrorStatus("failed to load promotions", http.StatusInternalServerError, w, err)
		return
	}

	// Filters (applied in memory so duplicate grouping reflects the global set).
	search := strings.ToLower(strings.TrimSpace(req.Search))
	filtered := items[:0]
	for _, it := range items {
		if req.DuplicatesOnly && it.DupGroupID == "" {
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

// AdminRpPromoFlaggedCountHandler returns how many promotions are currently
// flagged as possible duplicates and how many distinct sets they form — used
// for the sidebar review badge. Staff (admin or owner).
func (c Community) AdminRpPromoFlaggedCountHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req adminCurrentUserBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if err := checkAdminOrOwnerPermissions(req.CurrentUser); err != nil {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	items, err := c.computeRpPromoItems(ctx)
	if err != nil {
		config.ErrorStatus("failed to load promotions", http.StatusInternalServerError, w, err)
		return
	}

	// Count only sets that still have 2+ LIVE (not removed) members — once an
	// admin removes the duplicate posts, the set stops counting and the badge
	// clears, so it reflects what still needs review.
	liveByGroup := map[string]int{}
	for _, it := range items {
		if it.DupGroupID != "" && !it.Removed {
			liveByGroup[it.DupGroupID]++
		}
	}
	flaggedSets, flaggedPromos := 0, 0
	for _, n := range liveByGroup {
		if n >= 2 {
			flaggedSets++
			flaggedPromos += n
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"flaggedPromos": flaggedPromos,
		"flaggedSets":   flaggedSets,
	})
}

// AdminDeleteRpPromoHandler removes a single promotion from the Discord channel
// and marks its history entry removed (retained as evidence). Staff (admin or owner).
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
	if err := checkAdminOrOwnerPermissions(req.CurrentUser); err != nil {
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

// adminRpPromoBanRequest is the shared body for preview / ban / test-email. A
// ban can target the poster (banUser), the community (banCommunity), or both.
type adminRpPromoBanRequest struct {
	adminCurrentUserBody
	BanUser         bool                        `json:"banUser"`
	BanCommunity    bool                        `json:"banCommunity"`
	UserID          string                      `json:"userId"`        // the poster, when banUser
	CommunityID     string                      `json:"communityId"`   // when banCommunity
	CommunityName   string                      `json:"communityName"` // hint; resolved server-side too
	Reason          string                      `json:"reason"`
	Evidence        []adminRpPromoEvidenceInput `json:"evidence"`
	RemoveLivePosts bool                        `json:"removeLivePosts"`
	SendEmail       *bool                       `json:"sendEmail"`
	TestEmail       string                      `json:"testEmail"` // test-email override recipient
}

func (r adminRpPromoBanRequest) validate() error {
	if !r.BanUser && !r.BanCommunity {
		return fmt.Errorf("select a user and/or a community to ban")
	}
	if r.BanUser && strings.TrimSpace(r.UserID) == "" {
		return fmt.Errorf("userId is required to ban a user")
	}
	if r.BanCommunity && strings.TrimSpace(r.CommunityID) == "" {
		return fmt.Errorf("communityId is required to ban a community")
	}
	return nil
}

// rpPromoRestrictionLine is one restriction applied to a single recipient.
type rpPromoRestrictionLine struct {
	Scope         string
	Label         string
	OffenseNumber int
	ExpiresAt     *primitive.DateTime
}

// rpPromoNotification is the set of restrictions for one email recipient. When
// the poster also owns the banned community they collapse into one notification.
type rpPromoNotification struct {
	Email        string
	Username     string
	Restrictions []rpPromoRestrictionLine
}

// rpPromoBanPlan is the computed outcome of a ban request: the offense
// document(s) to insert and the deduped per-recipient email notifications.
type rpPromoBanPlan struct {
	UserOffense      *models.RpPromoOffense
	CommunityOffense *models.RpPromoOffense
	Notifications    []rpPromoNotification
}

// resolveCommunityOwnerContact returns the owner email/username and the
// community name for a community, used to notify the owner of a community ban.
func (c Community) resolveCommunityOwnerContact(ctx context.Context, communityID string) (email, username, communityName string) {
	objID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		return "", "", ""
	}
	community, err := c.DB.FindOneIncludingPending(ctx, bson.M{"_id": objID})
	if err != nil {
		return "", "", ""
	}
	email, username = resolveUserContact(c.UDB, community.Details.OwnerID)
	return email, username, community.Details.Name
}

// buildRpPromoBanPlan computes the offense(s) and grouped recipient
// notifications for a ban request without writing anything.
func (c Community) buildRpPromoBanPlan(ctx context.Context, req adminRpPromoBanRequest, adminEmail string) *rpPromoBanPlan {
	now := time.Now()
	nowDT := primitive.NewDateTimeFromTime(now)
	evidence := parseEvidence(req.Evidence)
	reason := strings.TrimSpace(req.Reason)
	plan := &rpPromoBanPlan{}

	// Group restrictions by recipient email so one person never gets two emails.
	byEmail := map[string]*rpPromoNotification{}
	add := func(email, username string, line rpPromoRestrictionLine) {
		if email == "" {
			return
		}
		n := byEmail[email]
		if n == nil {
			n = &rpPromoNotification{Email: email, Username: username}
			byEmail[email] = n
		}
		n.Restrictions = append(n.Restrictions, line)
	}

	if req.BanUser {
		prior, _ := c.countActiveRpPromoOffenses(ctx, req.UserID)
		n := int(prior) + 1
		exp := rpPromoOffenseExpiry(n, now)
		email, username := resolveUserContact(c.UDB, req.UserID)
		plan.UserOffense = &models.RpPromoOffense{
			Scope: models.RpPromoOffenseScopeUser, UserID: req.UserID, Username: username, Email: email,
			OffenseNumber: n, Reason: reason, Evidence: evidence, IssuedBy: adminEmail,
			IssuedAt: nowDT, ExpiresAt: exp, Status: models.RpPromoOffenseStatusActive,
		}
		add(email, username, rpPromoRestrictionLine{Scope: "user", Label: "your account", OffenseNumber: n, ExpiresAt: exp})
	}

	if req.BanCommunity {
		prior, _ := c.countActiveRpPromoCommunityOffenses(ctx, req.CommunityID)
		n := int(prior) + 1
		exp := rpPromoOffenseExpiry(n, now)
		ownerEmail, ownerName, commName := c.resolveCommunityOwnerContact(ctx, req.CommunityID)
		if commName == "" {
			commName = req.CommunityName
		}
		plan.CommunityOffense = &models.RpPromoOffense{
			Scope: models.RpPromoOffenseScopeCommunity, CommunityID: req.CommunityID, CommunityName: commName,
			Username: ownerName, Email: ownerEmail, OffenseNumber: n, Reason: reason, Evidence: evidence,
			IssuedBy: adminEmail, IssuedAt: nowDT, ExpiresAt: exp, Status: models.RpPromoOffenseStatusActive,
		}
		add(ownerEmail, ownerName, rpPromoRestrictionLine{Scope: "community", Label: `the community "` + commName + `"`, OffenseNumber: n, ExpiresAt: exp})
	}

	// Stable order (by email) so previews and tests are deterministic.
	emails := make([]string, 0, len(byEmail))
	for e := range byEmail {
		emails = append(emails, e)
	}
	sort.Strings(emails)
	for _, e := range emails {
		plan.Notifications = append(plan.Notifications, *byEmail[e])
	}
	return plan
}

// renderNotificationEmail builds the email bodies for one recipient notification.
func renderNotificationEmail(n rpPromoNotification, reason string, ev []models.RpPromoEvidence, testBanner string) (htmlBody, textBody string) {
	restrictions := make([]templates.RpPromoOffenseRestriction, 0, len(n.Restrictions))
	for _, l := range n.Restrictions {
		liftsAt := ""
		if l.ExpiresAt != nil {
			liftsAt = l.ExpiresAt.Time().UTC().Format("Jan 2, 2006")
		}
		restrictions = append(restrictions, templates.RpPromoOffenseRestriction{
			Label:         l.Label,
			PenaltyLabel:  rpPromoDurationPhrase(l.OffenseNumber),
			LiftsAt:       liftsAt,
			OffenseNumber: l.OffenseNumber,
		})
	}
	return templates.RenderRpPromoOffenseEmail(templates.RpPromoOffenseEmailParams{
		Username:     n.Username,
		Restrictions: restrictions,
		Reason:       reason,
		Evidence:     evidenceEmailLines(ev),
		ToSURL:       rpPromoModerationToSURL,
		AppealInfo:   rpPromoModerationAppealInfo,
		TestBanner:   testBanner,
	})
}

func offenseExpiresStr(o *models.RpPromoOffense) string {
	if o == nil || o.ExpiresAt == nil {
		return "permanent"
	}
	return o.ExpiresAt.Time().UTC().Format(time.RFC3339)
}

// AdminRpPromoBanPreviewHandler computes the penalty(ies) that would apply and
// renders the recipient email(s), without writing anything. Staff (admin/owner).
func (c Community) AdminRpPromoBanPreviewHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req adminRpPromoBanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if err := checkAdminOrOwnerPermissions(req.CurrentUser); err != nil {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, err)
		return
	}
	if err := req.validate(); err != nil {
		config.ErrorStatus(err.Error(), http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	adminEmail, _ := req.CurrentUser["email"].(string)

	plan := c.buildRpPromoBanPlan(ctx, req, adminEmail)
	evidence := parseEvidence(req.Evidence)

	notifications := make([]map[string]interface{}, 0, len(plan.Notifications))
	for _, n := range plan.Notifications {
		_, textBody := renderNotificationEmail(n, req.Reason, evidence, "")
		notifications = append(notifications, map[string]interface{}{
			"email":     n.Email,
			"username":  n.Username,
			"emailText": textBody,
		})
	}

	resp := map[string]interface{}{"notifications": notifications}
	if plan.UserOffense != nil {
		resp["user"] = map[string]interface{}{
			"offenseNumber": plan.UserOffense.OffenseNumber,
			"penaltyLabel":  rpPromoPenaltyLabel(plan.UserOffense.OffenseNumber),
			"expiresAt":     offenseExpiresStr(plan.UserOffense),
			"email":         plan.UserOffense.Email,
			"username":      plan.UserOffense.Username,
		}
	}
	if plan.CommunityOffense != nil {
		resp["community"] = map[string]interface{}{
			"offenseNumber": plan.CommunityOffense.OffenseNumber,
			"penaltyLabel":  rpPromoPenaltyLabel(plan.CommunityOffense.OffenseNumber),
			"expiresAt":     offenseExpiresStr(plan.CommunityOffense),
			"communityName": plan.CommunityOffense.CommunityName,
			"ownerEmail":    plan.CommunityOffense.Email,
			"ownerUsername": plan.CommunityOffense.Username,
		}
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// AdminRpPromoBanTestEmailHandler renders the same email(s) a real ban would
// send and delivers them to the admin (or a provided testEmail) — no offense is
// recorded, nothing is enforced, no posts are removed. Lets staff verify the
// email end-to-end (safe to run against the ignored test accounts). Staff only.
func (c Community) AdminRpPromoBanTestEmailHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req adminRpPromoBanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if err := checkAdminOrOwnerPermissions(req.CurrentUser); err != nil {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, err)
		return
	}
	if err := req.validate(); err != nil {
		config.ErrorStatus(err.Error(), http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	adminEmail, _ := req.CurrentUser["email"].(string)

	dest := strings.TrimSpace(req.TestEmail)
	if dest == "" {
		dest = adminEmail
	}
	if dest == "" {
		config.ErrorStatus("no test recipient — provide testEmail or sign in with an email", http.StatusBadRequest, w, nil)
		return
	}

	plan := c.buildRpPromoBanPlan(ctx, req, adminEmail)
	evidence := parseEvidence(req.Evidence)

	sent := 0
	for _, n := range plan.Notifications {
		realRecipient := n.Email
		if realRecipient == "" {
			realRecipient = "(no email on file)"
		}
		banner := "This is a test. A real ban would send this to " + realRecipient + "."
		if err := c.sendNotificationEmail(dest, n, req.Reason, evidence, banner); err != nil {
			zap.S().Errorw("rp promo moderation: test email failed", "to", dest, "error", err)
			config.ErrorStatus("failed to send test email", http.StatusBadGateway, w, err)
			return
		}
		sent++
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "test email sent",
		"sentTo":  dest,
		"count":   sent,
	})
}

// AdminRpPromoBanHandler issues escalating bans against the poster and/or the
// community, optionally removes their live promos from Discord, and emails each
// affected party. Staff (admin or owner).
func (c Community) AdminRpPromoBanHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req adminRpPromoBanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if err := checkAdminOrOwnerPermissions(req.CurrentUser); err != nil {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, err)
		return
	}
	if err := req.validate(); err != nil {
		config.ErrorStatus(err.Error(), http.StatusBadRequest, w, err)
		return
	}
	adminEmail, _ := req.CurrentUser["email"].(string)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	plan := c.buildRpPromoBanPlan(ctx, req, adminEmail)
	evidence := parseEvidence(req.Evidence)

	// Optionally remove the live promos first so a partial failure surfaces
	// before any ban is recorded.
	removed := 0
	if req.RemoveLivePosts {
		removed = c.removeEvidencePosts(ctx, evidence, adminEmail)
	}

	// Send one email per affected recipient (default on). Track which recipient
	// emails succeeded so we can stamp emailSentAt on the matching offense(s).
	emailsSent := 0
	sentTo := map[string]bool{}
	if req.SendEmail == nil || *req.SendEmail {
		for _, n := range plan.Notifications {
			if n.Email == "" {
				continue
			}
			if err := c.sendNotificationEmail(n.Email, n, req.Reason, evidence, ""); err != nil {
				zap.S().Errorw("rp promo moderation: offense email failed", "to", n.Email, "error", err)
				continue
			}
			emailsSent++
			sentTo[n.Email] = true
		}
	}

	nowDT := primitive.NewDateTimeFromTime(time.Now())
	resp := map[string]interface{}{
		"message":      "ban issued",
		"emailsSent":   emailsSent,
		"removedPosts": removed,
	}

	insert := func(o *models.RpPromoOffense, key, scope, targetID, targetName string) {
		if o == nil {
			return
		}
		if sentTo[o.Email] {
			o.EmailSentAt = &nowDT
		}
		res, err := c.OffDB.InsertOne(ctx, *o)
		if err != nil {
			zap.S().Errorw("rp promo moderation: failed to record offense", "scope", scope, "error", err)
			return
		}
		id := ""
		if oid, ok := res.Decode().(primitive.ObjectID); ok {
			id = oid.Hex()
		}
		resp[key] = map[string]interface{}{
			"offenseId":     id,
			"offenseNumber": o.OffenseNumber,
			"expiresAt":     offenseExpiresStr(o),
		}
		logAudit(c.ALDB, primitive.NilObjectID, "rp_promotion.banned", scope, "", adminEmail, targetID, targetName,
			map[string]interface{}{"offenseNumber": o.OffenseNumber, "reason": req.Reason, "removedPosts": removed})
	}
	insert(plan.UserOffense, "user", "user", req.UserID, "")
	if plan.CommunityOffense != nil {
		insert(plan.CommunityOffense, "community", "community", req.CommunityID, plan.CommunityOffense.CommunityName)
	}

	_ = json.NewEncoder(w).Encode(resp)
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

// sendNotificationEmail renders a recipient notification and sends it via
// SendGrid to toEmail (the real recipient, or an override for test sends).
func (c Community) sendNotificationEmail(toEmail string, n rpPromoNotification, reason string, ev []models.RpPromoEvidence, testBanner string) error {
	htmlBody, textBody := renderNotificationEmail(n, reason, ev, testBanner)
	subject := "Action taken on your Lines Police CAD server promotions"
	if testBanner != "" {
		subject = "[TEST] " + subject
	}
	from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
	to := mail.NewEmail(n.Username, toEmail)
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
// optionally filtered to one user or to currently in-force bans. Staff (admin or owner).
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
	if err := checkAdminOrOwnerPermissions(req.CurrentUser); err != nil {
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
			"scope":         o.Scope,
			"userId":        o.UserID,
			"communityId":   o.CommunityID,
			"communityName": o.CommunityName,
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
// counting toward escalation and lifts the ban. Staff (admin or owner).
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
	if err := checkAdminOrOwnerPermissions(req.CurrentUser); err != nil {
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
