package databases

// go generate: mockery --name UserDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
)

const userName = "users"

// UserDatabase contains the methods to use with the user database
type UserDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.User, error)
	Find(ctx context.Context, filter interface{}) ([]models.User, error)
	InsertOne(ctx context.Context, userDetails models.UserDetails) interface{}
	Aggregate(ctx context.Context, pipeline interface{}) (MongoCursor, error)
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

func (u *userDatabase) Find(ctx context.Context, filter interface{}) ([]models.User, error) {
	var users []models.User
	err := u.db.Collection(userName).Find(ctx, filter).Decode(&users)
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
