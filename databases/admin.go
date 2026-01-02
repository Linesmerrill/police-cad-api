package databases

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

const adminCollectionName = "admin_users"

// AdminDatabase defines the interface for admin user operations
type AdminDatabase interface {
	FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.AdminUser, error)
	InsertOne(ctx context.Context, admin models.AdminUser, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error
}

type adminDatabase struct {
	db DatabaseHelper
}

// NewAdminDatabase creates a new admin database wrapper
func NewAdminDatabase(db DatabaseHelper) AdminDatabase {
	return &adminDatabase{db: db}
}

func (a *adminDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.AdminUser, error) {
	admin := &models.AdminUser{}
	err := a.db.Collection(adminCollectionName).FindOne(ctx, filter, opts...).Decode(&admin)
	if err != nil {
		return nil, err
	}
	return admin, nil
}

func (a *adminDatabase) InsertOne(ctx context.Context, admin models.AdminUser, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return a.db.Collection(adminCollectionName).InsertOne(ctx, admin, opts...)
}

func (a *adminDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	return a.db.Collection(adminCollectionName).UpdateOne(ctx, filter, update, opts...)
}

func (a *adminDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return a.db.Collection(adminCollectionName).Find(ctx, filter, opts...)
}

func (a *adminDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return a.db.Collection(adminCollectionName).CountDocuments(ctx, filter, opts...)
}

func (a *adminDatabase) DeleteOne(ctx context.Context, filter interface{}, opts ...*options.DeleteOptions) error {
	return a.db.Collection(adminCollectionName).DeleteOne(ctx, filter, opts...)
}

// EnsureHeadAdmin bootstraps a head admin from env vars if not already present
// Env vars: ADMIN_HEAD_EMAIL, ADMIN_HEAD_PASSWORD
func EnsureHeadAdmin(db DatabaseHelper) error {
	headEmail := strings.TrimSpace(strings.ToLower(os.Getenv("ADMIN_HEAD_EMAIL")))
	if headEmail == "" {
		return nil
	}
	// Use timeout context to prevent hanging during cluster issues
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Check if exists
	err := db.Collection(adminCollectionName).FindOne(ctx, bson.M{"email": headEmail}).Decode(&struct{}{})
	if err == nil {
		return nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return err
	}
	headPassword := os.Getenv("ADMIN_HEAD_PASSWORD")
	if headPassword == "" {
		return errors.New("ADMIN_HEAD_PASSWORD must be set to bootstrap head admin")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(headPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	admin := models.AdminUser{
		Email:     headEmail,
		Password:  string(hash),
		Role:      "owner",
		Roles:     []string{"owner", "admin"},
		Active:    true,
		CreatedAt: time.Now(),
		CreatedBy: "system",
	}
	_, err = db.Collection(adminCollectionName).InsertOne(ctx, admin)
	return err
}

// AdminResetDatabase provides access to the admin password resets collection
type AdminResetDatabase interface {
    InsertOne(ctx context.Context, reset models.AdminPasswordReset, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
    FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.AdminPasswordReset, error)
    UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error)
}

// AdminActivityDatabase provides access to the admin activity collection
type AdminActivityDatabase interface {
	InsertOne(ctx context.Context, activity models.AdminActivityStorage, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error)
	Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error)
	CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error)
	Aggregate(ctx context.Context, pipeline interface{}, opts ...*options.AggregateOptions) (*MongoCursor, error)
}

const adminResetCollectionName = "admin_password_resets"
const adminActivityCollectionName = "admin_activity"

type adminResetDatabase struct {
    db DatabaseHelper
}

type adminActivityDatabase struct {
	db DatabaseHelper
}

// NewAdminResetDatabase initializes the admin reset database helper
func NewAdminResetDatabase(db DatabaseHelper) AdminResetDatabase {
    return &adminResetDatabase{db: db}
}

// NewAdminActivityDatabase initializes the admin activity database helper
func NewAdminActivityDatabase(db DatabaseHelper) AdminActivityDatabase {
	return &adminActivityDatabase{db: db}
}

func (r *adminResetDatabase) InsertOne(ctx context.Context, reset models.AdminPasswordReset, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
    return r.db.Collection(adminResetCollectionName).InsertOne(ctx, reset, opts...)
}

func (r *adminResetDatabase) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.AdminPasswordReset, error) {
    out := &models.AdminPasswordReset{}
    err := r.db.Collection(adminResetCollectionName).FindOne(ctx, filter, opts...).Decode(&out)
    if err != nil {
        return nil, err
    }
    return out, nil
}

func (r *adminResetDatabase) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
    return r.db.Collection(adminResetCollectionName).UpdateOne(ctx, filter, update, opts...)
}

func (a *adminActivityDatabase) InsertOne(ctx context.Context, activity models.AdminActivityStorage, opts ...*options.InsertOneOptions) (InsertOneResultHelper, error) {
	return a.db.Collection(adminActivityCollectionName).InsertOne(ctx, activity, opts...)
}

func (a *adminActivityDatabase) Find(ctx context.Context, filter interface{}, opts ...*options.FindOptions) (*MongoCursor, error) {
	return a.db.Collection(adminActivityCollectionName).Find(ctx, filter, opts...)
}

func (a *adminActivityDatabase) CountDocuments(ctx context.Context, filter interface{}, opts ...*options.CountOptions) (int64, error) {
	return a.db.Collection(adminActivityCollectionName).CountDocuments(ctx, filter, opts...)
}

func (a *adminActivityDatabase) Aggregate(ctx context.Context, pipeline interface{}, opts ...*options.AggregateOptions) (*MongoCursor, error) {
	return a.db.Collection(adminActivityCollectionName).Aggregate(ctx, pipeline, opts...)
}


