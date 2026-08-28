package databases

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo/options"
)

const countdownCollectionName = "countdowns"

// CountdownDatabase contains the methods to use with the countdowns collection.
type CountdownDatabase interface {
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
}

type countdownDatabase struct {
	db DatabaseHelper
}

// NewCountdownDatabase wires up the collection.
func NewCountdownDatabase(db DatabaseHelper) CountdownDatabase {
	return &countdownDatabase{db: db}
}

func (c *countdownDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := c.db.Collection(countdownCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}
