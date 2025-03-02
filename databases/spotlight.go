package databases

// go generate: mockery --name SpotlightDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const spotlightName = "spotlight"

// SpotlightDatabase contains the methods to use with the spotlight database
type SpotlightDatabase interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.Spotlight, error)
	Find(context.Context, interface{}, ...*options.FindOptions) ([]models.Spotlight, error)
	InsertOne(context.Context, models.SpotlightDetails) interface{}
}

type spotlightDatabase struct {
	db DatabaseHelper
}

// NewSpotlightDatabase initializes a new instance of user database with the provided db connection
func NewSpotlightDatabase(db DatabaseHelper) SpotlightDatabase {
	return &spotlightDatabase{
		db: db,
	}
}

func (s *spotlightDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.Spotlight, error) {
	spotlight := &models.Spotlight{}
	err := s.db.Collection(spotlightName).FindOne(ctx, filter, opts...).Decode(&spotlight)
	if err != nil {
		return nil, err
	}
	return spotlight, nil
}

func (s *spotlightDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.Spotlight, error) {
	var spotlight []models.Spotlight
	cur, err := s.db.Collection(spotlightName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	err = cur.Decode(&spotlight)
	if err != nil {
		return nil, err
	}
	return spotlight, nil
}

func (s *spotlightDatabase) InsertOne(ctx context.Context, spotlightDetails models.SpotlightDetails) interface{} {
	type spot struct {
		Spotlight models.SpotlightDetails `bson:"spotlight"`
	}
	spots := spot{Spotlight: spotlightDetails}
	res := s.db.Collection(spotlightName).InsertOne(ctx, spots)
	return res
}
