package databases

// go generate: mockery --name CourtSessionDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const courtSessionName = "courtsessions"

// CourtSessionDatabase contains the methods to use with the court session database
type CourtSessionDatabase interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.CourtSession, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.CourtSession, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
}

type courtSessionDatabase struct {
	db DatabaseHelper
}

// NewCourtSessionDatabase initializes a new instance of court session database with the provided db connection
func NewCourtSessionDatabase(db DatabaseHelper) CourtSessionDatabase {
	return &courtSessionDatabase{
		db: db,
	}
}

func (c *courtSessionDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.CourtSession, error) {
	courtSession := &models.CourtSession{}
	err := c.db.Collection(courtSessionName).FindOne(ctx, filter).Decode(&courtSession)
	if err != nil {
		return nil, err
	}
	return courtSession, nil
}

func (c *courtSessionDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.CourtSession, error) {
	var courtSessions []models.CourtSession
	curr, err := c.db.Collection(courtSessionName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer curr.Close(ctx)
	err = curr.All(ctx, &courtSessions)
	if err != nil {
		return nil, err
	}
	return courtSessions, nil
}

func (c *courtSessionDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(courtSessionName).CountDocuments(ctx, filter, opts...)
}

func (c *courtSessionDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(courtSessionName).InsertOne(ctx, document, opts...)
	return res, err
}

func (c *courtSessionDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(courtSessionName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *courtSessionDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(courtSessionName).DeleteOne(ctx, filter, opts...)
}
