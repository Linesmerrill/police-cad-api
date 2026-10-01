package databases

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	// financeTagCollectionName holds the owner's transaction tags (Steam,
	// Google Ads, ...). Unique index on name_key (scripts/create_indexes.js).
	financeTagCollectionName = "finance_tags"
	// financeTagRuleCollectionName maps a merchant to a tag, so new
	// transactions from that merchant are tagged on sync. Unique index on
	// merchant_key.
	financeTagRuleCollectionName = "finance_tag_rules"
)

// FinanceDocDatabase is the small set of operations the finance tag and rule
// collections need. Both are owner-only and hold a handful of documents.
type FinanceDocDatabase interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) SingleResultHelper
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	InsertOne(ctx context.Context, doc interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
	DeleteMany(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) (int64, error)
}

type financeDocDatabase struct {
	db   DatabaseHelper
	name string
}

// NewFinanceTagDatabase wraps finance_tags.
func NewFinanceTagDatabase(db DatabaseHelper) FinanceDocDatabase {
	return &financeDocDatabase{db: db, name: financeTagCollectionName}
}

// NewFinanceTagRuleDatabase wraps finance_tag_rules.
func NewFinanceTagRuleDatabase(db DatabaseHelper) FinanceDocDatabase {
	return &financeDocDatabase{db: db, name: financeTagRuleCollectionName}
}

func (f *financeDocDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) SingleResultHelper {
	return f.db.Collection(f.name).FindOne(ctx, filter, opts...)
}

func (f *financeDocDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return f.db.Collection(f.name).Find(ctx, filter, opts...)
}

func (f *financeDocDatabase) InsertOne(ctx context.Context, doc interface{}, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return f.db.Collection(f.name).InsertOne(ctx, doc, opts...)
}

func (f *financeDocDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return f.db.Collection(f.name).UpdateOne(ctx, filter, update, opts...)
}

func (f *financeDocDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return f.db.Collection(f.name).DeleteOne(ctx, filter, opts...)
}

func (f *financeDocDatabase) DeleteMany(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) (int64, error) {
	return f.db.Collection(f.name).DeleteMany(ctx, filter, opts...)
}
