package databases

// go generate: mockery --name ArrestReportDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const arrestReportName = "arrestReports"

// ArrestReportDatabase contains the methods to use with the arrestReport database
type ArrestReportDatabase interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.ArrestReport, error)
	Find(context.Context, interface{}, ...*options.FindOptions) ([]models.ArrestReport, error)
	CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error)
	InsertOne(ctx context.Context, arrestReport models.ArrestReport, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
}

type arrestReportDatabase struct {
	db DatabaseHelper
}

// NewArrestReportDatabase initializes a new instance of user database with the provided db connection
func NewArrestReportDatabase(db DatabaseHelper) ArrestReportDatabase {
	return &arrestReportDatabase{
		db: db,
	}
}

func (c *arrestReportDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.ArrestReport, error) {
	arrestReport := &models.ArrestReport{}
	err := c.db.Collection(arrestReportName).FindOne(ctx, filter, opts...).Decode(&arrestReport)
	if err != nil {
		return nil, err
	}
	return arrestReport, nil
}

func (c *arrestReportDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.ArrestReport, error) {
	var arrestReports []models.ArrestReport
	cr, err := c.db.Collection(arrestReportName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	err = cr.Decode(&arrestReports)
	if err != nil {
		return nil, err
	}
	return arrestReports, nil
}

func (c *arrestReportDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	count, err := c.db.Collection(arrestReportName).CountDocuments(ctx, filter, opts...)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (c *arrestReportDatabase) InsertOne(ctx context.Context, arrestReport models.ArrestReport, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(arrestReportName).InsertOne(ctx, arrestReport, opts...)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (c *arrestReportDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(arrestReportName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *arrestReportDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	err := c.db.Collection(arrestReportName).DeleteOne(ctx, filter, opts...)
	if err != nil {
		return err
	}
	return nil
}
