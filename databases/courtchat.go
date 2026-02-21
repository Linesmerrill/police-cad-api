package databases

// go generate: mockery --name CourtChatDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const courtChatName = "courtchat"

// CourtChatDatabase contains the methods to use with the court chat database
type CourtChatDatabase interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.CourtChatMessage, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.CourtChatMessage, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
}

type courtChatDatabase struct {
	db DatabaseHelper
}

// NewCourtChatDatabase initializes a new instance of court chat database with the provided db connection
func NewCourtChatDatabase(db DatabaseHelper) CourtChatDatabase {
	return &courtChatDatabase{
		db: db,
	}
}

func (c *courtChatDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.CourtChatMessage, error) {
	msg := &models.CourtChatMessage{}
	err := c.db.Collection(courtChatName).FindOne(ctx, filter).Decode(&msg)
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func (c *courtChatDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.CourtChatMessage, error) {
	var messages []models.CourtChatMessage
	curr, err := c.db.Collection(courtChatName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer curr.Close(ctx)
	err = curr.All(ctx, &messages)
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func (c *courtChatDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(courtChatName).CountDocuments(ctx, filter, opts...)
}

func (c *courtChatDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(courtChatName).InsertOne(ctx, document, opts...)
	return res, err
}

func (c *courtChatDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(courtChatName).DeleteOne(ctx, filter, opts...)
}
