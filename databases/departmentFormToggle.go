package databases

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const departmentFormToggleName = "departmentFormToggles"

// DepartmentFormToggleDatabase contains the methods to use with the departmentFormToggles collection
type DepartmentFormToggleDatabase interface {
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.DepartmentFormToggle, error)
	UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error
}

type departmentFormToggleDatabase struct {
	db DatabaseHelper
}

// NewDepartmentFormToggleDatabase initializes a new instance of the departmentFormToggles database
func NewDepartmentFormToggleDatabase(db DatabaseHelper) DepartmentFormToggleDatabase {
	return &departmentFormToggleDatabase{db: db}
}

func (c *departmentFormToggleDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.DepartmentFormToggle, error) {
	var out []models.DepartmentFormToggle
	curr, err := c.db.Collection(departmentFormToggleName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer curr.Close(ctx)
	if err := curr.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *departmentFormToggleDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(departmentFormToggleName).UpdateOne(ctx, filter, update, opts...)
	return err
}
