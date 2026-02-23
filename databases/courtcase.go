package databases

// go generate: mockery --name CourtCaseDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const courtCaseName = "courtcases"

// CourtCaseDatabase contains the methods to use with the court case database
type CourtCaseDatabase interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.CourtCase, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.CourtCase, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
}

type courtCaseDatabase struct {
	db DatabaseHelper
}

// NewCourtCaseDatabase initializes a new instance of court case database with the provided db connection
func NewCourtCaseDatabase(db DatabaseHelper) CourtCaseDatabase {
	return &courtCaseDatabase{
		db: db,
	}
}

func (c *courtCaseDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.CourtCase, error) {
	courtCase := &models.CourtCase{}
	err := c.db.Collection(courtCaseName).FindOne(ctx, filter).Decode(&courtCase)
	if err != nil {
		return nil, err
	}
	return courtCase, nil
}

func (c *courtCaseDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.CourtCase, error) {
	var courtCases []models.CourtCase
	curr, err := c.db.Collection(courtCaseName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer curr.Close(ctx)
	err = curr.All(ctx, &courtCases)
	if err != nil {
		return nil, err
	}
	return courtCases, nil
}

func (c *courtCaseDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(courtCaseName).CountDocuments(ctx, filter, opts...)
}

func (c *courtCaseDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(courtCaseName).InsertOne(ctx, document, opts...)
	return res, err
}

func (c *courtCaseDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(courtCaseName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *courtCaseDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(courtCaseName).DeleteOne(ctx, filter, opts...)
}
