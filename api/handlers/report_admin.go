package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// ReportAdmin serves the moderation queue in the admin console.
//
// These endpoints carry the most sensitive data in the product: named
// accusations against real accounts, including allegations about children. They
// are therefore stricter than the other /admin routes, which are reachable
// directly from a browser on our own origin. The website proxies these through
// its own server so the browser never calls them, and requireReportsGateway
// enforces that.
type ReportAdmin struct {
	RDB  databases.ReportDatabase
	CODB databases.ContentOffenseDatabase
	UDB  databases.UserDatabase
	CDB  databases.CommunityDatabase
}

const (
	defaultReportsLimit = 25
	maxReportsLimit     = 100
)

// reportAdminRequest is the common body: every write carries the acting admin,
// the same shape the other admin endpoints use.
type reportAdminRequest struct {
	CurrentUser map[string]interface{} `json:"currentUser"`
	Reason      string                 `json:"reason"`
	Note        string                 `json:"note"`
	SendEmail   *bool                  `json:"sendEmail"`
}

// adminDisplayName is what a decision is attributed to on screen: the admin's
// display name from their admin account. Never their email, which would put a
// staff member's personal address in front of everyone who opens a report.
// The admin's id is recorded alongside it for the audit trail.
func adminDisplayName(currentUser map[string]interface{}) string {
	for _, key := range []string{"name", "username"} {
		v, ok := currentUser[key].(string)
		if ok && strings.TrimSpace(v) != "" && !strings.Contains(v, "@") {
			return strings.TrimSpace(v)
		}
	}
	return "Staff"
}

