package databases

// go generate: mockery --name ReportDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const reportName = "reports"

// ReportDatabase contains the methods to use with the report database
type ReportDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.Report, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	InsertOne(ctx context.Context, report models.Report, opts ...*options.InsertOneOptions) InsertOneResultHelper
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
}

type reportDatabase struct {
	db DatabaseHelper
}

// NewReportDatabase initializes a new instance of report database with the provided db connection
func NewReportDatabase(db DatabaseHelper) ReportDatabase {
	return &reportDatabase{
		db: db,
	}
}

func (c *reportDatabase) FindOne(ctx context.Context, filter interface{}) (*models.Report, error) {
	report := &models.Report{}
	err := c.db.Collection(reportName).FindOne(ctx, filter).Decode(&report)
	if err != nil {
		return nil, err
	}
	return report, nil
}

func (c *reportDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := c.db.Collection(reportName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}

func (c *reportDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(reportName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *reportDatabase) InsertOne(ctx context.Context, report models.Report, opts ...*options.InsertOneOptions) InsertOneResultHelper {
	res := c.db.Collection(reportName).InsertOne(ctx, report, opts...)
	return res
}

func (c *reportDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(reportName).DeleteOne(ctx, filter, opts...)

}
