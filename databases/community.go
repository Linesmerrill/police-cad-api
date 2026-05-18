package databases

// go generate: mockery --name CommunityDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const collectionName = "communities"

// excludePending wraps a filter with a clause that excludes communities marked
// pending deletion. A community is "pending" iff community.pendingDeletionAt is
// set; we match on null OR missing so legacy docs (which lack the field) are
// treated as active.
func excludePending(filter interface{}) interface{} {
	notPending := bson.M{"community.pendingDeletionAt": nil}
	if filter == nil {
		return notPending
	}
	return bson.M{"$and": bson.A{filter, notPending}}
}

// CommunityDatabase contains the methods to use with the community database.
//
// FindOne / Find / CountDocuments transparently exclude communities marked
// pending deletion (community.pendingDeletionAt set). Use the *IncludingPending
// variants from contexts that explicitly need to see pending-deletion
// communities — the admin console, the scheduler hard-delete sweep, and the
// detail endpoint that returns 410 for a direct link to a pending community.
type CommunityDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.Community, error)
	FindOneIncludingPending(ctx context.Context, filter interface{}) (*models.Community, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	FindIncludingPending(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	InsertOne(ctx context.Context, community models.Community, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
	Aggregate(ctx context.Context, pipeline mongo.Pipeline, opts ...*options.AggregateOptions) (*MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	CountDocumentsIncludingPending(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type communityDatabase struct {
	db DatabaseHelper
}

// NewCommunityDatabase initializes a new instance of community database with the provided db connection
func NewCommunityDatabase(db DatabaseHelper) CommunityDatabase {
	return &communityDatabase{
		db: db,
	}
}

func (c *communityDatabase) FindOne(ctx context.Context, filter interface{}) (*models.Community, error) {
	return c.FindOneIncludingPending(ctx, excludePending(filter))
}

func (c *communityDatabase) FindOneIncludingPending(ctx context.Context, filter interface{}) (*models.Community, error) {
	community := &models.Community{}
	err := c.db.Collection(collectionName).FindOne(ctx, filter).Decode(&community)
	if err != nil {
		return nil, err
	}
	return community, nil
}

func (c *communityDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	return c.FindIncludingPending(ctx, excludePending(filter), opts...)
}

func (c *communityDatabase) FindIncludingPending(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := c.db.Collection(collectionName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}

func (c *communityDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(collectionName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *communityDatabase) InsertOne(ctx context.Context, community models.Community, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(collectionName).InsertOne(ctx, community, opts...)
	return res, err
}

func (c *communityDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(collectionName).DeleteOne(ctx, filter, opts...)

}

func (c *communityDatabase) Aggregate(ctx context.Context, pipeline mongo.Pipeline, opts ...*options.AggregateOptions) (*MongoCursor, error) {
	return c.db.Collection(collectionName).Aggregate(ctx, pipeline, opts...)
}

func (c *communityDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.CountDocumentsIncludingPending(ctx, excludePending(filter), opts...)
}

func (c *communityDatabase) CountDocumentsIncludingPending(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(collectionName).CountDocuments(ctx, filter, opts...)
}
