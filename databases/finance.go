package databases

import (
	"context"
	"fmt"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	// bankTransactionCollectionName has a unique index on transaction_id
	// (created at startup by EnsureFinanceIndexes).
	bankTransactionCollectionName = "bank_transactions"
	// plaidStateCollectionName holds a single sync-state document.
	plaidStateCollectionName = "finance_plaid_state"
)

// BankTransactionDatabase defines the interface for bank_transactions
// operations.
type BankTransactionDatabase interface {
	InsertOne(ctx context.Context, tx models.BankTransaction, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
	DeleteMany(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) (int64, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	EnsureUniqueTransactionIDIndex(ctx context.Context) error
}

type bankTransactionDatabase struct {
	db DatabaseHelper
}

// NewBankTransactionDatabase creates a new bank_transactions database wrapper.
func NewBankTransactionDatabase(db DatabaseHelper) BankTransactionDatabase {
	return &bankTransactionDatabase{db: db}
}

func (b *bankTransactionDatabase) InsertOne(ctx context.Context, tx models.BankTransaction, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return b.db.Collection(bankTransactionCollectionName).InsertOne(ctx, tx, opts...)
}

func (b *bankTransactionDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return b.db.Collection(bankTransactionCollectionName).UpdateOne(ctx, filter, update, opts...)
}

func (b *bankTransactionDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return b.db.Collection(bankTransactionCollectionName).DeleteOne(ctx, filter, opts...)
}

func (b *bankTransactionDatabase) DeleteMany(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) (int64, error) {
	return b.db.Collection(bankTransactionCollectionName).DeleteMany(ctx, filter, opts...)
}

func (b *bankTransactionDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return b.db.Collection(bankTransactionCollectionName).Find(ctx, filter, opts...)
}

func (b *bankTransactionDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return b.db.Collection(bankTransactionCollectionName).CountDocuments(ctx, filter, opts...)
}

// EnsureUniqueTransactionIDIndex creates the unique index on transaction_id
// so upserts keyed by Plaid transaction ID never create duplicates. Safe to
// call repeatedly: Mongo treats an identical IndexModel as a no-op.
func (b *bankTransactionDatabase) EnsureUniqueTransactionIDIndex(ctx context.Context) error {
	coll, ok := b.db.Collection(bankTransactionCollectionName).(interface {
		Indexes() mongo.IndexView
	})
	if !ok {
		return fmt.Errorf("collection helper does not expose Indexes()")
	}
	_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "transaction_id", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("transaction_id_unique"),
	})
	return err
}

// PlaidStateDatabase defines the interface for finance_plaid_state
// operations (a single document holding the Plaid sync cursor).
type PlaidStateDatabase interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) SingleResultHelper
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
}

type plaidStateDatabase struct {
	db DatabaseHelper
}

// NewPlaidStateDatabase creates a new finance_plaid_state database wrapper.
func NewPlaidStateDatabase(db DatabaseHelper) PlaidStateDatabase {
	return &plaidStateDatabase{db: db}
}

func (p *plaidStateDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) SingleResultHelper {
	return p.db.Collection(plaidStateCollectionName).FindOne(ctx, filter, opts...)
}

func (p *plaidStateDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return p.db.Collection(plaidStateCollectionName).UpdateOne(ctx, filter, update, opts...)
}
