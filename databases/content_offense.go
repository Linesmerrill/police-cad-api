package databases

// go generate: mockery --name ContentOffenseDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const contentOffenseName = "content_offenses"

// ContentOffenseDatabase contains the methods to use with the content_offenses
// collection, which records moderation actions taken after a user report was
// upheld.
type ContentOffenseDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.ContentOffense, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	InsertOne(ctx context.Context, offense models.ContentOffense, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
}

type contentOffenseDatabase struct {
	db DatabaseHelper
}

// NewContentOffenseDatabase initializes a new instance of the content_offenses
// database with the provided db connection.
func NewContentOffenseDatabase(db DatabaseHelper) ContentOffenseDatabase {
	return &contentOffenseDatabase{db: db}
}

func (c *contentOffenseDatabase) FindOne(ctx context.Context, filter interface{}) (*models.ContentOffense, error) {
	offense := &models.ContentOffense{}
	if err := c.db.Collection(contentOffenseName).FindOne(ctx, filter).Decode(offense); err != nil {
		return nil, err
	}
	return offense, nil
}

func (c *contentOffenseDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := c.db.Collection(contentOffenseName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, nil
}

func (c *contentOffenseDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(contentOffenseName).CountDocuments(ctx, filter, opts...)
}

func (c *contentOffenseDatabase) InsertOne(ctx context.Context, offense models.ContentOffense, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return c.db.Collection(contentOffenseName).InsertOne(ctx, offense, opts...)
}

func (c *contentOffenseDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(contentOffenseName).UpdateOne(ctx, filter, update, opts...)
	return err
}
