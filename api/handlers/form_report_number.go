package handlers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api/handlers/formdefaults"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// reportNumbering allocates report numbers and inserts form submissions.
//
// It lives on its own because there is more than one way into the
// formSubmissions collection. An officer filling in a form on the website goes
// through CreateFormSubmissionHandler; a supervisor filing a draft an
// integration prepared goes through the developer review routes. Both land in
// the same collection, and that collection carries a unique index on
// (communityID, reportNumber) — so a path that forgets to allocate a number
// writes reportNumber:"" and works exactly once per community, then fails on
// every report after it with a duplicate key. That is not a theoretical shape
// of bug: the review path shipped without numbering and did precisely this.
//
// Keeping the allocation and the insert together means a new caller cannot
// have one without the other.
type reportNumbering struct {
	Subs      databases.FormSubmissionDatabase
	Counters  databases.FormCounterDatabase
	Templates databases.FormTemplateDatabase
}

// defaultReportNumberFormat is used when neither the community's template nor
// the built-in default names one.
const defaultReportNumberFormat = "RR-{YYYY}-{NNNNNN}"

// maxReportNumberAttempts bounds the renumber-and-retry loop below.
const maxReportNumberAttempts = 5

// NextNumber allocates the next report number for a community and slug,
// formatted the way that template asks for.
func (n reportNumbering) NextNumber(ctx context.Context, communityID, slug string) (string, error) {
	if n.Counters == nil {
		return "", fmt.Errorf("report numbering unavailable")
	}
	year := time.Now().Year()
	seq, err := n.Counters.NextSeq(ctx, communityID, slug, year)
	if err != nil {
		return "", err
	}

	format := defaultReportNumberFormat
	if n.Templates != nil {
		tpl, err := n.Templates.FindOne(ctx, bson.M{
			"formTemplate.communityID": communityID,
			"formTemplate.slug":        slug,
		})
		if err == nil && tpl != nil && tpl.Details.NumberFormat != "" {
			format = tpl.Details.NumberFormat
		} else if def, ok := formdefaults.All()[slug]; ok && def.NumberFormat != "" {
			format = def.NumberFormat
		}
	} else if def, ok := formdefaults.All()[slug]; ok && def.NumberFormat != "" {
		format = def.NumberFormat
	}

	out := strings.ReplaceAll(format, "{YYYY}", strconv.Itoa(year))
	out = strings.ReplaceAll(out, "{NNNNNN}", fmt.Sprintf("%06d", seq))
	return out, nil
}

// maxUsedSeq returns the highest numeric tail across all reportNumbers used in
// this community for the given year, e.g. "RR-2026-000042" → 42. Used to seed a
// stale per-slug counter that trails the actually-used number space. Returns
// ok=false when no matching submissions exist.
func (n reportNumbering) maxUsedSeq(ctx context.Context, communityID string, year int) (int64, bool) {
	yearStr := strconv.Itoa(year)
	filter := bson.M{
		"formSubmission.communityID":  communityID,
		"formSubmission.reportNumber": bson.M{"$regex": "-" + yearStr + "-"},
	}
	opts := options.FindOne().SetSort(bson.M{"formSubmission.reportNumber": -1})
	top, err := n.Subs.FindOne(ctx, filter, opts)
	if err != nil || top == nil {
		return 0, false
	}
	parts := strings.Split(top.Details.ReportNumber, "-")
	if len(parts) == 0 {
		return 0, false
	}
	seq, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil {
		return 0, false
	}
	return seq, true
}

// Insert writes a submission, renumbering and retrying on a duplicate-number
// collision when the number was allocated rather than supplied by the caller.
//
// The formCounter is keyed by (communityID, slug, year) while the unique index
// on submissions is (communityID, reportNumber), so a brand-new template starts
// at seq=1 and collides with the existing template's first reports. On the
// first collision we catch the per-slug counter up to the highest seq used
// across every slug in this community and year, which bounds the retry depth
// even when another template has hundreds of submissions, then retry with a
// small budget to absorb concurrent writes.
//
// sub is updated in place: on success its ReportNumber and ID are whatever
// actually got written.
func (n reportNumbering) Insert(ctx context.Context, sub *models.FormSubmission, autoNumber bool) error {
	year := time.Now().Year()
	caughtUp := false
	var insertErr error
	for attempt := 0; attempt < maxReportNumberAttempts; attempt++ {
		_, insertErr = n.Subs.InsertOne(ctx, *sub)
		if insertErr == nil {
			return nil
		}
		if !mongo.IsDuplicateKeyError(insertErr) || !autoNumber || attempt == maxReportNumberAttempts-1 {
			return insertErr
		}
		if !caughtUp {
			if maxSeq, ok := n.maxUsedSeq(ctx, sub.Details.CommunityID, year); ok {
				_ = n.Counters.CatchUp(ctx, sub.Details.CommunityID, sub.Details.FormTemplateSlug, year, maxSeq)
			}
			caughtUp = true
		}
		next, err := n.NextNumber(ctx, sub.Details.CommunityID, sub.Details.FormTemplateSlug)
		if err != nil {
			return err
		}
		sub.Details.ReportNumber = next
		sub.ID = primitive.NewObjectID()
	}
	return insertErr
}
