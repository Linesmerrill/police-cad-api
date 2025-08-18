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
	ctx := context.Background()
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
		Email:        headEmail,
		PasswordHash: string(hash),
		Active:       true,
		Roles:        []string{"owner", "admin"},
		Permissions:  map[string]bool{"*": true},
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
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

const adminResetCollectionName = "admin_password_resets"

type adminResetDatabase struct {
    db DatabaseHelper
}

// NewAdminResetDatabase initializes the admin reset database helper
func NewAdminResetDatabase(db DatabaseHelper) AdminResetDatabase {
    return &adminResetDatabase{db: db}
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


