package databases

// go generate: mockery --name AnnouncementReadDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const announcementReadCollectionName = "announcementreads"

// AnnouncementReadDatabase contains the methods to use with the announcement read database
type AnnouncementReadDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.AnnouncementRead, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	InsertOne(ctx context.Context, announcementRead models.AnnouncementRead, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type announcementReadDatabase struct {
	db DatabaseHelper
}

// NewAnnouncementReadDatabase initializes a new instance of announcement read database with the provided db connection
func NewAnnouncementReadDatabase(db DatabaseHelper) AnnouncementReadDatabase {
	return &announcementReadDatabase{
		db: db,
	}
}

func (a *announcementReadDatabase) FindOne(ctx context.Context, filter interface{}) (*models.AnnouncementRead, error) {
	announcementRead := &models.AnnouncementRead{}
	err := a.db.Collection(announcementReadCollectionName).FindOne(ctx, filter).Decode(announcementRead)
	if err != nil {
		return nil, err
	}
	return announcementRead, nil
}

func (a *announcementReadDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := a.db.Collection(announcementReadCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}

func (a *announcementReadDatabase) InsertOne(ctx context.Context, announcementRead models.AnnouncementRead, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := a.db.Collection(announcementReadCollectionName).InsertOne(ctx, announcementRead, opts...)
	return res, err
}

func (a *announcementReadDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return a.db.Collection(announcementReadCollectionName).CountDocuments(ctx, filter, opts...)
}
