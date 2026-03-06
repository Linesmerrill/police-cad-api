package databases

import (
	"context"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const auditLogCollectionName = "audit_logs"

// AuditLogDatabase defines the interface for audit log operations.
type AuditLogDatabase interface {
	InsertOne(ctx context.Context, log models.AuditLog, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
}

type auditLogDatabase struct {
	db DatabaseHelper
}

// NewAuditLogDatabase creates a new audit log database wrapper.
func NewAuditLogDatabase(db DatabaseHelper) AuditLogDatabase {
	return &auditLogDatabase{db: db}
}

func (a *auditLogDatabase) InsertOne(ctx context.Context, log models.AuditLog, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return a.db.Collection(auditLogCollectionName).InsertOne(ctx, log, opts...)
}

func (a *auditLogDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return a.db.Collection(auditLogCollectionName).Find(ctx, filter, opts...)
}

func (a *auditLogDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return a.db.Collection(auditLogCollectionName).CountDocuments(ctx, filter, opts...)
}
