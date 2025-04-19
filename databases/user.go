package databases

// go generate: mockery --name UserDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const userName = "users"

// UserDatabase contains the methods to use with the user database
type UserDatabase interface {
	FindOne(ctx context.Context, filter interface{}) SingleResultHelper
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	InsertOne(ctx context.Context, userDetails models.UserDetails) (InsertOneResultHelper, error)
	Aggregate(ctx context.Context, pipeline interface{}) (MongoCursor, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	UpdateMany(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
}

type userDatabase struct {
	db DatabaseHelper
}

// NewUserDatabase initializes a new instance of user database with the provided db connection
func NewUserDatabase(db DatabaseHelper) UserDatabase {
	return &userDatabase{
		db: db,
	}
}

func (u *userDatabase) FindOne(ctx context.Context, filter interface{}) SingleResultHelper {
	// user := &models.User{}
	return u.db.Collection(userName).FindOne(ctx, filter)
	// if err != nil {
	// 	return nil, err
	// }
	// return user, nil
}

func (u *userDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := u.db.Collection(userName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}

func (u *userDatabase) InsertOne(ctx context.Context, userDetails models.UserDetails) (InsertOneResultHelper, error) {
	type user struct {
		User models.UserDetails `bson:"user"`
	}
	users := user{User: userDetails}
	res, err := u.db.Collection(userName).InsertOne(ctx, users)
	return res, err
}

func (u *userDatabase) Aggregate(ctx context.Context, pipeline interface{}) (MongoCursor, error) {
	cursor, err := u.db.Collection(userName).Aggregate(ctx, pipeline)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, nil
}

func (u *userDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	res, err := u.db.Collection(userName).UpdateOne(ctx, filter, update, opts...)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (u *userDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	count, err := u.db.Collection(userName).CountDocuments(ctx, filter, opts...)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (u *userDatabase) UpdateMany(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	res, err := u.db.Collection(userName).UpdateMany(ctx, filter, update, opts...)
	if err != nil {
		return nil, err
	}
	return res, nil
}
