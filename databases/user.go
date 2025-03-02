package databases

// go generate: mockery --name UserDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const userName = "users"

// UserDatabase contains the methods to use with the user database
type UserDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.User, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.User, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	InsertOne(ctx context.Context, userDetails models.UserDetails) interface{}
	Aggregate(ctx context.Context, pipeline interface{}) (MongoCursor, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (interface{}, error)
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

func (u *userDatabase) FindOne(ctx context.Context, filter interface{}) (*models.User, error) {
	user := &models.User{}
	err := u.db.Collection(userName).FindOne(ctx, filter).Decode(&user)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (u *userDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.User, error) {
	var users []models.User
	cur, err := u.db.Collection(userName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	err = cur.Decode(&users)
	if err != nil {
		return nil, err
	}
	return users, nil
}

func (u *userDatabase) InsertOne(ctx context.Context, userDetails models.UserDetails) interface{} {
	type user struct {
		User models.UserDetails `bson:"user"`
	}
	users := user{User: userDetails}
	res := u.db.Collection(userName).InsertOne(ctx, users)
	return res
}

func (u *userDatabase) Aggregate(ctx context.Context, pipeline interface{}) (MongoCursor, error) {
	cursor, err := u.db.Collection(userName).Aggregate(ctx, pipeline)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, nil
}

func (u *userDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (interface{}, error) {
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
