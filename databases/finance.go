package databases

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const financeExpenseCollectionName = "finance_expenses"

// FinanceExpenseDatabase defines the interface for finance_expenses operations.
type FinanceExpenseDatabase interface {
	InsertOne(ctx context.Context, expense models.FinanceExpense, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) SingleResultHelper
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type financeExpenseDatabase struct {
	db DatabaseHelper
}

// NewFinanceExpenseDatabase creates a new finance_expenses database wrapper.
func NewFinanceExpenseDatabase(db DatabaseHelper) FinanceExpenseDatabase {
	return &financeExpenseDatabase{db: db}
}

func (f *financeExpenseDatabase) InsertOne(ctx context.Context, expense models.FinanceExpense, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return f.db.Collection(financeExpenseCollectionName).InsertOne(ctx, expense, opts...)
}

func (f *financeExpenseDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) SingleResultHelper {
	return f.db.Collection(financeExpenseCollectionName).FindOne(ctx, filter, opts...)
}

func (f *financeExpenseDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return f.db.Collection(financeExpenseCollectionName).Find(ctx, filter, opts...)
}

func (f *financeExpenseDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return f.db.Collection(financeExpenseCollectionName).UpdateOne(ctx, filter, update, opts...)
}

func (f *financeExpenseDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return f.db.Collection(financeExpenseCollectionName).DeleteOne(ctx, filter, opts...)
}

func (f *financeExpenseDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return f.db.Collection(financeExpenseCollectionName).CountDocuments(ctx, filter, opts...)
}
