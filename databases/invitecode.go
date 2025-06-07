package databases

// go generate: mockery --name InviteCodeDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const inviteCodeName = "inviteCodes"

// InviteCodeDatabase contains the methods to use with the inviteCode database
type InviteCodeDatabase interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.InviteCode, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.InviteCode, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	InsertOne(ctx context.Context, inviteCode models.InviteCode, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
}

type inviteCodeDatabase struct {
	db DatabaseHelper
}

// NewInviteCodeDatabase initializes a new instance of inviteCode database with the provided db connection
func NewInviteCodeDatabase(db DatabaseHelper) InviteCodeDatabase {
	return &inviteCodeDatabase{
		db: db,
	}
}

func (c *inviteCodeDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.InviteCode, error) {
	inviteCode := &models.InviteCode{}
	err := c.db.Collection(inviteCodeName).FindOne(ctx, filter).Decode(&inviteCode)
	if err != nil {
		return nil, err
	}
	return inviteCode, nil
}

func (c *inviteCodeDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.InviteCode, error) {
	var inviteCodes []models.InviteCode
	cur, err := c.db.Collection(inviteCodeName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	err = cur.Decode(&inviteCodes)
	if err != nil {
		return nil, err
	}
	return inviteCodes, nil
}

func (c *inviteCodeDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	count, err := c.db.Collection(inviteCodeName).CountDocuments(ctx, filter, opts...)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (c *inviteCodeDatabase) InsertOne(ctx context.Context, inviteCode models.InviteCode, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return c.db.Collection(inviteCodeName).InsertOne(ctx, inviteCode, opts...)
}

func (c *inviteCodeDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := c.db.Collection(inviteCodeName).UpdateOne(ctx, filter, update, opts...)
	return err
}

func (c *inviteCodeDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return c.db.Collection(inviteCodeName).DeleteOne(ctx, filter, opts...)
}
