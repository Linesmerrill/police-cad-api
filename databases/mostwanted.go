package databases

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const mostWantedName = "most_wanted_entries"

// MostWantedDatabase contains the methods to use with the most wanted database
type MostWantedDatabase interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.MostWantedEntry, error)
	Find(context.Context, interface{}, ...*options.FindOptions) ([]models.MostWantedEntry, error)
	CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error)
	InsertOne(ctx context.Context, entry models.MostWantedEntry, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
}

type mostWantedDatabase struct {
	db DatabaseHelper
}

// NewMostWantedDatabase initializes a new instance of most wanted database with the provided db connection
func NewMostWantedDatabase(db DatabaseHelper) MostWantedDatabase {
	return &mostWantedDatabase{
		db: db,
	}
}

func (c *mostWantedDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.MostWantedEntry, error) {
	entry := &models.MostWantedEntry{}
	err := c.db.Collection(mostWantedName).FindOne(ctx, filter, opts...).Decode(&entry)
	if err != nil {
		return nil, err
	}
	return entry, nil
}

func (c *mostWantedDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.MostWantedEntry, error) {
	var entries []models.MostWantedEntry
	cr, err := c.db.Collection(mostWantedName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	err = cr.Decode(&entries)
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (c *mostWantedDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	count, err := c.db.Collection(mostWantedName).CountDocuments(ctx, filter, opts...)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (c *mostWantedDatabase) InsertOne(ctx context.Context, entry models.MostWantedEntry, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(mostWantedName).InsertOne(ctx, entry, opts...)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (c *mostWantedDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(mostWantedName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *mostWantedDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	err := c.db.Collection(mostWantedName).DeleteOne(ctx, filter, opts...)
	if err != nil {
		return err
	}
	return nil
}
