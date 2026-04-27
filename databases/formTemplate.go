package databases

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const formTemplateName = "formTemplates"

// FormTemplateDatabase contains the methods to use with the formTemplates collection
type FormTemplateDatabase interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.FormTemplate, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.FormTemplate, error)
	InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	DeleteOne(context.Context, interface{}, ...*options.DeleteOptions) error
	UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error
	CountDocuments(context.Context, interface{}, ...*options.CountOptions) (int64, error)
}

type formTemplateDatabase struct {
	db DatabaseHelper
}

// NewFormTemplateDatabase initializes a new instance of the formTemplates database
func NewFormTemplateDatabase(db DatabaseHelper) FormTemplateDatabase {
	return &formTemplateDatabase{db: db}
}

func (c *formTemplateDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.FormTemplate, error) {
	tpl := &models.FormTemplate{}
	if err := c.db.Collection(formTemplateName).FindOne(ctx, filter, opts...).Decode(&tpl); err != nil {
		return nil, err
	}
	return tpl, nil
}

func (c *formTemplateDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.FormTemplate, error) {
	var out []models.FormTemplate
	curr, err := c.db.Collection(formTemplateName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer curr.Close(ctx)
	if err := curr.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *formTemplateDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return c.db.Collection(formTemplateName).InsertOne(ctx, document, opts...)
}

func (c *formTemplateDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(formTemplateName).DeleteOne(ctx, filter, opts...)
}

func (c *formTemplateDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(formTemplateName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *formTemplateDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return c.db.Collection(formTemplateName).CountDocuments(ctx, filter, opts...)
}
