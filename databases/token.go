package databases

// go generate: mockery --name TokenDatabase

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const tokenName = "tokens"

// TokenDatabase contains the methods to use with the token database
type TokenDatabase interface {
	FindOne(context.Context, interface{}, ...*options.FindOneOptions) (*models.Token, error)
	Find(context.Context, interface{}, ...*options.FindOptions) ([]models.Token, error)
	InsertOne(context.Context, models.Token) (InsertOneResultHelper, error)
	DeleteOne(context.Context, interface{}, ...*options.DeleteOptions) error
}

type tokenDatabase struct {
	db DatabaseHelper
}

// NewTokenDatabase initializes a new instance of user database with the provided db connection
func NewTokenDatabase(db DatabaseHelper) TokenDatabase {
	return &tokenDatabase{
		db: db,
	}
}

func (t *tokenDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.Token, error) {
	token := &models.Token{}
	err := t.db.Collection(tokenName).FindOne(ctx, filter, opts...).Decode(&token)
	if err != nil {
		return nil, err
	}
	return token, nil
}

func (t *tokenDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) ([]models.Token, error) {
	var token []models.Token
	cur, err := t.db.Collection(tokenName).Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	err = cur.Decode(&token)
	if err != nil {
		return nil, err
	}
	return token, nil
}

func (t *tokenDatabase) InsertOne(ctx context.Context, tokenDetails models.Token) (InsertOneResultHelper, error) {
	type spot struct {
		Token models.Token `bson:"token"`
	}
	spots := spot{Token: tokenDetails}
	res, err := t.db.Collection(tokenName).InsertOne(ctx, spots)
	return res, err
}

func (t *tokenDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return t.db.Collection(tokenName).DeleteOne(ctx, filter)
}