// adminID is the acting admin's account id, for attribution that survives a
// display-name change.
func adminID(currentUser map[string]interface{}) string {
	if v, ok := currentUser["id"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// requireReportsGateway refuses a request that did not come through the
// website's server-to-server proxy.
//
// Every other admin endpoint is reachable straight from browser JS, trusted on
// the Origin header alone. That is not an acceptable posture for a queue of
// child-safety allegations, so these endpoints additionally require the
// first-party gateway secret, which only the website backend holds.
//
// When the secret is not configured the check is skipped, matching the rest of
// the gateway's fail-open posture so that deploying this cannot brick the
// console. That case is logged loudly because it means the protection is off.
func requireReportsGateway(w http.ResponseWriter, r *http.Request) bool {
	if GatewaySecretConfigured() {
		if !HasValidGatewaySecret(r) {
			config.ErrorStatus("forbidden", http.StatusForbidden, w,
				fmt.Errorf("reports endpoints require the first-party gateway secret"))
			return false
		}
		return true
	}
	zap.S().Warnw("reports admin endpoint served without gateway enforcement: API_GATEWAY_KEY is not set")
	return true
}

// authorizeReportAdmin runs both gates: the first-party proxy, then the acting
// admin's role. Reports are visible to admin and owner, matching Server Promos.
func authorizeReportAdmin(w http.ResponseWriter, r *http.Request, currentUser map[string]interface{}) bool {
	if !requireReportsGateway(w, r) {
		return false
	}
	if err := checkAdminOrOwnerPermissions(currentUser); err != nil {
		config.ErrorStatus("insufficient permissions", http.StatusForbidden, w, err)
		return false
	}
	return true
}

// reportListItem is a queue row: the report plus the names needed to read it
// without opening every one.
type reportListItem struct {
	models.Report
	EffectiveStatus string `json:"effectiveStatus"`
	EffectiveTier   string `json:"effectiveTier"`
	IssueKnown      bool   `json:"issueKnown"`

	TargetName string `json:"targetName,omitempty"`
	// TargetMissing is set when the reported user or community no longer
	// exists, so the console can say "deleted" rather than print a raw id.
	TargetMissing bool   `json:"targetMissing,omitempty"`
	ReporterName  string `json:"reporterName,omitempty"`

	// TargetReportCount is how many reports exist against this same target.
	// A flat chronological list hides repeat offenders, which is most of the
	// signal in a queue this size.
	TargetReportCount int `json:"targetReportCount"`
}

// AdminListReportsHandler returns the moderation queue.
//
// GET /api/v1/admin/reports?status=&tier=&page=&limit=
//
// Default order is severity then oldest first. Reports with no stored rank sort
// first, so anything written before the queue existed surfaces rather than
// hiding.
func (ra ReportAdmin) AdminListReportsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var currentUser map[string]interface{}
	if raw := r.URL.Query().Get("roles"); raw != "" {
		currentUser = map[string]interface{}{"roles": strings.Split(raw, ",")}
	}
	if !authorizeReportAdmin(w, r, currentUser) {
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	page, limit := reportPaging(r)

	q := r.URL.Query()
	itemType := normalizeReportItemType(q.Get("itemType"))
	status := strings.TrimSpace(q.Get("status"))

	search, err := ra.reportSearchClause(ctx, q.Get("q"))
	if err != nil {
		config.ErrorStatus("failed to search reports", http.StatusInternalServerError, w, err)
		return
	}
	tier := tierClause(q.Get("tier"))

	// The list, the status rail and the user/community split all describe the
	// same set, so each is built from the same clauses minus the one it varies.
	filter := andClauses(typeClause(itemType), tier, search, statusClause(status))

	// Grouped mode pages over targets: every report about one account or
	// community is one case and one row. Opt-in, so an older console keeps the
	// flat list it knows how to render.
	if q.Get("group") == "target" {
		groups, groupTotal, err := ra.listGrouped(ctx, filter, page, limit)
		if err != nil {
			config.ErrorStatus("failed to list reports", http.StatusInternalServerError, w, err)
			return
		}
		reportTotal, err := ra.RDB.CountDocuments(ctx, filter)
		if err != nil {
			config.ErrorStatus("failed to count reports", http.StatusInternalServerError, w, err)
			return
		}
		counts, err := ra.statusCounts(ctx, typeClause(itemType), tier, search)
		if err != nil {
			zap.S().Warnw("failed to build report status counts", "error", err)
		}
		typeCounts, err := ra.itemTypeCounts(ctx, tier, search, statusClause(status))
		if err != nil {
			zap.S().Warnw("failed to build report type counts", "error", err)
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"groups":      groups,
			"totalCount":  groupTotal,
			"reportCount": reportTotal,
			"page":        page,
			"limit":       limit,
			"counts":      counts,
			"typeCounts":  typeCounts,
		})
		return
	}

	totalCount, err := ra.RDB.CountDocuments(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to count reports", http.StatusInternalServerError, w, err)
		return
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "severityRank", Value: 1}, {Key: "createdAt", Value: 1}}).
		SetSkip(int64(page * limit)).
		SetLimit(int64(limit))

	cursor, err := ra.RDB.Find(ctx, filter, opts)
	if err != nil {
		config.ErrorStatus("failed to list reports", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	var reports []models.Report
	if err := cursor.All(ctx, &reports); err != nil {
		config.ErrorStatus("failed to decode reports", http.StatusInternalServerError, w, err)
		return
	}

	items := ra.hydrate(ctx, reports)

	// The rail and the split are conveniences. Losing either must not cost the
	// queue, so a failed count is logged and the list still returns.
	counts, err := ra.statusCounts(ctx, typeClause(itemType), tier, search)
	if err != nil {
		zap.S().Warnw("failed to build report status counts", "error", err)
	}
	typeCounts, err := ra.itemTypeCounts(ctx, tier, search, statusClause(status))
	if err != nil {
		zap.S().Warnw("failed to build report type counts", "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":       items,
		"totalCount": totalCount,
		"page":       page,
		"limit":      limit,
		"counts":     counts,
		"typeCounts": typeCounts,
	})
}

// reportPaging reads page and limit, clamped. An unbounded limit on a
// moderation queue is how a console ends up fetching every report ever filed.
func reportPaging(r *http.Request) (page, limit int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = defaultReportsLimit
	}
	if limit > maxReportsLimit {
		limit = maxReportsLimit
	}
	return page, limit
}

// Reports target either a person or a community. Anything else a client sends
// is treated as a person, which is what every report but the ad reports is.
const (
	reportItemTypeUser      = "user"
	reportItemTypeCommunity = "community"
)

// normalizeReportItemType returns "user", "community", or "" for no filter.
func normalizeReportItemType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case reportItemTypeCommunity:
		return reportItemTypeCommunity
	case reportItemTypeUser:
		return reportItemTypeUser
	default:
		return ""
	}
}

