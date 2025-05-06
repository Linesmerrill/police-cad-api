package databases

// go generate: mockery --name PendingVerificationDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const pendingVerificationName = "pendingVerifications"

// PendingVerificationDatabase contains the methods to use with the pendingVerification database
type PendingVerificationDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.PendingVerification, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	InsertOne(ctx context.Context, pendingVerification models.PendingVerification, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
}

type pendingVerificationDatabase struct {
	db DatabaseHelper
}

// NewPendingVerificationDatabase initializes a new instance of pendingVerification database with the provided db connection
func NewPendingVerificationDatabase(db DatabaseHelper) PendingVerificationDatabase {
	return &pendingVerificationDatabase{
		db: db,
	}
}

func (c *pendingVerificationDatabase) FindOne(ctx context.Context, filter interface{}) (*models.PendingVerification, error) {
	pendingVerification := &models.PendingVerification{}
	err := c.db.Collection(pendingVerificationName).FindOne(ctx, filter).Decode(&pendingVerification)
	if err != nil {
		return nil, err
	}
	return pendingVerification, nil
}

func (c *pendingVerificationDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := c.db.Collection(pendingVerificationName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}

func (c *pendingVerificationDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(pendingVerificationName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *pendingVerificationDatabase) InsertOne(ctx context.Context, pendingVerification models.PendingVerification, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	res, err := c.db.Collection(pendingVerificationName).InsertOne(ctx, pendingVerification, opts...)
	return res, err
}

func (c *pendingVerificationDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(pendingVerificationName).DeleteOne(ctx, filter, opts...)

}
