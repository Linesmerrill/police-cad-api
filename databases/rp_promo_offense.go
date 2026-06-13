package databases

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const rpPromoOffenseCollectionName = "rp_promo_offenses"

// RpPromoOffenseDatabase defines the interface for rp_promo_offenses operations.
type RpPromoOffenseDatabase interface {
	InsertOne(ctx context.Context, offense models.RpPromoOffense, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) SingleResultHelper
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
}

type rpPromoOffenseDatabase struct {
	db DatabaseHelper
}

// NewRpPromoOffenseDatabase creates a new rp_promo_offenses database wrapper.
func NewRpPromoOffenseDatabase(db DatabaseHelper) RpPromoOffenseDatabase {
	return &rpPromoOffenseDatabase{db: db}
}

func (s *rpPromoOffenseDatabase) InsertOne(ctx context.Context, offense models.RpPromoOffense, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return s.db.Collection(rpPromoOffenseCollectionName).InsertOne(ctx, offense, opts...)
}

func (s *rpPromoOffenseDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) SingleResultHelper {
	return s.db.Collection(rpPromoOffenseCollectionName).FindOne(ctx, filter, opts...)
}

func (s *rpPromoOffenseDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return s.db.Collection(rpPromoOffenseCollectionName).Find(ctx, filter, opts...)
}

func (s *rpPromoOffenseDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return s.db.Collection(rpPromoOffenseCollectionName).CountDocuments(ctx, filter, opts...)
}

func (s *rpPromoOffenseDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := s.db.Collection(rpPromoOffenseCollectionName).UpdateOne(ctx, filter, update, opts...)
	return err
}
