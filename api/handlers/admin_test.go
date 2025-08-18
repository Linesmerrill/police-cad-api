package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

// Minimal fake implementing databases.AdminDatabase
type fakeAdminDB struct {
    findOne func(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.AdminUser, error)
}

func (f fakeAdminDB) FindOne(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.AdminUser, error) {
    return f.findOne(ctx, filter, opts...)
}

func (f fakeAdminDB) InsertOne(ctx context.Context, admin models.AdminUser, opts ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
    return nil, nil
}

func (f fakeAdminDB) UpdateOne(ctx context.Context, filter interface{}, update interface{}, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
    return nil, nil
}

func TestAdminLogin_Success(t *testing.T) {
    password := "strong-pass"
    hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    adminUser := &models.AdminUser{
        ID:           primitive.NewObjectID(),
        Email:        "you@example.com",
        PasswordHash: string(hash),
        Active:       true,
        Roles:        []string{"owner", "admin"},
        CreatedAt:    time.Now(),
        UpdatedAt:    time.Now(),
    }

    h := handlers.Admin{ADB: fakeAdminDB{findOne: func(ctx context.Context, filter interface{}, opts ...*options.FindOneOptions) (*models.AdminUser, error) {
        return adminUser, nil
    }}}

    old := os.Getenv("JWT_SECRET")
    os.Setenv("JWT_SECRET", "test-secret")
    t.Cleanup(func() { os.Setenv("JWT_SECRET", old) })

    body, _ := json.Marshal(map[string]string{"email": adminUser.Email, "password": password})
    req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    rr := httptest.NewRecorder()

    http.HandlerFunc(h.AdminLoginHandler).ServeHTTP(rr, req)

    assert.Equal(t, http.StatusOK, rr.Code)
    var resp struct {
        Token string `json:"token"`
        Admin struct {
            ID    string   `json:"id"`
            Email string   `json:"email"`
            Roles []string `json:"roles"`
        } `json:"admin"`
    }
    _ = json.Unmarshal(rr.Body.Bytes(), &resp)
    assert.NotEmpty(t, resp.Token)
    assert.Equal(t, adminUser.Email, resp.Admin.Email)
}


