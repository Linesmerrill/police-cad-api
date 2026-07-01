package databases

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const changelogPostName = "changelogPosts"

// ChangelogPostDatabase contains the methods to use with the changelogPosts collection.
type ChangelogPostDatabase interface {
	InsertOne(ctx context.Context, post models.ChangelogPost) (InsertOneResultHelper, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
}

type changelogPostDatabase struct {
	db DatabaseHelper
}

// NewChangelogPostDatabase wires up the collection.
func NewChangelogPostDatabase(db DatabaseHelper) ChangelogPostDatabase {
	return &changelogPostDatabase{db: db}
}

func (c *changelogPostDatabase) InsertOne(ctx context.Context, post models.ChangelogPost) (InsertOneResultHelper, error) {
	return c.db.Collection(changelogPostName).InsertOne(ctx, post)
}

func (c *changelogPostDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return c.db.Collection(changelogPostName).Find(ctx, filter, opts...)
}

func (c *changelogPostDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(changelogPostName).CountDocuments(ctx, filter, opts...)
}

func (c *changelogPostDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(changelogPostName).UpdateOne(ctx, filter, update, opts...)
	return err
}
