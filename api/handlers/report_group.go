package handlers

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/models"
)

// Several reports about the same account or community are one case and are
// decided together: a pile-on of four reports is one strike, not four rungs up
// the ladder in one sitting.
//
// A decision only ever sweeps up reports on the same track, though. Dismissing
// a spam report must never quietly close a child safety allegation filed
// against the same account, and upholding a harassment report must never
// "resolve" a self-harm report that a person still has to read.
const (
	reportTrackLadder   = "ladder"
	reportTrackEscalate = "escalate"
	reportTrackWelfare  = "welfare"
)

// reportTrack says which decision a report belongs to.
func reportTrack(r models.Report) string {
	switch r.EffectiveTier() {
	case models.ReportTierEscalate:
		return reportTrackEscalate
	case models.ReportTierWelfare:
		return reportTrackWelfare
	default:
		return reportTrackLadder
	}
}

// openReportsAgainst returns every undecided report about the same target,
// including the one given.
func (ra ReportAdmin) openReportsAgainst(ctx context.Context, report models.Report) ([]models.Report, error) {
	if report.ItemID == "" {
		return []models.Report{report}, nil
	}
	filter := andClauses(
		bson.M{"itemId": report.ItemID},
		typeClause(reportItemTypeOf(report)),
		bson.M{"$or": []bson.M{
			{"status": models.ReportStatusNew},
			{"status": models.ReportStatusUnderReview},
			{"status": bson.M{"$eq": nil}},
		}},
	)
	cursor, err := ra.RDB.Find(ctx, filter, options.Find().SetSort(bson.D{
		{Key: "severityRank", Value: 1}, {Key: "createdAt", Value: 1},
	}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var reports []models.Report
	if err := cursor.All(ctx, &reports); err != nil {
		return nil, err
	}
	// The report being acted on is always part of its own case, even if a
	// replica lag or a race means the query did not return it.
	for _, r := range reports {
		if r.ID == report.ID {
			return reports, nil
		}
	}
	return append([]models.Report{report}, reports...), nil
}

// reportItemTypeOf mirrors typeClause: anything that is not a community
// report is a user report.
func reportItemTypeOf(r models.Report) string {
	if r.ItemType == reportItemTypeCommunity {
		return reportItemTypeCommunity
	}
	return reportItemTypeUser
}

// onTrack filters a case down to the reports on one track.
func onTrack(reports []models.Report, track string) []models.Report {
	var out []models.Report
	for _, r := range reports {
		if reportTrack(r) == track {
			out = append(out, r)
		}
	}
	return out
}

// mostSevereIssue picks the issue a combined strike is issued under: the one
// with the lowest severity rank, and the oldest among equals. A case holding
// one hate report and two spam reports is a hate case.
func mostSevereIssue(reports []models.Report, fallback string) string {
	best := fallback
	bestRank := models.ReportSeverityRank(fallback)
	for _, r := range reports {
		if rank := models.ReportSeverityRank(r.ReportedIssue); rank < bestRank {
			best, bestRank = r.ReportedIssue, rank
		}
	}
	return best
}

func reportIDs(reports []models.Report) []string {
	out := make([]string, 0, len(reports))
	for _, r := range reports {
		out = append(out, r.ID.Hex())
	}
	return out
}

// reportGroup is one row of the grouped queue: every report in the current
// view about one account or community, reviewed together.
type reportGroup struct {
	ItemType      string           `json:"itemType"`
	ItemID        string           `json:"itemId"`
	TargetName    string           `json:"targetName,omitempty"`
	TargetMissing bool             `json:"targetMissing,omitempty"`
	Tier          string           `json:"tier"`
	Reports       []reportListItem `json:"reports"`
	// TotalAgainst counts every report ever filed about this target, decided
	// or not, which can be more than the reports in this view.
	TotalAgainst int `json:"totalAgainst"`
}

// listGrouped pages over targets rather than reports, so a case is never split
// across two pages. Order is the most severe report in the case, then the
// oldest, which keeps the child safety cases at the top exactly as the flat
// list does.
func (ra ReportAdmin) listGrouped(ctx context.Context, filter bson.M, page, limit int) ([]reportGroup, int64, error) {
	targetKey := bson.M{
		"itemType": bson.M{"$cond": bson.A{bson.M{"$eq": bson.A{"$itemType", reportItemTypeCommunity}}, reportItemTypeCommunity, reportItemTypeUser}},
		"itemId":   "$itemId",
	}
	cursor, err := ra.RDB.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: filter}},
		{{Key: "$sort", Value: bson.D{{Key: "severityRank", Value: 1}, {Key: "createdAt", Value: 1}}}},
		{{Key: "$group", Value: bson.M{
			"_id":     targetKey,
			"reports": bson.M{"$push": "$$ROOT"},
			// A report with no rank predates the queue; -1 keeps it at the top
			// rather than letting $min skip it.
			"rank":   bson.M{"$min": bson.M{"$ifNull": bson.A{"$severityRank", -1}}},
			"oldest": bson.M{"$min": "$createdAt"},
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "rank", Value: 1}, {Key: "oldest", Value: 1}, {Key: "_id.itemId", Value: 1}}}},
		{{Key: "$facet", Value: bson.M{
			"page":  bson.A{bson.M{"$skip": page * limit}, bson.M{"$limit": limit}},
			"total": bson.A{bson.M{"$count": "n"}},
		}}},
	})
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var facets []struct {
		Page []struct {
			ID struct {
				ItemType string `bson:"itemType"`
				ItemID   string `bson:"itemId"`
			} `bson:"_id"`
			Reports []models.Report `bson:"reports"`
		} `bson:"page"`
		Total []struct {
			N int64 `bson:"n"`
		} `bson:"total"`
	}
	if err := cursor.All(ctx, &facets); err != nil {
		return nil, 0, err
	}
	if len(facets) == 0 {
		return []reportGroup{}, 0, nil
	}

	var total int64
	if len(facets[0].Total) > 0 {
		total = facets[0].Total[0].N
	}

	// Hydrate every report on the page in one pass, then split back out.
	var flat []models.Report
	for _, g := range facets[0].Page {
		flat = append(flat, g.Reports...)
	}
	hydrated := ra.hydrate(ctx, flat)

	groups := make([]reportGroup, 0, len(facets[0].Page))
	i := 0
	for _, g := range facets[0].Page {
		items := hydrated[i : i+len(g.Reports)]
		i += len(g.Reports)

		group := reportGroup{ItemType: g.ID.ItemType, ItemID: g.ID.ItemID, Reports: items}
		if len(items) > 0 {
			group.TargetName = items[0].TargetName
			group.TargetMissing = items[0].TargetMissing
			group.TotalAgainst = items[0].TargetReportCount
			// The pipeline sorted by severity, so the first report is the
			// most severe in the case.
			group.Tier = items[0].EffectiveTier
		}
		groups = append(groups, group)
	}
	return groups, total, nil
}
