package databases

// go generate: mockery --name CivilianDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
)

const civilianName = "civilians"

// CivilianDatabase contains the methods to use with the civilian database
type CivilianDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.Civilian, error)
	Find(ctx context.Context, filter interface{}) ([]models.Civilian, error)
}

type civilianDatabase struct {
	db DatabaseHelper
}

// NewCivilianDatabase initializes a new instance of user database with the provided db connection
func NewCivilianDatabase(db DatabaseHelper) CivilianDatabase {
	return &civilianDatabase{
		db: db,
	}
}

func (c *civilianDatabase) FindOne(ctx context.Context, filter interface{}) (*models.Civilian, error) {
	civilian := &models.Civilian{}
	err := c.db.Collection(civilianName).FindOne(ctx, filter).Decode(&civilian)
	if err != nil {
		return nil, err
	}
	return civilian, nil
}

func (c *civilianDatabase) Find(ctx context.Context, filter interface{}) ([]models.Civilian, error) {
	var civilians []models.Civilian
	err := c.db.Collection(civilianName).Find(ctx, filter).Decode(&civilians)
	if err != nil {
		return nil, err
	}
	return civilians, nil
}
