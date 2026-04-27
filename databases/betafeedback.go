package databases

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const betaFeedbackName = "betafeedback"

// BetaFeedbackDatabase contains the methods to use with the betafeedback collection.
type BetaFeedbackDatabase interface {
	InsertOne(ctx context.Context, fb models.BetaFeedback) (InsertOneResultHelper, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	Aggregate(ctx context.Context, pipeline interface{}) (*MongoCursor, error)
}

type betaFeedbackDatabase struct {
	db DatabaseHelper
}

// NewBetaFeedbackDatabase wires up the collection.
func NewBetaFeedbackDatabase(db DatabaseHelper) BetaFeedbackDatabase {
	return &betaFeedbackDatabase{db: db}
}

func (b *betaFeedbackDatabase) InsertOne(ctx context.Context, fb models.BetaFeedback) (InsertOneResultHelper, error) {
	return b.db.Collection(betaFeedbackName).InsertOne(ctx, fb)
}

func (b *betaFeedbackDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return b.db.Collection(betaFeedbackName).Find(ctx, filter, opts...)
}

func (b *betaFeedbackDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return b.db.Collection(betaFeedbackName).CountDocuments(ctx, filter, opts...)
}

func (b *betaFeedbackDatabase) Aggregate(ctx context.Context, pipeline interface{}) (*MongoCursor, error) {
	return b.db.Collection(betaFeedbackName).Aggregate(ctx, pipeline)
}
