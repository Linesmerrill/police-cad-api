package databases

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const formSubmissionName = "formSubmissions"

// FormSubmissionDatabase contains the methods to use with the formSubmissions collection
type FormSubmissionDatabase interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.FormSubmission, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.FormSubmission, error)
	InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	DeleteOne(context.Context, interface{}, ...*options.DeleteOptions) error
	UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error
	CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error)
}

type formSubmissionDatabase struct {
	db DatabaseHelper
}

// NewFormSubmissionDatabase initializes a new instance of the formSubmissions database
func NewFormSubmissionDatabase(db DatabaseHelper) FormSubmissionDatabase {
	return &formSubmissionDatabase{db: db}
}

func (c *formSubmissionDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.FormSubmission, error) {
	s := &models.FormSubmission{}
	if err := c.db.Collection(formSubmissionName).FindOne(ctx, filter, opts...).Decode(&s); err != nil {
		return nil, err
	}
	return s, nil
}

func (c *formSubmissionDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.FormSubmission, error) {
	var out []models.FormSubmission
	curr, err := c.db.Collection(formSubmissionName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer curr.Close(ctx)
	if err := curr.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *formSubmissionDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return c.db.Collection(formSubmissionName).InsertOne(ctx, document, opts...)
}

func (c *formSubmissionDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(formSubmissionName).DeleteOne(ctx, filter, opts...)
}

func (c *formSubmissionDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(formSubmissionName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *formSubmissionDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(formSubmissionName).CountDocuments(ctx, filter, opts...)
}
