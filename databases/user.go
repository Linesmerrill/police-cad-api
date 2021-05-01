package databases

//go generate: mockery --name UserDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
)

const userName = "users"

// UserDatabase contains the methods to use with the user database
type UserDatabase interface {
	FindOne(ctx context.Context, filter interface{}) (*models.User, error)
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

func (c *userDatabase) FindOne(ctx context.Context, filter interface{}) (*models.User, error) {
	user := &models.User{}
	err := c.db.Collection(userName).FindOne(ctx, filter).Decode(&user)
	if err != nil {
		return nil, err
	}
	return user, nil
}
