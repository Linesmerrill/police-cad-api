package databases

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const formTemplateVersionName = "formTemplateVersions"

// FormTemplateVersionDatabase contains the methods to use with the formTemplateVersions collection
type FormTemplateVersionDatabase interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.FormTemplateVersion, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.FormTemplateVersion, error)
	InsertOne(context.Context, interface{}, ...*options.InsertOneOptions) (InsertOneResultHelper, error)
}

type formTemplateVersionDatabase struct {
	db DatabaseHelper
}

// NewFormTemplateVersionDatabase initializes a new instance of the formTemplateVersions database
func NewFormTemplateVersionDatabase(db DatabaseHelper) FormTemplateVersionDatabase {
	return &formTemplateVersionDatabase{db: db}
}

func (c *formTemplateVersionDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.FormTemplateVersion, error) {
	v := &models.FormTemplateVersion{}
	if err := c.db.Collection(formTemplateVersionName).FindOne(ctx, filter, opts...).Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func (c *formTemplateVersionDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.FormTemplateVersion, error) {
	var out []models.FormTemplateVersion
	curr, err := c.db.Collection(formTemplateVersionName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer curr.Close(ctx)
	if err := curr.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *formTemplateVersionDatabase) InsertOne(ctx context.Context, document interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return c.db.Collection(formTemplateVersionName).InsertOne(ctx, document, opts...)
}
