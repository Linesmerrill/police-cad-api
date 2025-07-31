package databases

// go generate: mockery --name AnnouncementDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const announcementCollectionName = "announcements"

// AnnouncementDatabase contains the methods to use with the announcement database
type AnnouncementDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.Announcement, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	InsertOne(ctx context.Context, announcement models.Announcement, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
	Aggregate(ctx context.Context, pipeline mongo.Pipeline, opts ...*options.AggregateOptions) (*MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	FindOneAndUpdate(ctx context.Context, filter interface{}, update interface{}, opts ...*options.FindOneAndUpdateOptions) (*models.Announcement, error)
}

type announcementDatabase struct {
	db DatabaseHelper
}

// NewAnnouncementDatabase initializes a new instance of announcement database with the provided db connection
func NewAnnouncementDatabase(db DatabaseHelper) AnnouncementDatabase {
	return &announcementDatabase{
		db: db,
	}
}

func (a *announcementDatabase) FindOne(ctx context.Context, filter interface{}) (*models.Announcement, error) {
	announcement := &models.Announcement{}
	err := a.db.Collection(announcementCollectionName).FindOne(ctx, filter).Decode(&announcement)
	if err != nil {
		return nil, err
	}
	return announcement, nil
}

func (a *announcementDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := a.db.Collection(announcementCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}

func (a *announcementDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := a.db.Collection(announcementCollectionName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (a *announcementDatabase) InsertOne(ctx context.Context, announcement models.Announcement, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := a.db.Collection(announcementCollectionName).InsertOne(ctx, announcement, opts...)
	return res, err
}

func (a *announcementDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return a.db.Collection(announcementCollectionName).DeleteOne(ctx, filter, opts...)
}

func (a *announcementDatabase) Aggregate(ctx context.Context, pipeline mongo.Pipeline, opts ...*options.AggregateOptions) (*MongoCursor, error) {
	return a.db.Collection(announcementCollectionName).Aggregate(ctx, pipeline, opts...)
}

func (a *announcementDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return a.db.Collection(announcementCollectionName).CountDocuments(ctx, filter, opts...)
}

func (a *announcementDatabase) FindOneAndUpdate(ctx context.Context, filter interface{}, update interface{}, opts ...*options.FindOneAndUpdateOptions) (*models.Announcement, error) {
	announcement := &models.Announcement{}
	err := a.db.Collection(announcementCollectionName).FindOneAndUpdate(ctx, filter, update, opts...).Decode(&announcement)
	if err != nil {
		return nil, err
	}
	return announcement, nil
} 