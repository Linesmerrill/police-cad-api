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

func (c *spotlightDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.Spotlight, error) {
	spotlight := &models.Spotlight{}
	err := c.db.Collection(spotlightName).FindOne(ctx, filter, opts...).Decode(&spotlight)
	if err != nil {
		return nil, err
	}
	return spotlight, nil
}

func (c *spotlightDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.Spotlight, error) {
	var spotlight []models.Spotlight
	err := c.db.Collection(spotlightName).Find(ctx, filter, opts...).Decode(&spotlight)
	if err != nil {
		return nil, err
	}
	return spotlight, nil
}
