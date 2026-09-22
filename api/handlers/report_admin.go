package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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

// adminDisplayName pulls something to attribute the action to. Email is what
// the admin console sends and what the other admin endpoints record.
func adminDisplayName(currentUser map[string]interface{}) string {
	for _, key := range []string{"email", "name", "username"} {
		if v, ok := currentUser[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return "unknown admin"
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

	TargetName   string `json:"targetName,omitempty"`
	ReporterName string `json:"reporterName,omitempty"`

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
	filter := reportQueueFilter(r)

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

	counts, err := ra.statusCounts(ctx)
	if err != nil {
		// The rail is a convenience. Losing it must not cost the queue.
		zap.S().Warnw("failed to build report status counts", "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":       items,
		"totalCount": totalCount,
		"page":       page,
		"limit":      limit,
		"counts":     counts,
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

// reportQueueFilter builds the list filter from the query string.
func reportQueueFilter(r *http.Request) bson.M {
	filter := bson.M{}

	switch status := strings.TrimSpace(r.URL.Query().Get("status")); status {
	case "":
		// No filter: the whole queue.
	case models.ReportStatusNew:
		// A report written before the status field existed has none, and is
		// new by definition. Matching only "new" would hide all forty of them.
		filter["$or"] = []bson.M{
			{"status": models.ReportStatusNew},
			{"status": bson.M{"$eq": nil}},
		}
	default:
		filter["status"] = status
	}

	if tier := strings.TrimSpace(r.URL.Query().Get("tier")); tier != "" {
		filter["severityRank"] = models.ReportSeverityRank(tierProbeIssue(tier))
	}
	if itemType := strings.TrimSpace(r.URL.Query().Get("itemType")); itemType != "" {
		filter["itemType"] = itemType
	}
	return filter
}

// tierProbeIssue maps a tier back to an issue in it, so a tier filter can be
// expressed as the stored numeric rank.
func tierProbeIssue(tier string) string {
	switch tier {
	case models.ReportTierEscalate:
		return "Child Safety"
	case models.ReportTierWelfare:
		return "Suicide or Self-Harm"
	case models.ReportTierSerious:
		return "Hate"
	default:
		return "Spam"
	}
}

// statusCounts backs the rail. "new" counts reports with no status too.
func (ra ReportAdmin) statusCounts(ctx context.Context) (map[string]int64, error) {
	counts := map[string]int64{}
	for _, status := range []string{
		models.ReportStatusNew,
		models.ReportStatusUnderReview,
		models.ReportStatusResolved,
		models.ReportStatusDismissed,
		models.ReportStatusEscalated,
		models.ReportStatusWelfare,
	} {
		var filter bson.M
		if status == models.ReportStatusNew {
			filter = bson.M{"$or": []bson.M{
				{"status": models.ReportStatusNew},
				{"status": bson.M{"$eq": nil}},
			}}
		} else {
			filter = bson.M{"status": status}
		}
		n, err := ra.RDB.CountDocuments(ctx, filter)
		if err != nil {
			return counts, err
		}
		counts[status] = n
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