// typeClause scopes to user or community reports. A user report is anything
// that is not a community report, so a report with an unexpected or missing
// itemType still shows up under Users rather than in neither tab.
func typeClause(itemType string) bson.M {
	switch itemType {
	case reportItemTypeCommunity:
		return bson.M{"itemType": reportItemTypeCommunity}
	case reportItemTypeUser:
		return bson.M{"itemType": bson.M{"$ne": reportItemTypeCommunity}}
	default:
		return nil
	}
}

// statusClause scopes to one workflow state. "new" also matches reports with
// no status at all: those were written before the queue existed, and matching
// only "new" would hide the whole backlog.
func statusClause(status string) bson.M {
	switch status {
	case "":
		return nil
	case models.ReportStatusNew:
		return bson.M{"$or": []bson.M{
			{"status": models.ReportStatusNew},
			{"status": bson.M{"$eq": nil}},
		}}
	default:
		return bson.M{"status": status}
	}
}

// tierClause filters by tier through the stored severity rank.
func tierClause(tier string) bson.M {
	var rank int
	switch strings.TrimSpace(tier) {
	case "":
		return nil
	case models.ReportTierEscalate:
		rank = models.SeverityRankEscalate
	case models.ReportTierWelfare:
		rank = models.SeverityRankWelfare
	case models.ReportTierSerious:
		rank = models.SeverityRankSerious
	default:
		rank = models.SeverityRankMinor
	}
	return bson.M{"severityRank": rank}
}

// andClauses combines the non-nil clauses. Every clause is its own $and member
// so two of them can each carry an $or without one overwriting the other.
func andClauses(clauses ...bson.M) bson.M {
	parts := make([]bson.M, 0, len(clauses))
	for _, c := range clauses {
		if len(c) > 0 {
			parts = append(parts, c)
		}
	}
	switch len(parts) {
	case 0:
		return bson.M{}
	case 1:
		return parts[0]
	default:
		return bson.M{"$and": parts}
	}
}

// minReportSearchLength keeps a one-character search from matching nearly
// every name in the queue.
const minReportSearchLength = 2

var objectIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{24}$`)

// reportSearchClause matches reports by the reporter or the reported account,
// found by username or email, or by a community name, or by pasting a report,
// user or community id.
//
// It searches only the accounts and communities that already appear in a
// report, never the whole users collection. There are over 840,000 users, and
// a case-insensitive username match across all of them is a slow scan; the
// people named in reports are a few dozen today and grow only with reports.
//
// Returns nil when there is nothing to search for, and a clause that matches
// nothing when the search found no one, so an empty result is an empty list
// rather than the whole queue.
func (ra ReportAdmin) reportSearchClause(ctx context.Context, raw string) (bson.M, error) {
	q := strings.TrimSpace(raw)
	if len([]rune(q)) < minReportSearchLength {
		return nil, nil
	}

	var or []bson.M

	// A pasted id: the report itself, or anyone or anything it names.
	if objectIDPattern.MatchString(q) {
		id := strings.ToLower(q)
		if oid, err := primitive.ObjectIDFromHex(id); err == nil {
			or = append(or, bson.M{"_id": oid})
		}
		or = append(or, bson.M{"reportedById": id}, bson.M{"itemId": id})
	}

	userIDs, communityIDs, err := ra.referencedIDs(ctx)
	if err != nil {
		return nil, err
	}

	pattern := bson.M{"$regex": regexp.QuoteMeta(q), "$options": "i"}

	matchedUsers, err := ra.matchUsers(ctx, userIDs, pattern)
	if err != nil {
		return nil, err
	}
	if len(matchedUsers) > 0 {
		or = append(or,
			bson.M{"reportedById": bson.M{"$in": matchedUsers}},
			bson.M{"itemType": bson.M{"$ne": reportItemTypeCommunity}, "itemId": bson.M{"$in": matchedUsers}},
		)
	}

	matchedCommunities := ra.matchCommunities(ctx, communityIDs, pattern)
	if len(matchedCommunities) > 0 {
		or = append(or, bson.M{"itemType": reportItemTypeCommunity, "itemId": bson.M{"$in": matchedCommunities}})
	}

	if len(or) == 0 {
		// Nobody matched. Return a clause nothing satisfies rather than no
		// clause, which would show the entire queue for a failed search.
		return bson.M{"_id": bson.M{"$in": []primitive.ObjectID{}}}, nil
	}
	return bson.M{"$or": or}, nil
}

// referencedIDs returns every user and community id named in any report, as
// reporter or target.
func (ra ReportAdmin) referencedIDs(ctx context.Context) (users, communities []primitive.ObjectID, err error) {
	cursor, err := ra.RDB.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$group", Value: bson.M{
			"_id":       nil,
			"reporters": bson.M{"$addToSet": "$reportedById"},
			"userTargets": bson.M{"$addToSet": bson.M{"$cond": bson.A{
				bson.M{"$ne": bson.A{"$itemType", reportItemTypeCommunity}}, "$itemId", nil}}},
			"communityTargets": bson.M{"$addToSet": bson.M{"$cond": bson.A{
				bson.M{"$eq": bson.A{"$itemType", reportItemTypeCommunity}}, "$itemId", nil}}},
		}}},
	})
	if err != nil {
		return nil, nil, err
	}
	defer cursor.Close(ctx)

	var rows []struct {
		Reporters        []interface{} `bson:"reporters"`
		UserTargets      []interface{} `bson:"userTargets"`
		CommunityTargets []interface{} `bson:"communityTargets"`
	}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, nil, err
	}
	if len(rows) == 0 {
		return nil, nil, nil
	}

	toOIDs := func(values ...[]interface{}) []primitive.ObjectID {
		seen := map[primitive.ObjectID]bool{}
		var out []primitive.ObjectID
		for _, list := range values {
			for _, v := range list {
				str, ok := v.(string)
				if !ok {
					continue
				}
				oid, err := primitive.ObjectIDFromHex(str)
				if err != nil || seen[oid] {
					continue
				}
				seen[oid] = true
				out = append(out, oid)
			}
		}
		return out
	}
	return toOIDs(rows[0].Reporters, rows[0].UserTargets), toOIDs(rows[0].CommunityTargets), nil
}

// matchUsers returns the hex ids of the given users whose username or email
// matches. The _id $in keeps it to the handful of people named in reports.
func (ra ReportAdmin) matchUsers(ctx context.Context, ids []primitive.ObjectID, pattern bson.M) ([]string, error) {
	if len(ids) == 0 || ra.UDB == nil {
		return nil, nil
	}
	cursor, err := ra.UDB.Find(ctx, bson.M{
		"_id": bson.M{"$in": ids},
		"$or": []bson.M{
			{"user.username": pattern},
			{"user.email": pattern},
		},
	}, options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var users []struct {
		ID primitive.ObjectID `bson:"_id"`
	}
	if err := cursor.All(ctx, &users); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, u.ID.Hex())
	}
	return out, nil
}

// matchCommunities returns the hex ids of reported communities whose name
// matches. A community that has since been deleted cannot be matched by name,
// only by pasting its id.
func (ra ReportAdmin) matchCommunities(ctx context.Context, ids []primitive.ObjectID, pattern bson.M) []string {
	if len(ids) == 0 || ra.CDB == nil {
		return nil
	}
	var out []string
	for _, oid := range ids {
		community, err := ra.CDB.FindOneIncludingPending(ctx, bson.M{"_id": oid, "community.name": pattern})
		if err != nil || community == nil {
			continue
		}
		out = append(out, oid.Hex())
	}
	return out
}

// statusCounts backs the rail. It takes the same scope as the list (type,
// tier, search) so the numbers on the chips describe what is on screen.
func (ra ReportAdmin) statusCounts(ctx context.Context, scope ...bson.M) (map[string]int64, error) {
	counts := map[string]int64{}
	for _, status := range []string{
		models.ReportStatusNew,
		models.ReportStatusUnderReview,
		models.ReportStatusResolved,
		models.ReportStatusDismissed,
		models.ReportStatusEscalated,
		models.ReportStatusWelfare,
	} {
		n, err := ra.RDB.CountDocuments(ctx, andClauses(append(scope, statusClause(status))...))
		if err != nil {
			return counts, err
		}
		counts[status] = n
	}
	return counts, nil
}

// itemTypeCounts backs the Users / Communities split, under the current status
// and search, so each tab says how many it would show.
func (ra ReportAdmin) itemTypeCounts(ctx context.Context, scope ...bson.M) (map[string]int64, error) {
	counts := map[string]int64{}
	for _, t := range []string{reportItemTypeUser, reportItemTypeCommunity} {
		n, err := ra.RDB.CountDocuments(ctx, andClauses(append(scope, typeClause(t))...))
		if err != nil {
			return counts, err
		}
		counts[t] = n
	}
	return counts, nil
}

// hydrate resolves names in bulk. One query per collection, never one per row:
// a queue page of twenty-five reports must not be fifty lookups.
func (ra ReportAdmin) hydrate(ctx context.Context, reports []models.Report) []reportListItem {
	userIDs := map[string]bool{}
	communityIDs := map[string]bool{}
	for _, rep := range reports {
		if rep.ReportedByID != "" {
			userIDs[rep.ReportedByID] = true
		}
		if rep.ItemType == "community" {
			communityIDs[rep.ItemID] = true
		} else if rep.ItemID != "" {
			userIDs[rep.ItemID] = true
		}
	}

	names := ra.userNames(ctx, userIDs)
	for id, name := range ra.communityNames(ctx, communityIDs) {
		names[id] = name
	}

	// One count per distinct target, so a target reported four times shows it
	// on every one of its rows.
	targetCounts := map[string]int{}
	for _, rep := range reports {
		if rep.ItemID == "" {
			continue
		}
		if _, done := targetCounts[rep.ItemID]; done {
			continue
		}
		n, err := ra.RDB.CountDocuments(ctx, bson.M{"itemId": rep.ItemID})
		if err != nil {
			zap.S().Warnw("failed to count reports for target", "itemId", rep.ItemID, "error", err)
			continue
		}
		targetCounts[rep.ItemID] = int(n)
	}

	items := make([]reportListItem, 0, len(reports))
	for _, rep := range reports {
		items = append(items, reportListItem{
			Report:            rep,
			EffectiveStatus:   rep.EffectiveStatus(),
			EffectiveTier:     rep.EffectiveTier(),
			IssueKnown:        models.IsKnownReportIssue(rep.ReportedIssue),
			TargetName:        names[rep.ItemID],
			TargetMissing:     rep.ItemID != "" && names[rep.ItemID] == "",
			ReporterName:      names[rep.ReportedByID],
			TargetReportCount: targetCounts[rep.ItemID],
		})
	}
	return items
}

func (ra ReportAdmin) userNames(ctx context.Context, ids map[string]bool) map[string]string {
	names := map[string]string{}
	objectIDs := make([]primitive.ObjectID, 0, len(ids))
	for id := range ids {
		if oid, err := primitive.ObjectIDFromHex(id); err == nil {
			objectIDs = append(objectIDs, oid)
		}
	}
	if len(objectIDs) == 0 || ra.UDB == nil {
		return names
	}

	cursor, err := ra.UDB.Find(ctx, bson.M{"_id": bson.M{"$in": objectIDs}})
	if err != nil {
		zap.S().Warnw("failed to resolve reported user names", "error", err)
		return names
	}
	defer cursor.Close(ctx)

	var users []models.User
	if err := cursor.All(ctx, &users); err != nil {
		zap.S().Warnw("failed to decode reported users", "error", err)
		return names
	}
	for _, u := range users {
		label := u.Details.Username
		if label == "" {
			label = u.Details.Email
		}
		names[u.ID] = label
	}
	return names
}

func (ra ReportAdmin) communityNames(ctx context.Context, ids map[string]bool) map[string]string {
	names := map[string]string{}
	if ra.CDB == nil {
		return names
	}
	for id := range ids {
		oid, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			continue
		}
		// A reported community may already be delisted or pending deletion, so
		// this deliberately uses the variant that still finds it.
		community, err := ra.CDB.FindOneIncludingPending(ctx, bson.M{"_id": oid})
		if err != nil || community == nil {
			continue
		}
		names[id] = community.Details.Name
	}
	return names
}

// AdminGetReportHandler returns one report with everything needed to decide on
// it: the target, the reporter, and the target's history.
//
// GET /api/v1/admin/reports/{reportId}
func (ra ReportAdmin) AdminGetReportHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var currentUser map[string]interface{}
	if raw := r.URL.Query().Get("roles"); raw != "" {
		currentUser = map[string]interface{}{"roles": strings.Split(raw, ",")}
	}
	if !authorizeReportAdmin(w, r, currentUser) {
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	report, err := ra.findReport(ctx, mux.Vars(r)["reportId"])
	if err != nil {
		config.InfoStatus("report not found", http.StatusNotFound, w, err)
		return
	}

	items := ra.hydrate(ctx, []models.Report{*report})
	offenses := ra.offensesFor(ctx, *report)
	siblings := ra.otherReportsAgainst(ctx, *report)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"report":        items[0],
		"offenses":      offenses,
		"otherReports":  siblings,
		"reporterStats": ra.reporterStats(ctx, report.ReportedByID),
	})
}

func (ra ReportAdmin) findReport(ctx context.Context, id string) (*models.Report, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid report id: %w", err)
	}
	return ra.RDB.FindOne(ctx, bson.M{"_id": oid})
}

// offensesFor returns the target's offense history, which is what the ladder
// counts and what an admin needs to see before adding to it.
func (ra ReportAdmin) offensesFor(ctx context.Context, report models.Report) []models.ContentOffense {
	filter := offenseTargetFilter(report)
	if filter == nil {
		return nil
	}
	cursor, err := ra.CODB.Find(ctx, filter, options.Find().SetSort(bson.M{"issuedAt": -1}))
	if err != nil {
		zap.S().Warnw("failed to list offenses for report target", "error", err)
		return nil
	}
	defer cursor.Close(ctx)

	var offenses []models.ContentOffense
	if err := cursor.All(ctx, &offenses); err != nil {
		zap.S().Warnw("failed to decode offenses", "error", err)
		return nil
	}
	return offenses
}

// offenseTargetFilter scopes an offense lookup to whatever the report is about.
func offenseTargetFilter(report models.Report) bson.M {
	if report.ItemID == "" {
		return nil
	}
	if report.ItemType == "community" {
		return bson.M{"scope": models.ContentOffenseScopeCommunity, "communityId": report.ItemID}
	}
	return bson.M{"scope": models.ContentOffenseScopeUser, "userId": report.ItemID}
}

// otherReportsAgainst surfaces the pattern. One target in production has four
// reports against it, which a chronological queue would scatter across pages.
func (ra ReportAdmin) otherReportsAgainst(ctx context.Context, report models.Report) []models.Report {
	if report.ItemID == "" {
		return nil
	}
	cursor, err := ra.RDB.Find(ctx,
		bson.M{"itemId": report.ItemID, "_id": bson.M{"$ne": report.ID}},
		options.Find().SetSort(bson.M{"createdAt": -1}).SetLimit(maxReportsLimit))
	if err != nil {
		zap.S().Warnw("failed to list sibling reports", "error", err)
		return nil
	}
	defer cursor.Close(ctx)

	var siblings []models.Report
	if err := cursor.All(ctx, &siblings); err != nil {
		return nil
	}
	return siblings
}

// reporterStats is how coordinated reporting becomes visible. A reporter whose
// reports are all dismissed shows up immediately.
func (ra ReportAdmin) reporterStats(ctx context.Context, reporterID string) map[string]int64 {
	if reporterID == "" {
		return nil
	}
	stats := map[string]int64{}
	for label, filter := range map[string]bson.M{
		"filed":     {"reportedById": reporterID},
		"upheld":    {"reportedById": reporterID, "status": models.ReportStatusResolved},
		"dismissed": {"reportedById": reporterID, "status": models.ReportStatusDismissed},
	} {
		n, err := ra.RDB.CountDocuments(ctx, filter)
		if err != nil {
			zap.S().Warnw("failed to count reporter history", "error", err)
			continue
		}
		stats[label] = n
	}
	return stats
}

// writeJSON is the shared success writer for these endpoints.
func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		zap.S().Errorw("failed to encode response", "error", err)
	}
}
