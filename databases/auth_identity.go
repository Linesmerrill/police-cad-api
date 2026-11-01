package databases

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/models"
)

// AuthIdentityDatabase maps external sign-ins to LPC users.
//
// The collection carries a unique index on (provider, subject), which is what
// makes "one provider account signs into one LPC account" a property of the
// data rather than of the code that happens to write it.
type AuthIdentityDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.AuthIdentity, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	InsertOne(ctx context.Context, identity models.AuthIdentity) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	DeleteOne(ctx context.Context, filter interface{}) error
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type authIdentityDatabase struct{ db DatabaseHelper }

// NewAuthIdentityDatabase creates an auth identity database wrapper.
func NewAuthIdentityDatabase(db DatabaseHelper) AuthIdentityDatabase {
	return &authIdentityDatabase{db: db}
}

func (a *authIdentityDatabase) FindOne(ctx context.Context, filter interface{}) (*models.AuthIdentity, error) {
	identity := &models.AuthIdentity{}
	if err := a.db.Collection(authIdentityCollectionName).FindOne(ctx, filter).Decode(identity); err != nil {
		return nil, err
	}
	return identity, nil
}

func (a *authIdentityDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return a.db.Collection(authIdentityCollectionName).Find(ctx, filter, opts...)
}

func (a *authIdentityDatabase) InsertOne(ctx context.Context, identity models.AuthIdentity) (InsertOneResultHelper, error) {
	return a.db.Collection(authIdentityCollectionName).InsertOne(ctx, identity)
}

func (a *authIdentityDatabase) UpdateOne(ctx context.Context, filter, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return a.db.Collection(authIdentityCollectionName).UpdateOne(ctx, filter, update, opts...)
}

func (a *authIdentityDatabase) DeleteOne(ctx context.Context, filter interface{}) error {
	return a.db.Collection(authIdentityCollectionName).DeleteOne(ctx, filter)
}

func (a *authIdentityDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return a.db.Collection(authIdentityCollectionName).CountDocuments(ctx, filter, opts...)
}
