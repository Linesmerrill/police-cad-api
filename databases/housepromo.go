package databases

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo/options"
)

const housePromoCollectionName = "housePromos"

// HousePromoDatabase contains the methods to use with the housePromos collection.
type HousePromoDatabase interface {
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error)
}

type housePromoDatabase struct {
	db DatabaseHelper
}

// NewHousePromoDatabase wires up the collection.
func NewHousePromoDatabase(db DatabaseHelper) HousePromoDatabase {
	return &housePromoDatabase{db: db}
}

func (h *housePromoDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (MongoCursor, error) {
	cursor, err := h.db.Collection(housePromoCollectionName).Find(ctx, filter, opts...)
	if err != nil {
		return MongoCursor{}, err
	}
	return *cursor, err
}
