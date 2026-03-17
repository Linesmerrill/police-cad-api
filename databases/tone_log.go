package databases

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const toneLogCollectionName = "tone_logs"

// ToneLogDatabase defines the interface for tone log operations.
type ToneLogDatabase interface {
	InsertOne(ctx context.Context, log models.ToneLog, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type toneLogDatabase struct {
	db DatabaseHelper
}

// NewToneLogDatabase creates a new tone log database wrapper.
func NewToneLogDatabase(db DatabaseHelper) ToneLogDatabase {
	return &toneLogDatabase{db: db}
}

func (t *toneLogDatabase) InsertOne(ctx context.Context, log models.ToneLog, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return t.db.Collection(toneLogCollectionName).InsertOne(ctx, log, opts...)
}

func (t *toneLogDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return t.db.Collection(toneLogCollectionName).Find(ctx, filter, opts...)
}

func (t *toneLogDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return t.db.Collection(toneLogCollectionName).CountDocuments(ctx, filter, opts...)
}
