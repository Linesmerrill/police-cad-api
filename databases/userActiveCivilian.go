package databases

// go generate: mockery --name UserActiveCivilianDatabase

import (
	"context"
	"fmt"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Shared with the Discord bot (which historically wrote to
// "bot_active_civilians" directly). The bot will migrate to call the new
// /api/v2/user/active-civilian endpoint, at which point this is the single
// source of truth.
const userActiveCivilianCollectionName = "user_active_civilians"

// UserActiveCivilianDatabase contains the methods to use with the
// user_active_civilians collection.
type UserActiveCivilianDatabase interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.UserActiveCivilian, error)
	UpdateOne(context.Context, interface{}, interface{}, ...*options.UpdateOptions) error
	DeleteMany(context.Context, interface{}, ...*options.DeleteOptions) (int64, error)
	EnsureIndexes(context.Context) error
}

type userActiveCivilianDatabase struct {
	db DatabaseHelper
}

// NewUserActiveCivilianDatabase initializes the user_active_civilians database accessor.
func NewUserActiveCivilianDatabase(db DatabaseHelper) UserActiveCivilianDatabase {
	return &userActiveCivilianDatabase{db: db}
}

func (u *userActiveCivilianDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.UserActiveCivilian, error) {
	out := &models.UserActiveCivilian{}
	if err := u.db.Collection(userActiveCivilianCollectionName).FindOne(ctx, filter, opts...).Decode(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (u *userActiveCivilianDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) error {
	_, err := u.db.Collection(userActiveCivilianCollectionName).UpdateOne(ctx, filter, update, opts...)
	return err
}

// DeleteMany removes every active-civilian row matching filter. Used to
// cascade a civilian delete so we don't leave dangling pointers behind.
func (u *userActiveCivilianDatabase) DeleteMany(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) (int64, error) {
	return u.db.Collection(userActiveCivilianCollectionName).DeleteMany(ctx, filter, opts...)
}

// EnsureIndexes creates the unique (userId, communityId) index used for the
// upsert. Idempotent — Mongo treats an identical IndexModel as a no-op.
func (u *userActiveCivilianDatabase) EnsureIndexes(ctx context.Context) error {
	coll, ok := u.db.Collection(userActiveCivilianCollectionName).(interface {
		Indexes() mongo.IndexView
	})
	if !ok {
		return fmt.Errorf("collection helper does not expose Indexes()")
	}
	_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "userId", Value: 1}, {Key: "communityId", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("uniq_user_community"),
	})
	return err
}
