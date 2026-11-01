package databases

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/models"
)

// developerReportDraftCollectionName holds the reviewable drafts a key wrote.
//
// Its own collection, not the arrest_reports or citations one. That is the
// enforcement, not a convention: nothing that reads a filed report reads this
// collection, so a draft cannot be mistaken for a record by any surface, and a
// bug in this package cannot produce one.
const developerReportDraftCollectionName = "developer_report_drafts"

// DeveloperReportDraftDatabase stores report drafts.
//
// UpdateOne returns the document after the write so an upsert can answer with
// the current draft in one round trip, which is what the idempotent POST needs:
// a retry has to be told what the stored state now is, not just that it worked.
type DeveloperReportDraftDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.DeveloperReportDraft, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.DeveloperReportDraft, error)
	InsertOne(ctx context.Context, draft models.DeveloperReportDraft) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*models.DeveloperReportDraft, error)
	DeleteMany(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) (int64, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type developerReportDraftDatabase struct{ db DatabaseHelper }

// NewDeveloperReportDraftDatabase creates a report draft database wrapper.
func NewDeveloperReportDraftDatabase(db DatabaseHelper) DeveloperReportDraftDatabase {
	return &developerReportDraftDatabase{db: db}
}

func (d *developerReportDraftDatabase) FindOne(ctx context.Context, filter interface{}) (*models.DeveloperReportDraft, error) {
	draft := &models.DeveloperReportDraft{}
	if err := d.db.Collection(developerReportDraftCollectionName).FindOne(ctx, filter).Decode(draft); err != nil {
		return nil, err
	}
	return draft, nil
}

func (d *developerReportDraftDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.DeveloperReportDraft, error) {
	cur, err := d.db.Collection(developerReportDraftCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var drafts []models.DeveloperReportDraft
	if err := cur.All(ctx, &drafts); err != nil {
		return nil, err
	}
	return drafts, nil
}

func (d *developerReportDraftDatabase) InsertOne(ctx context.Context, draft models.DeveloperReportDraft) (InsertOneResultHelper, error) {
	return d.db.Collection(developerReportDraftCollectionName).InsertOne(ctx, draft)
}

func (d *developerReportDraftDatabase) UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*models.DeveloperReportDraft, error) {
	if _, err := d.db.Collection(developerReportDraftCollectionName).UpdateOne(ctx, filter, update, opts...); err != nil {
		return nil, err
	}
	draft, err := d.FindOne(ctx, filter)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return draft, err
}

func (d *developerReportDraftDatabase) DeleteMany(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) (int64, error) {
	return d.db.Collection(developerReportDraftCollectionName).DeleteMany(ctx, filter, opts...)
}

func (d *developerReportDraftDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return d.db.Collection(developerReportDraftCollectionName).CountDocuments(ctx, filter, opts...)
}
